package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/manifest"
)

func TestManifestPolicyResolvesWithoutActivatingNormalLaunch(t *testing.T) {
	cfg := &config.Config{}
	recommended := &manifest.SandboxPolicy{Profile: "desktop"}
	got, err := ResolvePackageSandbox(cfg, "vscode", recommended)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Home != "isolated" {
		t.Fatalf("forced policy did not resolve manifest recommendation: %+v", got)
	}
	if sandboxAlways(cfg, "vscode") {
		t.Fatal("manifest recommendation activated normal launches without package entry")
	}
}

func TestSandboxActivationDefaultsAlwaysAndSupportsOnDemand(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"always":    {},
		"explicit":  {Activation: "always"},
		"on-demand": {Activation: "on-demand"},
	}}}
	if !sandboxAlways(cfg, "always") || !sandboxAlways(cfg, "explicit") {
		t.Error("present package should default to always activation")
	}
	if sandboxAlways(cfg, "on-demand") || sandboxAlways(cfg, "absent") {
		t.Error("on-demand and absent packages must not affect normal launches")
	}
}

func TestPackageSelectsBuiltinProfileAndOverridesIt(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"codex": {
			Profile: config.SandboxProfileOnlineCLI,
			SandboxPolicy: config.SandboxPolicy{
				Features: map[string]bool{"audio": true},
			},
		},
	}}}
	got, err := ResolvePackageSandbox(cfg, "codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.feature("network") || got.feature("x11") || !got.feature("audio") {
		t.Errorf("built-in profile or inline override not applied: %+v", got)
	}
}

func TestResolvePackageSandboxLayersWithoutReplacing(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{
		Profiles: map[string]config.SandboxPolicy{
			"strict": {
				Home:     "isolated",
				Hide:     []string{"~/profile-secret"},
				Features: map[string]bool{"network": false, "audio": false},
			},
		},
		Packages: map[string]config.SandboxPackage{
			"vscode": {
				Activation: config.SandboxActivationOnDemand,
				Profile:    "strict",
				SandboxPolicy: config.SandboxPolicy{
					Hide:     []string{"~/package-secret"},
					Features: map[string]bool{"audio": true},
				},
			},
		},
	}}
	recommended := &manifest.SandboxPolicy{
		Home:     "shared",
		Hide:     []string{"~/.ssh"},
		Features: map[string]bool{"network": true, "wayland": true},
	}

	got, err := ResolvePackageSandbox(cfg, "vscode", recommended)
	if err != nil {
		t.Fatal(err)
	}
	if got.Home != "isolated" {
		t.Errorf("Home = %q, want profile override isolated", got.Home)
	}
	if sandboxAlways(cfg, "vscode") {
		t.Error("on-demand package override unexpectedly activated normal launches")
	}
	if got.Features["network"] || !got.Features["audio"] || !got.Features["wayland"] {
		t.Errorf("unexpected merged features: %v", got.Features)
	}
	for _, want := range []string{"~/.ssh", "~/profile-secret", "~/package-secret"} {
		if !slices.Contains(got.Hide, want) {
			t.Errorf("Hide = %v, missing %q", got.Hide, want)
		}
	}
}

func TestResolvePackageSandboxUsesRecommendedProfile(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{
		Profiles: map[string]config.SandboxPolicy{"restricted": {Features: map[string]bool{"network": false}}},
		Packages: map[string]config.SandboxPackage{"vscode": {}},
	}}
	got, err := ResolvePackageSandbox(cfg, "vscode", &manifest.SandboxPolicy{Profile: "restricted"})
	if err != nil {
		t.Fatal(err)
	}
	if got.feature("network") {
		t.Error("recommended profile was not applied")
	}
}

func TestResolvePackageSandboxUnknownRecommendedProfile(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{
		Packages: map[string]config.SandboxPackage{"vscode": {}},
	}}
	if _, err := ResolvePackageSandbox(cfg, "vscode", &manifest.SandboxPolicy{Profile: "missing"}); err == nil {
		t.Fatal("expected unknown recommended profile to fail when package is enabled")
	}
}

func TestSandboxArgsIsolatesHomeAndDisablesFeatures(t *testing.T) {
	root := t.TempDir()
	hostHome := t.TempDir()
	secret := filepath.Join(hostHome, ".ssh")
	if err := mkdir(secret); err != nil {
		t.Fatal(err)
	}
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "vscode"},
		BinPath:  "/opt/vscode/code",
		CmdArgs:  []string{"--version"},
		Vars:     map[string]string{"data": root},
	}
	policy := &PackageSandbox{
		Home: "isolated",
		Hide: []string{"~/.ssh"},
		Features: map[string]bool{
			"network": false, "x11": false, "wayland": false,
			"dbus": false, "audio": false,
		},
	}
	args, err := sandboxArgs(p, policy, "/work", hostHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"--dev-bind", "/", "/"},
		{"--unshare-net"},
		{"--setenv", "HOME", filepath.Join(root, "home")},
		{"--tmpfs", secret},
		{"--unsetenv", "DISPLAY"},
		{"--unsetenv", "WAYLAND_DISPLAY"},
		{"--chdir", "/work", "--", "/opt/vscode/code", "--version"},
	} {
		if indexSequence(args, want) < 0 {
			t.Errorf("args missing %v: %v", want, args)
		}
	}
}

func TestSandboxArgsSharedHomeAndDefaultFeatures(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "vscode"},
		BinPath:  "/opt/vscode/code",
		Vars:     map[string]string{"data": t.TempDir()},
	}
	args, err := sandboxArgs(p, &PackageSandbox{Home: "shared", Features: map[string]bool{}}, "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "--unshare-net") || indexSequence(args, []string{"--setenv", "HOME"}) >= 0 || slices.Contains(args, "--unsetenv") {
		t.Fatalf("shared home with default features should retain native integration: %v", args)
	}
}

func TestNestedSandboxUsesChildHomeWithoutAnotherLayer(t *testing.T) {
	root := t.TempDir()
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "node-22"},
		BinPath:  "/opt/node/bin/node",
		Vars:     map[string]string{"data": root},
		Env: []string{
			"HOME=/code/home", "DISPLAY=:99", "WAYLAND_DISPLAY=wayland-0",
			"BUNNY_INTERNAL_LAYOUT=1", "BUNNY_INTERNAL_DATA=/host/data",
		},
		BunnyEnv: []string{"BUNNY_INTERNAL_LAYOUT=1", "BUNNY_INTERNAL_DATA=/host/data"},
	}
	current := sandboxContext{
		Packages:         []string{"vscode"},
		HostHome:         "/host/home",
		DisabledFeatures: []string{"x11"},
	}
	plan, err := buildSandboxPlan(p, &PackageSandbox{Home: "isolated", Features: map[string]bool{}}, "/work", "/code/home", current)
	if err != nil {
		t.Fatal(err)
	}
	if plan.needsLayer || len(plan.args) != 0 {
		t.Fatalf("environment-only child should not add bwrap layer: %+v", plan)
	}
	if !slices.Contains(plan.env, "HOME="+filepath.Join(root, "home")) {
		t.Errorf("child HOME missing from env: %v", plan.env)
	}
	if slices.Contains(plan.env, "DISPLAY=:99") {
		t.Errorf("child restored outer-disabled X11: %v", plan.env)
	}
	if !slices.Contains(plan.env, "WAYLAND_DISPLAY=wayland-0") {
		t.Errorf("unrestricted inherited environment was lost: %v", plan.env)
	}
	if !slices.Contains(plan.env, "BUNNY_INTERNAL_DATA=/host/data") {
		t.Errorf("Bunny path anchors were lost: %v", plan.env)
	}
	context, err := readSandboxContext(plan.env)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(context.Packages, []string{"vscode", "node-22"}) || context.HostHome != "/host/home" {
		t.Errorf("unexpected propagated context: %+v", context)
	}
}

func TestNestedSandboxAddsOnlyNewMountAndNamespaceRestrictions(t *testing.T) {
	hostHome := t.TempDir()
	alreadyHidden := filepath.Join(hostHome, ".ssh")
	newHidden := filepath.Join(hostHome, ".npmrc")
	if err := mkdir(newHidden); err != nil {
		t.Fatal(err)
	}
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "node-22"},
		BinPath:  "/opt/node/bin/node",
		Vars:     map[string]string{"data": t.TempDir()},
	}
	current := sandboxContext{Packages: []string{"vscode"}, HostHome: hostHome, Hidden: []string{alreadyHidden}}
	policy := &PackageSandbox{
		Home:     "isolated",
		Hide:     []string{"~/.ssh", "~/.npmrc"},
		Features: map[string]bool{"network": false},
	}
	plan, err := buildSandboxPlan(p, policy, "/work", "/wrong/home", current)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.needsLayer || !slices.Contains(plan.args, "--unshare-net") {
		t.Fatalf("new network restriction should add a layer: %v", plan.args)
	}
	if indexSequence(plan.args, []string{"--tmpfs", newHidden}) < 0 {
		t.Errorf("new hidden path was not mounted: %v", plan.args)
	}
	if indexSequence(plan.args, []string{"--tmpfs", alreadyHidden}) >= 0 {
		t.Errorf("outer hidden path was redundantly mounted: %v", plan.args)
	}
}

func TestDirectChildCannotRestoreOuterDisabledEnvironment(t *testing.T) {
	encoded, err := json.Marshal(sandboxContext{
		Packages:         []string{"vscode"},
		HostHome:         "/host/home",
		DisabledFeatures: []string{"x11", "audio"},
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := inheritSandboxEnv([]string{
		sandboxContextEnv + "=" + string(encoded),
		"DISPLAY=:99", "PULSE_SERVER=restored", "WAYLAND_DISPLAY=wayland-0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(env, "DISPLAY=:99") || slices.Contains(env, "PULSE_SERVER=restored") {
		t.Errorf("direct child restored an outer-disabled integration: %v", env)
	}
	if !slices.Contains(env, "WAYLAND_DISPLAY=wayland-0") {
		t.Errorf("direct child lost an allowed integration: %v", env)
	}
}

func mkdir(path string) error { return os.MkdirAll(path, 0755) }

func indexSequence(values, sequence []string) int {
	for i := 0; i+len(sequence) <= len(values); i++ {
		if slices.Equal(values[i:i+len(sequence)], sequence) {
			return i
		}
	}
	return -1
}
