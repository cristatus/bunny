package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// launchState owns every filesystem artifact for one sandbox invocation.
// Keeping them together prevents package-scoped filename races and gives
// supervisors one bounded cleanup target.
type launchState struct {
	dir string
}

func newLaunchState(id string) launchState {
	return launchState{dir: uniqueRuntimePath("launch", id, "")}
}

func (s launchState) path(name string) string { return filepath.Join(s.dir, name) }

func (s launchState) ensure() error {
	if _, err := ensureRuntimeStateDir(); err != nil {
		return err
	}
	collectStaleLaunches(runtimeStateDir())
	if err := os.Mkdir(s.dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create sandbox launch directory: %w", err)
	}
	start, err := processStartTime(os.Getpid())
	if err != nil {
		return fmt.Errorf("identify sandbox launch process: %w", err)
	}
	owner := fmt.Sprintf("%d %s\n", os.Getpid(), start)
	if err := os.WriteFile(s.path("owner"), []byte(owner), 0o600); err != nil {
		return fmt.Errorf("record sandbox launch owner: %w", err)
	}
	return nil
}

func (s launchState) cleanup() { _ = os.RemoveAll(s.dir) }

// collectStaleLaunches removes only launch directories whose recorded PID is
// absent or has been reused. Comparing /proc start time avoids treating an
// unrelated process with the same recycled PID as the owner.
func collectStaleLaunches(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "launch-") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		data, err := os.ReadFile(filepath.Join(dir, "owner"))
		if err != nil {
			continue // possibly being created by a concurrent launch
		}
		fields := strings.Fields(string(data))
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		start, err := processStartTime(pid)
		if err != nil && !os.IsNotExist(err) {
			continue // permission or transient errors are not proof of death
		}
		if err == nil && start == fields[1] {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}

func processStartTime(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	// comm is parenthesized and may contain spaces or ')', so split after its
	// final closing delimiter. The remaining fields start at stat field 3;
	// process start time is field 22, hence index 19.
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return "", fmt.Errorf("malformed /proc stat")
	}
	fields := strings.Fields(string(data)[end+2:])
	if len(fields) <= 19 {
		return "", fmt.Errorf("short /proc stat")
	}
	return fields[19], nil
}
