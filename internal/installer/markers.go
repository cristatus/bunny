package installer

import (
	"os"
	"path/filepath"
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
