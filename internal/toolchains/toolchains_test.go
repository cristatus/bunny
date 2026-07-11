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
