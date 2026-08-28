package manifest

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
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
	if err := ValidateEnv("env", m.Env); err != nil {
		return err
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
		if err := validateRootedPath(gb); err != nil {
			return vErr(fmt.Sprintf("global-bins[%d]", i), err.Error())
		}
	}
	if m.Toolchains != "" && m.Toolchains != "gradle" && m.Toolchains != "maven" {
		return vErr("toolchains", `must be "gradle" or "maven"`)
	}
	seenTags := map[string]bool{}
	for i, tag := range m.Tags {
		if !tagPattern.MatchString(tag) {
			return vErr(fmt.Sprintf("tags[%d]", i), "must be lowercase alphanumeric with hyphens")
		}
		if seenTags[tag] {
			return vErr(fmt.Sprintf("tags[%d]", i), "duplicate tag "+tag)
		}
		seenTags[tag] = true
	}
	if m.Kind != "" && !KnownKind(m.Kind) {
		return vErr("kind", `must be "app", "cli", or "sdk"`)
	}
	// A desktop entry means a graphical application, which is what KindOf
	// infers when kind is absent. Declaring something else contradicts the
	// manifest's own evidence, and lands a GUI app in the wrong root.
	if len(m.Desktop) > 0 && m.Kind != "" && m.Kind != KindApp {
		return vErr("kind", `must be "app" for a package with desktop entries`)
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
			if !desktopBasePattern.MatchString(action.ID) || action.Name == "" {
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

// ValidateSandboxPolicy validates one policy layer's shape. Manifests carry
// no policy of their own, so every caller is user config (a profile or a
// package entry); the type stays here because it is the vocabulary both are
// written in. Paths are interpreted at launch: absolute paths stay absolute,
// while ~ and relative paths are resolved against the real host home.
// Cross-layer rules (an fs grant reaching a scoped effective boundary, say)
// are enforced again at resolve time; this catches everything visible within
// one policy layer.
func ValidateSandboxPolicy(field string, policy *SandboxPolicy) error {
	if policy == nil {
		return nil
	}
	if policy.Boundary != "" && policy.Boundary != "scoped" && policy.Boundary != "hardened" {
		return vErr(field+".boundary", `must be "scoped" or "hardened"`)
	}
	if policy.Home != "" && policy.Home != "isolated" && policy.Home != "shared" && policy.Home != "ephemeral" && policy.Home != "clean" {
		return vErr(field+".home", `must be "isolated", "shared", "ephemeral", or "clean"`)
	}
	if policy.Boundary == "hardened" && policy.Home == "shared" {
		return vErr(field+".home", "hardened boundary cannot share the host home")
	}
	for i, path := range policy.Hide {
		if err := validatePolicyPath(path); err != nil {
			return vErr(fmt.Sprintf("%s.hide[%d]", field, i), err.Error())
		}
	}
	if err := validateSandboxPersist(field, policy); err != nil {
		return err
	}
	for name := range policy.Features {
		if !KnownSandboxFeature(name) {
			msg := `must be one of "audio", "wayland", "x11", "dbus", "agents", or "tty"`
			if name == "network" {
				msg = `is not a feature toggle: use net (net: none, net: host, or a net: {mode: private, ...} block)`
			}
			return vErr(field+".features."+name, msg)
		}
	}
	if err := validateSandboxFS(field, policy); err != nil {
		return err
	}
	return validateSandboxNet(field, policy)
}

// validateSandboxPersist checks each persist entry is a home-relative path
// (no ~, no absolute path, no ".." escape, and not a bare "." or equivalent
// that resolves to the home root itself — either would let a persist bind
// replace the whole discard layer instead of punching a hole for one path)
// and that persist is only present with home: ephemeral, matching how egress
// under a non-private net.mode is rejected rather than silently ignored. An
// entry named in both hide and persist within the same policy layer is
// rejected by comparing cleaned paths, so equivalent lexical forms (like
// foo/../bar and bar) cannot slip past; the cross-layer case (set in
// different layers) is caught once policies are merged, since a single layer
// cannot see what another layer set.
func validateSandboxPersist(field string, policy *SandboxPolicy) error {
	for i, path := range policy.Persist {
		if err := validatePersistPath(path); err != nil {
			return vErr(fmt.Sprintf("%s.persist[%d]", field, i), err.Error())
		}
		if PathConflictsWithHide(policy.Hide, path) {
			return vErr(fmt.Sprintf("%s.persist[%d]", field, i), "cannot be both hidden and persisted")
		}
	}
	if len(policy.Persist) > 0 && policy.Home != "" && policy.Home != "ephemeral" {
		return vErr(field+".persist", "non-empty only with home: ephemeral")
	}
	return nil
}

// PathConflictsWithHide reports whether path names the same location as one
// of hide's entries, comparing filepath.Clean'd forms so equivalent lexical
// spellings (foo/../bar and bar) cannot slip past a raw string comparison.
// Shared by same-layer validation here and the merged-layer check in
// runtime.PackageSandbox.finalize, since hide and persist can each be set in
// a different policy layer.
func PathConflictsWithHide(hide []string, path string) bool {
	cleaned := filepath.Clean(path)
	return slices.ContainsFunc(hide, func(h string) bool { return filepath.Clean(h) == cleaned })
}

func validatePersistPath(path string) error {
	if err := validatePolicyPath(path); err != nil {
		return err
	}
	if path == "~" || strings.HasPrefix(path, "~/") || filepath.IsAbs(path) {
		return fmt.Errorf("must be home-relative, not absolute or ~")
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return fmt.Errorf("must not resolve to the home root: name a specific file or directory within it")
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("must not escape the home with ..")
	}
	return nil
}

func validateSandboxFS(field string, policy *SandboxPolicy) error {
	fs := policy.FS
	if fs == nil {
		return nil
	}
	// Silent no-ops are how policies lie: grants that cannot take effect in
	// scoped mode are an error, not decoration.
	if policy.Boundary == "scoped" {
		return vErr(field+".fs", "filesystem grants require boundary: hardened")
	}
	if fs.Cwd != "" && fs.Cwd != "read" && fs.Cwd != "write" && fs.Cwd != "hidden" {
		return vErr(field+".fs.cwd", `must be "read", "write", or "hidden"`)
	}
	for _, list := range []struct {
		name  string
		paths *[]string
	}{{"read", fs.Read}, {"write", fs.Write}} {
		if list.paths == nil {
			continue
		}
		for i, path := range *list.paths {
			if err := validatePolicyPath(path); err != nil {
				return vErr(fmt.Sprintf("%s.fs.%s[%d]", field, list.name, i), err.Error())
			}
		}
	}
	return nil
}

func validateSandboxNet(field string, policy *SandboxPolicy) error {
	net := policy.Net
	if net == nil {
		return nil
	}
	switch net.Mode {
	case "", "host", "private", "none":
	default:
		return vErr(field+".net.mode", `must be "host", "private", or "none"`)
	}
	if net.Inbound != nil {
		for i, entry := range *net.Inbound {
			if _, err := ParseInboundRule(entry); err != nil {
				return vErr(fmt.Sprintf("%s.net.inbound[%d]", field, i), err.Error())
			}
		}
	}
	if net.Egress != nil {
		for i, entry := range *net.Egress {
			if _, err := ParseEgressRule(entry); err != nil {
				return vErr(fmt.Sprintf("%s.net.egress[%d]", field, i), err.Error())
			}
		}
	}
	// Allowlists on a mode that cannot honour them are an error, not a
	// silent no-op: host has no filter, none has no stack to configure.
	if net.Mode == "host" || net.Mode == "none" {
		if net.Inbound != nil {
			return vErr(field+".net.inbound", fmt.Sprintf("meaningless with mode %q; only private filters inbound", net.Mode))
		}
		if net.Egress != nil {
			return vErr(field+".net.egress", fmt.Sprintf("meaningless with mode %q; only private filters egress", net.Mode))
		}
	}
	return nil
}

func validatePolicyPath(path string) error {
	if path == "" {
		return fmt.Errorf("must not be empty")
	}
	if strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("contains NUL")
	}
	return nil
}

// KnownSandboxFeature reports whether the lightweight runtime implements a
// feature toggle. Rejecting unknown keys avoids policies that appear to work
// while silently granting access.
func KnownSandboxFeature(name string) bool {
	switch name {
	case "audio", "wayland", "x11", "dbus", "agents", "tty":
		return true
	default:
		return false
	}
}

// ValidateEnv checks that every name is a usable environment-variable name and
// that no value carries a NUL. field prefixes the reported path, so manifests
// report "env.FOO" and the user config reports "env.node.FOO".
func ValidateEnv(field string, env map[string]string) error {
	for key, value := range env {
		if !envPattern.MatchString(key) {
			return vErr(field+"."+key, "invalid environment variable name")
		}
		if strings.ContainsRune(value, '\x00') {
			return vErr(field+"."+key, "value contains NUL")
		}
	}
	return nil
}

// globalBinRoots are the placeholders a global-bins entry may be anchored to.
// Both are per-package trees bunny owns: {data} is the package's data dir and
// {app} its install tree.
//
// {home} is deliberately excluded. A tool's native global dir (pnpm's
// ~/.local/share/pnpm, say) is shared across every version and is usually
// already on PATH via that tool's own setup, so shimming it would give bunny
// no version to dispatch on and would have it claim command names for
// binaries it did not install. Global shims only make sense over a directory
// that belongs to one package version.
var globalBinRoots = []string{"{data}", "{app}"}

// validateRootedPath requires a path to start at one of the known placeholder
// roots and stay inside it, so a manifest cannot walk out with "..".
func validateRootedPath(path string) error {
	for _, root := range globalBinRoots {
		if !strings.HasPrefix(path, root) {
			continue
		}
		expanded := "/root" + strings.TrimPrefix(path, root)
		if strings.ContainsAny(expanded, "{}") {
			return fmt.Errorf("may only use one leading %s placeholder", root)
		}
		rel, err := filepath.Rel("/root", filepath.Clean(expanded))
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("must remain inside %s", root)
		}
		return nil
	}
	return fmt.Errorf("must start with one of %s", strings.Join(globalBinRoots, ", "))
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
	idPattern          = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	binPattern         = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	versionPattern     = regexp.MustCompile(`^[a-zA-Z0-9._+-]+$`)
	desktopBasePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)
	iconPattern        = regexp.MustCompile(`^[a-z0-9_-]+$`)
	envPattern         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tagPattern         = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	iconSizePattern    = regexp.MustCompile(`^(?:[1-9][0-9]*x[1-9][0-9]*|scalable)$`)
	hexPattern         = regexp.MustCompile(`^[0-9a-fA-F]+$`)
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
	if !desktopBasePattern.MatchString(base) {
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
