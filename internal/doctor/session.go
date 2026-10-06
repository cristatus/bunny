package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cristatus/bunny/internal/paths"
)

// desktopSession reports whether doctor runs inside a graphical session. A
// var so tests can drive both branches.
var desktopSession = func() bool {
	for _, name := range []string{"XDG_CURRENT_DESKTOP", "WAYLAND_DISPLAY", "DISPLAY"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

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
		return []Result{{Name: name, Detail: "no systemd user manager to ask whether the session finds " + tilde(p.Share()), Severity: OK}}
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
