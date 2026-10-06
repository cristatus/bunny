package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/paths"
)

func stubSession(t *testing.T, desktop bool, dirs string, known bool) {
	t.Helper()
	prevDesktop, prevDirs := desktopSession, sessionDataDirs
	desktopSession = func() bool { return desktop }
	sessionDataDirs = func() (string, bool) { return dirs, known }
	t.Cleanup(func() { desktopSession, sessionDataDirs = prevDesktop, prevDirs })
}

// withEntry returns a root with one installed desktop entry, so the check has
// something the desktop should find.
func withEntry(t *testing.T) *paths.Paths {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := paths.At(t.TempDir())
	if err := os.MkdirAll(p.Desktop(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Desktop(), "code.desktop"), []byte("[Desktop Entry]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSessionCheckIsSilentWithNothingToShow(t *testing.T) {
	stubSession(t, false, "", true)
	if got := SessionCheck(withEntry(t)); got != nil {
		t.Errorf("headless: got %+v, want no row", got)
	}
	stubSession(t, true, "", true)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	p := paths.At(t.TempDir())
	if got := SessionCheck(p); got != nil {
		t.Errorf("no desktop entries: got %+v, want no row", got)
	}
	// update-desktop-database leaves its cache after the last app goes.
	if err := os.MkdirAll(p.Desktop(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Desktop(), "mimeinfo.cache"), []byte("[MIME Cache]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := SessionCheck(p); got != nil {
		t.Errorf("only a MIME cache left: got %+v, want no row", got)
	}
}

func TestSessionCheckFindsTheShareDir(t *testing.T) {
	p := withEntry(t)
	stubSession(t, true, "/usr/share:"+p.Share()+"/", true)
	if got := SessionCheck(p); len(got) != 1 || got[0].Severity != OK {
		t.Errorf("got %+v, want OK", got)
	}
}

// Without a systemd user manager, ~/.profile running `bunny init` is how
// setup reaches the session, so it is what doctor looks for.
func TestSessionCheckWithoutSystemdLooksAtTheProfile(t *testing.T) {
	p := withEntry(t)
	stubSession(t, true, "", false)
	if got := SessionCheck(p); len(got) != 1 || got[0].Severity != Warn || got[0].Fix != "bunny setup" {
		t.Fatalf("no profile line: got %+v, want a warning to run setup", got)
	}
	profile := filepath.Join(os.Getenv("HOME"), ".profile")
	// A commented-out line runs nothing, so the session never gets the env.
	if err := os.WriteFile(profile, []byte(`# eval "$(/h/.bunny/bin/bunny init sh)"`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := SessionCheck(p); len(got) != 1 || got[0].Severity != Warn {
		t.Fatalf("commented-out profile line: got %+v, want a warning", got)
	}
	if err := os.WriteFile(profile, []byte(`eval "$(/h/.bunny/bin/bunny init sh)"`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := SessionCheck(p); len(got) != 1 || got[0].Severity != OK {
		t.Errorf("profile line present: got %+v, want OK", got)
	}
}

// A session missing the share dir needs setup when the session env was
// never written, and only a fresh login when it was.
func TestSessionCheckNamesTheFix(t *testing.T) {
	p := withEntry(t)
	stubSession(t, true, "/usr/local/share:/usr/share", true)
	got := SessionCheck(p)
	if len(got) != 1 || got[0].Severity != Warn || got[0].Fix != "bunny setup" {
		t.Fatalf("without a session env file: got %+v, want a warning to run setup", got)
	}
	file, err := paths.SessionEnvFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("XDG_DATA_DIRS="+p.Share()+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got = SessionCheck(p)
	if len(got) != 1 || got[0].Severity != Warn || got[0].Fix != "log out and back in once" {
		t.Fatalf("with a session env file: got %+v, want a warning to log in again", got)
	}
}
