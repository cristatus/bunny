package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sys/unix"

	"github.com/cristatus/bunny/internal/suggest"
	"github.com/cristatus/bunny/internal/ui"
)

// InstallCmd installs one or more packages. --force reinstalls the same version.
type InstallCmd struct {
	IDs   []string `arg:"" name:"id" help:"Package ID(s)"`
	Force bool     `short:"f" help:"Force reinstall"`
}

func (c *InstallCmd) Run(a *App) error {
	rep := a.reporter()
	ctx := rep.Begin(a.context(), c.IDs)
	start := time.Now()
	done, failed := 0, 0
	err := a.withMutation(ctx, func() error {
		regenToolchains := false
		var errs []error
		// Preflight the catalog once so an unknown id fails fast with a
		// did-you-mean suggestion instead of a raw "not in remote index".
		catalogPkgs, _ := a.Catalog.List()
		for _, id := range c.IDs {
			if ctx.Err() != nil {
				break // cancelled (Ctrl+C)
			}
			if len(catalogPkgs) > 0 {
				if err := requireInCatalog(id, catalogPkgs); err != nil {
					rep.Fail(id, err)
					failed++
					continue
				}
			}
			// Version-agnostic skip: already installed and not forced. Reuse the
			// "installed" label (it is installed) — the dim styling distinguishes
			// it from a fresh install.
			if a.State.IsInstalled(id) && !c.Force {
				ver := ""
				if pi, ok := a.State.Packages[id]; ok {
					ver = pi.Version
				}
				rep.Skip(id, "installed", ver)
				continue
			}
			rep.Start(id)
			if err := a.Installer.Install(ctx, id, c.Force, reporterHook{rep, id}); err != nil {
				rep.Fail(id, err)
				failed++
				continue // resilient batch: never abort on one failure
			}
			m, err := a.loadInstalledManifest(id)
			if err != nil {
				rep.Fail(id, err)
				failed++
				continue
			}
			rep.Done(id, "installed", m.Version)
			done++
			if m.Provides == "jdk" || m.Toolchains != "" {
				regenToolchains = true
			}
			if m.Provides != "" {
				if active := a.State.Providers[m.Provides]; active != id {
					log.Debug("Installed, not active provider", "capability", m.Provides, "package", id, "active", active)
				}
			}
		}
		if regenToolchains {
			if err := a.regenerateToolchains(); err != nil {
				errs = append(errs, fmt.Errorf("regenerate toolchains: %w", err))
			}
		}
		return errors.Join(errs...) // post-op errors only (not per-package)
	})
	rep.Close() // tear down the live view before printing the stdout summary
	printSummary(a, "installed", done, failed, time.Since(start))
	return finishErr(err, failed)
}

// finishErr resolves a batch command's exit: a real (unreported) error prints
// via ui.Fatal; per-package failures are already shown, so signal a non-zero
// exit without re-printing; otherwise success.
func finishErr(postErr error, failed int) error {
	if postErr != nil {
		return postErr
	}
	if failed > 0 {
		return errHandled
	}
	return nil
}

// printSummary prints the end-of-op summary line to stdout after the live view
// has been closed, so it never interleaves with the Bubble Tea frame.
func printSummary(a *App, verb string, done, failed int, elapsed time.Duration) {
	p := ui.New(os.Stdout)
	p.Println()
	p.Print(installSummary(p, verb, done, failed, elapsed))
}

// installSummary is the end-of-op line, e.g. "installed 2 packages in 3.1s"
// with a trailing ", 1 failed" (in red) when any package failed.
func installSummary(p *ui.Printer, verb string, done, failed int, elapsed time.Duration) string {
	noun := "packages"
	if done == 1 {
		noun = "package"
	}
	line := fmt.Sprintf("%s %d %s in %s", verb, done, noun, elapsed.Round(100*time.Millisecond))
	if failed > 0 {
		line += ", " + p.PaintStatus(fmt.Sprintf("%d failed", failed), ui.Bad)
	}
	return line + "\n"
}

// confirmPurge prompts before --purge permanently deletes per-app data.
// Returns (confirmed, err); err when stdin is not interactive, since there is
// no safe way to ask (the caller should pass --yes instead).
func confirmPurge(ids []string) (bool, error) {
	if !stdinIsTTY() {
		return false, fmt.Errorf("refusing to --purge in a non-interactive session; pass --yes to confirm")
	}
	fmt.Fprintf(os.Stderr, "This permanently deletes the per-app data for: %s\nContinue? [y/N] ", strings.Join(ids, ", "))
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// stdinIsTTY reports whether stdin is a real terminal (not a pipe, file, or
// /dev/null — all of which a ModeCharDevice check would misclassify).
func stdinIsTTY() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	return err == nil
}

// UninstallCmd removes one or more packages.
type UninstallCmd struct {
	IDs   []string `arg:"" name:"id" help:"Package ID(s)"`
	Purge bool     `help:"Also remove the per-app data dir under var/app/{id}/"`
	Yes   bool     `short:"y" help:"Skip the --purge confirmation prompt"`
}

func (c *UninstallCmd) Run(a *App) error {
	// --purge irreversibly deletes per-app data; confirm first (unless --yes).
	if c.Purge && !c.Yes {
		ok, err := confirmPurge(c.IDs)
		if err != nil {
			return err
		}
		if !ok {
			ui.New(os.Stdout).Println("aborted")
			return nil
		}
	}
	rep := a.reporter()
	ctx := rep.Begin(a.context(), c.IDs)
	start := time.Now()
	done, failed := 0, 0
	err := a.withMutation(ctx, func() error {
		regenToolchains := false
		installed := a.State.Installed()
		var errs []error
		for _, id := range c.IDs {
			if ctx.Err() != nil {
				break // cancelled (Ctrl+C)
			}
			// Removing something not installed is a no-op — a dim skip, unless
			// the id looks like a typo of an installed one (then flag it).
			if !a.State.IsInstalled(id) {
				if match, ok := suggest.Closest(id, installed); ok {
					rep.Fail(id, fmt.Errorf("not installed — did you mean %q?", match))
					failed++
				} else {
					rep.Skip(id, "not installed", "")
				}
				continue
			}
			capability := ""
			if m, err := a.loadInstalledManifest(id); err == nil {
				capability = m.Provides
				if m.Provides == "jdk" || m.Toolchains != "" {
					regenToolchains = true
				}
			}
			rep.Start(id)
			rep.Phase(id, "removing")
			if err := a.Installer.Uninstall(id, c.Purge); err != nil {
				rep.Fail(id, err)
				failed++
				continue // resilient batch: never abort on one failure
			}
			rep.Done(id, "removed", "")
			done++
			if capability != "" {
				if _, removed, err := a.reshimCapabilities(capability); err != nil {
					errs = append(errs, fmt.Errorf("reshim %s after uninstall: %w", capability, err))
				} else if len(removed) > 0 {
					log.Debug("Pruned global tool shims", "capability", capability, "removed", len(removed))
				}
			}
		}
		if regenToolchains {
			if err := a.regenerateToolchains(); err != nil {
				errs = append(errs, fmt.Errorf("regenerate toolchains after uninstall: %w", err))
			}
		}
		return errors.Join(errs...) // post-op errors only (not per-package)
	})
	rep.Close()
	printSummary(a, "removed", done, failed, time.Since(start))
	return finishErr(err, failed)
}
