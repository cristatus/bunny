package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchStateGroupsArtifactsAndCleansUp(t *testing.T) {
	old := sandboxContextFile
	sandboxContextFile = filepath.Join(t.TempDir(), contextFileName)
	t.Cleanup(func() { sandboxContextFile = old })

	state := newLaunchState("tool")
	if filepath.Dir(state.path("dbus.sock")) != state.dir || filepath.Dir(state.path("egress.nft")) != state.dir {
		t.Fatal("launch artifacts do not share their launch directory")
	}
	if err := state.ensure(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(state.dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("launch directory mode = %o, want 700", info.Mode().Perm())
	}
	state.cleanup()
	if _, err := os.Stat(state.dir); !os.IsNotExist(err) {
		t.Errorf("launch directory survived cleanup: %v", err)
	}
}

func TestLaunchStateKeepsUnixSocketPathShort(t *testing.T) {
	state := newLaunchState("a" + strings.Repeat("b", 63))
	if path := state.path("dbus.sock"); len(path) >= 108 {
		t.Errorf("proxy socket exceeds Linux sockaddr_un limit: %q", path)
	}
}

func TestCollectStaleLaunchesUsesPIDStartTime(t *testing.T) {
	root := t.TempDir()
	start, err := processStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(root, "launch-active")
	stale := filepath.Join(root, "launch-stale")
	for _, dir := range []string{active, stale} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(active, "owner"), []byte(fmt.Sprintf("%d %s\n", os.Getpid(), start)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "owner"), []byte("99999999 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	collectStaleLaunches(root)
	if _, err := os.Stat(active); err != nil {
		t.Errorf("active launch removed: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale launch not removed: %v", err)
	}
}
