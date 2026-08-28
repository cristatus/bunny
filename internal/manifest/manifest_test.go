package manifest

import (
	"strings"
	"testing"
)

func TestParseMinimal(t *testing.T) {
	src := `
id: ripgrep
name: ripgrep
version: "14.1.0"
sources:
  - url: https://example.com/rg-14.1.0.tar.gz
    sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
bin:
  - name: rg
    path: "{app}/rg"
`
	m, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "ripgrep" || m.Version != "14.1.0" {
		t.Errorf("got %+v", m)
	}
}

func TestParseRejectsLegacyBindsBlock(t *testing.T) {
	src := `
id: zed
name: Zed
version: "1.0.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: zed, path: "{app}/bin/zed"}]
binds:
  - { host: "$HOME/.config/zed", bunny: "{data}/config" }
`
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Error("expected unknown-field error for removed binds: block")
	}
}

func TestParseRejectsMultipleDocuments(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
---
id: bar
`
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Fatal("expected multiple YAML documents to be rejected")
	}
}

func TestParseAllowsTrailingSeparator(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
---
`
	m, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatalf("a trailing document separator is a single document and should parse: %v", err)
	}
	if m.ID != "foo" {
		t.Errorf("got %+v", m)
	}
}

// Sandbox policy is the user's alone: a manifest carries no sandbox block at
// all, so one is an unknown field rather than a silently-ignored hint. This
// is what stops a shared catalog manifest from influencing the effective
// policy someone reads out of their own config.
func TestParseRejectsManifestSandboxPolicy(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
sandbox:
  home: isolated
  hide: [~/.ssh]
`
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Error("a manifest sandbox: block must be rejected, not treated as a recommendation")
	}
}

func TestValidateSandboxPolicyPersist(t *testing.T) {
	valid := &SandboxPolicy{Home: "ephemeral", Persist: []string{".claude/memory"}}
	if err := ValidateSandboxPolicy("sandbox", valid); err != nil {
		t.Fatalf("valid ephemeral persist entry rejected: %v", err)
	}

	for name, policy := range map[string]*SandboxPolicy{
		"persist without ephemeral home": {Home: "isolated", Persist: []string{".claude/memory"}},
		"persist with clean home":        {Home: "clean", Persist: []string{".claude/memory"}},
		"absolute persist path":          {Home: "ephemeral", Persist: []string{"/etc/passwd"}},
		"tilde persist path":             {Home: "ephemeral", Persist: []string{"~/memory"}},
		"persist path escapes home":      {Home: "ephemeral", Persist: []string{"../escape"}},
		"empty persist path":             {Home: "ephemeral", Persist: []string{""}},
		"persist also hidden":            {Home: "ephemeral", Hide: []string{".claude/memory"}, Persist: []string{".claude/memory"}},
		// "." and "dir/.." both clean to the home root itself: binding that
		// onto the overlay destination would replace the whole discard layer
		// rather than punching a hole for one path, defeating home: ephemeral
		// entirely.
		"persist path is the home root":        {Home: "ephemeral", Persist: []string{"."}},
		"persist path cleans to the home root": {Home: "ephemeral", Persist: []string{"dir/.."}},
		// foo/../bar and bar name the same path once cleaned; the hide/persist
		// conflict check must catch that even though the raw strings differ.
		"persist also hidden via different form": {Home: "ephemeral", Hide: []string{"bar"}, Persist: []string{"foo/../bar"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSandboxPolicy("sandbox", policy); err == nil {
				t.Errorf("expected %s to be rejected", name)
			}
		})
	}
}

func TestValidateSandboxPolicyAcceptsEphemeralHome(t *testing.T) {
	if err := ValidateSandboxPolicy("sandbox", &SandboxPolicy{Home: "ephemeral"}); err != nil {
		t.Errorf("home: ephemeral must be a valid policy value: %v", err)
	}
}

func TestValidateSandboxPolicyAcceptsCleanHome(t *testing.T) {
	if err := ValidateSandboxPolicy("sandbox", &SandboxPolicy{Home: "clean"}); err != nil {
		t.Errorf("home: clean must be a valid policy value: %v", err)
	}
}

func TestParseRejectsLegacyPathsBlock(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
paths:
  - { host: "$HOME/.config/foo", bunny: "{data}/config" }
`
	_, err := ParseBytes([]byte(src))
	if err == nil {
		t.Error("expected unknown-field error for legacy paths: block (renamed to binds:)")
	}
}

func TestValidateRejectsUnverifiedSource(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x}]
bin: [{name: foo, path: "{app}/foo"}]
`
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Error("expected validation error for missing checksum")
	}
}

func TestValidateID(t *testing.T) {
	good := []string{"foo", "foo-bar", "node-22", "jdk-21"}
	bad := []string{"", "FOO", "1foo", "foo--bar", "foo-", "foo bar"}
	for _, id := range good {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) unexpectedly failed: %v", id, err)
		}
	}
	for _, id := range bad {
		if err := ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) should have failed", id)
		}
	}
}

func TestValidateRejectsReservedBunnyCommand(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: bunny, path: "{app}/bunny"}]
`
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Fatal("expected reserved bunny command to be rejected")
	}
}

func TestExpand(t *testing.T) {
	vars := map[string]string{"app": "/x/app/foo", "data": "/x/var/app/foo", "version": "1.0"}
	if got := Expand("{app}/bin/foo", vars); got != "/x/app/foo/bin/foo" {
		t.Errorf("got %q", got)
	}
	if got := Expand("{data}/cache-{version}", vars); got != "/x/var/app/foo/cache-1.0" {
		t.Errorf("got %q", got)
	}
	// Unknown placeholders pass through untouched.
	if got := Expand("{unknown}/x", vars); got != "{unknown}/x" {
		t.Errorf("got %q", got)
	}
}

func TestSafeRelPath(t *testing.T) {
	good := []string{"prepare.sh", "scripts/install.sh"}
	bad := []string{"", "/abs", "../escape", "./foo/../../bar"}
	for _, p := range good {
		if err := SafeRelPath(p); err != nil {
			t.Errorf("SafeRelPath(%q) failed: %v", p, err)
		}
	}
	for _, p := range bad {
		if err := SafeRelPath(p); err == nil {
			t.Errorf("SafeRelPath(%q) should have failed", p)
		}
	}
}

func TestParseUnverifiedErrorMentionsChecksum(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x}]
bin: [{name: foo, path: "{app}/foo"}]
`
	_, err := ParseBytes([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "sha256 or sha512") {
		t.Errorf("expected checksum-required error, got %v", err)
	}
}

func TestParseGlobalBins(t *testing.T) {
	src := `
id: node-24
name: Node 24
version: "24.0.0"
provides: node
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: node, path: "{app}/bin/node"}]
global-bins:
  - "{data}/npm-global/bin"
  - "{data}/pnpm-global"
`
	m, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.GlobalBins) != 2 || m.GlobalBins[0] != "{data}/npm-global/bin" {
		t.Errorf("GlobalBins = %v", m.GlobalBins)
	}
}

func TestValidateGlobalBinsRejectsPlainPath(t *testing.T) {
	src := `
id: node-24
name: Node 24
version: "24.0.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: node, path: "{app}/bin/node"}]
global-bins:
  - "/usr/local/bin"
`
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Error("expected validation error for a global-bins entry with no placeholder root")
	}
}

func TestValidateGlobalBinsMustStayInsideData(t *testing.T) {
	src := `
id: node-24
name: Node 24
version: "24.0.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: node, path: "{app}/bin/node"}]
global-bins: ["{data}/../../bin"]
`
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Fatal("expected escaping global-bins path to be rejected")
	}
}

func TestValidateEnvironmentAndIconSize(t *testing.T) {
	badEnv := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
env: {"BAD-NAME": value}
`
	if _, err := ParseBytes([]byte(badEnv)); err == nil {
		t.Fatal("expected invalid environment key to be rejected")
	}
	badIcon := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
icons: [{src: "{app}/foo.png", name: foo, size: "../../bin"}]
`
	if _, err := ParseBytes([]byte(badIcon)); err == nil {
		t.Fatal("expected invalid icon size to be rejected")
	}
}

func TestParseToolchains(t *testing.T) {
	src := `
id: gradle
name: Gradle
version: "9.0.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: gradle, path: "{app}/bin/gradle"}]
toolchains: gradle
`
	m, err := ParseBytes([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if m.Toolchains != "gradle" {
		t.Errorf("Toolchains = %q, want gradle", m.Toolchains)
	}
}

func TestValidateToolchainsRejectsUnknown(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
toolchains: bazel
`
	if _, err := ParseBytes([]byte(src)); err == nil {
		t.Error("expected validation error for unknown toolchains value")
	}
}

func TestParseRequirement(t *testing.T) {
	cases := []struct {
		in  string
		cap string
		min int
		has bool
	}{
		{"jdk", "jdk", 0, false},
		{"jdk-21", "jdk-21", 0, false},
		{"jdk>=17", "jdk", 17, true},
		{"jdk>=8", "jdk", 8, true},
		{"jdk>=", "jdk", 0, true},  // malformed: hasMin but min=0
		{"jdk>=x", "jdk", 0, true}, // malformed
		{">=17", "", 17, true},     // malformed: empty capability
	}
	for _, c := range cases {
		cap, min, has := ParseRequirement(c.in)
		if cap != c.cap || min != c.min || has != c.has {
			t.Errorf("ParseRequirement(%q) = (%q,%d,%v), want (%q,%d,%v)", c.in, cap, min, has, c.cap, c.min, c.has)
		}
	}
}

func TestValidateRejectsMalformedRequirement(t *testing.T) {
	for _, bad := range []string{"jdk>=", "jdk>=x", ">=17"} {
		src := `
id: foo
name: Foo
version: "1.0"
requires: ["` + bad + `"]
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
`
		if _, err := ParseBytes([]byte(src)); err == nil {
			t.Errorf("expected validation error for requires %q", bad)
		}
	}
}

func TestValidateAllowsValidRequirement(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
requires: ["jdk>=17"]
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
`
	if _, err := ParseBytes([]byte(src)); err != nil {
		t.Errorf("valid jdk>=17 rejected: %v", err)
	}
}

// global-bins may live in either per-package tree bunny owns, but never in a
// shared host directory: there would be no version to dispatch on, and bunny
// would be claiming command names for binaries it did not install.
func TestValidateGlobalBinsRoots(t *testing.T) {
	for _, tc := range []struct {
		path    string
		wantErr bool
	}{
		{"{data}/npm-global/bin", false},
		{"{app}/bin", false},
		{"{home}/.local/share/pnpm", true},
		{"/usr/local/bin", true},
	} {
		src := `
id: node-24
name: Node 24
version: "24.0.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: node, path: "{app}/bin/node"}]
global-bins: ["` + tc.path + `"]
`
		_, err := ParseBytes([]byte(src))
		if tc.wantErr && err == nil {
			t.Errorf("%s: expected rejection", tc.path)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: unexpected error: %v", tc.path, err)
		}
	}
}

// A catalog that predates kind:, or a third-party one that never adopts it,
// must still put a GUI application in the app root rather than beside ripgrep.
func TestKindOfInfersAppFromDesktopEntry(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    Manifest
		want string
	}{
		{"explicit wins", Manifest{Kind: KindSDK, Desktop: []DesktopEntry{{ID: "x.desktop"}}}, KindSDK},
		{"desktop entry means app", Manifest{Desktop: []DesktopEntry{{ID: "code.desktop"}}}, KindApp},
		{"nothing to go on", Manifest{}, KindCLI},
		// No sdk inference: maven and gradle declare no provides:, so guessing
		// from it would misplace more than it placed.
		{"provides alone is not enough", Manifest{Provides: "jdk"}, KindCLI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.KindOf(); got != tc.want {
				t.Errorf("KindOf() = %q, want %q", got, tc.want)
			}
		})
	}
}

// kind and the desktop block are two statements about the same thing, so a
// manifest may not make them disagree.
func TestValidateRejectsDesktopEntryWithNonAppKind(t *testing.T) {
	base := `
id: code
name: Code
version: "1.0.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: code, path: "{app}/code"}]
desktop: [{id: code.desktop, name: Code, exec: "{app}/code"}]
`
	if _, err := ParseBytes([]byte(base + "kind: cli\n")); err == nil {
		t.Error("a desktop entry contradicts kind: cli")
	}
	if _, err := ParseBytes([]byte(base + "kind: app\n")); err != nil {
		t.Errorf("kind: app agrees with the desktop entry: %v", err)
	}
	if _, err := ParseBytes([]byte(base)); err != nil {
		t.Errorf("an absent kind is inferred, not rejected: %v", err)
	}
}
