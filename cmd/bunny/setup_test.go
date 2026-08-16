package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/paths"
)

func TestWriteEnvironmentDSingleRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := paths.At("/h/.bunny")
	path, wrote, err := writeEnvironmentD(p)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("a single-root install always needs the session env")
	}
	if filepath.Base(path) != "bunny.conf" || !strings.Contains(path, "environment.d") {
		t.Errorf("path = %q", path)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if !strings.Contains(s, "XDG_DATA_DIRS=/h/.bunny/share:${XDG_DATA_DIRS}") {
		t.Errorf("missing XDG line: %s", s)
	}
	if !strings.Contains(s, "PATH=/h/.bunny/bin:${PATH}") {
		t.Errorf("missing PATH line: %s", s)
	}
	// idempotent: second write returns same path, no error
	if _, _, err := writeEnvironmentD(p); err != nil {
		t.Fatal(err)
	}
}

// The session only needs XDG_DATA_DIRS when bunny's desktop entries live
// somewhere the desktop would not otherwise look.
func TestWriteEnvironmentDXDGOmitsDataDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(paths.EnvHome, "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", "")
	p, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	path, wrote, err := writeEnvironmentD(p)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("expected the file to be written")
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "XDG_DATA_DIRS") {
		t.Errorf("XDG layout should not set XDG_DATA_DIRS: %s", data)
	}
	if !strings.Contains(string(data), "PATH="+p.Bin()) {
		t.Errorf("missing PATH line: %s", data)
	}
}

func TestRcHasBunnyInit(t *testing.T) {
	if !rcHasBunnyInit(`eval "$(/h/.bunny/bin/bunny init zsh)"`) {
		t.Error("should detect eval form")
	}
	if !rcHasBunnyInit("# run bunny   init bash for setup") {
		t.Error("should detect spaced form")
	}
	if rcHasBunnyInit("export PATH=/h/.bunny/bin:$PATH") {
		t.Error("should not match unrelated bunny path line")
	}
}

func TestRcPathForShell(t *testing.T) {
	home, _ := os.UserHomeDir()
	if p, _ := rcPathForShell("zsh"); p != filepath.Join(home, ".zshrc") {
		t.Errorf("zsh rc = %q", p)
	}
	if p, _ := rcPathForShell("bash"); p != filepath.Join(home, ".bashrc") {
		t.Errorf("bash rc = %q", p)
	}
	if _, err := rcPathForShell("tcsh"); err == nil {
		t.Error("unknown shell should error")
	}
}

func TestEnsureRcInit(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".zshrc")
	os.WriteFile(rc, []byte("# my zshrc\n"), 0644)

	added, err := ensureRcInit(rc, "/h/.bunny/bin/bunny", "zsh")
	if err != nil || !added {
		t.Fatalf("first run should append: added=%v err=%v", added, err)
	}
	data, _ := os.ReadFile(rc)
	if !strings.Contains(string(data), `eval "$(/h/.bunny/bin/bunny init zsh)"`) {
		t.Errorf("missing eval line: %s", data)
	}
	// idempotent: second run detects bunny init, does not append again
	added2, _ := ensureRcInit(rc, "/h/.bunny/bin/bunny", "zsh")
	if added2 {
		t.Error("second run should not append")
	}
	if strings.Count(string(mustRead(t, rc)), "init zsh") != 1 {
		t.Error("init line duplicated")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSetupCmdRun(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	a := &App{Paths: paths.At(root)}

	cmd := &SetupCmd{Shell: "zsh"}
	if err := cmd.Run(a); err != nil {
		t.Fatal(err)
	}
	// environment.d written
	if _, err := os.Stat(filepath.Join(home, ".config", "environment.d", "bunny.conf")); err != nil {
		t.Errorf("environment.d not written: %v", err)
	}
	// completion file written
	if _, err := os.Stat(filepath.Join(root, "share", "zsh", "site-functions", "_bunny")); err != nil {
		t.Errorf("completion file not written: %v", err)
	}
	// rc got the eval line once
	data, _ := os.ReadFile(filepath.Join(home, ".zshrc"))
	if strings.Count(string(data), "bunny init zsh") != 1 {
		t.Errorf("rc eval line count wrong: %s", data)
	}
}

func TestSetupCmdRejectsInvalidShell(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	a := &App{Paths: paths.At(root)}

	cmd := &SetupCmd{Shell: "foo"}
	err := cmd.Run(a)
	if err == nil {
		t.Fatal("expected error for invalid shell, got nil")
	}

	// no environment.d file should be written
	if _, err := os.Stat(filepath.Join(home, ".config", "environment.d", "bunny.conf")); err == nil {
		t.Error("environment.d should not be written for invalid shell")
	}

	// no bash completion file should be written (the fallback)
	if _, err := os.Stat(filepath.Join(root, "share", "bash-completion", "completions", "bunny")); err == nil {
		t.Error("bash completion file should not be written for invalid shell")
	}

	// no shell rc should be modified
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); err == nil {
		t.Error(".bashrc should not be created for invalid shell")
	}
}

// stubSession makes the session PATH deterministic, so both branches can be
// tested without depending on the machine running them.
func stubSession(t *testing.T, value string, known bool) {
	t.Helper()
	prev := sessionPath
	sessionPath = func() (string, bool) { return value, known }
	t.Cleanup(func() { sessionPath = prev })
}

func xdgPaths(t *testing.T) *paths.Paths {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(paths.EnvHome, "")
	for _, v := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, "")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	p, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// systemd's generator does not deduplicate, so writing when the session
// already exports the shim dir would only add a second identical PATH entry.
func TestWriteEnvironmentDSkipsWhenSessionAlreadyProvides(t *testing.T) {
	p := xdgPaths(t)
	stubSession(t, "/usr/bin:"+p.Bin()+":/bin", true)

	path, wrote, err := writeEnvironmentD(p)
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("expected the write to be skipped")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("no file should have been created")
	}
}

// "Cannot tell" must not mean "skip": a missing PATH entry breaks toolchains
// inside GUI-launched IDEs, which is far worse than a duplicate one.
func TestWriteEnvironmentDWritesWhenSessionUnknown(t *testing.T) {
	p := xdgPaths(t)
	stubSession(t, "", false)

	_, wrote, err := writeEnvironmentD(p)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Error("an undeterminable session must still get the file")
	}
}

// The check would otherwise read bunny's own earlier contribution to the
// session and decline to correct a line that has since gone stale.
func TestWriteEnvironmentDRewritesExistingFile(t *testing.T) {
	p := xdgPaths(t)
	stubSession(t, p.Bin(), true) // session already provides it

	path, err := environmentDPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	stale := "# managed by bunny — do not edit\nPATH=/old/bunny/bin:${PATH}\n"
	if err := os.WriteFile(path, []byte(stale), 0644); err != nil {
		t.Fatal(err)
	}

	if _, wrote, err := writeEnvironmentD(p); err != nil || !wrote {
		t.Fatalf("existing file must be kept correct: wrote=%v err=%v", wrote, err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "/old/bunny/bin") {
		t.Errorf("stale entry survived: %s", data)
	}
	if !strings.Contains(string(data), p.Bin()) {
		t.Errorf("current shim dir missing: %s", data)
	}
}
