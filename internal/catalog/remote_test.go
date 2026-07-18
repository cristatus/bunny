package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeHTTP returns an HTTPGet that serves a fixed map of url → body.
func fakeHTTP(bodies map[string]string) HTTPGet {
	return func(url string) (*http.Response, error) {
		body, ok := bodies[url]
		if !ok {
			return &http.Response{
				StatusCode: 404,
				Body:       io.NopCloser(strings.NewReader("not found")),
			}, nil
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		}, nil
	}
}

const testIndex = `{
  "version": 1,
  "updated": "2026-01-01T00:00:00Z",
  "packages": {
    "rg": {"name": "ripgrep", "version": "14.1.0", "category": "cli", "description": "fast grep", "provides": "search", "requires": ["jdk>=17"]},
    "code": {"name": "VS Code", "version": "1.98.2", "category": "editor", "description": "editor"}
  }
}`

const remoteManifest = `id: rg
name: ripgrep
version: "14.1.0"
sources:
  - url: https://example.com/rg.tar.gz
    sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
bin:
  - name: rg
    path: "{app}/rg"
`

func TestRemoteListAndLoad(t *testing.T) {
	cache := t.TempDir()
	r := NewRemote("https://x", cache)
	r.WithHTTPGet(fakeHTTP(map[string]string{
		"https://x/index.json":           testIndex,
		"https://x/cli/rg/manifest.yaml": remoteManifest,
	}))

	pkgs, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Errorf("expected 2 pkgs, got %d", len(pkgs))
	}
	for _, pkg := range pkgs {
		if pkg.ID == "rg" && (pkg.Provides != "search" || len(pkg.Requires) != 1 || pkg.Requires[0] != "jdk>=17") {
			t.Errorf("rg capability metadata = provides %q, requires %v", pkg.Provides, pkg.Requires)
		}
	}

	m, err := r.Load("rg")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "14.1.0" {
		t.Errorf("version=%q", m.Version)
	}
}

func TestRemoteLoadFileSafeRelPath(t *testing.T) {
	r := NewRemote("https://x", t.TempDir())
	r.WithHTTPGet(fakeHTTP(map[string]string{
		"https://x/index.json":      testIndex,
		"https://x/cli/rg/build.sh": "#!/bin/sh\n",
	}))

	data, err := r.LoadFile("rg", "build.sh")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "#!/bin/sh\n" {
		t.Errorf("got %q", data)
	}

	if _, err := r.LoadFile("rg", "../../passwd"); err == nil {
		t.Error("traversal should be rejected")
	}
	if _, err := r.LoadFile("../rg", "build.sh"); err == nil {
		t.Error("invalid package id should be rejected")
	}
}

func TestRemoteFetchFailuresAreErrNotFound(t *testing.T) {
	t.Run("HTTP 404", func(t *testing.T) {
		r := NewRemote("https://x", t.TempDir())
		r.WithHTTPGet(fakeHTTP(nil))
		_, err := r.List()
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("404 should map to ErrNotFound, got %v", err)
		}
	})
	t.Run("HTTP 500", func(t *testing.T) {
		r := NewRemote("https://x", t.TempDir())
		r.WithHTTPGet(func(string) (*http.Response, error) {
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		_, err := r.List()
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("500 should map to ErrNotFound, got %v", err)
		}
	})
	t.Run("network error", func(t *testing.T) {
		r := NewRemote("https://x", t.TempDir())
		r.WithHTTPGet(func(string) (*http.Response, error) {
			return nil, fmt.Errorf("dial tcp: lookup x: no such host")
		})
		_, err := r.List()
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("network error should map to ErrNotFound, got %v", err)
		}
	})
}

func TestRemoteCachesIndex(t *testing.T) {
	cache := t.TempDir()
	calls := 0
	r := NewRemote("https://x", cache)
	r.WithHTTPGet(func(url string) (*http.Response, error) {
		calls++
		if url == "https://x/index.json" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(testIndex))}, nil
		}
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
	})

	if _, err := r.List(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.List(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 HTTP call (in-memory cache), got %d", calls)
	}

	calls = 0
	r2 := NewRemote("https://x", cache)
	r2.WithHTTPGet(func(url string) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("should not be called")
	})
	if _, err := r2.List(); err != nil {
		t.Fatalf("expected on-disk cache to satisfy: %v", err)
	}
	if calls != 0 {
		t.Errorf("expected 0 HTTP calls, got %d", calls)
	}
}

func TestRemoteRefreshesExpiredIndexAndFallsBackWhenOffline(t *testing.T) {
	cache := t.TempDir()
	indexPath := filepath.Join(cache, "index.json")
	if err := os.WriteFile(indexPath, []byte(testIndex), 0644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-indexTTL - time.Hour)
	if err := os.Chtimes(indexPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	freshIndex := strings.Replace(testIndex, `"version": "14.1.0"`, `"version": "15.0.0"`, 1)
	r := NewRemote("https://x", cache).WithHTTPGet(fakeHTTP(map[string]string{
		"https://x/index.json": freshIndex,
	}))
	pkgs, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	var refreshed bool
	for _, pkg := range pkgs {
		if pkg.ID == "rg" && pkg.Version == "15.0.0" {
			refreshed = true
			break
		}
	}
	if !refreshed {
		t.Fatalf("stale index was not refreshed: %+v", pkgs)
	}

	if err := os.Chtimes(indexPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	offline := NewRemote("https://x", cache).WithHTTPGet(func(string) (*http.Response, error) {
		return nil, errors.New("offline")
	})
	if _, err := offline.List(); err != nil {
		t.Fatalf("stale cache should remain usable offline: %v", err)
	}
}

// TestRemoteStaleIndexDoesNotBlockOnSlowFetch pins #10: a stale index must be
// served immediately when the revalidation fetch is slow, rather than stalling
// the command for the full HTTP timeout. The in-flight fetch still refreshes
// the on-disk cache in the background.
func TestRemoteStaleIndexDoesNotBlockOnSlowFetch(t *testing.T) {
	cache := t.TempDir()
	indexPath := filepath.Join(cache, "index.json")
	if err := os.WriteFile(indexPath, []byte(testIndex), 0644); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-indexTTL - time.Hour)
	if err := os.Chtimes(indexPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	freshIndex := strings.Replace(testIndex, `"version": "14.1.0"`, `"version": "15.0.0"`, 1)
	release := make(chan struct{})
	r := NewRemote("https://x", cache).WithHTTPGet(func(url string) (*http.Response, error) {
		<-release // simulate a slow/flaky link
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(freshIndex)),
		}, nil
	})
	r.revalidateTimeout = 20 * time.Millisecond

	start := time.Now()
	pkgs, err := r.List()
	elapsed := time.Since(start)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if elapsed > 2*time.Second {
		close(release)
		t.Fatalf("List blocked on slow fetch for %v; should serve stale immediately", elapsed)
	}
	// Served the stale cache (rg is still 14.1.0, not the fresh 15.0.0).
	for _, pkg := range pkgs {
		if pkg.ID == "rg" && pkg.Version != "14.1.0" {
			t.Fatalf("expected stale rg 14.1.0, got %s", pkg.Version)
		}
	}

	// Let the background revalidation finish; it must refresh the on-disk cache.
	close(release)
	r.Wait()
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "15.0.0") {
		t.Fatalf("background revalidation did not refresh the cache: %s", data)
	}
}

func TestListCached(t *testing.T) {
	dir := t.TempDir()
	idx := `{"version":1,"packages":{"jdk-21":{"name":"JDK 21","version":"21","category":"sdk","description":"d"}}}`
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(idx), 0644); err != nil {
		t.Fatal(err)
	}
	// A get func that fails — proves ListCached never touches the network.
	r := NewRemote("https://example.invalid", dir).WithHTTPGet(func(string) (*http.Response, error) {
		return nil, fmt.Errorf("network must not be used")
	})
	pkgs, err := r.ListCached()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].ID != "jdk-21" {
		t.Fatalf("got %+v", pkgs)
	}

	// No cache file → error (caller treats as empty), still no network.
	r2 := NewRemote("https://example.invalid", t.TempDir()).WithHTTPGet(func(string) (*http.Response, error) {
		return nil, fmt.Errorf("network must not be used")
	})
	if _, err := r2.ListCached(); err == nil {
		t.Error("expected error when no cache present")
	}
}
