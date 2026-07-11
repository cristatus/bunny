// Package verparse extracts a major version number from the varied version
// strings bunny encounters — JDK builds ("21.0.11+10"), vendor-prefixed pins
// ("temurin-21.0.2"), legacy schemes ("8u492-b09"), and bare majors ("21").
package verparse

import "strconv"

// Major returns the first maximal run of digits in s — the major version —
// handling a vendor prefix or suffix and patch/build metadata uniformly:
// "21.0.11+10"→"21", "temurin-21.0.2"→"21", "8u492-b09"→"8". Returns "" if s
// contains no digits (e.g. an alias like "latest").
func Major(s string) string {
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			start = i
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := start
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	return s[start:end]
}

// MajorInt is Major parsed as an int, or 0 when there is no digit run.
func MajorInt(s string) int {
	n, _ := strconv.Atoi(Major(s))
	return n
}
