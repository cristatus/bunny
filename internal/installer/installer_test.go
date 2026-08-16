package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

func TestMarkDisposableWritesMarkers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")

	markDisposable(dir)

	tag, err := os.ReadFile(filepath.Join(dir, "CACHEDIR.TAG"))
	if err != nil {
		t.Fatalf("CACHEDIR.TAG not written: %v", err)
	}
	if !strings.HasPrefix(string(tag), "Signature: 8a477f597d28d172789f06886806bc55") {
		t.Errorf("CACHEDIR.TAG missing the spec signature, got: %q", string(tag))
	}
	if _, err := os.Stat(filepath.Join(dir, ".nobackup")); err != nil {
		t.Errorf(".nobackup not written: %v", err)
	}

	// Idempotent: re-marking must not clobber existing markers.
	custom := []byte("user edited\n")
	if err := os.WriteFile(filepath.Join(dir, ".nobackup"), custom, 0644); err != nil {
		t.Fatal(err)
	}
	markDisposable(dir)
	got, _ := os.ReadFile(filepath.Join(dir, ".nobackup"))
	if string(got) != string(custom) {
		t.Errorf("re-mark clobbered existing .nobackup: %q", string(got))
	}
}

// fakeCatalog is a tiny in-memory Loader for tests.
type fakeCatalog struct {
	manifests map[string]*manifest.Manifest
	files     map[string][]byte
}

func (c *fakeCatalog) List() ([]catalog.PackageInfo, error) { return nil, nil }
func (c *fakeCatalog) Load(id string) (*manifest.Manifest, error) {
	m, ok := c.manifests[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", catalog.ErrNotFound, id)
	}
	return m, nil
}
func (c *fakeCatalog) LoadFile(id, rel string) ([]byte, error) {
	d, ok := c.files[id+"/"+rel]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", catalog.ErrNotFound, id, rel)
	}
	return d, nil
}

// noopPrepare just creates an empty marker file in pkgDir so place() has
// something non-empty to rename.
func noopPrepare(_ context.Context, workDir, srcDir string, shadow map[string]string, commands []string, vars map[string]string) error {
	return os.WriteFile(filepath.Join(workDir, "pkg", "marker"), []byte("ok"), 0644)
}

// installerWith builds an Installer wired to a temp $BUNNY_HOME, a fake
// bunny binary, and a noop Prepare.
func installerWith(t *testing.T, manifests map[string]*manifest.Manifest, files map[string][]byte) *Installer {
	t.Helper()
	root := t.TempDir()
	st := state.Empty()
	// Wire the layout the way cmd/bunny does, so AppDir resolves through the
	// kind state recorded at install time instead of guessing a root. Without
	// it the tests exercise a path resolution production never uses.
	paths := paths.At(root).WithLayout(nil, st.Location)

	// Pretend bunny is in BinDir so shim.Install has a target.
	os.MkdirAll(paths.Bin(), 0755)
	os.WriteFile(paths.BunnyBinary(), []byte("#!/bin/sh\n"), 0755)
	t.Setenv("PATH", paths.Bin()+":"+os.Getenv("PATH"))

	cat := &fakeCatalog{manifests: manifests, files: files}
	i := New(paths, cat, st)
	i.Prepare = noopPrepare
	i.Download = NewDownloader() // file:// only — no network
	i.BunnyPath = func(binDir string) (string, error) {
		return paths.BunnyBinary(), nil
	}
	return i
}

func TestInstallEndToEnd(t *testing.T) {
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "rg.tar.gz")
	os.WriteFile(srcFile, []byte("payload"), 0644)

	m := &manifest.Manifest{
		ID:      "rg",
		Name:    "ripgrep",
		Version: "14.1.0",
		Sources: []manifest.Source{
			{URL: "file://" + srcFile, File: "rg.tar.gz", SHA256: sha256Of("payload")},
		},
		Bin: []manifest.Binary{{Name: "rg", Path: "{app}/rg"}},
	}

	i := installerWith(t, map[string]*manifest.Manifest{"rg": m}, nil)
	if err := i.Install(context.Background(), "rg", false, nil); err != nil {
		t.Fatal(err)
	}
	if !i.State.IsInstalled("rg") {
		t.Fatal("not in state")
	}
	if owner, _ := i.State.CommandOwner("rg"); owner != "rg" {
		t.Errorf("command map: got %s", owner)
	}
	// Shim symlink should exist.
	target, err := os.Readlink(i.Paths.Shim("rg"))
	if err != nil {
		t.Fatalf("shim link: %v", err)
	}
	if target != i.Paths.BunnyBinary() {
		t.Errorf("shim → %s, want %s", target, i.Paths.BunnyBinary())
	}
	// State file persisted.
	if _, err := os.Stat(i.Paths.StateFile()); err != nil {
		t.Errorf("state file: %v", err)
	}
	// Manifest cached so runtime doesn't need to hit the catalog/remote.
	cachePath := i.Paths.ManifestFile("rg")
	cached, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("manifest cache: %v", err)
	}
	parsed, err := manifest.ParseBytes(cached)
	if err != nil {
		t.Fatalf("re-parse cached manifest: %v", err)
	}
	if parsed.ID != "rg" || parsed.Version != "14.1.0" {
		t.Errorf("cached manifest mismatch: id=%q version=%q", parsed.ID, parsed.Version)
	}
	// The snapshot lives beside state.json, never inside {data}. A package
	// told to keep its data there may have it cleared by its own tooling or
	// by hand, and that must not take bunny's record of the install with it.
	if entries, err := os.ReadDir(i.Paths.AppData("rg")); err == nil && len(entries) > 0 {
		t.Errorf("nothing unrecoverable belongs in {data}, found %d entries", len(entries))
	}
}

func TestCheckRequiresVersionConstraint(t *testing.T) {
	m := &manifest.Manifest{
		ID:       "gradle",
		Name:     "Gradle",
		Version:  "8.8",
		Requires: []string{"jdk>=17"},
	}

	t.Run("satisfying JDK installed", func(t *testing.T) {
		i := installerWith(t, map[string]*manifest.Manifest{"gradle": m}, nil)
		i.State.SetInstalled("jdk-21", "21.0.11+10", "jdk", "", "")
		if err := i.checkRequires(m); err != nil {
			t.Errorf("checkRequires should return nil when jdk-21 is installed, got: %v", err)
		}
	})

	t.Run("only insufficient JDK installed", func(t *testing.T) {
		i := installerWith(t, map[string]*manifest.Manifest{"gradle": m}, nil)
		i.State.SetInstalled("jdk-11", "11.0.24+8", "jdk", "", "")
		if err := i.checkRequires(m); err == nil {
			t.Error("checkRequires should return an error when only jdk-11 is installed")
		}
	})
}

func TestInstallRequiresUnsatisfied(t *testing.T) {
	m := &manifest.Manifest{
		ID:       "maven",
		Name:     "Maven",
		Version:  "3.9",
		Sources:  []manifest.Source{{URL: "file:///tmp/never"}},
		Bin:      []manifest.Binary{{Name: "mvn", Path: "{app}/mvn"}},
		Requires: []string{"jdk"},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"maven": m}, nil)
	err := i.Install(context.Background(), "maven", false, nil)
	if err == nil || !strings.Contains(err.Error(), "requires") {
		t.Errorf("expected requires error, got %v", err)
	}
}

func TestInstallCommandConflict(t *testing.T) {
	srcA := filepath.Join(t.TempDir(), "a")
	os.WriteFile(srcA, []byte("a"), 0644)
	srcB := filepath.Join(t.TempDir(), "b")
	os.WriteFile(srcB, []byte("b"), 0644)

	mA := &manifest.Manifest{
		ID: "first", Name: "first", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcA, SHA256: sha256Of("a")}},
		Bin:     []manifest.Binary{{Name: "shared", Path: "{app}/shared"}},
	}
	mB := &manifest.Manifest{
		ID: "second", Name: "second", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcB, SHA256: sha256Of("b")}},
		Bin:     []manifest.Binary{{Name: "shared", Path: "{app}/shared"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"first": mA, "second": mB}, nil)
	if err := i.Install(context.Background(), "first", false, nil); err != nil {
		t.Fatal(err)
	}
	err := i.Install(context.Background(), "second", false, nil)
	if err == nil || !strings.Contains(err.Error(), "shared") {
		t.Errorf("expected conflict error, got %v", err)
	}
}

func TestInstallProvidesSiblingsAllowed(t *testing.T) {
	srcA := filepath.Join(t.TempDir(), "a")
	os.WriteFile(srcA, []byte("a"), 0644)
	srcB := filepath.Join(t.TempDir(), "b")
	os.WriteFile(srcB, []byte("b"), 0644)

	mA := &manifest.Manifest{
		ID: "node-22", Name: "Node 22", Version: "22", Provides: "node",
		Sources: []manifest.Source{{URL: "file://" + srcA, SHA256: sha256Of("a")}},
		Bin: []manifest.Binary{
			{Name: "node", Path: "{app}/node"},
			{Name: "old-only", Path: "{app}/old-only"},
		},
	}
	mB := &manifest.Manifest{
		ID: "node-24", Name: "Node 24", Version: "24", Provides: "node",
		Sources: []manifest.Source{{URL: "file://" + srcB, SHA256: sha256Of("b")}},
		Bin: []manifest.Binary{
			{Name: "node", Path: "{app}/node"},
			{Name: "new-only", Path: "{app}/new-only"},
		},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"node-22": mA, "node-24": mB}, nil)
	if err := i.Install(context.Background(), "node-22", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), "node-24", false, nil); err != nil {
		t.Errorf("provides siblings should not conflict: %v", err)
	}
	// Installing the sibling does not steal the active slot: node-22 (first)
	// stays active, its shims remain, and the inactive sibling adds no shims.
	if got := i.State.Providers["node"]; got != "node-22" {
		t.Errorf("active provider: got %s, want node-22", got)
	}
	if _, err := os.Lstat(i.Paths.Shim("old-only")); err != nil {
		t.Errorf("active provider's shim should remain, err=%v", err)
	}
	if _, err := os.Lstat(i.Paths.Shim("new-only")); !os.IsNotExist(err) {
		t.Errorf("inactive sibling's exclusive shim should not be installed, err=%v", err)
	}
	if owner, ok := i.State.CommandOwner("node"); !ok || owner != "node-22" {
		t.Errorf("node command owner = %q (%v), want node-22", owner, ok)
	}
	if !i.State.IsInstalled("node-24") {
		t.Error("sibling node-24 should still be installed")
	}
}

func TestInstallReturnsStateSaveError(t *testing.T) {
	srcFile := filepath.Join(t.TempDir(), "x")
	os.WriteFile(srcFile, []byte("x"), 0644)
	m := &manifest.Manifest{
		ID: "rg", Name: "ripgrep", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcFile, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "rg", Path: "{app}/rg"}},
		Desktop: []manifest.DesktopEntry{{ID: "bunny-rg.desktop", Name: "rg", Exec: "rg"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"rg": m}, nil)
	i.SaveState = func(st *state.State, path string) error {
		return errors.New("disk full")
	}

	err := i.Install(context.Background(), "rg", false, nil)
	if err == nil || !strings.Contains(err.Error(), "save state") {
		t.Fatalf("expected save state error, got %v", err)
	}
	if i.State.IsInstalled("rg") {
		t.Fatal("state should be restored after save failure")
	}
	if _, err := os.Stat(i.Paths.AppDir("rg")); !os.IsNotExist(err) {
		t.Fatalf("app dir should be rolled back after save failure, err=%v", err)
	}
	if _, err := os.Stat(i.Paths.Shim("rg")); !os.IsNotExist(err) {
		t.Fatalf("shim should be rolled back after save failure, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(i.Paths.Desktop(), "bunny-rg.desktop")); !os.IsNotExist(err) {
		t.Fatalf("desktop integration should be rolled back after save failure, err=%v", err)
	}
}

func TestForceInstallReplacesCommandSet(t *testing.T) {
	srcA := filepath.Join(t.TempDir(), "a")
	os.WriteFile(srcA, []byte("a"), 0644)
	srcB := filepath.Join(t.TempDir(), "b")
	os.WriteFile(srcB, []byte("b"), 0644)

	m1 := &manifest.Manifest{
		ID: "tool", Name: "tool", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcA, SHA256: sha256Of("a")}},
		Bin:     []manifest.Binary{{Name: "oldcmd", Path: "{app}/oldcmd"}},
		Desktop: []manifest.DesktopEntry{{ID: "bunny-old.desktop", Name: "old", Exec: "oldcmd"}},
	}
	m2 := &manifest.Manifest{
		ID: "tool", Name: "tool", Version: "2",
		Sources: []manifest.Source{{URL: "file://" + srcB, SHA256: sha256Of("b")}},
		Bin:     []manifest.Binary{{Name: "newcmd", Path: "{app}/newcmd"}},
	}
	manifests := map[string]*manifest.Manifest{"tool": m1}
	i := installerWith(t, manifests, nil)
	if err := i.Install(context.Background(), "tool", false, nil); err != nil {
		t.Fatal(err)
	}

	manifests["tool"] = m2
	if err := i.Install(context.Background(), "tool", true, nil); err != nil {
		t.Fatal(err)
	}

	if _, ok := i.State.CommandOwner("oldcmd"); ok {
		t.Fatal("old command should be removed from state")
	}
	if owner, ok := i.State.CommandOwner("newcmd"); !ok || owner != "tool" {
		t.Fatalf("new command owner: got %q %v", owner, ok)
	}
	if _, err := os.Stat(i.Paths.Shim("oldcmd")); !os.IsNotExist(err) {
		t.Fatalf("old shim should be removed, err=%v", err)
	}
	if _, err := os.Stat(i.Paths.Shim("newcmd")); err != nil {
		t.Fatalf("new shim should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(i.Paths.Desktop(), "bunny-old.desktop")); !os.IsNotExist(err) {
		t.Fatalf("stale desktop integration should be removed, err=%v", err)
	}
}

func TestForceInstallRestoresPreviousAppOnStateSaveError(t *testing.T) {
	srcA := filepath.Join(t.TempDir(), "a")
	os.WriteFile(srcA, []byte("a"), 0644)
	srcB := filepath.Join(t.TempDir(), "b")
	os.WriteFile(srcB, []byte("b"), 0644)

	m1 := &manifest.Manifest{
		ID: "tool", Name: "tool", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcA, SHA256: sha256Of("a")}},
		Bin:     []manifest.Binary{{Name: "oldcmd", Path: "{app}/oldcmd"}},
		Desktop: []manifest.DesktopEntry{{ID: "bunny-old.desktop", Name: "old", Exec: "oldcmd"}},
	}
	m2 := &manifest.Manifest{
		ID: "tool", Name: "tool", Version: "2",
		Sources: []manifest.Source{{URL: "file://" + srcB, SHA256: sha256Of("b")}},
		Bin:     []manifest.Binary{{Name: "newcmd", Path: "{app}/newcmd"}},
		Desktop: []manifest.DesktopEntry{{ID: "bunny-new.desktop", Name: "new", Exec: "newcmd"}},
	}
	manifests := map[string]*manifest.Manifest{"tool": m1}
	i := installerWith(t, manifests, nil)
	if err := i.Install(context.Background(), "tool", false, nil); err != nil {
		t.Fatal(err)
	}
	oldMarker := filepath.Join(i.Paths.AppDir("tool"), "old-only")
	if err := os.WriteFile(oldMarker, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	manifests["tool"] = m2
	i.SaveState = func(st *state.State, path string) error {
		return errors.New("disk full")
	}
	err := i.Install(context.Background(), "tool", true, nil)
	if err == nil || !strings.Contains(err.Error(), "save state") {
		t.Fatalf("expected save state error, got %v", err)
	}
	if _, err := os.Stat(oldMarker); err != nil {
		t.Fatalf("previous app dir should be restored: %v", err)
	}
	if owner, ok := i.State.CommandOwner("oldcmd"); !ok || owner != "tool" {
		t.Fatalf("old command should be restored, got %q %v", owner, ok)
	}
	if _, ok := i.State.CommandOwner("newcmd"); ok {
		t.Fatal("new command should not remain after rollback")
	}
	if _, err := os.Stat(i.Paths.Shim("oldcmd")); err != nil {
		t.Fatalf("old shim should be restored: %v", err)
	}
	if _, err := os.Stat(i.Paths.Shim("newcmd")); !os.IsNotExist(err) {
		t.Fatalf("new shim should be removed after rollback, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(i.Paths.Desktop(), "bunny-old.desktop")); err != nil {
		t.Fatalf("old desktop entry should be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(i.Paths.Desktop(), "bunny-new.desktop")); !os.IsNotExist(err) {
		t.Fatalf("new desktop entry should be removed: %v", err)
	}
	cached, err := os.ReadFile(i.Paths.ManifestFile("tool"))
	if err != nil {
		t.Fatal(err)
	}
	cachedManifest, err := manifest.ParseBytes(cached)
	if err != nil {
		t.Fatal(err)
	}
	if cachedManifest.Version != "1" {
		t.Fatalf("cached manifest version = %q, want 1", cachedManifest.Version)
	}
}

func TestUninstallRejectsBreakingLastRequiredProvider(t *testing.T) {
	src := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	jdk := &manifest.Manifest{
		ID: "jdk-21", Name: "JDK", Version: "21", Provides: "jdk",
		Sources: []manifest.Source{{URL: "file://" + src, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "java", Path: "{app}/java"}},
	}
	maven := &manifest.Manifest{
		ID: "maven", Name: "Maven", Version: "3", Requires: []string{"jdk"},
		Sources: []manifest.Source{{URL: "file://" + src, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "mvn", Path: "{app}/mvn"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"jdk-21": jdk, "maven": maven}, nil)
	if err := i.Install(context.Background(), "jdk-21", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), "maven", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := i.Uninstall("jdk-21", false); err == nil || !strings.Contains(err.Error(), "maven requires jdk") {
		t.Fatalf("expected reverse dependency error, got %v", err)
	}
}

func TestUninstallActivatesFallbackProvider(t *testing.T) {
	src := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	makeNode := func(id, version string) *manifest.Manifest {
		return &manifest.Manifest{
			ID: id, Name: id, Version: version, Provides: "node",
			Sources: []manifest.Source{{URL: "file://" + src, SHA256: sha256Of("x")}},
			Bin:     []manifest.Binary{{Name: "node", Path: "{app}/node"}},
		}
	}
	i := installerWith(t, map[string]*manifest.Manifest{
		"node-22": makeNode("node-22", "22"),
		"node-24": makeNode("node-24", "24"),
	}, nil)
	// node-24 first so it's the active provider; node-22 is the inactive sibling.
	if err := i.Install(context.Background(), "node-24", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), "node-22", false, nil); err != nil {
		t.Fatal(err)
	}
	// Uninstalling the active provider must promote the sibling and wire it up.
	if err := i.Uninstall("node-24", false); err != nil {
		t.Fatal(err)
	}
	if got := i.State.ResolveProvider("node"); got != "node-22" {
		t.Fatalf("provider = %q, want node-22", got)
	}
	if owner, ok := i.State.CommandOwner("node"); !ok || owner != "node-22" {
		t.Fatalf("node command owner = %q, %v", owner, ok)
	}
}

// Installing a second provider for a capability must not hijack the active one.
// A provider becomes active only when nothing else already provides it.
func TestInstallSecondProviderDoesNotStealActive(t *testing.T) {
	src := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	makeNode := func(id, version string) *manifest.Manifest {
		return &manifest.Manifest{
			ID: id, Name: id, Version: version, Provides: "node",
			Sources: []manifest.Source{{URL: "file://" + src, SHA256: sha256Of("x")}},
			Bin:     []manifest.Binary{{Name: "node", Path: "{app}/node"}},
		}
	}
	i := installerWith(t, map[string]*manifest.Manifest{
		"node-22": makeNode("node-22", "22"),
		"node-24": makeNode("node-24", "24"),
	}, nil)
	if err := i.Install(context.Background(), "node-22", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := i.State.ResolveProvider("node"); got != "node-22" {
		t.Fatalf("first provider should be active: got %q", got)
	}
	if err := i.Install(context.Background(), "node-24", false, nil); err != nil {
		t.Fatal(err)
	}
	if !i.State.IsInstalled("node-24") {
		t.Fatal("node-24 should be installed")
	}
	if got := i.State.ResolveProvider("node"); got != "node-22" {
		t.Errorf("active provider = %q, want node-22 (install must not hijack the active provider)", got)
	}
	if owner, ok := i.State.CommandOwner("node"); !ok || owner != "node-22" {
		t.Errorf("node command owner = %q (%v), want node-22", owner, ok)
	}
	if cmds := i.State.CommandsOwnedBy("node-24"); len(cmds) != 0 {
		t.Errorf("inactive provider node-24 should own no commands, got %v", cmds)
	}
}

// TestUninstallActivatesFallbackWhenManifestMissing pins the regression where
// the fallback provider is recorded but never wired: the uninstalled package's
// manifest is unavailable everywhere, yet the fallback must still get its
// commands and shim so the capability keeps working.
func TestUninstallActivatesFallbackWhenManifestMissing(t *testing.T) {
	src := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	makeNode := func(id, version string) *manifest.Manifest {
		return &manifest.Manifest{
			ID: id, Name: id, Version: version, Provides: "node",
			Sources: []manifest.Source{{URL: "file://" + src, SHA256: sha256Of("x")}},
			Bin:     []manifest.Binary{{Name: "node", Path: "{app}/node"}},
		}
	}
	manifests := map[string]*manifest.Manifest{
		"node-22": makeNode("node-22", "22"),
		"node-24": makeNode("node-24", "24"),
	}
	i := installerWith(t, manifests, nil)
	// node-24 first so it's the active provider; node-22 is the inactive sibling.
	if err := i.Install(context.Background(), "node-24", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := i.Install(context.Background(), "node-22", false, nil); err != nil {
		t.Fatal(err)
	}
	// node-24's manifest vanishes everywhere: dropped from the catalog and its
	// install-time cache removed.
	delete(manifests, "node-24")
	os.Remove(i.Paths.ManifestFile("node-24"))

	if err := i.Uninstall("node-24", false); err != nil {
		t.Fatal(err)
	}
	if got := i.State.ResolveProvider("node"); got != "node-22" {
		t.Fatalf("provider = %q, want node-22", got)
	}
	if owner, ok := i.State.CommandOwner("node"); !ok || owner != "node-22" {
		t.Fatalf("node command owner = %q, %v", owner, ok)
	}
	if _, err := os.Stat(i.Paths.Shim("node")); err != nil {
		t.Fatalf("node shim should point to fallback provider: %v", err)
	}
}

func TestUninstallCleansShimsAndState(t *testing.T) {
	srcFile := filepath.Join(t.TempDir(), "x")
	os.WriteFile(srcFile, []byte("x"), 0644)
	m := &manifest.Manifest{
		ID: "rg", Name: "ripgrep", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcFile, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "rg", Path: "{app}/rg"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"rg": m}, nil)
	if err := i.Install(context.Background(), "rg", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := i.Uninstall("rg", false); err != nil {
		t.Fatal(err)
	}
	if i.State.IsInstalled("rg") {
		t.Error("still in state")
	}
	if _, err := os.Stat(i.Paths.Shim("rg")); !os.IsNotExist(err) {
		t.Error("shim should be gone")
	}
	if _, err := os.Stat(i.Paths.AppDir("rg")); !os.IsNotExist(err) {
		t.Error("app dir should be gone")
	}
}

func TestUninstallReturnsStateSaveError(t *testing.T) {
	srcFile := filepath.Join(t.TempDir(), "x")
	os.WriteFile(srcFile, []byte("x"), 0644)
	m := &manifest.Manifest{
		ID: "rg", Name: "ripgrep", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcFile, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "rg", Path: "{app}/rg"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"rg": m}, nil)
	if err := i.Install(context.Background(), "rg", false, nil); err != nil {
		t.Fatal(err)
	}
	i.SaveState = func(st *state.State, path string) error {
		return errors.New("disk full")
	}

	err := i.Uninstall("rg", false)
	if err == nil || !strings.Contains(err.Error(), "save state") {
		t.Fatalf("expected save state error, got %v", err)
	}
	if !i.State.IsInstalled("rg") {
		t.Fatal("state should be restored after save failure")
	}
	if _, err := os.Stat(i.Paths.AppDir("rg")); err != nil {
		t.Fatalf("app dir should be restored after save failure: %v", err)
	}
	if _, err := os.Stat(i.Paths.Shim("rg")); err != nil {
		t.Fatalf("shim should be restored after save failure: %v", err)
	}
}

func TestUninstallRemovesStateOwnedShimsWhenManifestMissing(t *testing.T) {
	srcFile := filepath.Join(t.TempDir(), "x")
	os.WriteFile(srcFile, []byte("x"), 0644)
	m := &manifest.Manifest{
		ID: "rg", Name: "ripgrep", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcFile, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "rg", Path: "{app}/rg"}},
	}
	manifests := map[string]*manifest.Manifest{"rg": m}
	i := installerWith(t, manifests, nil)
	if err := i.Install(context.Background(), "rg", false, nil); err != nil {
		t.Fatal(err)
	}
	delete(manifests, "rg")
	// The cache file is what now stands in for the live catalog. Drop it too
	// so we exercise the "manifest gone everywhere" path.
	os.Remove(i.Paths.ManifestFile("rg"))

	if err := i.Uninstall("rg", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(i.Paths.Shim("rg")); !os.IsNotExist(err) {
		t.Fatalf("shim should be removed even without manifest, err=%v", err)
	}
}

// TestUninstallUsesCachedManifestForCleanup pins the regression: if the
// catalog manifest drifts after install (a desktop entry or icon removed
// upstream), uninstall must still clean up using what was *actually*
// installed. The install-time manifest cache is the authority.
func TestUninstallUsesCachedManifestForCleanup(t *testing.T) {
	srcFile := filepath.Join(t.TempDir(), "x")
	os.WriteFile(srcFile, []byte("x"), 0644)
	original := &manifest.Manifest{
		ID: "code", Name: "Visual Studio Code", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + srcFile, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "code", Path: "{app}/code"}},
		Desktop: []manifest.DesktopEntry{
			{ID: "bunny-code.desktop", Name: "Code", Exec: "code"},
		},
	}
	manifests := map[string]*manifest.Manifest{"code": original}
	i := installerWith(t, manifests, nil)
	if err := i.Install(context.Background(), "code", false, nil); err != nil {
		t.Fatal(err)
	}
	desktopFile := filepath.Join(i.Paths.Desktop(), "bunny-code.desktop")
	if _, err := os.Stat(desktopFile); err != nil {
		t.Fatalf("desktop file missing after install: %v", err)
	}

	// Catalog drifts: desktop entries removed upstream. Without the cache,
	// uninstall would now skip cleanup of the old .desktop file.
	manifests["code"] = &manifest.Manifest{
		ID: "code", Name: "Visual Studio Code", Version: "2",
		Sources: []manifest.Source{{URL: "file://" + srcFile, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "code", Path: "{app}/code"}},
	}

	if err := i.Uninstall("code", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(desktopFile); !os.IsNotExist(err) {
		t.Errorf("desktop file should be removed (cache should drive uninstall): %v", err)
	}
	if _, err := os.Stat(i.Paths.ManifestFile("code")); !os.IsNotExist(err) {
		t.Errorf("manifest cache should be removed after uninstall")
	}
}

type recordingHook struct {
	phases    []string
	downloads int
}

func (h *recordingHook) Phase(name string)          { h.phases = append(h.phases, name) }
func (h *recordingHook) Download(done, total int64) { h.downloads++ }

func TestInstallEmitsPhasesInOrder(t *testing.T) {
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "rg.tar.gz")
	os.WriteFile(srcFile, []byte("payload"), 0644)
	m := &manifest.Manifest{
		ID: "rg", Name: "ripgrep", Version: "14.1.0",
		Sources: []manifest.Source{{URL: "file://" + srcFile, File: "rg.tar.gz", SHA256: sha256Of("payload")}},
		Bin:     []manifest.Binary{{Name: "rg", Path: "{app}/rg"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"rg": m}, nil)
	h := &recordingHook{}
	if err := i.Install(context.Background(), "rg", false, h); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.phases, ","); got != "downloading,extracting,installing" {
		t.Fatalf("phases = %q, want downloading,extracting,installing", got)
	}
}

// Desktop integration runs before state records where a package went, so its
// {app} placeholder has to come from the directory just placed rather than a
// state lookup. Getting that wrong put an app's icon source under the cli root
// and silently skipped integration on every first install.
func TestFirstInstallResolvesAppPlaceholderForIntegration(t *testing.T) {
	src := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		ID: "code", Name: "Code", Version: "1",
		Sources: []manifest.Source{{URL: "file://" + src, SHA256: sha256Of("x")}},
		Bin:     []manifest.Binary{{Name: "code", Path: "{app}/code"}},
		// A desktop entry makes this kind app, so it installs somewhere other
		// than the cli root AppDir would fall back to.
		Desktop: []manifest.DesktopEntry{{ID: "code.desktop", Name: "Code", Exec: "{app}/code"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"code": m}, nil)
	if err := i.Install(context.Background(), "code", false, nil); err != nil {
		t.Fatal(err)
	}

	entry := filepath.Join(i.Paths.Desktop(), "code.desktop")
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("first install must write the desktop entry: %v", err)
	}
	wantExec := "Exec=" + filepath.Join(i.Paths.InstallRoot(manifest.KindApp), "code", "code")
	if !strings.Contains(string(data), wantExec) {
		t.Errorf("Exec should point at the app root:\nwant %s\ngot:\n%s", wantExec, data)
	}
}

// Changing an install root must not strand an installed tree. The package is
// updated where it lives, and its location is written down so every later
// command still finds it. Without this, `install --force` builds a second tree
// at the new root and abandons the first, still recorded and still on disk.
func TestReinstallKeepsExistingLocationWhenRootMoves(t *testing.T) {
	srcFile := filepath.Join(t.TempDir(), "n.tar.gz")
	os.WriteFile(srcFile, []byte("payload"), 0644)
	m := &manifest.Manifest{
		ID: "node-24", Name: "Node", Version: "24.0.0", Kind: manifest.KindSDK,
		Sources: []manifest.Source{{URL: "file://" + srcFile, File: "n.tar.gz", SHA256: sha256Of("payload")}},
		Bin:     []manifest.Binary{{Name: "node", Path: "{app}/node"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"node-24": m}, nil)
	if err := i.Install(context.Background(), "node-24", false, nil); err != nil {
		t.Fatal(err)
	}
	original := i.Paths.AppDir("node-24")
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("install dir missing: %v", err)
	}

	// The user repoints the sdk root, as `install.sdk: ~/opt` does.
	moved := filepath.Join(t.TempDir(), "opt")
	i.Paths = i.Paths.WithLayout(map[string]string{manifest.KindSDK: moved}, i.State.Location)

	if err := i.Install(context.Background(), "node-24", true, nil); err != nil {
		t.Fatal(err)
	}
	if got := i.Paths.AppDir("node-24"); got != original {
		t.Errorf("package moved to %q, want it left at %q", got, original)
	}
	if entries, err := os.ReadDir(moved); err == nil && len(entries) > 0 {
		t.Errorf("a second tree was built under the new root: %v", entries)
	}
	if info, ok := i.State.Packages["node-24"]; !ok || info.Path != original {
		t.Errorf("state.Path = %q, want the recorded location %q", info.Path, original)
	}
}

// The state record is proof of ownership, so it must name the real location.
// While Path was always cleared, state agreed that a package lived at whatever
// path the current config computed, which let --force replace an unrelated
// directory that happened to sit there.
func TestForceRefusesUnownedDirectoryAtAMovedRoot(t *testing.T) {
	srcFile := filepath.Join(t.TempDir(), "n.tar.gz")
	os.WriteFile(srcFile, []byte("payload"), 0644)
	m := &manifest.Manifest{
		ID: "node-24", Name: "Node", Version: "24.0.0", Kind: manifest.KindSDK,
		Sources: []manifest.Source{{URL: "file://" + srcFile, File: "n.tar.gz", SHA256: sha256Of("payload")}},
		Bin:     []manifest.Binary{{Name: "node", Path: "{app}/node"}},
	}
	i := installerWith(t, map[string]*manifest.Manifest{"node-24": m}, nil)
	if err := i.Install(context.Background(), "node-24", false, nil); err != nil {
		t.Fatal(err)
	}

	// A directory of the user's, at the path the new root would compute.
	moved := filepath.Join(t.TempDir(), "opt")
	theirs := filepath.Join(moved, "node-24")
	if err := os.MkdirAll(theirs, 0755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(theirs, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0644); err != nil {
		t.Fatal(err)
	}
	i.Paths = i.Paths.WithLayout(map[string]string{manifest.KindSDK: moved}, i.State.Location)

	if err := i.Install(context.Background(), "node-24", true, nil); err != nil {
		t.Fatalf("reinstall should update in place, not touch %s: %v", theirs, err)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "mine" {
		t.Fatalf("user's directory was destroyed: %q, %v", data, err)
	}
}

// Seeding is copy-if-absent: an upgrade fills in what a package needs and
// leaves anything already there alone, because {data} is the user's once
// written and a config edited between installs must survive.
func TestSeedDataFillsGapsWithoutOverwriting(t *testing.T) {
	seedDir, dataDir := t.TempDir(), t.TempDir()
	mustWrite := func(dir, rel, content string) string {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	mustWrite(seedDir, "conf/server.xml", "shipped")
	mustWrite(seedDir, "conf/web.xml", "shipped")
	edited := mustWrite(dataDir, "conf/server.xml", "the user's edit")

	if err := seedData(seedDir, dataDir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(edited); string(data) != "the user's edit" {
		t.Errorf("an edited config was overwritten: %q", data)
	}
	added := filepath.Join(dataDir, "conf", "web.xml")
	if data, err := os.ReadFile(added); err != nil || string(data) != "shipped" {
		t.Errorf("a missing file was not seeded: %q, %v", data, err)
	}
}

// Nothing to seed is the common case and must not be an error, nor create the
// data dir for a package that never asked for one.
func TestSeedDataIsANoopWhenNothingWasStaged(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data", "rg")
	if err := seedData(t.TempDir(), dataDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Errorf("empty seed created %s", dataDir)
	}
	if err := seedData(filepath.Join(t.TempDir(), "absent"), dataDir); err != nil {
		t.Errorf("a missing seed dir should be fine: %v", err)
	}
}

// What a prepare step writes through the {data} shadow has to reach the real
// data dir. Staging it and never merging it out looks like a working install
// until the package starts and finds no config.
func TestInstallSeedsDataFromTheShadow(t *testing.T) {
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "tomcat.tar.gz")
	os.WriteFile(srcFile, []byte("payload"), 0644)

	m := &manifest.Manifest{
		ID:      "tomcat",
		Name:    "Apache Tomcat",
		Version: "11.0.24",
		Sources: []manifest.Source{
			{URL: "file://" + srcFile, File: "tomcat.tar.gz", SHA256: sha256Of("payload")},
		},
		Prepare: []string{"seed the config"},
		Bin:     []manifest.Binary{{Name: "catalina", Path: "{app}/bin/catalina.sh"}},
	}

	i := installerWith(t, map[string]*manifest.Manifest{"tomcat": m}, nil)
	i.Prepare = func(_ context.Context, workDir, srcDir string, shadow map[string]string, commands []string, vars map[string]string) error {
		if err := noopPrepare(nil, workDir, srcDir, shadow, commands, vars); err != nil {
			return err
		}
		// Stand in for `cp -r conf {data}/conf`: the step names the real path,
		// which the sandbox has mounted from staging.
		staged, ok := shadow[vars["data"]]
		if !ok {
			return fmt.Errorf("{data} %q is not shadowed", vars["data"])
		}
		if err := os.MkdirAll(filepath.Join(staged, "conf"), 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(staged, "conf", "server.xml"), []byte("shipped"), 0644)
	}
	if err := i.Install(context.Background(), "tomcat", false, nil); err != nil {
		t.Fatal(err)
	}

	seeded := filepath.Join(i.Paths.AppData("tomcat"), "conf", "server.xml")
	if data, err := os.ReadFile(seeded); err != nil || string(data) != "shipped" {
		t.Fatalf("prepare's {data} writes never reached %s: %q, %v", seeded, data, err)
	}
}

// bwrap mounts a shadow over the real path, and can only create that
// mountpoint where the sandbox is already writable. Under $HOME the --tmpfs
// supplies that; under a BUNNY_HOME elsewhere the path sits inside the
// read-only bind, so the directory has to exist on the host beforehand or
// prepare fails before running a single step.
func TestRunPrepareCreatesShadowedDirectories(t *testing.T) {
	i := installerWith(t, nil, nil)
	var got map[string]string
	i.Prepare = func(_ context.Context, _, _ string, shadow map[string]string, _ []string, _ map[string]string) error {
		got = shadow
		return nil
	}

	dataDir := filepath.Join(t.TempDir(), "elsewhere", "data", "tomcat")
	shadow := map[string]string{dataDir: filepath.Join(t.TempDir(), "seed")}
	m := &manifest.Manifest{ID: "tomcat", Prepare: []string{"true"}}

	if err := i.runPrepare(context.Background(), m, "work", "src", shadow, nil); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
		t.Errorf("shadowed mountpoint %s was not created: %v", dataDir, err)
	}
	if len(got) != 1 {
		t.Errorf("shadow map not passed through: %v", got)
	}
}

// Seeding happens past the last rollback for a reason: an install that fails
// after prepare must leave {data} exactly as it was. Seeding earlier would
// leave a config directory behind for a package that is not installed, and on
// the retry that directory would suppress the seed it was supposed to receive.
func TestFailedInstallLeavesDataUntouched(t *testing.T) {
	srcFile := filepath.Join(t.TempDir(), "tomcat.tar.gz")
	os.WriteFile(srcFile, []byte("payload"), 0644)
	m := &manifest.Manifest{
		ID: "tomcat", Name: "Apache Tomcat", Version: "11.0.24",
		Sources: []manifest.Source{{URL: "file://" + srcFile, File: "tomcat.tar.gz", SHA256: sha256Of("payload")}},
		Prepare: []string{"seed the config"},
		Bin:     []manifest.Binary{{Name: "catalina", Path: "{app}/bin/catalina.sh"}},
	}

	i := installerWith(t, map[string]*manifest.Manifest{"tomcat": m}, nil)
	i.Prepare = func(_ context.Context, workDir, srcDir string, shadow map[string]string, commands []string, vars map[string]string) error {
		if err := noopPrepare(nil, workDir, srcDir, shadow, commands, vars); err != nil {
			return err
		}
		staged := shadow[vars["data"]]
		if err := os.MkdirAll(filepath.Join(staged, "conf"), 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(staged, "conf", "server.xml"), []byte("shipped"), 0644)
	}
	// Fail at the last step that still unwinds the install.
	i.SaveState = func(*state.State, string) error { return errors.New("disk full") }

	if err := i.Install(context.Background(), "tomcat", false, nil); err == nil {
		t.Fatal("expected the install to fail")
	}
	seeded := filepath.Join(i.Paths.AppData("tomcat"), "conf", "server.xml")
	if _, err := os.Stat(seeded); !os.IsNotExist(err) {
		t.Errorf("a failed install seeded %s: %v", seeded, err)
	}
}
