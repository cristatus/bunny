package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// tree makes a store tree for id at version, marked as bunny's, with size
// bytes of payload.
func tree(t *testing.T, p *paths.Paths, id, version string, size int) string {
	t.Helper()
	dir := p.VersionDir(id, version)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".bunny-package"), []byte(`{"id":"`+id+`","version":"`+version+`"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload"), make([]byte, size), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Old versions are kept on purpose, so the row reports them as a note that
// `bunny clean` frees them, never as a fault, and counts only the trees no
// package link points at.
func TestStoreCheckReportsOldVersions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := paths.At(t.TempDir())
	if got := StoreCheck(p, state.Empty()); got != nil {
		t.Errorf("before any install: got %+v, want no row", got)
	}

	tree(t, p, "jdk-21", "21.0.8", 2048)
	current := tree(t, p, "jdk-21", "21.0.9", 4096)
	if err := os.MkdirAll(filepath.Join(p.Root, "sdk"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(current, filepath.Join(p.Root, "sdk", "jdk-21")); err != nil {
		t.Fatal(err)
	}
	got := StoreCheck(p, state.Empty())
	if len(got) != 1 || got[0].Severity != OK || !strings.Contains(got[0].Detail, "1 old version(s);") {
		t.Errorf("got %+v, want one old version", got)
	}

	if err := os.RemoveAll(p.VersionDir("jdk-21", "21.0.8")); err != nil {
		t.Fatal(err)
	}
	if got := StoreCheck(p, state.Empty()); len(got) != 1 || got[0].Detail != "no old versions" {
		t.Errorf("got %+v, want no old versions", got)
	}
}
