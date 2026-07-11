package main

import (
	"fmt"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/installer"
)

// CleanCmd prunes the download cache and tmp dirs.
//
// Default: drop cache files for uninstalled apps + everything in var/tmp/.
// `--all` widens to include caches for installed apps (forces a re-download
// next install). Per-app data dirs (var/app/<id>/) are NOT touched here —
// those go via `bunny uninstall --purge`.
type CleanCmd struct {
	ID  string `arg:"" optional:"" help:"Package ID (default: all)"`
	All bool   `help:"Drop all download cache, even for installed apps"`
}

func (c *CleanCmd) Run(a *App) error {
	return a.withMutation(a.context(), func() error {
		cleaner := installer.NewCleaner(a.Paths, a.Catalog, a.State)
		report, err := cleaner.Clean(c.ID, c.All)
		if report == nil {
			return err
		}
		if len(report.Removed) == 0 {
			if err == nil {
				log.Info("Nothing to clean")
			}
			return err
		}
		for _, p := range report.Removed {
			fmt.Println(p)
		}
		log.Info("Cleaned", "items", len(report.Removed), "freed", fmt.Sprintf("%d bytes", report.Bytes))
		return err
	})
}
