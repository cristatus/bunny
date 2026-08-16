package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// --force must replace bunny's own install and nothing else. Install roots are
// configurable, so the target path may be a directory the user keeps things in.
func TestCheckOwned(t *testing.T) {
	// A bare Installer: no state, so only the marker can vouch for a directory.
	i := &Installer{State: state.Empty(), Paths: paths.At(t.TempDir())}

	t.Run("missing directory is fine", func(t *testing.T) {
		if err := i.checkOwned(filepath.Join(t.TempDir(), "absent"), "ripgrep"); err != nil {
			t.Errorf("nothing to protect, got %v", err)
		}
	})

	t.Run("unmarked directory is refused", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "NOTES.txt"), []byte("mine"), 0644); err != nil {
			t.Fatal(err)
		}
		err := i.checkOwned(dir, "ripgrep")
		if err == nil {
			t.Fatal("expected a directory bunny did not create to be refused")
		}
		if !strings.Contains(err.Error(), "not created by bunny") {
			t.Errorf("error should say why: %v", err)
		}
	})

	t.Run("own install is replaceable", func(t *testing.T) {
		dir := t.TempDir()
		if err := writePackageMarker(dir, packageMarker{ID: "ripgrep", Version: "1.0", Kind: "cli"}); err != nil {
			t.Fatal(err)
		}
		if err := i.checkOwned(dir, "ripgrep"); err != nil {
			t.Errorf("bunny's own tree should be replaceable: %v", err)
		}
	})

	t.Run("another package's tree is refused", func(t *testing.T) {
		dir := t.TempDir()
		if err := writePackageMarker(dir, packageMarker{ID: "fd", Version: "1.0", Kind: "cli"}); err != nil {
			t.Fatal(err)
		}
		if err := i.checkOwned(dir, "ripgrep"); err == nil {
			t.Error("expected a mismatched package id to be refused")
		}
	})
}

func TestPackageMarkerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := writePackageMarker(dir, packageMarker{ID: "node-22", Version: "22.0.0", Kind: "sdk"}); err != nil {
		t.Fatal(err)
	}
	m, err := readPackageMarker(dir)
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

// A tree installed before markers existed carries none, but state recording
// the package at that path is bunny's own word and must keep it removable.
func TestCheckOwnedAcceptsStateRecord(t *testing.T) {
	root := t.TempDir()
	st := state.Empty()
	p := paths.At(root).WithLayout(nil, st.Location)
	i := &Installer{State: st, Paths: p}

	st.SetInstalled("ripgrep", "1.0", "", "cli", "")
	dir := p.AppDir("ripgrep")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := i.checkOwned(dir, "ripgrep"); err != nil {
		t.Errorf("state records this install; it must stay removable: %v", err)
	}
	// The dangerous case still refuses: nothing bunny recorded, no marker.
	other := filepath.Join(root, "opt", "fd")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	if err := i.checkOwned(other, "fd"); err == nil {
		t.Error("an unrecorded, unmarked directory must be refused")
	}
}
