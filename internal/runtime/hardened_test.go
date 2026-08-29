package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/manifest"
)

func hardenedPrepared(t *testing.T) (*Prepared, string) {
	t.Helper()
	hostHome := t.TempDir()
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "tool"},
		BinPath:  "/opt/tool/tool",
		Vars:     map[string]string{"data": t.TempDir(), "app": "/opt/tool"},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir(), "DISPLAY=:0"},
	}
	return p, hostHome
}

func TestHardenedBoundaryAllowlistsTheFilesystem(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	granted := filepath.Join(hostHome, "Projects")
	writable := filepath.Join(hostHome, "scratch")
	for _, dir := range []string{granted, writable} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	policy := finalized(t, &PackageSandbox{
		Boundary: "hardened",
		FS: FSPolicy{
			Read: []string{"~/Projects"}, ReadSet: true,
			Write: []string{"~/scratch"}, WriteSet: true,
		},
	})
	plan, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.needsLayer {
		t.Fatal("top-level hardened launch always builds its layer")
	}
	dataDir := p.Vars["data"]
	for _, want := range [][]string{
		{"--ro-bind", "/", "/"},
		{"--dev", "/dev"},
		{"--proc", "/proc"},
		{"--tmpfs", "/tmp"},
		{"--tmpfs", "/var/tmp"},
		{"--tmpfs", "/run"},
		{"--tmpfs", hostHome},
		{"--bind", dataDir, dataDir},
		{"--ro-bind", granted, granted},
		{"--bind", writable, writable},
		{"--ro-bind", "/work", "/work"}, // cwd defaults to read
		{"--unshare-net"},               // hardened network defaults to none
		{"--unshare-pid"}, {"--unshare-ipc"}, {"--unshare-uts"},
		{"--new-session"},
		{"--cap-drop", "ALL"},
		{"--die-with-parent"},
		{"--clearenv"},
	} {
		if indexSequence(plan.args, want) < 0 {
			t.Errorf("hardened args missing %v", want)
		}
	}
	// Integrations default off under hardened, so DISPLAY is never set for
	// the payload (it is dropped by --clearenv, not re-set).
	if _, ok := setenvValue(plan.args, "DISPLAY"); ok {
		t.Errorf("hardened default-off integration must not set DISPLAY: %v", plan.args)
	}
	if plan.context.Boundary != "hardened" || plan.context.NetMode != "none" {
		t.Errorf("context must carry the hardened boundary: %+v", plan.context)
	}
	if !slices.Equal(plan.context.FSRead, []string{granted}) || !slices.Equal(plan.context.FSWrite, []string{writable}) {
		t.Errorf("context must carry effective grants: %+v", plan.context)
	}
	if slices.Contains(plan.env, "DISPLAY=:0") {
		t.Errorf("hardened default-off integrations must strip their variables: %v", plan.env)
	}
}

func TestHardenedEphemeralHomeOverlaysAfterDataBind(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	dataDir := p.Vars["data"]
	home := filepath.Join(dataDir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "memory"), 0755); err != nil {
		t.Fatal(err)
	}
	policy := finalized(t, &PackageSandbox{
		Boundary: "hardened", Home: "ephemeral", Persist: []string{".claude/memory"},
	})
	plan, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	dataBindAt := indexSequence(plan.args, []string{"--bind", dataDir, dataDir})
	overlayAt := indexSequence(plan.args, []string{"--overlay-src", home, "--tmp-overlay", home})
	if dataBindAt < 0 || overlayAt < 0 || overlayAt < dataBindAt {
		t.Fatalf("ephemeral overlay must be layered after the data bind: %v", plan.args)
	}
	persistPath := filepath.Join(home, ".claude", "memory")
	if indexSequence(plan.args, []string{"--bind", persistPath, persistPath}) < 0 {
		t.Errorf("hardened ephemeral persist bind missing: %v", plan.args)
	}
}

// home: clean needs no pre-existing seed — the home directory doesn't even
// need to exist first, since a bare tmpfs has no lower layer to read.
func TestHardenedCleanHomeReplacesHomeAfterDataBind(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	dataDir := p.Vars["data"]
	home := filepath.Join(dataDir, "home")
	policy := finalized(t, &PackageSandbox{Boundary: "hardened", Home: "clean"})
	plan, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	dataBindAt := indexSequence(plan.args, []string{"--bind", dataDir, dataDir})
	tmpfsAt := indexSequence(plan.args, []string{"--tmpfs", home})
	if dataBindAt < 0 || tmpfsAt < 0 || tmpfsAt < dataBindAt {
		t.Fatalf("clean home tmpfs must be layered after the data bind: %v", plan.args)
	}
}

func TestHardenedGrantsRefuseProtectedRoots(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	// A grant equal to, or an ancestor of, a masked/private root would
	// re-expose the real host content the baseline hid.
	for name, grant := range map[string]*[]string{
		"home itself":             {"~"},
		"root":                    {"/"},
		"run":                     {"/run"},
		"var ancestor of var/tmp": {"/var"},
	} {
		policy := finalized(t, &PackageSandbox{
			Boundary: "hardened",
			FS:       FSPolicy{Read: *grant, ReadSet: true},
		})
		if _, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{}); err == nil {
			t.Errorf("%s: grant %v must be refused", name, *grant)
		}
	}
}

func TestHardenedGrantSymlinkToProtectedRootRefused(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	link := filepath.Join(hostHome, "escape")
	if err := os.Symlink("/", link); err != nil {
		t.Fatal(err)
	}
	policy := finalized(t, &PackageSandbox{
		Boundary: "hardened",
		FS:       FSPolicy{Read: []string{"~/escape"}, ReadSet: true},
	})
	if _, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{}); err == nil {
		t.Error("a grant symlinked to / must be refused")
	}
}

func TestHardenedCwdAtHomeStaysMasked(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	// Default read cwd at the home root must not rebind the (hidden) home.
	policy := finalized(t, &PackageSandbox{Boundary: "hardened"})
	plan, err := buildSandboxPlan(p, policy, hostHome, hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(plan.args, []string{"--ro-bind", hostHome, hostHome}) >= 0 {
		t.Errorf("cwd at home must not re-expose the home: %v", plan.args)
	}
	if indexSequence(plan.args, []string{"--tmpfs", hostHome}) < 0 {
		t.Errorf("home must remain masked: %v", plan.args)
	}
	// An explicit write cwd at the home root is a hard error.
	policy = finalized(t, &PackageSandbox{Boundary: "hardened", FS: FSPolicy{Cwd: "write"}})
	if _, err := buildSandboxPlan(p, policy, hostHome, hostHome, sandboxContext{}); err == nil {
		t.Error("cwd: write at the home root must be refused")
	}
}

func TestHardenedGrantsFailClosedOnMissingPaths(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	policy := finalized(t, &PackageSandbox{
		Boundary: "hardened",
		FS:       FSPolicy{Read: []string{"~/does-not-exist"}, ReadSet: true},
	})
	_, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{})
	if err == nil || !strings.Contains(err.Error(), "fail closed") {
		t.Fatalf("missing grant must fail closed, got %v", err)
	}
}

func TestHardenedCwdModes(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	for cwdMode, want := range map[string][]string{
		"write":  {"--bind", "/work", "/work"},
		"hidden": {"--tmpfs", "/work"},
	} {
		policy := finalized(t, &PackageSandbox{Boundary: "hardened", FS: FSPolicy{Cwd: cwdMode}})
		plan, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{})
		if err != nil {
			t.Fatal(err)
		}
		if indexSequence(plan.args, want) < 0 {
			t.Errorf("cwd %s missing %v", cwdMode, want)
		}
	}
}

func TestHardenedRejectsSharedHomeAndScopedRejectsGrants(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"tool": {SandboxPolicy: config.SandboxPolicy{Boundary: "hardened", Home: "shared"}},
	}}}
	if _, err := ResolvePackageSandbox(cfg, "tool", ""); err == nil {
		t.Error("hardened boundary with shared home must be rejected")
	}
	read := []string{"~/Projects"}
	cfg = &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"tool": {SandboxPolicy: config.SandboxPolicy{FS: &manifest.SandboxFS{Read: &read}}},
	}}}
	if _, err := ResolvePackageSandbox(cfg, "tool", ""); err == nil {
		t.Error("fs grants under an effective scoped boundary must be rejected")
	}
}

func TestHardenedOverBuiltinProfileForcesTTYOffWithoutError(t *testing.T) {
	cfg := &config.Config{Sandbox: config.Sandbox{Packages: map[string]config.SandboxPackage{
		"tool": {
			Profile:       config.SandboxProfileDesktop, // sets tty: true
			SandboxPolicy: config.SandboxPolicy{Boundary: "hardened"},
		},
	}}}
	got, err := ResolvePackageSandbox(cfg, "tool", "")
	if err != nil {
		t.Fatalf("built-in profiles must remain valid hardened bases: %v", err)
	}
	if got.feature("tty") {
		t.Error("hardened boundary must force tty off")
	}
	// A built-in states its net mode explicitly, so it means the same thing
	// under either boundary rather than silently picking up hardened's
	// default of none.
	if got.Net.Mode != "host" {
		t.Errorf("profile's explicit net mode should survive: %q", got.Net.Mode)
	}
}

func TestHardenedNonHostModeExcludesTheProxy(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	p.Env = append(p.Env, "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus")
	policy := finalized(t, &PackageSandbox{
		Boundary: "hardened",
		Features: map[string]bool{"dbus": true},
		Net:      NetPolicy{Mode: "none"},
	})
	plan, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.proxy != nil {
		t.Fatal("non-host network mode must exclude the portal proxy")
	}
	if !plan.forcedDBus {
		t.Error("the forced D-Bus restriction must be recorded for --explain")
	}
	if _, ok := setenvValue(plan.args, "DBUS_SESSION_BUS_ADDRESS"); ok {
		t.Errorf("hardened without a bus must not set the address for the payload: %v", plan.args)
	}
}

func TestHardenedHostModeDBusSelectsThePortalProxy(t *testing.T) {
	if _, err := FindXDGDBusProxy(); err != nil {
		t.Skip("xdg-dbus-proxy not installed")
	}
	p, hostHome := hardenedPrepared(t)
	runtimeDir := envMap(p.Env)["XDG_RUNTIME_DIR"]
	policy := finalized(t, &PackageSandbox{
		Boundary: "hardened",
		Features: map[string]bool{"dbus": true},
		Net:      NetPolicy{Mode: "host"},
	})
	plan, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.proxy == nil {
		t.Fatal("hardened dbus: true with host networking selects the filtered proxy")
	}
	busTarget := filepath.Join(runtimeDir, "bus")
	if indexSequence(plan.args, []string{"--bind", plan.proxy.socketPath, busTarget}) < 0 {
		t.Errorf("proxy socket must be bound to the bus path: %v", plan.args)
	}
	if !slices.Contains(plan.env, "DBUS_SESSION_BUS_ADDRESS=unix:path="+busTarget) {
		t.Errorf("bus address must point at the proxy: %v", plan.env)
	}
	args := plan.proxy.proxyArgs()
	for _, want := range []string{"--filter", "--call=org.freedesktop.portal.*=*"} {
		if !slices.Contains(args, want) {
			t.Errorf("proxy args missing %q: %v", want, args)
		}
	}
}

// dropped and re-entry is what breaks, not the boundary.
func TestHardenedDropsLayoutGrantThatWouldReopenTheHostHome(t *testing.T) {
	p, hostHome := hardenedPrepared(t)
	p.LayoutRoots = []string{hostHome, filepath.Join(hostHome, "bin")}
	if err := os.MkdirAll(filepath.Join(hostHome, "bin"), 0755); err != nil {
		t.Fatal(err)
	}

	policy := finalized(t, &PackageSandbox{Boundary: "hardened"})
	plan, err := buildSandboxPlan(p, policy, "/work", hostHome, sandboxContext{})
	if err != nil {
		t.Fatalf("a layout root at $HOME must not fail the launch: %v", err)
	}
	if indexSequence(plan.args, []string{"--ro-bind", hostHome, hostHome}) >= 0 {
		t.Errorf("the host home must stay masked: %v", plan.args)
	}
	// A subdirectory of the home is still a scoped path, so it survives.
	shims := filepath.Join(hostHome, "bin")
	if indexSequence(plan.args, []string{"--ro-bind", shims, shims}) < 0 {
		t.Errorf("a layout path below the home is scoped and must be bound: %v", plan.args)
	}
}

func TestHardenedHostNetworkKeepsResolverReachable(t *testing.T) {
	target, err := filepath.EvalSymlinks("/etc/resolv.conf")
	if err != nil || !strings.HasPrefix(target, "/run/") {
		t.Skip("host does not resolve /etc/resolv.conf into /run")
	}
	p, hostHome := hardenedPrepared(t)

	host := finalized(t, &PackageSandbox{Boundary: "hardened", Net: NetPolicy{Mode: "host"}})
	plan, err := buildSandboxPlan(p, host, "/work", hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	bindAt := indexSequence(plan.args, []string{"--ro-bind", target, target})
	tmpfsAt := indexSequence(plan.args, []string{"--tmpfs", "/run"})
	if bindAt < 0 {
		t.Fatalf("host networking must keep the resolver readable: %v", plan.args)
	}
	if tmpfsAt < 0 || bindAt < tmpfsAt {
		t.Errorf("the resolver bind must land after the private /run, or the tmpfs hides it again")
	}

	none := finalized(t, &PackageSandbox{Boundary: "hardened", Net: NetPolicy{Mode: "none"}})
	plan, err = buildSandboxPlan(p, none, "/work", hostHome, sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(plan.args, []string{"--ro-bind", target, target}) >= 0 {
		t.Errorf("a restricted network mode must keep the resolver masked: %v", plan.args)
	}
}
