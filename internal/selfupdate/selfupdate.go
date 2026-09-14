// Package selfupdate checks GitHub for a newer bunny release and replaces the
// running executable in place. It is deliberately independent of the
// catalog/installer machinery every other package goes through: bunny is not
// a catalog package, carries no state.json entry, and is the one file every
// shim already resolves to (shim.ReservedName) — there is nothing here to
// track in state, validate against the manifest schema, or wire as a shim.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cristatus/bunny/internal/checker"
	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/installer"
	"github.com/cristatus/bunny/internal/manifest"
)

// Repo is the GitHub repository bunny releases are published to.
const Repo = "cristatus/bunny"

// assetPattern matches the one release asset install.sh itself downloads:
// bunny_<version>_linux_amd64.tar.gz.
const assetPattern = `bunny_.*_linux_amd64\.tar\.gz$`

// Check reports whether a newer bunny release than currentVersion is
// published, without downloading anything. Reuses the same GitHub checker
// backend every catalog package's update: type: github goes through.
func Check(ctx context.Context, currentVersion string) (*checker.Result, error) {
	cfg := &manifest.UpdateConfig{Type: "github", Repo: Repo, Asset: assetPattern}
	return checker.Check(ctx, "bunny", currentVersion, "", cfg)
}

// Apply downloads the release r points at, verifies its upstream-published
// checksum, extracts the bunny binary, and atomically replaces binPath.
// Safe even while binPath is the currently-running executable:
// fsutil.CopyFile stages the replacement beside it and renames into place —
// the same same-filesystem-rename trick install.sh's own upgrade already
// relies on, since the process keeps its old inode mapped after the name is
// repointed.
func Apply(ctx context.Context, r *checker.Result, binPath string) error {
	if r.DownloadURL == "" {
		return fmt.Errorf("no downloadable release asset found")
	}
	if r.Hash == "" {
		return fmt.Errorf("upstream did not publish a checksum for the new release")
	}
	src := manifest.Source{URL: r.DownloadURL, Size: r.Size}
	switch r.HashAlgorithm {
	case "sha512":
		src.SHA512 = r.Hash
	default:
		src.SHA256 = r.Hash
	}

	scratch, err := os.MkdirTemp("", "bunny-self-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	archivePath, err := installer.NewDownloader().FetchContext(ctx, scratch, src)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}

	extracted, err := extractBinary(archivePath, scratch)
	if err != nil {
		return err
	}
	return fsutil.CopyFile(extracted, binPath, 0755)
}

// extractBinary pulls the "bunny" entry out of a gzipped tar archive into
// destDir, returning its path.
func extractBinary(archivePath, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read archive: %w", err)
		}
		if filepath.Clean(hdr.Name) != "bunny" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		out := filepath.Join(destDir, "bunny")
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(w, tr); err != nil {
			w.Close()
			return "", err
		}
		if err := w.Close(); err != nil {
			return "", err
		}
		return out, nil
	}
	return "", fmt.Errorf("archive has no bunny binary")
}
