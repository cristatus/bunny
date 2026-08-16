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
	if p.Home != "/x/y" {
		t.Errorf("Home = %q, want /x/y", p.Home)
	}
}

func TestResolveDefaultsToHomeDotBunny(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(EnvHome, "")
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if p.Home != filepath.Join(tmp, ".bunny") {
		t.Errorf("Home = %q, want %s/.bunny", p.Home, tmp)
	}
}

func TestPathsLayout(t *testing.T) {
	p := At("/x")
	cases := []struct{ got, want string }{
		{p.Home, "/x"},
		{p.Bin(), "/x/bin"},
		{p.App(), "/x/app"},
		{p.Catalog(), "/x/catalog"},
		{p.Share(), "/x/share"},
		{p.Var(), "/x/var"},
		{p.AppDir("vscode"), "/x/app/vscode"},
		{p.BunnyBinary(), "/x/bin/bunny"},
		{p.Shim("node"), "/x/bin/node"},
		{p.VarApp(), "/x/var/app"},
		{p.AppData("vscode"), "/x/var/app/vscode"},
		{p.Cache(), "/x/var/cache"},
		{p.AppDownloadCache("vscode"), "/x/var/cache/vscode"},
		{p.Staging(), "/x/app/.staging"},
		{p.AppStaging("vscode"), "/x/app/.staging/vscode"},
		{p.StateFile(), "/x/var/state.json"},
		{p.MutationLock(), "/x/var/mutation.lock"},
		{p.ManifestFile("vscode"), "/x/var/app/vscode/manifest.yaml"},
		{p.UserConfigFile(), "/x/config.yaml"},
		{p.Desktop(), "/x/share/applications"},
		{p.Icons(), "/x/share/icons"},
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
		"data":    "/x/var/app/vscode",
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
	if !filepath.IsAbs(p.Home) {
		t.Errorf("Home should be absolute, got %q", p.Home)
	}
	cwd, _ := os.Getwd()
	want := filepath.Join(cwd, "relative/path")
	if p.Home != want {
		t.Errorf("Home = %q, want %q", p.Home, want)
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
