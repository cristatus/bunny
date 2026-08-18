// Package config is the user's settings file, at ~/.config/bunny/config.yaml
// or $BUNNY_HOME/config.yaml under a single-root install.
//
// It is the only place that can activate optional behavior. Manifests may
// recommend a sandbox policy, but data redirection and run-time sandboxing
// remain inert until the user opts in here.
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
	// This is how tool-specific data redirection is expressed.
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

	// Sandbox holds reusable profiles and the package IDs the user explicitly
	// opts into run-time sandboxing. A manifest may recommend policy, but only
	// presence in Sandbox.Packages activates it.
	Sandbox Sandbox `yaml:"sandbox,omitempty"`
}

// Sandbox is the user-owned activation and policy layer. There is deliberately
// no global enabled flag: each package must be named explicitly.
type Sandbox struct {
	Profiles map[string]SandboxPolicy  `yaml:"profiles,omitempty"`
	Packages map[string]SandboxPackage `yaml:"packages,omitempty"`
}

// SandboxPolicy is a reusable user policy. Feature maps merge by key and Hide
// is additive; Home, when set, replaces the inherited value.
type SandboxPolicy struct {
	Home     string          `yaml:"home,omitempty"`
	Hide     []string        `yaml:"hide,omitempty"`
	Features map[string]bool `yaml:"features,omitempty"`
}

// SandboxPackage stores activation plus a package-specific policy. Presence
// defaults activation to always; on-demand keeps normal launches direct while
// retaining policy for `bunny sandbox <id>`. Profile selects a reusable policy;
// the inline policy overrides it without replacing unspecified settings.
type SandboxPackage struct {
	Activation    string `yaml:"activation,omitempty"`
	Profile       string `yaml:"profile,omitempty"`
	SandboxPolicy `yaml:",inline"`
}

const (
	SandboxActivationAlways   = "always"
	SandboxActivationOnDemand = "on-demand"

	SandboxProfileDesktop    = "desktop"
	SandboxProfileOnlineCLI  = "online-cli"
	SandboxProfileOfflineCLI = "offline-cli"
)

// builtinSandboxProfiles provide stable policy vocabulary without putting
// generic feature boilerplate in manifests or every user's config. Activation
// remains package-specific and user-owned; selecting one of these names only
// chooses policy.
var builtinSandboxProfiles = map[string]SandboxPolicy{
	SandboxProfileDesktop: {
		Home: "isolated",
		Features: map[string]bool{
			"network": true, "x11": true, "wayland": true, "dbus": true, "audio": true,
		},
	},
	SandboxProfileOnlineCLI: {
		Home: "isolated",
		Features: map[string]bool{
			"network": true, "x11": false, "wayland": false, "dbus": false, "audio": false,
		},
	},
	SandboxProfileOfflineCLI: {
		Home: "isolated",
		Features: map[string]bool{
			"network": false, "x11": false, "wayland": false, "dbus": false, "audio": false,
		},
	},
}

// SandboxProfile resolves a built-in or user-defined profile. Built-in names
// are reserved so their behavior remains stable across machines and configs.
// The returned policy is a copy and may be safely merged by the caller.
func (c *Config) SandboxProfile(name string) (SandboxPolicy, bool) {
	if policy, ok := builtinSandboxProfiles[name]; ok {
		return cloneSandboxPolicy(policy), true
	}
	if c == nil {
		return SandboxPolicy{}, false
	}
	policy, ok := c.Sandbox.Profiles[name]
	if !ok {
		return SandboxPolicy{}, false
	}
	return cloneSandboxPolicy(policy), true
}

func cloneSandboxPolicy(policy SandboxPolicy) SandboxPolicy {
	policy.Hide = slices.Clone(policy.Hide)
	policy.Features = maps.Clone(policy.Features)
	return policy
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
	for _, name := range slices.Sorted(maps.Keys(c.Sandbox.Profiles)) {
		if err := manifest.ValidateID(name); err != nil {
			return fmt.Errorf("sandbox.profiles.%s: %w", name, err)
		}
		if _, reserved := builtinSandboxProfiles[name]; reserved {
			return fmt.Errorf("sandbox.profiles.%s: built-in profile name is reserved", name)
		}
		if err := validateSandboxPolicy("sandbox.profiles."+name, c.Sandbox.Profiles[name]); err != nil {
			return err
		}
	}
	for _, id := range slices.Sorted(maps.Keys(c.Sandbox.Packages)) {
		if err := manifest.ValidateID(id); err != nil {
			return fmt.Errorf("sandbox.packages.%s: %w", id, err)
		}
		pkg := c.Sandbox.Packages[id]
		if pkg.Activation != "" && pkg.Activation != SandboxActivationAlways && pkg.Activation != SandboxActivationOnDemand {
			return fmt.Errorf("sandbox.packages.%s.activation: must be \"always\" or \"on-demand\"", id)
		}
		if pkg.Profile != "" {
			if _, ok := c.SandboxProfile(pkg.Profile); !ok {
				return fmt.Errorf("sandbox.packages.%s.profile: unknown profile %q", id, pkg.Profile)
			}
		}
		if err := validateSandboxPolicy("sandbox.packages."+id, pkg.SandboxPolicy); err != nil {
			return err
		}
	}
	return nil
}

func validateSandboxPolicy(field string, policy SandboxPolicy) error {
	return manifest.ValidateSandboxPolicy(field, &manifest.SandboxPolicy{
		Home: policy.Home, Hide: policy.Hide, Features: policy.Features,
	})
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
	merged := maps.Clone(base)
	if merged == nil {
		merged = make(map[string]string)
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
