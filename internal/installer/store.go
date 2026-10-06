package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// A package's path is a link into the store, store/<id>/<version>, one tree
// per installed version. An install places the new tree, then switches the
// link with a rename, so a running process keeps the tree it started from.
// Old versions stay until `bunny clean` removes them; uninstall removes all.

// checkOwnedLink accepts a link only when it points at one of id's trees in
// the store. Anything else is a package someone moved out of the root and
// linked back, which bunny must not replace or delete through.
func (i *Installer) checkOwnedLink(link, id string) error {
	target, err := os.Readlink(link)
	if err != nil {
		return err
	}
	if resolved, _ := fsutil.LinkTarget(link); filepath.Dir(resolved) != i.Paths.PackageStore(id) {
		return fmt.Errorf("%s links to %s, and bunny only manages packages inside its own root\n"+
			"hint: move the package back to %s", link, target, link)
	}
	return nil
}

// checkLink refuses anything at a package's path but a link bunny made.
func (i *Installer) checkLink(path, id string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%s is not a link bunny made\nhint: move it aside", path)
	}
	return i.checkOwnedLink(path, id)
}

// versionLinkTarget is what the link at link holds for the tree at
// versionDir: relative to the link's own directory, so the root can move
// without rewriting every link.
func versionLinkTarget(link, versionDir string) (string, error) {
	return filepath.Rel(filepath.Dir(link), versionDir)
}

// pointLink makes link point at target by renaming a link made at tmp onto
// it, so no reader ever finds the path missing.
func pointLink(link, target, tmp string) error {
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(tmp), 0755); err != nil {
		return err
	}
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// linkedVersionDir resolves the tree the link at link points to, or "" when
// there is no link.
func linkedVersionDir(link string) string {
	target, _ := fsutil.LinkTarget(link)
	return target
}

// removeVersions deletes id's trees under packageStore except keep, then the
// package's store directory once nothing is left in it. Only trees whose
// marker names id go, so a stray directory there is left alone.
func removeVersions(packageStore, id string, keep ...string) error {
	entries, err := os.ReadDir(packageStore)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		path := filepath.Join(packageStore, e.Name())
		if slices.Contains(keep, path) {
			continue
		}
		if marker, err := readPackageMarker(path); err != nil || marker.ID != id {
			continue
		}
		errs = append(errs, removeOwnedTree(path))
	}
	_ = os.Remove(packageStore) // only succeeds once it is empty
	return errors.Join(errs...)
}

// StaleVersions lists the trees in the store neither a package link nor state
// accounts for: the versions updates left behind, and those of packages no
// longer installed. Both are consulted: a link proves something still
// reaches a tree, and state keeps the version of a package whose link an
// interrupted uninstall moved aside. Only trees whose marker names the
// package they are filed under are listed.
func StaleVersions(p *paths.Paths, st *state.State) ([]string, error) {
	linked := map[string]bool{}
	for _, id := range st.Installed() {
		linked[p.VersionDir(id, st.VersionOf(id))] = true
	}
	for _, root := range p.InstallRoots() {
		entries, err := os.ReadDir(root)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				linked[linkedVersionDir(filepath.Join(root, e.Name()))] = true
			}
		}
	}
	packages, err := os.ReadDir(p.Store())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, pkg := range packages {
		versions, err := os.ReadDir(p.PackageStore(pkg.Name()))
		if err != nil {
			continue
		}
		for _, v := range versions {
			path := p.VersionDir(pkg.Name(), v.Name())
			if linked[path] {
				continue
			}
			if marker, err := readPackageMarker(path); err == nil && marker.ID == pkg.Name() {
				stale = append(stale, path)
			}
		}
	}
	return stale, nil
}
