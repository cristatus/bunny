// Package paths is the single source of truth for "where does X live"
// inside $BUNNY_HOME. Every other package consumes it; nothing here
// touches the filesystem.
package paths

import (
	"os"
	"path/filepath"
)

// EnvHome is the environment variable that overrides the default $HOME/.bunny.
const EnvHome = "BUNNY_HOME"

// Paths centralizes every directory under $BUNNY_HOME.
type Paths struct {
	Home string // root, e.g. /home/u/.bunny
}

// Resolve returns the active Paths from $BUNNY_HOME or the ~/.bunny default.
// The directory is not created — callers do that lazily.
func Resolve() (*Paths, error) {
	home := os.Getenv(EnvHome)
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".bunny")
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	return &Paths{Home: abs}, nil
}

// At builds Paths rooted at an arbitrary directory (used in tests).
func At(home string) *Paths { return &Paths{Home: home} }

// --- Top-level dirs ---

func (p *Paths) Bin() string     { return filepath.Join(p.Home, "bin") }
func (p *Paths) App() string     { return filepath.Join(p.Home, "app") }
func (p *Paths) Catalog() string { return filepath.Join(p.Home, "catalog") }
func (p *Paths) Share() string   { return filepath.Join(p.Home, "share") }
func (p *Paths) Var() string     { return filepath.Join(p.Home, "var") }

// --- Per-resource dirs ---

func (p *Paths) AppDir(id string) string { return filepath.Join(p.App(), id) }
func (p *Paths) BunnyBinary() string     { return filepath.Join(p.Bin(), "bunny") }
func (p *Paths) Shim(name string) string { return filepath.Join(p.Bin(), name) }

// --- var/* ---

func (p *Paths) VarApp() string                    { return filepath.Join(p.Var(), "app") }
func (p *Paths) AppData(id string) string          { return filepath.Join(p.VarApp(), id) }
func (p *Paths) Cache() string                     { return filepath.Join(p.Var(), "cache") }
func (p *Paths) AppDownloadCache(id string) string { return filepath.Join(p.Cache(), id) }
func (p *Paths) Tmp() string                       { return filepath.Join(p.Var(), "tmp") }
func (p *Paths) AppTmp(id string) string           { return filepath.Join(p.Tmp(), id) }
func (p *Paths) StateFile() string                 { return filepath.Join(p.Var(), "state.json") }
func (p *Paths) MutationLock() string              { return filepath.Join(p.Var(), "mutation.lock") }

// ManifestFile is the runtime cache of the install-time manifest, used so
// `bunny run` never has to hit the remote catalog at launch time.
func (p *Paths) ManifestFile(id string) string {
	return filepath.Join(p.AppData(id), "manifest.yaml")
}

// --- Config + integration ---

func (p *Paths) UserConfigFile() string { return filepath.Join(p.Home, "config.yaml") }
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
