package catalog

import (
	"errors"

	"github.com/cristatus/bunny/internal/manifest"
)

// ErrNotFound is the sentinel a Loader returns when a package is not present
// in that loader. Composite falls through on ErrNotFound only — every other
// error stops the chain so a corrupt local override can't silently be
// replaced by remote content.
var ErrNotFound = errors.New("not found in catalog")

// Composite tries Loaders in priority order, taking the first hit.
//
// Typical setup: local (user catalog) > remote (HTTP).
type Composite struct {
	loaders []Loader
}

// NewComposite returns a Composite over the given loaders, in priority order.
func NewComposite(loaders ...Loader) *Composite {
	return &Composite{loaders: loaders}
}

// List unions package summaries; first loader to return a given ID wins.
func (c *Composite) List() ([]PackageInfo, error) {
	seen := map[string]bool{}
	var out []PackageInfo
	var errs []error
	successes := 0
	for _, l := range c.loaders {
		pkgs, err := l.List()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		successes++
		for _, p := range pkgs {
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	if successes == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Load returns the first successful manifest load. Falls through to the next
// loader only on ErrNotFound; any other error is returned immediately.
func (c *Composite) Load(id string) (*manifest.Manifest, error) {
	var lastErr error
	for _, l := range c.loaders {
		m, err := l.Load(id)
		if err == nil {
			return m, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// LoadFile returns the first successful sibling file fetch. Falls through to
// the next loader only on ErrNotFound; any other error stops the chain.
func (c *Composite) LoadFile(id, relPath string) ([]byte, error) {
	var lastErr error
	for _, l := range c.loaders {
		data, err := l.LoadFile(id, relPath)
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}
