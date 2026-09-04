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

// The pattern is built from the layout constants, so it must accept the path
// this process would actually derive. Spelling the layout twice is what this
// guards against: a rename that updates one and not the other.
func TestContextMountPatternAcceptsTheDerivedPath(t *testing.T) {
	derived := filepath.Join(runtimeStateRoot, "1000", runtimeStateName, contextFileName)
	if !contextMountPattern.MatchString(derived) {
		t.Errorf("pattern %v does not match the derived layout %q", contextMountPattern, derived)
	}
	if !contextMountPattern.MatchString(filepath.Join(runtimeStateRoot, "0", runtimeStateName, contextFileName)) {
		t.Error("pattern must match uid 0, which is the case it exists for")
	}
}

// The derived path wins when it exists, so the ordinary case never consults
// the mount table.
func TestReadMountedContextPrefersTheDerivedPath(t *testing.T) {
	path := mountTestContextFile(t, `{"packages":["tool"],"boundary":"hardened"}`)
	got, err := readContextAt(path, true, func() string {
		t.Error("the mount table must not be consulted when the derived path exists")
		return ""
	})
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
	mountTestContextFile(t, "")
	got, err := readMountedContext()
	if err != nil {
		t.Fatalf("an absent context is not an error: %v", err)
	}
	if len(got.Packages) != 0 || got.Boundary != "" {
		t.Errorf("absent context must be empty, got %+v", got)
	}
}

func TestReadMountedContextRejectsUnknownVersion(t *testing.T) {
	path := mountTestContextFile(t, `{"version":999,"packages":["future"]}`)
	if _, err := readSandboxContextFile(path); err == nil {
		t.Fatal("unknown context version must fail closed")
	}
}

// The wiring, not the parser: when the derived path is absent and the uid
// cannot be trusted, the path the mount table reports is the one actually
// read. Without this a nested launch inside a private-network sandbox sees no
// context and fails trying to build a layer it cannot create.
func TestReadMountedContextReadsWhatTheMountTableReports(t *testing.T) {
	dir := t.TempDir()
	staged := filepath.Join(dir, "from-mount-table.json")
	if err := os.WriteFile(staged, []byte(`{"packages":["outer"],"netMode":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(dir, "derived-absent.json")

	got, err := readContextAt(absent, true, func() string { return staged })
	if err != nil {
		t.Fatal(err)
	}
	if got.NetMode != "private" || len(got.Packages) != 1 {
		t.Errorf("the reported path must be the one read, got %+v", got)
	}
}

// An ordinary launch must not consult the mount table at all: it is on the
// path of every shim invocation, sandboxed or not.
func TestReadMountedContextSkipsTheMountTableWhenUidIsTrusted(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "derived-absent.json")
	got, err := readContextAt(absent, false, func() string {
		t.Error("an untrusted-uid lookup ran on an ordinary launch")
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Packages) != 0 {
		t.Errorf("absent context must be empty, got %+v", got)
	}
}
