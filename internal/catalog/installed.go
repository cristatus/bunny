package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/cristatus/bunny/internal/manifest"
)

// Installed overlays install-time manifest snapshots on a live catalog.
// Runtime, uninstall, and repair paths therefore share exactly one rule for
// resolving the manifest that describes files already present on disk.
type Installed struct {
	inner        Loader
	manifestFile func(string) string
}

func NewInstalled(inner Loader, manifestFile func(string) string) *Installed {
	return &Installed{inner: inner, manifestFile: manifestFile}
}

func (i *Installed) Load(id string) (*manifest.Manifest, error) {
	data, err := os.ReadFile(i.manifestFile(id))
	if err == nil {
		m, err := manifest.ParseBytes(data)
		if err != nil {
			return nil, fmt.Errorf("parse installed manifest for %s: %w", id, err)
		}
		return m, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read installed manifest for %s: %w", id, err)
	}
	if i.inner == nil {
		return nil, fmt.Errorf("%w: package %q has no installed snapshot", ErrNotFound, id)
	}
	return i.inner.Load(id)
}

func (i *Installed) List() ([]PackageInfo, error) {
	if i.inner == nil {
		return nil, fmt.Errorf("%w: no live catalog configured", ErrNotFound)
	}
	return i.inner.List()
}

func (i *Installed) LoadFile(id, rel string) ([]byte, error) {
	if i.inner == nil {
		return nil, fmt.Errorf("%w: no live catalog configured", ErrNotFound)
	}
	return i.inner.LoadFile(id, rel)
}
