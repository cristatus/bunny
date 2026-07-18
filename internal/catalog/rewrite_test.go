package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
)

func TestRewriteManifestVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	original := `id: vscode
name: Visual Studio Code
version: "1.118.0"
category: editor

sources:
  - url: "https://example.com/{version}/file.tar.gz"
    sha256: "OLDSHA"
    size: 100

bin:
  - { name: code, path: "{app}/bin/code" }
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	newSHA := strings.Repeat("b", 64)
	err := RewriteManifestVersion(path, "1.119.0", SourceUpdate{
		SHA256: newSHA,
		Size:   2048,
	})
	if err != nil {
		t.Fatalf("RewriteManifestVersion: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	if !strings.Contains(out, `version: "1.119.0"`) {
		t.Errorf("expected updated version in output, got:\n%s", out)
	}
	if !strings.Contains(out, `sha256: "`+newSHA+`"`) {
		t.Errorf("expected updated sha256 in output, got:\n%s", out)
	}
	if !strings.Contains(out, `size: 2048`) {
		t.Errorf("expected updated size in output, got:\n%s", out)
	}
	if strings.Contains(out, "OLDSHA") {
		t.Error("old sha256 should have been replaced")
	}
	// Re-parse to confirm the result is still a valid manifest.
	if _, err := manifest.ParseBytes(got); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
}

func TestRewriteManifestVersion_PreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	// Manifest with multiple sources and other fields. The rewriter must only
	// touch root.version and sources[0].sha256/size — every other byte should
	// round-trip.
	original := `id: example
name: Example
version: "1.0.0"
category: editor
homepage: https://example.com/

sources:
  - url: "https://example.com/{version}/a.tar.gz"
    sha256: "AAAA"
    size: 1
  - url: "https://example.com/{version}/b.tar.gz"
    sha256: "BBBB"
    size: 2

bin:
  - { name: example, path: "{app}/bin/example" }

env:
  EXAMPLE_HOME: "{data}"
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	if err := RewriteManifestVersion(path, "1.0.1", SourceUpdate{SHA256: "AAAA-NEW", Size: 11}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	out := string(got)
	for _, want := range []string{
		`version: "1.0.1"`,
		`sha256: "AAAA-NEW"`,
		`size: 11`,
		// Second source untouched:
		`sha256: "BBBB"`,
		`size: 2`,
		`homepage: https://example.com/`,
		`EXAMPLE_HOME: "{data}"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestRewriteManifestVersion_PinnedURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	original := `id: pinned
name: Pinned
version: "1.0.0"

sources:
  - url: "https://example.com/files/pinned-1.0.0.tar.gz"
    sha256: "OLD"

bin:
  - { name: pinned, path: "{app}/bin/pinned" }
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	err := RewriteManifestVersion(path, "1.0.1", SourceUpdate{
		URL:    "https://example.com/files/pinned-1.0.1.tar.gz",
		SHA256: "NEW",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "pinned-1.0.1.tar.gz") {
		t.Errorf("URL not updated:\n%s", got)
	}
	if strings.Contains(string(got), "pinned-1.0.0.tar.gz") {
		t.Errorf("old URL still present:\n%s", got)
	}
}

func TestRewriteManifestVersion_SHA512Manifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	original := `id: nodelike
name: NodeLike
version: "1.0.0"

sources:
  - url: "https://example.com/{version}/file.tar.gz"
    sha512: "OLD512"

bin:
  - { name: nodelike, path: "{app}/bin/nodelike" }
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	// Only SHA512 set; SHA256 left empty in the update.
	err := RewriteManifestVersion(path, "1.0.1", SourceUpdate{SHA512: "NEW512"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `sha512: "NEW512"`) {
		t.Errorf("sha512 not updated:\n%s", got)
	}
	if strings.Contains(string(got), "OLD512") {
		t.Errorf("old sha512 still present:\n%s", got)
	}
}

func TestRewriteSource_LeavesVersionAndSiblings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	original := `id: example
name: Example
version: "1.0.0"

sources:
  - url: "https://example.com/primary-1.0.0.tar.gz"
    sha256: "AAA"
    size: 100
  - url: "https://example.com/plugin-2.0.0.jar"
    sha256: "BBB"
    size: 200

bin:
  - { name: example, path: "{app}/bin/example" }
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	err := RewriteSource(path, 1, SourceUpdate{
		URL:    "https://example.com/plugin-2.1.0.jar",
		SHA256: "CCC",
		Size:   222,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	out := string(got)
	for _, want := range []string{
		`version: "1.0.0"`, // unchanged
		`url: "https://example.com/primary-1.0.0.tar.gz"`, // primary unchanged
		`sha256: "AAA"`, // primary hash unchanged
		`url: "https://example.com/plugin-2.1.0.jar"`,
		`sha256: "CCC"`,
		`size: 222`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
	if strings.Contains(out, `sha256: "BBB"`) {
		t.Error("old secondary sha256 should have been replaced")
	}
}

func TestRewriteSource_OutOfRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	original := `id: x
name: X
version: "1"
sources:
  - { url: "https://example.com/a.tar.gz", sha256: "A" }
bin:
  - { name: x, path: "{app}/x" }
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RewriteSource(path, 5, SourceUpdate{SHA256: "Z"}); err == nil {
		t.Error("expected out-of-range error")
	}
}

func TestRewriteIndexEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	original := `{
  "version": 1,
  "updated": "2026-01-01T00:00:00Z",
  "packages": {
    "vscode": {
      "name": "Visual Studio Code",
      "version": "1.118.0",
      "category": "editor",
      "description": "Code editor from Microsoft"
    },
    "node-22": {
      "name": "Node.js 22 LTS",
      "version": "22.22.1",
      "category": "sdk"
    }
  }
}
`
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	err := RewriteIndexEntry(path, "vscode", IndexEntry{
		Name:        "Visual Studio Code",
		Version:     "1.119.0",
		Category:    "editor",
		Description: "Code editor from Microsoft",
		Provides:    "editor",
		Requires:    []string{"jdk>=17"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var idx map[string]any
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	pkgs := idx["packages"].(map[string]any)
	vscode := pkgs["vscode"].(map[string]any)
	if vscode["version"] != "1.119.0" {
		t.Errorf("vscode.version = %v, want 1.119.0", vscode["version"])
	}
	if vscode["provides"] != "editor" {
		t.Errorf("vscode.provides = %v, want editor", vscode["provides"])
	}
	requires, _ := vscode["requires"].([]any)
	if len(requires) != 1 || requires[0] != "jdk>=17" {
		t.Errorf("vscode.requires = %v, want [jdk>=17]", vscode["requires"])
	}
	// Other packages untouched:
	node := pkgs["node-22"].(map[string]any)
	if node["version"] != "22.22.1" {
		t.Errorf("node-22.version = %v, want 22.22.1 (untouched)", node["version"])
	}
	// updated timestamp got bumped:
	if idx["updated"] == "2026-01-01T00:00:00Z" {
		t.Error("updated timestamp should have been bumped")
	}
}

// TestCommit_AppliesBothFiles drives the manifest+index rewrite as a single
// batch, the way 'bunny dev update' does for one package.
func TestCommit_AppliesBothFiles(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	indexPath := filepath.Join(dir, "index.json")
	if err := os.WriteFile(manifestPath, []byte(`id: rg
name: ripgrep
version: "13.0.0"
sources:
  - url: "https://example.com/{version}.tar.gz"
    sha256: "OLD"
bin:
  - { name: rg, path: "{app}/rg" }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte(`{
  "version": 1,
  "packages": {
    "rg": {"name": "ripgrep", "version": "13.0.0", "category": "cli"}
  }
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	mw, err := PrepareManifestVersion(manifestPath, "14.0.0", SourceUpdate{SHA256: "NEW"})
	if err != nil {
		t.Fatal(err)
	}
	iw, err := PrepareIndexEntry(indexPath, "rg", IndexEntry{
		Name: "ripgrep", Version: "14.0.0", Category: "cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit([]PreparedWrite{mw, iw}); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	mb, _ := os.ReadFile(manifestPath)
	if !strings.Contains(string(mb), `version: "14.0.0"`) || !strings.Contains(string(mb), `sha256: "NEW"`) {
		t.Errorf("manifest not updated after Commit:\n%s", mb)
	}
	ib, _ := os.ReadFile(indexPath)
	var idx map[string]any
	if err := json.Unmarshal(ib, &idx); err != nil {
		t.Fatal(err)
	}
	rg := idx["packages"].(map[string]any)["rg"].(map[string]any)
	if rg["version"] != "14.0.0" {
		t.Errorf("index entry not updated: %v", rg)
	}

	// No leftover tempfiles from the staging step.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover staging file: %s", e.Name())
		}
	}
}

// TestCommit_StagingFailureLeavesTargetsUntouched verifies that if any
// PreparedWrite can't be staged (e.g., its directory disappears), no targets
// are mutated and no tempfiles are left behind.
func TestCommit_StagingFailureLeavesTargetsUntouched(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	original := `id: rg
name: ripgrep
version: "13.0.0"
sources:
  - url: "https://example.com/{version}.tar.gz"
    sha256: "OLD"
bin:
  - { name: rg, path: "{app}/rg" }
`
	if err := os.WriteFile(manifestPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	mw, err := PrepareManifestVersion(manifestPath, "14.0.0", SourceUpdate{SHA256: "NEW"})
	if err != nil {
		t.Fatal(err)
	}
	// Second PreparedWrite targets a directory that doesn't exist — staging
	// must fail before any rename happens.
	bad := PreparedWrite{
		path: filepath.Join(dir, "nope", "index.json"),
		data: []byte("{}"),
		perm: 0644,
	}
	if err := Commit([]PreparedWrite{mw, bad}); err == nil {
		t.Fatal("expected Commit to fail when staging cannot create tempfile")
	}

	got, _ := os.ReadFile(manifestPath)
	if string(got) != original {
		t.Errorf("manifest mutated despite failed batch:\n%s", got)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover staging file: %s", e.Name())
		}
	}
}
