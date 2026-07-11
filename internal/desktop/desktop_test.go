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

	if err := InstallEntries(p, entries, vars); err != nil {
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
	os.WriteFile(dst, []byte{}, 0644)

	if err := RemoveEntries(p, []manifest.DesktopEntry{{ID: "x.desktop"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("expected entry to be removed")
	}
}

func TestInstallIcon(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "code.png")
	os.WriteFile(src, []byte("fake-png"), 0644)

	if err := InstallIcons(p, []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}, nil); err != nil {
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
		if err := InstallIcons(p, []manifest.Icon{{Src: src, Name: "code", Size: "256x256"}}, nil); err != nil {
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
	if err := InstallCompletions(p, comps, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.BashCompletions(), "code.bash")); err != nil {
		t.Errorf("bash missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.ZshCompletions(), "_code")); err != nil {
		t.Errorf("zsh missing: %v", err)
	}
}
