package toolchains

import (
	"strings"
	"testing"
)

func TestMergeGradlePropertiesFresh(t *testing.T) {
	out := MergeGradleProperties("", []string{"/a/jdk-21", "/a/jdk-25"})
	if !strings.Contains(out, "org.gradle.java.installations.paths=/a/jdk-21,/a/jdk-25") {
		t.Errorf("missing installations.paths:\n%s", out)
	}
	if !strings.Contains(out, "org.gradle.java.installations.auto-download=false") {
		t.Errorf("missing auto-download:\n%s", out)
	}
}

func TestMergeGradlePropertiesPreservesAndReplaces(t *testing.T) {
	existing := "org.gradle.parallel=true\n" +
		"# >>> bunny managed (jdk toolchains) >>>\n" +
		"org.gradle.java.installations.paths=/old/jdk-11\n" +
		"# <<< bunny managed <<<\n"
	out := MergeGradleProperties(existing, []string{"/a/jdk-21"})
	if !strings.Contains(out, "org.gradle.parallel=true") {
		t.Errorf("clobbered user property:\n%s", out)
	}
	if strings.Contains(out, "/old/jdk-11") {
		t.Errorf("stale managed block not replaced:\n%s", out)
	}
	if !strings.Contains(out, "/a/jdk-21") {
		t.Errorf("new path missing:\n%s", out)
	}
	if strings.Count(out, "# >>> bunny managed (jdk toolchains) >>>") != 1 {
		t.Errorf("expected exactly one managed block:\n%s", out)
	}
}

func TestMavenToolchainsXML(t *testing.T) {
	out := MavenToolchainsXML([]JDK{{Home: "/a/jdk-21", Major: "21"}, {Home: "/a/jdk-25", Major: "25"}})
	if strings.Count(out, "<toolchain>") != 2 {
		t.Errorf("want 2 toolchains:\n%s", out)
	}
	if !strings.Contains(out, "<version>21</version>") || !strings.Contains(out, "<jdkHome>/a/jdk-21</jdkHome>") {
		t.Errorf("jdk-21 entry malformed:\n%s", out)
	}
}

// Two JDKs can share a major version — a Temurin 25 and a GraalVM 25 — and
// without a vendor Maven resolves whichever the file happens to list first.
func TestMavenToolchainsXMLDistinguishesVendors(t *testing.T) {
	out := MavenToolchainsXML([]JDK{
		{Home: "/a/graalvm-25", Major: "25", Vendor: "graalvm_community"},
		{Home: "/a/jbr-25", Major: "25", Vendor: "jetbrains"},
	})
	for _, want := range []string{
		"<version>25</version><vendor>graalvm_community</vendor>",
		"<version>25</version><vendor>jetbrains</vendor>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// A manifest with no update block has no vendor to report, and an empty
// <vendor/> would be a requirement no build can match.
func TestMavenToolchainsXMLOmitsAnUnknownVendor(t *testing.T) {
	out := MavenToolchainsXML([]JDK{{Home: "/a/jdk-21", Major: "21"}})
	if strings.Contains(out, "<vendor>") {
		t.Errorf("an unknown vendor must be omitted entirely:\n%s", out)
	}
}

// Java 8 and earlier are "1.8" in a Maven toolchain requirement, and Maven
// matches a non-range requirement as a string, so a bare "8" is unmatchable
// by the spelling every pom actually uses.
func TestMavenToolchainsXMLUsesTheJava8Convention(t *testing.T) {
	out := MavenToolchainsXML([]JDK{
		{Home: "/a/jdk-8", Major: "8"},
		{Home: "/a/jdk-11", Major: "11"},
	})
	if !strings.Contains(out, "<version>1.8</version>") {
		t.Errorf("Java 8 must be spelled 1.8:\n%s", out)
	}
	if strings.Contains(out, "<version>8</version>") {
		t.Errorf("a bare 8 no pom asks for must not appear:\n%s", out)
	}
	if !strings.Contains(out, "<version>11</version>") {
		t.Errorf("11 and later stay bare:\n%s", out)
	}
}
