package doctor

import (
	"fmt"
	"os"

	"github.com/cristatus/bunny/internal/installer"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// StoreCheck counts the versions updates left in the store. They are kept on
// purpose, since a running process or a path a tool resolved may still use
// one, so this is a note that `bunny clean` would free them, not a fault. It
// counts rather than sizes them: walking every tree on each doctor run costs
// more than the note is worth, and clean reports what it frees. It says
// nothing before anything is installed.
func StoreCheck(p *paths.Paths, st *state.State) []Result {
	const name = "store"
	if _, err := os.Stat(p.Store()); err != nil {
		return nil
	}
	stale, err := installer.StaleVersions(p, st)
	if err != nil {
		return []Result{{Name: name, Detail: "cannot read " + tilde(p.Store()) + ": " + err.Error(), Severity: Warn}}
	}
	if len(stale) == 0 {
		return []Result{{Name: name, Detail: "no old versions", Severity: OK}}
	}
	return []Result{{
		Name:     name,
		Detail:   fmt.Sprintf("%d old version(s); `bunny clean` frees them", len(stale)),
		Severity: OK,
	}}
}
