package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// SyncCurrent points current/<capability> at each capability's active
// provider and drops links for capabilities nothing provides any more. It
// rebuilds from state rather than applying one change, so whichever command
// ran last leaves every link right. Anything in current/ that is not a link
// is not bunny's and is left alone.
func SyncCurrent(p *paths.Paths, st *state.State) error {
	var errs []error
	want := map[string]string{}
	for capability, id := range st.Providers {
		if st.IsInstalled(id) {
			want[capability] = id
		}
	}
	if len(want) > 0 {
		if err := os.MkdirAll(p.CurrentDir(), 0755); err != nil {
			return err
		}
	}
	for capability, id := range want {
		link := p.Current(capability)
		if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink == 0 {
			errs = append(errs, fmt.Errorf("%s is not a link bunny made", link))
			continue
		}
		target, err := filepath.Rel(p.CurrentDir(), p.AppDir(id))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if current, err := os.Readlink(link); err == nil && current == target {
			continue
		}
		errs = append(errs, pointLink(link, target, filepath.Join(p.Staging(), "current", capability)))
	}
	entries, err := os.ReadDir(p.CurrentDir())
	if err != nil && !os.IsNotExist(err) {
		errs = append(errs, err)
	}
	for _, e := range entries {
		if _, ok := want[e.Name()]; ok || e.Type()&os.ModeSymlink == 0 {
			continue
		}
		if err := os.Remove(filepath.Join(p.CurrentDir(), e.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
