// Package paths is the single source of truth for "where does X live".
// Every other package consumes it; nothing here touches the filesystem.
//
// There is one logical layout with two ways to prefix it. By default each
// class of file goes to its XDG base directory, so desktop entries land where
// the desktop already scans and shims land in ~/.local/bin. Setting
// $BUNNY_HOME collapses the layout under a single root instead, which is what
// containers, CI, tests, and fleet images want.
package paths

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/cristatus/bunny/internal/manifest"
)

// EnvHome is the environment variable that collapses bunny into a single root.
// Its value must be an absolute path.
const EnvHome = "BUNNY_HOME"

// Paths centralizes every directory bunny reads or writes. The roots are
// resolved once at construction, so accessors are plain joins with no
// branching on which layout is active.
type Paths struct {
	// Root is the single-prefix install root under $BUNNY_HOME, "" under XDG.
	// Only diagnostics should need to ask.
	Root string

	data   string // installs, catalog, state, per-package data
	config string // config.yaml
	cache  string // downloads and the catalog index
	bin    string // shims
	share  string // desktop entries, icons, bash/zsh completions
	fish   string // fish completions, which XDG puts under config, not share

	// roots overrides the default install root per kind ("app"/"cli"/"sdk").
	roots map[string]string
	// locate reports an installed package's kind and, when it sits outside the
	// default root for that kind, its recorded path. Backed by state.
	locate func(id string) (kind, path string)
}

// WithLayout returns a copy of p that resolves install locations through the
// user's configured roots and what state recorded about each package. roots
// decides where a new install goes, locate where an existing one already is.
// Consulting the recorded location is what makes roots safe to change.
func (p *Paths) WithLayout(roots map[string]string, locate func(id string) (kind, path string)) *Paths {
	clone := *p
	clone.roots = roots
	clone.locate = locate
	return &clone
}

// Resolve returns the active Paths: a single root when $BUNNY_HOME is set,
// otherwise the XDG base directories. No directory is created here; callers
// do that lazily.
//
// A relative $BUNNY_HOME is an error rather than a path resolved against the
// working directory, which would move the whole layout as the user cd'd around
// and leave shims resolving to a different install per directory. Silently
// ignoring it, the way the XDG spec requires for its own variables, is worse
// here: this is the variable that selects the layout, so ignoring it hands back
// the XDG layout with none of the packages the caller meant to reach.
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
	var (
		dataHome   = xdgDir("XDG_DATA_HOME", home, ".local", "share")
		configHome = xdgDir("XDG_CONFIG_HOME", home, ".config")
		cacheHome  = xdgDir("XDG_CACHE_HOME", home, ".cache")
	)
	return &Paths{
		data:   filepath.Join(dataHome, "bunny"),
		config: filepath.Join(configHome, "bunny"),
		cache:  filepath.Join(cacheHome, "bunny"),
		// ~/.local/bin is its own convention, not derived from XDG_DATA_HOME.
		bin:   filepath.Join(home, ".local", "bin"),
		share: dataHome,
		fish:  filepath.Join(configHome, "fish", "completions"),
	}, nil
}

// At builds Paths collapsed under a single root. This is what $BUNNY_HOME
// selects, and what tests use.
func At(root string) *Paths {
	return &Paths{
		Root:   root,
		data:   root,
		config: root,
		cache:  filepath.Join(root, "cache"),
		bin:    filepath.Join(root, "bin"),
		share:  filepath.Join(root, "share"),
		fish:   filepath.Join(root, "share", "fish", "vendor_completions.d"),
	}
}

// XDG reports whether bunny is spread across the XDG base directories rather
// than collapsed under a single root. Callers use it where the two layouts
// genuinely differ, such as deciding whether the shell needs XDG_DATA_DIRS.
func (p *Paths) XDG() bool { return p.Root == "" }

// xdgDir returns $name when it holds an absolute path, else the fallback under
// the user's home. The spec requires relative values to be ignored.
func xdgDir(name, home string, fallback ...string) string {
	if v := os.Getenv(name); filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}

// --- Top-level dirs ---

func (p *Paths) Bin() string     { return p.bin }
func (p *Paths) Data() string    { return p.data }
func (p *Paths) Cache() string   { return p.cache }
func (p *Paths) Share() string   { return p.share }
func (p *Paths) Catalog() string { return filepath.Join(p.data, "catalog") }

// --- Install roots ---

// InstallRoot returns the directory packages of the given kind are installed
// into: the user's configured root when there is one, else a per-kind
// directory under bunny's data dir. An unknown kind falls back to cli.
func (p *Paths) InstallRoot(kind string) string {
	if !manifest.KnownKind(kind) {
		kind = manifest.KindCLI
	}
	if root := p.roots[kind]; root != "" {
		return root
	}
	return filepath.Join(p.data, kind)
}

// InstallRoots returns every distinct install root, sorted, so callers that
// sweep all of them do not have to know how many kinds exist.
func (p *Paths) InstallRoots() []string {
	seen := map[string]bool{}
	for _, kind := range manifest.Kinds {
		seen[p.InstallRoot(kind)] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// InstallDir is where a package of the given kind will be installed. Use it
// when choosing a destination for a new install; use AppDir for a package that
// is already installed.
func (p *Paths) InstallDir(id, kind string) string {
	return filepath.Join(p.InstallRoot(kind), id)
}

// --- Per-resource dirs ---

// AppDir is where an installed package lives, and the {app} placeholder. A
// recorded path wins; otherwise the location comes from the recorded kind.
func (p *Paths) AppDir(id string) string {
	kind := manifest.KindCLI
	if p.locate != nil {
		recordedKind, path := p.locate(id)
		if path != "" {
			return path
		}
		if recordedKind != "" {
			kind = recordedKind
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
func (p *Paths) AppData(id string) string { return filepath.Join(p.data, "data", id) }

func (p *Paths) AppDownloadCache(id string) string { return filepath.Join(p.Cache(), id) }
func (p *Paths) StateFile() string                 { return filepath.Join(p.data, "state.json") }
func (p *Paths) MutationLock() string              { return filepath.Join(p.data, "mutation.lock") }

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

// StagingBeside is where to stage a tree destined for dir. An install that
// updates a package in place lands wherever that package already is, which is
// not necessarily under the root currently configured for its kind, and the
// rename that completes it still has to stay on one filesystem.
func (p *Paths) StagingBeside(dir string) string {
	return filepath.Join(filepath.Dir(dir), stagingDirName, filepath.Base(dir))
}

// StagingRoots returns the staging dir for every install root, so cleanup can
// sweep them all without knowing which kinds are configured where.
func (p *Paths) StagingRoots() []string {
	roots := p.InstallRoots()
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, filepath.Join(root, stagingDirName))
	}
	return out
}

// Manifests holds one snapshot per installed package, in the same durability
// class as state.json. Bunny's record of what it installed has to survive a
// user clearing a package's {data} dir, which the package itself owns.
func (p *Paths) Manifests() string { return filepath.Join(p.data, "manifests") }

// ManifestFile is the install-time manifest snapshot, kept so `bunny run`
// never has to hit the remote catalog at launch time and uninstall sees the
// package as it was installed.
func (p *Paths) ManifestFile(id string) string {
	return filepath.Join(p.Manifests(), id+".yaml")
}

// --- Config + integration ---

func (p *Paths) UserConfigFile() string { return filepath.Join(p.config, "config.yaml") }
func (p *Paths) Desktop() string        { return filepath.Join(p.Share(), "applications") }
func (p *Paths) Icons() string          { return filepath.Join(p.Share(), "icons") }
func (p *Paths) BashCompletions() string {
	return filepath.Join(p.Share(), "bash-completion", "completions")
}
func (p *Paths) ZshCompletions() string {
	return filepath.Join(p.Share(), "zsh", "site-functions")
}
func (p *Paths) FishCompletions() string { return p.fish }

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
