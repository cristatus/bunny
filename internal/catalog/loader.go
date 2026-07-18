// Package catalog defines the Loader interface every catalog source
// implements (local on-disk, remote HTTP, etc.) and provides the Composite
// loader that layers them in priority order.
package catalog

import "github.com/cristatus/bunny/internal/manifest"

// PackageInfo is a lightweight summary of a manifest, used by `bunny list`.
type PackageInfo struct {
	ID          string
	Category    string
	Name        string
	Description string
	Version     string
	Provides    string
	Requires    []string
}

// Loader is the interface every catalog source implements.
type Loader interface {
	// List returns summary info for every package in the catalog.
	List() ([]PackageInfo, error)

	// Load returns a parsed and validated manifest for the package.
	Load(id string) (*manifest.Manifest, error)

	// LoadFile reads a sibling file (e.g. an embedded shell script) from the
	// package's directory. relPath must be a clean relative path;
	// implementations reject path traversal via manifest.SafeRelPath.
	LoadFile(id, relPath string) ([]byte, error)
}
