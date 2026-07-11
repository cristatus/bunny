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

func TestParseRejectsLegacySandboxBlock(t *testing.T) {
	src := `
id: foo
name: Foo
version: "1.0"
sources: [{url: x, sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa}]
bin: [{name: foo, path: "{app}/foo"}]
sandbox:
  enabled: true
`
	_, err := ParseBytes([]byte(src))
	if err == nil {
		t.Error("expected unknown-field error for legacy sandbox: block")
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

func TestValidateGlobalBinsRequiresData(t *testing.T) {
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
		t.Error("expected validation error for global-bins entry without {data}")
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
