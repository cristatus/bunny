// Cache and tmp cleanup: removes stale data under $BUNNY_HOME/var.
//
// "Stale" by default means:
//   - download cache files for uninstalled packages (orphan dirs)
//   - older versions of cache files for installed packages (keep current only)
//   - everything under var/tmp (crashed-install leftovers, always safe to drop)
//
// Per-app sandbox writable dirs (var/app/<id>/{config,cache,data}) are NOT
// touched here — they're user data, removed only by `bunny uninstall --purge`.
package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// Report summarizes what one Clean call did.
type Report struct {
	Removed []string // absolute paths that were deleted
	Bytes   int64    // total bytes freed
	Errors  []error  // paths that could not be inspected or removed
}

// Cleaner is the entry point. Construct via New, call Clean.
type Cleaner struct {
	Paths     *paths.Paths
	Catalog   catalog.Loader
	Installed catalog.Loader
	State     *state.State
}

// New constructs a Cleaner.
func NewCleaner(p *paths.Paths, cat catalog.Loader, st *state.State) *Cleaner {
	return &Cleaner{Paths: p, Catalog: cat, Installed: catalog.NewInstalled(cat, p.ManifestFile), State: st}
}

// Clean runs the cleanup pass.
//
//	id == "":  scan everything under var/cache and var/tmp.
//	id != "":  scope to one app (cache + tmp for that id only).
//	all:       drop all download cache, including for installed apps.
func (c *Cleaner) Clean(id string, all bool) (*Report, error) {
	r := &Report{}

	if id != "" {
		if err := manifest.ValidateID(id); err != nil {
			return nil, fmt.Errorf("invalid package id %q: %w", id, err)
		}
		c.cleanOneCache(r, id, all)
		c.cleanOneTmp(r, id)
		return r, errors.Join(r.Errors...)
	}

	c.cleanAllTmp(r)
	c.cleanAllCache(r, all)
	return r, errors.Join(r.Errors...)
}

// --- per-app ---

func (c *Cleaner) cleanOneCache(r *Report, id string, all bool) {
	dir := c.Paths.AppDownloadCache(id)
	if _, err := os.Stat(dir); err != nil {
		if !os.IsNotExist(err) {
			r.Errors = append(r.Errors, fmt.Errorf("inspect cache directory %s: %w", dir, err))
		}
		return
	}
	if all || !c.State.IsInstalled(id) {
		c.removeAll(r, dir)
		return
	}
	c.pruneCacheDir(r, id, dir)
}

func (c *Cleaner) cleanOneTmp(r *Report, id string) {
	dir := c.Paths.AppTmp(id)
	if _, err := os.Stat(dir); err == nil {
		c.removeAll(r, dir)
	} else if !os.IsNotExist(err) {
		r.Errors = append(r.Errors, fmt.Errorf("inspect temporary directory %s: %w", dir, err))
	}
}

// --- all-apps ---

func (c *Cleaner) cleanAllCache(r *Report, all bool) {
	cacheRoot := c.Paths.Cache()
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			r.Errors = append(r.Errors, fmt.Errorf("read cache directory: %w", err))
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue // index.json etc. — left alone unless --all
			// (handled below)
		}
		id := e.Name()
		dir := filepath.Join(cacheRoot, id)
		if all || !c.State.IsInstalled(id) {
			c.removeAll(r, dir)
			continue
		}
		c.pruneCacheDir(r, id, dir)
	}

	if all {
		// Also drop top-level files like index.json so the remote catalog
		// re-fetches on next operation.
		for _, e := range entries {
			if e.IsDir() || isDisposableMarker(e.Name()) {
				continue // keep CACHEDIR.TAG/.nobackup so the dir stays tagged
			}
			c.removeFile(r, filepath.Join(cacheRoot, e.Name()))
		}
	}
}

func (c *Cleaner) cleanAllTmp(r *Report) {
	tmpRoot := c.Paths.Tmp()
	entries, err := os.ReadDir(tmpRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			r.Errors = append(r.Errors, fmt.Errorf("read temporary directory: %w", err))
		}
		return
	}
	for _, e := range entries {
		if isDisposableMarker(e.Name()) {
			continue // keep the dir tagged disposable even when empty
		}
		c.removeAll(r, filepath.Join(tmpRoot, e.Name()))
	}
}

// pruneCacheDir keeps only the cache files referenced by the current manifest,
// drops the rest. Used for installed apps when --all is not set.
func (c *Cleaner) pruneCacheDir(r *Report, id, dir string) {
	keep, err := c.expectedFiles(id)
	if err != nil {
		r.Errors = append(r.Errors, err)
		return
	}
	if len(keep) == 0 {
		// Manifest unloadable — leave the dir alone rather than wipe it.
		return
	}
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[k] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		r.Errors = append(r.Errors, fmt.Errorf("read cache directory %s: %w", dir, err))
		return
	}
	for _, e := range entries {
		if keepSet[e.Name()] {
			continue
		}
		c.removeFile(r, filepath.Join(dir, e.Name()))
	}
}

// expectedFiles returns the basenames of cache files that the current
// manifest's `sources:` would write to var/cache/<id>/.
func (c *Cleaner) expectedFiles(id string) ([]string, error) {
	m, err := c.Installed.Load(id)
	if err != nil {
		return nil, fmt.Errorf("load installed manifest %s while pruning cache: %w", id, err)
	}
	vars := c.Paths.Vars(id, m.Version)
	var names []string
	for _, s := range m.Sources {
		expanded := Source{
			Name:   manifest.Expand(s.Name, vars),
			URL:    manifest.Expand(s.URL, vars),
			File:   manifest.Expand(s.File, vars),
			SHA256: s.SHA256,
			SHA512: s.SHA512,
			Size:   s.Size,
		}
		name, err := safeFileName(expanded)
		if err != nil {
			return nil, fmt.Errorf("source filename for %s: %w", id, err)
		}
		names = append(names, name)
	}
	return names, nil
}

// --- removal helpers ---

func (c *Cleaner) removeAll(r *Report, path string) {
	bytes := dirSize(path)
	if err := os.RemoveAll(path); err != nil {
		log.Warn("Failed to remove", "path", path, "error", err)
		r.Errors = append(r.Errors, fmt.Errorf("remove %s: %w", path, err))
		return
	}
	r.Removed = append(r.Removed, path)
	r.Bytes += bytes
	log.Info("Removed", "path", path, "size", FormatBytes(bytes))
}

func (c *Cleaner) removeFile(r *Report, path string) {
	info, err := os.Stat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			r.Errors = append(r.Errors, fmt.Errorf("inspect %s: %w", path, err))
		}
		return
	}
	bytes := info.Size()
	if info.IsDir() {
		bytes = dirSize(path)
	}
	if err := os.RemoveAll(path); err != nil {
		log.Warn("Failed to remove", "path", path, "error", err)
		r.Errors = append(r.Errors, fmt.Errorf("remove %s: %w", path, err))
		return
	}
	r.Removed = append(r.Removed, path)
	r.Bytes += bytes
	log.Info("Removed", "path", path, "size", FormatBytes(bytes))
}

// dirSize sums file sizes recursively. Returns 0 on error.
func dirSize(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// FormatBytes renders a byte count in the largest sensible unit.
func FormatBytes(b int64) string {
	const k = 1024
	if b < k {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(k), 0
	for n := b / k; n >= k; n /= k {
		div *= k
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
