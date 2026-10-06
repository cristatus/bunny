package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/paths"
)

func TestWriteEnvironmentDCustomRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := paths.At("/opt/bunny")
	path, wrote, err := writeEnvironmentD(p)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("expected the file to be written")
	}
	if filepath.Base(path) != "bunny.conf" || !strings.Contains(path, "environment.d") {
		t.Errorf("path = %q", path)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	// The graphical session resolves the root from this, so a session given
	// only PATH would launch shims that read ~/.bunny instead.
	if !strings.Contains(s, "BUNNY_HOME=/opt/bunny\n") {
		t.Errorf("missing root line: %s", s)
	}
	// An unset XDG_DATA_DIRS means the system dirs; dropping them would hide
	// every other application from the desktop.
	if !strings.Contains(s, "XDG_DATA_DIRS=/opt/bunny/share:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}\n") {
		t.Errorf("missing XDG line: %s", s)
	}
	if !strings.Contains(s, "PATH=/opt/bunny/bin:${PATH}") {
		t.Errorf("missing PATH line: %s", s)
	}
	if _, wrote, err := writeEnvironmentD(p); err != nil || wrote {
		t.Errorf("an up-to-date file is left alone: wrote=%v err=%v", wrote, err)
	}
}

// ~/.bunny is found without being named, so the session need not carry it.
func TestWriteEnvironmentDDefaultRootOmitsHome(t *testing.T) {
	p := defaultPaths(t)
	path, _, err := writeEnvironmentD(p)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), paths.EnvHome) {
		t.Errorf("default root should not set %s: %s", paths.EnvHome, data)
	}
	if !strings.Contains(string(data), "PATH="+p.Bin()) {
		t.Errorf("missing PATH line: %s", data)
	}
}

func defaultPaths(t *testing.T) *paths.Paths {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(paths.EnvHome, "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	p, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// stubDesktop decides whether setup sees a graphical session, with a systemd
// user manager behind it.
func stubDesktop(t *testing.T, desktop bool) {
	t.Helper()
	stubSession(t, desktop, true)
}

func stubSession(t *testing.T, desktop, systemd bool) {
	t.Helper()
	prevDesktop, prevSystemd := desktopSession, systemdSession
	desktopSession = func() bool { return desktop }
	systemdSession = func() bool { return systemd }
	t.Cleanup(func() { desktopSession, systemdSession = prevDesktop, prevSystemd })
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

	added, err := ensureRcInit(rc, "", "/h/.bunny/bin/bunny", "zsh")
	if err != nil || !added {
		t.Fatalf("first run should append: added=%v err=%v", added, err)
	}
	data, _ := os.ReadFile(rc)
	if !strings.Contains(string(data), `eval "$(/h/.bunny/bin/bunny init zsh)"`) {
		t.Errorf("missing eval line: %s", data)
	}
	// idempotent: second run detects bunny init, does not append again
	added2, _ := ensureRcInit(rc, "", "/h/.bunny/bin/bunny", "zsh")
	if added2 {
		t.Error("second run should not append")
	}
	if strings.Count(string(mustRead(t, rc)), "init zsh") != 1 {
		t.Error("init line duplicated")
	}
}

func TestBlankLinePrefix(t *testing.T) {
	cases := []struct {
		name, data, want string
	}{
		{"empty file", "", ""},
		{"no trailing newline", "# my zshrc", "\n\n"},
		{"one trailing newline", "# my zshrc\n", "\n"},
		{"already one blank line", "# my zshrc\n\n", ""},
		{"already multiple blank lines", "# my zshrc\n\n\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := blankLinePrefix(c.data); got != c.want {
				t.Errorf("blankLinePrefix(%q) = %q, want %q", c.data, got, c.want)
			}
		})
	}
}

// The rc runs in shells that never had $BUNNY_HOME, and `bunny init` reads the
// root from its own environment, so the rc line has to carry a custom root. An
// unpinned line would emit the ~/.bunny snippet while the install lives
// elsewhere.
func TestInitEvalLinePinsCustomRoot(t *testing.T) {
	for _, c := range []struct{ shell, want string }{
		{"bash", `eval "$(env BUNNY_HOME='/opt/bunny' /opt/bunny/bin/bunny init bash)"`},
		{"zsh", `eval "$(env BUNNY_HOME='/opt/bunny' /opt/bunny/bin/bunny init zsh)"`},
		{"fish", `env BUNNY_HOME='/opt/bunny' /opt/bunny/bin/bunny init fish | source`},
	} {
		got := initEvalLine("/opt/bunny", "/opt/bunny/bin/bunny", c.shell)
		if strings.TrimSpace(got) != c.want {
			t.Errorf("%s: got %q, want %q", c.shell, strings.TrimSpace(got), c.want)
		}
		// The line still has to look like a bunny init line to the dedup guard.
		if !rcHasBunnyInit(got) {
			t.Errorf("%s: pinned line not recognised as bunny init: %q", c.shell, got)
		}
	}
	// ~/.bunny needs no pin, and the bare line stays bare.
	if got := initEvalLine("", "/h/.bunny/bin/bunny", "bash"); strings.Contains(got, paths.EnvHome) {
		t.Errorf("the default root should not be pinned: %q", got)
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
	stubDesktop(t, true)
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

func TestWriteEnvironmentDRewritesStaleFile(t *testing.T) {
	p := defaultPaths(t)

	path, err := paths.SessionEnvFile()
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

// Headless machines have no desktop to find bunny's applications, so the
// session env is not written; the rc line alone sets the shell up.
func TestSetupCmdHeadlessSkipsSessionEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	stubDesktop(t, false)
	a := &App{Paths: paths.At(t.TempDir())}

	if err := (&SetupCmd{Shell: "bash"}).Run(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "environment.d", "bunny.conf")); !os.IsNotExist(err) {
		t.Errorf("headless setup must not write environment.d: %v", err)
	}
	if err := (&SetupCmd{Shell: "bash", Desktop: true}).Run(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "environment.d", "bunny.conf")); err != nil {
		t.Errorf("--desktop must write environment.d: %v", err)
	}
}

// A desktop without a systemd user manager never reads environment.d, so
// setup reaches it through ~/.profile instead, with the POSIX snippet.
func TestSetupCmdWithoutSystemdUsesTheProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	stubSession(t, true, false)
	a := &App{Paths: paths.At(t.TempDir())}

	for range 2 {
		if err := (&SetupCmd{Shell: "zsh"}).Run(a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "environment.d", "bunny.conf")); !os.IsNotExist(err) {
		t.Errorf("environment.d is pointless without systemd: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".profile"))
	if strings.Count(string(data), "init sh)") != 1 {
		t.Errorf("~/.profile should run the sh snippet exactly once:\n%s", data)
	}
}

// Setup leaves an existing bunny init line alone, but one written for another
// root (a default setup, then a reinstall under BUNNY_HOME) points the shell
// somewhere else, and "already configured" alone would hide that. A line
// that reaches bunny through PATH is equivalent, and a commented-out one runs
// nothing: neither is stale.
func TestStaleRcInitNamesALineForAnotherRoot(t *testing.T) {
	rc := filepath.Join(t.TempDir(), ".zshrc")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(rc, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	defaultLine := initEvalLine("", "/h/.bunny/bin/bunny", "zsh")

	write("# added by bunny setup\n" + defaultLine)
	if got := staleRcInit(rc, "", "zsh"); got != "" {
		t.Errorf("the line this root writes is not stale: %q", got)
	}
	if got := staleRcInit(rc, "/opt/bunny", "zsh"); !strings.Contains(got, "/h/.bunny/bin/bunny") {
		t.Errorf("an unpinned line under a custom root must be named, got %q", got)
	}

	write("# eval \"$(bunny init zsh)\"  # old\neval \"$(bunny init zsh)\"\n")
	if got := staleRcInit(rc, "", "zsh"); got != "" {
		t.Errorf("a PATH-based line is equivalent and a comment runs nothing, got %q", got)
	}

	// A comment that would pin the right root runs nothing either: the active
	// unpinned line below it is still the stale one.
	write("# " + initEvalLine("/opt/bunny", "/opt/bunny/bin/bunny", "zsh") + defaultLine)
	if got := staleRcInit(rc, "/opt/bunny", "zsh"); !strings.Contains(got, "/h/.bunny/bin/bunny") {
		t.Errorf("a comment must not stand in for the active line, got %q", got)
	}

	write(initEvalLine("/opt/old", "/opt/old/bin/bunny", "zsh"))
	if got := staleRcInit(rc, "/opt/bunny", "zsh"); !strings.Contains(got, "/opt/old") {
		t.Errorf("a line pinning another root must be named, got %q", got)
	}
	if got := staleRcInit(rc, "/opt/old", "zsh"); got != "" {
		t.Errorf("a line pinning this root is not stale, got %q", got)
	}
}
