package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/charmbracelet/log"
	"gopkg.in/yaml.v3"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/checker"
	"github.com/cristatus/bunny/internal/installer"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/progress"
	"github.com/cristatus/bunny/internal/reshim"
	"github.com/cristatus/bunny/internal/runtime"
	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/state"
	"github.com/cristatus/bunny/internal/suggest"
	"github.com/cristatus/bunny/internal/ui"
)

// App is the orchestration root the CLI handlers call into. Holds the
// resolved $BUNNY_HOME paths, the active catalog, the on-disk state, and a
// pre-wired installer.
type App struct {
	Context    context.Context
	Paths      *paths.Paths
	State      *state.State
	Catalog    catalog.Loader
	Installed  catalog.Loader
	Installer  *installer.Installer
	NoProgress bool   // force plain (final-line-only) progress output
	Pager      string // auto, always, or never for pageable result commands

	local  *catalog.Local
	remote *catalog.Remote
}

func (a *App) pageOutput(output string) error {
	return ui.Page(os.Stdout, output, a.Pager)
}

// reporter returns the progress Reporter for install/uninstall/update: a plain
// final-line-only reporter when --no-progress is set, otherwise the TTY-aware
// one. Progress always goes to stderr, keeping stdout results pipe-clean.
func (a *App) reporter() progress.Reporter {
	if a.NoProgress {
		return progress.NewPlain(os.Stderr)
	}
	return progress.New(os.Stderr)
}

// reporterHook adapts a per-package progress.Reporter to the installer's
// ProgressHook so the installer can drive phase and download updates for the
// package currently being installed.
type reporterHook struct {
	rep progress.Reporter
	pkg string
}

func (h reporterHook) Phase(name string)          { h.rep.Phase(h.pkg, name) }
func (h reporterHook) Download(done, total int64) { h.rep.Download(h.pkg, done, total) }

// userConfig is the on-disk shape of $BUNNY_HOME/config.yaml.
type userConfig struct {
	Catalog struct {
		Remote string `yaml:"remote,omitempty"`
	} `yaml:"catalog,omitempty"`
}

// New constructs an App from $BUNNY_HOME, with the catalog wired
// local→remote and state loaded from disk.
func New() (*App, error) {
	p, err := paths.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}
	st, err := state.Load(p.StateFile())
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	cfg, err := loadUserConfig(p.UserConfigFile())
	if err != nil {
		return nil, fmt.Errorf("load user config: %w", err)
	}

	local := catalog.NewLocal(p.Catalog())
	remote := catalog.NewRemote(cfg.Catalog.Remote, p.Cache())

	var cat catalog.Loader
	if local.Exists() {
		cat = catalog.NewComposite(local, remote)
	} else {
		cat = remote
	}

	installed := catalog.NewInstalled(cat, p.ManifestFile)
	return &App{
		Context:   context.Background(),
		Paths:     p,
		State:     st,
		Catalog:   cat,
		Installed: installed,
		Installer: installer.New(p, cat, st),
		local:     local,
		remote:    remote,
	}, nil
}

func (a *App) context() context.Context {
	if a.Context != nil {
		return a.Context
	}
	return context.Background()
}

// withMutation serializes all filesystem/state mutations across Bunny
// processes. State is reloaded only after the lock is held so a process never
// commits changes based on a stale snapshot.
func (a *App) withMutation(ctx context.Context, fn func() error) error {
	lock, err := state.AcquireLock(ctx, a.Paths.MutationLock())
	if err != nil {
		return err
	}
	defer lock.Close()

	st, err := state.Load(a.Paths.StateFile())
	if err != nil {
		return fmt.Errorf("reload state after locking: %w", err)
	}
	a.State = st
	if a.Installer != nil {
		a.Installer.State = st
	}
	return fn()
}

func loadUserConfig(path string) (*userConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &userConfig{}, nil
		}
		return nil, err
	}
	cfg := &userConfig{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return cfg, nil // empty or comment-only config is valid
		}
		return nil, fmt.Errorf("parse user config: %w", err)
	}
	// A trailing "---" is not a second document; reject only a real one.
	for {
		var extra any
		err := dec.Decode(&extra)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse user config trailer: %w", err)
		}
		if extra != nil {
			return nil, fmt.Errorf("parse user config: multiple YAML documents are not allowed")
		}
	}
	return cfg, nil
}

// loadInstalledManifest reads the per-package manifest cache the installer
// drops at install time, falling back to the live catalog if the cache is
// missing. The cache lets `bunny run` work offline.
func (a *App) loadInstalledManifest(id string) (*manifest.Manifest, error) {
	return a.installedCatalog().Load(id)
}

func (a *App) installedCatalog() catalog.Loader {
	if a.Installed == nil {
		a.Installed = catalog.NewInstalled(a.Catalog, a.Paths.ManifestFile)
	}
	return a.Installed
}

// run executes a package's binary. command="" runs the first one. Used by
// both `bunny run` and the shim dispatch path.
func (a *App) run(id, command string, args []string) error {
	if !a.State.IsInstalled(id) {
		return fmt.Errorf("package %q is not installed", id)
	}
	m, err := a.loadInstalledManifest(id)
	if err != nil {
		return err
	}
	prep, err := runtime.Prepare(a.Paths, a.installedCatalog(), a.State, m, command, args)
	if err != nil {
		return err
	}
	return runtime.Exec(prep)
}

// RunShim dispatches a shim invocation (argv[0] = "node", "code", ...) to
// the owning package, applying any `.bunny-version` pin along the way.
func (a *App) RunShim(name string, args []string) error {
	// SDK command shims (manifest bin:) take precedence and resolve per-project.
	if _, ok := a.State.CommandOwner(name); ok {
		r := &shim.Resolver{State: a.State, Catalog: a.installedCatalog()}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		resolved, err := r.Resolve(name, cwd)
		if err != nil {
			return err
		}
		log.Debug("Shim dispatch", "name", name, "package", resolved.PackageID, "via", resolved.Source)
		return a.run(resolved.PackageID, name, args)
	}
	// Runtime-installed global executables (npm -g, etc.).
	if _, ok := a.State.GlobalCommandCapability(name); ok {
		log.Debug("Global shim dispatch", "name", name)
		return a.runGlobal(name, args)
	}
	return fmt.Errorf("no installed package provides %q", name)
}

// findGlobalExe locates a runtime-installed executable `name` in provider m's
// expanded global-bins dirs. Returns an error naming the provider if absent.
func (a *App) findGlobalExe(m *manifest.Manifest, providerID, name string) (string, error) {
	vars := a.Paths.Vars(providerID, m.Version)
	for _, gb := range m.GlobalBins {
		dir := manifest.Expand(gb, vars)
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s is not installed for %s\nhint: install it (e.g. npm -g install %s) then run: bunny reshim", name, providerID, name)
}

// resolveCapabilityProvider returns the package id that should run a global
// tool for capability cap: the .bunny-version-pinned version if present
// (erroring if pinned-but-not-installed), else the active provider.
func (a *App) resolveCapabilityProvider(capability, cwd string) (string, error) {
	pinned, err := shim.ResolveProjectVersion(cwd, capability)
	if err != nil {
		return "", fmt.Errorf("resolve project version for %s: %w", capability, err)
	}
	if pinned != nil {
		candidate := capability + "-" + pinned.Version
		if a.State.IsInstalled(candidate) {
			return candidate, nil
		}
		return "", fmt.Errorf("%s %s pinned in %s, but %s is not installed\nhint: bunny install %s",
			capability, pinned.Version, pinned.Source, candidate, candidate)
	}
	if active := a.State.ResolveProvider(capability); active != "" {
		return active, nil
	}
	return "", fmt.Errorf("no active provider for capability %q (run: bunny use <pkg>)", capability)
}

// runGlobal dispatches a global-tool shim invocation: resolve the provider for
// the tool's capability, locate the executable under that version, exec it.
func (a *App) runGlobal(name string, args []string) error {
	capability, _ := a.State.GlobalCommandCapability(name)
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	providerID, err := a.resolveCapabilityProvider(capability, cwd)
	if err != nil {
		return err
	}
	m, err := a.loadInstalledManifest(providerID)
	if err != nil {
		return fmt.Errorf("load provider manifest: %w", err)
	}
	exe, err := a.findGlobalExe(m, providerID, name)
	if err != nil {
		return err
	}
	prep, err := runtime.PrepareGlobal(a.Paths, a.installedCatalog(), a.State, m, exe, args)
	if err != nil {
		return err
	}
	return runtime.Exec(prep)
}

// refreshRemote tries to update the on-disk index. Failures are debug-logged
// and ignored — falling back to whatever's cached is preferable to bubbling
// network errors out of routine read paths.
func (a *App) refreshRemote() {
	if a.remote == nil {
		return
	}
	if err := a.remote.Refresh(); err != nil {
		log.Debug("Remote catalog refresh failed; using cached index", "error", err)
	}
}

// UpdateReport distinguishes "no updates" from "updates could not be checked".
type UpdateReport struct {
	Results  []checker.Result
	Failures []error
}

func (r *UpdateReport) Err() error {
	if len(r.Failures) == 0 {
		return nil
	}
	return fmt.Errorf("%d update check(s) failed: %w", len(r.Failures), errors.Join(r.Failures...))
}

// checkUpdates compares installed packages against the catalog. id="" (the
// default) checks every installed package; a non-empty id checks just that one.
func (a *App) checkUpdates(ctx context.Context, id string) (*UpdateReport, error) {
	status := progress.NewStatus(os.Stderr)
	defer status.Clear()

	status.Update("refreshing catalog…")
	a.refreshRemote()
	pkgs, err := a.Catalog.List()
	if err != nil {
		return nil, err
	}
	if id != "" {
		if err := requireInCatalog(id, pkgs); err != nil {
			return nil, err
		}
	}

	// Compare each installed package's version against the catalog's. The
	// catalog is the source of truth for available versions — kept current by
	// `bunny dev update`, which is what actually queries upstream sources. This
	// command never hits a package's source; it's a fast local comparison.
	report := &UpdateReport{}
	for _, p := range pkgs {
		if id != "" && p.ID != id {
			continue
		}
		installed, ok := a.State.Packages[p.ID]
		if !ok {
			continue // not installed → nothing to update
		}
		if installed.Version != p.Version {
			report.Results = append(report.Results, checker.Result{
				ID:             p.ID,
				CurrentVersion: installed.Version,
				LatestVersion:  p.Version,
				HasUpdate:      true,
			})
		}
	}
	return report, nil
}

// reshimCapabilities rebuilds global-tool shims. capability=="" covers every
// installed provider that declares global-bins; otherwise only that capability.
// It creates/removes shims in $BUNNY_HOME/bin, updates state.GlobalCommands,
// and saves state. Returns the command names added and removed.
func (a *App) reshimCapabilities(capability string) (added, removed []string, err error) {
	var providers []reshim.Provider
	for _, id := range a.State.Installed() {
		m, err := a.loadInstalledManifest(id)
		if err != nil {
			return nil, nil, fmt.Errorf("load installed manifest %s: %w", id, err)
		}
		if len(m.GlobalBins) == 0 {
			continue
		}
		cap := m.Provides
		if cap == "" {
			cap = id
		}
		if capability != "" && cap != capability {
			continue
		}
		vars := a.Paths.Vars(id, m.Version)
		var tools []string
		for _, gb := range m.GlobalBins {
			names, derr := reshim.Executables(manifest.Expand(gb, vars))
			if derr != nil {
				return nil, nil, fmt.Errorf("scan %s global-bins: %w", id, derr)
			}
			tools = append(tools, names...)
		}
		providers = append(providers, reshim.Provider{Capability: cap, Tools: tools})
	}

	protected := map[string]bool{}
	protected[shim.ReservedName] = true
	for name := range a.State.Commands {
		protected[name] = true
	}
	current := map[string]string{}
	for _, name := range a.State.GlobalCommandNames() {
		c, _ := a.State.GlobalCommandCapability(name)
		if capability == "" || c == capability {
			current[name] = c
		}
	}

	add, remove, conflicts := reshim.Plan(providers, protected, current)
	for _, c := range conflicts {
		log.Warn("Global command conflict — keeping first", "command", c.Command, "kept", c.KeptCapability, "skipped", c.SkippedCapability)
	}

	bunnyPath, err := shim.BunnyBinaryPath(a.Paths.Bin())
	if err != nil {
		return nil, nil, fmt.Errorf("locate bunny binary: %w", err)
	}
	stateBefore := a.State.Clone()
	addNames := make([]string, 0, len(add))
	for name := range add {
		addNames = append(addNames, name)
	}
	sort.Strings(addNames)
	if err := shim.Install(a.Paths.Bin(), addNames, bunnyPath); err != nil {
		return nil, nil, fmt.Errorf("install global shims: %w", err)
	}
	if err := shim.Remove(a.Paths.Bin(), remove); err != nil {
		rollbackErr := restoreGlobalShims(a.Paths.Bin(), addNames, sortedKeys(current), bunnyPath)
		return nil, nil, errors.Join(fmt.Errorf("remove stale global shims: %w", err), rollbackErr)
	}
	for _, name := range addNames {
		cap := add[name]
		a.State.SetGlobalCommand(name, cap)
		added = append(added, name)
	}
	for _, name := range remove {
		a.State.RemoveGlobalCommand(name)
		removed = append(removed, name)
	}
	sort.Strings(added)
	sort.Strings(removed)
	if err := a.State.Save(a.Paths.StateFile()); err != nil {
		*a.State = *stateBefore
		rollbackErr := restoreGlobalShims(a.Paths.Bin(), addNames, sortedKeys(current), bunnyPath)
		return nil, nil, errors.Join(fmt.Errorf("save state: %w", err), rollbackErr)
	}
	return added, removed, nil
}

func restoreGlobalShims(binDir string, added, previous []string, bunnyPath string) error {
	var errs []error
	if err := shim.Remove(binDir, added); err != nil {
		errs = append(errs, fmt.Errorf("remove new global shims during rollback: %w", err))
	}
	if err := shim.Install(binDir, previous, bunnyPath); err != nil {
		errs = append(errs, fmt.Errorf("restore global shims during rollback: %w", err))
	}
	return errors.Join(errs...)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != shim.ReservedName {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// requireInCatalog validates an explicit id against the catalog list and, on a
// miss, suggests the nearest catalog id (edit distance ≤ 2 or a prefix match).
func requireInCatalog(id string, pkgs []catalog.PackageInfo) error {
	ids := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		if p.ID == id {
			return nil
		}
		ids = append(ids, p.ID)
	}
	msg := fmt.Sprintf("package %q not found in catalog", id)
	if best, ok := suggest.Closest(id, ids); ok {
		msg += fmt.Sprintf(" (did you mean %q?)", best)
	}
	return errors.New(msg)
}
