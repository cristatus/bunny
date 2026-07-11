package main

import (
	"fmt"
	"os"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/shim"
)

// WhichCmd reports which installed package a shimmed command resolves to in the
// current directory, and why (default provider vs a project pin).
type WhichCmd struct {
	Command string `arg:"" help:"Command name (e.g. node)"`
}

func (c *WhichCmd) Run(a *App) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	name := c.Command

	// SDK command shims (manifest bin:) resolve per-project.
	if _, ok := a.State.CommandOwner(name); ok {
		r := &shim.Resolver{State: a.State, Catalog: a.installedCatalog()}
		resolved, err := r.Resolve(name, cwd)
		if err != nil {
			return err
		}
		fmt.Printf("%s → %s (%s)\n", name, resolved.PackageID, a.State.Packages[resolved.PackageID].Version)
		fmt.Printf("  source: %s\n", resolved.Source)
		if path, err := a.commandPath(resolved.PackageID, name); err == nil {
			fmt.Printf("  path:   %s\n", path)
		}
		return nil
	}

	// Runtime-installed global executables (npm -g, etc.).
	if capability, ok := a.State.GlobalCommandCapability(name); ok {
		providerID, err := a.resolveCapabilityProvider(capability, cwd)
		if err != nil {
			return err
		}
		fmt.Printf("%s → %s (%s) — global tool for %q\n", name, providerID, a.State.Packages[providerID].Version, capability)
		if m, err := a.loadInstalledManifest(providerID); err == nil {
			if path, err := a.findGlobalExe(m, providerID, name); err == nil {
				fmt.Printf("  path:   %s\n", path)
			}
		}
		return nil
	}

	return fmt.Errorf("no installed package provides %q", name)
}

// commandPath returns the on-disk path of a package's manifest bin command.
func (a *App) commandPath(pkgID, command string) (string, error) {
	m, err := a.loadInstalledManifest(pkgID)
	if err != nil {
		return "", err
	}
	vars := a.Paths.Vars(pkgID, m.Version)
	for _, b := range m.Bin {
		if b.Name == command {
			return manifest.Expand(b.Path, vars), nil
		}
	}
	return "", fmt.Errorf("no bin %q in %s", command, pkgID)
}
