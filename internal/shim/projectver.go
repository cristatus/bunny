package shim

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"
)

// ProjectVersionFile is the project-local version pin file name. It is the
// only pin format bunny reads: foreign files (.tool-versions, .sdkmanrc,
// .java-version) encode a vendor and patch level bunny cannot honor, so
// reading them meant silently running a different build than the file asked
// for.
const ProjectVersionFile = ".bunny-version"

// ProjectPin is one `<capability> <value>` line. Value is either a bare
// version, joined to the capability to form the package id ("21" → "jdk-21"),
// or a package id outright ("corretto-21") so a pin can name a specific
// provider. manifest.ValidateID forbids a leading digit, so the two forms are
// never ambiguous.
type ProjectPin struct {
	Capability string // e.g. "jdk"
	Value      string // e.g. "21" or "corretto-21"
	Source     string // absolute path to the .bunny-version file
}

// PackageID is the package this pin selects.
func (p *ProjectPin) PackageID() string {
	if p.Value == "" {
		return ""
	}
	if p.Value[0] >= '0' && p.Value[0] <= '9' {
		return p.Capability + "-" + p.Value
	}
	return p.Value
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
			if v, ok := parseBunnyVersion(string(content))[capability]; ok {
				return &ProjectPin{Capability: capability, Value: v, Source: path}, nil
			}
		case !errors.Is(err, fs.ErrNotExist):
			// An unreadable pin (bad perms, a directory, ...) must not break
			// every shimmed command; treat it as absent and keep searching.
			log.Debug("Ignoring unreadable version pin", "path", path, "error", err)
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
				out = append(out, newLine)
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
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0644)
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
	return true, os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0644)
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
			if pins := parseBunnyVersion(string(content)); len(pins) > 0 {
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

// parseBunnyVersion reads bunny's own format: "<capability> <version>" lines,
// kept literal (keys are already bunny capabilities; values already majors).
func parseBunnyVersion(content string) map[string]string {
	out := map[string]string{}
	for _, line := range pinLines(content) {
		if f := strings.Fields(line); len(f) >= 2 {
			out[f[0]] = f[1]
		}
	}
	return out
}
