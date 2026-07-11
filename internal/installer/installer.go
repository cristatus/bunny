// Package installer orchestrates the install/uninstall workflow: download
// sources, run prepare in the install-time sandbox, place files, register
// shims and desktop integration, persist state.
package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
	"gopkg.in/yaml.v3"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/desktop"
	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/runtime"
	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/state"
)

// PrepareFn runs an install-time bwrap step. Defaults to runtime.PrepareStepsContext;
// tests inject a noop.
type PrepareFn func(ctx context.Context, srcDir, pkgDir string, commands []string, vars map[string]string) error

// BunnyPathFn returns the absolute path to the bunny binary that shims should
// point at. Defaults to shim.BunnyBinaryPath; tests inject a fixed value.
type BunnyPathFn func(binDir string) (string, error)

// SaveStateFn persists state. Tests inject failures so install/uninstall do
// not accidentally degrade state persistence errors into warnings.
type SaveStateFn func(st *state.State, path string) error

// Installer holds the long-lived dependencies the install workflow needs.
type Installer struct {
	Paths     *paths.Paths
	Catalog   catalog.Loader
	Installed catalog.Loader
	State     *state.State
	Download  *Downloader

	Prepare   PrepareFn
	BunnyPath BunnyPathFn
	SaveState SaveStateFn
}

// New returns an Installer wired with default Download + Prepare + BunnyPath.
func New(paths *paths.Paths, cat catalog.Loader, st *state.State) *Installer {
	return &Installer{
		Paths:     paths,
		Catalog:   cat,
		Installed: catalog.NewInstalled(cat, paths.ManifestFile),
		State:     st,
		Download:  NewDownloader(),
		Prepare:   runtime.PrepareStepsContext,
		BunnyPath: shim.BunnyBinaryPath,
		SaveState: func(st *state.State, path string) error {
			return st.Save(path)
		},
	}
}

// Install runs the full workflow for a single package ID. force=true
// re-installs even if the same version is already present.
func (i *Installer) Install(ctx context.Context, id string, force bool) error {
	m, err := i.Catalog.Load(id)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	stateBefore := i.State.Clone()
	var oldManifest *manifest.Manifest
	if i.State.IsInstalled(id) {
		oldManifest, err = i.loadInstalledManifest(id)
		if err != nil {
			return fmt.Errorf("load previous installed manifest: %w", err)
		}
	}
	cacheBefore, err := snapshotFile(i.Paths.ManifestFile(id))
	if err != nil {
		return fmt.Errorf("snapshot installed manifest: %w", err)
	}
	oldCommands := i.State.CommandsOwnedBy(id)
	newCommands := binNames(m)
	// A capability provider becomes active only when nothing already provides
	// the capability, or when we're reinstalling the current active provider.
	// Installing an additional provider (e.g. a second JDK) must not hijack the
	// active one's shims — the user switches explicitly with `bunny use`.
	active := ""
	if m.Provides != "" {
		active = i.State.ResolveProvider(m.Provides)
	}
	becomeActive := m.Provides == "" || active == "" || active == id
	replacedCommands := append([]string(nil), oldCommands...)
	if m.Provides != "" && becomeActive {
		replacedCommands = appendMissing(replacedCommands, i.State.CommandsForCapability(m.Provides)...)
	}

	if !force && i.alreadyInstalledSame(id, m.Version) {
		return fmt.Errorf("%s@%s is already installed (use --force to reinstall)", id, m.Version)
	}

	log.Info("Installing", "package", m.Name, "version", m.Version)

	if err := i.checkRequires(m); err != nil {
		return err
	}
	if err := i.checkCommandConflicts(id, m); err != nil {
		return err
	}
	if err := i.checkIntegrationConflicts(id, m); err != nil {
		return err
	}

	workDir := i.Paths.AppTmp(id)
	srcDir := filepath.Join(workDir, "src")
	pkgDir := filepath.Join(workDir, "pkg")
	cleanup := func() { os.RemoveAll(workDir) }

	cleanup() // remove any stale work dir
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		cleanup()
		return err
	}

	// Tag var/cache and var/tmp as disposable so backup tools skip them.
	markDisposable(i.Paths.Cache())
	markDisposable(i.Paths.Tmp())

	prepareVars := map[string]string{
		"id":      id,
		"version": m.Version,
		"src":     srcDir,
		"pkg":     pkgDir,
		"work":    workDir,
	}

	if err := i.fetchSources(ctx, m, srcDir, prepareVars); err != nil {
		cleanup()
		return err
	}

	if err := i.runPrepare(ctx, m, srcDir, pkgDir, prepareVars); err != nil {
		cleanup()
		return err
	}

	placed, err := i.place(id, pkgDir, force)
	if err != nil {
		cleanup()
		return err
	}

	// Shims are how the user actually launches the app — failure here is a
	// hard install failure, not a soft-warn. Without a shim, state would
	// claim the package is installed but `bunny run <id>` and direct shim
	// invocation would silently break.
	if becomeActive {
		if err := i.installShims(m); err != nil {
			*i.State = *stateBefore
			i.rollbackInstall(placed, replacedCommands, newCommands)
			cleanup()
			return fmt.Errorf("install shims: %w", err)
		}
	}

	integrationChanged := false
	if err := i.replaceDesktopIntegration(oldManifest, m, id); err != nil {
		log.Warn("Desktop integration partial failure", "package", id, "error", err)
	} else {
		integrationChanged = true
	}

	// The cached manifest is part of the runtime contract, not optional cache:
	// stage it before state so an installed package is always runnable offline.
	if err := writeManifestCache(i.Paths.ManifestFile(id), m); err != nil {
		*i.State = *stateBefore
		if integrationChanged {
			i.restoreDesktopIntegration(oldManifest, m, id)
		}
		i.rollbackInstall(placed, replacedCommands, newCommands)
		cleanup()
		return fmt.Errorf("cache installed manifest: %w", err)
	}
	if err := shim.Remove(i.Paths.Bin(), shim.Difference(replacedCommands, newCommands)); err != nil {
		*i.State = *stateBefore
		if restoreErr := cacheBefore.Restore(i.Paths.ManifestFile(id)); restoreErr != nil {
			log.Warn("Failed to restore installed manifest", "package", id, "error", restoreErr)
		}
		if integrationChanged {
			i.restoreDesktopIntegration(oldManifest, m, id)
		}
		i.rollbackInstall(placed, replacedCommands, newCommands)
		cleanup()
		return fmt.Errorf("remove stale shims: %w", err)
	}

	i.State.SetInstalled(id, m.Version, m.Provides)
	switch {
	case m.Provides != "" && becomeActive:
		if err := i.State.SetProviderCommands(m.Provides, id, newCommands); err != nil {
			*i.State = *stateBefore
			if restoreErr := cacheBefore.Restore(i.Paths.ManifestFile(id)); restoreErr != nil {
				log.Warn("Failed to restore installed manifest", "package", id, "error", restoreErr)
			}
			if integrationChanged {
				i.restoreDesktopIntegration(oldManifest, m, id)
			}
			i.rollbackInstall(placed, replacedCommands, newCommands)
			cleanup()
			return fmt.Errorf("activate provider commands: %w", err)
		}
	case m.Provides == "":
		i.State.SetCommands(id, newCommands)
	}
	// m.Provides != "" && !becomeActive: installed as an inactive provider —
	// no shims, no owned commands, active provider untouched.
	if err := i.saveState(); err != nil {
		*i.State = *stateBefore
		if err := cacheBefore.Restore(i.Paths.ManifestFile(id)); err != nil {
			log.Warn("Failed to restore installed manifest", "package", id, "error", err)
		}
		if integrationChanged {
			i.restoreDesktopIntegration(oldManifest, m, id)
		}
		i.rollbackInstall(placed, replacedCommands, newCommands)
		cleanup()
		return fmt.Errorf("save state: %w", err)
	}
	if err := placed.Commit(); err != nil {
		log.Warn("Failed to remove previous install backup", "package", id, "error", err)
	}

	cleanup()
	log.Info("Installed", "package", m.Name, "version", m.Version)
	return nil
}

type fileSnapshot struct {
	data   []byte
	mode   os.FileMode
	exists bool
}

func snapshotFile(path string) (fileSnapshot, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{data: data, mode: info.Mode().Perm(), exists: true}, nil
}

func (s fileSnapshot) Restore(path string) error {
	if !s.exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return fsutil.WriteFile(path, s.data, s.mode)
}

func writeManifestCache(path string, m *manifest.Manifest) error {
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return fsutil.WriteFile(path, data, 0644)
}

// loadInstalledManifest returns the manifest exactly as it was at install
// time by reading the per-app cache, falling back to the live catalog only
// when the cache is missing. Mirrors Manager.loadInstalledManifest so that
// uninstall sees the same view as the runtime paths.
func (i *Installer) loadInstalledManifest(id string) (*manifest.Manifest, error) {
	return i.Installed.Load(id)
}

// Uninstall removes a package. purge=true also removes the per-app data dir.
func (i *Installer) Uninstall(id string, purge bool) error {
	if !i.State.IsInstalled(id) {
		return fmt.Errorf("package %q is not installed", id)
	}
	if err := i.checkReverseDependencies(id); err != nil {
		return err
	}
	log.Info("Uninstalling", "package", id)
	stateBefore := i.State.Clone()

	// Best-effort manifest load: prefer the install-time cache so we clean up
	// exactly what was installed (icons, .desktop entries, completions) even
	// if the catalog manifest has since changed shape or vanished. Fall back
	// to the catalog only if the cache is missing — and if both are gone,
	// proceed with whatever state has.
	installedManifest, manifestErr := i.loadInstalledManifest(id)
	if manifestErr != nil && !errors.Is(manifestErr, catalog.ErrNotFound) {
		return fmt.Errorf("load installed manifest: %w", manifestErr)
	}
	manifest := installedManifest
	names := i.State.CommandsOwnedBy(id)
	removed, err := i.stageRemoveApp(id)
	if err != nil {
		return err
	}
	if manifest != nil {
		names = appendMissing(names, binNames(manifest)...)
	}
	if err := shim.Remove(i.Paths.Bin(), names); err != nil {
		_ = removed.Rollback()
		return fmt.Errorf("remove shims: %w", err)
	}
	if err := i.removeDesktopIntegration(manifest, id); err != nil {
		i.rollbackUninstall(removed, manifest, id, names, nil)
		return fmt.Errorf("remove desktop integration: %w", err)
	}

	// The capability comes from state, not the removed package's manifest:
	// state always knows what the package provided even when its cached manifest
	// is missing/corrupt. SetUninstalled records a fallback provider from state
	// too, so gating the shim/command wiring on the manifest would leave the
	// fallback recorded but unwired (no commands, no shim).
	capability := i.State.Packages[id].Provides
	i.State.SetUninstalled(id)
	var fallbackNames []string
	if capability != "" {
		if fallbackID := i.State.ResolveProvider(capability); fallbackID != "" {
			fallback, err := i.loadInstalledManifest(fallbackID)
			if err != nil {
				*i.State = *stateBefore
				i.rollbackUninstall(removed, manifest, id, names, fallbackNames)
				return fmt.Errorf("load fallback provider %s: %w", fallbackID, err)
			}
			fallbackNames = binNames(fallback)
			if err := i.installShims(fallback); err != nil {
				*i.State = *stateBefore
				i.rollbackUninstall(removed, manifest, id, names, fallbackNames)
				return fmt.Errorf("restore fallback provider %s shims: %w", fallbackID, err)
			}
			if err := i.State.SetProviderCommands(capability, fallbackID, fallbackNames); err != nil {
				*i.State = *stateBefore
				i.rollbackUninstall(removed, manifest, id, names, fallbackNames)
				return fmt.Errorf("activate fallback provider %s: %w", fallbackID, err)
			}
		}
	}
	if err := i.saveState(); err != nil {
		*i.State = *stateBefore
		i.rollbackUninstall(removed, manifest, id, names, fallbackNames)
		return fmt.Errorf("save state: %w", err)
	}
	var cleanupErrs []error
	if err := removed.Commit(); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove staged app directory: %w", err))
	}
	// Drop the manifest cache so a future Manager.loadInstalledManifest doesn't
	// keep returning a stale view of an uninstalled package. Best effort —
	// state.IsInstalled gates the runtime paths anyway.
	if err := os.Remove(i.Paths.ManifestFile(id)); err != nil && !os.IsNotExist(err) {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove cached manifest: %w", err))
	}
	if purge {
		if err := os.RemoveAll(i.Paths.AppData(id)); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("remove app data: %w", err))
		}
	}

	log.Info("Uninstalled", "package", id)
	if err := errors.Join(cleanupErrs...); err != nil {
		return fmt.Errorf("%s was uninstalled, but cleanup was incomplete: %w", id, err)
	}
	return nil
}

func (i *Installer) checkReverseDependencies(id string) error {
	future := i.State.Clone()
	future.SetUninstalled(id)
	var dependents []string
	for _, otherID := range i.State.Installed() {
		if otherID == id {
			continue
		}
		m, err := i.loadInstalledManifest(otherID)
		if err != nil {
			return fmt.Errorf("load %s while checking reverse dependencies: %w", otherID, err)
		}
		for _, req := range m.Requires {
			capability, minMajor, hasMin := manifest.ParseRequirement(req)
			satisfied := future.IsSatisfied(req)
			if hasMin {
				satisfied = future.ResolveProviderMin(capability, minMajor) != ""
			}
			if !satisfied {
				dependents = append(dependents, fmt.Sprintf("%s requires %s", otherID, req))
				break
			}
		}
	}
	if len(dependents) > 0 {
		return fmt.Errorf("cannot uninstall %q: %s", id, strings.Join(dependents, "; "))
	}
	return nil
}

// --- internal steps ---

func (i *Installer) alreadyInstalledSame(id, version string) bool {
	if !i.State.IsInstalled(id) {
		return false
	}
	return i.State.Packages[id].Version == version
}

func (i *Installer) saveState() error {
	if i.SaveState != nil {
		return i.SaveState(i.State, i.Paths.StateFile())
	}
	return i.State.Save(i.Paths.StateFile())
}

func (i *Installer) checkRequires(m *manifest.Manifest) error {
	for _, req := range m.Requires {
		capability, minMajor, hasMin := manifest.ParseRequirement(req)
		if hasMin {
			if i.State.ResolveProviderMin(capability, minMajor) == "" {
				return fmt.Errorf("%s requires %q which is not installed", m.ID, req)
			}
		} else {
			if !i.State.IsSatisfied(req) {
				return fmt.Errorf("%s requires %q which is not installed", m.ID, req)
			}
		}
	}
	return nil
}

// checkCommandConflicts rejects installs where a binary name is already owned
// by an unrelated package. Two packages that share a `provides:` capability
// (e.g. node-22 and node-24) are allowed to share command names.
func (i *Installer) checkCommandConflicts(id string, m *manifest.Manifest) error {
	for _, bin := range m.Bin {
		owner, ok := i.State.CommandOwner(bin.Name)
		if !ok || owner == id {
			continue
		}
		ownerInfo := i.State.Packages[owner]
		if m.Provides == "" || ownerInfo.Provides != m.Provides {
			return fmt.Errorf("command %q is already provided by %q", bin.Name, owner)
		}
	}
	return nil
}

func (i *Installer) checkIntegrationConflicts(id string, m *manifest.Manifest) error {
	desired := integrationDestinations(m, i.Paths.Vars(id, m.Version))
	for _, otherID := range i.State.Installed() {
		if otherID == id {
			continue
		}
		other, err := i.loadInstalledManifest(otherID)
		if err != nil {
			return fmt.Errorf("load %s while checking integration ownership: %w", otherID, err)
		}
		for destination := range integrationDestinations(other, i.Paths.Vars(otherID, other.Version)) {
			if desired[destination] {
				return fmt.Errorf("integration path %q is already owned by %q", destination, otherID)
			}
		}
	}
	return nil
}

func integrationDestinations(m *manifest.Manifest, vars map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, entry := range m.Desktop {
		out["desktop/"+entry.ID] = true
	}
	for _, icon := range m.Icons {
		size := icon.Size
		if size == "" {
			size = "128x128"
		}
		ext := filepath.Ext(manifest.Expand(icon.Src, vars))
		out["icon/"+size+"/"+icon.Name+ext] = true
	}
	if m.Completions != nil {
		for shell, source := range map[string]string{
			"bash": m.Completions.Bash,
			"zsh":  m.Completions.Zsh,
			"fish": m.Completions.Fish,
		} {
			if source != "" {
				out["completion/"+shell+"/"+filepath.Base(manifest.Expand(source, vars))] = true
			}
		}
	}
	return out
}

func (i *Installer) fetchSources(ctx context.Context, m *manifest.Manifest, srcDir string, vars map[string]string) error {
	dlSources := make([]Source, 0, len(m.Sources))
	for _, s := range m.Sources {
		dlSources = append(dlSources, Source{
			Name:   manifest.Expand(s.Name, vars),
			URL:    manifest.Expand(s.URL, vars),
			File:   manifest.Expand(s.File, vars),
			SHA256: s.SHA256,
			SHA512: s.SHA512,
			Size:   s.Size,
		})
	}
	cacheDir := i.Paths.AppDownloadCache(m.ID)
	paths, err := i.Download.FetchAllContext(ctx, cacheDir, dlSources)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	// Hard-link or copy each archive into srcDir so prepare can find it locally.
	for j, p := range paths {
		dst := filepath.Join(srcDir, filepath.Base(p))
		if dlSources[j].File != "" {
			dst = filepath.Join(srcDir, dlSources[j].File)
		}
		os.Remove(dst)
		if err := os.Link(p, dst); err != nil {
			// Fall back to copy if hard-link fails (e.g. cross-device).
			if cerr := copyFile(p, dst); cerr != nil {
				return fmt.Errorf("stage source %s: %w", filepath.Base(p), cerr)
			}
		}
	}
	return nil
}

func (i *Installer) runPrepare(ctx context.Context, m *manifest.Manifest, srcDir, pkgDir string, vars map[string]string) error {
	if len(m.Prepare) == 0 {
		return nil
	}
	return i.Prepare(ctx, srcDir, pkgDir, m.Prepare, vars)
}

// placement tracks a staged app-dir swap until state has been saved.
type placement struct {
	finalDir    string
	backupDir   string
	hadExisting bool
}

func (p *placement) Commit() error {
	if !p.hadExisting {
		return nil
	}
	return os.RemoveAll(p.backupDir)
}

func (p *placement) Rollback() error {
	if p.hadExisting {
		if err := os.RemoveAll(p.finalDir); err != nil {
			return err
		}
		return os.Rename(p.backupDir, p.finalDir)
	}
	return os.RemoveAll(p.finalDir)
}

// place atomically swaps pkgDir into Paths.AppDir(id). For non-force installs
// an existing target is an error. For force installs the existing dir is
// renamed aside and kept there until the caller commits after state save. If
// the swap itself fails, the previous install is restored before returning.
func (i *Installer) place(id, pkgDir string, force bool) (*placement, error) {
	finalDir := i.Paths.AppDir(id)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(finalDir); err == nil {
		if !force {
			return nil, fmt.Errorf("%s already exists (use --force to reinstall)", finalDir)
		}
		backup := finalDir + ".old"
		os.RemoveAll(backup)
		if err := os.Rename(finalDir, backup); err != nil {
			return nil, fmt.Errorf("backup existing install: %w", err)
		}
		if err := os.Rename(pkgDir, finalDir); err != nil {
			// Restore the backup so the user isn't left with no install at all.
			if rerr := os.Rename(backup, finalDir); rerr != nil {
				log.Error("Failed to restore previous install", "error", rerr, "backup", backup)
			}
			return nil, fmt.Errorf("place app dir: %w", err)
		}
		return &placement{finalDir: finalDir, backupDir: backup, hadExisting: true}, nil
	}
	if err := os.Rename(pkgDir, finalDir); err != nil {
		return nil, fmt.Errorf("place app dir: %w", err)
	}
	return &placement{finalDir: finalDir}, nil
}

type removalPlacement struct {
	finalDir string
	trashDir string
	existed  bool
}

func (p *removalPlacement) Commit() error {
	if !p.existed {
		return nil
	}
	return os.RemoveAll(p.trashDir)
}

func (p *removalPlacement) Rollback() error {
	if !p.existed {
		return nil
	}
	if _, err := os.Stat(p.finalDir); err == nil {
		if err := os.RemoveAll(p.finalDir); err != nil {
			return err
		}
	}
	return os.Rename(p.trashDir, p.finalDir)
}

func (i *Installer) stageRemoveApp(id string) (*removalPlacement, error) {
	finalDir := i.Paths.AppDir(id)
	trashDir := finalDir + ".delete"
	if _, err := os.Stat(finalDir); os.IsNotExist(err) {
		return &removalPlacement{finalDir: finalDir, trashDir: trashDir}, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat app dir: %w", err)
	}
	os.RemoveAll(trashDir)
	if err := os.Rename(finalDir, trashDir); err != nil {
		return nil, fmt.Errorf("stage app removal: %w", err)
	}
	return &removalPlacement{finalDir: finalDir, trashDir: trashDir, existed: true}, nil
}

func (i *Installer) installShims(m *manifest.Manifest) error {
	bunnyPath, err := i.BunnyPath(i.Paths.Bin())
	if err != nil {
		return fmt.Errorf("locate bunny binary: %w", err)
	}
	return shim.Install(i.Paths.Bin(), binNames(m), bunnyPath)
}

func (i *Installer) installDesktopIntegration(m *manifest.Manifest, id string) error {
	finalVars := i.Paths.Vars(id, m.Version)
	if err := desktop.InstallEntries(i.Paths, m.Desktop, finalVars); err != nil {
		return err
	}
	if err := desktop.InstallIcons(i.Paths, m.Icons, finalVars); err != nil {
		return err
	}
	if err := desktop.InstallCompletions(i.Paths, m.Completions, finalVars); err != nil {
		return err
	}
	if len(m.Icons) > 0 {
		desktop.RefreshIconCache(i.Paths) // so new icons show without a re-login
	}
	return nil
}

func (i *Installer) removeDesktopIntegration(m *manifest.Manifest, id string) error {
	if m == nil {
		return nil
	}
	var errs []error
	if err := desktop.RemoveEntries(i.Paths, m.Desktop); err != nil {
		errs = append(errs, err)
	}
	if err := desktop.RemoveIcons(i.Paths, m.Icons); err != nil {
		errs = append(errs, err)
	}
	vars := i.Paths.Vars(id, m.Version)
	if err := desktop.RemoveCompletions(i.Paths, m.Completions, vars); err != nil {
		errs = append(errs, err)
	}
	if len(m.Icons) > 0 {
		desktop.RefreshIconCache(i.Paths) // drop removed icons from the theme cache
	}
	return errors.Join(errs...)
}

func (i *Installer) replaceDesktopIntegration(old, next *manifest.Manifest, id string) error {
	if err := i.removeDesktopIntegration(old, id); err != nil {
		if old != nil {
			_ = i.installDesktopIntegration(old, id)
		}
		return fmt.Errorf("remove previous integration: %w", err)
	}
	if err := i.installDesktopIntegration(next, id); err != nil {
		removeErr := i.removeDesktopIntegration(next, id)
		errs := []error{fmt.Errorf("install new integration: %w", err)}
		if removeErr != nil {
			errs = append(errs, fmt.Errorf("clean partial integration: %w", removeErr))
		}
		if old != nil {
			if restoreErr := i.installDesktopIntegration(old, id); restoreErr != nil {
				errs = append(errs, fmt.Errorf("restore previous integration: %w", restoreErr))
			}
		}
		return errors.Join(errs...)
	}
	return nil
}

func (i *Installer) restoreDesktopIntegration(old, next *manifest.Manifest, id string) {
	if err := i.removeDesktopIntegration(next, id); err != nil {
		log.Warn("Failed to remove new desktop integration during rollback", "package", id, "error", err)
	}
	if old != nil {
		if err := i.installDesktopIntegration(old, id); err != nil {
			log.Warn("Failed to restore previous desktop integration", "package", id, "error", err)
		}
	}
}

func (i *Installer) rollbackInstall(p *placement, oldCommands, newCommands []string) {
	if err := p.Rollback(); err != nil {
		log.Warn("Failed to roll back app directory", "path", p.finalDir, "error", err)
	}
	if err := shim.Remove(i.Paths.Bin(), shim.Difference(newCommands, oldCommands)); err != nil {
		log.Warn("Failed to remove new shims during rollback", "error", err)
	}
	if len(oldCommands) == 0 {
		return
	}
	bunnyPath, err := i.BunnyPath(i.Paths.Bin())
	if err != nil {
		log.Warn("Failed to locate bunny binary while restoring shims", "error", err)
		return
	}
	if err := shim.Install(i.Paths.Bin(), oldCommands, bunnyPath); err != nil {
		log.Warn("Failed to restore previous shims", "error", err)
	}
}

func (i *Installer) rollbackUninstall(p *removalPlacement, m *manifest.Manifest, id string, names, fallbackNames []string) {
	if err := p.Rollback(); err != nil {
		log.Warn("Failed to roll back app removal", "path", p.finalDir, "error", err)
	}
	if err := shim.Remove(i.Paths.Bin(), shim.Difference(fallbackNames, names)); err != nil {
		log.Warn("Failed to remove fallback shims during rollback", "error", err)
	}
	if len(names) > 0 {
		bunnyPath, err := i.BunnyPath(i.Paths.Bin())
		if err != nil {
			log.Warn("Failed to locate bunny binary while restoring shims", "error", err)
		} else if err := shim.Install(i.Paths.Bin(), names, bunnyPath); err != nil {
			log.Warn("Failed to restore previous shims", "error", err)
		}
	}
	if m != nil {
		if err := i.installDesktopIntegration(m, id); err != nil {
			log.Warn("Failed to restore desktop integration", "package", id, "error", err)
		}
	}
}

func binNames(m *manifest.Manifest) []string {
	names := make([]string, 0, len(m.Bin))
	for _, b := range m.Bin {
		names = append(names, b.Name)
	}
	return names
}

func appendMissing(names []string, additions ...string) []string {
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	for _, name := range additions {
		if seen[name] {
			continue
		}
		names = append(names, name)
		seen[name] = true
	}
	return names
}
