package installer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cristatus/bunny/internal/state"
)

// storeVersions lists the versions the store holds for id.
func storeVersions(t *testing.T, i *Installer, id string) []string {
	t.Helper()
	entries, err := os.ReadDir(i.Paths.PackageStore(id))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func installVersion(t *testing.T, i *Installer, version string) {
	t.Helper()
	i.Catalog.(*fakeCatalog).manifests["probe"].Version = version
	if err := i.Install(t.Context(), "probe", true, nil); err != nil {
		t.Fatalf("install %s: %v", version, err)
	}
}

func linkedTo(t *testing.T, i *Installer, id string) string {
	t.Helper()
	return linkedVersionDir(i.Paths.AppDir(id))
}

// An update places the new tree beside the old ones and switches the link.
// No version is removed: a process started from one may still be running,
// and paths a tool resolved through the link stay valid until `bunny clean`.
func TestUpdatesKeepEveryVersion(t *testing.T) {
	i := reviewInstaller(t)
	for _, v := range []string{"1.0", "2.0", "3.0"} {
		installVersion(t, i, v)
	}
	if got := linkedTo(t, i, "probe"); got != i.Paths.VersionDir("probe", "3.0") {
		t.Fatalf("link resolves to %q, want the 3.0 tree", got)
	}
	if target, _ := os.Readlink(i.Paths.AppDir("probe")); filepath.IsAbs(target) {
		t.Errorf("link target %q should be relative, so the root can move", target)
	}
	if got := storeVersions(t, i, "probe"); !slices.Equal(got, []string{"1.0", "2.0", "3.0"}) {
		t.Errorf("store holds %v, want every installed version", got)
	}
	if _, err := os.Stat(i.Paths.StagingDir("probe")); !os.IsNotExist(err) {
		t.Errorf("a finished install leaves nothing in staging: %v", err)
	}
}

// Dying before state commits leaves the link on the old version, and recovery
// drops the tree the install placed.
func TestInterruptedUpgradeRestoresTheLink(t *testing.T) {
	i := reviewInstaller(t)
	installVersion(t, i, "1.0")
	i.Catalog.(*fakeCatalog).manifests["probe"].Version = "2.0"
	i.SaveState = func(_ *state.State, _ string) error { panic("simulated process death before state commit") }
	func() { defer func() { _ = recover() }(); _ = i.Install(t.Context(), "probe", true, nil) }()
	if err := RecoverPendingInstall(i.Paths); err != nil {
		t.Fatal(err)
	}
	if got := linkedTo(t, i, "probe"); got != i.Paths.VersionDir("probe", "1.0") {
		t.Fatalf("link resolves to %q, want it back on 1.0", got)
	}
	if got := storeVersions(t, i, "probe"); !slices.Equal(got, []string{"1.0"}) {
		t.Errorf("store holds %v after recovery, want only 1.0", got)
	}
}

// Reinstalling the same version sets its tree aside in staging. Whether the
// reinstall fails or the process dies, the original tree comes back intact.
func TestFailedReinstallOfTheSameVersionKeepsTheTree(t *testing.T) {
	for _, crash := range []bool{false, true} {
		i := reviewInstaller(t)
		installVersion(t, i, "1.0")
		mine := filepath.Join(i.Paths.VersionDir("probe", "1.0"), "mine")
		if err := os.WriteFile(mine, []byte("original"), 0644); err != nil {
			t.Fatal(err)
		}
		i.SaveState = func(_ *state.State, _ string) error {
			if crash {
				panic("simulated process death before state commit")
			}
			return errors.New("disk full")
		}
		func() { defer func() { _ = recover() }(); _ = i.Install(t.Context(), "probe", true, nil) }()
		// A failed install rolls back by itself; only a crash needs recovery.
		if crash {
			if err := RecoverPendingInstall(i.Paths); err != nil {
				t.Fatal(err)
			}
		}
		if data, err := os.ReadFile(mine); err != nil || string(data) != "original" {
			t.Errorf("crash=%v: the reinstalled version's tree was not restored: %q, %v", crash, data, err)
		}
		if got := linkedTo(t, i, "probe"); got != i.Paths.VersionDir("probe", "1.0") {
			t.Errorf("crash=%v: link resolves to %q, want 1.0", crash, got)
		}
	}
}

// Uninstall removes the link and every version.
func TestUninstallRemovesEveryVersion(t *testing.T) {
	i := reviewInstaller(t)
	installVersion(t, i, "1.0")
	installVersion(t, i, "2.0")
	if err := i.Uninstall("probe", false); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{i.Paths.AppDir("probe"), i.Paths.PackageStore("probe"), i.Paths.StagingDir("probe")} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be gone: %v", gone, err)
		}
	}
}

// clean drops the versions no link points at and trees of packages no longer
// installed, never the linked version or a directory bunny cannot show is its
// own.
func TestCleanPrunesTheStoreToLinkedVersions(t *testing.T) {
	i := reviewInstaller(t)
	installVersion(t, i, "1.0")
	installVersion(t, i, "2.0")
	orphan := i.Paths.VersionDir("gone", "1")
	foreign := i.Paths.VersionDir("mine", "1")
	for _, dir := range []string{orphan, foreign} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := writePackageMarker(orphan, packageMarker{ID: "gone", Version: "1"}); err != nil {
		t.Fatal(err)
	}

	stale, err := StaleVersions(i.Paths, i.State)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{orphan, i.Paths.VersionDir("probe", "1.0")}; !slices.Equal(stale, want) {
		t.Errorf("StaleVersions = %v, want %v", stale, want)
	}

	report, err := NewCleaner(i.Paths, i.Catalog, i.State).Clean("", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := storeVersions(t, i, "probe"); !slices.Equal(got, []string{"2.0"}) {
		t.Errorf("store holds %v, want only the linked 2.0", got)
	}
	if _, err := os.Stat(i.Paths.PackageStore("gone")); !os.IsNotExist(err) {
		t.Errorf("an uninstalled package's store dir should be gone: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("an unmarked directory must be left alone: %v", err)
	}
	if !slices.Contains(report.Removed, i.Paths.VersionDir("probe", "1.0")) {
		t.Errorf("report should name the removed version: %v", report.Removed)
	}
}

// An uninstall that dies after moving the package's link aside, before state
// commits, leaves a package state still records with no link. clean must keep
// its tree, or the package is installed with nothing behind it.
func TestCleanKeepsTheVersionStateRecords(t *testing.T) {
	i := reviewInstaller(t)
	installVersion(t, i, "1.0")
	if _, err := i.stageRemoveApp("probe"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCleaner(i.Paths, i.Catalog, i.State).Clean("", false); err != nil {
		t.Fatal(err)
	}
	if got := storeVersions(t, i, "probe"); !slices.Equal(got, []string{"1.0"}) {
		t.Errorf("store holds %v, want the installed 1.0 kept", got)
	}
}

// A package whose link is already gone still leaves nothing behind in the
// store when it is uninstalled.
func TestUninstallWithoutALinkRemovesTheTrees(t *testing.T) {
	i := reviewInstaller(t)
	installVersion(t, i, "1.0")
	if err := os.Remove(i.Paths.AppDir("probe")); err != nil {
		t.Fatal(err)
	}
	if err := i.Uninstall("probe", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(i.Paths.PackageStore("probe")); !os.IsNotExist(err) {
		t.Errorf("the store dir should be gone: %v", err)
	}
}
