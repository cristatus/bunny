// Package checker discovers new versions for installed packages.
//
// Backends (GitHub releases, JSON APIs, HTML scraping, Debian repo metadata)
// are pluggable behind the Backend interface and selected per-manifest
// via the `update.type:` field.
package checker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/httpx"
	"github.com/cristatus/bunny/internal/manifest"
)

// httpClient is the timeout-bound HTTP client every checker backend shares.
// Five minutes is plenty for metadata pulls and small artifact lookups; the
// installer maintains its own client for large binary downloads.
var httpClient = newMetadataClient()

const maxMetadataBody = 16 << 20

func newMetadataClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.IdleConnTimeout = 90 * time.Second
	return &http.Client{Transport: transport, Timeout: 5 * time.Minute}
}

func doRequest(ctx context.Context, method, url string) (*http.Response, error) {
	return doRequestWithClient(ctx, httpClient, method, url)
}

func doRequestWithClient(ctx context.Context, client *http.Client, method, url string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(httpx.Backoff(attempt, 100*time.Millisecond)):
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if httpx.ShouldRetryStatus(resp.StatusCode) {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			resp.Body.Close()
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func readBodyLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxMetadataBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMetadataBody {
		return nil, fmt.Errorf("response exceeds %d bytes", maxMetadataBody)
	}
	return data, nil
}

// Result is the outcome of a single update check.
type Result struct {
	ID             string
	CurrentVersion string
	LatestVersion  string
	DownloadURL    string
	Hash           string
	HashAlgorithm  string // "sha256" or "sha512"
	Size           int64
	HasUpdate      bool
}

// Backend implements update discovery for one update.type.
type Backend interface {
	Type() string
	Check(ctx context.Context, cfg *manifest.UpdateConfig, currentVersion, sourceURL string) (*Result, error)
}

var registry = map[string]Backend{}

// Register adds a backend. Called from each backend file's init().
func Register(b Backend) { registry[b.Type()] = b }

// Available returns the registered backend types.
func Available() []string {
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	return out
}

// Check dispatches to the matching backend. cfg may be nil — in that case the
// package has no update mechanism and the result reports HasUpdate=false.
func Check(ctx context.Context, id, currentVersion, sourceURL string, cfg *manifest.UpdateConfig) (*Result, error) {
	if cfg == nil {
		return &Result{ID: id, CurrentVersion: currentVersion, HasUpdate: false}, nil
	}
	b, ok := registry[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown update type: %s", cfg.Type)
	}
	log.Debug("Checking for updates", "id", id, "type", cfg.Type)

	r, err := b.Check(ctx, cfg, currentVersion, sourceURL)
	if err != nil {
		return nil, err
	}
	r.ID = id
	r.CurrentVersion = currentVersion
	return r, nil
}
