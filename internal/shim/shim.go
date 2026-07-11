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
// Existing symlinks may be updated, but regular files are never overwritten
// because Bunny cannot prove that it owns them.
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
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("refusing to replace non-shim file %s", path)
			}
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read existing shim %s: %w", name, err)
			}
			previous[name] = target
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect existing shim %s: %w", name, err)
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

// Remove deletes symlinks owned by Bunny. Regular files and the Bunny binary
// itself are preserved and reported as errors.
func Remove(binDir string, names []string) error {
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
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect shim %s: %w", name, err))
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			errs = append(errs, fmt.Errorf("refusing to remove non-shim file %s", path))
			continue
		}
		if err := os.Remove(path); err != nil {
			errs = append(errs, fmt.Errorf("remove shim %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
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
		fallback := filepath.Join(binDir, "bunny")
		if _, statErr := os.Stat(fallback); statErr == nil {
			return fallback, nil
		}
		return "", err
	}
	if filepath.Dir(resolved) == binDir && filepath.Base(resolved) != "bunny" {
		return filepath.Join(binDir, "bunny"), nil
	}
	return resolved, nil
}
