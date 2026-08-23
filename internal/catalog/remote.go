package catalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

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
	// stale-index refresh before serving the cached copy, so a slow or flaky
	// link cannot stall an interactive command for the full httpClient timeout.
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
	Name    string `json:"name"`
	Version string `json:"version"`
	// Path is the package's directory in the catalog, relative to its root.
	// Recorded rather than derived so the catalog can lay itself out however
	// it likes without bunny modelling the scheme.
	Path string   `json:"path"`
	Tags []string `json:"tags,omitempty"`
	// Kind is the manifest's resolved kind, recorded so a remote listing can
	// report where a package installs without downloading every manifest.
	Kind        string   `json:"kind,omitempty"`
	Description string   `json:"description"`
	Provides    string   `json:"provides,omitempty"`
	Requires    []string `json:"requires,omitempty"`
}

// info converts an index entry into the loader-facing summary.
func (e IndexEntry) info(id string) PackageInfo {
	return PackageInfo{
		ID:          id,
		Tags:        append([]string(nil), e.Tags...),
		Kind:        e.Kind,
		Name:        e.Name,
		Description: e.Description,
		Version:     e.Version,
		Provides:    e.Provides,
		Requires:    append([]string(nil), e.Requires...),
	}
}

// HTTPGet matches the small subset of net/http we need; injectable for tests.
type HTTPGet func(url string) (*http.Response, error)

// Remote serves manifests from an HTTP catalog with a local index cache.
//
// Safe for concurrent use: several catalogs refresh at once, and a refresh that
// outlives its deadline keeps running while the command reads on.
type Remote struct {
	baseURL           string
	indexPath         string
	get               HTTPGet
	retries           int
	revalidateTimeout time.Duration
	wg                sync.WaitGroup

	mu    sync.RWMutex
	index *Index
}

// cached returns the in-memory index, nil until something loads one.
func (r *Remote) cached() *Index {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.index
}

func (r *Remote) setCached(idx *Index) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.index = idx
}

// NewRemote constructs a Remote with the default HTTP client. cacheDir is this
// catalog's own directory, so several remotes never overwrite one another's
// index.
func NewRemote(baseURL, cacheDir string) *Remote {
	return &Remote{
		baseURL:           baseURL,
		indexPath:         filepath.Join(cacheDir, "index.json"),
		get:               httpClient.Get,
		retries:           2,
		revalidateTimeout: defaultRevalidateTimeout,
	}
}

// IndexPath is where this remote caches the catalog index.
func (r *Remote) IndexPath() string { return r.indexPath }

// URL is the catalog root actually in use, which is the built-in default when
// config names none. Reporting the configured value instead would show an
// empty remote for the common case of not having configured one.
func (r *Remote) URL() string { return r.baseURL }

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
	r.setCached(idx)
	return nil
}

// List returns packages from the index.
func (r *Remote) List() ([]PackageInfo, error) {
	idx, err := r.loadIndex()
	if err != nil {
		return nil, r.unavailable(err)
	}
	out := make([]PackageInfo, 0, len(idx.Packages))
	for id, e := range idx.Packages {
		out = append(out, e.info(id))
	}
	return out, nil
}

// ListCached returns index packages from the on-disk cache only, never
// fetching. Used by shell completion, which must not touch the network.
func (r *Remote) ListCached() ([]PackageInfo, error) {
	idx := r.cached()
	if idx == nil {
		var err error
		idx, err = r.loadCachedIndex()
		if err != nil {
			return nil, err
		}
	}
	out := make([]PackageInfo, 0, len(idx.Packages))
	for id, e := range idx.Packages {
		out = append(out, e.info(id))
	}
	return out, nil
}

// Lookup answers from the index, which every other read already needs.
func (r *Remote) Lookup(id string) (PackageInfo, error) {
	if err := manifest.ValidateID(id); err != nil {
		return PackageInfo{}, fmt.Errorf("invalid package id %q: %w", id, err)
	}
	idx, err := r.loadIndex()
	if err != nil {
		return PackageInfo{}, r.unavailable(err)
	}
	entry, ok := idx.Packages[id]
	if !ok {
		return PackageInfo{}, fmt.Errorf("%w: package %q not in %s", ErrNotFound, id, r.baseURL)
	}
	return entry.info(id), nil
}

// Load fetches and parses a manifest.
func (r *Remote) Load(id string) (*manifest.Manifest, error) {
	url, err := r.manifestURL(id)
	if err != nil {
		return nil, err
	}
	log.Debug("Fetching manifest", "package", id, "url", url)
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
		return nil, r.unavailable(err)
	}
	entry, ok := idx.Packages[id]
	if !ok {
		return nil, fmt.Errorf("%w: package %q not in remote index", ErrNotFound, id)
	}
	return r.fetch(fmt.Sprintf("%s/%s/%s", r.baseURL, entry.Path, relPath))
}

// --- internal ---

// unavailable marks an index failure as "this catalog cannot answer". The cause
// is folded in as text rather than wrapped: a 404 on the index means the catalog
// root is wrong, and letting its ErrNotFound through would have the error claim
// the package does not exist.
func (r *Remote) unavailable(err error) error {
	return fmt.Errorf("%w: %s: %v", ErrUnavailable, r.baseURL, err)
}

func (r *Remote) manifestURL(id string) (string, error) {
	if err := manifest.ValidateID(id); err != nil {
		return "", fmt.Errorf("invalid package id %q: %w", id, err)
	}
	idx, err := r.loadIndex()
	if err != nil {
		return "", r.unavailable(err)
	}
	entry, ok := idx.Packages[id]
	if !ok {
		return "", fmt.Errorf("%w: package %q not in remote index", ErrNotFound, id)
	}
	return fmt.Sprintf("%s/%s/manifest.yaml", r.baseURL, entry.Path), nil
}

func (r *Remote) loadIndex() (*Index, error) {
	if idx := r.cached(); idx != nil {
		return idx, nil
	}
	if idx, err := r.loadCachedIndex(); err == nil {
		if r.cacheFresh() {
			log.Debug("Index cache hit", "packages", len(idx.Packages), "updated", idx.Updated)
			r.setCached(idx)
			return idx, nil
		}
		log.Debug("Index cache stale, revalidating", "updated", idx.Updated,
			"timeout", r.revalidateTimeout)
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
				r.setCached(res.idx)
				return res.idx, nil
			}
			log.Debug("Index refresh failed, serving cache", "error", res.err)
		case <-time.After(r.revalidateTimeout):
			log.Debug("Index refresh too slow, serving cache", "timeout", r.revalidateTimeout)
		}
		r.setCached(idx)
		return idx, nil
	}
	idx, err := r.fetchIndex()
	if err != nil {
		return nil, err
	}
	_ = r.cacheIndex(idx)
	r.setCached(idx)
	return idx, nil
}

func (r *Remote) loadCachedIndex() (*Index, error) {
	data, err := os.ReadFile(r.indexPath)
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
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFile(r.indexPath, data, 0644)
}

func (r *Remote) cacheFresh() bool {
	info, err := os.Stat(r.indexPath)
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
		if err := safeIndexPath(entry.Path); err != nil {
			return fmt.Errorf("package %q: %w", id, err)
		}
	}
	return nil
}

// fetch retrieves url, retrying what is worth retrying. ErrNotFound is only for
// a status that says the catalog looked and has nothing there; a link that is
// down, slow, or erroring carries ErrUnavailable.
func (r *Remote) fetch(url string) ([]byte, error) {
	var lastErr error
	absent := false
	for attempt := 0; attempt <= r.retries; attempt++ {
		if attempt > 0 {
			time.Sleep(httpx.Backoff(attempt, 100*time.Millisecond))
		}
		resp, err := r.get(url)
		if err != nil {
			lastErr, absent = err, false
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			absent = resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone
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
			lastErr, absent = readErr, false
			continue
		}
		if len(body) > maxCatalogBody {
			return nil, fmt.Errorf("fetch %s: response exceeds %d bytes", url, maxCatalogBody)
		}
		return body, nil
	}
	sentinel := ErrUnavailable
	if absent {
		sentinel = ErrNotFound
	}
	return nil, fmt.Errorf("fetch %s: %v (%w)", url, lastErr, sentinel)
}

// safeIndexPath rejects a package path that is absolute or escapes the catalog
// root. The index is remote input, so its paths are used to build URLs only
// after they are known to stay inside the catalog.
func safeIndexPath(p string) error {
	if p == "" {
		return fmt.Errorf("index entry has no path")
	}
	if path.IsAbs(p) || strings.HasPrefix(p, "/") {
		return fmt.Errorf("unsafe package path %q: must be relative", p)
	}
	if path.Clean(p) != p {
		return fmt.Errorf("unsafe package path %q: must be clean", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "" {
			return fmt.Errorf("unsafe package path %q", p)
		}
	}
	return nil
}
