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

func TestResolveTemplateURL(t *testing.T) {
	cases := []struct {
		name     string
		srcURL   string
		version  string
		resolved string
		want     string
		wantErr  bool
	}{
		{
			name:     "literal url replaced with resolved asset",
			srcURL:   "https://example.com/files/pinned-1.0.0.tar.gz",
			version:  "1.0.1",
			resolved: "https://example.com/files/pinned-1.0.1.tar.gz",
			want:     "https://example.com/files/pinned-1.0.1.tar.gz",
		},
		{
			name:     "literal url with no resolved asset falls back to itself",
			srcURL:   "https://example.com/files/pinned-1.0.0.tar.gz",
			version:  "1.0.1",
			resolved: "",
			want:     "https://example.com/files/pinned-1.0.0.tar.gz",
		},
		{
			name:     "templated url matching resolved asset is left untouched",
			srcURL:   "https://github.com/foo/bar/releases/download/{version}/bar.zip",
			version:  "2.0.0",
			resolved: "https://github.com/foo/bar/releases/download/2.0.0/bar.zip",
			want:     "",
		},
		{
			name:     "templated url with no resolved asset is left untouched",
			srcURL:   "https://github.com/foo/bar/releases/download/{version}/bar.zip",
			version:  "2.0.0",
			resolved: "",
			want:     "",
		},
		{
			// VisualVM-style case: the release tag is templated, but the
			// asset filename embeds an unrelated NetBeans build number.
			name:     "templated url diverging from resolved asset errors",
			srcURL:   "https://github.com/oracle/visualvm/releases/download/{version}/visualvm_221.zip",
			version:  "2.2.2",
			resolved: "https://github.com/oracle/visualvm/releases/download/2.2.2/visualvm_222.zip",
			wantErr:  true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveTemplateURL(c.srcURL, c.version, c.resolved)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
