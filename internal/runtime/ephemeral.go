package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// persistBindArgs binds each home-relative persist entry from the seed back
// over the ephemeral overlay, punching a hole to durable storage. bwrap
// resolves a --bind source on the host, not in the sandbox under
// construction, so binding {home}/<path> onto itself works even though
// {home} is the overlay mountpoint.
//
// An entry must already exist: Bunny cannot mount over a nonexistent target
// without mutating the host, and cannot guess file vs directory.
//
// Entries are symlink-resolved and refused if they land outside home. The
// manifest-time check sees only a literal ".." escape, so an earlier
// isolated run could otherwise plant a symlink a later ephemeral run
// follows out. The resolved path is what gets bound, not the original.
func persistBindArgs(home string, persist []string) ([]string, error) {
	if len(persist) == 0 {
		return nil, nil
	}
	// home may not exist yet; the per-entry lookup below reports that.
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("resolve sandbox home %s: %w", home, err)
		}
		realHome = home
	}
	args := make([]string, 0, 2*len(persist))
	for _, rel := range persist {
		path := filepath.Join(home, rel)
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("sandbox persist path %s does not exist: launch the package persistently (home: isolated) to create it first, or remove it from persist", path)
			}
			return nil, fmt.Errorf("inspect sandbox persist path %s: %w", path, err)
		}
		if real != realHome && !isAncestor(realHome, real) {
			return nil, fmt.Errorf("sandbox persist path %s resolves to %s, outside the isolated home: refusing to bind a path a symlink could redirect out of the sandbox", path, real)
		}
		args = append(args, "--bind", real, path)
	}
	return args, nil
}

// ephemeralOverlayArgs seeds HOME from isolatedHome and discards writes to
// a tmpfs upper, or nil when the policy is not ephemeral.
func ephemeralOverlayArgs(policy *PackageSandbox, isolatedHome string) []string {
	if policy.Home != "ephemeral" {
		return nil
	}
	return []string{"--overlay-src", isolatedHome, "--tmp-overlay", isolatedHome}
}

// ephemeralPersistArgs skips persistBindArgs for a non-ephemeral policy.
func ephemeralPersistArgs(policy *PackageSandbox, isolatedHome string) ([]string, error) {
	if policy.Home != "ephemeral" {
		return nil, nil
	}
	return persistBindArgs(isolatedHome, policy.Persist)
}

// CheckOverlaySupport probes for unprivileged overlayfs in a user namespace
// (Linux ~5.11+), the one non-universal dependency of an ephemeral home.
// Callers fail closed: falling back to a persistent home would keep exactly
// what the user asked to discard.
func CheckOverlaySupport() error {
	bwrapPath, err := FindBwrap()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "bunny-overlay-probe-*")
	if err != nil {
		return fmt.Errorf("create overlay probe directory: %w", err)
	}
	defer os.RemoveAll(tmp)
	cmd := exec.Command(bwrapPath, "--dev-bind", "/", "/",
		"--overlay-src", tmp, "--tmp-overlay", tmp, "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf(
			"unprivileged overlayfs unavailable (home: ephemeral needs Linux ~5.11+ with unprivileged user namespaces): %s: %w",
			strings.TrimSpace(string(out)), err)
	}
	return nil
}
