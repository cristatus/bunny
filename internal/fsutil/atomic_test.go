package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicallyReplacesContentsAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "state.yaml")
	if err := WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0640); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("contents = %q, want new", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Fatalf("mode = %o, want 640", got)
	}
}

func TestCopyFileAtomicallyReplacesDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "nested", "destination")
	if err := os.WriteFile(src, []byte("source contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CopyFile(src, dst, 0755); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "source contents" {
		t.Fatalf("contents = %q", data)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0755 {
		t.Fatalf("mode = %o, want 755", got)
	}
}
