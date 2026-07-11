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
func InstallEntries(p *paths.Paths, entries []manifest.DesktopEntry, vars map[string]string) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(p.Desktop(), 0755); err != nil {
		return err
	}
	for _, d := range entries {
		content := buildDesktopEntry(&d, vars)
		dst := filepath.Join(p.Desktop(), d.ID)
		if err := fsutil.WriteFile(dst, []byte(content), 0644); err != nil {
			return fmt.Errorf("write desktop entry %s: %w", d.ID, err)
		}
		log.Debug("Created desktop entry", "id", d.ID)
	}
	return nil
}

// RemoveEntries deletes .desktop files referenced by the manifest.
func RemoveEntries(p *paths.Paths, entries []manifest.DesktopEntry) error {
	var errs []error
	for _, d := range entries {
		path := filepath.Join(p.Desktop(), d.ID)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove desktop entry %s: %w", d.ID, err))
		}
	}
	return errors.Join(errs...)
}

// InstallIcons copies icons into the XDG icons hierarchy.
func InstallIcons(p *paths.Paths, icons []manifest.Icon, vars map[string]string) error {
	for _, ic := range icons {
		src := runtime.Expand(ic.Src, vars)
		size := ic.Size
		if size == "" {
			size = "128x128"
		}
		dstDir := filepath.Join(p.Icons(), "hicolor", size, "apps")
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return err
		}
		ext := filepath.Ext(src)
		dst := filepath.Join(dstDir, ic.Name+ext)
		if err := fsutil.CopyFile(src, dst, 0644); err != nil {
			return fmt.Errorf("install icon %s: %w", ic.Name, err)
		}
		log.Debug("Installed icon", "name", ic.Name, "size", size)
	}
	return nil
}

// RemoveIcons removes installed icons across the supported extensions.
func RemoveIcons(p *paths.Paths, icons []manifest.Icon) error {
	var errs []error
	for _, ic := range icons {
		size := ic.Size
		if size == "" {
			size = "128x128"
		}
		for _, ext := range []string{".png", ".svg", ".xpm"} {
			path := filepath.Join(p.Icons(), "hicolor", size, "apps", ic.Name+ext)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Errorf("remove icon %s: %w", ic.Name+ext, err))
			}
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
func InstallCompletions(p *paths.Paths, comps *manifest.Completions, vars map[string]string) error {
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
		if err := os.MkdirAll(c.dir, 0755); err != nil {
			return err
		}
		dst := filepath.Join(c.dir, filepath.Base(src))
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
		path := filepath.Join(c.dir, filepath.Base(src))
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Errorf("remove completion %s: %w", filepath.Base(src), err))
		}
	}
	return errors.Join(errs...)
}

// --- internal ---

func buildDesktopEntry(d *manifest.DesktopEntry, vars map[string]string) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")

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
