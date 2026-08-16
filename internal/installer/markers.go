package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cristatus/bunny/internal/fsutil"
)

// cachedirTag is the Cache Directory Tagging Specification marker. A directory
// containing a CACHEDIR.TAG whose first 43 bytes are this signature is treated
// as disposable cache by backup tools that support it (borg/restic
// --exclude-caches, GNU tar --exclude-caches, etc.). See https://bford.info/cachedir/.
const cachedirTag = "Signature: 8a477f597d28d172789f06886806bc55\n" +
	"# This file is a cache directory tag created by bunny.\n" +
	"# For information about cache directory tags, see https://bford.info/cachedir/\n"

const nobackupNote = "# Disposable bunny cache/work data — safe to exclude from backups.\n"

// markDisposable writes backup-exclusion markers into dir so backup tools skip
// it: CACHEDIR.TAG (Cache Directory Tagging Spec) and .nobackup (honored by
// tools' --exclude-if-present). Best-effort and idempotent — failures never
// abort an install, and existing files are left untouched.
func markDisposable(dir string) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	writeMarkerIfAbsent(filepath.Join(dir, "CACHEDIR.TAG"), cachedirTag)
	writeMarkerIfAbsent(filepath.Join(dir, ".nobackup"), nobackupNote)
}

func writeMarkerIfAbsent(path, content string) {
	if _, err := os.Stat(path); err == nil {
		return // present already — don't clobber
	}
	_ = os.WriteFile(path, []byte(content), 0644)
}

// isDisposableMarker reports whether name is one of the backup-exclusion marker
// files markDisposable writes. `bunny clean` preserves these so the cache/tmp
// roots stay tagged disposable even after their contents are pruned.
func isDisposableMarker(name string) bool {
	return name == "CACHEDIR.TAG" || name == ".nobackup"
}

// PackageMarkerName identifies an install tree as bunny's. It sits at the root
// of every installed package.
//
// State is the authoritative record of what is installed; this marker answers
// a different question, asked of the filesystem rather than of state: is this
// directory mine to delete? That mattered little when everything lived under
// one bunny-owned root, and matters a great deal now that install roots are
// configurable and may point at a directory the user also keeps things in.
const PackageMarkerName = ".bunny-package"

// PackageMarker is the marker's contents. It carries enough to rebuild a state
// entry, so an installation whose state.json is lost is still identifiable.
type PackageMarker struct {
	ID        string    `json:"id"`
	Version   string    `json:"version"`
	Kind      string    `json:"kind,omitempty"`
	Bunny     string    `json:"bunny,omitempty"`
	Installed time.Time `json:"installed"`
}

// WritePackageMarker stamps an install tree as belonging to bunny.
func WritePackageMarker(dir string, m PackageMarker) error {
	if m.Installed.IsZero() {
		m.Installed = time.Now()
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFile(filepath.Join(dir, PackageMarkerName), append(data, '\n'), 0644)
}

// ReadPackageMarker reads the marker from an install tree. A missing marker
// returns os.ErrNotExist, which callers treat as "not bunny's".
func ReadPackageMarker(dir string) (*PackageMarker, error) {
	data, err := os.ReadFile(filepath.Join(dir, PackageMarkerName))
	if err != nil {
		return nil, err
	}
	var m PackageMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", PackageMarkerName, err)
	}
	return &m, nil
}

// checkOwned reports whether dir is a package directory bunny created, and
// refuses with an explanatory error when it is not. A missing directory is
// fine: there is nothing to protect.
//
// This is what stands between `--force` and a user directory that happens to
// share a name with a package, now that install roots can point anywhere.
func checkOwned(dir, id string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	marker, err := ReadPackageMarker(dir)
	if os.IsNotExist(err) {
		return fmt.Errorf("%s exists and was not created by bunny; refusing to replace it\n"+
			"hint: move it aside, or point this package's install root elsewhere", dir)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", dir, err)
	}
	if marker.ID != id {
		return fmt.Errorf("%s belongs to package %q, not %q; refusing to replace it", dir, marker.ID, id)
	}
	return nil
}
