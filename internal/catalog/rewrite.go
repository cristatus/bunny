package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// SourceUpdate is the set of source-entry fields to overwrite during a manifest
// rewrite. Empty/zero values leave the existing field untouched.
type SourceUpdate struct {
	URL    string
	SHA256 string
	SHA512 string
	Size   int64
}

// PreparedWrite is a pending write whose target bytes have been computed but
// not yet flushed to disk. Use Commit to atomically apply a batch.
type PreparedWrite struct {
	path string
	data []byte
	perm os.FileMode
}

// Path returns the destination file path. Useful for error reporting.
func (p PreparedWrite) Path() string { return p.path }

// PrepareManifestVersion computes the new manifest bytes after bumping
// `version:` and `sources[0]`'s URL / hash / size, preserving comments, key
// order, and quoting style. The file on disk is not touched.
func PrepareManifestVersion(path, newVersion string, src SourceUpdate) (PreparedWrite, error) {
	return prepareManifest(path, func(root *yaml.Node) error {
		v := mappingValue(root, "version")
		if v == nil {
			return fmt.Errorf("missing 'version' field")
		}
		v.Value = newVersion
		return applySourceAt(root, 0, src)
	})
}

// PrepareSource computes new manifest bytes with one source entry's URL /
// hash / size overwritten by index. `version:` and other sources are
// untouched.
func PrepareSource(path string, index int, src SourceUpdate) (PreparedWrite, error) {
	return prepareManifest(path, func(root *yaml.Node) error {
		return applySourceAt(root, index, src)
	})
}

// PrepareIndexEntry computes new index.json bytes with one package entry
// updated (added if missing). The top-level "updated" timestamp is bumped to
// now. The file on disk is not touched.
func PrepareIndexEntry(indexPath, id string, entry IndexEntry) (PreparedWrite, error) {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return PreparedWrite{}, err
	}
	var idx map[string]any
	if err := json.Unmarshal(data, &idx); err != nil {
		return PreparedWrite{}, fmt.Errorf("parse %s: %w", indexPath, err)
	}
	pkgs, ok := idx["packages"].(map[string]any)
	if !ok {
		pkgs = map[string]any{}
		idx["packages"] = pkgs
	}
	pkg, _ := pkgs[id].(map[string]any)
	if pkg == nil {
		pkg = map[string]any{}
	}
	pkg["name"] = entry.Name
	pkg["version"] = entry.Version
	pkg["category"] = entry.Category
	if entry.Description != "" {
		pkg["description"] = entry.Description
	}
	pkgs[id] = pkg
	idx["updated"] = time.Now().UTC().Format(time.RFC3339)

	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return PreparedWrite{}, err
	}
	out = append(out, '\n')
	return PreparedWrite{path: indexPath, data: out, perm: 0644}, nil
}

// Commit atomically writes a batch of PreparedWrites. Each entry is staged to
// a tempfile in the same directory as its target, fsynced, and only then
// renamed into place. If any staging step fails, no targets are touched.
//
// Multi-file atomicity on POSIX has a small unavoidable window: after the
// first successful rename, a crash before the second rename leaves a
// half-applied batch. We minimize that window by doing all renames back to
// back as the final step. For larger consistency guarantees (e.g. cross-
// filesystem), the catalog would need a journal.
func Commit(writes []PreparedWrite) error {
	type staged struct {
		tmp, target string
	}
	var stagedList []staged
	cleanup := func() {
		for _, s := range stagedList {
			os.Remove(s.tmp)
		}
	}
	for _, w := range writes {
		tmp, err := stageTempFile(w.path, w.data, w.perm)
		if err != nil {
			cleanup()
			return err
		}
		stagedList = append(stagedList, staged{tmp: tmp, target: w.path})
	}
	for i, s := range stagedList {
		if err := os.Rename(s.tmp, s.target); err != nil {
			for _, rest := range stagedList[i:] {
				os.Remove(rest.tmp)
			}
			return fmt.Errorf("rename %s -> %s after %d/%d targets committed: %w",
				s.tmp, s.target, i, len(stagedList), err)
		}
	}
	return nil
}

// RewriteManifestVersion is a convenience wrapper that prepares and commits
// a single manifest bump.
func RewriteManifestVersion(path, newVersion string, src SourceUpdate) error {
	pw, err := PrepareManifestVersion(path, newVersion, src)
	if err != nil {
		return err
	}
	return Commit([]PreparedWrite{pw})
}

// RewriteSource is a convenience wrapper for a single secondary-source bump.
func RewriteSource(path string, index int, src SourceUpdate) error {
	pw, err := PrepareSource(path, index, src)
	if err != nil {
		return err
	}
	return Commit([]PreparedWrite{pw})
}

// RewriteIndexEntry is a convenience wrapper for a single index update.
func RewriteIndexEntry(indexPath, id string, entry IndexEntry) error {
	pw, err := PrepareIndexEntry(indexPath, id, entry)
	if err != nil {
		return err
	}
	return Commit([]PreparedWrite{pw})
}

func prepareManifest(path string, mutate func(*yaml.Node) error) (PreparedWrite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PreparedWrite{}, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return PreparedWrite{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return PreparedWrite{}, fmt.Errorf("%s: top level must be a mapping", path)
	}
	if err := mutate(doc.Content[0]); err != nil {
		return PreparedWrite{}, fmt.Errorf("%s: %w", path, err)
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return PreparedWrite{}, err
	}
	return PreparedWrite{path: path, data: out, perm: 0644}, nil
}

func applySourceAt(root *yaml.Node, index int, src SourceUpdate) error {
	sources := mappingValue(root, "sources")
	if sources == nil || sources.Kind != yaml.SequenceNode {
		return fmt.Errorf("missing 'sources' list")
	}
	if index < 0 || index >= len(sources.Content) {
		return fmt.Errorf("sources[%d] out of range (have %d)", index, len(sources.Content))
	}
	target := sources.Content[index]
	if target.Kind != yaml.MappingNode {
		return fmt.Errorf("sources[%d] is not a mapping", index)
	}
	if src.URL != "" {
		if n := mappingValue(target, "url"); n != nil {
			n.Value = src.URL
		}
	}
	if src.SHA256 != "" {
		if n := mappingValue(target, "sha256"); n != nil {
			n.Value = src.SHA256
		}
	}
	if src.SHA512 != "" {
		if n := mappingValue(target, "sha512"); n != nil {
			n.Value = src.SHA512
		}
	}
	if src.Size > 0 {
		if n := mappingValue(target, "size"); n != nil {
			n.Tag = "!!int"
			n.Value = strconv.FormatInt(src.Size, 10)
			n.Style = 0
		}
	}
	return nil
}

// stageTempFile writes data to a tempfile alongside path, fsyncs it, applies
// perm, and returns the tempfile path. Caller is responsible for the rename.
func stageTempFile(path string, data []byte, perm os.FileMode) (string, error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// mappingValue returns the value node for `key` in a mapping, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
