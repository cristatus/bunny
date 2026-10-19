package installer

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// SyncLinks makes each configured path a link to its package's install
// directory while the package is installed, and removes the link once it is
// not. It rebuilds from state and config like SyncCurrent. A path that holds
// anything but a link into this package's install directory is not bunny's:
// it is reported and left alone.
func SyncLinks(p *paths.Paths, st *state.State, links map[string]string) error {
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(links)) {
		if err := syncLink(p, st, id, links[id]); err != nil {
			errs = append(errs, fmt.Errorf("links.%s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func syncLink(p *paths.Paths, st *state.State, id, link string) error {
	if link == p.Root || strings.HasPrefix(link, p.Root+string(filepath.Separator)) {
		return fmt.Errorf("%s is inside bunny's root", link)
	}
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 || !p.OwnsLink(id, link) {
			return fmt.Errorf("%s is not a link bunny made", link)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if !st.IsInstalled(id) {
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	target := p.AppDir(id)
	if current, err := os.Readlink(link); err == nil && current == target {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		return err
	}
	return pointLink(link, target, filepath.Join(p.Staging(), "links", id))
}
