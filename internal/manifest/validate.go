package manifest

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ValidationError wraps a structural problem with the offending field path.
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return fmt.Sprintf("%s: %s", e.Field, e.Msg)
}

func vErr(field, msg string) error { return &ValidationError{Field: field, Msg: msg} }

// Validate enforces the structural rules and the small security guards
// (path-traversal, shell-unfriendly names) that protect the install path.
func (m *Manifest) Validate() error {
	if err := ValidateID(m.ID); err != nil {
		return vErr("id", err.Error())
	}
	if m.Name == "" {
		return vErr("name", "required")
	}
	if err := validateVersion(m.Version); err != nil {
		return vErr("version", err.Error())
	}
	if m.Provides != "" {
		if err := ValidateID(m.Provides); err != nil {
			return vErr("provides", err.Error())
		}
	}
	if len(m.Sources) == 0 {
		return vErr("sources", "at least one source required")
	}
	for i, s := range m.Sources {
		if s.URL == "" {
			return vErr(fmt.Sprintf("sources[%d].url", i), "required")
		}
		if err := validateSourceFileName(s.File); err != nil {
			return vErr(fmt.Sprintf("sources[%d].file", i), err.Error())
		}
		if err := validateSourceFileName(s.Name); err != nil {
			return vErr(fmt.Sprintf("sources[%d].name", i), err.Error())
		}
		if s.SHA256 == "" && s.SHA512 == "" {
			return vErr(fmt.Sprintf("sources[%d]", i),
				"sha256 or sha512 required (bunny refuses to install from unverified sources)")
		}
		if s.SHA256 != "" && (len(s.SHA256) != 64 || !hexPattern.MatchString(s.SHA256)) {
			return vErr(fmt.Sprintf("sources[%d].sha256", i), "must be 64 hexadecimal characters")
		}
		if s.SHA512 != "" && (len(s.SHA512) != 128 || !hexPattern.MatchString(s.SHA512)) {
			return vErr(fmt.Sprintf("sources[%d].sha512", i), "must be 128 hexadecimal characters")
		}
		if s.Size < 0 {
			return vErr(fmt.Sprintf("sources[%d].size", i), "must not be negative")
		}
	}
	if len(m.Bin) == 0 {
		return vErr("bin", "at least one binary required")
	}
	seenBins := map[string]bool{}
	for i, b := range m.Bin {
		if err := validateBinaryName(b.Name); err != nil {
			return vErr(fmt.Sprintf("bin[%d].name", i), err.Error())
		}
		if b.Path == "" {
			return vErr(fmt.Sprintf("bin[%d].path", i), "required")
		}
		if strings.ContainsRune(b.Path, '\x00') {
			return vErr(fmt.Sprintf("bin[%d].path", i), "contains NUL")
		}
		for j, arg := range b.Args {
			if strings.ContainsRune(arg, '\x00') {
				return vErr(fmt.Sprintf("bin[%d].args[%d]", i, j), "contains NUL")
			}
		}
		if b.Name == "bunny" {
			return vErr(fmt.Sprintf("bin[%d].name", i), `"bunny" is reserved for the Bunny executable`)
		}
		if seenBins[b.Name] {
			return vErr(fmt.Sprintf("bin[%d].name", i), "duplicate binary name")
		}
		seenBins[b.Name] = true
	}
	for key, value := range m.Env {
		if !envPattern.MatchString(key) {
			return vErr("env."+key, "invalid environment variable name")
		}
		if strings.ContainsRune(value, '\x00') {
			return vErr("env."+key, "value contains NUL")
		}
	}
	for i, req := range m.Requires {
		cap, min, hasMin := ParseRequirement(req)
		if hasMin && (cap == "" || min <= 0) {
			return vErr(fmt.Sprintf("requires[%d]", i), `invalid version constraint (use "<capability>>=<major>", e.g. "jdk>=17")`)
		}
		if err := ValidateID(cap); err != nil {
			return vErr(fmt.Sprintf("requires[%d]", i), err.Error())
		}
	}
	for i, gb := range m.GlobalBins {
		if !strings.Contains(gb, "{data}") {
			return vErr(fmt.Sprintf("global-bins[%d]", i), "must reference {data} (global bins live under the package data dir)")
		}
		if err := validateDataPath(gb); err != nil {
			return vErr(fmt.Sprintf("global-bins[%d]", i), err.Error())
		}
	}
	if m.Toolchains != "" && m.Toolchains != "gradle" && m.Toolchains != "maven" {
		return vErr("toolchains", `must be "gradle" or "maven"`)
	}
	seenDesktop := map[string]bool{}
	for i, d := range m.Desktop {
		if err := validateDesktopID(d.ID); err != nil {
			return vErr(fmt.Sprintf("desktop[%d].id", i), err.Error())
		}
		if d.Name == "" || d.Exec == "" {
			return vErr(fmt.Sprintf("desktop[%d]", i), "name and exec are required")
		}
		if seenDesktop[d.ID] {
			return vErr(fmt.Sprintf("desktop[%d].id", i), "duplicate desktop id")
		}
		seenDesktop[d.ID] = true
		if hasNewline(d.Name, d.GenericName, d.Comment, d.Exec, d.Icon, d.Type, d.StartupWMClass) {
			return vErr(fmt.Sprintf("desktop[%d]", i), "fields must not contain newlines")
		}
		seenActions := map[string]bool{}
		for j, action := range d.Actions {
			if !desktopBasePattrn.MatchString(action.ID) || action.Name == "" {
				return vErr(fmt.Sprintf("desktop[%d].actions[%d]", i, j), "valid id and name are required")
			}
			if seenActions[action.ID] {
				return vErr(fmt.Sprintf("desktop[%d].actions[%d].id", i, j), "duplicate action id")
			}
			if hasNewline(action.ID, action.Name, action.Exec) {
				return vErr(fmt.Sprintf("desktop[%d].actions[%d]", i, j), "fields must not contain newlines")
			}
			seenActions[action.ID] = true
		}
	}
	seenIcons := map[string]bool{}
	for i, ic := range m.Icons {
		if err := validateIconName(ic.Name); err != nil {
			return vErr(fmt.Sprintf("icons[%d].name", i), err.Error())
		}
		if ic.Src == "" {
			return vErr(fmt.Sprintf("icons[%d].src", i), "required")
		}
		if ic.Size != "" && !iconSizePattern.MatchString(ic.Size) {
			return vErr(fmt.Sprintf("icons[%d].size", i), `must be WIDTHxHEIGHT or "scalable"`)
		}
		size := ic.Size
		if size == "" {
			size = "128x128"
		}
		iconKey := size + "\x00" + ic.Name + filepath.Ext(ic.Src)
		if seenIcons[iconKey] {
			return vErr(fmt.Sprintf("icons[%d]", i), "duplicate icon destination")
		}
		seenIcons[iconKey] = true
	}
	return nil
}

func validateDataPath(path string) error {
	expanded := strings.ReplaceAll(path, "{data}", "/data")
	if strings.ContainsAny(expanded, "{}") {
		return fmt.Errorf("may only use the {data} placeholder")
	}
	rel, err := filepath.Rel("/data", filepath.Clean(expanded))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("must remain inside {data}")
	}
	return nil
}

func hasNewline(values ...string) bool {
	for _, value := range values {
		if strings.ContainsAny(value, "\r\n") {
			return true
		}
	}
	return false
}

// --- internal validators ---

var (
	idPattern         = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	binPattern        = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	versionPattern    = regexp.MustCompile(`^[a-zA-Z0-9._+-]+$`)
	desktopBasePattrn = regexp.MustCompile(`^[a-z0-9_-]+$`)
	iconPattern       = regexp.MustCompile(`^[a-z0-9_-]+$`)
	envPattern        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	iconSizePattern   = regexp.MustCompile(`^(?:[1-9][0-9]*x[1-9][0-9]*|scalable)$`)
	hexPattern        = regexp.MustCompile(`^[0-9a-fA-F]+$`)
)

// ValidateID enforces the package-ID syntax used in manifests, state, and
// user-facing commands.
func ValidateID(id string) error {
	if len(id) < 2 || len(id) > 64 {
		return fmt.Errorf("must be 2-64 chars, got %d", len(id))
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("must be lowercase, start with a letter, contain only [a-z0-9-]")
	}
	if strings.Contains(id, "--") || strings.HasSuffix(id, "-") {
		return fmt.Errorf("invalid hyphen usage")
	}
	return nil
}

func validateVersion(v string) error {
	if v == "" || len(v) > 64 {
		return fmt.Errorf("must be 1-64 chars")
	}
	if !versionPattern.MatchString(v) {
		return fmt.Errorf("must contain only [a-zA-Z0-9._+-]")
	}
	if strings.HasPrefix(v, ".") || strings.HasSuffix(v, ".") {
		return fmt.Errorf("cannot start or end with dot")
	}
	return nil
}

func validateBinaryName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("must be 1-64 chars")
	}
	if !binPattern.MatchString(name) {
		return fmt.Errorf("must be lowercase, start with letter, contain only [a-z0-9_-]")
	}
	if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return fmt.Errorf("invalid name")
	}
	return nil
}

func validateDesktopID(id string) error {
	if id == "" || len(id) > 128 {
		return fmt.Errorf("must be 1-128 chars")
	}
	if !strings.HasSuffix(id, ".desktop") {
		return fmt.Errorf("must end with .desktop")
	}
	base := strings.TrimSuffix(id, ".desktop")
	if !desktopBasePattrn.MatchString(base) {
		return fmt.Errorf("invalid characters")
	}
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("cannot contain slashes")
	}
	return nil
}

func validateIconName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("must be 1-64 chars")
	}
	if !iconPattern.MatchString(name) {
		return fmt.Errorf("must contain only [a-z0-9_-]")
	}
	if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return fmt.Errorf("invalid name")
	}
	return nil
}

func validateSourceFileName(name string) error {
	if name == "" {
		return nil
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("must be a filename, not a path")
	}
	if name == "." || name == ".." || filepath.Clean(name) != name {
		return fmt.Errorf("invalid filename")
	}
	return nil
}

// SafeRelPath is the guard used by catalog loaders to prevent path-traversal
// in manifest sibling-file lookups (e.g. prepare-script paths). Exported
// because the catalog package uses it too.
func SafeRelPath(rel string) error {
	if rel == "" {
		return fmt.Errorf("empty")
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("absolute path not allowed")
	}
	cleaned := filepath.Clean(rel)
	if cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "..\\") {
		return fmt.Errorf("path traversal not allowed")
	}
	return nil
}
