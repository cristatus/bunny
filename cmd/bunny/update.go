package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/checker"
	"github.com/cristatus/bunny/internal/ui"
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
	p := ui.New(os.Stdout)
	p.Println() // blank before the live check status (and the results below it)
	report, err := a.checkUpdates(a.context(), c.ID, c.All)
	if err != nil {
		return err
	}
	if err := report.Err(); err != nil {
		return err
	}
	if len(report.Results) == 0 {
		p.Println("all packages are up to date")
		return nil
	}
	if c.All {
		p.Printf("%d packages have updates\n\n", len(report.Results))
	} else {
		p.Printf("%d of %d packages have updates\n\n", len(report.Results), len(a.State.Packages))
	}
	p.Print(renderUpdateTable(p, report.Results))
	p.Println()
	p.Println("run 'bunny update --apply' to install") // the one kept action line
	return nil
}

// renderUpdateTable prints Package / Change (current → latest) / Bump. Bump is
// plain text (no per-level color — that would need a third color, out of scope).
func renderUpdateTable(p *ui.Printer, results []checker.Result) string {
	var cells [][]ui.Cell
	for _, r := range results {
		cells = append(cells, []ui.Cell{
			{Text: r.ID},
			{Text: fmt.Sprintf("%s → %s", r.CurrentVersion, r.LatestVersion)},
			{Text: bumpKind(r.CurrentVersion, r.LatestVersion)},
		})
	}
	return p.Table([]string{"Package", "Change", "Bump"}, cells)
}

// bumpKind classifies a version change by the first differing dotted-numeric
// component: index 0 → major, index 1 → minor, anything later (or non-numeric
// noise) → patch. Presentation only; not a semver authority.
func bumpKind(current, latest string) string {
	split := func(r rune) bool { return r == '.' || r == '+' || r == '-' }
	cf := strings.FieldsFunc(current, split)
	lf := strings.FieldsFunc(latest, split)
	for i := 0; i < len(cf) && i < len(lf); i++ {
		if cf[i] != lf[i] {
			switch i {
			case 0:
				return "major"
			case 1:
				return "minor"
			default:
				return "patch"
			}
		}
	}
	return "patch"
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
	rep := a.reporter()
	updateIDs := make([]string, len(report.Results))
	for i, r := range report.Results {
		updateIDs[i] = r.ID
	}
	ctx = rep.Begin(ctx, updateIDs)
	errs := append([]error(nil), report.Failures...)
	start := time.Now()
	applied, failed := 0, 0
	mutationErr := a.withMutation(ctx, func() error {
		if c.ID != "" && !a.State.IsInstalled(c.ID) {
			return fmt.Errorf("package %q is not installed", c.ID)
		}
		for _, r := range report.Results {
			if ctx.Err() != nil {
				break // cancelled (Ctrl+C)
			}
			installed, ok := a.State.Packages[r.ID]
			if !ok {
				rep.Skip(r.ID, "missing", "")
				continue
			}
			if installed.Version != r.CurrentVersion {
				rep.Skip(r.ID, "changed", "")
				continue
			}
			m, err := a.Catalog.Load(r.ID)
			if err != nil {
				rep.Fail(r.ID, err)
				failed++
				continue
			}
			if m.Version == r.CurrentVersion {
				rep.Skip(r.ID, "stale", "")
				continue
			}
			rep.Start(r.ID)
			if err := a.Installer.Install(ctx, r.ID, true, reporterHook{rep, r.ID}); err != nil {
				rep.Fail(r.ID, err)
				failed++
				continue
			}
			rep.Done(r.ID, "updated", fmt.Sprintf("%s → %s", r.CurrentVersion, m.Version))
			applied++
		}
		if applied > 0 {
			if err := a.regenerateToolchains(); err != nil {
				errs = append(errs, fmt.Errorf("regenerate toolchains: %w", err))
			}
		}
		return nil
	})
	rep.Close()
	printSummary(a, "updated", applied, failed, time.Since(start))
	// errs holds check-phase + post-op failures (not shown per-package); print
	// those. Per-apply failures are already shown, so a bare failed count exits
	// non-zero without re-printing.
	return finishErr(errors.Join(mutationErr, errors.Join(errs...)), failed)
}
