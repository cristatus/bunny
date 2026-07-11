package main

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
)

// UpdateCmd checks for packages with newer upstream versions. By default it
// only reports what is available (read-only); pass --apply to reinstall each
// outdated package with the latest catalog version.
//
// Scope defaults to installed packages. --all widens the check to the whole
// catalog (maintainer view); ignored when --apply is set.
type UpdateCmd struct {
	ID    string `arg:"" optional:"" help:"Package ID (default: every installed package)"`
	All   bool   `help:"Check the whole catalog, not just installed packages"`
	Apply bool   `help:"Apply available updates (reinstall with newer versions)"`
}

func (c *UpdateCmd) Run(a *App) error {
	if c.Apply {
		return c.apply(a)
	}
	return c.check(a)
}

func (c *UpdateCmd) check(a *App) error {
	report, err := a.checkUpdates(a.context(), c.ID, c.All)
	if err != nil {
		return err
	}
	for _, r := range report.Results {
		log.Info("Update available", "package", r.ID, "current", r.CurrentVersion, "latest", r.LatestVersion)
	}
	if err := report.Err(); err != nil {
		return err
	}
	if len(report.Results) == 0 {
		if c.All {
			log.Info("All checked packages are up to date", "checked", report.Checked, "unsupported", report.Skipped)
		} else {
			log.Info("All checked installed packages are up to date", "checked", report.Checked, "unsupported", report.Skipped)
		}
		return nil
	}
	return nil
}

func (c *UpdateCmd) apply(a *App) error {
	if c.All {
		log.Warn("--all has no effect with --apply; applying updates to installed packages only")
	}
	ctx := a.context()
	report, err := a.checkUpdates(ctx, c.ID, false)
	if err != nil {
		return err
	}
	errs := append([]error(nil), report.Failures...)
	mutationErr := a.withMutation(ctx, func() error {
		if c.ID != "" && !a.State.IsInstalled(c.ID) {
			return fmt.Errorf("package %q is not installed", c.ID)
		}
		applied := 0
		for _, r := range report.Results {
			installed, ok := a.State.Packages[r.ID]
			if !ok {
				log.Debug("Skipping uninstalled package", "package", r.ID)
				continue
			}
			if installed.Version != r.CurrentVersion {
				log.Debug("Skipping package changed since update check", "package", r.ID,
					"checked", r.CurrentVersion, "installed", installed.Version)
				continue
			}
			m, err := a.Catalog.Load(r.ID)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: load manifest: %w", r.ID, err))
				continue
			}
			if m.Version == r.CurrentVersion {
				log.Warn("Skipping update: catalog manifest still at installed version",
					"package", r.ID,
					"version", m.Version,
					"hint", "the local catalog is stale; refresh or wait for an auto-update PR")
				continue
			}
			log.Info("Updating", "package", r.ID, "from", r.CurrentVersion, "to", m.Version)
			if err := a.Installer.Install(ctx, r.ID, true); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.ID, err))
				continue
			}
			applied++
		}
		if applied > 0 {
			if err := a.regenerateToolchains(); err != nil {
				errs = append(errs, fmt.Errorf("regenerate toolchains: %w", err))
			}
		}
		return nil
	})
	return errors.Join(mutationErr, errors.Join(errs...))
}
