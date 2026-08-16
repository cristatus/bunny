// Package paths is the single source of truth for "where does X live".
// Every other package consumes it; nothing here touches the filesystem.
//
// Bunny has one logical layout and two ways to prefix it. By default each
// class of file goes to its XDG base directory, so bunny's own data sits
// where a Linux desktop already expects to find it: desktop entries land in
// ~/.local/share/applications and are picked up with no XDG_DATA_DIRS
// plumbing, and shims land in ~/.local/bin, which is usually on PATH already.
//
// Setting $BUNNY_HOME collapses the whole layout back under a single root.
// That keeps the self-contained install available for containers, CI, tests,
// and anyone who wants one directory they can delete.
package paths

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/cristatus/bunny/internal/manifest"
)

// EnvHome is the environment variable that collapses bunny into a single root.
const EnvHome = "BUNNY_HOME"

// Paths centralizes every directory bunny reads or writes. The unexported
// roots are resolved once at construction so every accessor is a plain join,
// with no branching on which layout is active.
type Paths struct {
	// Root is the single-prefix install root when $BUNNY_HOME is in use, and
	// "" under the XDG layout. Only diagnostics should need to ask.
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
	// default root for that kind, its recorded path. Set from state, which is
	// what makes a config change affect the next install rather than strand
	// existing ones.
	locate func(id string) (kind, path string)
}

// WithLayout returns a copy of p that resolves install locations through the
// user's configured roots and what state recorded about each package.
//
// The two answer different questions. roots decides where a *new* install
// goes; locate says where an existing one already is. Consulting the recorded
// location is what makes the roots safe to change: editing config, or a
// catalog changing a package's kind, can never move or lose a tree that is
// already on disk.
func (p *Paths) WithLayout(roots map[string]string, locate func(id string) (kind, path string)) *Paths {
	clone := *p
	clone.roots = roots
	clone.locate = locate
	return &clone
}

// Resolve returns the active Paths: a single root when $BUNNY_HOME is set,
// otherwise the XDG base directories. No directory is created here; callers
// do that lazily.
func Resolve() (*Paths, error) {
	if root := os.Getenv(EnvHome); root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		return At(abs), nil
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
// directory under bunny's data dir. An unknown kind falls back to "cli".
func (p *Paths) InstallRoot(kind string) string {
	if !manifest.ValidKind(kind) || kind == "" {
		kind = manifest.KindCLI
	}
	if root, ok := p.roots[kind]; ok && root != "" {
		return root
	}
	return filepath.Join(p.data, kind)
}

// InstallRoots returns every distinct install root, sorted, so callers that
// have to sweep all of them (pruning abandoned staging dirs, diagnostics) do
// not have to know how many kinds exist.
func (p *Paths) InstallRoots() []string {
	seen := map[string]bool{}
	var out []string
	for _, kind := range manifest.Kinds {
		if root := p.InstallRoot(kind); !seen[root] {
			seen[root] = true
			out = append(out, root)
		}
	}
	sort.Strings(out)
	return out
}

// InstallDir is where a package of the given kind will be installed. Use it
// when choosing a destination for a new install; use AppDir for a package that
// is already installed.
func (p *Paths) InstallDir(id, kind string) string {
	return filepath.Join(p.InstallRoot(kind), id)
}

// --- Per-resource dirs ---

// AppDir is where an installed package lives, and the {app} placeholder. A
// path recorded at install time wins, so a package installed somewhere custom
// keeps working after the configured roots change; otherwise the location is
// derived from the kind it was installed as.
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

// AppData is a package's own data dir, the {data} placeholder. It holds the
// install-time manifest snapshot and whatever the user redirects here through
// config, and it deliberately outlives an uninstall unless --purge is given.
func (p *Paths) AppData(id string) string { return filepath.Join(p.data, "data", id) }

func (p *Paths) AppDownloadCache(id string) string { return filepath.Join(p.Cache(), id) }
func (p *Paths) StateFile() string                 { return filepath.Join(p.data, "state.json") }
func (p *Paths) MutationLock() string              { return filepath.Join(p.data, "mutation.lock") }

// --- staging ---

// Staging holds in-progress installs. It lives inside the app root, not beside
// the cache, because the final step of an install is renaming the staged tree
// into place: rename(2) cannot cross filesystems, and only a sibling directory
// guarantees the two are on the same one. Downloads stay in the cache, which
// may live anywhere, since they are hard-linked or copied into the staging
// dir rather than renamed.
func (p *Paths) Staging(kind string) string {
	return filepath.Join(p.InstallRoot(kind), ".staging")
}

func (p *Paths) AppStaging(id, kind string) string {
	return filepath.Join(p.Staging(kind), id)
}

// StagingRoots returns the staging dir for every install root, so cleanup can
// sweep them all without knowing which kinds are configured where.
func (p *Paths) StagingRoots() []string {
	roots := p.InstallRoots()
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		out = append(out, filepath.Join(root, ".staging"))
	}
	return out
}

// ManifestFile is the runtime cache of the install-time manifest, used so
// `bunny run` never has to hit the remote catalog at launch time.
func (p *Paths) ManifestFile(id string) string {
	return filepath.Join(p.AppData(id), "manifest.yaml")
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

// VarsAt is Vars with {app} pinned to a known directory. The install path
// needs it: placeholders are expanded while the tree is being integrated,
// which happens before state records where the package went, so asking Vars
// to look the location up would get the fallback root rather than the truth.
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
