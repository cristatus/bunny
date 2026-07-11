package main

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
)

// InstallCmd installs one or more packages. --force reinstalls the same version.
type InstallCmd struct {
	IDs   []string `arg:"" name:"id" help:"Package ID(s)"`
	Force bool     `short:"f" help:"Force reinstall"`
}

func (c *InstallCmd) Run(a *App) error {
	return a.withMutation(a.context(), func() error {
		regenToolchains := false
		for _, id := range c.IDs {
			if err := a.Installer.Install(a.context(), id, c.Force); err != nil {
				return fmt.Errorf("install %s: %w", id, err)
			}
			m, err := a.loadInstalledManifest(id)
			if err != nil {
				return fmt.Errorf("load installed manifest after installing %s: %w", id, err)
			}
			if m.Provides == "jdk" || m.Toolchains != "" {
				regenToolchains = true
			}
			// A provider that didn't become active needs an explicit `use`;
			// otherwise the install looks like a no-op to the user.
			if m.Provides != "" {
				if active := a.State.Providers[m.Provides]; active != id {
					log.Info("Installed, but not the active provider — run 'bunny use "+id+"' to switch",
						"capability", m.Provides, "active", active)
				}
			}
		}
		if regenToolchains {
			if err := a.regenerateToolchains(); err != nil {
				return fmt.Errorf("regenerate toolchains after install: %w", err)
			}
		}
		return nil
	})
}

// UninstallCmd removes one or more packages.
type UninstallCmd struct {
	IDs   []string `arg:"" name:"id" help:"Package ID(s)"`
	Purge bool     `help:"Also remove the per-app data dir under var/app/{id}/"`
}

func (c *UninstallCmd) Run(a *App) error {
	return a.withMutation(a.context(), func() error {
		var postErrors []error
		regenToolchains := false
		for _, id := range c.IDs {
			capability := ""
			if m, err := a.loadInstalledManifest(id); err == nil {
				capability = m.Provides
				if m.Provides == "jdk" || m.Toolchains != "" {
					regenToolchains = true
				}
			}
			if err := a.Installer.Uninstall(id, c.Purge); err != nil {
				return fmt.Errorf("uninstall %s: %w", id, err)
			}
			if capability != "" {
				if _, removed, err := a.reshimCapabilities(capability); err != nil {
					postErrors = append(postErrors, fmt.Errorf("reshim %s after uninstall: %w", capability, err))
				} else if len(removed) > 0 {
					log.Info("Pruned global tool shims", "capability", capability, "removed", len(removed))
				}
			}
		}
		if regenToolchains {
			if err := a.regenerateToolchains(); err != nil {
				postErrors = append(postErrors, fmt.Errorf("regenerate toolchains after uninstall: %w", err))
			}
		}
		return errors.Join(postErrors...)
	})
}
