package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/checker"
	"github.com/cristatus/bunny/internal/config"
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
	Config     *config.Config
	Catalog    catalog.Loader
	Installed  catalog.Loader
	Installer  *installer.Installer
	NoProgress bool // force plain (final-line-only) progress output

	catalogs []catalogEntry
}

// catalogEntry is one configured catalog: the Source the Composite resolves
// through, plus the concrete loader, so doctor can report where it points and
// the dev commands can rewrite a checkout without re-deriving either.
type catalogEntry struct {
	src    catalog.Source
	local  *catalog.Local  // nil for a remote
	remote *catalog.Remote // nil for a checkout
	// present is resolved once, at wiring time: five callers ask, and two
	// answers would let New wire one thing while the rest report another.
	present bool
	// location is where the catalog reads from, for diagnostics.
	location string
}

// listCached lists without touching the network, for shell completion.
func (e catalogEntry) listCached() ([]catalog.PackageInfo, error) {
	if e.local != nil {
		return e.local.List()
	}
	return e.remote.ListCached()
}

// catalogEntries wires every configured catalog, in priority order.
func catalogEntries(cfg *config.Config, p *paths.Paths) []catalogEntry {
	cats := cfg.ResolveCatalogs()
	entries := make([]catalogEntry, 0, len(cats))
	for _, cat := range cats {
		if cat.IsLocal() {
			l := catalog.NewLocal(cat.Local)
			entries = append(entries, catalogEntry{
				src:      catalog.Source{Name: cat.Name, Loader: l},
				local:    l,
				present:  l.Exists(),
				location: l.Root(),
			})
			continue
		}
		r := catalog.NewRemote(cat.Remote, p.CatalogCache(cat.Name))
		entries = append(entries, catalogEntry{
			src:      catalog.Source{Name: cat.Name, Loader: r},
			remote:   r,
			present:  true,
			location: r.URL(),
		})
	}
	return entries
}

// reporter returns the progress Reporter for install/uninstall/update: a plain
// final-line-only reporter when --no-progress is set, otherwise the TTY-aware
// one. Progress always goes to stderr, keeping stdout results pipe-clean.
//
// With diagnostics enabled there is no progress output at all: the log covers
// the same events in more detail on the same stream, and the live reporter
// redraws in place, so the two interleave into an unreadable line.
func (a *App) reporter() progress.Reporter {
	switch {
	case logging():
		return logReporter{}
	case a.NoProgress:
		return progress.NewPlain(os.Stderr)
	}
	return progress.New(os.Stderr)
}

// logReporter is the Reporter used while -l is on. Phase/byte events are
// dropped; per-package outcomes are logged, since a skipped or failed package
// is reported nowhere else and a failing batch exits non-zero without
// printing (see errHandled).
type logReporter struct{}

func (logReporter) Begin(parent context.Context, ids []string) context.Context {
	log.Debug("Batch", "packages", ids)
	return parent
}
func (logReporter) Start(pkg string)                       {}
func (logReporter) Download(pkg string, done, total int64) {}
func (logReporter) Close()                                 {}

func (logReporter) Phase(pkg, name string) { log.Debug("Phase", "package", pkg, "phase", name) }
func (logReporter) Done(pkg, status, version string) {
	log.Debug("Finished", append([]any{"package", pkg, "status", status}, versionKV(version)...)...)
}
func (logReporter) Skip(pkg, status, version string) {
	log.Info("Skipped", append([]any{"package", pkg, "reason", status}, versionKV(version)...)...)
}
func (logReporter) Fail(pkg string, err error) {
	log.Error("Failed", "package", pkg, "error", err)
}

// versionKV drops the version rather than logging version="": removals and
// skips have no version, and an empty field on every such line is noise.
func versionKV(version string) []any {
	if version == "" {
		return nil
	}
	return []any{"version", version}
}

// logging reports whether -l enabled the log channel. main parks the level
// above FatalLevel to disable it.
func logging() bool { return log.GetLevel() <= log.FatalLevel }

// status returns the printer for bunny's narration of what it did: activated a
// provider, wrote a pin, removed a cache file. With -l that narration is the
// log's job, so this discards and the log becomes the only output.
//
// Not for command results. `bunny list`, `search`, `info`, `doctor`, and the
// snippet-printing `init`/`completion` produce the data the user asked for on
// stdout, and `eval "$(bunny init zsh -l debug)"` has to keep working.
func (a *App) status() *ui.Printer {
	if logging() {
		return ui.New(io.Discard)
	}
	return ui.New(os.Stdout)
}

// reporterHook adapts a progress.Reporter to installer.ProgressHook for one package.
type reporterHook struct {
	rep progress.Reporter
	pkg string
}

func (h reporterHook) Phase(name string)          { h.rep.Phase(h.pkg, name) }
func (h reporterHook) Download(done, total int64) { h.rep.Download(h.pkg, done, total) }

// New constructs an App from $BUNNY_HOME, with the configured catalogs wired in
// priority order and state loaded from disk.
func New() (*App, error) {
	p, err := paths.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve paths: %w", err)
	}
	st, err := state.Load(p.StateFile())
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}
	cfg, err := config.Load(p.UserConfigFile())
	if err != nil {
		return nil, fmt.Errorf("load user config: %w", err)
	}

	app := &App{Context: context.Background(), State: st, Config: cfg}

	// Install locations come from config for new installs and from state for
	// existing ones, so editing config never moves a tree that already exists.
	// The lookup reads through the App because withMutation swaps in a freshly
	// loaded state once it holds the lock.
	p = p.WithLayout(cfg.InstallRoots(), func(id string) (string, string) { return app.State.Location(id) })
	app.Paths = p

	app.catalogs = catalogEntries(cfg, p)
	// A checkout that is not on disk is dropped: an empty answer from it would
	// mask a remote that is down.
	var wired []catalog.Source
	for _, e := range app.catalogs {
		if e.present {
			wired = append(wired, e.src)
		}
	}
	cat := catalog.NewComposite(wired...)

	// The first thing worth knowing when an install lands somewhere unexpected:
	// which config was read, which layout resolved, which catalog won. Guarded
	// because New runs on every shim dispatch, so `node --version` shouldn't pay
	// to assemble it.
	if log.GetLevel() <= log.DebugLevel {
		log.Debug("Layout", "xdg", p.XDG(), "config", p.UserConfigFile(),
			"state", p.StateFile(), "manifests", p.Manifests(), "bin", p.Bin(),
			"cache", p.Cache())
		// Per kind, not p.InstallRoots(): that sorts and deduplicates, so a
		// customized root shows up as an unlabelled path with nothing saying
		// which kind sent it there.
		roots := make([]any, 0, len(manifest.Kinds)*2)
		for _, kind := range manifest.Kinds {
			roots = append(roots, kind, p.InstallRoot(kind))
		}
		log.Debug("Install roots", roots...)
		cats := make([]any, 0, len(app.catalogs)*2)
		for _, e := range app.catalogs {
			detail := e.location
			if !e.present {
				detail += " (absent)"
			}
			cats = append(cats, e.src.Name, detail)
		}
		log.Debug("Catalogs", cats...)
	}

	app.Catalog = cat
	app.Installed = catalog.NewInstalled(cat, p.ManifestFile)
	app.Installer = installer.New(p, cat, st)
	app.Installer.Version = version
	return app, nil
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

// launcher wires the runtime with everything a launch consults, including the
// user config that supplies any env beyond what the manifest declares.
func (a *App) launcher() *runtime.Launcher {
	return &runtime.Launcher{
		Paths:   a.Paths,
		Catalog: a.installedCatalog(),
		State:   a.State,
		Config:  a.Config,
	}
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
	return a.runPackage(id, command, args, false)
}

// runSandboxed performs a package-aware forced sandbox launch. Unlike normal
// run/shim dispatch, on-demand and unconfigured packages are sandboxed too.
func (a *App) runSandboxed(id, command string, args []string) error {
	return a.runPackage(id, command, args, true)
}

func (a *App) runPackage(id, command string, args []string, forceSandbox bool) error {
	if !a.State.IsInstalled(id) {
		return fmt.Errorf("package %q is not installed", id)
	}
	m, err := a.loadInstalledManifest(id)
	if err != nil {
		return err
	}
	prep, err := a.launcher().Prepare(m, command, args)
	if err != nil {
		return err
	}
	if forceSandbox {
		return runtime.ExecPackageSandboxed(prep, a.Config)
	}
	return runtime.ExecPackage(prep, a.Config)
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
	prep, err := a.launcher().PrepareGlobal(m, exe, args)
	if err != nil {
		return err
	}
	return runtime.ExecPackage(prep, a.Config)
}

// refreshDeadline caps the wait on catalog refreshes; a fetch that outlives it
// keeps running and warms its own cache for next time.
const refreshDeadline = 5 * time.Second

// refreshRemote updates the on-disk index of every remote catalog at once, since
// sequential refreshes add up one round trip at a time. Failures are
// debug-logged and ignored — falling back to whatever's cached is preferable to
// bubbling network errors out of routine read paths.
func (a *App) refreshRemote() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, e := range a.catalogs {
		if e.remote == nil {
			continue
		}
		wg.Go(func() {
			if err := e.remote.Refresh(); err != nil {
				log.Debug("Remote catalog refresh failed; using cached index",
					"catalog", e.src.Name, "error", err)
			}
		})
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(refreshDeadline):
		log.Debug("Catalog refresh still running; serving what is cached",
			"waited", refreshDeadline)
	}
}

// localCatalog returns the checkout the dev commands rewrite. name selects one
// when several are configured; empty means the only one.
func (a *App) localCatalog(name string) (*catalog.Local, error) {
	var found []catalogEntry
	var names []string
	for _, e := range a.catalogs {
		if e.local == nil {
			continue
		}
		names = append(names, e.src.Name)
		if name == "" || e.src.Name == name {
			found = append(found, e)
		}
	}
	switch {
	case len(found) == 0 && name != "":
		if len(names) == 0 {
			return nil, fmt.Errorf("no catalog checkout is configured, so there is no %q to rewrite", name)
		}
		return nil, fmt.Errorf("no catalog checkout named %q; configured: %s", name, strings.Join(names, ", "))
	case len(found) == 0:
		return nil, fmt.Errorf("no catalog checkout configured; 'bunny dev' rewrites one, so list it under catalogs: in %s", a.Paths.UserConfigFile())
	case len(found) > 1:
		return nil, fmt.Errorf("several catalog checkouts configured (%s); pass --catalog to choose one",
			strings.Join(names, ", "))
	}
	if !found[0].present {
		return nil, fmt.Errorf("no catalog checkout at %s", found[0].location)
	}
	return found[0].local, nil
}

// sourceChangeNote reports a package that arrived from a different catalog than
// it was installed from. A note, not a refusal: layering catalogs is the point.
// A previous catalog no longer configured gets its own wording, since dropping
// the catalog that owned a package is the same handover by another route.
func (a *App) sourceChangeNote(id, before string) string {
	after := a.State.Packages[id].Source
	if before == "" || after == "" || before == after {
		return ""
	}
	if !slices.Contains(a.catalogNames(), before) {
		return fmt.Sprintf("%s now comes from catalog %q; %q is no longer configured", id, after, before)
	}
	return fmt.Sprintf("%s now comes from catalog %q (was %q)", id, after, before)
}

func (a *App) catalogNames() []string {
	names := make([]string, 0, len(a.catalogs))
	for _, e := range a.catalogs {
		names = append(names, e.src.Name)
	}
	return names
}

// printNotes writes post-operation notes, after the live reporter is torn down
// so they land on a clean stdout.
func (a *App) printNotes(notes []string) {
	if len(notes) == 0 {
		return
	}
	p := a.status()
	p.Println()
	for _, note := range notes {
		p.Println(note)
	}
}

// multiCatalog reports whether more than one catalog can actually serve a
// package — naming one only informs then. Usable, not configured: a checkout
// listed in config but never cloned answers nothing.
func (a *App) multiCatalog() bool {
	usable := 0
	for _, e := range a.catalogs {
		if e.present {
			usable++
		}
	}
	return usable > 1
}

// packageSource names the catalog a package came from: the one recorded at
// install time, else the one it resolves to now.
func (a *App) packageSource(id string, resolved string) string {
	if pkg, ok := a.State.Packages[id]; ok && pkg.Source != "" {
		return pkg.Source
	}
	return resolved
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

	// The catalog is the source of truth for available versions — kept current
	// by `bunny dev update`, which queries upstream. This command never hits a
	// package's source: it's a fast local comparison.
	report := &UpdateReport{}
	for _, p := range pkgs {
		if id != "" && p.ID != id {
			continue
		}
		installed, ok := a.State.Packages[p.ID]
		if !ok {
			continue
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
	if err := shim.Remove(a.Paths.Bin(), remove, bunnyPath); err != nil {
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
	if err := shim.Remove(binDir, added, bunnyPath); err != nil {
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
