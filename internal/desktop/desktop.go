// Package desktop handles XDG desktop integration: .desktop files, icons,
// and shell completions. Shims (symlinks to the bunny binary) live in the
// shim package because they're a different concern.
package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/runtime"
)

// InstallEntries writes .desktop files for every entry in the manifest.
func InstallEntries(p *paths.Paths, entries []manifest.DesktopEntry, vars map[string]string, pkgID string) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(p.Desktop(), 0755); err != nil {
		return err
	}
	for _, d := range entries {
		dst := filepath.Join(p.Desktop(), d.ID)
		if err := checkEntryOwned(dst); err != nil {
			return err
		}
		content := buildDesktopEntry(&d, vars, pkgID)
		if err := fsutil.WriteFile(dst, []byte(content), 0644); err != nil {
			return fmt.Errorf("write desktop entry %s: %w", d.ID, err)
		}
		log.Debug("Created desktop entry", "id", d.ID)
	}
	return nil
}

// RemoveEntries deletes .desktop files referenced by the manifest, skipping
// any that bunny did not write.
func RemoveEntries(p *paths.Paths, entries []manifest.DesktopEntry) error {
	var errs []error
	for _, d := range entries {
		path := filepath.Join(p.Desktop(), d.ID)
		if err := checkEntryOwned(path); err != nil {
			log.Warn("Leaving desktop entry alone", "id", d.ID, "reason", err)
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove desktop entry %s: %w", d.ID, err))
		}
	}
	return errors.Join(errs...)
}

// managedKey marks a .desktop file as bunny's. X- keys are the spec's reserved
// space for exactly this, and desktops ignore unknown ones.
const managedKey = "X-Bunny-Package"

// checkEntryOwned refuses an existing .desktop file bunny did not write.
// Entries share ~/.local/share/applications with distro packages and
// hand-written launchers, and clobbering one is not recoverable.
func checkEntryOwned(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect desktop entry %s: %w", filepath.Base(path), err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), managedKey+"=") {
			return nil
		}
	}
	return fmt.Errorf("%s was not created by bunny", path)
}

// defaultIconSize is the hicolor subdirectory an icon without a declared size
// lands in.
const defaultIconSize = "128x128"

// IconPath is where an icon lands, derived from the same manifest fields at
// install and removal so the two can never disagree.
func IconPath(p *paths.Paths, ic manifest.Icon, vars map[string]string) string {
	size := ic.Size
	if size == "" {
		size = defaultIconSize
	}
	src := runtime.Expand(ic.Src, vars)
	return filepath.Join(p.Icons(), "hicolor", size, "apps", ic.Name+filepath.Ext(src))
}

// CompletionPaths are the files a completions block installs.
func CompletionPaths(p *paths.Paths, comps *manifest.Completions, vars map[string]string) []string {
	if comps == nil {
		return nil
	}
	var out []string
	for _, c := range []struct{ src, dir string }{
		{comps.Bash, p.BashCompletions()},
		{comps.Zsh, p.ZshCompletions()},
		{comps.Fish, p.FishCompletions()},
	} {
		if c.src == "" {
			continue
		}
		out = append(out, filepath.Join(c.dir, filepath.Base(runtime.Expand(c.src, vars))))
	}
	return out
}

// ManagedFiles is every icon and completion path a manifest lays claim to.
// A .desktop entry carries its owner inside it; an icon is binary and a zsh
// completion has to keep #compdef on its first line, so neither can. The
// install-time manifest snapshot is the record instead: if the previous
// snapshot declared a path, bunny put it there and may replace it.
func ManagedFiles(p *paths.Paths, m *manifest.Manifest, vars map[string]string) map[string]bool {
	if m == nil {
		return nil
	}
	owned := map[string]bool{}
	for _, ic := range m.Icons {
		owned[IconPath(p, ic, vars)] = true
	}
	for _, path := range CompletionPaths(p, m.Completions, vars) {
		owned[path] = true
	}
	return owned
}

// InstallIcons copies icons into the XDG icons hierarchy, leaving alone any
// file bunny cannot show it installed. These directories are shared with the
// distro and every other application, so an existing file is somebody's.
func InstallIcons(p *paths.Paths, icons []manifest.Icon, vars map[string]string, owned map[string]bool) error {
	for _, ic := range icons {
		dst := IconPath(p, ic, vars)
		if !claimable(dst, owned) {
			log.Warn("Leaving icon alone", "path", dst, "reason", "not installed by bunny")
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err := fsutil.CopyFile(runtime.Expand(ic.Src, vars), dst, 0644); err != nil {
			return fmt.Errorf("install icon %s: %w", ic.Name, err)
		}
		log.Debug("Installed icon", "name", ic.Name, "path", dst)
	}
	return nil
}

// claimable reports whether bunny may write dst: either nothing is there, or
// the package's previous install put it there.
func claimable(dst string, owned map[string]bool) bool {
	if _, err := os.Lstat(dst); os.IsNotExist(err) {
		return true
	}
	return owned[dst]
}

// RemoveIcons removes the icons this manifest installed. Only the extension
// the manifest actually declares: sweeping .png/.svg/.xpm for the name took
// out variants bunny never wrote, in a directory it shares with everything
// else on the system.
func RemoveIcons(p *paths.Paths, icons []manifest.Icon, vars map[string]string) error {
	var errs []error
	for _, ic := range icons {
		path := IconPath(p, ic, vars)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove icon %s: %w", ic.Name, err))
		}
	}
	return errors.Join(errs...)
}

// iconCacheUpdater runs gtk-update-icon-cache on a hicolor dir. A package-level
// var so tests can stub it out (and so a missing tool is a no-op, not an error).
var iconCacheUpdater = func(hicolorDir string) error {
	path, err := exec.LookPath("gtk-update-icon-cache")
	if err != nil {
		return nil // not a GTK desktop, or tool absent — the mtime bump suffices
	}
	// -f forces a rebuild; -t tolerates a missing index.theme (the user-local
	// hicolor dir usually has none, unlike the system one).
	if out, err := exec.Command(path, "-f", "-t", hicolorDir).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RefreshIconCache rebuilds the hicolor icon-theme cache so freshly installed
// icons appear without a re-login. Best-effort and idempotent: it bumps the
// theme directory mtime (which GTK and most desktops watch to reload a theme)
// and, when available, runs gtk-update-icon-cache. Failure is non-fatal — the
// icons still resolve after the next login.
func RefreshIconCache(p *paths.Paths) {
	hicolor := filepath.Join(p.Icons(), "hicolor")
	if _, err := os.Stat(hicolor); err != nil {
		return // nothing installed under this icon root
	}
	now := time.Now()
	_ = os.Chtimes(hicolor, now, now)
	if err := iconCacheUpdater(hicolor); err != nil {
		log.Debug("gtk-update-icon-cache failed; icons will appear after re-login", "error", err)
	}
}

// InstallCompletions copies shell-completion files to the XDG share dirs.
func InstallCompletions(p *paths.Paths, comps *manifest.Completions, vars map[string]string, owned map[string]bool) error {
	if comps == nil {
		return nil
	}
	pairs := []struct {
		src string
		dir string
	}{
		{comps.Bash, p.BashCompletions()},
		{comps.Zsh, p.ZshCompletions()},
		{comps.Fish, p.FishCompletions()},
	}
	for _, c := range pairs {
		if c.src == "" {
			continue
		}
		src := runtime.Expand(c.src, vars)
		dst := filepath.Join(c.dir, filepath.Base(src))
		if !claimable(dst, owned) {
			log.Warn("Leaving completion alone", "path", dst, "reason", "not installed by bunny")
			continue
		}
		if err := os.MkdirAll(c.dir, 0755); err != nil {
			return err
		}
		if err := fsutil.CopyFile(src, dst, 0644); err != nil {
			return fmt.Errorf("install completion %s: %w", filepath.Base(src), err)
		}
	}
	return nil
}

// RemoveCompletions deletes installed completion files.
func RemoveCompletions(p *paths.Paths, comps *manifest.Completions, vars map[string]string) error {
	if comps == nil {
		return nil
	}
	var errs []error
	for _, path := range CompletionPaths(p, comps, vars) {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove completion %s: %w", filepath.Base(path), err))
		}
	}
	return errors.Join(errs...)
}

// --- internal ---

func buildDesktopEntry(d *manifest.DesktopEntry, vars map[string]string, pkg string) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")
	fmt.Fprintf(&b, "%s=%s\n", managedKey, pkg)

	entryType := d.Type
	if entryType == "" {
		entryType = "Application"
	}
	fmt.Fprintf(&b, "Type=%s\n", entryType)

	fmt.Fprintf(&b, "Name=%s\n", d.Name)
	fmt.Fprintf(&b, "Exec=%s\n", runtime.Expand(d.Exec, vars))

	if d.GenericName != "" {
		fmt.Fprintf(&b, "GenericName=%s\n", d.GenericName)
	}
	if d.Comment != "" {
		fmt.Fprintf(&b, "Comment=%s\n", d.Comment)
	}
	if d.Icon != "" {
		fmt.Fprintf(&b, "Icon=%s\n", d.Icon)
	}
	if d.NoDisplay {
		b.WriteString("NoDisplay=true\n")
	}
	if d.StartupNotify != nil {
		fmt.Fprintf(&b, "StartupNotify=%t\n", *d.StartupNotify)
	}
	if d.StartupWMClass != "" {
		fmt.Fprintf(&b, "StartupWMClass=%s\n", d.StartupWMClass)
	}
	if d.Terminal {
		b.WriteString("Terminal=true\n")
	}
	if len(d.Categories) > 0 {
		fmt.Fprintf(&b, "Categories=%s;\n", strings.Join(d.Categories, ";"))
	}
	if len(d.Keywords) > 0 {
		fmt.Fprintf(&b, "Keywords=%s;\n", strings.Join(d.Keywords, ";"))
	}
	if len(d.MimeTypes) > 0 {
		fmt.Fprintf(&b, "MimeType=%s;\n", strings.Join(d.MimeTypes, ";"))
	}

	if len(d.Actions) > 0 {
		ids := make([]string, 0, len(d.Actions))
		for _, a := range d.Actions {
			ids = append(ids, a.ID)
		}
		fmt.Fprintf(&b, "Actions=%s;\n", strings.Join(ids, ";"))
		for _, a := range d.Actions {
			fmt.Fprintf(&b, "\n[Desktop Action %s]\n", a.ID)
			fmt.Fprintf(&b, "Name=%s\n", a.Name)
			if a.Exec != "" {
				fmt.Fprintf(&b, "Exec=%s\n", runtime.Expand(a.Exec, vars))
			}
		}
	}

	return b.String()
}
