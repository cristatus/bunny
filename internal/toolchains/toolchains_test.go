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
	for _, tc := range []struct {
		name    string
		why     string
		jdks    []JDK
		want    []string
		notWant []string
	}{
		{
			name: "entries and homes",
			jdks: []JDK{{Home: "/a/jdk-21", Major: "21"}, {Home: "/a/jdk-25", Major: "25"}},
			want: []string{"<version>21</version>", "<jdkHome>/a/jdk-21</jdkHome>"},
		},
		{
			name: "vendors distinguish a shared major",
			why:  "a Temurin 25 and a GraalVM 25 are otherwise interchangeable, and Maven resolves whichever the file lists first",
			jdks: []JDK{
				{Home: "/a/graalvm-25", Major: "25", Vendor: "graalvm_community"},
				{Home: "/a/jbr-25", Major: "25", Vendor: "jetbrains"},
			},
			want: []string{
				"<version>25</version><vendor>graalvm_community</vendor>",
				"<version>25</version><vendor>jetbrains</vendor>",
			},
		},
		{
			name:    "an unknown vendor is omitted",
			why:     "a manifest whose update backend has no distribution reports none, and an empty <vendor/> would be a requirement no build can match",
			jdks:    []JDK{{Home: "/a/jdk-21", Major: "21"}},
			notWant: []string{"<vendor>"},
		},
		{
			name:    "Java 8 keeps the 1.8 spelling",
			why:     "Maven matches a non-range requirement as a string, so a bare 8 is unmatchable by the spelling every pom uses",
			jdks:    []JDK{{Home: "/a/jdk-8", Major: "8"}, {Home: "/a/jdk-11", Major: "11"}},
			want:    []string{"<version>1.8</version>", "<version>11</version>"},
			notWant: []string{"<version>8</version>"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := MavenToolchainsXML(tc.jdks)
			if n := strings.Count(out, "<toolchain>"); n != len(tc.jdks) {
				t.Errorf("want %d toolchains, got %d:\n%s", len(tc.jdks), n, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q (%s):\n%s", want, tc.why, out)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(out, notWant) {
					t.Errorf("must not contain %q (%s):\n%s", notWant, tc.why, out)
				}
			}
		})
	}
}
