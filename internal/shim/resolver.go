package shim

import (
	"fmt"

	"github.com/cristatus/bunny/internal/manifest"
)

// State is the small slice of state.State the resolver needs.
type State interface {
	CommandOwner(name string) (string, bool)
	IsInstalled(id string) bool
}

// Catalog is the small slice of catalog.Loader the resolver needs.
type Catalog interface {
	Load(id string) (*manifest.Manifest, error)
}

// Resolved describes which package a shim invocation should run.
type Resolved struct {
	PackageID string
	Source    string // "default" or `.bunny-version (<path>)`
}

// Resolver dispatches a bare command-name invocation (argv[0] = "node") to
// the right installed package, applying any `.bunny-version` pin.
type Resolver struct {
	State   State
	Catalog Catalog
}

// Resolve implements:
//
//  1. Look up command → owning package via state.Commands.
//  2. If the owning manifest has `provides:`, walk up cwd for `.bunny-version`.
//  3. If pinned, attempt package ID `<provides>-<version>`; if installed, use it.
//  4. If pinned but the candidate isn't installed, error — silent fallback to
//     the global default would run the wrong toolchain version, which is the
//     opposite of what a project pin means.
func (r *Resolver) Resolve(name, cwd string) (*Resolved, error) {
	owner, ok := r.State.CommandOwner(name)
	if !ok {
		return nil, fmt.Errorf("no installed package provides %q", name)
	}

	// The owner's manifest is the command being resolved — without it we can't
	// find the binary to exec (a.run reloads it and would fail regardless), so
	// this is a legitimate hard failure, not a peripheral one to tolerate.
	m, err := r.Catalog.Load(owner)
	if err != nil {
		return nil, fmt.Errorf("load manifest for command %q: %w", name, err)
	}
	if m.Provides == "" {
		return &Resolved{PackageID: owner, Source: "default"}, nil
	}

	pinned, err := ResolveProjectVersion(cwd, m.Provides)
	if err != nil {
		return nil, fmt.Errorf("resolve project version for %s: %w", m.Provides, err)
	}
	if pinned == nil {
		return &Resolved{PackageID: owner, Source: "default"}, nil
	}

	candidate := m.Provides + "-" + pinned.Version
	if r.State.IsInstalled(candidate) {
		return &Resolved{
			PackageID: candidate,
			Source:    fmt.Sprintf(".bunny-version (%s)", pinned.Source),
		}, nil
	}
	return nil, fmt.Errorf(
		"%s %s pinned in %s, but %s is not installed\nhint: bunny install %s",
		m.Provides, pinned.Version, pinned.Source, candidate, candidate,
	)
}
