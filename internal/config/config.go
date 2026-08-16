// Package config is the user's own settings file at $BUNNY_HOME/config.yaml.
//
// It is the only place isolation policy lives. Catalog manifests describe how
// to install and wire a package; they no longer decide whether a tool's global
// data (`~/.m2`, `~/.gradle`, npm's prefix) gets redirected somewhere private.
// Bunny's default is to redirect nothing, so a tool run through bunny writes
// exactly where it would have written had the user installed it themselves.
// Users who want per-version isolation opt into it here, one env var at a time.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/cristatus/bunny/internal/manifest"
)

// Wildcard is the Env/Dirs key that applies to every package.
const Wildcard = "*"

// Config is the on-disk shape of $BUNNY_HOME/config.yaml.
type Config struct {
	Catalog Catalog `yaml:"catalog,omitempty"`

	// Env adds environment variables to a package's launch environment. The
	// outer key is a package id ("node-22"), a capability ("node"), or "*" for
	// every package. Values expand the same placeholders manifests use, so
	// "{data}/gradle" resolves per package version.
	//
	// This is how isolation is expressed: bunny ships none of it by default.
	Env map[string]map[string]string `yaml:"env,omitempty"`

	// Dirs are created before launch, keyed like Env. Most tools create their
	// own directories; this is for the ones that do not, and for values buried
	// inside a compound variable (MAVEN_ARGS) that bunny cannot parse out.
	Dirs map[string][]string `yaml:"dirs,omitempty"`
}

// Catalog selects which remote catalog bunny reads packages from.
type Catalog struct {
	Remote string `yaml:"remote,omitempty"`
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

// Validate rejects env entries that could not be safely exported.
func (c *Config) Validate() error {
	for _, key := range sortedKeys(c.Env) {
		if err := manifest.ValidateEnv("env."+key, c.Env[key]); err != nil {
			return err
		}
	}
	return nil
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

// OverlayEnv returns base with the user's configured entries layered on top.
// The result is a fresh map; base is never mutated. Nil-safe, so callers with
// no config on hand can pass nil and get the manifest's env back unchanged.
//
// Placeholders are left unexpanded: the caller owns the vars map.
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

func sortedKeys(m map[string]map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
