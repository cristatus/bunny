package paths

import (
	"path/filepath"
	"slices"
	"strings"
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
	if got, want := p.InstallRoot(manifest.KindSDK), "/x/y/sdk"; got != want {
		t.Errorf("sdk root = %q, want %q", got, want)
	}
}

func TestResolveDefaultsToDotBunny(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv(EnvHome, "")
	// XDG variables do not move anything: the root is the whole layout.
	t.Setenv("XDG_DATA_HOME", "/d")
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(tmp, ".bunny"); p.Root != want {
		t.Errorf("Root = %q, want %q", p.Root, want)
	}
	if !p.DefaultRoot() {
		t.Error("~/.bunny should report as the default root")
	}
	if At("/elsewhere").DefaultRoot() {
		t.Error("a custom root should not report as the default")
	}
}

func TestRuntimeEnvPreservesRootAfterHomeChanges(t *testing.T) {
	t.Setenv(EnvHome, "")
	t.Setenv("HOME", "/host/home")
	original, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	for _, assignment := range original.RuntimeEnv() {
		name, value, _ := strings.Cut(assignment, "=")
		t.Setenv(name, value)
	}
	t.Setenv("HOME", "/sandbox/home")

	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != original.Root {
		t.Errorf("Root = %q, want preserved %q", got.Root, original.Root)
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
		{p.Share(), "/x/share"},
		{p.InstallDir("vscode", manifest.KindApp), "/x/app/vscode"},
		{p.BunnyBinary(), "/x/bin/bunny"},
		{p.Shim("node"), "/x/bin/node"},
		{p.AppData("vscode"), "/x/data/vscode"},
		{p.Cache(), "/x/cache"},
		{p.AppDownloadCache("vscode"), "/x/cache/vscode"},
		{p.Staging(manifest.KindSDK), "/x/sdk/.staging"},
		{p.StagingBeside("/x/app/vscode"), "/x/app/.staging/vscode"},
		{p.StateFile(), "/x/state/state.json"},
		{p.MutationLock(), "/x/state/mutation.lock"},
		{p.ManifestFile("vscode"), "/x/state/manifests/vscode.yaml"},
		{p.UserConfigFile(), "/x/config/config.yaml"},
		{p.TransactionFile(), "/x/state/install-transaction.json"},
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
	// {app} resolves through the recorded kind, the same as AppDir.
	p := At("/x").WithKinds(func(string) string { return manifest.KindApp })
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

// A relative root would follow the working directory, so shims would resolve to
// a different install per directory. Erroring beats ignoring the variable, which
// would silently serve the XDG layout in place of the root that was asked for.
func TestResolveRejectsRelativeRoot(t *testing.T) {
	for _, v := range []string{"relative/path", "./b", "..", "b"} {
		t.Setenv(EnvHome, v)
		if _, err := Resolve(); err == nil {
			t.Errorf("%s=%q should be rejected", EnvHome, v)
		} else if !strings.Contains(err.Error(), EnvHome) {
			t.Errorf("error should name the variable, got %v", err)
		}
	}
}

// An absolute root is cleaned, so a trailing slash or a traversal does not reach
// state.json and the manifests as a second spelling of the same directory.
func TestResolveCleansAbsoluteRoot(t *testing.T) {
	for _, c := range []struct{ set, want string }{
		{"/opt/bunny", "/opt/bunny"},
		{"/opt/bunny/", "/opt/bunny"},
		{"/opt//bunny", "/opt/bunny"},
		{"/opt/x/../bunny", "/opt/bunny"},
	} {
		t.Setenv(EnvHome, c.set)
		p, err := Resolve()
		if err != nil {
			t.Fatal(err)
		}
		if p.Root != c.want {
			t.Errorf("%s=%q: Root = %q, want %q", EnvHome, c.set, p.Root, c.want)
		}
	}
}

// Staging must sit inside the root it will be renamed into. Installing
// finishes with rename(2), which cannot cross filesystems, so this has to hold
// for every kind.
func TestStagingIsSiblingOfInstallTarget(t *testing.T) {
	for _, p := range []*Paths{At("/x")} {
		for _, kind := range manifest.Kinds {
			root := p.InstallRoot(kind)
			if got := filepath.Dir(p.Staging(kind)); got != root {
				t.Errorf("%s: staging parent = %q, want %q", kind, got, root)
			}
			staged := p.StagingBeside(p.InstallDir("node-22", kind))
			if got := filepath.Dir(staged); got != p.Staging(kind) {
				t.Errorf("%s: per-app staging parent = %q, want %q", kind, got, p.Staging(kind))
			}
			if filepath.Dir(staged) == filepath.Dir(p.InstallDir("node-22", kind)) {
				t.Errorf("%s: staging must not collide with the install dirs it is renamed into", kind)
			}
		}
	}
}

// A sandbox grants LayoutRoots read-only, so it has to name every place a
// shim looks.
func TestLayoutRoots(t *testing.T) {
	p := At("/x")
	want := []string{"/x/app", "/x/bin", "/x/cli", "/x/sdk", "/x/state/manifests", "/x/state/state.json"}
	if got := p.LayoutRoots(); !slices.Equal(got, want) {
		t.Errorf("LayoutRoots() = %v, want %v", got, want)
	}
}

// An installed package is found under the root for the kind it was installed
// as, whatever kind the catalog says it is now.
func TestAppDirResolvesThroughState(t *testing.T) {
	recorded := map[string]string{"jdk-21": manifest.KindSDK, "ripgrep": manifest.KindCLI}
	p := At("/x").WithKinds(func(id string) string { return recorded[id] })

	for _, c := range []struct{ id, want string }{
		{"jdk-21", "/x/sdk/jdk-21"},
		{"ripgrep", "/x/cli/ripgrep"},
		{"unknown", "/x/cli/unknown"}, // nothing recorded: no guessing a root
	} {
		if got := p.AppDir(c.id); got != c.want {
			t.Errorf("AppDir(%s) = %q, want %q", c.id, got, c.want)
		}
	}
}

// The default root hangs off HOME. A relative HOME would resolve against the
// working directory, moving state and shims with each cd, the failure a
// relative BUNNY_HOME is already refused for.
func TestResolveRefusesARelativeHome(t *testing.T) {
	t.Setenv(EnvHome, "")
	t.Setenv("HOME", "foo")
	if _, err := Resolve(); err == nil || !strings.Contains(err.Error(), "HOME must be an absolute path") {
		t.Errorf("Resolve with HOME=foo: got %v, want a refusal", err)
	}
}
