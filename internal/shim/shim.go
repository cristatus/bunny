// Package shim manages the symlinks in $BUNNY_HOME/bin that dispatch to
// the bunny binary. When invoked via a shim (e.g. `node`), bunny detects
// argv[0] and resolves the right package + version at every invocation
// — that's what makes `.bunny-version` work across terminals, IDEs, and CI
// without any shell hooks.
package shim

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"
)

// ReservedName is the executable Bunny must never replace with a shim.
const ReservedName = "bunny"

// Install creates symlinks `binDir/<name>` → bunnyPath as an all-or-nothing
// batch: every name is validated up front, then applied, and if any symlink
// fails the already-applied ones are rolled back to their previous targets.
// Only Bunny's own shims are replaced; regular files and symlinks belonging to
// other tools are refused, because Bunny cannot prove that it owns them.
func Install(binDir string, names []string, bunnyPath string) error {
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}
	previous := make(map[string]string, len(names))
	for _, name := range names {
		path := filepath.Join(binDir, name)
		if name == ReservedName {
			return fmt.Errorf("command %q is reserved for the Bunny executable", name)
		}
		if filepath.Base(name) != name || name == "." || name == ".." {
			return fmt.Errorf("invalid shim name %q", name)
		}
		target, err := ownedTarget(path, bunnyPath)
		if err != nil {
			return fmt.Errorf("replace shim %s: %w", name, err)
		}
		if target != "" {
			previous[name] = target
		}
	}

	var installed []string
	for _, name := range names {
		path := filepath.Join(binDir, name)
		if err := replaceSymlink(path, bunnyPath); err != nil {
			rollbackSymlinks(binDir, installed, previous)
			return fmt.Errorf("symlink %s -> %s: %w", path, bunnyPath, err)
		}
		installed = append(installed, name)
		log.Debug("Created shim", "name", name, "target", bunnyPath)
	}
	return nil
}

// Remove deletes symlinks owned by Bunny. The Bunny binary itself, regular
// files, and links belonging to other tools are preserved and reported as
// errors.
func Remove(binDir string, names []string, bunnyPath string) error {
	var errs []error
	for _, name := range names {
		if name == ReservedName {
			continue
		}
		if filepath.Base(name) != name || name == "." || name == ".." {
			errs = append(errs, fmt.Errorf("invalid shim name %q", name))
			continue
		}
		path := filepath.Join(binDir, name)
		target, err := ownedTarget(path, bunnyPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("remove shim %s: %w", name, err))
			continue
		}
		if target == "" {
			continue // nothing there
		}
		if err := os.Remove(path); err != nil {
			errs = append(errs, fmt.Errorf("remove shim %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// ownedTarget inspects path and returns the symlink target if it is a shim
// Bunny owns, or "" if nothing is there. A file Bunny cannot prove it owns is
// an error rather than something to overwrite: the shim directory may be
// shared with other tools' entry points (pipx, cargo, hand-written links), and
// silently clobbering one of those is not recoverable.
//
// A link is ours in three cases, in order of how much they prove:
//
//   - it points at "<binDir>/bunny", which survives running from a different
//     bunny binary than the one that wrote the shim, without claiming an
//     unrelated dangling link to some other /opt/some-tool/bunny;
//   - it resolves to the same file as the running bunny binary;
//   - it is dangling and points at a file called "bunny", which is nobody's
//     working entry point, so reclaiming it keeps a shim repairable after the
//     binary it named moves away.
func ownedTarget(path, bunnyPath string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("%s is not a bunny shim (regular file)", path)
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", fmt.Errorf("read link %s: %w", path, err)
	}
	binDir := filepath.Dir(path)
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Join(binDir, absTarget)
	}
	if filepath.Clean(absTarget) == filepath.Join(binDir, ReservedName) {
		return target, nil
	}
	resolved, resolveErr := filepath.EvalSymlinks(path)
	if resolveErr == nil {
		if bunnyResolved, err := filepath.EvalSymlinks(bunnyPath); err == nil && resolved == bunnyResolved {
			return target, nil
		}
		return "", fmt.Errorf("%s is not a bunny shim (points to %s)", path, resolved)
	}
	if filepath.Base(target) == ReservedName {
		return target, nil
	}
	return "", fmt.Errorf("%s is not a bunny shim (dangling link to %s)", path, target)
}

// Difference returns the names in `from` that are not in `keep`. It computes
// which shims to remove when a command set shrinks (e.g. switching providers,
// or reinstalling a package whose command list changed).
func Difference(from, keep []string) []string {
	kept := make(map[string]bool, len(keep))
	for _, name := range keep {
		kept[name] = true
	}
	var out []string
	for _, name := range from {
		if !kept[name] {
			out = append(out, name)
		}
	}
	return out
}

func replaceSymlink(path, target string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.shim")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func rollbackSymlinks(binDir string, names []string, previous map[string]string) {
	for _, name := range names {
		path := filepath.Join(binDir, name)
		if target, ok := previous[name]; ok {
			_ = replaceSymlink(path, target)
		} else {
			_ = os.Remove(path)
		}
	}
}

// BunnyBinaryPath returns an absolute, dereferenced path to the running
// bunny binary, suitable as a symlink target. If the running binary is
// itself a shim under binDir, fall back to "<binDir>/bunny" — otherwise
// every shim would point at another shim.
func BunnyBinaryPath(binDir string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		fallback := filepath.Join(binDir, ReservedName)
		if _, statErr := os.Stat(fallback); statErr == nil {
			return fallback, nil
		}
		return "", err
	}
	if filepath.Dir(resolved) == binDir && filepath.Base(resolved) != ReservedName {
		return filepath.Join(binDir, ReservedName), nil
	}
	return resolved, nil
}
