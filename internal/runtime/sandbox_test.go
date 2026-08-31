package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/manifest"
)

// Policy resolves for a package with no config entry at all — that is what
// `bunny run --sandbox-profile <name> <id>` relies on — without that
// resolution implying the sandbox applies to ordinary launches.
func TestPolicyResolvesForUnconfiguredPackageWithoutActivatingIt(t *testing.T) {
	cfg := &config.Config{}
	got, err := ResolvePackageSandbox(cfg, "vscode", config.SandboxProfileDesktop)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Home != "isolated" {
		t.Fatalf("--sandbox-profile did not resolve the built-in: %+v", got)
	}
	if sandboxActivated(cfg, "vscode") {
		t.Fatal("resolving a policy must not activate normal launches")
	}
}

// Presence under sandbox.packages is the whole activation rule: there is no
// activation field, so a package that should stay direct is simply absent.
func TestSandboxActivationIsPackageMapPresence(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"present":      {},
		"with-profile": {Profile: config.SandboxProfileDesktop},
	}}}
	if !sandboxActivated(cfg, "present") || !sandboxActivated(cfg, "with-profile") {
		t.Error("a present package entry activates normal launches")
	}
	if sandboxActivated(cfg, "absent") {
		t.Error("an absent package must not affect normal launches")
	}
	if sandboxActivated(nil, "present") {
		t.Error("no config means no activation")
	}
}

func TestPackageSelectsBuiltinProfileAndOverridesIt(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"codex": {
			Profile: config.SandboxProfileAgent,
			SandboxPolicy: config.SandboxPolicy{
				Features: map[string]bool{"audio": true},
			},
		},
	}}}
	got, err := ResolvePackageSandbox(cfg, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Net.Mode != "host" || got.feature("x11") || !got.feature("audio") {
		t.Errorf("built-in profile or inline override not applied: %+v", got)
	}
}

// The agent profile is the only hardened built-in, and the only one that
// grants the working directory. Both have to survive profile resolution, or
// the profile silently degrades into a plain isolated launch.
func TestAgentProfileIsHardenedWithWritableCwd(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"claude": {Profile: config.SandboxProfileAgent},
	}}}
	got, err := ResolvePackageSandbox(cfg, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Boundary != "hardened" {
		t.Errorf("agent profile must be hardened, got %q", got.Boundary)
	}
	if got.FS.Cwd != "write" {
		t.Errorf("agent profile must grant the working directory, got cwd %q", got.FS.Cwd)
	}
	if got.Net.Mode != "host" {
		t.Errorf("agent profile needs the network for the model API, got %q", got.Net.Mode)
	}
	if got.feature("agents") {
		t.Error("agent profile must not expose credential agents")
	}
	// The boundary mandates a new session and PID namespace, so tty is forced
	// off and the profile says so rather than asking for something it cannot
	// have. An interactive TUI still renders over inherited stdio.
	if got.feature("tty") {
		t.Error("hardened boundary forces tty off; the agent profile must not claim otherwise")
	}
	if got.Home != "isolated" {
		t.Errorf("agent config must persist across runs, got home %q", got.Home)
	}
}

func TestBuiltinProfilesSetAgentsAndTTY(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"tool": {Profile: config.SandboxProfileOffline},
	}}}
	got, err := ResolvePackageSandbox(cfg, "tool", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.feature("agents") {
		t.Error("offline must disable credential agents")
	}
	if !got.feature("tty") {
		t.Error("offline must keep the controlling terminal")
	}
	// Asserted on the *resolved* policy, not just the profile definition:
	// offline expressed network through features.network until that alias
	// was removed, and a stale feature key would leave it silently online.
	if got.Net.Mode != "none" {
		t.Errorf("offline must isolate the network, got mode %q", got.Net.Mode)
	}
	for _, profile := range []string{
		config.SandboxProfileDesktop, config.SandboxProfileEphemeral, config.SandboxProfileClean,
	} {
		cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
			"tool": {Profile: profile},
		}}}
		got, err := ResolvePackageSandbox(cfg, "tool", "")
		if err != nil {
			t.Fatal(err)
		}
		if !got.feature("agents") || !got.feature("tty") {
			t.Errorf("profile %s should keep agents and tty enabled: %+v", profile, got.Features)
		}
	}
}

// The two remaining layers compose as documented: the profile supplies the
// base, the package's inline override wins per key for home/features, and
// hide accumulates across both rather than replacing.
func TestResolvePackageSandboxLayersProfileThenInlineOverride(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{
		Profiles: map[string]config.SandboxPolicy{
			"strict": {
				Home:     "shared",
				Hide:     []string{"~/profile-secret"},
				Features: map[string]bool{"audio": false, "wayland": false},
				Net:      &manifest.SandboxNet{Mode: "none"},
			},
		},
		Packages: map[string]config.SandboxPackage{
			"vscode": {
				Profile: "strict",
				SandboxPolicy: config.SandboxPolicy{
					Home:     "isolated",
					Hide:     []string{"~/package-secret"},
					Features: map[string]bool{"audio": true},
				},
			},
		},
	}}
	got, err := ResolvePackageSandbox(cfg, "vscode", "")
	if err != nil {
		t.Fatal(err)
	}
	// Inline override wins over the profile for a key both set.
	if got.Home != "isolated" {
		t.Errorf("Home = %q, want inline override isolated", got.Home)
	}
	if !got.Features["audio"] {
		t.Errorf("inline features override not applied: %v", got.Features)
	}
	// A key only the profile set survives.
	if got.Features["wayland"] {
		t.Errorf("profile-only feature lost: %v", got.Features)
	}
	if got.Net.Mode != "none" {
		t.Errorf("profile net.mode lost: %q", got.Net.Mode)
	}
	// hide accumulates across layers instead of replacing.
	for _, want := range []string{"~/profile-secret", "~/package-secret"} {
		if !slices.Contains(got.Hide, want) {
			t.Errorf("Hide = %v, missing %q", got.Hide, want)
		}
	}
}

func TestResolvePackageSandboxUnknownProfileFails(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{
		Packages: map[string]config.SandboxPackage{"vscode": {Profile: "missing"}},
	}}
	if _, err := ResolvePackageSandbox(cfg, "vscode", ""); err == nil {
		t.Fatal("expected an unknown configured profile to fail")
	}
	if _, err := ResolvePackageSandbox(&config.Config{}, "vscode", "missing"); err == nil {
		t.Fatal("expected an unknown --sandbox-profile to fail")
	}
}

func TestFinalizeRejectsPersistWithoutEphemeralHome(t *testing.T) {
	p := &PackageSandbox{Home: "isolated", Features: map[string]bool{}, Persist: []string{".claude/memory"}}
	if err := p.finalize("tool"); err == nil {
		t.Fatal("persist without home: ephemeral must be rejected")
	}
}

func TestFinalizeRejectsPathBothHiddenAndPersisted(t *testing.T) {
	p := &PackageSandbox{
		Home: "ephemeral", Features: map[string]bool{},
		Hide: []string{".claude/memory"}, Persist: []string{".claude/memory"},
	}
	if err := p.finalize("tool"); err == nil {
		t.Fatal("a path in both hide and persist must be rejected")
	}
}

// hide and persist can each be set in a different policy layer (a package
// override hiding what a profile persists, say); the merged-layer check in
// finalize must catch the conflict even when the two layers spelled the same
// path differently, unlike the same-layer check in manifest validation which
// only ever sees one literal spelling per layer.
func TestFinalizeRejectsPathBothHiddenAndPersistedAcrossLexicalForms(t *testing.T) {
	p := &PackageSandbox{
		Home: "ephemeral", Features: map[string]bool{},
		Hide: []string{"bar"}, Persist: []string{"foo/../bar"},
	}
	if err := p.finalize("tool"); err == nil {
		t.Fatal("equivalent hide/persist paths in different lexical forms must be rejected")
	}
}

func TestResolvePackageSandboxProfileOverride(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{
		Profiles: map[string]config.SandboxPolicy{
			"claude-persist": {Home: "isolated"},
			"claude-scratch": {Home: "ephemeral", Persist: []string{".claude/memory"}},
		},
		Packages: map[string]config.SandboxPackage{
			"claude": {Profile: "claude-persist"},
		},
	}}
	got, err := ResolvePackageSandbox(cfg, "claude", "claude-scratch")
	if err != nil {
		t.Fatal(err)
	}
	if got.Home != "ephemeral" || !slices.Equal(got.Persist, []string{".claude/memory"}) {
		t.Errorf("--sandbox-profile override did not take effect: %+v", got)
	}
	// Without an override the package's configured profile still applies.
	got, err = ResolvePackageSandbox(cfg, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Home != "isolated" {
		t.Errorf("configured profile should apply without an override: %+v", got)
	}
}

// bunny run --sandbox-profile ephemeral <id> must work ad hoc on a package
// with no sandbox.packages entry at all: the built-in profile is available
// without any config, and a profileOverride forces it regardless of
// activation.
func TestResolvePackageSandboxEphemeralProfileOnUnconfiguredPackage(t *testing.T) {
	got, err := ResolvePackageSandbox(&config.Config{}, "gh", config.SandboxProfileEphemeral)
	if err != nil {
		t.Fatal(err)
	}
	if got.Home != "ephemeral" || len(got.Persist) != 0 {
		t.Errorf("built-in ephemeral profile did not apply: %+v", got)
	}
	if got.Net.Mode != "host" || !got.feature("x11") {
		t.Errorf("built-in ephemeral profile should stay as permissive as desktop: %+v", got)
	}
}

func TestResolvePackageSandboxUnknownProfileOverride(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{"claude": {}}}}
	if _, err := ResolvePackageSandbox(cfg, "claude", "does-not-exist"); err == nil {
		t.Fatal("expected an unknown --sandbox-profile override to fail")
	}
}

func TestSandboxArgsEphemeralHomeOverlaysAndPersists(t *testing.T) {
	root := t.TempDir()
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "claude"},
		BinPath:  "/opt/claude/claude",
		Vars:     map[string]string{"data": root},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	home := filepath.Join(root, "home")
	persistPath := filepath.Join(home, ".claude", "memory")
	// A persist entry must already exist in the seed, as if a prior
	// persistent (home: isolated) launch had established it.
	if err := mkdir(persistPath); err != nil {
		t.Fatal(err)
	}
	policy := finalized(t, &PackageSandbox{Home: "ephemeral", Persist: []string{".claude/memory"}})
	args, err := sandboxArgs(p, policy, "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(args, []string{"--overlay-src", home, "--tmp-overlay", home}) < 0 {
		t.Errorf("ephemeral home missing overlay mount: %v", args)
	}
	if indexSequence(args, []string{"--bind", persistPath, persistPath}) < 0 {
		t.Errorf("persist path missing bind-through: %v", args)
	}
	if v, _ := setenvValue(args, "HOME"); v != home {
		t.Errorf("HOME = %q, want %q", v, home)
	}
}

// A persist entry that does not yet exist must fail closed, exactly like a
// missing hide path, rather than have Bunny guess a type and create it —
// which would also mean a read-only --explain mutates the host.
func TestPersistPathMustAlreadyExist(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "claude"},
		BinPath:  "/opt/claude/claude",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	policy := finalized(t, &PackageSandbox{Home: "ephemeral", Persist: []string{".claude/memory"}})
	_, err := sandboxArgs(p, policy, "/work", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing persist path must fail closed, got %v", err)
	}
	home := filepath.Join(p.Vars["data"], "home")
	if _, statErr := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(statErr) {
		t.Errorf("planning must not create the persist path on failure: %v", statErr)
	}
}

// A persist entry may be an existing regular file (a "memory file"), not
// just a directory; Bunny must preserve whatever type is already there.
func TestPersistPathPreservesExistingFileType(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	memory := filepath.Join(home, ".claude", "memory.md")
	if err := mkdir(filepath.Dir(memory)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memory, []byte("notes"), 0644); err != nil {
		t.Fatal(err)
	}
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "claude"},
		BinPath:  "/opt/claude/claude",
		Vars:     map[string]string{"data": root},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	policy := finalized(t, &PackageSandbox{Home: "ephemeral", Persist: []string{".claude/memory.md"}})
	args, err := sandboxArgs(p, policy, "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(args, []string{"--bind", memory, memory}) < 0 {
		t.Errorf("persisted file missing bind-through: %v", args)
	}
	if info, err := os.Stat(memory); err != nil || info.IsDir() {
		t.Errorf("persist must not turn an existing file into a directory: %v", err)
	}
}

// A persist entry that resolves outside the home through a symlink must be
// refused: a package run with an isolated home could plant such a symlink
// during an earlier run, and an ephemeral run must not follow it back out to
// an arbitrary host path.
func TestPersistPathSymlinkEscapeRefused(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := mkdir(filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, ".claude", "memory")); err != nil {
		t.Fatal(err)
	}
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "claude"},
		BinPath:  "/opt/claude/claude",
		Vars:     map[string]string{"data": root},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	policy := finalized(t, &PackageSandbox{Home: "ephemeral", Persist: []string{".claude/memory"}})
	if _, err := sandboxArgs(p, policy, "/work", t.TempDir()); err == nil {
		t.Fatal("persist path escaping home through a symlink must be refused")
	}
}

// home: clean needs no pre-existing seed at all — unlike ephemeral, it is a
// bare tmpfs with no lower layer — so this deliberately does not create
// anything under {data}/home first.
func TestSandboxArgsCleanHomeReplacesHomeWithTmpfs(t *testing.T) {
	root := t.TempDir()
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "claude"},
		BinPath:  "/opt/claude/claude",
		Vars:     map[string]string{"data": root},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	home := filepath.Join(root, "home")
	policy := finalized(t, &PackageSandbox{Home: "clean"})
	args, err := sandboxArgs(p, policy, "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(args, []string{"--tmpfs", home}) < 0 {
		t.Errorf("clean home missing tmpfs mount: %v", args)
	}
	if v, _ := setenvValue(args, "HOME"); v != home {
		t.Errorf("HOME = %q, want %q", v, home)
	}
}

// A persist entry has nothing to punch a hole back to under home: clean —
// there is no seed — so it must be rejected the same way it is for isolated
// and shared, not silently ignored.
func TestFinalizeRejectsPersistWithCleanHome(t *testing.T) {
	p := &PackageSandbox{Home: "clean", Features: map[string]bool{}, Persist: []string{".claude/memory"}}
	if err := p.finalize("tool"); err == nil {
		t.Fatal("persist with home: clean must be rejected")
	}
}

func TestExplainSandboxReportsEphemeralHomeAndPersist(t *testing.T) {
	root := t.TempDir()
	// Seed the persist path as a prior persistent launch would; --explain
	// must not be the thing that creates it (see TestExplainSandboxIsReadOnly).
	if err := mkdir(filepath.Join(root, "home", ".claude", "memory")); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"claude": {SandboxPolicy: config.SandboxPolicy{Home: "ephemeral", Persist: []string{".claude/memory"}}},
	}}}
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "claude"},
		BinPath:  "/opt/claude/claude",
		Vars:     map[string]string{"data": root},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	out, err := ExplainSandbox(p, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ephemeral") {
		t.Errorf("--explain must report the ephemeral home mode: %s", out)
	}
	if !strings.Contains(out, "persist") || !strings.Contains(out, ".claude/memory") {
		t.Errorf("--explain must report the persist paths: %s", out)
	}
}

// --explain must never touch the host: it plans and formats only. A prior
// bug had planning auto-create persist directories, which fired even for
// --explain since it shares the same planPackageSandbox call as a real
// launch.
func TestExplainSandboxIsReadOnly(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"claude": {SandboxPolicy: config.SandboxPolicy{Home: "ephemeral", Persist: []string{".claude/memory"}}},
	}}}
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "claude"},
		BinPath:  "/opt/claude/claude",
		Vars:     map[string]string{"data": root},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	// The persist path does not exist yet, so explain resolves policy and
	// reports the ephemeral home fine, but planning the persist bind fails —
	// and either way must not have created anything under {data}.
	if _, err := ExplainSandbox(p, cfg, ""); err == nil {
		t.Fatal("expected --explain to surface the missing persist path as an error")
	}
	if _, statErr := os.Stat(home); !os.IsNotExist(statErr) {
		t.Errorf("--explain must not create the isolated/ephemeral home on disk: %v", statErr)
	}
}

// Explain must report a plain `bunny run` accurately: a package with no
// forced sandbox and no configured "always" activation runs directly, so it
// must say that rather than the plan a forced launch would produce.
func TestExplainReportsDirectRunWhenNotSandboxed(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "node-22"},
		BinPath:  "/opt/node/bin/node",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	out, err := Explain(p, &config.Config{}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "runs directly") {
		t.Errorf("expected a direct-run report, got: %s", out)
	}
}

// --sandbox forces the full plan even for a package with no configured
// activation, matching ExecPackageSandboxed.
func TestExplainReportsSandboxPlanWhenForced(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "node-22"},
		BinPath:  "/opt/node/bin/node",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	out, err := Explain(p, &config.Config{}, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "runs directly") {
		t.Errorf("--sandbox must force the full plan, got: %s", out)
	}
	if !strings.Contains(out, "boundary") {
		t.Errorf("expected the full sandbox report, got: %s", out)
	}
}

// A package configured for "always" activation is already sandboxed on a
// plain `bunny run`; Explain must report that plan without needing --sandbox.
func TestExplainReportsSandboxPlanWhenAlwaysActivated(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "claude"},
		BinPath:  "/opt/claude/claude",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{"claude": {}}}}
	out, err := Explain(p, cfg, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "runs directly") {
		t.Errorf("always-activated package must report the full plan, got: %s", out)
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
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir(), "DISPLAY=:0", "WAYLAND_DISPLAY=wayland-0"},
	}
	policy := finalized(t, &PackageSandbox{
		Home: "isolated",
		Hide: []string{"~/.ssh"},
		Net:  NetPolicy{Mode: "none"},
		Features: map[string]bool{
			"x11": false, "wayland": false,
			"dbus": false, "audio": false,
		},
	})
	args, err := sandboxArgs(p, policy, "/work", hostHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"--dev-bind", "/", "/"},
		{"--unshare-net"},
		{"--clearenv"},
		{"--setenv", "HOME", filepath.Join(root, "home")},
		{"--tmpfs", secret},
		{"--chdir", "/work", "--", "/opt/vscode/code", "--version"},
	} {
		if indexSequence(args, want) < 0 {
			t.Errorf("args missing %v: %v", want, args)
		}
	}
	// Disabled-feature variables are dropped from the payload env, not
	// re-set: --clearenv means their absence is the removal.
	for _, name := range []string{"DISPLAY", "WAYLAND_DISPLAY"} {
		if _, ok := setenvValue(args, name); ok {
			t.Errorf("disabled feature variable %q must not be set for the payload: %v", name, args)
		}
	}
}

func TestSandboxArgsSharedHomeAndDefaultFeatures(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "vscode"},
		BinPath:  "/opt/vscode/code",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	args, err := sandboxArgs(p, finalized(t, &PackageSandbox{Home: "shared"}), "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(args, "--unshare-net") || slices.Contains(args, "--tmpfs") ||
		indexSequence(args, []string{"--setenv", "HOME"}) >= 0 ||
		indexSequence(args, []string{"--unsetenv", "DISPLAY"}) >= 0 ||
		slices.Contains(args, "--new-session") {
		t.Fatalf("shared home with default features should retain native integration: %v", args)
	}
}

func TestDisabledFeaturesMaskDocumentedEndpoints(t *testing.T) {
	runtimeDir := t.TempDir()
	hostHome := t.TempDir()
	busSocket := filepath.Join(runtimeDir, "bus")
	waylandSocket := filepath.Join(runtimeDir, "wayland-0")
	waylandLock := filepath.Join(runtimeDir, "wayland-0.lock")
	pulseDir := filepath.Join(runtimeDir, "pulse")
	pipewireSocket := filepath.Join(runtimeDir, "pipewire-0")
	gnupgDir := filepath.Join(runtimeDir, "gnupg")
	keyringDir := filepath.Join(runtimeDir, "keyring")
	sshSocket := filepath.Join(runtimeDir, "gcr-ssh")
	xauthority := filepath.Join(hostHome, ".Xauthority")
	for _, dir := range []string{pulseDir, gnupgDir, keyringDir} {
		if err := mkdir(dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{busSocket, waylandSocket, waylandLock, pipewireSocket, sshSocket, xauthority} {
		if err := os.WriteFile(file, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}

	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "tool"},
		BinPath:  "/opt/tool/tool",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + runtimeDir, "SSH_AUTH_SOCK=" + sshSocket},
	}
	policy := finalized(t, &PackageSandbox{Home: "isolated", Features: map[string]bool{
		"x11": false, "wayland": false, "dbus": false, "audio": false, "agents": false,
	}})
	args, err := sandboxArgs(p, policy, "/work", hostHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"--ro-bind", "/dev/null", busSocket},
		{"--ro-bind", "/dev/null", waylandSocket},
		{"--ro-bind", "/dev/null", waylandLock},
		{"--tmpfs", pulseDir},
		{"--ro-bind", "/dev/null", pipewireSocket},
		{"--tmpfs", gnupgDir},
		{"--tmpfs", keyringDir},
		{"--ro-bind", "/dev/null", sshSocket},
		{"--ro-bind", "/dev/null", xauthority},
	} {
		if indexSequence(args, want) < 0 {
			t.Errorf("args missing %v: %v", want, args)
		}
	}
	// SSH_AUTH_SOCK is a real value here; disabling agents must keep it out
	// of the payload env, which --clearenv does by simply not re-setting it.
	if _, ok := setenvValue(args, "SSH_AUTH_SOCK"); ok {
		t.Errorf("agents: false must not set SSH_AUTH_SOCK for the payload: %v", args)
	}
}

func TestNetworkFalseForcesDBusMasksWithoutUnsettingItsVariable(t *testing.T) {
	runtimeDir := t.TempDir()
	busSocket := filepath.Join(runtimeDir, "bus")
	if err := os.WriteFile(busSocket, nil, 0600); err != nil {
		t.Fatal(err)
	}
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "tool"},
		BinPath:  "/opt/tool/tool",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + runtimeDir},
	}
	p.Env = append(p.Env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+busSocket)
	policy := finalized(t, &PackageSandbox{
		Home: "isolated", Net: NetPolicy{Mode: "none"},
		Features: map[string]bool{"dbus": true},
	})
	args, err := sandboxArgs(p, policy, "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(args, []string{"--ro-bind", "/dev/null", busSocket}) < 0 {
		t.Errorf("network isolation did not force the user bus mask: %v", args)
	}
	// The masks are the enforcement; a scoped policy that left dbus enabled
	// keeps the address variable for the payload rather than misreporting.
	if v, ok := setenvValue(args, "DBUS_SESSION_BUS_ADDRESS"); !ok || v != "unix:path="+busSocket {
		t.Errorf("dbus left enabled must keep its variable for the payload: %v", args)
	}
}

func TestMissingHidePathFailsClosed(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "tool"},
		BinPath:  "/opt/tool/tool",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	policy := finalized(t, &PackageSandbox{Home: "isolated", Hide: []string{"~/.does-not-exist"}})
	_, err := sandboxArgs(p, policy, "/work", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing hide path must fail closed, got %v", err)
	}
}

func TestTTYFalseAddsProcessIsolation(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "tool"},
		BinPath:  "/opt/tool/tool",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	args, err := sandboxArgs(p, finalized(t, &PackageSandbox{Home: "isolated", Features: map[string]bool{"tty": false}}), "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{{"--new-session"}, {"--unshare-pid"}, {"--proc", "/proc"}} {
		if indexSequence(args, want) < 0 {
			t.Errorf("tty: false missing %v: %v", want, args)
		}
	}
}

func TestConfigFileBoundReadOnlyOnlyWhenPresent(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	p := &Prepared{
		Manifest:   &manifest.Manifest{ID: "tool"},
		BinPath:    "/opt/tool/tool",
		Vars:       map[string]string{"data": t.TempDir()},
		Env:        []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
		ConfigFile: configFile,
	}
	policy := finalized(t, &PackageSandbox{Home: "isolated"})

	args, err := sandboxArgs(p, policy, "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(args, []string{"--ro-bind", configFile, configFile}) >= 0 {
		t.Errorf("absent optional config must not be bound or created: %v", args)
	}

	if err := os.WriteFile(configFile, []byte("sandbox: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	args, err = sandboxArgs(p, policy, "/work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(args, []string{"--ro-bind", configFile, configFile}) < 0 {
		t.Errorf("existing config must be bound read-only: %v", args)
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
			"XDG_RUNTIME_DIR=" + t.TempDir(),
			"BUNNY_INTERNAL_LAYOUT=1", "BUNNY_INTERNAL_DATA=/host/data",
		},
		BunnyEnv: []string{"BUNNY_INTERNAL_LAYOUT=1", "BUNNY_INTERNAL_DATA=/host/data"},
	}
	current := sandboxContext{
		Packages:         []string{"vscode"},
		HostHome:         "/host/home",
		DisabledFeatures: []string{"x11"},
	}
	plan, err := buildSandboxPlan(p, finalized(t, &PackageSandbox{Home: "isolated"}), "/work", "/code/home", current)
	if err != nil {
		t.Fatal(err)
	}
	if plan.needsLayer || len(plan.args) != 0 {
		t.Fatalf("environment-only child should not add bwrap layer: %+v", plan)
	}
	if !slices.Contains(plan.env, "HOME="+filepath.Join(root, "home")) {
		t.Errorf("child HOME missing from env: %v", plan.env)
	}
	if plan.isolatedHome != filepath.Join(root, "home") {
		t.Errorf("nested plan must still name its own home: %q", plan.isolatedHome)
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
	if !slices.Equal(plan.context.Packages, []string{"vscode", "node-22"}) || plan.context.HostHome != "/host/home" {
		t.Errorf("unexpected propagated context: %+v", plan.context)
	}
}

func TestForgedEnvironmentContextIsIgnoredAndStripped(t *testing.T) {
	mountTestContextFile(t, "") // no mounted context anywhere
	forged, err := json.Marshal(sandboxContext{Packages: []string{"vscode"}, DisabledFeatures: []string{"network"}})
	if err != nil {
		t.Fatal(err)
	}
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "tool"},
		BinPath:  "/opt/tool/tool",
		Vars:     map[string]string{"data": t.TempDir()},
		Env: []string{
			legacySandboxContextEnv + "=" + string(forged),
			"XDG_RUNTIME_DIR=" + t.TempDir(),
		},
	}
	policy := finalized(t, &PackageSandbox{Home: "isolated", Net: NetPolicy{Mode: "none"}})
	context, err := readMountedContext()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildSandboxPlan(p, policy, "/work", t.TempDir(), context)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.needsLayer || !slices.Contains(plan.args, "--unshare-net") {
		t.Fatalf("forged environment context must not elide restrictions: %+v", plan)
	}
	for _, entry := range plan.env {
		if strings.HasPrefix(entry, legacySandboxContextEnv+"=") {
			t.Errorf("legacy context variable leaked into child env: %q", entry)
		}
	}
	// --clearenv drops the whole inherited environment; the legacy variable
	// is simply never re-set, so it cannot reach the payload.
	if !slices.Contains(plan.args, "--clearenv") {
		t.Errorf("layer must clear the inherited environment: %v", plan.args)
	}
	if _, ok := setenvValue(plan.args, legacySandboxContextEnv); ok {
		t.Errorf("layer must not re-set the legacy context variable: %v", plan.args)
	}
}

func TestMalformedMountedContextFailsLaunch(t *testing.T) {
	path := mountTestContextFile(t, "{not json")
	if _, err := readSandboxContextFile(path); err == nil {
		t.Fatal("malformed mounted context must be a launch error")
	}
	if _, err := inheritSandboxEnv(nil); err == nil {
		t.Fatal("direct launch must fail on a malformed mounted context")
	}
}

func TestMountedContextRoundTrip(t *testing.T) {
	want := sandboxContext{
		Packages:         []string{"vscode"},
		HostHome:         "/host/home",
		Hidden:           []string{"/host/home/.ssh"},
		DisabledFeatures: []string{"network", "x11"},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	mountTestContextFile(t, string(data))
	got, err := readMountedContext()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Packages, want.Packages) || !slices.Equal(got.Hidden, want.Hidden) ||
		!slices.Equal(got.DisabledFeatures, want.DisabledFeatures) || got.HostHome != want.HostHome {
		t.Errorf("context round trip mismatch: %+v", got)
	}
}

func TestDirectChildCannotRestoreOuterDisabledEnvironment(t *testing.T) {
	context := sandboxContext{
		Packages:         []string{"vscode"},
		HostHome:         "/host/home",
		DisabledFeatures: []string{"x11", "audio"},
	}
	env := inheritedSandboxEnv([]string{
		"DISPLAY=:99", "PULSE_SERVER=restored", "WAYLAND_DISPLAY=wayland-0",
	}, context)
	if slices.Contains(env, "DISPLAY=:99") || slices.Contains(env, "PULSE_SERVER=restored") {
		t.Errorf("direct child restored an outer-disabled integration: %v", env)
	}
	if !slices.Contains(env, "WAYLAND_DISPLAY=wayland-0") {
		t.Errorf("direct child lost an allowed integration: %v", env)
	}
}

// finalized resolves a hand-built test policy the way ResolvePackageSandbox
// does, so plans see concrete modes and boundary-sensitive defaults.
func finalized(t *testing.T, p *PackageSandbox) *PackageSandbox {
	t.Helper()
	if p.Home == "" {
		p.Home = "isolated"
	}
	if p.Features == nil {
		p.Features = map[string]bool{}
	}
	if err := p.finalize("test"); err != nil {
		t.Fatal(err)
	}
	return p
}

// mountTestContextFile points the fixed context location at a scratch file
// holding content, or at a nonexistent path when content is empty. It stands
// in for the read-only file a real sandbox layer mounts.
func mountTestContextFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sandbox-context.json")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := sandboxContextFile
	sandboxContextFile = path
	t.Cleanup(func() { sandboxContextFile = old })
	return path
}

// mountTestContext installs an encoded context, as an enclosing layer would.
func mountTestContext(t *testing.T, context sandboxContext) {
	t.Helper()
	data, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	mountTestContextFile(t, string(data))
}

func mkdir(path string) error { return os.MkdirAll(path, 0755) }

// setenvValue returns the value of a bwrap --setenv NAME VALUE triple.
func setenvValue(args []string, name string) (string, bool) {
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--setenv" && args[i+1] == name {
			return args[i+2], true
		}
	}
	return "", false
}

func indexSequence(values, sequence []string) int {
	for i := 0; i+len(sequence) <= len(values); i++ {
		if slices.Equal(values[i:i+len(sequence)], sequence) {
			return i
		}
	}
	return -1
}

// A policy that resolves but cannot be built here is exactly what --explain is
// for, so it reports what was asked rather than returning nothing. The error
// still travels, so a caller's exit status says the launch would fail.
func TestExplainBlockedPolicyStillReports(t *testing.T) {
	policy := &PackageSandbox{
		Profile:  "agent",
		Boundary: "hardened",
		Home:     "isolated",
		Net:      NetPolicy{Mode: "host"},
		FS:       FSPolicy{Cwd: "write"},
	}
	out := explainBlocked(policy)
	for _, want := range []string{"profile", "agent", "hardened", "cwd write"} {
		if !strings.Contains(out, want) {
			t.Errorf("blocked explanation missing %q:\n%s", want, out)
		}
	}
	// Every row is what was requested, not what is in force — nothing here was
	// planned, so claiming enforcement would be a lie.
	if strings.Contains(out, "mount") {
		t.Errorf("a blocked policy must not report enforcement levels:\n%s", out)
	}
	// The caller prints the error; repeating it would say it twice.
	if strings.Contains(out, "protected root") {
		t.Errorf("the blocker belongs to the caller's error, not a row:\n%s", out)
	}
}

// An error about a value the user did not write says where it came from, so it
// points at something they can change.
func TestPolicySourceNamesTheProfile(t *testing.T) {
	if got := policySource(&PackageSandbox{Profile: "agent"}); !strings.Contains(got, "agent") {
		t.Errorf("got %q, want the profile named", got)
	}
	if got := policySource(&PackageSandbox{}); strings.Contains(got, "profile") {
		t.Errorf("an inline policy has no profile to name, got %q", got)
	}
}

// The wiring, not the renderer: ExplainSandbox must return the explanation
// alongside the error, or --explain tells a reader less than attempting the
// launch would. Planning is made to fail the way it does in practice — a
// hardened cwd: write launched from the host home.
func TestExplainSandboxReturnsOutputWithTheError(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", cwd) // so cwd is a protected root

	p, _ := hardenedPrepared(t)
	cfg := &config.Config{Sandbox: config.Sandbox{
		Profiles: map[string]config.SandboxPolicy{
			"locked": {Boundary: "hardened", FS: &manifest.SandboxFS{Cwd: "write"}},
		},
	}}
	out, err := ExplainSandbox(p, cfg, "locked")
	if err == nil {
		t.Fatal("a hardened cwd: write at the home root must fail to plan")
	}
	if out == "" {
		t.Fatal("the explanation must accompany the error")
	}
	if !strings.Contains(out, "locked") {
		t.Errorf("the explanation must name the profile that asked:\n%s", out)
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("the error must name the profile too: %v", err)
	}
}
