package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/state"
)

func reviewInstaller(t *testing.T) *Installer {
	t.Helper()
	src := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{ID: "probe", Name: "Probe", Version: "1.0", Sources: []manifest.Source{{URL: "file://" + src, SHA256: sha256Of("payload")}}, Bin: []manifest.Binary{{Name: "probe", Path: "{app}/probe"}}}
	return installerWith(t, map[string]*manifest.Manifest{"probe": m}, nil)
}
func TestUninstallRefusesSymlinkPackageRoot(t *testing.T) {
	i := reviewInstaller(t)
	if err := i.Install(context.Background(), "probe", false, nil); err != nil {
		t.Fatal(err)
	}
	victim := t.TempDir()
	sentinel := filepath.Join(victim, "user-data")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writePackageMarker(victim, packageMarker{ID: "probe", Version: "1.0"}); err != nil {
		t.Fatal(err)
	}
	app := i.Paths.AppDir("probe")
	if err := os.RemoveAll(app); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, app); err != nil {
		t.Fatal(err)
	}
	if err := i.Uninstall("probe", false); err == nil {
		t.Fatal("uninstall accepted symlink package root")
	}
	if !i.State.IsInstalled("probe") {
		t.Fatal("uninstall changed state after refusing the symlink")
	}
	if info, err := os.Lstat(app); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("uninstall moved the symlink: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("uninstall deleted data outside its install tree: %v", err)
	}
}
func TestInstallRejectsMissingExecutable(t *testing.T) {
	i := reviewInstaller(t)
	i.ValidateBinaries = validateBinaries
	if err := i.Install(context.Background(), "probe", false, nil); err == nil {
		t.Fatal("install accepted a missing executable")
	}
	if i.State.IsInstalled("probe") {
		t.Fatal("failed install was recorded in state")
	}
}
func TestInstallRejectsSymlinkCreatedByPrepare(t *testing.T) {
	i := reviewInstaller(t)
	victim := t.TempDir()
	i.Catalog.(*fakeCatalog).manifests["probe"].Prepare = []string{"create package root"}
	i.Prepare = func(_ context.Context, workDir, _ string, _ map[string]string, _ []string, _ map[string]string) error {
		pkg := filepath.Join(workDir, "pkg")
		if err := os.Remove(pkg); err != nil {
			return err
		}
		return os.Symlink(victim, pkg)
	}
	if err := i.Install(context.Background(), "probe", false, nil); err == nil {
		t.Fatal("install accepted symlink package root")
	}
	if _, err := os.Stat(filepath.Join(victim, packageMarkerName)); err == nil {
		t.Fatal("installer followed prepared symlink and wrote marker outside staging")
	}
}

func TestInterruptedUpgradeRestoresPreviousInstallation(t *testing.T) {
	i := reviewInstaller(t)
	if err := i.Install(context.Background(), "probe", false, nil); err != nil {
		t.Fatal(err)
	}
	i.Catalog.(*fakeCatalog).manifests["probe"].Version = "2.0"
	i.SaveState = func(_ *state.State, _ string) error { panic("simulated process death before state commit") }
	func() { defer func() { _ = recover() }(); _ = i.Install(context.Background(), "probe", true, nil) }()
	if err := RecoverPendingInstall(i.Paths); err != nil {
		t.Fatal(err)
	}
	restarted, err := state.Load(i.Paths.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	installed, err := i.Installed.Load("probe")
	if err != nil {
		t.Fatal(err)
	}
	marker, err := readPackageMarker(i.Paths.AppDir("probe"))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.VersionOf("probe") != installed.Version || restarted.VersionOf("probe") != marker.Version {
		t.Fatalf("restart accepted inconsistent upgrade: state=%s, manifest=%s, installed tree=%s", restarted.VersionOf("probe"), installed.Version, marker.Version)
	}
}

func TestInterruptedUpgradeRestoresDesktopEntry(t *testing.T) {
	i := reviewInstaller(t)
	m := i.Catalog.(*fakeCatalog).manifests["probe"]
	m.Desktop = []manifest.DesktopEntry{{ID: "probe.desktop", Name: "Probe", Exec: "old"}}
	if err := i.Install(context.Background(), "probe", false, nil); err != nil {
		t.Fatal(err)
	}
	desktopPath := filepath.Join(i.Paths.Desktop(), "probe.desktop")
	before, err := os.ReadFile(desktopPath)
	if err != nil {
		t.Fatal(err)
	}
	m.Version = "2.0"
	m.Desktop[0].Exec = "new"
	i.SaveState = func(_ *state.State, _ string) error { panic("simulated process death") }
	func() { defer func() { _ = recover() }(); _ = i.Install(context.Background(), "probe", true, nil) }()
	if err := RecoverPendingInstall(i.Paths); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(desktopPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("desktop entry not restored: %q", after)
	}
}

func TestInterruptedAfterStateCommitKeepsUpgrade(t *testing.T) {
	i := reviewInstaller(t)
	if err := i.Install(context.Background(), "probe", false, nil); err != nil {
		t.Fatal(err)
	}
	i.Catalog.(*fakeCatalog).manifests["probe"].Version = "2.0"
	i.SaveState = func(st *state.State, path string) error {
		if err := st.Save(path); err != nil {
			return err
		}
		panic("simulated process death after commit")
	}
	func() { defer func() { _ = recover() }(); _ = i.Install(context.Background(), "probe", true, nil) }()
	if err := RecoverPendingInstall(i.Paths); err != nil {
		t.Fatal(err)
	}
	reloaded, err := state.Load(i.Paths.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.VersionOf("probe") != "2.0" {
		t.Fatalf("committed upgrade was rolled back: %s", reloaded.VersionOf("probe"))
	}
	if _, err := os.Lstat(i.Paths.AppDir("probe") + ".old"); !os.IsNotExist(err) {
		t.Fatalf("old package tree was not cleaned: %v", err)
	}
}

func TestInterruptedFirstInstallRemovesUncommittedTree(t *testing.T) {
	i := reviewInstaller(t)
	i.SaveState = func(_ *state.State, _ string) error { panic("simulated process death") }
	func() { defer func() { _ = recover() }(); _ = i.Install(context.Background(), "probe", false, nil) }()
	if err := RecoverPendingInstall(i.Paths); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(i.Paths.AppDir("probe")); !os.IsNotExist(err) {
		t.Fatalf("uncommitted tree remains: %v", err)
	}
	if _, err := os.Lstat(i.Paths.Shim("probe")); !os.IsNotExist(err) {
		t.Fatalf("uncommitted shim remains: %v", err)
	}
	if _, err := os.Lstat(i.Paths.ManifestFile("probe")); !os.IsNotExist(err) {
		t.Fatalf("uncommitted manifest remains: %v", err)
	}
}

func TestInstallRejectsNonExecutableBinary(t *testing.T) {
	i := reviewInstaller(t)
	i.ValidateBinaries = validateBinaries
	i.Prepare = func(_ context.Context, workDir, srcDir string, shadow map[string]string, commands []string, vars map[string]string) error {
		return os.WriteFile(filepath.Join(workDir, "pkg", "probe"), []byte("not executable"), 0644)
	}
	if err := i.Install(context.Background(), "probe", false, nil); err == nil {
		t.Fatal("install accepted a non-executable binary")
	}
}

func TestRejectedInstallDoesNotCleanPreexistingTree(t *testing.T) {
	i := reviewInstaller(t)
	target := i.Paths.AppDir("probe")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writePackageMarker(target, packageMarker{ID: "probe", Version: "1.0"}); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(target, "user-data")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), "probe", false, nil); err == nil {
		t.Fatal("install replaced an existing tree")
	}
	if err := RecoverPendingInstall(i.Paths); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "keep" {
		t.Fatalf("preexisting tree was removed: %q, %v", data, err)
	}
}

func TestInstallPreservesDanglingSymlinkDestination(t *testing.T) {
	i := reviewInstaller(t)
	target := i.Paths.AppDir("probe")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), target); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), "probe", false, nil); err == nil {
		t.Fatal("install replaced dangling symlink")
	}
	if err := RecoverPendingInstall(i.Paths); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination symlink was replaced: %v", err)
	}
}

func TestInterruptedUpgradeRestoresLegacyTreeWithoutMarker(t *testing.T) {
	i := reviewInstaller(t)
	if err := i.Install(context.Background(), "probe", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(i.Paths.AppDir("probe"), packageMarkerName)); err != nil {
		t.Fatal(err)
	}
	i.Catalog.(*fakeCatalog).manifests["probe"].Version = "2.0"
	i.SaveState = func(_ *state.State, _ string) error { panic("simulated process death") }
	func() { defer func() { _ = recover() }(); _ = i.Install(context.Background(), "probe", true, nil) }()
	if err := RecoverPendingInstall(i.Paths); err != nil {
		t.Fatal(err)
	}
	reloaded, err := state.Load(i.Paths.StateFile())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.VersionOf("probe") != "1.0" {
		t.Fatalf("legacy installation was not restored: %s", reloaded.VersionOf("probe"))
	}
}
