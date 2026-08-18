package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/manifest"
)

const sandboxContextEnv = "BUNNY_SANDBOX_CONTEXT"

// sandboxContext is inherited by shims launched from a sandboxed application.
// It makes restrictions monotonic and lets a child package change only its
// state directories without paying for a redundant nested mount namespace.
type sandboxContext struct {
	Packages         []string `json:"packages"`
	HostHome         string   `json:"hostHome"`
	Hidden           []string `json:"hidden,omitempty"`
	DisabledFeatures []string `json:"disabledFeatures,omitempty"`
}

type sandboxPlan struct {
	args       []string
	env        []string
	needsLayer bool
}

// PackageSandbox is the effective run-time policy after merging a manifest's
// recommendations, the selected user profile, and the package's inline user
// override. Activation is evaluated separately so the same policy can be used
// automatically or only by an explicit command.
type PackageSandbox struct {
	Home     string
	Hide     []string
	Features map[string]bool
}

// ResolvePackageSandbox computes policy independently of activation. This lets
// `bunny sandbox <id>` use manifest defaults for an unconfigured package and
// lets an on-demand package retain overrides without affecting normal runs.
// Policy merges from least to most authoritative: manifest recommendation,
// selected user profile, package override. A profile named by package config
// replaces the manifest's recommended profile.
func ResolvePackageSandbox(cfg *config.Config, id string, recommended *manifest.SandboxPolicy) (*PackageSandbox, error) {
	var pkg config.SandboxPackage
	if cfg != nil {
		pkg = cfg.Sandbox.Packages[id]
	}

	effective := &PackageSandbox{Home: "isolated", Features: map[string]bool{}}
	if recommended != nil {
		mergeManifestPolicy(effective, recommended)
	}

	profile := pkg.Profile
	if profile == "" && recommended != nil {
		profile = recommended.Profile
	}
	if profile != "" {
		policy, ok := cfg.SandboxProfile(profile)
		if !ok {
			return nil, fmt.Errorf("sandbox package %q selects unknown profile %q", id, profile)
		}
		mergeConfigPolicy(effective, policy)
	}
	mergeConfigPolicy(effective, pkg.SandboxPolicy)
	effective.Hide = dedupSorted(effective.Hide)
	return effective, nil
}

// sandboxAlways reports whether ordinary launches should use the effective
// policy. Package-map presence defaults to always; on-demand retains policy
// only for an explicit `bunny sandbox <id>` launch.
func sandboxAlways(cfg *config.Config, id string) bool {
	if cfg == nil {
		return false
	}
	pkg, present := cfg.Sandbox.Packages[id]
	return present && pkg.Activation != config.SandboxActivationOnDemand
}

func mergeManifestPolicy(dst *PackageSandbox, src *manifest.SandboxPolicy) {
	if src == nil {
		return
	}
	if src.Home != "" {
		dst.Home = src.Home
	}
	dst.Hide = append(dst.Hide, src.Hide...)
	for name, enabled := range src.Features {
		dst.Features[name] = enabled
	}
}

func mergeConfigPolicy(dst *PackageSandbox, src config.SandboxPolicy) {
	if src.Home != "" {
		dst.Home = src.Home
	}
	dst.Hide = append(dst.Hide, src.Hide...)
	for name, enabled := range src.Features {
		dst.Features[name] = enabled
	}
}

// feature defaults to enabled. The sandbox exists to scope an installed
// package's persistent state, not to guess which desktop integrations it can
// live without; manifests and user profiles turn individual features off.
func (p *PackageSandbox) feature(name string) bool {
	enabled, set := p.Features[name]
	return !set || enabled
}

// sandboxArgs builds the bwrap invocation for the original lightweight
// per-package model: the trusted package retains a native host filesystem view
// while HOME/XDG state can be redirected into its Bunny data directory,
// selected paths can be masked, and optional integrations can be disabled.
// It is data isolation, not a hardened boundary against malicious code.
func sandboxArgs(p *Prepared, policy *PackageSandbox, cwd, hostHome string) ([]string, error) {
	plan, err := buildSandboxPlan(p, policy, cwd, hostHome, sandboxContext{})
	return plan.args, err
}

func buildSandboxPlan(p *Prepared, policy *PackageSandbox, cwd, hostHome string, current sandboxContext) (sandboxPlan, error) {
	if current.HostHome != "" {
		hostHome = current.HostHome
	}
	packages := slices.Clone(current.Packages)
	if !slices.Contains(packages, p.Manifest.ID) {
		packages = append(packages, p.Manifest.ID)
	}
	next := sandboxContext{
		Packages:         packages,
		HostHome:         hostHome,
		Hidden:           slices.Clone(current.Hidden),
		DisabledFeatures: slices.Clone(current.DisabledFeatures),
	}

	disabled := make(map[string]bool, len(current.DisabledFeatures)+len(policy.Features))
	for _, name := range current.DisabledFeatures {
		disabled[name] = true
	}
	for _, name := range []string{"network", "x11", "wayland", "dbus", "audio"} {
		if !policy.feature(name) {
			disabled[name] = true
		}
	}
	next.DisabledFeatures = sortedMapKeys(disabled)

	hidden := make(map[string]bool, len(current.Hidden)+len(policy.Hide))
	for _, path := range current.Hidden {
		hidden[path] = true
	}
	var newHidden []string
	for _, raw := range policy.Hide {
		path := expandHome(raw, hostHome)
		if hidden[path] {
			continue
		}
		_, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return sandboxPlan{}, fmt.Errorf("inspect sandbox hide path %s: %w", path, err)
		}
		hidden[path] = true
		newHidden = append(newHidden, path)
	}
	next.Hidden = sortedMapKeys(hidden)

	encoded, err := json.Marshal(next)
	if err != nil {
		return sandboxPlan{}, fmt.Errorf("encode sandbox context: %w", err)
	}

	overrides := map[string]string{sandboxContextEnv: string(encoded)}
	for _, assignment := range p.BunnyEnv {
		name, value, ok := strings.Cut(assignment, "=")
		if ok {
			overrides[name] = value
		}
	}

	if policy.Home == "isolated" {
		home := filepath.Join(p.Vars["data"], "home")
		configHome := filepath.Join(home, ".config")
		cacheHome := filepath.Join(home, ".cache")
		dataHome := filepath.Join(home, ".local", "share")
		for _, dir := range []string{configHome, cacheHome, dataHome} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return sandboxPlan{}, fmt.Errorf("create sandbox data directory %s: %w", dir, err)
			}
		}
		overrides["HOME"] = home
		overrides["XDG_CONFIG_HOME"] = configHome
		overrides["XDG_CACHE_HOME"] = cacheHome
		overrides["XDG_DATA_HOME"] = dataHome
	}

	newNetworkRestriction := disabled["network"] && !slices.Contains(current.DisabledFeatures, "network")
	nested := len(current.Packages) > 0
	needsLayer := !nested || newNetworkRestriction || len(newHidden) > 0
	env := sandboxEnv(p.Env, overrides, disabled)
	if !needsLayer {
		return sandboxPlan{env: env}, nil
	}

	args := []string{"--dev-bind", "/", "/", "--die-with-parent"}
	if newNetworkRestriction {
		args = append(args, "--unshare-net")
	}
	for _, path := range newHidden {
		info, err := os.Stat(path)
		if err != nil {
			return sandboxPlan{}, fmt.Errorf("inspect sandbox hide path %s: %w", path, err)
		}
		if info.IsDir() {
			args = append(args, "--tmpfs", path)
		} else {
			args = append(args, "--ro-bind", "/dev/null", path)
		}
	}
	for _, name := range sortedMapKeys(overrides) {
		args = append(args, "--setenv", name, overrides[name])
	}

	for _, feature := range []struct {
		name string
		env  []string
	}{
		{name: "x11", env: []string{"DISPLAY"}},
		{name: "wayland", env: []string{"WAYLAND_DISPLAY"}},
		{name: "dbus", env: []string{"DBUS_SESSION_BUS_ADDRESS"}},
		{name: "audio", env: []string{"PULSE_SERVER", "PIPEWIRE_REMOTE"}},
	} {
		if disabled[feature.name] {
			for _, name := range feature.env {
				args = append(args, "--unsetenv", name)
			}
		}
	}

	args = append(args, "--chdir", cwd, "--", p.BinPath)
	return sandboxPlan{args: append(args, p.CmdArgs...), env: env, needsLayer: true}, nil
}

func sandboxEnv(base []string, overrides map[string]string, disabled map[string]bool) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, assignment := range base {
		name, value, ok := strings.Cut(assignment, "=")
		if ok {
			values[name] = value
		}
	}
	for name, value := range overrides {
		values[name] = value
	}
	for _, feature := range []struct {
		name string
		env  []string
	}{
		{name: "x11", env: []string{"DISPLAY"}},
		{name: "wayland", env: []string{"WAYLAND_DISPLAY"}},
		{name: "dbus", env: []string{"DBUS_SESSION_BUS_ADDRESS"}},
		{name: "audio", env: []string{"PULSE_SERVER", "PIPEWIRE_REMOTE"}},
	} {
		if disabled[feature.name] {
			for _, name := range feature.env {
				delete(values, name)
			}
		}
	}
	names := sortedMapKeys(values)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+values[name])
	}
	return out
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func readSandboxContext(env []string) (sandboxContext, error) {
	for _, assignment := range env {
		value, ok := strings.CutPrefix(assignment, sandboxContextEnv+"=")
		if !ok {
			continue
		}
		var context sandboxContext
		if err := json.Unmarshal([]byte(value), &context); err != nil {
			return sandboxContext{}, fmt.Errorf("decode inherited sandbox context: %w", err)
		}
		return context, nil
	}
	return sandboxContext{}, nil
}

// inheritSandboxEnv reapplies environment-based restrictions after manifest
// and user overlays. This also covers an on-demand or unconfigured package:
// it does not get its own isolated home, but it cannot loosen its parent's
// sandbox merely by setting DISPLAY or another integration variable.
func inheritSandboxEnv(env []string) ([]string, error) {
	context, err := readSandboxContext(env)
	if err != nil {
		return nil, err
	}
	if len(context.Packages) == 0 {
		return env, nil
	}
	disabled := make(map[string]bool, len(context.DisabledFeatures))
	for _, name := range context.DisabledFeatures {
		disabled[name] = true
	}
	return sandboxEnv(env, nil, disabled), nil
}

// ExecPackage runs a prepared package directly unless the user explicitly
// enabled it in sandbox.packages. Sandboxed launches still use syscall.Exec,
// preserving normal signals and exit status exactly like the direct path.
func ExecPackage(p *Prepared, cfg *config.Config) error {
	if !sandboxAlways(cfg, p.Manifest.ID) {
		return directExec(p)
	}
	return execPackageSandboxed(p, cfg)
}

// ExecPackageSandboxed forces one package-aware sandbox launch regardless of
// its normal activation. Manifest recommendations and any configured profile
// or package override still compose exactly as they do for always activation.
func ExecPackageSandboxed(p *Prepared, cfg *config.Config) error {
	return execPackageSandboxed(p, cfg)
}

func execPackageSandboxed(p *Prepared, cfg *config.Config) error {
	policy, err := ResolvePackageSandbox(cfg, p.Manifest.ID, p.Manifest.Sandbox)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	hostHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve host home: %w", err)
	}
	context, err := readSandboxContext(p.Env)
	if err != nil {
		return err
	}
	plan, err := buildSandboxPlan(p, policy, cwd, hostHome, context)
	if err != nil {
		return err
	}
	log.Debug("Sandboxed exec", "package", p.Manifest.ID, "profileHome", policy.Home,
		"features", policy.Features, "hide", policy.Hide, "nestedLayer", plan.needsLayer)
	if !plan.needsLayer {
		args := append([]string{p.BinPath}, p.CmdArgs...)
		return syscall.Exec(p.BinPath, args, plan.env)
	}
	bwrapPath, err := FindBwrap()
	if err != nil {
		return err
	}
	return syscall.Exec(bwrapPath, append([]string{bwrapPath}, plan.args...), p.Env)
}

func expandHome(path, home string) string {
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	case filepath.IsAbs(path):
		return filepath.Clean(path)
	default:
		return filepath.Join(home, path)
	}
}

func dedupSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}
