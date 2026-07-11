// Source download with checksum + size verification. Lives in the installer
// package so the install loop and catalog manifest loop don't share an HTTP
// layer — they have different timeout profiles.
package installer

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/httpx"
	"github.com/cristatus/bunny/internal/manifest"
)

// defaultClient is the timeout-bound HTTP client used for binary downloads.
// Long enough to cover IDEA-sized tarballs on slow links; stalls past that
// should fail loudly.
var defaultClient = newDownloadClient()

func newDownloadClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 45 * time.Second
	transport.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: transport, Timeout: 30 * time.Minute}
}

// Source is the manifest artifact type. Keeping a single representation avoids
// drift when source metadata grows new integrity or transport fields.
type Source = manifest.Source

// Downloader fetches sources to a per-package cache. Tests inject behavior via
// Client (a custom http.RoundTripper), exercising the real fetch path.
type Downloader struct {
	Client  *http.Client
	Retries int
}

// New returns a Downloader using bunny's shared (timeout-bound) HTTP client.
func NewDownloader() *Downloader { return &Downloader{Client: defaultClient, Retries: 2} }

// Fetch downloads a single source into cacheDir, returns the absolute path.
// If the file already exists with matching integrity metadata, no network is hit.
func (d *Downloader) Fetch(cacheDir string, src Source) (string, error) {
	return d.FetchContext(context.Background(), cacheDir, src)
}

// FetchContext is Fetch with cancellation propagated to HTTP requests.
func (d *Downloader) FetchContext(ctx context.Context, cacheDir string, src Source) (string, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	name, err := safeFileName(src)
	if err != nil {
		return "", err
	}
	target := filepath.Join(cacheDir, name)

	// If we have it cached and the checksum/size matches, skip the download.
	if hasGoodIntegrity(target, src) {
		return target, nil
	}

	if err := d.fetchURL(ctx, src, target); err != nil {
		return "", err
	}
	if err := verifyIntegrity(target, src); err != nil {
		os.Remove(target)
		return "", err
	}
	return target, nil
}

// FetchAll downloads every source into cacheDir. Returns the absolute paths
// in source order.
func (d *Downloader) FetchAll(cacheDir string, sources []Source) ([]string, error) {
	return d.FetchAllContext(context.Background(), cacheDir, sources)
}

// FetchAllContext downloads sources in order and stops promptly on cancellation.
func (d *Downloader) FetchAllContext(ctx context.Context, cacheDir string, sources []Source) ([]string, error) {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		path, err := d.FetchContext(ctx, cacheDir, s)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", s.URL, err)
		}
		out = append(out, path)
	}
	return out, nil
}

// fetchURL streams URL → target. Supports file:// for tests.
func (d *Downloader) fetchURL(ctx context.Context, src Source, target string) error {
	rawURL := src.URL
	if strings.HasPrefix(rawURL, "file://") {
		if err := ctx.Err(); err != nil {
			return err
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		return copyFile(u.Path, target)
	}

	log.Info("Downloading", "url", rawURL)
	var lastErr error
	attempts := d.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := httpx.Backoff(attempt, 200*time.Millisecond)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		retry, err := d.fetchHTTPOnce(ctx, src, target)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry || ctx.Err() != nil {
			break
		}
	}
	return fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (d *Downloader) fetchHTTPOnce(ctx context.Context, src Source, target string) (bool, error) {
	part := target + ".part"
	offset := int64(0)
	if info, err := os.Lstat(part); err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("partial download %s is not a regular file", part)
		}
		offset = info.Size()
	} else if !os.IsNotExist(err) {
		return false, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return false, err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	client := d.Client
	if client == nil {
		client = defaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && src.Size > 0 && offset == src.Size {
		return false, os.Rename(part, target)
	}
	if resp.StatusCode != http.StatusOK && !(offset > 0 && resp.StatusCode == http.StatusPartialContent) {
		return httpx.ShouldRetryStatus(resp.StatusCode), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusOK {
		offset = 0 // server ignored Range; restart safely
	}
	if src.Size > 0 && resp.ContentLength > 0 && offset+resp.ContentLength > src.Size {
		_ = os.Remove(part)
		return false, fmt.Errorf("response exceeds expected size %d", src.Size)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(part, flags, 0644)
	if err != nil {
		return false, err
	}
	reader := io.Reader(resp.Body)
	if src.Size > 0 {
		reader = io.LimitReader(resp.Body, src.Size-offset+1)
	}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return true, copyErr
	}
	if closeErr != nil {
		return true, closeErr
	}
	total := offset + written
	if src.Size > 0 && total > src.Size {
		_ = os.Remove(part)
		return false, fmt.Errorf("response exceeds expected size %d", src.Size)
	}
	if src.Size > 0 && total < src.Size {
		return true, fmt.Errorf("short response: expected %d bytes, received %d", src.Size, total)
	}
	if err := os.Rename(part, target); err != nil {
		return false, err
	}
	return false, nil
}

func safeFileName(s Source) (string, error) {
	name := fileName(s)
	if err := validateFileName(name); err != nil {
		return "", err
	}
	return name, nil
}

func fileName(s Source) string {
	if s.File != "" {
		return s.File
	}
	if s.Name != "" {
		return s.Name
	}
	// Fall back to the basename of the URL.
	if u, err := url.Parse(s.URL); err == nil && u.Path != "" {
		return filepath.Base(u.Path)
	}
	return "source"
}

func validateFileName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid source filename %q", name)
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) || filepath.Clean(name) != name {
		return fmt.Errorf("source filename %q must not contain path separators", name)
	}
	return nil
}

// hasGoodIntegrity reports whether the file exists at path and matches at
// least one declared integrity guard (hash or size).
func hasGoodIntegrity(path string, src Source) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if src.SHA256 == "" && src.SHA512 == "" && src.Size <= 0 {
		// no expected integrity metadata: can't validate; treat as miss to be safe
		return false
	}
	return verifyIntegrity(path, src) == nil
}

func verifyIntegrity(path string, src Source) error {
	if src.Size > 0 {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if got := info.Size(); got != src.Size {
			return fmt.Errorf("size mismatch: want %d, got %d", src.Size, got)
		}
	}
	if src.SHA256 != "" {
		got, err := hashFile(path, sha256.New())
		if err != nil {
			return err
		}
		if !equalsHex(got, src.SHA256) {
			return fmt.Errorf("sha256 mismatch: want %s, got %s", src.SHA256, got)
		}
	}
	if src.SHA512 != "" {
		got, err := hashFile(path, sha512.New())
		if err != nil {
			return err
		}
		if !equalsHex(got, src.SHA512) {
			return fmt.Errorf("sha512 mismatch: want %s, got %s", src.SHA512, got)
		}
	}
	return nil
}

func hashFile(path string, h hash.Hash) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func equalsHex(a, b string) bool {
	return strings.EqualFold(a, b)
}

func copyFile(srcPath, dstPath string) error {
	return fsutil.CopyFile(srcPath, dstPath, 0644)
}
