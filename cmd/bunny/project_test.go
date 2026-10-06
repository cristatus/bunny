package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/installer"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/runtime"
	"github.com/cristatus/bunny/internal/state"
	"gopkg.in/yaml.v3"
)

func sdkApp(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root, checkout := t.TempDir(), t.TempDir()
	a := &App{State: state.Empty(), Config: &config.Config{}, Catalog: catalog.NewLocal(checkout), NoProgress: true}
	a.Paths = paths.At(root).WithKinds(func(id string) string { return a.State.KindOf(id) })
	os.MkdirAll(a.Paths.Bin(), 0755)
	os.WriteFile(a.Paths.BunnyBinary(), []byte("#!/bin/sh\n"), 0755)
	a.Installer = installer.New(a.Paths, a.Catalog, a.State)
	a.Installer.Prepare = func(ctx context.Context, work, src string, shadow map[string]string, commands []string, vars map[string]string) error {
		if err := os.MkdirAll(filepath.Join(vars["pkg"], "bin"), 0755); err != nil {
			return err
		}
		for _, name := range []string{"java", "node", "mvn", "gradle"} {
			if err := os.WriteFile(filepath.Join(vars["pkg"], "bin", name), []byte("#!/bin/sh\necho "+vars["version"]+"\n"), 0755); err != nil {
				return err
			}
		}
		return nil
	}
	return a, checkout
}
func addSDK(t *testing.T, root, id, cap, version, binary string, requires []string, edit ...func(*manifest.Manifest)) {
	t.Helper()
	dir := filepath.Join(root, "packages", id)
	os.MkdirAll(dir, 0755)
	payload := "payload-" + id + "-" + version
	source := filepath.Join(t.TempDir(), "sdk.tar.gz")
	os.WriteFile(source, []byte(payload), 0644)
	sum := sha256.Sum256([]byte(payload))
	m := &manifest.Manifest{ID: id, Name: id, Version: version, Kind: manifest.KindSDK, Provides: cap, Requires: requires, Sources: []manifest.Source{{URL: "file://" + source, SHA256: hex.EncodeToString(sum[:]), File: "sdk.tar.gz"}}, Prepare: []string{"extract"}, Bin: []manifest.Binary{{Name: binary, Path: "{app}/bin/" + binary}}}
	if id == "maven" || id == "gradle" {
		m.Toolchains = id
	}
	if cap == "node" {
		m.GlobalBins = []string{"{app}/bin"}
	}
	if cap == "jdk" {
		m.Env = map[string]string{"JAVA_HOME": "{app}"}
	}
	for _, fn := range edit {
		fn(m)
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), data, 0644); err != nil {
		t.Fatal(err)
	}

}
func installSDK(t *testing.T, a *App, ref string) {
	t.Helper()
	if err := (&InstallCmd{IDs: []string{ref}}).Run(a); err != nil {
		t.Fatal(err)
	}
}
func TestProjectInstallInheritsSDKsAndPreservesGlobalProvider(t *testing.T) {
	a, root := sdkApp(t)
	addSDK(t, root, "jdk-17", "jdk", "17.0.1", "java", nil)
	installSDK(t, a, "jdk-17")
	addSDK(t, root, "jdk-21", "jdk", "21.0.1", "java", nil)
	addSDK(t, root, "maven", "maven", "3.9.9", "mvn", []string{"jdk>=17"})
	addSDK(t, root, "gradle", "gradle", "8.14", "gradle", []string{"jdk>=17"})
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".bunny-version"), []byte("jdk 21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, ".bunny-version"), []byte("maven maven\ngradle gradle\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	if err := (&InstallCmd{Project: true}).Run(a); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"jdk-21", "maven", "gradle"} {
		if !a.State.IsInstalled(id) {
			t.Fatal("missing project SDK", id)
		}
		if strings.Contains(id, "@") {
			t.Fatal("qualified installation key", id)
		}
	}
	if a.State.Providers["jdk"] != "jdk-17" {
		t.Fatal("project install changed global Java")
	}
	for id, command := range map[string]string{"maven": "mvn", "gradle": "gradle"} {
		prep, err := a.preparePackage(id, command, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, v := range prep.Env {
			// The version the link points at now, not the link: a running
			// build keeps its JDK when an update switches the link.
			found = found || v == "JAVA_HOME="+a.Paths.VersionDir("jdk-21", a.State.VersionOf("jdk-21"))
		}
		if !found {
			t.Fatal("project Java environment missing", id, prep.Env)
		}
		explanation, err := runtime.Explain(prep, a.Config, runtime.ActivationDefault, "", a.status())
		if err != nil || !strings.Contains(explanation, "runs directly") {
			t.Fatalf("%s %v", explanation, err)
		}
	}
	oldPath := a.Paths.AppDir("jdk-21")
	addSDK(t, root, "jdk-21", "jdk", "21.0.2", "java", nil)
	if err := (&InstallCmd{Project: true}).Run(a); err != nil {
		t.Fatal(err)
	}
	if a.State.VersionOf("jdk-21") != "21.0.1" {
		t.Fatal("project install updated an existing SDK")
	}
	t.Chdir(t.TempDir())
	if err := (&UpdateCmd{ID: "jdk-21", Apply: true}).Run(a); err != nil {
		t.Fatal(err)
	}
	if a.State.VersionOf("jdk-21") != "21.0.2" || a.Paths.AppDir("jdk-21") != oldPath {
		t.Fatal("update changed package identity", a.State.Packages)
	}
}

func TestProjectInstallPreflightsBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"incompatible", "wrong-capability", "non-sdk", "cycle", "exact"} {
		t.Run(scenario, func(t *testing.T) {
			a, root := sdkApp(t)
			addSDK(t, root, "jdk-21", "jdk", "21.0.1", "java", nil)
			addSDK(t, root, "gradle", "gradle", "9.0", "gradle", []string{"jdk>=25"})
			contents := "jdk 21\ngradle gradle\n"
			switch scenario {
			case "wrong-capability":
				contents = "node jdk-21\n"
			case "non-sdk":
				addSDK(t, root, "node-22", "node", "22.1.0", "node", nil, func(m *manifest.Manifest) { m.Kind = manifest.KindCLI })
				contents = "node 22\n"
			case "cycle":
				addSDK(t, root, "gradle", "gradle", "9.0", "gradle", []string{"maven"})
				addSDK(t, root, "maven", "maven", "3.9.9", "mvn", []string{"gradle"})
				contents = "maven maven\ngradle gradle\n"
			case "exact":
				contents = "jdk jdk-21@21.0.1\n"
			}
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, ".bunny-version"), []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(project)
			if err := (&InstallCmd{Project: true}).Run(a); err == nil {
				t.Fatal("invalid project accepted")
			}
			if len(a.State.Packages) != 0 {
				t.Fatal("preflight changed installed packages", a.State.Packages)
			}
		})
	}
}

func TestPinInitCapturesInheritedSDKPackagesAndPreservesFile(t *testing.T) {
	a, root := sdkApp(t)
	addSDK(t, root, "jdk-17", "jdk", "17.0.1", "java", nil)
	addSDK(t, root, "corretto-21", "jdk", "21.0.1", "java", nil)
	addSDK(t, root, "maven", "maven", "3.9.9", "mvn", []string{"jdk"})
	installSDK(t, a, "jdk-17")
	installSDK(t, a, "corretto-21")
	installSDK(t, a, "maven")
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".bunny-version"), []byte("jdk corretto-21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(child)
	if err := (&PinCmd{Init: true}).Run(a); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(child, ".bunny-version")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "jdk corretto-21\nmaven maven\n" {
		t.Fatal(string(data))
	}
	if err := (&PinCmd{Init: true}).Run(a); err == nil {
		t.Fatal("existing file overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatalf("existing pins changed: %s %v", after, err)
	}
}

func TestProjectInstallArguments(t *testing.T) {
	for _, cmd := range []InstallCmd{{}, {Project: true, IDs: []string{"jdk-21"}}} {
		if err := cmd.Run(&App{}); err == nil {
			t.Fatal("invalid install arguments accepted")
		}
	}
}

func TestInactiveProjectSDKKeepsActiveShim(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(fmt.Sprint(failSave), func(t *testing.T) {
			a, checkout := sdkApp(t)
			addSDK(t, checkout, "jdk-17", "jdk", "17.0.1", "java", nil)
			addSDK(t, checkout, "jdk-21", "jdk", "21.0.1", "java", nil)
			installSDK(t, a, "jdk-17")
			if failSave {
				a.Installer.SaveState = func(*state.State, string) error { return errors.New("save failed") }
			}
			err := a.Installer.Install(context.Background(), "jdk-21", false, nil)
			if failSave && err == nil {
				t.Fatal("expected save failure")
			}
			if !failSave {
				if err != nil {
					t.Fatal(err)
				}
				if err := a.Installer.Uninstall("jdk-21", false); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(filepath.Join(a.Paths.Bin(), "java")); err != nil {
				t.Fatalf("active shim lost: %v", err)
			}
			if got := a.State.ResolveProvider("jdk"); got != "jdk-17" {
				t.Fatalf("active provider = %s", got)
			}
		})
	}
}

func TestProjectInstallForcePreflightsNewManifest(t *testing.T) {
	a, root := sdkApp(t)
	addSDK(t, root, "jdk-21", "jdk", "21.0.1", "java", nil)
	addSDK(t, root, "maven", "maven", "3.9.9", "mvn", []string{"jdk>=21"})
	installSDK(t, a, "jdk-21")
	installSDK(t, a, "maven")
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".bunny-version"), []byte("jdk 21\nmaven maven\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	addSDK(t, root, "maven", "maven", "4.0.0", "mvn", []string{"jdk>=25"})
	if err := (&InstallCmd{Project: true, Force: true}).Run(a); err == nil {
		t.Fatal("new incompatible manifest accepted")
	}
	if got := a.State.VersionOf("maven"); got != "3.9.9" {
		t.Fatalf("preflight changed installation: %s", got)
	}
}

func TestProjectInstallSkipsInstalledPackageRemovedFromCatalog(t *testing.T) {
	a, root := sdkApp(t)
	addSDK(t, root, "jdk-21", "jdk", "21.0.1", "java", nil)
	installSDK(t, a, "jdk-21")
	// Leave a valid catalog entry so an unknown ID check would otherwise reject it.
	addSDK(t, root, "node-22", "node", "22.1.0", "node", nil)
	if err := os.RemoveAll(filepath.Join(root, "packages", "jdk-21")); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".bunny-version"), []byte("jdk 21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	if err := (&InstallCmd{Project: true}).Run(a); err != nil {
		t.Fatal(err)
	}
	if got := a.State.VersionOf("jdk-21"); got != "21.0.1" {
		t.Fatalf("installed version = %s", got)
	}
}

func TestInactiveSDKReinstallRetainsIntegrationOwnership(t *testing.T) {
	a, root := sdkApp(t)
	addSDK(t, root, "jdk-17", "jdk", "17.0.1", "java", nil)
	addSDK(t, root, "jdk-21", "jdk", "21.0.1", "java", nil, func(m *manifest.Manifest) {
		m.Completions = &manifest.Completions{Bash: "{app}/bin/java"}
	})
	installSDK(t, a, "jdk-17")
	installSDK(t, a, "jdk-21")
	file := filepath.Join(a.Paths.BashCompletions(), "java")
	if _, err := os.Stat(file); err != nil {
		t.Fatal(err)
	}
	if err := a.Installer.Install(context.Background(), "jdk-21", true, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(a.State.Packages["jdk-21"].Files, file) {
		t.Fatalf("inactive reinstall lost integration ownership: %v", a.State.Packages["jdk-21"].Files)
	}
	if err := a.Installer.Uninstall("jdk-21", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("integration left orphaned: %v", err)
	}
	if got := a.State.ResolveProvider("jdk"); got != "jdk-17" {
		t.Fatalf("active provider = %s", got)
	}
	if _, err := os.Stat(a.Paths.Shim("java")); err != nil {
		t.Fatalf("active shim lost: %v", err)
	}
}

// --project learns its packages from the pins, under the lock. The reporter
// sizes its id column from them, so it has to start once they are known, or
// every line comes out unpadded and the status column wanders.
func TestProjectInstallAlignsItsOutput(t *testing.T) {
	a, root := sdkApp(t)
	addSDK(t, root, "jdk-21", "jdk", "21.0.1", "java", nil)
	addSDK(t, root, "maven", "maven", "3.9.9", "mvn", []string{"jdk>=17"})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".bunny-version"), []byte("jdk 21\nmaven maven\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = write
	runErr := (&InstallCmd{Project: true}).Run(a)
	os.Stderr = stderr
	write.Close()
	out, _ := io.ReadAll(read)
	if runErr != nil {
		t.Fatal(runErr)
	}

	columns := map[int]bool{}
	for line := range strings.Lines(string(out)) {
		if i := strings.Index(line, "   installed"); i > 0 {
			columns[i] = true
		}
	}
	if len(columns) != 1 {
		t.Errorf("status column is not aligned:\n%s", out)
	}
}
