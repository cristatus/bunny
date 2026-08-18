// Package runtime owns two distinct execution paths:
//
//  1. Run-time launch (Prepare → ExecPackage). Resolves a manifest's binary,
//     applies its env + bin.args, then direct-execs it unless the package is
//     explicitly enabled under sandbox.packages.
//  2. Install-time isolation (PrepareStepsContext). Runs a manifest's `prepare:`
//     shell commands inside an `--unshare-all` bwrap with writable views
//     only of the source dir and the package staging dir.
//
// Both paths use bwrap when isolation is selected, but with different trust
// models: install-time isolation is strict, while the opt-in run-time model
// trusts the installed package and scopes its persistent state.
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
