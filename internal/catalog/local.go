package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/manifest"
)

// Local serves manifests from a directory of the form
// `<root>/packages/<id>/manifest.yaml`.
type Local struct {
	root string
}

// NewLocal creates a Local loader rooted at the given packages directory.
func NewLocal(root string) *Local { return &Local{root: root} }

// Exists reports whether the root directory is present.
func (l *Local) Exists() bool {
	info, err := os.Stat(l.root)
	return err == nil && info.IsDir()
}

// Root returns the configured root path.
func (l *Local) Root() string { return l.root }

// List walks <root>/packages/<id>/manifest.yaml.
func (l *Local) List() ([]PackageInfo, error) {
	entries, err := os.ReadDir(filepath.Join(l.root, PackagesDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var pkgs []PackageInfo
	for _, pkg := range entries {
		if !pkg.IsDir() || strings.HasPrefix(pkg.Name(), ".") {
			continue
		}
		path := filepath.Join(l.root, PackagesDir, pkg.Name(), "manifest.yaml")
		f, err := os.Open(path)
		if err != nil {
			// A directory with no manifest.yaml simply isn't a package —
			// normal (helper dirs, VCS metadata), not worth a warning. Only
			// surface a manifest that genuinely can't be read.
			if !errors.Is(err, fs.ErrNotExist) {
				log.Warn("Skipping catalog entry: cannot open manifest", "path", path, "error", err)
			}
			continue
		}
		m, err := manifest.Parse(f)
		f.Close()
		if err != nil {
			log.Warn("Skipping catalog entry: invalid manifest", "path", path, "error", err)
			continue
		}
		pkgs = append(pkgs, InfoOf(m))
	}
	return pkgs, nil
}

// Lookup summarizes one package, reading only its manifest. Returns
// ErrNotFound (wrapped) when this catalog does not carry it; a checkout is
// either present or not, so it never reports ErrUnavailable.
func (l *Local) Lookup(id string) (PackageInfo, error) {
	m, err := l.Load(id)
	if err != nil {
		return PackageInfo{}, err
	}
	return InfoOf(m), nil
}

// Load returns a parsed manifest for the given ID. Returns ErrNotFound
// (wrapped) if the package isn't present in this catalog so Composite can
// fall through; parse errors propagate as themselves so a corrupt local
// override does not silently get replaced by remote content.
func (l *Local) Load(id string) (*manifest.Manifest, error) {
	path, err := l.manifestPath(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, err
	}
	defer f.Close()
	return manifest.Parse(f)
}

// LoadFile reads a sibling file in the package's directory. Once the package
// exists locally, a missing sibling file is a package error and must not
// fall through to remote content.
func (l *Local) LoadFile(id, relPath string) ([]byte, error) {
	if err := manifest.SafeRelPath(relPath); err != nil {
		return nil, err
	}
	pkgDir, err := l.packageDir(id)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(pkgDir, relPath))
}

// --- internal ---

// PackagesDir is the single directory every package lives in. The layout is
// deliberately flat: a package's kind and tags can change, and a semantic
// directory would turn every reclassification into a directory move.
const PackagesDir = "packages"

func (l *Local) packageDir(id string) (string, error) {
	if err := manifest.ValidateID(id); err != nil {
		return "", fmt.Errorf("invalid package id %q: %w", id, err)
	}
	dir := filepath.Join(l.root, PackagesDir, id)
	if _, err := os.Stat(filepath.Join(dir, "manifest.yaml")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: package %q", ErrNotFound, id)
		}
		return "", err
	}
	return dir, nil
}

func (l *Local) manifestPath(id string) (string, error) {
	dir, err := l.packageDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "manifest.yaml"), nil
}
