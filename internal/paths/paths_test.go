package paths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
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
	if got, want := p.InstallRoot(manifest.KindSDK), "/x/y/sdk"; got != want {
		t.Errorf("sdk root = %q, want %q", got, want)
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
		{p.InstallRoot(manifest.KindSDK), filepath.Join(tmp, ".local/share/bunny/sdk")},
		{p.InstallRoot(manifest.KindApp), filepath.Join(tmp, ".local/share/bunny/app")},
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
		{p.InstallRoot(manifest.KindSDK), "/d/bunny/sdk"},
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
	if got, want := p.InstallRoot(manifest.KindApp), filepath.Join(tmp, ".local/share/bunny/app"); got != want {
		t.Errorf("app root = %q, want %q", got, want)
	}
}

func TestSingleRootLayout(t *testing.T) {
	p := At("/x")
	cases := []struct{ got, want string }{
		{p.Root, "/x"},
		{p.Bin(), "/x/bin"},
		{p.InstallRoot(manifest.KindApp), "/x/app"},
		{p.InstallRoot(manifest.KindCLI), "/x/cli"},
		{p.InstallRoot(manifest.KindSDK), "/x/sdk"},
		{p.Catalog(), "/x/catalog"},
		{p.Share(), "/x/share"},
		{p.Data(), "/x"},
		{p.InstallDir("vscode", manifest.KindApp), "/x/app/vscode"},
		{p.BunnyBinary(), "/x/bin/bunny"},
		{p.Shim("node"), "/x/bin/node"},
		{p.AppData("vscode"), "/x/data/vscode"},
		{p.Cache(), "/x/cache"},
		{p.AppDownloadCache("vscode"), "/x/cache/vscode"},
		{p.Staging(manifest.KindSDK), "/x/sdk/.staging"},
		{p.AppStaging("vscode", manifest.KindApp), "/x/app/.staging/vscode"},
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
	// {app} resolves through the recorded install location, the same as AppDir.
	p := At("/x").WithLayout(nil, func(id string) (string, string) { return "", "/x/app/" + id })
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

// Staging must sit inside the root it will be renamed into. Installing
// finishes with rename(2), which cannot cross filesystems, so this has to hold
// for every kind and for user-configured roots that may be on separate disks.
func TestStagingIsSiblingOfInstallTarget(t *testing.T) {
	for _, p := range []*Paths{
		At("/x"),
		At("/x").WithLayout(map[string]string{manifest.KindSDK: "/mnt/big/sdks"}, nil),
	} {
		for _, kind := range manifest.Kinds {
			root := p.InstallRoot(kind)
			if got := filepath.Dir(p.Staging(kind)); got != root {
				t.Errorf("%s: staging parent = %q, want %q", kind, got, root)
			}
			staged := p.AppStaging("node-22", kind)
			if got := filepath.Dir(staged); got != p.Staging(kind) {
				t.Errorf("%s: per-app staging parent = %q, want %q", kind, got, p.Staging(kind))
			}
			if filepath.Dir(staged) == filepath.Dir(p.InstallDir("node-22", kind)) {
				t.Errorf("%s: staging must not collide with the install dirs it is renamed into", kind)
			}
		}
	}
}

// A configured root replaces the default for that kind only.
func TestInstallRootOverrides(t *testing.T) {
	p := At("/x").WithLayout(map[string]string{manifest.KindSDK: "/opt"}, nil)
	if got, want := p.InstallDir("jdk-21", manifest.KindSDK), "/opt/jdk-21"; got != want {
		t.Errorf("sdk install dir = %q, want %q", got, want)
	}
	if got, want := p.InstallDir("ripgrep", manifest.KindCLI), "/x/cli/ripgrep"; got != want {
		t.Errorf("cli install dir = %q, want %q", got, want)
	}
	roots := p.InstallRoots()
	if len(roots) != 3 {
		t.Errorf("InstallRoots() = %v, want three distinct roots", roots)
	}
}

// Collapsing every kind onto one root must not produce duplicate sweeps.
func TestInstallRootsDeduplicates(t *testing.T) {
	p := At("/x").WithLayout(map[string]string{
		manifest.KindApp: "/opt", manifest.KindCLI: "/opt", manifest.KindSDK: "/opt",
	}, nil)
	if got := p.InstallRoots(); len(got) != 1 || got[0] != "/opt" {
		t.Errorf("InstallRoots() = %v, want [/opt]", got)
	}
	if got := p.StagingRoots(); len(got) != 1 || got[0] != "/opt/.staging" {
		t.Errorf("StagingRoots() = %v, want [/opt/.staging]", got)
	}
}

// An installed package is found where state says it is: a recorded path wins
// outright, otherwise the location is derived from the kind it was installed
// as. Both survive the configured roots changing underneath them.
func TestAppDirResolvesThroughState(t *testing.T) {
	recorded := map[string]struct{ kind, path string }{
		"jdk-21":  {manifest.KindSDK, "/somewhere/else/jdk-21"}, // installed somewhere custom
		"node-22": {manifest.KindSDK, ""},                       // default root for its kind
		"ripgrep": {manifest.KindCLI, ""},
	}
	p := At("/x").WithLayout(
		map[string]string{manifest.KindSDK: "/opt"},
		func(id string) (string, string) { r := recorded[id]; return r.kind, r.path },
	)

	for _, c := range []struct{ id, want string }{
		{"jdk-21", "/somewhere/else/jdk-21"}, // recorded path wins over the root
		{"node-22", "/opt/node-22"},          // derived from kind + configured root
		{"ripgrep", "/x/cli/ripgrep"},        // derived from kind + default root
		{"unknown", "/x/cli/unknown"},        // nothing recorded: no guessing a root
	} {
		if got := p.AppDir(c.id); got != c.want {
			t.Errorf("AppDir(%s) = %q, want %q", c.id, got, c.want)
		}
	}
	if got := p.Vars("jdk-21", "21")["app"]; got != "/somewhere/else/jdk-21" {
		t.Errorf("{app} = %q, want the recorded path", got)
	}
}
