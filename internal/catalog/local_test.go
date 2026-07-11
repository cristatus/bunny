package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testManifestTemplate = `id: %s
name: %s
version: "1.0.0"
sources:
  - url: https://example.com/x.tar.gz
    sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
bin:
  - name: x
    path: "{app}/x"
`

func writeTestManifest(t *testing.T, dir, id, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf(testManifestTemplate, id, name)
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalListAndLoad(t *testing.T) {
	root := t.TempDir()
	writeTestManifest(t, filepath.Join(root, "cli", "rg"), "rg", "ripgrep")
	writeTestManifest(t, filepath.Join(root, "editor", "code"), "code", "VS Code")

	l := NewLocal(root)
	if !l.Exists() {
		t.Error("Exists should be true")
	}

	pkgs, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d: %v", len(pkgs), pkgs)
	}

	m, err := l.Load("rg")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "ripgrep" {
		t.Errorf("name=%q", m.Name)
	}

	cat, err := l.Category("code")
	if err != nil {
		t.Fatal(err)
	}
	if cat != "editor" {
		t.Errorf("cat=%q", cat)
	}
}

func TestLocalListSkipsBadEntries(t *testing.T) {
	root := t.TempDir()
	writeTestManifest(t, filepath.Join(root, "cli", "rg"), "rg", "ripgrep")
	// A stray package dir with no manifest.yaml.
	if err := os.MkdirAll(filepath.Join(root, "cli", "stray"), 0755); err != nil {
		t.Fatal(err)
	}
	// Hidden dirs (VCS metadata, tooling) are not catalog content and must be
	// skipped wholesale, not descended into.
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0755); err != nil {
		t.Fatal(err)
	}
	// A package dir with an invalid manifest.
	badDir := filepath.Join(root, "editor", "broken")
	if err := os.MkdirAll(badDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "manifest.yaml"), []byte("not: [valid"), 0644); err != nil {
		t.Fatal(err)
	}

	pkgs, err := NewLocal(root).List()
	if err != nil {
		t.Fatalf("List should skip bad entries, not fail the whole listing: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].ID != "rg" {
		t.Fatalf("expected only rg to survive, got %v", pkgs)
	}
}

func TestLocalLoadFile(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "cli", "foo")
	writeTestManifest(t, pkgDir, "foo", "Foo")
	if err := os.WriteFile(filepath.Join(pkgDir, "build.sh"), []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
		t.Fatal(err)
	}

	l := NewLocal(root)
	data, err := l.LoadFile("foo", "build.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "echo hi") {
		t.Errorf("got %q", data)
	}
	if _, err := l.LoadFile("foo", "../../etc/passwd"); err == nil {
		t.Error("expected traversal to be rejected")
	}
	if _, err := l.LoadFile("foo", "/etc/passwd"); err == nil {
		t.Error("expected absolute path to be rejected")
	}
	if _, err := l.LoadFile("../foo", "build.sh"); err == nil {
		t.Error("expected invalid package id to be rejected")
	}
}

func TestLocalMissingExists(t *testing.T) {
	l := NewLocal(filepath.Join(t.TempDir(), "missing"))
	if l.Exists() {
		t.Error("Exists should be false for missing dir")
	}
	pkgs, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 0 {
		t.Errorf("expected empty list, got %v", pkgs)
	}
}
