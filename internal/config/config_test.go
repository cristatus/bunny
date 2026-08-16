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
