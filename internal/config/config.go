// Package config is the user's settings file, at ~/.config/bunny/config.yaml
// or $BUNNY_HOME/config.yaml under a single-root install.
//
// It is the only place isolation policy lives. Manifests describe how to
// install and wire a package, never whether a tool's global data is
// redirected: bunny redirects nothing by default, and users opt in here.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cristatus/bunny/internal/manifest"
)

// Wildcard is the Env/Dirs key that applies to every package.
const Wildcard = "*"

// Config is the on-disk shape of config.yaml.
type Config struct {
	Catalog Catalog `yaml:"catalog,omitempty"`

	// Env adds environment variables at launch, keyed by package id
	// ("node-22"), capability ("node"), or "*". Values expand the same
	// placeholders manifests use, so "{data}/gradle" resolves per version.
	// This is how isolation is expressed; bunny ships none of it.
	Env map[string]map[string]string `yaml:"env,omitempty"`

	// Dirs are created before launch, keyed like Env. Most tools create their
	// own directories; this is for the ones that do not, and for values buried
	// inside a compound variable (MAVEN_ARGS) that bunny cannot parse out.
	Dirs map[string][]string `yaml:"dirs,omitempty"`

	// Install overrides where each kind of package goes, keyed by "app",
	// "cli", or "sdk". Pointing "sdk" somewhere shallow puts every JDK and
	// build tool where an IDE's file picker can reach it. A leading ~/ is
	// expanded; anything else must be absolute.
	Install map[string]string `yaml:"install,omitempty"`
}

// Catalog selects where bunny reads package manifests from.
type Catalog struct {
	// Remote is the HTTP catalog bunny falls back to.
	Remote string `yaml:"remote,omitempty"`

	// Local is a catalog checkout that takes precedence over Remote, for
	// working on a catalog or shipping a vendored one. Defaults to
	// <data>/catalog. A leading ~/ is expanded.
	Local string `yaml:"local,omitempty"`
}

// Load reads path. A missing file is not an error: it yields an empty config,
// which is the same thing as "isolate nothing, use the default catalog".
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}
	cfg := &Config{}
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate rejects env entries that could not be safely exported and install
// roots that are not usable directories.
func (c *Config) Validate() error {
	for _, key := range slices.Sorted(maps.Keys(c.Env)) {
		if err := manifest.ValidateEnv("env."+key, c.Env[key]); err != nil {
			return err
		}
	}
	for _, kind := range slices.Sorted(maps.Keys(c.Install)) {
		if !manifest.KnownKind(kind) {
			return fmt.Errorf("install.%s: must be one of %s", kind, strings.Join(manifest.Kinds, ", "))
		}
		root, err := absPath("install."+kind, c.Install[kind])
		if err != nil {
			return err
		}
		if root == string(filepath.Separator) {
			return fmt.Errorf("install.%s: refusing to install into the filesystem root", kind)
		}
		c.Install[kind] = root
	}
	if c.Catalog.Local != "" {
		local, err := absPath("catalog.local", c.Catalog.Local)
		if err != nil {
			return err
		}
		c.Catalog.Local = local
	}
	return nil
}

// absPath expands a leading ~/ and requires the result to be absolute, so a
// config path is never resolved against whatever directory bunny was run from.
func absPath(field, value string) (string, error) {
	expanded, err := expandHome(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", field, err)
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("%s: must be an absolute path or start with ~/, got %q", field, value)
	}
	return filepath.Clean(expanded), nil
}

// InstallRoots returns the configured roots keyed by kind, with placeholders
// already expanded by Validate. Nil-safe.
func (c *Config) InstallRoots() map[string]string {
	if c == nil {
		return nil
	}
	return c.Install
}

// expandHome resolves a leading ~/ against the user's home directory. Only the
// leading form is supported: "~other/dir" is not a path bunny should guess at.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand ~: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// keysFor returns the config keys that apply to a package, least specific
// first, so a later overlay wins: "*", then the capability, then the id.
func keysFor(id, capability string) []string {
	keys := []string{Wildcard}
	if capability != "" && capability != id {
		keys = append(keys, capability)
	}
	if id != "" {
		keys = append(keys, id)
	}
	return keys
}

// OverlayEnv returns base with the user's configured entries layered on top,
// as a fresh map. Nil-safe. Placeholders are left unexpanded: the caller owns
// the vars map.
func (c *Config) OverlayEnv(base map[string]string, id, capability string) map[string]string {
	merged := make(map[string]string, len(base))
	for key, value := range base {
		merged[key] = value
	}
	if c == nil {
		return merged
	}
	for _, key := range keysFor(id, capability) {
		for name, value := range c.Env[key] {
			merged[name] = value
		}
	}
	return merged
}

// DirsFor returns the directories configured for a package, in the same
// least-specific-first order. Nil-safe.
func (c *Config) DirsFor(id, capability string) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, key := range keysFor(id, capability) {
		out = append(out, c.Dirs[key]...)
	}
	return out
}
