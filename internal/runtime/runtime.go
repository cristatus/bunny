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
	return findTool("bwrap", "required for sandboxing", "bubblewrap", "bubblewrap")
}

// findTool locates an optional sandbox helper, failing closed with an install
// hint. The reason names which policy needs it.
func findTool(name, reason, archPkg, debianPkg string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found (%s): %w\nInstall: sudo pacman -S %s (Arch) or sudo apt install %s (Debian/Ubuntu)",
			name, reason, err, archPkg, debianPkg)
	}
	return path, nil
}

// Expand performs `{key}` substitution against vars. Re-export of
// manifest.Expand so callers in this package don't need a second import.
func Expand(s string, vars map[string]string) string { return manifest.Expand(s, vars) }
