package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/verparse"
)

func (a *App) projectInstalls(cwd string, force bool) ([]string, error) {
	pins, err := shim.EffectivePins(cwd)
	if err != nil {
		return nil, err
	}
	if len(pins) == 0 {
		return nil, fmt.Errorf("no project pins found from %s", cwd)
	}
	selected := map[string]*manifest.Manifest{}
	ids := map[string]string{}
	for _, capability := range slices.Sorted(maps.Keys(pins)) {
		pin := pins[capability]
		id := pin.PackageID()
		var m *manifest.Manifest
		if a.State.IsInstalled(id) && !force {
			m, err = a.loadInstalledManifest(id)
		} else {
			var pkg *catalog.ResolvedPackage
			pkg, err = catalog.ResolvePackage(a.Catalog, id)
			if err == nil {
				m, err = pkg.LoadManifest()
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s %s (%s): %w", capability, pin.Value, pin.Source, err)
		}
		if m.KindOf() != manifest.KindSDK || m.Provides != capability {
			return nil, fmt.Errorf("%s does not provide SDK capability %s", id, capability)
		}

		selected[capability], ids[capability] = m, id
	}
	future := a.State.Clone()
	for _, capability := range slices.Sorted(maps.Keys(pins)) {
		m, id := selected[capability], ids[capability]
		names := make([]string, 0, len(m.Bin))
		for _, binary := range m.Bin {
			names = append(names, binary.Name)
		}
		if err := future.CheckCommandConflicts(id, m.Provides, names); err != nil {
			return nil, err
		}
		future.SetInstalled(id, m.Version, m.Provides, m.KindOf(), "")
		if err := future.SetProviderCommands(m.Provides, id, names); err != nil {
			return nil, err
		}
	}
	// Validate the full dependency graph before any download or installation.
	var ordered []string
	visited, visiting := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(capability string) error {
		if visited[capability] {
			return nil
		}
		if visiting[capability] {
			return fmt.Errorf("project SDK dependency cycle at %s", capability)
		}
		visiting[capability] = true
		m := selected[capability]
		for _, req := range m.Requires {
			depCap, min, constrained := manifest.ParseRequirement(req)
			dep := selected[depCap]
			// A requirement can also name a particular package ID.
			if dep == nil {
				for cap, candidate := range selected {
					if candidate.ID == depCap {
						depCap, dep = cap, candidate
						break
					}
				}
			}
			if dep != nil {
				if constrained && verparse.MajorInt(dep.Version) < min {
					return fmt.Errorf("%s requires %s, but project pins %s", m.ID, req, ids[depCap])
				}
				if err := visit(depCap); err != nil {
					return err
				}
			} else if constrained {
				if a.State.ResolveProviderMin(depCap, min) == "" {
					return fmt.Errorf("%s requires %s; install or pin a qualifying SDK", m.ID, req)
				}
			} else if !a.State.IsSatisfied(req) {
				return fmt.Errorf("%s requires %s; install or pin that dependency", m.ID, req)
			}
		}
		visiting[capability], visited[capability] = false, true
		ordered = append(ordered, ids[capability])
		return nil
	}
	caps := slices.Sorted(maps.Keys(pins))
	for _, cap := range caps {
		if err := visit(cap); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func (c *PinCmd) initialize(a *App, cwd string) error {
	path := filepath.Join(cwd, shim.ProjectVersionFile)
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s already exists; use bunny pin to edit individual pins", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	inherited, err := shim.EffectivePins(cwd)
	if err != nil {
		return err
	}
	selections := map[string]string{}
	for cap, id := range a.State.Providers {
		selections[cap] = id
	}
	for cap, pin := range inherited {
		if err := pin.CheckInstalled(a.State); err != nil {
			return err
		}
		selections[cap] = pin.PackageID()
	}
	var lines []string
	caps := slices.Sorted(maps.Keys(selections))
	for _, cap := range caps {
		id := selections[cap]
		m, err := a.loadInstalledManifest(id)
		if err != nil {
			return err
		}
		if m.KindOf() != manifest.KindSDK {
			continue
		}
		lines = append(lines, cap+" "+id)
	}
	if len(lines) == 0 {
		return fmt.Errorf("no active SDKs to pin")
	}
	// Exclusive create: a file that appeared since the check above is never
	// overwritten. A failed write removes what it started.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	a.status().Printf("created ./%s with %d SDK pins\n", shim.ProjectVersionFile, len(lines))
	return nil
}
