// Package paths is the single source of truth for "where does X live".
// Every other package consumes it; nothing here touches the filesystem.
//
// Bunny lives under one root, $BUNNY_HOME or ~/.bunny. The variable only
// moves the root; the layout under it is fixed.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/cristatus/bunny/internal/manifest"
)

// EnvHome is the environment variable that moves bunny's root. Its value must
// be an absolute path.
const EnvHome = "BUNNY_HOME"

// DefaultRootName is the root's name under the user's home when $BUNNY_HOME
// is unset.
const DefaultRootName = ".bunny"

// Paths centralizes every directory bunny reads or writes, all under Root.
type Paths struct {
	Root string

	// kindOf reports an installed package's kind, backed by state.
	kindOf func(id string) string
}

// WithKinds returns a copy of p that finds installed packages through the
// kind state recorded for each, which is what decides their install root.
func (p *Paths) WithKinds(kindOf func(id string) string) *Paths {
	clone := *p
	clone.kindOf = kindOf
	return &clone
}

// Resolve returns the active Paths: $BUNNY_HOME when set, else ~/.bunny. No
// directory is created here; callers do that lazily.
//
// A relative $BUNNY_HOME or HOME is an error rather than a path resolved
// against the working directory, which would move the whole install as the
// user cd'd around and leave shims resolving to a different one per directory.
func Resolve() (*Paths, error) {
	if root := os.Getenv(EnvHome); root != "" {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("%s must be an absolute path, got %q", EnvHome, root)
		}
		return At(filepath.Clean(root)), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("HOME must be an absolute path, got %q", home)
	}
	return At(filepath.Join(home, DefaultRootName)), nil
}

// DefaultRoot reports whether p is the root bunny resolves without
// $BUNNY_HOME, so the shell setup need not pin it.
func (p *Paths) DefaultRoot() bool {
	home, err := os.UserHomeDir()
	return err == nil && p.Root == filepath.Join(home, DefaultRootName)
}

// RuntimeEnv anchors the root for a launched package. A sandbox may redirect
// HOME, and a shim re-entering bunny from inside must still find this root.
func (p *Paths) RuntimeEnv() []string {
	return []string{EnvHome + "=" + p.Root}
}

// At builds Paths rooted at root.
func At(root string) *Paths { return &Paths{Root: root} }

// --- Top-level dirs ---

func (p *Paths) Bin() string   { return filepath.Join(p.Root, "bin") }
func (p *Paths) Cache() string { return filepath.Join(p.Root, "cache") }
func (p *Paths) Share() string { return filepath.Join(p.Root, "share") }

// State holds bunny's record of what it installed: state.json, the manifest
// snapshots, the transaction journal and the mutation lock. They stay together
// so a restore cannot bring back one without the others.
func (p *Paths) State() string { return filepath.Join(p.Root, "state") }

// Config holds config.yaml. It is its own directory because the sandbox keeps
// the config's directory read-only, which must not cover the rest of the root.
func (p *Paths) Config() string { return filepath.Join(p.Root, "config") }

// --- Install roots ---

// InstallRoot returns the directory packages of the given kind are installed
// into. An unknown kind falls back to cli.
func (p *Paths) InstallRoot(kind string) string {
	if !manifest.KnownKind(kind) {
		kind = manifest.KindCLI
	}
	return filepath.Join(p.Root, kind)
}

// InstallRoots returns every install root, sorted, so callers that sweep all
// of them do not have to know how many kinds exist.
func (p *Paths) InstallRoots() []string {
	roots := make([]string, 0, len(manifest.Kinds))
	for _, kind := range manifest.Kinds {
		roots = append(roots, p.InstallRoot(kind))
	}
	slices.Sort(roots)
	return roots
}

// InstallDir is where a package of the given kind is installed. Use it when
// the kind is at hand, as for a new install; use AppDir for a package that is
// already installed.
func (p *Paths) InstallDir(id, kind string) string {
	return filepath.Join(p.InstallRoot(kind), id)
}

// --- Per-resource dirs ---

// AppDir is where an installed package lives, and the {app} placeholder. The
// kind state recorded decides the root, so a package whose kind later changes
// in the catalog is still found; one bunny does not know is looked for under
// cli, without guessing.
func (p *Paths) AppDir(id string) string {
	kind := manifest.KindCLI
	if p.kindOf != nil {
		if recorded := p.kindOf(id); recorded != "" {
			kind = recorded
		}
	}
	return p.InstallDir(id, kind)
}

func (p *Paths) BunnyBinary() string     { return filepath.Join(p.Bin(), "bunny") }
func (p *Paths) Shim(name string) string { return filepath.Join(p.Bin(), name) }

// AppData is a package's own data dir, the {data} placeholder, where config
// redirects a tool's native paths. Bunny writes only regenerable files here
// (Maven's toolchains.xml), never anything it would need to recover the
// install. It outlives an uninstall unless --purge is given.
func (p *Paths) AppData(id string) string { return filepath.Join(p.Root, "data", id) }

func (p *Paths) AppDownloadCache(id string) string { return filepath.Join(p.Cache(), id) }

// CatalogCacheRoot holds one directory per catalog. It shares the cache root
// with the per-package download directories, so its name is not a package id.
func (p *Paths) CatalogCacheRoot() string { return filepath.Join(p.Cache(), "catalogs") }

// CatalogCache is where one named catalog caches what it reads.
func (p *Paths) CatalogCache(name string) string {
	return filepath.Join(p.CatalogCacheRoot(), name)
}
func (p *Paths) StateFile() string    { return filepath.Join(p.State(), "state.json") }
func (p *Paths) MutationLock() string { return filepath.Join(p.State(), "mutation.lock") }

// TransactionFile journals an install in flight, so the next invocation can
// finish or undo one that was interrupted.
func (p *Paths) TransactionFile() string {
	return filepath.Join(p.State(), "install-transaction.json")
}

// --- staging ---

// stagingDirName is the in-progress install directory inside an install root.
const stagingDirName = ".staging"

// Staging holds in-progress installs, inside the install root rather than
// beside the cache: an install ends by renaming the staged tree into place,
// and rename(2) cannot cross filesystems. Downloads stay in the cache, which
// may live anywhere, since they are copied rather than renamed.
func (p *Paths) Staging(kind string) string {
	return filepath.Join(p.InstallRoot(kind), stagingDirName)
}

// StagingBeside is where to stage a tree destined for dir, keeping the rename
// that completes the install on one filesystem.
func (p *Paths) StagingBeside(dir string) string {
	return filepath.Join(filepath.Dir(dir), stagingDirName, filepath.Base(dir))
}

// StagingRoots returns the staging dir for every install root, so cleanup can
// sweep them all.
func (p *Paths) StagingRoots() []string {
	roots := p.InstallRoots()
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, filepath.Join(root, stagingDirName))
	}
	return out
}

// Manifests holds one snapshot per installed package, beside state.json.
// Bunny's record of what it installed has to survive a user clearing a
// package's {data} dir, which the package itself owns.
func (p *Paths) Manifests() string { return filepath.Join(p.State(), "manifests") }

// ManifestFile is the install-time manifest snapshot, kept so `bunny run`
// never has to hit the remote catalog at launch time and uninstall sees the
// package as it was installed.
func (p *Paths) ManifestFile(id string) string {
	return filepath.Join(p.Manifests(), id+".yaml")
}

// LayoutRoots are the paths a launched process must be able to read for a shim
// to re-enter Bunny and resolve another package: the shim directory, the record
// of what is installed, and the install roots themselves. Callers that
// restrict filesystem access grant them read-only;
// nothing here needs to be writable, so a package still cannot install,
// uninstall, or rewrite state from inside a sandbox.
func (p *Paths) LayoutRoots() []string {
	roots := append([]string{p.Bin(), p.StateFile(), p.Manifests()}, p.InstallRoots()...)
	return slices.Sorted(slices.Values(roots))
}

// --- Config + integration ---

func (p *Paths) UserConfigFile() string { return filepath.Join(p.Config(), "config.yaml") }
func (p *Paths) Desktop() string        { return filepath.Join(p.Share(), "applications") }
func (p *Paths) Icons() string          { return filepath.Join(p.Share(), "icons") }
func (p *Paths) BashCompletions() string {
	return filepath.Join(p.Share(), "bash-completion", "completions")
}
func (p *Paths) ZshCompletions() string {
	return filepath.Join(p.Share(), "zsh", "site-functions")
}
func (p *Paths) FishCompletions() string {
	return filepath.Join(p.Share(), "fish", "vendor_completions.d")
}

// ManPages is the root bunny installs man pages under; each section gets its
// own man{N} subdirectory, so `man` finds them once MANPATH includes this
// root (see the MANPATH guard in cmd/bunny/init.go).
func (p *Paths) ManPages() string { return filepath.Join(p.Share(), "man") }

// VarsAt is Vars with {app} pinned to a known directory. Integration expands
// placeholders before state records where the package went, so a lookup would
// return the fallback root rather than the truth.
func (p *Paths) VarsAt(id, version, appDir string) map[string]string {
	vars := p.Vars(id, version)
	vars["app"] = appDir
	return vars
}

// Vars returns the standard {key} placeholder map used in manifests
// (sources, prepare, bin.path, env values, bind targets).
func (p *Paths) Vars(id, version string) map[string]string {
	home, _ := os.UserHomeDir() // empty home is acceptable; manifests rarely use {home}
	return map[string]string{
		"id":      id,
		"version": version,
		"app":     p.AppDir(id),
		"bin":     p.Bin(),
		"data":    p.AppData(id),
		"home":    home,
		"share":   p.Share(),
	}
}
