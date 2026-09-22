package desktop

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
)

func TestInstallDesktopEntry(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)

	tr := true
	entries := []manifest.DesktopEntry{
		{
			ID:             "bunny-vscode.desktop",
			Name:           "VS Code",
			GenericName:    "Editor",
			Comment:        "Edit code",
			Exec:           "{bin}/code %F",
			Icon:           "code",
			Categories:     []string{"Development", "TextEditor"},
			MimeTypes:      []string{"text/plain"},
			StartupNotify:  &tr,
			StartupWMClass: "Code",
			Actions: []manifest.Action{
				{ID: "new-window", Name: "New Window", Exec: "{bin}/code --new-window"},
			},
		},
	}
	vars := map[string]string{"bin": "/x/bin"}

	if err := InstallEntries(p, entries, vars, "code"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(p.Desktop(), "bunny-vscode.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	required := []string{
		"[Desktop Entry]",
		"Type=Application",
		"Name=VS Code",
		"Exec=/x/bin/code %F",
		"Icon=code",
		"Categories=Development;TextEditor;",
		"MimeType=text/plain;",
		"StartupNotify=true",
		"StartupWMClass=Code",
		"Actions=new-window;",
		"[Desktop Action new-window]",
		"Exec=/x/bin/code --new-window",
	}
	for _, r := range required {
		if !strings.Contains(out, r) {
			t.Errorf("missing line %q in:\n%s", r, out)
		}
	}
}

func TestRemoveEntries(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)
	if err := os.MkdirAll(p.Desktop(), 0755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(p.Desktop(), "x.desktop")
	os.WriteFile(dst, []byte("[Desktop Entry]\n"+managedKey+"=code\n"), 0644)

	if err := RemoveEntries(p, []manifest.DesktopEntry{{ID: "x.desktop"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("expected entry to be removed")
	}
}

// Entries live in the shared ~/.local/share/applications, so a name collision
// with a distro package or a hand-written launcher must not cost the user
// their file, in either direction.
func TestEntriesNotOwnedByBunnyAreLeftAlone(t *testing.T) {
	p := paths.At(t.TempDir())
	if err := os.MkdirAll(p.Desktop(), 0755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(p.Desktop(), "code.desktop")
	foreign := "[Desktop Entry]\nName=Someone else's launcher\n"
	if err := os.WriteFile(dst, []byte(foreign), 0644); err != nil {
		t.Fatal(err)
	}

	entries := []manifest.DesktopEntry{{ID: "code.desktop", Name: "Code", Exec: "/bin/true"}}
	if err := InstallEntries(p, entries, nil, "code"); err == nil {
		t.Error("installing over a foreign entry should be refused")
	}
	if err := RemoveEntries(p, entries); err != nil {
		t.Fatalf("removal should skip, not fail: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != foreign {
		t.Errorf("foreign entry was modified or removed: %q, %v", data, err)
	}
}

func TestInstallIcon(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "code.png")
	os.WriteFile(src, []byte("fake-png"), 0644)

	if _, err := InstallIcons(p, []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(p.Icons(), "hicolor", "256x256", "apps", "code.png")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("icon missing at %s: %v", dst, err)
	}
}

func TestRefreshIconCache(t *testing.T) {
	orig := iconCacheUpdater
	t.Cleanup(func() { iconCacheUpdater = orig })

	t.Run("runs the updater on the hicolor dir when it exists", func(t *testing.T) {
		root := t.TempDir()
		p := paths.At(root)
		src := filepath.Join(t.TempDir(), "code.png")
		os.WriteFile(src, []byte("x"), 0644)
		if _, err := InstallIcons(p, []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}, nil, nil); err != nil {
			t.Fatal(err)
		}
		var got string
		iconCacheUpdater = func(dir string) error { got = dir; return nil }
		RefreshIconCache(p)
		want := filepath.Join(p.Icons(), "hicolor")
		if got != want {
			t.Errorf("updater called with %q, want %q", got, want)
		}
	})

	t.Run("no-op when no icons installed", func(t *testing.T) {
		p := paths.At(t.TempDir())
		called := false
		iconCacheUpdater = func(string) error { called = true; return nil }
		RefreshIconCache(p)
		if called {
			t.Error("updater must not run when the hicolor dir is absent")
		}
	})
}

func TestInstallCompletions(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)
	srcDir := t.TempDir()

	bash := filepath.Join(srcDir, "code.bash")
	zsh := filepath.Join(srcDir, "_code")
	os.WriteFile(bash, []byte("# bash"), 0644)
	os.WriteFile(zsh, []byte("# zsh"), 0644)

	comps := &manifest.Completions{
		Bash: bash,
		Zsh:  zsh,
	}
	if _, err := InstallCompletions(p, comps, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.BashCompletions(), "code.bash")); err != nil {
		t.Errorf("bash missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.ZshCompletions(), "_code")); err != nil {
		t.Errorf("zsh missing: %v", err)
	}
}

// ~/.local/share/icons is shared with the distro and every other application,
// so an icon already sitting at the target path belongs to somebody. Bunny
// installs around it rather than over it.
func TestInstallIconsLeavesAForeignIconAlone(t *testing.T) {
	p := paths.At(t.TempDir())
	src := filepath.Join(t.TempDir(), "code.png")
	os.WriteFile(src, []byte("bunny-png"), 0644)

	dst := filepath.Join(p.Icons(), "hicolor", "256x256", "apps", "code.png")
	os.MkdirAll(filepath.Dir(dst), 0755)
	os.WriteFile(dst, []byte("the distro's icon"), 0644)

	icons := []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}
	if _, err := InstallIcons(p, icons, nil, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "the distro's icon" {
		t.Errorf("overwrote a foreign icon: %q", data)
	}

	// The previous install's manifest is what proves the file is bunny's, so
	// with that in hand the same write goes through.
	prev := &manifest.Manifest{Icons: icons}
	if _, err := InstallIcons(p, icons, nil, ManagedFiles(p, prev, nil)); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "bunny-png" {
		t.Errorf("declined to replace its own icon: %q", data)
	}
}

func TestInstallCompletionsLeavesAForeignFileAlone(t *testing.T) {
	p := paths.At(t.TempDir())
	src := filepath.Join(t.TempDir(), "code.bash")
	os.WriteFile(src, []byte("bunny's completion"), 0644)

	dst := filepath.Join(p.BashCompletions(), "code.bash")
	os.MkdirAll(filepath.Dir(dst), 0755)
	os.WriteFile(dst, []byte("hand-written"), 0644)

	if _, err := InstallCompletions(p, &manifest.Completions{Bash: src}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "hand-written" {
		t.Errorf("overwrote a foreign completion: %q", data)
	}
}

func TestInstallMan(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)
	srcDir := t.TempDir()

	page1 := filepath.Join(srcDir, "bunnytool.1")
	pageGz := filepath.Join(srcDir, "bunnytool.3p.gz")
	os.WriteFile(page1, []byte(".TH BUNNYTOOL 1"), 0644)
	os.WriteFile(pageGz, []byte("not really gzipped, doesn't matter here"), 0644)

	man := []string{page1, pageGz}
	if _, err := InstallMan(p, man, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.ManPages(), "man1", "bunnytool.1")); err != nil {
		t.Errorf("section 1 page missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.ManPages(), "man3p", "bunnytool.3p.gz")); err != nil {
		t.Errorf("section 3p page missing: %v", err)
	}
}

func TestInstallManLeavesAForeignFileAlone(t *testing.T) {
	p := paths.At(t.TempDir())
	src := filepath.Join(t.TempDir(), "ls.1")
	os.WriteFile(src, []byte("bunny's page"), 0644)

	dst := filepath.Join(p.ManPages(), "man1", "ls.1")
	os.MkdirAll(filepath.Dir(dst), 0755)
	os.WriteFile(dst, []byte("the distro's page"), 0644)

	if _, err := InstallMan(p, []string{src}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "the distro's page" {
		t.Errorf("overwrote a foreign man page: %q", data)
	}
}

func TestRemoveMan(t *testing.T) {
	p := paths.At(t.TempDir())
	src := filepath.Join(t.TempDir(), "bunnytool.1")
	os.WriteFile(src, []byte(".TH BUNNYTOOL 1"), 0644)

	written, err := InstallMan(p, []string{src}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(p.ManPages(), "man1", "bunnytool.1")
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("setup: page missing: %v", err)
	}
	if _, err := RemoveFiles(written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("page still present after removal: %v", err)
	}
}

// A directory entry (for a tool like gh that ships one page per subcommand)
// symlinks every recognizable page inside it, mixed sections included, and
// skips anything that isn't one.
func TestInstallManDirectory(t *testing.T) {
	p := paths.At(t.TempDir())
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "bunnytool.1"), []byte(".TH BUNNYTOOL 1"), 0644)
	os.WriteFile(filepath.Join(srcDir, "bunnytool-config.5"), []byte(".TH BUNNYTOOL-CONFIG 5"), 0644)
	os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("not a man page"), 0644)

	written, err := InstallMan(p, []string{srcDir}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	page1 := filepath.Join(p.ManPages(), "man1", "bunnytool.1")
	page5 := filepath.Join(p.ManPages(), "man5", "bunnytool-config.5")
	if _, err := os.Lstat(page1); err != nil {
		t.Errorf("section 1 page missing: %v", err)
	}
	if _, err := os.Lstat(page5); err != nil {
		t.Errorf("section 5 page missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(p.ManPages(), "man1", "README.md")); !os.IsNotExist(err) {
		t.Error("README.md should not have been installed as a man page")
	}
	if target, err := os.Readlink(page1); err != nil || target != filepath.Join(srcDir, "bunnytool.1") {
		t.Errorf("expected a symlink to the source page, got target %q, err %v", target, err)
	}

	if _, err := RemoveFiles(written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(page1); !os.IsNotExist(err) {
		t.Error("section 1 page still present after removal")
	}
	if _, err := os.Lstat(page5); !os.IsNotExist(err) {
		t.Error("section 5 page still present after removal")
	}
}

// The reinstall path removes the *old* manifest's integration after {app}
// already holds the *new* version's files (see installer.replaceDesktopIntegration).
// For a directory entry that means re-reading srcDir would see the new files,
// not the old ones — so this simulates exactly that ordering and checks that
// both the recorded files and ManPaths, which stands in for an install that
// predates the record, still name only the pages the old install created.
func TestManDirectoryReinstallSurvivesAppSwap(t *testing.T) {
	p := paths.At(t.TempDir())
	appDir := t.TempDir()
	manDir := filepath.Join(appDir, "share", "man", "man1")
	os.MkdirAll(manDir, 0755)
	os.WriteFile(filepath.Join(manDir, "old-cmd.1"), []byte(".TH OLD-CMD 1"), 0644)
	os.WriteFile(filepath.Join(manDir, "shared-cmd.1"), []byte(".TH SHARED-CMD 1 (old)"), 0644)

	entry := []string{manDir}
	written, err := InstallMan(p, entry, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate installer.place(): the staged new version's tree replaces
	// {app} in full before the old manifest's integration is torn down.
	os.RemoveAll(appDir)
	os.MkdirAll(manDir, 0755)
	os.WriteFile(filepath.Join(manDir, "new-cmd.1"), []byte(".TH NEW-CMD 1"), 0644)
	os.WriteFile(filepath.Join(manDir, "shared-cmd.1"), []byte(".TH SHARED-CMD 1 (new)"), 0644)

	// Same entry string (same directory path) as the old manifest declared —
	// the derivation must still find the *old* symlinks, not the now-live new
	// files.
	if got := ManPaths(p, entry, nil); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(written))) {
		t.Errorf("ManPaths after the swap = %v, want the old install's %v", got, written)
	}
	if _, err := RemoveFiles(written); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(p.ManPages(), "man1", "old-cmd.1")); !os.IsNotExist(err) {
		t.Error("old-cmd.1 should have been removed")
	}
	if _, err := os.Lstat(filepath.Join(p.ManPages(), "man1", "shared-cmd.1")); !os.IsNotExist(err) {
		t.Error("shared-cmd.1 should have been removed along with the rest of the old install")
	}

	// The new manifest's install then runs against the now-live new tree.
	if _, err := InstallMan(p, entry, nil, nil); err != nil {
		t.Fatal(err)
	}
	newCmd := filepath.Join(p.ManPages(), "man1", "new-cmd.1")
	shared := filepath.Join(p.ManPages(), "man1", "shared-cmd.1")
	if target, err := os.Readlink(newCmd); err != nil || target != filepath.Join(manDir, "new-cmd.1") {
		t.Errorf("new-cmd.1: got target %q, err %v", target, err)
	}
	if target, err := os.Readlink(shared); err != nil || target != filepath.Join(manDir, "shared-cmd.1") {
		t.Errorf("shared-cmd.1: expected it repointed at the new version's page, got target %q, err %v", target, err)
	}
}

// Removal used to sweep .png/.svg/.xpm for the icon's name, taking out
// variants bunny never installed.
func TestRemoveIconsOnlyTouchesTheDeclaredExtension(t *testing.T) {
	p := paths.At(t.TempDir())
	src := filepath.Join(t.TempDir(), "code.png")
	os.WriteFile(src, []byte("bunny-png"), 0644)

	icons := []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}
	written, err := InstallIcons(p, icons, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Somebody else's scalable variant, same name, same theme directory.
	svg := filepath.Join(p.Icons(), "hicolor", "256x256", "apps", "code.svg")
	os.WriteFile(svg, []byte("<svg/>"), 0644)

	if _, err := RemoveFiles(written); err != nil {
		t.Fatal(err)
	}
	png := filepath.Join(p.Icons(), "hicolor", "256x256", "apps", "code.png")
	if _, err := os.Stat(png); !os.IsNotExist(err) {
		t.Errorf("bunny's own icon should be gone: %v", err)
	}
	if data, err := os.ReadFile(svg); err != nil || string(data) != "<svg/>" {
		t.Errorf("removed an icon bunny never installed: %q, %v", data, err)
	}
}
