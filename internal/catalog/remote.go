package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/httpx"
	"github.com/cristatus/bunny/internal/manifest"
)

// DefaultRemoteURL is the upstream catalog the cli ships with.
const DefaultRemoteURL = "https://raw.githubusercontent.com/cristatus/bunny-catalog/main"

// httpClient is the timeout-bound client every remote loader uses. The
// 5-minute timeout covers both metadata fetches and manifest pulls; binary
// downloads happen in the installer with its own client.
var httpClient = &http.Client{Timeout: 5 * time.Minute}

const (
	indexTTL = 6 * time.Hour
	// defaultRevalidateTimeout bounds how long the hot path waits for a
	// stale-index refresh before serving the cached copy. A slow or flaky link
	// no longer stalls interactive commands for the full httpClient timeout.
	defaultRevalidateTimeout = 3 * time.Second
	maxCatalogBody           = 4 << 20
)

// Index is the cached top-level catalog index (index.json).
type Index struct {
	Version  int                   `json:"version"`
	Updated  time.Time             `json:"updated"`
	Packages map[string]IndexEntry `json:"packages"`
}

// IndexEntry is the per-package summary stored in the index.
type IndexEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

// HTTPGet matches the small subset of net/http we need; injectable for tests.
type HTTPGet func(url string) (*http.Response, error)

// Remote serves manifests from an HTTP catalog with a local index cache.
type Remote struct {
	baseURL           string
	cacheDir          string
	get               HTTPGet
	retries           int
	index             *Index
	revalidateTimeout time.Duration
	wg                sync.WaitGroup
}

// NewRemote constructs a Remote with the default HTTP client.
func NewRemote(baseURL, cacheDir string) *Remote {
	if baseURL == "" {
		baseURL = DefaultRemoteURL
	}
	return &Remote{
		baseURL:           baseURL,
		cacheDir:          cacheDir,
		get:               httpClient.Get,
		retries:           2,
		revalidateTimeout: defaultRevalidateTimeout,
	}
}

// WithHTTPGet overrides the HTTP client (used by tests).
func (r *Remote) WithHTTPGet(g HTTPGet) *Remote {
	r.get = g
	r.retries = 0
	return r
}

// Wait blocks until any in-flight background index revalidation finishes.
// The hot path never needs this; it lets tests (and a shutdown hook) await a
// stale-while-revalidate refresh that outlived the command.
func (r *Remote) Wait() {
	r.wg.Wait()
}

// Refresh fetches the index from the remote and overwrites the cache.
func (r *Remote) Refresh() error {
	idx, err := r.fetchIndex()
	if err != nil {
		return err
	}
	if err := r.cacheIndex(idx); err != nil {
		return err
	}
	r.index = idx
	return nil
}

// List returns packages from the index.
func (r *Remote) List() ([]PackageInfo, error) {
	idx, err := r.loadIndex()
	if err != nil {
		return nil, err
	}
	out := make([]PackageInfo, 0, len(idx.Packages))
	for id, e := range idx.Packages {
		out = append(out, PackageInfo{
			ID:          id,
			Category:    e.Category,
			Name:        e.Name,
			Description: e.Description,
			Version:     e.Version,
		})
	}
	return out, nil
}

// ListCached returns index packages from the on-disk cache only, never
// fetching. Used by shell completion, which must not touch the network.
func (r *Remote) ListCached() ([]PackageInfo, error) {
	idx := r.index
	if idx == nil {
		var err error
		idx, err = r.loadCachedIndex()
		if err != nil {
			return nil, err
		}
	}
	out := make([]PackageInfo, 0, len(idx.Packages))
	for id, e := range idx.Packages {
		out = append(out, PackageInfo{
			ID:          id,
			Category:    e.Category,
			Name:        e.Name,
			Description: e.Description,
			Version:     e.Version,
		})
	}
	return out, nil
}

// Load fetches and parses a manifest.
func (r *Remote) Load(id string) (*manifest.Manifest, error) {
	url, err := r.manifestURL(id)
	if err != nil {
		return nil, err
	}
	body, err := r.fetch(url)
	if err != nil {
		return nil, err
	}
	return manifest.ParseBytes(body)
}

// LoadFile fetches a sibling file.
func (r *Remote) LoadFile(id, relPath string) ([]byte, error) {
	if err := manifest.ValidateID(id); err != nil {
		return nil, fmt.Errorf("invalid package id %q: %w", id, err)
	}
	if err := manifest.SafeRelPath(relPath); err != nil {
		return nil, err
	}
	idx, err := r.loadIndex()
	if err != nil {
		return nil, err
	}
	entry, ok := idx.Packages[id]
	if !ok {
		return nil, fmt.Errorf("%w: package %q not in remote index", ErrNotFound, id)
	}
	return r.fetch(fmt.Sprintf("%s/%s/%s/%s", r.baseURL, entry.Category, id, relPath))
}

// --- internal ---

func (r *Remote) manifestURL(id string) (string, error) {
	if err := manifest.ValidateID(id); err != nil {
		return "", fmt.Errorf("invalid package id %q: %w", id, err)
	}
	idx, err := r.loadIndex()
	if err != nil {
		return "", err
	}
	entry, ok := idx.Packages[id]
	if !ok {
		return "", fmt.Errorf("%w: package %q not in remote index", ErrNotFound, id)
	}
	return fmt.Sprintf("%s/%s/%s/manifest.yaml", r.baseURL, entry.Category, id), nil
}

func (r *Remote) loadIndex() (*Index, error) {
	if r.index != nil {
		return r.index, nil
	}
	if idx, err := r.loadCachedIndex(); err == nil {
		if r.cacheFresh() {
			r.index = idx
			return idx, nil
		}
		// Stale-while-revalidate: kick off a refresh but never let it stall an
		// interactive command. If the fetch beats revalidateTimeout we serve the
		// fresh index; otherwise we serve the stale cache immediately and let the
		// in-flight fetch keep running to refresh the on-disk cache for next time.
		// Any fetch error also falls back to the stale cache.
		type fetchResult struct {
			idx *Index
			err error
		}
		done := make(chan fetchResult, 1)
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			fresh, err := r.fetchIndex()
			if err == nil {
				_ = r.cacheIndex(fresh)
			}
			done <- fetchResult{fresh, err}
		}()
		select {
		case res := <-done:
			if res.err == nil {
				r.index = res.idx
				return res.idx, nil
			}
		case <-time.After(r.revalidateTimeout):
		}
		r.index = idx
		return idx, nil
	}
	idx, err := r.fetchIndex()
	if err != nil {
		return nil, err
	}
	_ = r.cacheIndex(idx)
	r.index = idx
	return idx, nil
}

func (r *Remote) loadCachedIndex() (*Index, error) {
	data, err := os.ReadFile(filepath.Join(r.cacheDir, "index.json"))
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	if err := validateIndex(&idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

func (r *Remote) fetchIndex() (*Index, error) {
	body, err := r.fetch(r.baseURL + "/index.json")
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("parse index: %w", err)
	}
	if err := validateIndex(&idx); err != nil {
		return nil, fmt.Errorf("validate index: %w", err)
	}
	return &idx, nil
}

func (r *Remote) cacheIndex(idx *Index) error {
	if err := os.MkdirAll(r.cacheDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFile(filepath.Join(r.cacheDir, "index.json"), data, 0644)
}

func (r *Remote) cacheFresh() bool {
	info, err := os.Stat(filepath.Join(r.cacheDir, "index.json"))
	return err == nil && time.Since(info.ModTime()) <= indexTTL
}

func validateIndex(idx *Index) error {
	if idx.Version <= 0 {
		return fmt.Errorf("invalid version %d", idx.Version)
	}
	if idx.Packages == nil {
		return fmt.Errorf("packages is required")
	}
	for id, entry := range idx.Packages {
		if err := manifest.ValidateID(id); err != nil {
			return fmt.Errorf("package %q: %w", id, err)
		}
		if entry.Category == "" || filepath.Base(entry.Category) != entry.Category || strings.ContainsAny(entry.Category, `/\\`) {
			return fmt.Errorf("package %q has unsafe category %q", id, entry.Category)
		}
	}
	return nil
}

// fetch issues an HTTP GET. Network errors and non-200 responses are wrapped
// as ErrNotFound so Composite falls through to the next loader; only success
// (or a parse error in the caller) breaks out of the chain.
func (r *Remote) fetch(url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= r.retries; attempt++ {
		if attempt > 0 {
			time.Sleep(httpx.Backoff(attempt, 100*time.Millisecond))
		}
		resp, err := r.get(url)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			retry := httpx.ShouldRetryStatus(resp.StatusCode)
			resp.Body.Close()
			if retry {
				continue
			}
			break
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBody+1))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if len(body) > maxCatalogBody {
			return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", url, maxCatalogBody)
		}
		return body, nil
	}
	return nil, fmt.Errorf("fetch %s: %v (%w)", url, lastErr, ErrNotFound)
}
