package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --force must replace bunny's own install and nothing else. Install roots are
// configurable, so the target path may be a directory the user keeps things in.
func TestCheckOwned(t *testing.T) {
	t.Run("missing directory is fine", func(t *testing.T) {
		if err := checkOwned(filepath.Join(t.TempDir(), "absent"), "ripgrep"); err != nil {
			t.Errorf("nothing to protect, got %v", err)
		}
	})

	t.Run("unmarked directory is refused", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "NOTES.txt"), []byte("mine"), 0644); err != nil {
			t.Fatal(err)
		}
		err := checkOwned(dir, "ripgrep")
		if err == nil {
			t.Fatal("expected a directory bunny did not create to be refused")
		}
		if !strings.Contains(err.Error(), "not created by bunny") {
			t.Errorf("error should say why: %v", err)
		}
	})

	t.Run("own install is replaceable", func(t *testing.T) {
		dir := t.TempDir()
		if err := WritePackageMarker(dir, PackageMarker{ID: "ripgrep", Version: "1.0", Kind: "cli"}); err != nil {
			t.Fatal(err)
		}
		if err := checkOwned(dir, "ripgrep"); err != nil {
			t.Errorf("bunny's own tree should be replaceable: %v", err)
		}
	})

	t.Run("another package's tree is refused", func(t *testing.T) {
		dir := t.TempDir()
		if err := WritePackageMarker(dir, PackageMarker{ID: "fd", Version: "1.0", Kind: "cli"}); err != nil {
			t.Fatal(err)
		}
		if err := checkOwned(dir, "ripgrep"); err == nil {
			t.Error("expected a mismatched package id to be refused")
		}
	})
}

func TestPackageMarkerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WritePackageMarker(dir, PackageMarker{ID: "node-22", Version: "22.0.0", Kind: "sdk"}); err != nil {
		t.Fatal(err)
	}
	m, err := ReadPackageMarker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "node-22" || m.Version != "22.0.0" || m.Kind != "sdk" {
		t.Errorf("marker = %+v", m)
	}
	// The timestamp is filled in so a marker can rebuild a state entry.
	if m.Installed.IsZero() {
		t.Error("Installed should be stamped")
	}
}
