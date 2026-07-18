package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
	"gopkg.in/yaml.v3"
)

type reportCatalog struct {
	packages  []catalog.PackageInfo
	manifests map[string]*manifest.Manifest
	err       error
}

func (c reportCatalog) List() ([]catalog.PackageInfo, error) { return c.packages, c.err }
func (c reportCatalog) Load(id string) (*manifest.Manifest, error) {
	if c.err != nil {
		return nil, c.err
	}
	m, ok := c.manifests[id]
	if !ok {
		return nil, errors.New("missing")
	}
	return m, nil
}
func (c reportCatalog) LoadFile(string, string) ([]byte, error) { return nil, errors.New("missing") }

func TestLoadUserConfigEmptyFile(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"empty", ""},
		{"comment-only", "# just a comment\n"},
		{"trailing-separator", "catalog:\n  remote: https://example.com\n---\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadUserConfig(path)
			if err != nil {
				t.Fatalf("config should be valid, got: %v", err)
			}
			if cfg == nil {
				t.Fatal("expected non-nil config")
			}
		})
	}
}

func TestFindGlobalExe(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}
	m := &manifest.Manifest{ID: "node-24", Version: "24.0.0", GlobalBins: []string{"{data}/npm-global/bin"}}

	binDir := filepath.Join(root, "var", "app", "node-24", "npm-global", "bin")
	os.MkdirAll(binDir, 0755)
	os.WriteFile(filepath.Join(binDir, "tsc"), []byte("#!/bin/sh\n"), 0755)

	exe, err := a.findGlobalExe(m, "node-24", "tsc")
	if err != nil {
		t.Fatal(err)
	}
	if exe != filepath.Join(binDir, "tsc") {
		t.Errorf("exe = %q", exe)
	}

	if _, err := a.findGlobalExe(m, "node-24", "missing"); err == nil {
		t.Error("expected error for missing tool")
	}
}

func TestReshimCapabilities(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}

	// Install-like setup: node-24 provides node, declares global-bins, and has
	// a globally-installed tsc. Cache its manifest so loadInstalledManifest finds it.
	a.State.SetInstalled("node-24", "24.0.0", "node")
	a.State.SetCommand("node", "node-24") // protected SDK shim
	m := &manifest.Manifest{ID: "node-24", Name: "Node", Version: "24.0.0", Provides: "node",
		Sources:    []manifest.Source{{URL: "https://example.com/x", SHA256: strings.Repeat("a", 64)}},
		Bin:        []manifest.Binary{{Name: "node", Path: "{app}/bin/node"}},
		GlobalBins: []string{"{data}/npm-global/bin"}}
	cacheManifest(t, a.Paths.ManifestFile("node-24"), m)

	binDir := filepath.Join(root, "var", "app", "node-24", "npm-global", "bin")
	os.MkdirAll(binDir, 0755)
	os.WriteFile(filepath.Join(binDir, "tsc"), []byte("#!/bin/sh\n"), 0755)
	os.MkdirAll(a.Paths.Bin(), 0755)
	os.WriteFile(a.Paths.BunnyBinary(), []byte("#!/bin/sh\n"), 0755)

	added, removed, err := a.reshimCapabilities("node")
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "tsc" {
		t.Errorf("added = %v, want [tsc]", added)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v", removed)
	}
	if _, ok := a.State.GlobalCommandCapability("tsc"); !ok {
		t.Error("tsc not registered in state")
	}
	if _, err := os.Lstat(a.Paths.Shim("tsc")); err != nil {
		t.Errorf("tsc shim not created: %v", err)
	}
	if _, ok := a.State.GlobalCommandCapability("node"); ok {
		t.Error("protected SDK command 'node' must not be registered as global")
	}
}

func TestReshimPrunesWhenToolGone(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}
	a.State.SetInstalled("node-24", "24.0.0", "node")
	m := &manifest.Manifest{ID: "node-24", Name: "Node", Version: "24.0.0", Provides: "node",
		Sources:    []manifest.Source{{URL: "https://example.com/x", SHA256: strings.Repeat("a", 64)}},
		Bin:        []manifest.Binary{{Name: "node", Path: "{app}/bin/node"}},
		GlobalBins: []string{"{data}/npm-global/bin"}}
	cacheManifest(t, a.Paths.ManifestFile("node-24"), m)
	os.MkdirAll(a.Paths.Bin(), 0755)
	os.WriteFile(a.Paths.BunnyBinary(), []byte("#!/bin/sh\n"), 0755)

	// tsc is registered but no longer exists on disk (uninstalled via npm -g).
	a.State.SetGlobalCommand("tsc", "node")
	os.Symlink(a.Paths.BunnyBinary(), a.Paths.Shim("tsc"))

	_, removed, err := a.reshimCapabilities("node")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "tsc" {
		t.Errorf("removed = %v, want [tsc]", removed)
	}
	if _, ok := a.State.GlobalCommandCapability("tsc"); ok {
		t.Error("tsc should be unregistered")
	}
	if _, err := os.Lstat(a.Paths.Shim("tsc")); !os.IsNotExist(err) {
		t.Error("tsc shim should be removed")
	}
}

func TestReshimNeverReplacesBunnyExecutable(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}
	a.State.SetInstalled("node-24", "24.0.0", "node")
	m := &manifest.Manifest{ID: "node-24", Name: "Node", Version: "24.0.0", Provides: "node",
		Sources:    []manifest.Source{{URL: "https://example.com/x", SHA256: strings.Repeat("a", 64)}},
		Bin:        []manifest.Binary{{Name: "node", Path: "{app}/bin/node"}},
		GlobalBins: []string{"{data}/npm-global/bin"}}
	cacheManifest(t, a.Paths.ManifestFile("node-24"), m)
	globalBin := filepath.Join(root, "var", "app", "node-24", "npm-global", "bin")
	os.MkdirAll(globalBin, 0755)
	os.WriteFile(filepath.Join(globalBin, "bunny"), []byte("global tool"), 0755)
	os.MkdirAll(a.Paths.Bin(), 0755)
	os.WriteFile(a.Paths.BunnyBinary(), []byte("real bunny"), 0755)

	if _, _, err := a.reshimCapabilities("node"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.Paths.BunnyBinary())
	if err != nil || string(data) != "real bunny" {
		t.Fatalf("bunny executable changed: %q, %v", data, err)
	}
	if _, ok := a.State.GlobalCommandCapability("bunny"); ok {
		t.Fatal("reserved bunny command was registered")
	}
}

func TestCheckUpdatesComparesToCatalog(t *testing.T) {
	// update compares installed versions to the catalog (no upstream source
	// checks — that's `dev update`'s job), so a differing catalog version is an
	// update and a matching one is not.
	st := state.Empty()
	st.SetInstalled("tool", "1.0", "")
	st.SetInstalled("other", "2.0", "")
	cat := reportCatalog{packages: []catalog.PackageInfo{
		{ID: "tool", Version: "1.1"},        // catalog moved on → update
		{ID: "other", Version: "2.0"},       // same version → no update
		{ID: "uninstalled", Version: "3.0"}, // not installed → ignored
	}}
	a := &App{State: st, Catalog: cat}

	report, err := a.checkUpdates(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Err() != nil {
		t.Fatalf("no source-check failures expected: %v", report.Err())
	}
	if len(report.Results) != 1 || report.Results[0].ID != "tool" {
		t.Fatalf("want exactly one update (tool), got %+v", report.Results)
	}
	if r := report.Results[0]; r.CurrentVersion != "1.0" || r.LatestVersion != "1.1" {
		t.Fatalf("versions wrong: %+v", r)
	}
}

func TestRegenerateToolchains(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}

	addJDK := func(id, ver string) {
		a.State.SetInstalled(id, ver, "jdk")
		cacheManifest(t, a.Paths.ManifestFile(id), &manifest.Manifest{
			ID: id, Name: id, Version: ver, Provides: "jdk",
			Sources: []manifest.Source{{URL: "https://example.com/" + id, SHA256: strings.Repeat("a", 64)}},
			Bin:     []manifest.Binary{{Name: "java", Path: "{app}/bin/java"}},
		})
	}
	addJDK("jdk-21", "21.0.11+10")
	addJDK("jdk-25", "25.0.3+9")

	a.State.SetInstalled("gradle", "9.0.0", "")
	cacheManifest(t, a.Paths.ManifestFile("gradle"), &manifest.Manifest{
		ID: "gradle", Name: "Gradle", Version: "9.0.0", Toolchains: "gradle",
		Sources: []manifest.Source{{URL: "https://example.com/gradle", SHA256: strings.Repeat("a", 64)}},
		Bin:     []manifest.Binary{{Name: "gradle", Path: "{app}/bin/gradle"}},
		Env:     map[string]string{"GRADLE_USER_HOME": "{data}/gradle"},
	})
	a.State.SetInstalled("maven", "3.9.0", "")
	cacheManifest(t, a.Paths.ManifestFile("maven"), &manifest.Manifest{
		ID: "maven", Name: "Maven", Version: "3.9.0", Toolchains: "maven",
		Sources: []manifest.Source{{URL: "https://example.com/maven", SHA256: strings.Repeat("a", 64)}},
		Bin:     []manifest.Binary{{Name: "mvn", Path: "{app}/bin/mvn"}},
	})

	if err := a.regenerateToolchains(); err != nil {
		t.Fatal(err)
	}

	gp, err := os.ReadFile(filepath.Join(root, "var", "app", "gradle", "gradle", "gradle.properties"))
	if err != nil {
		t.Fatalf("gradle.properties: %v", err)
	}
	for _, want := range []string{
		filepath.Join(root, "app", "jdk-21"),
		filepath.Join(root, "app", "jdk-25"),
		"auto-download=false",
	} {
		if !strings.Contains(string(gp), want) {
			t.Errorf("gradle.properties missing %q:\n%s", want, gp)
		}
	}

	tx, err := os.ReadFile(filepath.Join(root, "var", "app", "maven", "toolchains.xml"))
	if err != nil {
		t.Fatalf("toolchains.xml: %v", err)
	}
	for _, want := range []string{"<version>21</version>", "<version>25</version>", filepath.Join(root, "app", "jdk-21")} {
		if !strings.Contains(string(tx), want) {
			t.Errorf("toolchains.xml missing %q:\n%s", want, tx)
		}
	}
}

// cacheManifest writes m to path as YAML (mirrors the installer's manifest cache).
func cacheManifest(t *testing.T, path string, m *manifest.Manifest) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0755)
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRequireInCatalogSuggests(t *testing.T) {
	pkgs := []catalog.PackageInfo{{ID: "jdk-21"}, {ID: "jdk-25"}, {ID: "maven"}}
	err := requireInCatalog("jdk-2", pkgs)
	if err == nil || !strings.Contains(err.Error(), `did you mean "jdk-21"`) {
		t.Fatalf("err = %v, want suggestion of jdk-21", err)
	}
}

func TestRequireInCatalogHit(t *testing.T) {
	pkgs := []catalog.PackageInfo{{ID: "maven"}}
	if err := requireInCatalog("maven", pkgs); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
