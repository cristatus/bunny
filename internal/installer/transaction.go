package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cristatus/bunny/internal/desktop"
	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
)

// installTransaction records the files changed outside the staged package tree.
// The saved state bytes are the commit marker: state.Save replaces the file
// atomically, so recovery can distinguish a committed install from a rollback.
type installTransaction struct {
	ID, FinalDir, StoreDir, WorkDir, NewVersion string
	// StoreExisted marks a reinstall of the same version, whose earlier
	// tree waits at Backup until the install commits.
	StoreExisted bool
	Backup       string
	StateBefore  []byte
	// Link is the package's path as it was: the link to the previous
	// version, or missing for a first install.
	Link        fileRecord
	Manifest    fileRecord
	Shims       []fileRecord
	Integration []fileRecord
}

type fileRecord struct {
	Path, Kind, Target string
	Data               []byte
	Mode               os.FileMode
}

func captureFile(path string) (fileRecord, error) {
	r := fileRecord{Path: path, Kind: "missing"}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		r.Kind = "symlink"
		r.Target, err = os.Readlink(path)
		return r, err
	}
	if !info.Mode().IsRegular() {
		return r, fmt.Errorf("transaction path %s is not a file", path)
	}
	r.Kind = "regular"
	r.Mode = info.Mode().Perm()
	r.Data, err = os.ReadFile(path)
	return r, err
}

func (r fileRecord) restore() error {
	switch r.Kind {
	case "missing":
		if err := os.Remove(r.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	case "regular":
		return fsutil.WriteFile(r.Path, r.Data, r.Mode)
	case "symlink":
		if err := os.Remove(r.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(r.Path), 0755); err != nil {
			return err
		}
		return os.Symlink(r.Target, r.Path)
	default:
		return fmt.Errorf("unknown transaction file type %q", r.Kind)
	}
	return nil
}

func (j *installTransaction) save(p *paths.Paths) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return fsutil.WriteFile(p.TransactionFile(), data, 0600)
}

func startInstallTransaction(i *Installer, id, finalDir, workDir, newVersion string, shimNames []string) (*installTransaction, error) {
	stateBefore, err := os.ReadFile(i.Paths.StateFile())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	manifestBefore, err := captureFile(i.Paths.ManifestFile(id))
	if err != nil {
		return nil, err
	}
	link, err := captureFile(finalDir)
	if err != nil {
		return nil, err
	}
	j := &installTransaction{ID: id, FinalDir: finalDir, StoreDir: i.Paths.VersionDir(id, newVersion),
		Backup: filepath.Join(i.Paths.StagingDir(id), "replaced"), WorkDir: workDir, NewVersion: newVersion,
		StateBefore: stateBefore, Link: link, Manifest: manifestBefore}
	if _, err := os.Lstat(j.StoreDir); err == nil {
		j.StoreExisted = true
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, name := range shimNames {
		record, err := captureFile(i.Paths.Shim(name))
		if err != nil {
			return nil, err
		}
		j.Shims = append(j.Shims, record)
	}
	if err := j.save(i.Paths); err != nil {
		return nil, err
	}
	return j, nil
}

// captureIntegration runs after placement, when directory man entries can be
// enumerated, and before any shared integration file is changed.
func (j *installTransaction) captureIntegration(i *Installer, old, next *manifest.Manifest) error {
	pathsToSave := map[string]bool{}
	for _, path := range i.ownedFiles(j.ID, old) {
		pathsToSave[path] = true
	}
	for _, m := range []*manifest.Manifest{old, next} {
		if m == nil {
			continue
		}
		for _, entry := range m.Desktop {
			pathsToSave[filepath.Join(i.Paths.Desktop(), entry.ID)] = true
		}
	}
	if next != nil {
		vars := i.Paths.VarsAt(j.ID, next.Version, j.FinalDir)
		for path := range desktop.ManagedFiles(i.Paths, next, vars) {
			pathsToSave[path] = true
		}
		for _, raw := range next.Man {
			src := manifest.Expand(raw, vars)
			if manifest.ManSection(src) != "" {
				continue
			}
			entries, err := os.ReadDir(src)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}
				if section := manifest.ManSection(entry.Name()); section != "" {
					pathsToSave[filepath.Join(i.Paths.ManPages(), "man"+section, entry.Name())] = true
				}
			}
		}
	}
	names := make([]string, 0, len(pathsToSave))
	for path := range pathsToSave {
		names = append(names, path)
	}
	sort.Strings(names)
	for _, path := range names {
		record, err := captureFile(path)
		if err != nil {
			return err
		}
		j.Integration = append(j.Integration, record)
	}
	return j.save(i.Paths)
}

// isNewTree reports whether path holds this transaction's package and version.
func (j *installTransaction) isNewTree(path string) bool {
	marker, err := readPackageMarker(path)
	return err == nil && marker.ID == j.ID && marker.Version == j.NewVersion
}

// isOwnTree reports whether path holds a tree of this transaction's package.
func (j *installTransaction) isOwnTree(path string) bool {
	marker, err := readPackageMarker(path)
	return err == nil && marker.ID == j.ID
}

// RecoverPendingInstall must run while the mutation lock is held. It either
// finishes cleanup for committed state or restores every pre-commit artifact.
func RecoverPendingInstall(p *paths.Paths) error {
	data, err := os.ReadFile(p.TransactionFile())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var j installTransaction
	if err := json.Unmarshal(data, &j); err != nil {
		return fmt.Errorf("read install transaction: %w", err)
	}
	if err := manifest.ValidateID(j.ID); err != nil {
		return err
	}
	current, err := os.ReadFile(p.StateFile())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	backup := j.Backup
	if !bytes.Equal(current, j.StateBefore) {
		// State is the durable commit. What it replaced may still await deletion.
		if !j.isNewTree(j.StoreDir) {
			return fmt.Errorf("committed transaction destination %s is inconsistent", j.StoreDir)
		}
		if j.isOwnTree(backup) {
			if err := removeOwnedTree(backup); err != nil {
				return err
			}
		}
	} else {
		if err := j.Link.restore(); err != nil {
			return err
		}
		switch {
		case j.isOwnTree(backup):
			if j.isNewTree(j.StoreDir) {
				if err := removeOwnedTree(j.StoreDir); err != nil {
					return err
				}
			}
			if err := os.Rename(backup, j.StoreDir); err != nil {
				return err
			}
		case !j.StoreExisted && j.isNewTree(j.StoreDir):
			if err := removeOwnedTree(j.StoreDir); err != nil {
				return err
			}
		}
		for _, record := range j.Integration {
			if err := record.restore(); err != nil {
				return err
			}
		}
		for _, record := range j.Shims {
			if err := record.restore(); err != nil {
				return err
			}
		}
		if err := j.Manifest.restore(); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(j.WorkDir); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(j.WorkDir)) // the package's staging dir, once empty
	if err := os.Remove(p.TransactionFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
