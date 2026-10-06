package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// Each capability's link follows its active provider. A link for a
// capability nothing installed provides goes; anything that is not a link
// stays, since bunny did not make it.
func TestSyncCurrentFollowsProviders(t *testing.T) {
	st := state.Empty()
	p := paths.At(t.TempDir()).WithKinds(st.KindOf)
	st.SetInstalled("jdk-21", "21", "jdk", manifest.KindSDK)
	st.Providers["node"] = "node-22" // recorded, but not installed

	for _, dir := range []string{p.CurrentDir(), filepath.Join(p.CurrentDir(), "mine")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	stale := p.Current("maven")
	if err := os.Symlink("../sdk/maven", stale); err != nil {
		t.Fatal(err)
	}

	if err := SyncCurrent(p, st); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(p.Current("jdk")); err != nil || target != filepath.Join("..", "sdk", "jdk-21") {
		t.Errorf("current/jdk = %q, %v; want ../sdk/jdk-21", target, err)
	}
	for _, gone := range []string{stale, p.Current("node")} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should not exist: %v", gone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.CurrentDir(), "mine")); err != nil {
		t.Errorf("a directory bunny did not make must stay: %v", err)
	}
}

// Installing a provider creates its capability's link and uninstalling the
// last provider removes it.
func TestInstallAndUninstallMaintainCurrent(t *testing.T) {
	i := reviewInstaller(t)
	i.Catalog.(*fakeCatalog).manifests["probe"].Provides = "probing"
	if err := i.Install(t.Context(), "probe", false, nil); err != nil {
		t.Fatal(err)
	}
	link := i.Paths.Current("probing")
	if resolved, err := filepath.EvalSymlinks(link); err != nil || resolved != i.Paths.VersionDir("probe", "1.0") {
		t.Errorf("current/probing resolves to %q, %v; want the installed tree", resolved, err)
	}
	if err := i.Uninstall("probe", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("current/probing should be gone after uninstall: %v", err)
	}
}
