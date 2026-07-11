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
// `<root>/<category>/<id>/manifest.yaml`.
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

// List walks <root>/<category>/<id>/manifest.yaml.
func (l *Local) List() ([]PackageInfo, error) {
	categories, err := os.ReadDir(l.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var pkgs []PackageInfo
	for _, cat := range categories {
		// Skip non-dirs and hidden dirs (.git, .github, ...) — never catalog content.
		if !cat.IsDir() || strings.HasPrefix(cat.Name(), ".") {
			continue
		}
		catDir := filepath.Join(l.root, cat.Name())
		entries, err := os.ReadDir(catDir)
		if err != nil {
			// One unreadable category must not sink the whole listing.
			log.Warn("Skipping unreadable catalog category", "category", cat.Name(), "error", err)
			continue
		}
		for _, pkg := range entries {
			if !pkg.IsDir() || strings.HasPrefix(pkg.Name(), ".") {
				continue
			}
			path := filepath.Join(catDir, pkg.Name(), "manifest.yaml")
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
			pkgs = append(pkgs, PackageInfo{
				ID:          m.ID,
				Category:    cat.Name(),
				Name:        m.Name,
				Description: m.Description,
				Version:     m.Version,
			})
		}
	}
	return pkgs, nil
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

// Category returns the category folder a package lives in. Returns
// ErrNotFound when the catalog root is missing or no manifest matches the
// id, so callers can distinguish "not in this catalog" from real I/O errors.
func (l *Local) Category(id string) (string, error) {
	if err := manifest.ValidateID(id); err != nil {
		return "", fmt.Errorf("invalid package id %q: %w", id, err)
	}
	categories, err := os.ReadDir(l.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: catalog root %s", ErrNotFound, l.root)
		}
		return "", err
	}
	for _, cat := range categories {
		if !cat.IsDir() {
			continue
		}
		path := filepath.Join(l.root, cat.Name(), id, "manifest.yaml")
		if _, err := os.Stat(path); err == nil {
			return cat.Name(), nil
		}
	}
	return "", fmt.Errorf("%w: package %q", ErrNotFound, id)
}

// --- internal ---

func (l *Local) packageDir(id string) (string, error) {
	cat, err := l.Category(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(l.root, cat, id), nil
}

func (l *Local) manifestPath(id string) (string, error) {
	dir, err := l.packageDir(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "manifest.yaml"), nil
}
