package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

type stubCat struct{}

func (stubCat) List() ([]catalog.PackageInfo, error)    { return nil, nil }
func (stubCat) Load(string) (*manifest.Manifest, error) { return nil, nil }
func (stubCat) LoadFile(string, string) ([]byte, error) { return nil, nil }

func TestPrepareDirectExec(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)
	st := state.Empty()
	m := &manifest.Manifest{
		ID:      "foo",
		Version: "1.0",
		Bin: []manifest.Binary{{
			Name: "foo",
			Path: "{app}/foo",
			Args: []string{"--bunny", "{data}/state"},
		}},
		Env:  map[string]string{"FOO_HOME": "{data}/home"},
		Dirs: []string{"{data}/state", "{data}/home"},
	}

	prep, err := Prepare(p, stubCat{}, st, m, "", []string{"extra"})
	if err != nil {
		t.Fatal(err)
	}

	wantBin := filepath.Join(root, "app", "foo", "foo")
	if prep.BinPath != wantBin {
		t.Errorf("BinPath = %q, want %q", prep.BinPath, wantBin)
	}
	wantArg := "--bunny"
	wantArgVal := filepath.Join(root, "var", "app", "foo", "state")
	if len(prep.CmdArgs) != 3 || prep.CmdArgs[0] != wantArg || prep.CmdArgs[1] != wantArgVal || prep.CmdArgs[2] != "extra" {
		t.Errorf("CmdArgs = %v, want [%s %s extra]", prep.CmdArgs, wantArg, wantArgVal)
	}
	wantEnv := "FOO_HOME=" + filepath.Join(root, "var", "app", "foo", "home")
	if !envHas(prep.Env, wantEnv) {
		t.Errorf("env missing %q in %v", wantEnv, lastEntries(prep.Env, 3))
	}
	// Dirs should have been mkdir'd.
	for _, d := range []string{"state", "home"} {
		if _, err := os.Stat(filepath.Join(root, "var", "app", "foo", d)); err != nil {
			t.Errorf("dir %s not created: %v", d, err)
		}
	}
}

func TestPreparePackageEnvOverridesHostWithoutDuplicates(t *testing.T) {
	t.Setenv("FOO_HOME", "host-value")
	p := paths.At(t.TempDir())
	m := &manifest.Manifest{
		ID:      "foo",
		Version: "1.0",
		Bin:     []manifest.Binary{{Name: "foo", Path: "{app}/foo"}},
		Env:     map[string]string{"FOO_HOME": "{data}/home"},
	}
	prep, err := Prepare(p, stubCat{}, state.Empty(), m, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "FOO_HOME=" + filepath.Join(p.AppData("foo"), "home")
	count := 0
	for _, entry := range prep.Env {
		if len(entry) >= len("FOO_HOME=") && entry[:len("FOO_HOME=")] == "FOO_HOME=" {
			count++
			if entry != want {
				t.Errorf("FOO_HOME = %q, want %q", entry, want)
			}
		}
	}
	if count != 1 {
		t.Errorf("FOO_HOME entries = %d, want 1", count)
	}
}

func TestPrepareGlobal(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)
	st := state.Empty()
	m := &manifest.Manifest{
		ID:      "node-24",
		Version: "24.0.0",
		Env:     map[string]string{"NPM_CONFIG_PREFIX": "{data}/npm-global"},
	}
	exe := filepath.Join(root, "var", "app", "node-24", "npm-global", "bin", "tsc")
	prep, err := PrepareGlobal(p, stubCat{}, st, m, exe, []string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
	if prep.BinPath != exe {
		t.Errorf("BinPath = %q, want %q", prep.BinPath, exe)
	}
	if len(prep.CmdArgs) != 1 || prep.CmdArgs[0] != "--version" {
		t.Errorf("CmdArgs = %v", prep.CmdArgs)
	}
	wantEnv := "NPM_CONFIG_PREFIX=" + filepath.Join(root, "var", "app", "node-24", "npm-global")
	if !envHas(prep.Env, wantEnv) {
		t.Errorf("env missing %q", wantEnv)
	}
}

func envHas(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func lastEntries(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// reqCat returns a manifest carrying the given env for each provider id.
type reqCat struct{ envs map[string]map[string]string }

func (reqCat) List() ([]catalog.PackageInfo, error)    { return nil, nil }
func (reqCat) LoadFile(string, string) ([]byte, error) { return nil, nil }
func (c reqCat) Load(id string) (*manifest.Manifest, error) {
	return &manifest.Manifest{ID: id, Version: "0", Env: c.envs[id]}, nil
}

func TestMergeDepEnvVersionConstraint(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)
	st := state.Empty()
	st.SetInstalled("jdk-11", "11.0.0", "jdk")
	st.SetInstalled("jdk-21", "21.0.0", "jdk")
	_ = st.SetProvider("jdk", "jdk-11") // active too old for >=17
	cat := reqCat{envs: map[string]map[string]string{
		"jdk-21": {"JAVA_HOME": "{app}"},
		"jdk-11": {"JAVA_HOME": "{app}"},
	}}

	env, err := mergeDepEnv(nil, []string{"jdk>=17"}, p, cat, st)
	if err != nil {
		t.Fatal(err)
	}
	want := "JAVA_HOME=" + filepath.Join(root, "app", "jdk-21")
	if !envHas(env, want) {
		t.Errorf("want %q in %v", want, env)
	}
	if envHas(env, "JAVA_HOME="+filepath.Join(root, "app", "jdk-11")) {
		t.Error("must not use the too-old active jdk-11")
	}
}

// An unsatisfiable version requirement degrades (launch without that dep's
// env) rather than refusing to run the program.
func TestMergeDepEnvUnsatisfiableDegrades(t *testing.T) {
	p := paths.At(t.TempDir())
	st := state.Empty()
	st.SetInstalled("jdk-11", "11.0.0", "jdk")
	_ = st.SetProvider("jdk", "jdk-11")
	env, err := mergeDepEnv(nil, []string{"jdk>=17"}, p, reqCat{}, st)
	if err != nil {
		t.Fatalf("unsatisfiable requirement should degrade, not error: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("no dep env should be applied, got %v", env)
	}
}

func TestMergeDepEnvMissingBareRequirementDegrades(t *testing.T) {
	env, err := mergeDepEnv(nil, []string{"jdk"}, paths.At(t.TempDir()), reqCat{}, state.Empty())
	if err != nil {
		t.Fatalf("missing bare requirement should degrade, not error: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("no dep env should be applied, got %v", env)
	}
}
