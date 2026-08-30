// Package toolchains generates build-tool config that points Gradle and Maven
// JDK-toolchain resolution at bunny's installed JDKs. Pure string generation
// (no filesystem/state) so it is easy to test; the caller writes the files.
package toolchains

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
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
	// Vendor is the distribution name, so a build can ask for one of several
	// JDKs sharing a major version. Two installed 25s are indistinguishable
	// without it, and Maven then resolves whichever the file lists first.
	Vendor string
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
	sorted := slices.Clone(jdks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Home < sorted[j].Home })
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString("<toolchains>\n")
	for _, j := range sorted {
		b.WriteString("  <toolchain>\n")
		b.WriteString("    <type>jdk</type>\n")
		fmt.Fprintf(&b, "    <provides><version>%s</version>", mavenVersion(j.Major))
		if j.Vendor != "" {
			fmt.Fprintf(&b, "<vendor>%s</vendor>", j.Vendor)
		}
		b.WriteString("</provides>\n")
		fmt.Fprintf(&b, "    <configuration><jdkHome>%s</jdkHome></configuration>\n", j.Home)
		b.WriteString("  </toolchain>\n")
	}
	b.WriteString("</toolchains>\n")
	return b.String()
}

// mavenVersion spells a major version the way Maven toolchain requirements do.
// Java 8 and earlier are conventionally "1.8", not "8", and Maven matches a
// non-range requirement as a string, so a pom asking for 1.8 finds nothing
// against a bare 8.
func mavenVersion(major string) string {
	if n, err := strconv.Atoi(major); err == nil && n > 0 && n <= 8 {
		return "1." + major
	}
	return major
}
