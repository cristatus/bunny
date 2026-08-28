package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteProjectVersion(t *testing.T) {
	dir := t.TempDir()

	// Create.
	if err := WriteProjectVersion(dir, "node", "22"); err != nil {
		t.Fatal(err)
	}
	if p, _ := ResolveProjectVersion(dir, "node"); p == nil || p.Value != "22" {
		t.Fatalf("node pin not created: %+v", p)
	}

	// Add a second capability; the first must survive.
	if err := WriteProjectVersion(dir, "jdk", "21"); err != nil {
		t.Fatal(err)
	}
	if p, _ := ResolveProjectVersion(dir, "node"); p == nil || p.Value != "22" {
		t.Errorf("node pin lost after adding jdk: %+v", p)
	}
	if p, _ := ResolveProjectVersion(dir, "jdk"); p == nil || p.Value != "21" {
		t.Errorf("jdk pin missing: %+v", p)
	}

	// Update in place, preserving comments and other pins.
	os.WriteFile(filepath.Join(dir, ProjectVersionFile), []byte("# my pins\nnode 22\njdk 21\n"), 0644)
	if err := WriteProjectVersion(dir, "node", "24"); err != nil {
		t.Fatal(err)
	}
	s := readFile(t, filepath.Join(dir, ProjectVersionFile))
	if !strings.Contains(s, "# my pins") {
		t.Error("comment not preserved")
	}
	if !strings.Contains(s, "node 24") || strings.Contains(s, "node 22") {
		t.Errorf("node not updated in place: %q", s)
	}
	if !strings.Contains(s, "jdk 21") {
		t.Errorf("jdk pin lost: %q", s)
	}
	if c := strings.Count(s, "node "); c != 1 {
		t.Errorf("expected exactly one node line, got %d in %q", c, s)
	}
}

func TestRemoveProjectVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ProjectVersionFile)
	os.WriteFile(path, []byte("# pins\nnode 22\njdk 21\n"), 0644)

	// Remove one, keep the other and the comment.
	if removed, err := RemoveProjectVersion(dir, "node"); err != nil || !removed {
		t.Fatalf("remove node: removed=%v err=%v", removed, err)
	}
	s := readFile(t, path)
	if strings.Contains(s, "node ") {
		t.Errorf("node pin should be gone: %q", s)
	}
	if !strings.Contains(s, "jdk 21") || !strings.Contains(s, "# pins") {
		t.Errorf("jdk pin / comment should survive: %q", s)
	}

	// Removing a capability that isn't pinned reports false, no error.
	if removed, err := RemoveProjectVersion(dir, "python"); err != nil || removed {
		t.Errorf("removing absent pin: removed=%v err=%v", removed, err)
	}

	// Removing on a nonexistent file is a no-op, not an error.
	if removed, err := RemoveProjectVersion(t.TempDir(), "node"); err != nil || removed {
		t.Errorf("no file: removed=%v err=%v", removed, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestResolveProjectVersionWalksUp(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "a", "b")
	leaf := filepath.Join(parent, "c")
	if err := os.MkdirAll(leaf, 0755); err != nil {
		t.Fatal(err)
	}
	pin := []byte("# pinned for project\nnode 22\njdk 21\n")
	if err := os.WriteFile(filepath.Join(parent, ProjectVersionFile), pin, 0644); err != nil {
		t.Fatal(err)
	}

	r, err := ResolveProjectVersion(leaf, "node")
	if err != nil {
		t.Fatal(err)
	}
	if r == nil || r.Value != "22" {
		t.Fatalf("expected node 22, got %+v", r)
	}
	if r.Source != filepath.Join(parent, ProjectVersionFile) {
		t.Errorf("unexpected source: %s", r.Source)
	}

	r, _ = ResolveProjectVersion(leaf, "jdk")
	if r == nil || r.Value != "21" {
		t.Fatalf("expected jdk 21, got %+v", r)
	}

	r, _ = ResolveProjectVersion(leaf, "go")
	if r != nil {
		t.Errorf("expected nil for unknown capability, got %+v", r)
	}
}

func TestResolveProjectVersionNoFile(t *testing.T) {
	r, err := ResolveProjectVersion(t.TempDir(), "node")
	if err != nil {
		t.Fatal(err)
	}
	if r != nil {
		t.Errorf("expected nil, got %+v", r)
	}
}

func TestResolveProjectVersionSkipsUnreadablePin(t *testing.T) {
	dir := t.TempDir()
	// A .bunny-version that is a directory can't be read as a file. It must be
	// skipped rather than breaking every shimmed command run from here.
	if err := os.Mkdir(filepath.Join(dir, ProjectVersionFile), 0755); err != nil {
		t.Fatal(err)
	}
	r, err := ResolveProjectVersion(dir, "node")
	if err != nil {
		t.Fatalf("unreadable pin should be skipped, not error: %v", err)
	}
	if r != nil {
		t.Errorf("expected no pin, got %+v", r)
	}
}

func TestResolveAllPins(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ProjectVersionFile), []byte("node 22\njdk 21\n"), 0644)
	pins, source, err := ResolveAllPins(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 || pins["node"] != "22" || pins["jdk"] != "21" {
		t.Errorf("got %v", pins)
	}
	if source == "" {
		t.Error("source should be set")
	}
}

func TestResolveAllPinsMissing(t *testing.T) {
	pins, source, err := ResolveAllPins(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if pins != nil || source != "" {
		t.Errorf("expected nil/empty, got %v %q", pins, source)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveNearestDirWins(t *testing.T) {
	parent := t.TempDir()
	writeFile(t, parent, ".bunny-version", "jdk 11\n")
	child := filepath.Join(parent, "sub")
	os.MkdirAll(child, 0755)
	writeFile(t, child, ".bunny-version", "jdk 17\n")
	pin, _ := ResolveProjectVersion(child, "jdk")
	if pin == nil || pin.Value != "17" {
		t.Fatalf("got %+v, want jdk=17 (nearer file wins)", pin)
	}
}

// The walk is per capability: a nearer file that pins something else must not
// stop it, so a subproject overrides only what it names.
func TestResolveInheritsUnpinnedCapabilityFromAncestor(t *testing.T) {
	parent := t.TempDir()
	writeFile(t, parent, ".bunny-version", "jdk 11\n")
	child := filepath.Join(parent, "sub")
	os.MkdirAll(child, 0755)
	writeFile(t, child, ".bunny-version", "node 24\n")

	if pin, _ := ResolveProjectVersion(child, "jdk"); pin == nil || pin.Value != "11" {
		t.Fatalf("jdk should be inherited from the parent, got %+v", pin)
	}
	if pin, _ := ResolveProjectVersion(child, "node"); pin == nil || pin.Value != "24" {
		t.Fatalf("node should come from the child, got %+v", pin)
	}
}

// A foreign pin file is no longer read at all: honoring it meant discarding
// the vendor and patch level it encodes and silently running another build.
func TestForeignPinFilesAreIgnored(t *testing.T) {
	for _, name := range []string{".tool-versions", ".sdkmanrc", ".java-version"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, name, "java=21.0.2-amzn\njava 21.0.2-amzn\n21.0.2\n")
			if pin, err := ResolveProjectVersion(dir, "jdk"); err != nil || pin != nil {
				t.Fatalf("%s must be ignored, got %+v (err %v)", name, pin, err)
			}
		})
	}
}

// A pin may name a package id outright, which is how a specific provider is
// selected; a bare version still derives <capability>-<version>.
func TestPinPackageID(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"21", "jdk-21"},
		{"corretto-21", "corretto-21"},
		{"", ""},
	} {
		p := &ProjectPin{Capability: "jdk", Value: tc.value}
		if got := p.PackageID(); got != tc.want {
			t.Errorf("PackageID(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestParseBunnyVersionLiteral(t *testing.T) {
	got := parseBunnyVersion("jdk 21\nnode 20\n# c\n")
	if got["jdk"] != "21" || got["node"] != "20" {
		t.Errorf("got %v, want jdk=21 node=20", got)
	}
}
