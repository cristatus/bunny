package main

import (
	"fmt"
	"os"

	"github.com/cristatus/bunny/internal/installer"
	"github.com/cristatus/bunny/internal/ui"
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
		p := ui.New(os.Stdout)
		p.Println()
		if len(report.Removed) == 0 {
			if err == nil {
				p.Println("nothing to clean")
			}
			return err
		}
		for _, item := range report.Removed {
			p.Println(item)
		}
		p.Println() // blank before the summary, matching install/update
		p.Printf("cleaned %d items, freed %s\n", len(report.Removed), humanBytes(report.Bytes))
		return err
	})
}

// humanBytes formats a byte count as a short human-readable size.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
