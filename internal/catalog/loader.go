// Package catalog defines the Loader interface every catalog source
// implements (local on-disk, remote HTTP, etc.) and provides the Composite
// loader that layers them in priority order.
package catalog

import (
	"slices"

	"github.com/cristatus/bunny/internal/manifest"
)

// PackageInfo is a lightweight summary of a manifest, used by `bunny list`.
type PackageInfo struct {
	ID   string
	Tags []string
	// Kind is the resolved install kind ("app", "cli", "sdk"), so a listing
	// can say where a package lands without fetching its manifest.
	Kind        string
	Name        string
	Description string
	Version     string
	Provides    string
	Requires    []string
	// Source names the catalog the entry came from, stamped while resolving.
	Source string
}

// InfoOf summarizes a manifest for listings.
func InfoOf(m *manifest.Manifest) PackageInfo {
	return PackageInfo{
		ID:          m.ID,
		Tags:        slices.Clone(m.Tags),
		Kind:        m.KindOf(),
		Name:        m.Name,
		Description: m.Description,
		Version:     m.Version,
		Provides:    m.Provides,
		Requires:    slices.Clone(m.Requires),
	}
}

// Loader is the interface every catalog source implements.
type Loader interface {
	// List returns summary info for every package in the catalog.
	List() ([]PackageInfo, error)

	// Lookup returns the summary for one package. Resolution calls it on every
	// catalog, so keep it cheap. Returns ErrNotFound when the catalog does not
	// carry the package and ErrUnavailable when it cannot say either way.
	Lookup(id string) (PackageInfo, error)

	// Load returns a parsed and validated manifest for the package.
	Load(id string) (*manifest.Manifest, error)

	// LoadFile reads a sibling file (e.g. an embedded shell script) from the
	// package's directory. relPath must be a clean relative path;
	// implementations reject path traversal via manifest.SafeRelPath.
	LoadFile(id, relPath string) ([]byte, error)
}
