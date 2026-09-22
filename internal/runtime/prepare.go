package runtime

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/manifest"
)

// PrepareStepsContext runs each manifest `prepare:` command sequentially in an
// install-time bwrap whose only writable area is workDir, the staging root
// holding src/ and pkg/. This is the strict isolation used while extracting
// and laying out a package; it is distinct from the run-time launch path
// which is portability-flavored. Cancellation propagates to bwrap via ctx.
//
// shadow maps a real directory to a staging directory that stands in for it,
// mounted at the real path. A step writing to {data} therefore uses the path
// the package will really see at run time, while the bytes land in staging for
// the caller to merge out once the install commits. Without it every such
// placeholder would have to name a staging path instead, and any manifest that
// bakes one into a config file would record a location that gets deleted.
func PrepareStepsContext(ctx context.Context, workDir, srcDir string, shadow map[string]string, commands []string, vars map[string]string) error {
	for _, cmd := range commands {
		expanded := manifest.Expand(cmd, vars)
		if err := runPrepareStep(ctx, workDir, srcDir, shadow, expanded); err != nil {
			return fmt.Errorf("prepare command %q failed: %w", cmd, err)
		}
	}
	return nil
}

// runPrepareStep makes the staging root writable and nothing else: the rest of
// the filesystem is a read-only bind, and $HOME is a tmpfs that goes away with
// the sandbox. The root is bound rather than src/ and pkg/ individually,
// because {work} is a placeholder manifests are given and everything under it
// has to persist. Binding only children would leave the root masked by the
// tmpfs, where writes succeed and then vanish.
//
// A read-only bind does not stop connect() on a Unix socket, so /run is a
// tmpfs too: the session bus under /run/user can start host processes through
// systemd, outside any sandbox. The real home is hidden, so a step cannot copy
// keys into the install tree, and the environment is cleared so it cannot read
// the user's tokens either.
func runPrepareStep(ctx context.Context, workDir, srcDir string, shadow map[string]string, command string) error {
	bwrapPath, err := FindBwrap()
	if err != nil {
		return err
	}
	hostHome, err := prepareHostHome()
	if err != nil {
		return err
	}
	args := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/run",
		"--tmpfs", "/tmp",
		// A dedicated tmpfs under /var/tmp, not /home: on ostree distros
		// (Silverblue, Kinoite) /home is a symlink to /var/home, which bwrap
		// refuses to mount onto. Not under /tmp either — some prepare-step
		// tooling (e.g. codex's installer) refuses to place itself under
		// what looks like a temp dir. /var/tmp is a real, non-symlink
		// directory bwrap can mount onto directly, unlike a synthetic
		// top-level path, which the read-only root bind has no room to
		// create.
		"--tmpfs", "/var/tmp",
		"--dir", "/var/tmp/home",
	}
	if hostHome != "/" {
		args = append(args, "--tmpfs", hostHome)
	}
	args = append(args, prepareEnvArgs()...)
	// After the home tmpfs, since staging normally sits under the home.
	args = append(args, "--bind", workDir, workDir)
	// After the staging bind, so a shadow of a directory inside staging still
	// wins, and sorted so the sandbox is identical run to run.
	for _, real := range slices.Sorted(maps.Keys(shadow)) {
		args = append(args, "--bind", shadow[real], real)
	}
	args = append(args,
		"--chdir", srcDir,
		"--unshare-all",
		"--die-with-parent",
		"sh", "-c", command,
	)
	log.Debug("Prepare bwrap", "cmd", command)
	c := exec.CommandContext(ctx, bwrapPath, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// prepareHostHome is the login home a prepare step must not see. The passwd
// entry is authoritative; $HOME is the fallback for a container whose uid has
// none.
func prepareHostHome() (string, error) {
	if home, err := realUserHomeDir(); err == nil {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the home directory to hide from prepare: %w", err)
	}
	return home, nil
}

// prepareEnvArgs gives a step a cleared environment with only what unpacking
// tools need: the host PATH and locale, and the scratch home.
func prepareEnvArgs() []string {
	args := []string{"--clearenv", "--setenv", "HOME", "/var/tmp/home"}
	for _, name := range []string{"PATH", "LANG", "LC_ALL"} {
		if value, ok := os.LookupEnv(name); ok {
			args = append(args, "--setenv", name, value)
		}
	}
	return args
}
