package catalog

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/manifest"
)

// ErrNotFound is the sentinel a Loader returns when a package is not present
// in that loader. Composite falls through on it, and on ErrUnavailable, only —
// every other error stops the chain so a corrupt local override can't silently
// be replaced by remote content.
var ErrNotFound = errors.New("not found in catalog")

// ErrUnavailable is the sentinel a Loader returns when it cannot answer at
// all: a remote whose index is neither reachable nor cached. Separate from
// ErrNotFound because "this catalog is down" and "this catalog does not carry
// it" stop meaning the same thing once another catalog might carry it.
var ErrUnavailable = errors.New("catalog unavailable")

// Source is one catalog in a Composite.
type Source struct {
	// Name identifies the catalog in state, in messages, and in its cache.
	Name   string
	Loader Loader
}

// Resolver is implemented by a catalog that reads from several sources and can
// bind a package to the one serving it.
type Resolver interface {
	Resolve(id string) (*ResolvedPackage, error)
}

// Composite resolves a package across several catalogs, in priority order: the
// first catalog carrying a package id serves it, whatever versions the others
// publish, so no catalog can take over an id held by one above it.
type Composite struct {
	sources []Source
}

// NewComposite returns a Composite over the given sources, in priority order.
func NewComposite(sources ...Source) *Composite {
	return &Composite{sources: sources}
}

// ResolvedPackage binds a package to the catalog serving it, so a manifest and
// its sibling files cannot come from different catalogs.
type ResolvedPackage struct {
	// Source is the catalog serving the package, read after loading rather than
	// before: a fallback updates it to whichever one answered.
	Source Source
	// Info is the summary the resolution was decided on.
	Info PackageInfo

	id string
	// chain is the catalogs to try, best first: the one resolution chose, then
	// the ones below it, which were never asked.
	chain []Source
	bound bool
}

// LoadManifest returns the manifest, binding the handle to whichever catalog
// produced it.
func (p *ResolvedPackage) LoadManifest() (*manifest.Manifest, error) {
	return serve(p, func(l Loader) (*manifest.Manifest, error) { return l.Load(p.id) })
}

// LoadFile returns a sibling file from the catalog bound by the first load, so an
// embedded script belongs to the manifest that referenced it.
func (p *ResolvedPackage) LoadFile(relPath string) ([]byte, error) {
	return serve(p, func(l Loader) ([]byte, error) { return l.LoadFile(p.id, relPath) })
}

// serve walks the chain until one catalog answers, then pins it so every later
// load comes from the same one.
//
// Getting past the first means a catalog that owns the package could not produce
// it. Serving another one's copy is then a substitution the ordering did not ask
// for, so it is logged rather than done quietly, and state records who answered.
func serve[T any](p *ResolvedPackage, load func(Loader) (T, error)) (T, error) {
	var zero T
	if p.bound {
		return load(p.Source.Loader)
	}
	var lastErr error
	chose := p.chain[0]
	for _, src := range p.chain {
		v, err := load(src.Loader)
		if err == nil {
			if src.Name != chose.Name {
				log.Warn("Package served by a different catalog than resolution chose",
					"package", p.id, "chose", chose.Name, "served", src.Name)
			}
			p.Source, p.bound = src, true
			return v, nil
		}
		if !Unresolved(err) {
			return zero, err
		}
		log.Warn("Catalog could not serve package, trying the next one",
			"package", p.id, "catalog", src.Name, "error", err)
		lastErr = err
	}
	return zero, lastErr
}

// Resolve binds a package to the first catalog carrying it, keeping the ones
// below it as the rest of the chain.
func (c *Composite) Resolve(id string) (*ResolvedPackage, error) {
	var err error
	for i, src := range c.sources {
		info, lookupErr := src.Loader.Lookup(id)
		switch {
		case lookupErr == nil:
			// Order is the answer, so stop rather than pay for index fetches
			// that cannot change it. The rest stay unasked.
			return &ResolvedPackage{
				Source: src,
				Info:   withSource(info, src.Name),
				id:     id,
				chain:  c.sources[i:],
			}, nil
		case errors.Is(lookupErr, ErrUnavailable):
			log.Warn("Catalog unavailable, skipping", "catalog", src.Name, "error", lookupErr)
			err = lookupErr
		case errors.Is(lookupErr, ErrNotFound):
			err = lookupErr
		default:
			return nil, fmt.Errorf("catalog %s: %w", src.Name, lookupErr)
		}
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: package %q", ErrNotFound, id)
}

// List unions package summaries, each stamped with the catalog that serves it.
// A duplicate id is dropped: a catalog higher up already answered for it, and
// listing has to agree with what an install would resolve to.
func (c *Composite) List() ([]PackageInfo, error) {
	seen := map[string]bool{}
	var out []PackageInfo // first-seen order, so listings stay stable
	var errs []error
	for _, src := range c.sources {
		pkgs, err := src.Loader.List()
		if err != nil {
			errs = append(errs, fmt.Errorf("catalog %s: %w", src.Name, err))
			continue
		}
		for _, p := range pkgs {
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			out = append(out, withSource(p, src.Name))
		}
	}
	if len(errs) == len(c.sources) && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

// Load returns the manifest from the catalog that serves the package.
func (c *Composite) Load(id string) (*manifest.Manifest, error) {
	pkg, err := c.Resolve(id)
	if err != nil {
		return nil, err
	}
	return pkg.LoadManifest()
}

// LoadFile returns a sibling file from the catalog that serves the package.
func (c *Composite) LoadFile(id, relPath string) ([]byte, error) {
	pkg, err := c.Resolve(id)
	if err != nil {
		return nil, err
	}
	return pkg.LoadFile(relPath)
}

// Lookup returns the summary from the catalog that would serve the package.
func (c *Composite) Lookup(id string) (PackageInfo, error) {
	pkg, err := c.Resolve(id)
	if err != nil {
		return PackageInfo{}, err
	}
	return pkg.Info, nil
}

// ResolvePackage binds a package on any catalog, so callers need not know whether
// theirs reads several. A single loader has no configured name, leaving
// provenance empty rather than inventing one.
func ResolvePackage(cat Loader, id string) (*ResolvedPackage, error) {
	if r, ok := cat.(Resolver); ok {
		return r.Resolve(id)
	}
	info, err := cat.Lookup(id)
	if err != nil {
		return nil, err
	}
	src := Source{Loader: cat}
	return &ResolvedPackage{Source: src, Info: info, id: id, chain: []Source{src}}, nil
}

// withSource stamps the catalog's name onto a summary; a loader does not know
// which configured source it is.
func withSource(info PackageInfo, name string) PackageInfo {
	info.Source = name
	return info
}

// Unresolved reports whether err means a source produced no package: it does
// not carry it, or it could not answer. Composite falls through on these, and
// callers that can carry on without a manifest treat them alike.
func Unresolved(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable)
}
