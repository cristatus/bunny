package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cristatus/bunny/internal/paths"
)

// DesktopSession reports whether bunny runs inside a graphical session, the
// only place the session env matters: the desktop finds bunny's entries and
// icons through XDG_DATA_DIRS.
func DesktopSession() bool {
	for _, name := range []string{"XDG_CURRENT_DESKTOP", "WAYLAND_DISPLAY", "DISPLAY"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// desktopSession is DesktopSession, a var so tests can drive both branches.
var desktopSession = DesktopSession

// sessionDataDirs returns XDG_DATA_DIRS as the systemd user manager exports
// it to the graphical session, and false when there is no manager to ask. The
// shell's own value proves nothing: `bunny init` sets it there whether or not
// the desktop ever saw it. A var so tests can drive every branch.
var sessionDataDirs = func() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "show-environment").Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if value, ok := strings.CutPrefix(line, "XDG_DATA_DIRS="); ok {
			return value, true
		}
	}
	return "", true
}

// SessionDataDirs is sessionDataDirs for callers outside doctor: setup asks
// it whether a systemd user manager is there to read environment.d.
func SessionDataDirs() (string, bool) { return sessionDataDirs() }

// BunnyInitRe matches a `bunny init` invocation in a shell rc or profile, in
// any spelling setup or a user might have written.
var BunnyInitRe = regexp.MustCompile(`bunny\s+init`)

// profileRunsInit reports whether ~/.profile runs `bunny init`, which is how
// setup reaches a desktop session that has no systemd user manager. A line
// commented out runs nothing, so only active lines count.
func profileRunsInit() bool {
	profile, err := paths.ProfileFile()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(data)) {
		if line = strings.TrimSpace(line); !strings.HasPrefix(line, "#") && BunnyInitRe.MatchString(line) {
			return true
		}
	}
	return false
}

// hasDesktopEntries reports whether dir holds any .desktop file. Other files
// there, like the mimeinfo.cache update-desktop-database leaves behind after
// the last application is uninstalled, give the desktop nothing to show.
func hasDesktopEntries(dir string) bool {
	entries, _ := os.ReadDir(dir)
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool {
		return strings.HasSuffix(e.Name(), ".desktop")
	})
}

// SessionCheck reports whether the desktop session finds bunny's applications,
// which it does through XDG_DATA_DIRS once `bunny setup` has run and the user
// has logged in again. It says nothing on a headless machine or before any
// package has installed a desktop entry, where there is nothing to find.
func SessionCheck(p *paths.Paths) []Result {
	const name = "desktop"
	if !desktopSession() {
		return nil
	}
	if !hasDesktopEntries(p.Desktop()) {
		return nil
	}
	dirs, known := sessionDataDirs()
	if !known {
		// Without a manager to ask, ~/.profile running `bunny init` is the
		// best evidence there is; whether the session read it shows only on
		// the desktop itself.
		if profileRunsInit() {
			return []Result{{Name: name, Detail: "~/.profile sets the session env; log out and back in once if applications are missing", Severity: OK}}
		}
		return []Result{{
			Name:     name,
			Detail:   "nothing puts " + tilde(p.Share()) + " on the session's XDG_DATA_DIRS, so the desktop does not show bunny's applications",
			Severity: Warn,
			Fix:      "bunny setup",
		}}
	}
	if slices.ContainsFunc(filepath.SplitList(dirs), func(dir string) bool {
		return dir != "" && filepath.Clean(dir) == p.Share()
	}) {
		return []Result{{Name: name, Detail: "session finds applications in " + tilde(p.Share()), Severity: OK}}
	}
	fix := "log out and back in once"
	if file, err := paths.SessionEnvFile(); err == nil {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			fix = "bunny setup"
		}
	}
	return []Result{{
		Name:     name,
		Detail:   "the session's XDG_DATA_DIRS lacks " + tilde(p.Share()) + ", so the desktop does not show bunny's applications",
		Severity: Warn,
		Fix:      fix,
	}}
}
