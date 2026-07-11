package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledPrefersSnapshotAndRejectsCorruption(t *testing.T) {
	dir := t.TempDir()
	localRoot := filepath.Join(dir, "catalog")
	writeTestManifest(t, filepath.Join(localRoot, "cli", "foo"), "foo", "Live")
	inner := NewLocal(localRoot)
	manifestPath := filepath.Join(dir, "installed", "foo.yaml")
	installed := NewInstalled(inner, func(string) string { return manifestPath })

	if err := os.MkdirAll(filepath.Dir(manifestPath), 0755); err != nil {
		t.Fatal(err)
	}
	snapshot := fmt.Sprintf(testManifestTemplate, "foo", "Snapshot")
	if err := os.WriteFile(manifestPath, []byte(snapshot), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := installed.Load("foo")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Snapshot" {
		t.Fatalf("name = %q, want snapshot", m.Name)
	}

	if err := os.WriteFile(manifestPath, []byte("not: [valid"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := installed.Load("foo"); err == nil {
		t.Fatal("expected corrupt snapshot error instead of live-catalog fallback")
	}
}
