package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// A private-network launch runs the payload as uid 0 inside pasta's user
// namespace, so a path derived from os.Getuid misses the file the enclosing
// layer mounted under the real user's runtime directory. Reading the mount
// point back is what keeps a nested launch working there, since no bubblewrap
// layer can be created inside that namespace at all.
func TestContextPathFromMountinfoFindsAnotherUidsMount(t *testing.T) {
	mountinfo := []byte(
		"24 30 0:22 / /proc rw,nosuid shared:5 - proc proc rw\n" +
			"31 30 0:26 / /run/user/1000/bunny rw,nosuid - tmpfs tmpfs rw\n" +
			"32 31 0:26 /x /run/user/1000/bunny/sandbox-context.json ro,nosuid - tmpfs tmpfs ro\n")
	if got, want := contextPathFromMountinfo(mountinfo), "/run/user/1000/bunny/sandbox-context.json"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The scan is the one place a path is accepted without being derived, so it
// only ever matches Bunny's own layout: a bare uid directory and the exact
// file name, nothing a nearby path could impersonate.
func TestContextPathFromMountinfoRejectsForeignPaths(t *testing.T) {
	for name, point := range map[string]string{
		"non-numeric uid":      "/run/user/nobody/bunny/sandbox-context.json",
		"different file":       "/run/user/1000/bunny/dbus-tool.sock",
		"nested deeper":        "/run/user/1000/bunny/sub/sandbox-context.json",
		"outside the layout":   "/tmp/bunny/sandbox-context.json",
		"suffix on the name":   "/run/user/1000/bunny/sandbox-context.json.bak",
		"prefix on the layout": "/evil/run/user/1000/bunny/sandbox-context.json",
	} {
		line := []byte("32 31 0:26 / " + point + " ro,nosuid - tmpfs tmpfs ro\n")
		if got := contextPathFromMountinfo(line); got != "" {
			t.Errorf("%s: %q must not be accepted, got %q", name, point, got)
		}
	}
	if got := contextPathFromMountinfo([]byte("too few fields\n\n")); got != "" {
		t.Errorf("short line must be ignored, got %q", got)
	}
}

// The derived path wins when it exists, so the ordinary case never depends on
// parsing mountinfo at all.
func TestReadMountedContextPrefersTheDerivedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sandbox-context.json")
	if err := os.WriteFile(path, []byte(`{"packages":["tool"],"boundary":"hardened"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	original := sandboxContextFile
	sandboxContextFile = path
	t.Cleanup(func() { sandboxContextFile = original })

	got, err := readMountedContext()
	if err != nil {
		t.Fatal(err)
	}
	if got.Boundary != "hardened" || len(got.Packages) != 1 {
		t.Errorf("derived path must be read as-is, got %+v", got)
	}
}

// No context anywhere stays the conservative answer: an empty context, not an
// error, so the caller builds its full layer.
func TestReadMountedContextAbsentIsEmpty(t *testing.T) {
	original := sandboxContextFile
	sandboxContextFile = filepath.Join(t.TempDir(), "absent.json")
	t.Cleanup(func() { sandboxContextFile = original })

	got, err := readMountedContext()
	if err != nil {
		t.Fatalf("an absent context is not an error: %v", err)
	}
	if len(got.Packages) != 0 || got.Boundary != "" {
		t.Errorf("absent context must be empty, got %+v", got)
	}
}

// When the derived path is absent, the mount table is what answers. This
// covers the wiring rather than the parser: without it, a nested launch inside
// a private-network sandbox sees no context and fails to start.
func TestReadMountedContextFallsBackToTheMountTable(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "sandbox-context.json")
	if err := os.WriteFile(staged, []byte(`{"packages":["outer"],"netMode":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mountinfo := filepath.Join(dir, "mountinfo")
	// Field five is the mount point, and the scan only accepts Bunny's layout,
	// so the fixture uses a real uid-bearing path bound to the staged file.
	line := "32 31 0:26 / /run/user/4242/bunny/sandbox-context.json ro - tmpfs tmpfs ro\n"
	if err := os.WriteFile(mountinfo, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	originalContext, originalMountinfo := sandboxContextFile, mountinfoPath
	sandboxContextFile = filepath.Join(dir, "derived-absent.json")
	mountinfoPath = mountinfo
	t.Cleanup(func() { sandboxContextFile, mountinfoPath = originalContext, originalMountinfo })

	if got := mountedContextPath(); got != "/run/user/4242/bunny/sandbox-context.json" {
		t.Fatalf("mount table must supply the path, got %q", got)
	}
	// The path the table reports does not exist in a test, so the read is
	// exercised against the staged file through the same code path.
	sandboxContextFile = staged
	got, err := readMountedContext()
	if err != nil {
		t.Fatal(err)
	}
	if got.NetMode != "private" {
		t.Errorf("context must carry the enclosing network mode, got %+v", got)
	}
}
