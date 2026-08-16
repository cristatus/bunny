package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveHonorsBUNNYHOME(t *testing.T) {
	t.Setenv(EnvHome, "/x/y")
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != "/x/y" {
		t.Errorf("Root = %q, want /x/y", p.Root)
	}
	if p.XDG() {
		t.Error("BUNNY_HOME should collapse the layout under one root")
	}
	if got, want := p.App(), "/x/y/app"; got != want {
		t.Errorf("App() = %q, want %q", got, want)
	}
}

func TestResolveDefaultsToXDG(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(EnvHome, "")
	for _, v := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, "")
	}
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !p.XDG() {
		t.Error("expected the XDG layout when BUNNY_HOME is unset")
	}
	cases := []struct{ got, want string }{
		{p.App(), filepath.Join(tmp, ".local/share/bunny/app")},
		{p.AppData("node-22"), filepath.Join(tmp, ".local/share/bunny/data/node-22")},
		{p.Catalog(), filepath.Join(tmp, ".local/share/bunny/catalog")},
		{p.StateFile(), filepath.Join(tmp, ".local/share/bunny/state.json")},
		{p.Cache(), filepath.Join(tmp, ".cache/bunny")},
		{p.UserConfigFile(), filepath.Join(tmp, ".config/bunny/config.yaml")},
		{p.Bin(), filepath.Join(tmp, ".local/bin")},
		// Desktop entries and icons go to the real XDG data home, which the
		// desktop already scans. That is the point of the layout.
		{p.Desktop(), filepath.Join(tmp, ".local/share/applications")},
		{p.Icons(), filepath.Join(tmp, ".local/share/icons")},
		{p.BashCompletions(), filepath.Join(tmp, ".local/share/bash-completion/completions")},
		{p.FishCompletions(), filepath.Join(tmp, ".config/fish/completions")},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestResolveHonorsXDGOverrides(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(EnvHome, "")
	t.Setenv("XDG_DATA_HOME", "/d")
	t.Setenv("XDG_CONFIG_HOME", "/c")
	t.Setenv("XDG_CACHE_HOME", "/k")
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ got, want string }{
		{p.App(), "/d/bunny/app"},
		{p.UserConfigFile(), "/c/bunny/config.yaml"},
		{p.Cache(), "/k/bunny"},
		{p.Desktop(), "/d/applications"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// The spec says a relative XDG value must be ignored in favour of the default.
func TestResolveIgnoresRelativeXDGValues(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(EnvHome, "")
	t.Setenv("XDG_DATA_HOME", "relative/share")
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.App(), filepath.Join(tmp, ".local/share/bunny/app"); got != want {
		t.Errorf("App() = %q, want %q", got, want)
	}
}

func TestSingleRootLayout(t *testing.T) {
	p := At("/x")
	cases := []struct{ got, want string }{
		{p.Root, "/x"},
		{p.Bin(), "/x/bin"},
		{p.App(), "/x/app"},
		{p.Catalog(), "/x/catalog"},
		{p.Share(), "/x/share"},
		{p.Data(), "/x"},
		{p.AppDir("vscode"), "/x/app/vscode"},
		{p.BunnyBinary(), "/x/bin/bunny"},
		{p.Shim("node"), "/x/bin/node"},
		{p.AppData("vscode"), "/x/data/vscode"},
		{p.Cache(), "/x/cache"},
		{p.AppDownloadCache("vscode"), "/x/cache/vscode"},
		{p.Staging(), "/x/app/.staging"},
		{p.AppStaging("vscode"), "/x/app/.staging/vscode"},
		{p.StateFile(), "/x/state.json"},
		{p.MutationLock(), "/x/mutation.lock"},
		{p.ManifestFile("vscode"), "/x/data/vscode/manifest.yaml"},
		{p.UserConfigFile(), "/x/config.yaml"},
		{p.Desktop(), "/x/share/applications"},
		{p.Icons(), "/x/share/icons"},
		{p.FishCompletions(), "/x/share/fish/vendor_completions.d"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestVars(t *testing.T) {
	p := At("/x")
	t.Setenv("HOME", "/h/u")
	v := p.Vars("vscode", "1.2.3")
	checks := map[string]string{
		"id":      "vscode",
		"version": "1.2.3",
		"app":     "/x/app/vscode",
		"data":    "/x/data/vscode",
		"bin":     "/x/bin",
		"share":   "/x/share",
		"home":    "/h/u",
	}
	for k, want := range checks {
		if v[k] != want {
			t.Errorf("Vars[%q] = %q, want %q", k, v[k], want)
		}
	}
}

func TestResolveAbsPath(t *testing.T) {
	t.Setenv(EnvHome, "relative/path")
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(p.Root) {
		t.Errorf("Root should be absolute, got %q", p.Root)
	}
	cwd, _ := os.Getwd()
	if want := filepath.Join(cwd, "relative/path"); p.Root != want {
		t.Errorf("Root = %q, want %q", p.Root, want)
	}
}

// Staging must sit inside the app root. Installing finishes by renaming the
// staged tree into place, and rename(2) cannot cross filesystems: only a
// sibling directory guarantees the two ends are on the same one.
func TestStagingIsSiblingOfInstallTarget(t *testing.T) {
	p := At("/x")
	if got, want := filepath.Dir(p.Staging()), p.App(); got != want {
		t.Errorf("staging parent = %q, want %q (same root as install targets)", got, want)
	}
	if got, want := filepath.Dir(p.AppStaging("node-22")), p.Staging(); got != want {
		t.Errorf("per-app staging parent = %q, want %q", got, want)
	}
	if filepath.Dir(p.AppStaging("node-22")) == filepath.Dir(p.AppDir("node-22")) {
		t.Error("staging must not collide with the install dirs it is renamed into")
	}
}
