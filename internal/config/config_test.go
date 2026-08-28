package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadEmptyFile(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"empty", ""},
		{"comment-only", "# just a comment\n"},
		{"trailing-separator", "catalogs:\n  - name: org\n    remote: https://example.com\n---\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(write(t, tc.body))
			if err != nil {
				t.Fatalf("config should be valid, got: %v", err)
			}
			if cfg == nil {
				t.Fatal("expected non-nil config")
			}
		})
	}
}

func TestLoadMissingFileIsEmptyConfig(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if len(cfg.Env) != 0 || len(cfg.Dirs) != 0 {
		t.Errorf("expected empty config, got %+v", cfg)
	}
}

func TestLoadEnvAndDirs(t *testing.T) {
	cfg, err := Load(write(t, `
env:
  node:
    NPM_CONFIG_PREFIX: "{data}/npm-global"
  gradle:
    GRADLE_USER_HOME: "{data}/gradle"
dirs:
  gradle:
    - "{data}/gradle"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Env["node"]["NPM_CONFIG_PREFIX"]; got != "{data}/npm-global" {
		t.Errorf("NPM_CONFIG_PREFIX = %q", got)
	}
	if got := cfg.DirsFor("gradle", "gradle"); !reflect.DeepEqual(got, []string{"{data}/gradle"}) {
		t.Errorf("DirsFor = %v", got)
	}
}

func TestLoadRejectsBadEnvName(t *testing.T) {
	if _, err := Load(write(t, "env:\n  node:\n    \"BAD NAME\": x\n")); err == nil {
		t.Fatal("expected an error for an invalid env var name")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	if _, err := Load(write(t, "isolate: true\n")); err == nil {
		t.Fatal("expected an error for an unknown top-level field")
	}
}

func TestOverlayEnvPrecedence(t *testing.T) {
	cfg := &Config{Env: map[string]map[string]string{
		Wildcard:  {"SHARED": "star", "TARGET": "star"},
		"node":    {"TARGET": "capability", "FROM_CAP": "yes"},
		"node-22": {"TARGET": "id"},
	}}
	base := map[string]string{"TARGET": "manifest", "FROM_MANIFEST": "yes"}

	got := cfg.OverlayEnv(base, "node-22", "node")
	want := map[string]string{
		"TARGET":        "id", // id beats capability beats "*" beats manifest
		"SHARED":        "star",
		"FROM_CAP":      "yes",
		"FROM_MANIFEST": "yes",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OverlayEnv = %v, want %v", got, want)
	}
	if base["TARGET"] != "manifest" {
		t.Error("OverlayEnv must not mutate the manifest env it was given")
	}
}

func TestOverlayEnvNilConfigReturnsCopyOfBase(t *testing.T) {
	base := map[string]string{"JAVA_HOME": "{app}"}
	var cfg *Config
	got := cfg.OverlayEnv(base, "jdk-21", "jdk")
	if !reflect.DeepEqual(got, base) {
		t.Errorf("OverlayEnv = %v, want %v", got, base)
	}
	got["JAVA_HOME"] = "mutated"
	if base["JAVA_HOME"] != "{app}" {
		t.Error("result must be a copy, not the base map itself")
	}
}

// A package with no `provides:` is keyed by id alone; the capability slot must
// not double as a second id lookup.
func TestOverlayEnvNoCapability(t *testing.T) {
	cfg := &Config{Env: map[string]map[string]string{"maven": {"MAVEN_OPTS": "-Xmx2g"}}}
	got := cfg.OverlayEnv(nil, "maven", "")
	if got["MAVEN_OPTS"] != "-Xmx2g" {
		t.Errorf("OverlayEnv = %v", got)
	}
}

func TestDirsForNilConfig(t *testing.T) {
	var cfg *Config
	if got := cfg.DirsFor("node-22", "node"); got != nil {
		t.Errorf("DirsFor = %v, want nil", got)
	}
}

func TestDirsForCombinesKeys(t *testing.T) {
	cfg := &Config{Dirs: map[string][]string{
		Wildcard:  {"{data}/common"},
		"node":    {"{data}/cap"},
		"node-22": {"{data}/id"},
	}}
	want := []string{"{data}/common", "{data}/cap", "{data}/id"}
	if got := cfg.DirsFor("node-22", "node"); !reflect.DeepEqual(got, want) {
		t.Errorf("DirsFor = %v, want %v", got, want)
	}
}

func TestLoadInstallRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load(write(t, "install:\n  sdk: ~/opt\n  app: /srv/apps\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Install["sdk"], filepath.Join(home, "opt"); got != want {
		t.Errorf("sdk root = %q, want %q (leading ~/ expanded)", got, want)
	}
	if got := cfg.Install["app"]; got != "/srv/apps" {
		t.Errorf("app root = %q", got)
	}
	if _, ok := cfg.InstallRoots()["cli"]; ok {
		t.Error("unset kinds should not appear; they fall back to the default root")
	}
}

func TestLoadRejectsBadInstallRoots(t *testing.T) {
	for name, body := range map[string]string{
		"unknown kind":    "install:\n  editor: /srv/apps\n",
		"relative":        "install:\n  sdk: opt/sdks\n",
		"filesystem root": "install:\n  sdk: /\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatalf("expected an error for %s", name)
			}
		})
	}
}

func TestInstallRootsNilConfig(t *testing.T) {
	var cfg *Config
	if got := cfg.InstallRoots(); got != nil {
		t.Errorf("InstallRoots() = %v, want nil", got)
	}
}

// The example config ships in the repo, so it has to stay parseable and stay
// inert: someone copying it should get bunny's defaults until they uncomment
// something, and it should not drift out of the schema.
func TestExampleConfigIsValidAndInert(t *testing.T) {
	path := filepath.Join("..", "..", "config.example.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("%s must be valid config: %v", path, err)
	}
	if len(cfg.Env) != 0 || len(cfg.Dirs) != 0 || len(cfg.Install) != 0 || len(cfg.Catalogs) != 0 ||
		len(cfg.Sandbox.Profiles) != 0 || len(cfg.Sandbox.Packages) != 0 {
		t.Errorf("the example must be entirely commented out, got %+v", cfg)
	}
}

func TestLoadCatalogs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load(write(t, `
catalogs:
  - name: axelor
    local: ~/src/axelor-catalog
  - name: upstream
    remote: https://example.com/catalog/main
`))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.ResolveCatalogs()
	want := []Catalog{
		{Name: "axelor", Local: filepath.Join(home, "src", "axelor-catalog")},
		{Name: "upstream", Remote: "https://example.com/catalog/main"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("ResolveCatalogs() = %+v, want %+v", got, want)
	}
	if !got[0].IsLocal() || got[1].IsLocal() {
		t.Error("IsLocal must distinguish a checkout from an HTTP catalog")
	}
}

// A relative path would resolve against whatever directory bunny happened to
// be run from, which is never what someone means.
func TestLoadRejectsRelativeCatalogLocal(t *testing.T) {
	yaml := "catalogs:\n  - name: org\n    local: ../catalog\n"
	if _, err := Load(write(t, yaml)); err == nil {
		t.Fatal("expected a relative catalog path to be rejected")
	}
}

// Nothing configured: the public catalog alone, whose URL the catalog package
// fills in. No checkout is implied — one is a catalog like any other, so it has
// to be listed.
func TestResolveCatalogsDefaultChain(t *testing.T) {
	want := []Catalog{{Name: DefaultCatalog, Remote: catalog.DefaultRemoteURL}}
	for _, cfg := range []*Config{nil, {}} {
		if got := cfg.ResolveCatalogs(); !slices.Equal(got, want) {
			t.Errorf("ResolveCatalogs() = %+v, want %+v", got, want)
		}
	}
}

func TestLoadRejectsBadCatalogs(t *testing.T) {
	cases := []struct{ name, yaml string }{
		{"no name", "catalogs:\n  - local: /src/org\n"},
		{"bad name", "catalogs:\n  - name: Org_1\n    local: /src/org\n"},
		{"duplicate name", "catalogs:\n  - name: org\n    local: /src/a\n  - name: org\n    local: /src/b\n"},
		{"both kinds", "catalogs:\n  - name: org\n    local: /src/org\n    remote: https://example.com/c\n"},
		{"neither kind", "catalogs:\n  - name: org\n"},
		{"relative local", "catalogs:\n  - name: org\n    local: ../org\n"},
		{"old catalog key", "catalog:\n  remote: https://example.com/c\n"},
	}
	for _, c := range cases {
		if _, err := Load(write(t, c.yaml)); err == nil {
			t.Errorf("%s: expected an error", c.name)
		}
	}
}

func TestLoadSandbox(t *testing.T) {
	cfg, err := Load(write(t, `
sandbox:
  profiles:
    custom-desktop:
      home: isolated
      hide: [~/.ssh]
      net: host
      features:
        audio: true
  packages:
    vscode:
      profile: custom-desktop
      features:
        audio: false
      hide: [~/Documents/private]
`))
	if err != nil {
		t.Fatal(err)
	}
	desktop, ok := cfg.Sandbox.Profiles["custom-desktop"]
	if !ok {
		t.Fatal("expected desktop profile")
	}
	if desktop.Home != "isolated" || desktop.Net == nil || desktop.Net.Mode != "host" {
		t.Fatalf("unexpected desktop profile: %+v", desktop)
	}
	vscode, ok := cfg.Sandbox.Packages["vscode"]
	if !ok {
		t.Fatal("expected vscode package activation")
	}
	if vscode.Profile != "custom-desktop" || vscode.Features["audio"] || len(vscode.Hide) != 1 {
		t.Fatalf("unexpected vscode package policy: %+v", vscode)
	}
}

func TestLoadSandboxPersist(t *testing.T) {
	cfg, err := Load(write(t, `
sandbox:
  packages:
    claude:
      home: ephemeral
      persist: [.claude/memory]
`))
	if err != nil {
		t.Fatal(err)
	}
	claude, ok := cfg.Sandbox.Packages["claude"]
	if !ok {
		t.Fatal("expected claude package activation")
	}
	if claude.Home != "ephemeral" || len(claude.Persist) != 1 || claude.Persist[0] != ".claude/memory" {
		t.Fatalf("unexpected claude package policy: %+v", claude)
	}
}

func TestLoadRejectsPersistWithoutEphemeralHome(t *testing.T) {
	if _, err := Load(write(t, "sandbox:\n  packages:\n    claude:\n      home: isolated\n      persist: [.claude/memory]\n")); err == nil {
		t.Fatal("expected persist without home: ephemeral to be rejected")
	}
}

func TestBuiltinSandboxProfilesAreAvailableWithoutConfig(t *testing.T) {
	var cfg *Config
	for _, name := range []string{SandboxProfileDesktop, SandboxProfileOnlineCLI, SandboxProfileOfflineCLI, SandboxProfileEphemeral, SandboxProfileClean} {
		if _, ok := cfg.SandboxProfile(name); !ok {
			t.Errorf("built-in profile %q is unavailable", name)
		}
	}
	// Every built-in states its net mode explicitly rather than leaning on
	// the boundary default, so a profile means the same under either boundary.
	netMode := func(p SandboxPolicy) string {
		if p.Net == nil {
			return ""
		}
		return p.Net.Mode
	}
	desktop, _ := cfg.SandboxProfile(SandboxProfileDesktop)
	if desktop.Home != "isolated" || netMode(desktop) != "host" || !desktop.Features["audio"] {
		t.Errorf("unexpected desktop profile: %+v", desktop)
	}
	online, _ := cfg.SandboxProfile(SandboxProfileOnlineCLI)
	if netMode(online) != "host" || online.Features["x11"] || online.Features["audio"] {
		t.Errorf("unexpected online-cli profile: %+v", online)
	}
	offline, _ := cfg.SandboxProfile(SandboxProfileOfflineCLI)
	if netMode(offline) != "none" || offline.Features["dbus"] {
		t.Errorf("unexpected offline-cli profile: %+v", offline)
	}
	// features.network is gone: no built-in may smuggle network policy back
	// in through the feature map, where it would now be silently inert.
	for _, name := range BuiltinSandboxProfileNames() {
		p, _ := cfg.SandboxProfile(name)
		if _, ok := p.Features["network"]; ok {
			t.Errorf("built-in %q still sets the removed features.network key", name)
		}
		if netMode(p) == "" {
			t.Errorf("built-in %q leaves net.mode implicit", name)
		}
	}
	// Ephemeral differs from desktop only in Home: every integration stays
	// as permissive as desktop's, and it carries no persist entries, so a
	// run started under it leaves nothing behind in its own state.
	ephemeral, _ := cfg.SandboxProfile(SandboxProfileEphemeral)
	if ephemeral.Home != "ephemeral" || len(ephemeral.Persist) != 0 ||
		netMode(ephemeral) != "host" || !ephemeral.Features["x11"] || !ephemeral.Features["audio"] {
		t.Errorf("unexpected ephemeral profile: %+v", ephemeral)
	}
	// Clean differs from ephemeral in what HOME starts from (never seeded,
	// not seed-and-discard), not in feature permissiveness or persist.
	clean, _ := cfg.SandboxProfile(SandboxProfileClean)
	if clean.Home != "clean" || len(clean.Persist) != 0 ||
		netMode(clean) != "host" || !clean.Features["x11"] || !clean.Features["audio"] {
		t.Errorf("unexpected clean profile: %+v", clean)
	}
	// Callers receive a copy rather than mutable process-global policy —
	// including the Net pointer, which the built-ins now carry.
	desktop.Features["audio"] = false
	desktop.Net.Mode = "none"
	again, _ := cfg.SandboxProfile(SandboxProfileDesktop)
	if !again.Features["audio"] {
		t.Fatal("mutating a resolved profile's features changed the built-in")
	}
	if netMode(again) != "host" {
		t.Fatal("mutating a resolved profile's net changed the built-in")
	}
}

// BuiltinSandboxProfileNames is shell completion's source of truth for
// --sandbox-profile; it must name exactly the reserved profiles, sorted,
// with no separate hardcoded list to drift out of sync.
func TestBuiltinSandboxProfileNames(t *testing.T) {
	want := []string{"clean", "desktop", "ephemeral", "offline-cli", "online-cli"}
	if got := BuiltinSandboxProfileNames(); !slices.Equal(got, want) {
		t.Errorf("BuiltinSandboxProfileNames() = %v, want %v", got, want)
	}
}

func TestLoadAcceptsBuiltinProfileAndRejectsRedefinition(t *testing.T) {
	if _, err := Load(write(t, "sandbox:\n  packages:\n    vscode:\n      profile: desktop\n")); err != nil {
		t.Fatalf("built-in profile should be selectable without a definition: %v", err)
	}
	if _, err := Load(write(t, "sandbox:\n  profiles:\n    desktop:\n      home: shared\n")); err == nil {
		t.Fatal("expected built-in profile redefinition to be rejected")
	}
}

func TestSandboxPackagePresenceActivatesEvenWhenEmpty(t *testing.T) {
	cfg, err := Load(write(t, "sandbox:\n  packages:\n    vscode:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Sandbox.Packages["vscode"]; !ok {
		t.Fatal("an empty package entry must remain present for activation")
	}
}

func TestLoadRejectsInvalidSandboxConfig(t *testing.T) {
	for name, body := range map[string]string{
		"global enable":    "sandbox:\n  enabled: true\n",
		"unknown profile":  "sandbox:\n  packages:\n    vscode:\n      profile: missing\n",
		"unknown feature":  "sandbox:\n  profiles:\n    custom:\n      features:\n        gpu: true\n",
		"bad activation":   "sandbox:\n  packages:\n    vscode:\n      activation: sometimes\n",
		"invalid package":  "sandbox:\n  packages:\n    Bad_ID: {}\n",
		"absolute persist": "sandbox:\n  packages:\n    vscode:\n      home: ephemeral\n      persist: [/etc/passwd]\n",
		"bad home":         "sandbox:\n  packages:\n    vscode:\n      home: gone\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
		})
	}
}
