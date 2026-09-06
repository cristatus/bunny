package shim

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"
)

// ProjectVersionFile is the project-local version pin file name. It is the
// only pin format Bunny reads; foreign files use vendor/version identifiers
// that are not translated into catalog package IDs.
const ProjectVersionFile = ".bunny-version"

// ProjectPin is one `<capability> <value>` line. Value is either a bare
// version, joined to the capability to form the package id ("21" → "jdk-21"),
// or a package id outright ("corretto-21") so a pin can name a specific
// provider. Either form may carry an @version suffix to guard the exact
// installed release. manifest.ValidateID forbids a leading digit, so the two
// forms are never ambiguous.
type ProjectPin struct {
	Capability string // e.g. "jdk"
	Value      string // e.g. "21" or "corretto-21"
	Source     string // absolute path to the .bunny-version file
}

// PackageID is the package this pin selects.
func (p *ProjectPin) PackageID() string {
	value, _, _ := strings.Cut(p.Value, "@")
	if value == "" {
		return ""
	}
	if value[0] >= '0' && value[0] <= '9' {
		return p.Capability + "-" + value
	}
	return value
}

// ExactVersion is the optional installed-release guard after @.
func (p *ProjectPin) ExactVersion() string {
	_, version, _ := strings.Cut(p.Value, "@")
	return version
}

// Validate rejects malformed pins before they can silently select another tool.
func (p *ProjectPin) Validate() error {
	if err := manifest.ValidateID(p.Capability); err != nil {
		return fmt.Errorf("invalid pin capability %q: %w", p.Capability, err)
	}
	if err := manifest.ValidateID(p.PackageID()); err != nil {
		return fmt.Errorf("invalid pin %s %q: %w", p.Capability, p.Value, err)
	}
	if _, version, exact := strings.Cut(p.Value, "@"); exact {
		if err := manifest.ValidateVersion(version); err != nil {
			return fmt.Errorf("invalid exact version in pin %s %q: %w", p.Capability, p.Value, err)
		}
	}
	return nil
}

// CheckInstalled enforces both provider identity and any exact release guard.
func (p *ProjectPin) CheckInstalled(st PinState) error {
	if err := p.Validate(); err != nil {
		return err
	}
	id := p.PackageID()
	if !st.IsInstalled(id) {
		return fmt.Errorf("%s %s pinned in %s, but %s is not installed\nhint: bunny install %s (the catalog must contain the requested release)", p.Capability, p.Value, p.Source, id, id)
	}
	if st.ProvidesOf(id) != p.Capability {
		return fmt.Errorf("%s %s pinned in %s, but %s does not provide %s\nhint: pin a package that does, or a bare version", p.Capability, p.Value, p.Source, id, p.Capability)
	}
	if version := p.ExactVersion(); version != "" && st.VersionOf(id) != version {
		return fmt.Errorf("%s %s pinned in %s, but %s has installed version %s\nhint: restore %s from a catalog containing version %s, or explicitly update the project pin", p.Capability, p.Value, p.Source, id, st.VersionOf(id), id, version)
	}
	return nil
}

// ResolveProjectVersion walks up from cwd and returns the pin for the given
// capability from the nearest .bunny-version that names it, or (nil, nil) if
// none does. A file that pins other capabilities does not stop the walk, so a
// subproject overrides only what it names.
func ResolveProjectVersion(cwd, capability string) (*ProjectPin, error) {
	if capability == "" {
		return nil, errors.New("capability name required")
	}
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	for dir := cwd; ; {
		path := filepath.Join(dir, ProjectVersionFile)
		content, err := os.ReadFile(path)
		switch {
		case err == nil:
			pins, err := parseBunnyVersion(string(content))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", path, err)
			}
			if v, ok := pins[capability]; ok {
				return &ProjectPin{Capability: capability, Value: v, Source: path}, nil
			}
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("read project pin %s: %w", path, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

// WriteProjectVersion sets capability→version in dir's .bunny-version file,
// preserving any other pins and comment lines. The file is created if absent
// and an existing pin for the capability is replaced in place.
func WriteProjectVersion(dir, capability, version string) error {
	if err := (&ProjectPin{Capability: capability, Value: version}).Validate(); err != nil {
		return err
	}
	path := filepath.Join(dir, ProjectVersionFile)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	newLine := capability + " " + version
	var out []string
	replaced := false
	if len(data) > 0 {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if !strings.HasPrefix(strings.TrimSpace(line), "#") && len(fields) >= 1 && fields[0] == capability {
				if !replaced {
					out = append(out, newLine)
				}
				replaced = true
			} else {
				out = append(out, line)
			}
		}
	}
	// Drop trailing blank lines from the file's final newline, then re-add one.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if !replaced {
		out = append(out, newLine)
	}
	content := strings.Join(out, "\n") + "\n"
	if _, err := parseBunnyVersion(content); err != nil {
		return fmt.Errorf("refusing to write invalid pin file %s: %w", path, err)
	}
	return fsutil.WriteFile(path, []byte(content), 0644)
}

// RemoveProjectVersion removes capability's pin from dir's .bunny-version,
// preserving other pins and comments. Returns whether a pin was actually
// removed. If nothing meaningful remains, the file is deleted.
func RemoveProjectVersion(dir, capability string) (bool, error) {
	path := filepath.Join(dir, ProjectVersionFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var out []string
	removed := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && len(fields) >= 1 && fields[0] == capability {
			removed = true
			continue
		}
		out = append(out, line)
	}
	if !removed {
		return false, nil
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return true, os.Remove(path)
	}
	return true, fsutil.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0644)
}

// ResolveAllPins returns every pin from the nearest .bunny-version walking up
// from cwd, plus its path. Returns (nil, "", nil) when there is none. Note a
// shim keeps walking per capability, so a capability inherited from a further
// ancestor will not appear here.
func ResolveAllPins(cwd string) (map[string]string, string, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, "", err
	}
	for dir := cwd; ; {
		path := filepath.Join(dir, ProjectVersionFile)
		content, err := os.ReadFile(path)
		if err == nil {
			pins, err := parseBunnyVersion(string(content))
			if err != nil {
				return nil, "", fmt.Errorf("read %s: %w", path, err)
			}
			if len(pins) > 0 {
				return pins, path, nil
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", nil
		}
		dir = parent
	}
}

// pinLines returns trimmed, non-blank, non-comment lines of content.
func pinLines(content string) []string {
	var out []string
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// parseBunnyVersion reads literal capability/value pairs with optional comments.
func parseBunnyVersion(content string) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range pinLines(content) {
		line, _, _ = strings.Cut(line, "#")
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("expected <capability> <version-or-package>, got %q", line)
		}
		p := ProjectPin{Capability: f[0], Value: f[1]}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := out[f[0]]; duplicate {
			return nil, fmt.Errorf("duplicate pin for %s", f[0])
		}
		out[f[0]] = f[1]
	}
	return out, nil
}
