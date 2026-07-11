// Package runtime owns two distinct execution paths:
//
//  1. Run-time launch (Prepare → Exec). Resolves a manifest's binary,
//     applies its env + bin.args, and direct-execs the binary. No bwrap
//     is involved.
//  2. Install-time isolation (PrepareStepsContext). Runs a manifest's `prepare:`
//     shell commands inside an `--unshare-all` bwrap with writable views
//     only of the source dir and the package staging dir.
//
// The two paths share the placeholder expander (Expand) but are otherwise
// independent — only install-time isolation uses bwrap/FindBwrap.
package runtime

import (
	"fmt"
	"os/exec"

	"github.com/cristatus/bunny/internal/manifest"
)

// FindBwrap returns the path to bwrap or a helpful error.
func FindBwrap() (string, error) {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return "", fmt.Errorf("bubblewrap not found: %w\nInstall: sudo pacman -S bubblewrap (Arch) or sudo apt install bubblewrap (Debian/Ubuntu)", err)
	}
	return path, nil
}

// Expand performs `{key}` substitution against vars. Re-export of
// manifest.Expand so callers in this package don't need a second import.
func Expand(s string, vars map[string]string) string { return manifest.Expand(s, vars) }
