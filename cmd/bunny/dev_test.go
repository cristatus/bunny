package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
)

func TestValidateCatalog(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, catalog.PackagesDir, "foo")
	if err := os.WriteFile(filepath.Join(root, "tags.yaml"), []byte("cli: command-line tool\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := "id: foo\n" +
		"name: Foo\n" +
		"version: \"1.0.0\"\n" +
		"tags: [cli]\n" +
		"sources:\n" +
		"  - url: https://example.com/foo.tar.gz\n" +
		"    sha256: " + strings.Repeat("a", 64) + "\n" +
		"bin:\n" +
		"  - name: foo\n" +
		"    path: \"{app}/foo\"\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "manifest.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(catalog.Index{Packages: map[string]catalog.IndexEntry{
		"foo": {Name: "Foo", Version: "1.0.0", Path: "packages/foo", Tags: []string{"cli"}, Kind: "cli"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), index, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCatalog(root); err != nil {
		t.Fatal(err)
	}

	index = []byte(strings.Replace(string(index), "1.0.0", "2.0.0", 1))
	if err := os.WriteFile(filepath.Join(root, "index.json"), index, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCatalog(root); err == nil {
		t.Fatal("expected index mismatch")
	}
}
