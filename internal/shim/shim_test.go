package shim

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
)

func TestInstallCreatesSymlinks(t *testing.T) {
	binDir := t.TempDir()
	bunny := filepath.Join(binDir, "bunny")
	os.WriteFile(bunny, []byte("#!/bin/sh\n"), 0755)

	if err := Install(binDir, []string{"node", "npm"}, bunny); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node", "npm"} {
		path := filepath.Join(binDir, name)
		target, err := os.Readlink(path)
		if err != nil {
			t.Errorf("readlink %s: %v", path, err)
		}
		if target != bunny {
			t.Errorf("%s → %s, want %s", name, target, bunny)
		}
	}
}

func TestInstallRefusesExistingRegularFile(t *testing.T) {
	binDir := t.TempDir()
	stale := filepath.Join(binDir, "node")
	os.WriteFile(stale, []byte("#!/bin/sh\necho old\n"), 0755)

	bunny := filepath.Join(binDir, "bunny")
	os.WriteFile(bunny, []byte("ok"), 0755)

	if err := Install(binDir, []string{"node"}, bunny); err == nil {
		t.Fatal("expected regular-file conflict")
	}
	data, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "echo old") {
		t.Fatal("existing file was modified")
	}
}

func TestRemoveDeletesShims(t *testing.T) {
	binDir := t.TempDir()
	bunny := filepath.Join(binDir, "bunny")
	os.WriteFile(bunny, []byte{}, 0755)
	os.Symlink(bunny, filepath.Join(binDir, "x"))
	if err := Remove(binDir, []string{"x", "missing"}, bunny); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "x")); !os.IsNotExist(err) {
		t.Error("x not removed")
	}
}

func TestInstallAndRemoveProtectBunnyExecutable(t *testing.T) {
	binDir := t.TempDir()
	bunny := filepath.Join(binDir, ReservedName)
	if err := os.WriteFile(bunny, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Install(binDir, []string{ReservedName}, bunny); err == nil {
		t.Fatal("expected reserved-name error")
	}
	if err := Remove(binDir, []string{ReservedName}, bunny); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(bunny); err != nil || string(data) != "binary" {
		t.Fatalf("bunny executable changed: %q, %v", data, err)
	}
}

func TestRemoveRejectsUnsafeName(t *testing.T) {
	if err := Remove(t.TempDir(), []string{"../escape"}, "bunny"); err == nil {
		t.Fatal("expected unsafe shim name to be rejected")
	}
}

// --- Resolver ---

type stubState struct {
	owner     map[string]string
	installed map[string]bool
}

func (s *stubState) CommandOwner(name string) (string, bool) {
	v, ok := s.owner[name]
	return v, ok
}
func (s *stubState) IsInstalled(id string) bool { return s.installed[id] }

type stubCatalog struct {
	manifests map[string]*manifest.Manifest
}

func (c *stubCatalog) Load(id string) (*manifest.Manifest, error) {
	m, ok := c.manifests[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return m, nil
}

func TestResolverNoCommandOwner(t *testing.T) {
	r := &Resolver{State: &stubState{owner: map[string]string{}}, Catalog: &stubCatalog{}}
	if _, err := r.Resolve("node", "/tmp"); err == nil {
		t.Error("expected error for unknown shim")
	}
}

func TestResolverDefaultWhenNoProvides(t *testing.T) {
	r := &Resolver{
		State: &stubState{
			owner:     map[string]string{"code": "vscode"},
			installed: map[string]bool{"vscode": true},
		},
		Catalog: &stubCatalog{
			manifests: map[string]*manifest.Manifest{
				"vscode": {ID: "vscode", Version: "1.0", Provides: ""},
			},
		},
	}
	got, err := r.Resolve("code", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if got.PackageID != "vscode" {
		t.Errorf("got %q", got.PackageID)
	}
}

// The owner's manifest is the command being resolved: a.run reloads it and
// cannot proceed without it, so Resolve fails clearly rather than pretending to
// degrade.
func TestResolverErrorsWhenManifestUnavailable(t *testing.T) {
	r := &Resolver{
		State: &stubState{
			owner:     map[string]string{"node": "node-24"},
			installed: map[string]bool{"node-24": true},
		},
		Catalog: &stubCatalog{}, // no manifests → Load errors
	}
	if _, err := r.Resolve("node", "/tmp"); err == nil {
		t.Fatal("expected an error when the command's manifest can't be loaded")
	}
}

func TestResolverDotBunnyVersionPicksPinned(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ProjectVersionFile), []byte("node 22\n"), 0644)

	r := &Resolver{
		State: &stubState{
			owner:     map[string]string{"node": "node-24"},
			installed: map[string]bool{"node-22": true, "node-24": true},
		},
		Catalog: &stubCatalog{
			manifests: map[string]*manifest.Manifest{
				"node-24": {ID: "node-24", Provides: "node"},
			},
		},
	}
	got, err := r.Resolve("node", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.PackageID != "node-22" {
		t.Errorf(".bunny-version pinned 22 but got %q", got.PackageID)
	}
}

func TestResolverErrorsWhenPinnedNotInstalled(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ProjectVersionFile), []byte("node 23\n"), 0644)

	r := &Resolver{
		State: &stubState{
			owner:     map[string]string{"node": "node-24"},
			installed: map[string]bool{"node-24": true}, // 23 not installed
		},
		Catalog: &stubCatalog{
			manifests: map[string]*manifest.Manifest{
				"node-24": {ID: "node-24", Provides: "node"},
			},
		},
	}
	_, err := r.Resolve("node", dir)
	if err == nil {
		t.Fatal("expected error when pinned version isn't installed, got nil")
	}
	for _, want := range []string{"node 23", "node-23", "bunny install node-23"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// The shim directory may be shared with other tools (pipx, cargo, hand-written
// links), so a symlink Bunny did not create must never be replaced or removed.
func TestInstallAndRemoveRefuseForeignSymlink(t *testing.T) {
	binDir := t.TempDir()
	bunny := filepath.Join(binDir, ReservedName)
	if err := os.WriteFile(bunny, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	foreignTarget := filepath.Join(binDir, "other-tool")
	if err := os.WriteFile(foreignTarget, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(binDir, "node")
	if err := os.Symlink(foreignTarget, foreign); err != nil {
		t.Fatal(err)
	}

	if err := Install(binDir, []string{"node"}, bunny); err == nil {
		t.Fatal("expected Install to refuse a foreign symlink")
	}
	if err := Remove(binDir, []string{"node"}, bunny); err == nil {
		t.Fatal("expected Remove to refuse a foreign symlink")
	}
	target, err := os.Readlink(foreign)
	if err != nil || target != foreignTarget {
		t.Fatalf("foreign symlink changed: %q, %v", target, err)
	}
}

// A shim left dangling by a moved bunny binary is still ours to repair.
func TestInstallRepairsDanglingShim(t *testing.T) {
	binDir := t.TempDir()
	bunny := filepath.Join(binDir, ReservedName)
	if err := os.WriteFile(bunny, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(binDir, "old", "bunny"), filepath.Join(binDir, "node")); err != nil {
		t.Fatal(err)
	}
	if err := Install(binDir, []string{"node"}, bunny); err != nil {
		t.Fatalf("dangling bunny shim should be repairable: %v", err)
	}
	target, err := os.Readlink(filepath.Join(binDir, "node"))
	if err != nil || target != bunny {
		t.Fatalf("shim not repointed: %q, %v", target, err)
	}
}

// A dangling link into some other tool is not ours, even though it is broken.
func TestRemoveRefusesForeignDanglingLink(t *testing.T) {
	binDir := t.TempDir()
	bunny := filepath.Join(binDir, ReservedName)
	if err := os.WriteFile(bunny, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(binDir, "gone", "black"), filepath.Join(binDir, "black")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(binDir, []string{"black"}, bunny); err == nil {
		t.Fatal("expected Remove to refuse a foreign dangling link")
	}
	if _, err := os.Lstat(filepath.Join(binDir, "black")); err != nil {
		t.Error("foreign dangling link was removed")
	}
}

// A link to some other tool's binary that happens to be named "bunny" is not
// ours. The name is not proof of ownership; where it points is.
func TestInstallAndRemoveRefuseForeignBunnyNamedBinary(t *testing.T) {
	binDir := t.TempDir()
	bunny := filepath.Join(binDir, ReservedName)
	if err := os.WriteFile(bunny, []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	// An unrelated project's entry point, also called "bunny", outside binDir.
	otherDir := t.TempDir()
	other := filepath.Join(otherDir, ReservedName)
	if err := os.WriteFile(other, []byte("someone else's binary"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "node")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}

	if err := Install(binDir, []string{"node"}, bunny); err == nil {
		t.Fatal("Install claimed a link to an unrelated binary named bunny")
	}
	if err := Remove(binDir, []string{"node"}, bunny); err == nil {
		t.Fatal("Remove claimed a link to an unrelated binary named bunny")
	}
	if target, err := os.Readlink(link); err != nil || target != other {
		t.Fatalf("foreign link changed: %q, %v", target, err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("foreign binary disturbed: %v", err)
	}
}

// Shims stay ours when a different bunny binary is the one running, which is
// the case during `make install`, an upgrade, or a one-off ./bin/bunny.
func TestShimsStayOwnedWhenRunFromAnotherBinary(t *testing.T) {
	binDir := t.TempDir()
	installed := filepath.Join(binDir, ReservedName)
	if err := os.WriteFile(installed, []byte("installed"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Install(binDir, []string{"node"}, installed); err != nil {
		t.Fatal(err)
	}
	// A freshly built binary elsewhere, run against shims naming the installed one.
	running := filepath.Join(t.TempDir(), ReservedName)
	if err := os.WriteFile(running, []byte("freshly built"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := Install(binDir, []string{"node"}, running); err != nil {
		t.Fatalf("reinstalling shims from another binary should work: %v", err)
	}
	if err := Remove(binDir, []string{"node"}, running); err != nil {
		t.Fatalf("removing shims from another binary should work: %v", err)
	}
}
