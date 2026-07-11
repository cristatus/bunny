package shim

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/verparse"
)

// ProjectVersionFile is the project-local version pin file name.
const ProjectVersionFile = ".bunny-version"

const (
	toolVersionsFile = ".tool-versions"
	sdkmanrcFile     = ".sdkmanrc"
	javaVersionFile  = ".java-version"
)

// ProjectPin describes one resolved capability → version pin from the
// nearest recognized pin file walking up from cwd.
type ProjectPin struct {
	Capability string // e.g. "node"
	Version    string // e.g. "22"
	Source     string // absolute path to the .bunny-version file
}

// ResolveProjectVersion walks up from cwd and returns the version pinned for
// the given capability by the nearest recognized pin file, or (nil, nil) if
// none pins it. Within a directory, files are consulted in pinSources order
// (.bunny-version first); foreign formats are normalized to a major version.
func ResolveProjectVersion(cwd, capability string) (*ProjectPin, error) {
	if capability == "" {
		return nil, errors.New("capability name required")
	}
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	for dir := cwd; ; {
		for _, src := range pinSources {
			path := filepath.Join(dir, src.file)
			content, err := os.ReadFile(path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				// An unreadable pin (bad perms, it's a directory, ...) must not
				// break every shimmed command. Treat it as absent and keep
				// searching up the tree.
				log.Debug("Ignoring unreadable version pin", "path", path, "error", err)
				continue
			}
			if v, ok := src.parse(string(content))[capability]; ok {
				return &ProjectPin{Capability: capability, Version: v, Source: path}, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, nil
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

// ResolveAllPins returns every capability pinning from the nearest directory
// containing any recognized pin file (merging the files in pinSources order,
// higher precedence overwriting), plus a representative pin path. Returns
// (nil, "", nil) if no pin file is found walking up from cwd.
func ResolveAllPins(cwd string) (map[string]string, string, error) {
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, "", err
	}
	for dir := cwd; ; {
		merged := map[string]string{}
		repPath := ""
		// Iterate lowest-precedence first so higher-precedence overwrites; the
		// last existing file read (highest precedence) becomes the rep path.
		for i := len(pinSources) - 1; i >= 0; i-- {
			path := filepath.Join(dir, pinSources[i].file)
			content, err := os.ReadFile(path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, "", err
			}
			for k, v := range pinSources[i].parse(string(content)) {
				merged[k] = v
			}
			repPath = path
		}
		if len(merged) > 0 {
			return merged, repPath, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, "", nil
}

// mapCapability maps a foreign tool/key name to a bunny capability, or "" if
// bunny has no matching capability (the pin is then skipped).
func mapCapability(name string) string {
	switch name {
	case "java", "jdk":
		return "jdk"
	case "nodejs", "node":
		return "node"
	default:
		return ""
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

// parseToolVersions reads asdf/mise ".tool-versions": "<tool> <version>…" lines.
func parseToolVersions(content string) map[string]string {
	out := map[string]string{}
	for _, line := range pinLines(content) {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		cap := mapCapability(f[0])
		if cap == "" {
			continue
		}
		if v := verparse.Major(f[1]); v != "" {
			out[cap] = v
		}
	}
	return out
}

// parseSdkmanrc reads SDKMAN ".sdkmanrc": "<key>=<value>" lines.
func parseSdkmanrc(content string) map[string]string {
	out := map[string]string{}
	for _, line := range pinLines(content) {
		k, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cap := mapCapability(strings.TrimSpace(k))
		if cap == "" {
			continue
		}
		if v := verparse.Major(strings.TrimSpace(val)); v != "" {
			out[cap] = v
		}
	}
	return out
}

// parseJavaVersion reads jenv ".java-version": a single bare version → jdk.
func parseJavaVersion(content string) map[string]string {
	for _, line := range pinLines(content) {
		if v := verparse.Major(line); v != "" {
			return map[string]string{"jdk": v}
		}
	}
	return map[string]string{}
}

// pinSources lists the recognized pin files in within-directory precedence
// order (highest first); resolvers consult them in this order per directory.
var pinSources = []struct {
	file  string
	parse func(string) map[string]string
}{
	{ProjectVersionFile, parseBunnyVersion},
	{toolVersionsFile, parseToolVersions},
	{sdkmanrcFile, parseSdkmanrc},
	{javaVersionFile, parseJavaVersion},
}
