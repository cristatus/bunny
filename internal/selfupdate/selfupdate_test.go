package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/checker"
)

// buildArchive writes a gzipped tar at dir/name containing one regular file
// "bunny" with the given content, mirroring the real release layout
// (.goreleaser.yaml also ships README/CHANGELOG/LICENSE alongside it, so the
// archive here carries an unrelated entry too, to prove extraction picks the
// right one). Returns the archive path, its sha256, and its size.
func buildArchive(t *testing.T, dir, content string) (path, sha256hex string, size int64) {
	t.Helper()
	path = filepath.Join(dir, "bunny.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	write := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "hi")
	write("bunny", content)

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return path, hex.EncodeToString(sum[:]), int64(len(data))
}

func TestApplyReplacesRunningBinary(t *testing.T) {
	dir := t.TempDir()
	archivePath, sum, size := buildArchive(t, dir, "new bunny bytes")

	binPath := filepath.Join(dir, "bunny")
	if err := os.WriteFile(binPath, []byte("old bunny bytes"), 0755); err != nil {
		t.Fatal(err)
	}

	r := &checker.Result{
		LatestVersion: "0.7.0",
		DownloadURL:   "file://" + archivePath,
		Hash:          sum,
		HashAlgorithm: "sha256",
		Size:          size,
		HasUpdate:     true,
	}
	if err := Apply(context.Background(), r, binPath); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new bunny bytes" {
		t.Errorf("binary content = %q, want %q", got, "new bunny bytes")
	}
	if info, err := os.Stat(binPath); err != nil || info.Mode().Perm()&0100 == 0 {
		t.Errorf("replaced binary is not executable: %v %v", info, err)
	}
}

func TestApplyRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	archivePath, _, size := buildArchive(t, dir, "new bunny bytes")

	binPath := filepath.Join(dir, "bunny")
	if err := os.WriteFile(binPath, []byte("old bunny bytes"), 0755); err != nil {
		t.Fatal(err)
	}

	r := &checker.Result{
		DownloadURL:   "file://" + archivePath,
		Hash:          "0000000000000000000000000000000000000000000000000000000000000000", // deliberately wrong
		HashAlgorithm: "sha256",
		Size:          size,
	}
	if err := Apply(context.Background(), r, binPath); err == nil {
		t.Fatal("expected a checksum mismatch to be rejected")
	}
	got, _ := os.ReadFile(binPath)
	if string(got) != "old bunny bytes" {
		t.Error("a rejected download must not touch the existing binary")
	}
}

func TestApplyRequiresDownloadURLAndHash(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "bunny")
	os.WriteFile(binPath, []byte("old"), 0755)

	if err := Apply(context.Background(), &checker.Result{}, binPath); err == nil {
		t.Fatal("expected a missing DownloadURL to be rejected")
	}
	if err := Apply(context.Background(), &checker.Result{DownloadURL: "file:///dev/null"}, binPath); err == nil {
		t.Fatal("expected a missing checksum to be rejected")
	}
}
