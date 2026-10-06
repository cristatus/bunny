package fsutil

import (
	"os"
	"path/filepath"
)

// LinkTarget returns the path the symlink at link points to, resolving a
// relative target against the link's directory. It follows that one link
// only, so a chain of links above it keeps the paths it names.
func LinkTarget(link string) (string, error) {
	target, err := os.Readlink(link)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	return filepath.Join(filepath.Dir(link), target), nil
}
