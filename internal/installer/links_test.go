package installer

import (
	"os"
	"path/filepath"
	"testing"
)

// A configured link follows install and update, goes on uninstall, and never
// replaces a path bunny did not make.
func TestInstallAndUninstallMaintainLinks(t *testing.T) {
	i := reviewInstaller(t)
	link := filepath.Join(t.TempDir(), "opt", "probe")
	i.Links = map[string]string{"probe": link}

	if err := i.Install(t.Context(), "probe", false, nil); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != i.Paths.AppDir("probe") {
		t.Fatalf("link = %q, %v; want the install directory", target, err)
	}
	if err := i.Uninstall("probe", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("link should be gone after uninstall: %v", err)
	}
}

func TestSyncLinksLeavesForeignPaths(t *testing.T) {
	i := reviewInstaller(t)
	if err := i.Install(t.Context(), "probe", false, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	userDir := filepath.Join(dir, "dir")
	userLink := filepath.Join(dir, "link")
	for _, err := range []error{os.Mkdir(userDir, 0755), os.Symlink("/elsewhere", userLink)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{userDir, userLink, filepath.Join(i.Paths.Root, "mine")} {
		if err := SyncLinks(i.Paths, i.State, map[string]string{"probe": path}); err == nil {
			t.Errorf("%s: expected a refusal", path)
		}
	}
	if target, _ := os.Readlink(userLink); target != "/elsewhere" {
		t.Errorf("a link bunny did not make was changed to %q", target)
	}
}
