package desktop

import (
	"os"
	"path/filepath"
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

	if err := InstallIcons(p, []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}, nil, nil); err != nil {
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
		if err := InstallIcons(p, []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}, nil, nil); err != nil {
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
	if err := InstallCompletions(p, comps, nil, nil); err != nil {
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
	if err := InstallIcons(p, icons, nil, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "the distro's icon" {
		t.Errorf("overwrote a foreign icon: %q", data)
	}

	// The previous install's manifest is what proves the file is bunny's, so
	// with that in hand the same write goes through.
	prev := &manifest.Manifest{Icons: icons}
	if err := InstallIcons(p, icons, nil, ManagedFiles(p, prev, nil)); err != nil {
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

	if err := InstallCompletions(p, &manifest.Completions{Bash: src}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dst); string(data) != "hand-written" {
		t.Errorf("overwrote a foreign completion: %q", data)
	}
}

// Removal used to sweep .png/.svg/.xpm for the icon's name, taking out
// variants bunny never installed.
func TestRemoveIconsOnlyTouchesTheDeclaredExtension(t *testing.T) {
	p := paths.At(t.TempDir())
	src := filepath.Join(t.TempDir(), "code.png")
	os.WriteFile(src, []byte("bunny-png"), 0644)

	icons := []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}
	if err := InstallIcons(p, icons, nil, nil); err != nil {
		t.Fatal(err)
	}
	// Somebody else's scalable variant, same name, same theme directory.
	svg := filepath.Join(p.Icons(), "hicolor", "256x256", "apps", "code.svg")
	os.WriteFile(svg, []byte("<svg/>"), 0644)

	if err := RemoveIcons(p, icons, nil); err != nil {
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
