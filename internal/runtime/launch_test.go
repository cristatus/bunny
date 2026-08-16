package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/config"
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

	l := &Launcher{Paths: p, Catalog: stubCat{}, State: st}
	prep, err := l.Prepare(m, "", []string{"extra"})
	if err != nil {
		t.Fatal(err)
	}

	wantBin := filepath.Join(root, "cli", "foo", "foo")
	if prep.BinPath != wantBin {
		t.Errorf("BinPath = %q, want %q", prep.BinPath, wantBin)
	}
	wantArg := "--bunny"
	wantArgVal := filepath.Join(root, "data", "foo", "state")
	if len(prep.CmdArgs) != 3 || prep.CmdArgs[0] != wantArg || prep.CmdArgs[1] != wantArgVal || prep.CmdArgs[2] != "extra" {
		t.Errorf("CmdArgs = %v, want [%s %s extra]", prep.CmdArgs, wantArg, wantArgVal)
	}
	wantEnv := "FOO_HOME=" + filepath.Join(root, "data", "foo", "home")
	if !envHas(prep.Env, wantEnv) {
		t.Errorf("env missing %q in %v", wantEnv, lastEntries(prep.Env, 3))
	}
	// Dirs should have been mkdir'd.
	for _, d := range []string{"state", "home"} {
		if _, err := os.Stat(filepath.Join(root, "data", "foo", d)); err != nil {
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
	l := &Launcher{Paths: p, Catalog: stubCat{}, State: state.Empty()}
	prep, err := l.Prepare(m, "", nil)
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
	exe := filepath.Join(root, "data", "node-24", "npm-global", "bin", "tsc")
	l := &Launcher{Paths: p, Catalog: stubCat{}, State: st}
	prep, err := l.PrepareGlobal(m, exe, []string{"--version"})
	if err != nil {
		t.Fatal(err)
	}
	if prep.BinPath != exe {
		t.Errorf("BinPath = %q, want %q", prep.BinPath, exe)
	}
	if len(prep.CmdArgs) != 1 || prep.CmdArgs[0] != "--version" {
		t.Errorf("CmdArgs = %v", prep.CmdArgs)
	}
	wantEnv := "NPM_CONFIG_PREFIX=" + filepath.Join(root, "data", "node-24", "npm-global")
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
	st.SetInstalled("jdk-11", "11.0.0", "jdk", "", "")
	st.SetInstalled("jdk-21", "21.0.0", "jdk", "", "")
	_ = st.SetProvider("jdk", "jdk-11") // active too old for >=17
	cat := reqCat{envs: map[string]map[string]string{
		"jdk-21": {"JAVA_HOME": "{app}"},
		"jdk-11": {"JAVA_HOME": "{app}"},
	}}

	l := &Launcher{Paths: p, Catalog: cat, State: st}
	env, err := l.mergeDepEnv(nil, []string{"jdk>=17"})
	if err != nil {
		t.Fatal(err)
	}
	want := "JAVA_HOME=" + filepath.Join(root, "cli", "jdk-21")
	if !envHas(env, want) {
		t.Errorf("want %q in %v", want, env)
	}
	if envHas(env, "JAVA_HOME="+filepath.Join(root, "cli", "jdk-11")) {
		t.Error("must not use the too-old active jdk-11")
	}
}

// An unsatisfiable version requirement degrades (launch without that dep's
// env) rather than refusing to run the program.
func TestMergeDepEnvUnsatisfiableDegrades(t *testing.T) {
	p := paths.At(t.TempDir())
	st := state.Empty()
	st.SetInstalled("jdk-11", "11.0.0", "jdk", "", "")
	_ = st.SetProvider("jdk", "jdk-11")
	l := &Launcher{Paths: p, Catalog: reqCat{}, State: st}
	env, err := l.mergeDepEnv(nil, []string{"jdk>=17"})
	if err != nil {
		t.Fatalf("unsatisfiable requirement should degrade, not error: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("no dep env should be applied, got %v", env)
	}
}

func TestMergeDepEnvMissingBareRequirementDegrades(t *testing.T) {
	l := &Launcher{Paths: paths.At(t.TempDir()), Catalog: reqCat{}, State: state.Empty()}
	env, err := l.mergeDepEnv(nil, []string{"jdk"})
	if err != nil {
		t.Fatalf("missing bare requirement should degrade, not error: %v", err)
	}
	if len(env) != 0 {
		t.Errorf("no dep env should be applied, got %v", env)
	}
}

// User config layers on top of the manifest: the manifest keeps the wiring a
// package cannot run without, config supplies anything extra (isolation
// included) and wins on conflict.
func TestPrepareConfigEnvOverridesManifest(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)
	m := &manifest.Manifest{
		ID:       "gradle",
		Version:  "8.7",
		Provides: "gradle",
		Bin:      []manifest.Binary{{Name: "gradle", Path: "{app}/bin/gradle"}},
		Env:      map[string]string{"GRADLE_HOME": "{app}"},
	}
	cfg := &config.Config{
		Env:  map[string]map[string]string{"gradle": {"GRADLE_USER_HOME": "{data}/gradle"}},
		Dirs: map[string][]string{"gradle": {"{data}/gradle"}},
	}
	l := &Launcher{Paths: p, Catalog: stubCat{}, State: state.Empty(), Config: cfg}
	prep, err := l.Prepare(m, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	gradleHome := filepath.Join(root, "data", "gradle", "gradle")
	if !envHas(prep.Env, "GRADLE_USER_HOME="+gradleHome) {
		t.Errorf("config env not applied: %v", lastEntries(prep.Env, 3))
	}
	if !envHas(prep.Env, "GRADLE_HOME="+filepath.Join(root, "cli", "gradle")) {
		t.Error("manifest env must survive alongside config env")
	}
	if _, err := os.Stat(gradleHome); err != nil {
		t.Errorf("config dirs not created: %v", err)
	}
}

// Nothing is isolated unless the user asks for it: a manifest that only carries
// wiring leaves the tool's global dirs at their native host locations.
func TestPrepareNoConfigIsolatesNothing(t *testing.T) {
	p := paths.At(t.TempDir())
	m := &manifest.Manifest{
		ID:      "gradle",
		Version: "8.7",
		Bin:     []manifest.Binary{{Name: "gradle", Path: "{app}/bin/gradle"}},
		Env:     map[string]string{"GRADLE_HOME": "{app}"},
	}
	l := &Launcher{Paths: p, Catalog: stubCat{}, State: state.Empty()}
	prep, err := l.Prepare(m, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range prep.Env {
		if strings.HasPrefix(entry, "GRADLE_USER_HOME=") {
			t.Errorf("bunny set %q with no config asking for it", entry)
		}
	}
}

// Config keyed by capability reaches a package through the requires: chain, so
// every tool that requires jdk sees the same override.
func TestMergeDepEnvAppliesConfig(t *testing.T) {
	root := t.TempDir()
	st := state.Empty()
	st.SetInstalled("jdk-21", "21.0.0", "jdk", "", "")
	_ = st.SetProvider("jdk", "jdk-21")
	cat := reqCat{envs: map[string]map[string]string{"jdk-21": {"JAVA_HOME": "{app}"}}}
	cfg := &config.Config{Env: map[string]map[string]string{
		"jdk": {"JAVA_TOOL_OPTIONS": "-Dfile.encoding=UTF-8"},
	}}

	l := &Launcher{Paths: paths.At(root), Catalog: cat, State: st, Config: cfg}
	env, err := l.mergeDepEnv(nil, []string{"jdk"})
	if err != nil {
		t.Fatal(err)
	}
	if !envHas(env, "JAVA_TOOL_OPTIONS=-Dfile.encoding=UTF-8") {
		t.Errorf("dependency config env not applied: %v", env)
	}
	if !envHas(env, "JAVA_HOME="+filepath.Join(root, "cli", "jdk-21")) {
		t.Errorf("dependency manifest env lost: %v", env)
	}
}
