package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
		{"trailing-separator", "catalog:\n  remote: https://example.com\n---\n"},
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
	if len(cfg.Env) != 0 || len(cfg.Dirs) != 0 || len(cfg.Install) != 0 || cfg.Catalog.Remote != "" ||
		len(cfg.Sandbox.Profiles) != 0 || len(cfg.Sandbox.Packages) != 0 {
		t.Errorf("the example must be entirely commented out, got %+v", cfg)
	}
}

func TestLoadCatalogLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := Load(write(t, "catalog:\n  local: ~/src/bunny-catalog\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Catalog.Local, filepath.Join(home, "src", "bunny-catalog"); got != want {
		t.Errorf("catalog.local = %q, want %q", got, want)
	}
}

// A relative path would resolve against whatever directory bunny happened to
// be run from, which is never what someone means.
func TestLoadRejectsRelativeCatalogLocal(t *testing.T) {
	if _, err := Load(write(t, "catalog:\n  local: ../catalog\n")); err == nil {
		t.Fatal("expected a relative catalog.local to be rejected")
	}
}

func TestLoadSandbox(t *testing.T) {
	cfg, err := Load(write(t, `
sandbox:
  profiles:
    custom-desktop:
      home: isolated
      hide: [~/.ssh]
      features:
        network: true
        audio: true
  packages:
    vscode:
      activation: on-demand
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
	if desktop.Home != "isolated" || !desktop.Features["network"] {
		t.Fatalf("unexpected desktop profile: %+v", desktop)
	}
	vscode, ok := cfg.Sandbox.Packages["vscode"]
	if !ok {
		t.Fatal("expected vscode package activation")
	}
	if vscode.Activation != "on-demand" || vscode.Profile != "custom-desktop" || vscode.Features["audio"] || len(vscode.Hide) != 1 {
		t.Fatalf("unexpected vscode package policy: %+v", vscode)
	}
}

func TestBuiltinSandboxProfilesAreAvailableWithoutConfig(t *testing.T) {
	var cfg *Config
	for _, name := range []string{SandboxProfileDesktop, SandboxProfileOnlineCLI, SandboxProfileOfflineCLI} {
		if _, ok := cfg.SandboxProfile(name); !ok {
			t.Errorf("built-in profile %q is unavailable", name)
		}
	}
	desktop, _ := cfg.SandboxProfile(SandboxProfileDesktop)
	if desktop.Home != "isolated" || !desktop.Features["network"] || !desktop.Features["audio"] {
		t.Errorf("unexpected desktop profile: %+v", desktop)
	}
	online, _ := cfg.SandboxProfile(SandboxProfileOnlineCLI)
	if !online.Features["network"] || online.Features["x11"] || online.Features["audio"] {
		t.Errorf("unexpected online-cli profile: %+v", online)
	}
	offline, _ := cfg.SandboxProfile(SandboxProfileOfflineCLI)
	if offline.Features["network"] || offline.Features["dbus"] {
		t.Errorf("unexpected offline-cli profile: %+v", offline)
	}

	// Callers receive a copy rather than mutable process-global policy.
	desktop.Features["network"] = false
	again, _ := cfg.SandboxProfile(SandboxProfileDesktop)
	if !again.Features["network"] {
		t.Fatal("mutating a resolved profile changed the built-in")
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
		"global enable":   "sandbox:\n  enabled: true\n",
		"unknown profile": "sandbox:\n  packages:\n    vscode:\n      profile: missing\n",
		"unknown feature": "sandbox:\n  profiles:\n    custom:\n      features:\n        gpu: true\n",
		"bad activation":  "sandbox:\n  packages:\n    vscode:\n      activation: sometimes\n",
		"invalid package": "sandbox:\n  packages:\n    Bad_ID: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
		})
	}
}
