package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/manifest"
)

// PrepareStepsContext runs each manifest `prepare:` command sequentially in an
// install-time bwrap that exposes only srcDir + pkgDir as writable. This is
// the strict isolation used while extracting and laying out a package; it is
// distinct from the run-time launch path which is portability-flavored.
// Cancellation propagates to bwrap via ctx.
func PrepareStepsContext(ctx context.Context, srcDir, pkgDir string, commands []string, vars map[string]string) error {
	for _, cmd := range commands {
		expanded := manifest.Expand(cmd, vars)
		if err := runPrepareStep(ctx, srcDir, pkgDir, expanded); err != nil {
			return fmt.Errorf("prepare command %q failed: %w", cmd, err)
		}
	}
	return nil
}

func runPrepareStep(ctx context.Context, srcDir, pkgDir, command string) error {
	bwrapPath, err := FindBwrap()
	if err != nil {
		return err
	}
	args := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--tmpfs", "/home",
		"--setenv", "HOME", "/home",
		"--bind", srcDir, srcDir,
		"--bind", pkgDir, pkgDir,
		"--chdir", srcDir,
		"--unshare-all",
		"--die-with-parent",
		"sh", "-c", command,
	}
	log.Debug("Prepare bwrap", "cmd", command)
	c := exec.CommandContext(ctx, bwrapPath, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
