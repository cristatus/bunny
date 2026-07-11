// Package toolchains generates build-tool config that points Gradle and Maven
// JDK-toolchain resolution at bunny's installed JDKs. Pure string generation
// (no filesystem/state) so it is easy to test; the caller writes the files.
package toolchains

import (
	"fmt"
	"sort"
	"strings"
)

const (
	blockStart = "# >>> bunny managed (jdk toolchains) >>>"
	blockEnd   = "# <<< bunny managed <<<"
)

// JDK is one installed JDK available for build toolchains.
type JDK struct {
	Home  string // absolute JDK home (install dir)
	Major string // major version, e.g. "21"
}

// MergeGradleProperties returns the full gradle.properties content with bunny's
// managed block set to point Gradle toolchain resolution at homes. Content
// outside the markers is preserved; a missing block is appended; an empty homes
// slice yields an empty managed block (Gradle defaults apply).
func MergeGradleProperties(existing string, homes []string) string {
	var managed string
	if len(homes) > 0 {
		managed = blockStart + "\n" +
			"org.gradle.java.installations.paths=" + strings.Join(homes, ",") + "\n" +
			"org.gradle.java.installations.auto-download=false\n" +
			blockEnd
	} else {
		managed = blockStart + "\n" + blockEnd
	}

	start := strings.Index(existing, blockStart)
	if start == -1 {
		if existing == "" {
			return managed + "\n"
		}
		sep := "\n"
		if strings.HasSuffix(existing, "\n") {
			sep = ""
		}
		return existing + sep + managed + "\n"
	}
	end := strings.Index(existing, blockEnd)
	if end == -1 || end < start {
		return existing[:start] + managed + "\n" // corrupt half-block: replace tail
	}
	end += len(blockEnd)
	return existing[:start] + managed + existing[end:]
}

// MavenToolchainsXML returns a complete Maven toolchains.xml listing each JDK as
// a jdk toolchain, matched on major version. Sorted by home for determinism.
func MavenToolchainsXML(jdks []JDK) string {
	sorted := append([]JDK(nil), jdks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Home < sorted[j].Home })
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString("<toolchains>\n")
	for _, j := range sorted {
		b.WriteString("  <toolchain>\n")
		b.WriteString("    <type>jdk</type>\n")
		b.WriteString(fmt.Sprintf("    <provides><version>%s</version></provides>\n", j.Major))
		b.WriteString(fmt.Sprintf("    <configuration><jdkHome>%s</jdkHome></configuration>\n", j.Home))
		b.WriteString("  </toolchain>\n")
	}
	b.WriteString("</toolchains>\n")
	return b.String()
}
