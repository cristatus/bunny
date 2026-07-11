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
	if err := Remove(binDir, []string{"x", "missing"}); err != nil {
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
	if err := Remove(binDir, []string{ReservedName}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(bunny); err != nil || string(data) != "binary" {
		t.Fatalf("bunny executable changed: %q, %v", data, err)
	}
}

func TestRemoveRejectsUnsafeName(t *testing.T) {
	if err := Remove(t.TempDir(), []string{"../escape"}); err == nil {
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
