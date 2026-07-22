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

// Compare orders two version strings for "is one newer than the other",
// scheme-agnostically. It splits each string into maximal digit and non-digit
// runs and compares them run by run: digit runs numerically (so build "9" is
// older than "10", and "b08" equals "b8"), non-digit runs lexically. When every
// shared run is equal, the string with more runs is newer — so a respin like
// "8u482-b08.1" beats "8u482-b08", and "25.0.3+9" beats "25.0.3". Returns -1, 0
// or +1 for a<b, a==b, a>b.
//
// This deliberately favors "more segments = newer", so it does not model semver
// pre-release precedence (it treats "1.0.0-rc" as newer than "1.0.0"). Bunny's
// catalog tracks stable vendor releases, where that case does not arise.
func Compare(a, b string) int {
	ta, tb := tokenize(a), tokenize(b)
	for i := 0; i < len(ta) && i < len(tb); i++ {
		if c := ta[i].cmp(tb[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(ta) < len(tb):
		return -1
	case len(ta) > len(tb):
		return 1
	}
	return 0
}

type verToken struct {
	text  string
	isNum bool
}

// cmp orders two runs: numeric vs numeric numerically, text vs text lexically,
// and a numeric run ranks above a text run when their kinds differ.
func (t verToken) cmp(o verToken) int {
	switch {
	case t.isNum && o.isNum:
		return cmpNumeric(t.text, o.text)
	case !t.isNum && !o.isNum:
		if t.text < o.text {
			return -1
		}
		if t.text > o.text {
			return 1
		}
		return 0
	case t.isNum:
		return 1
	default:
		return -1
	}
}

// cmpNumeric compares two digit runs by magnitude without parsing (avoids
// overflow and ignores leading zeros, so "08" == "8").
func cmpNumeric(a, b string) int {
	a, b = trimLeadingZeros(a), trimLeadingZeros(b)
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func trimLeadingZeros(s string) string {
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	return s[i:]
}

// tokenize splits s into alternating digit and non-digit runs.
func tokenize(s string) []verToken {
	var out []verToken
	for i := 0; i < len(s); {
		num := s[i] >= '0' && s[i] <= '9'
		j := i
		for j < len(s) && (s[j] >= '0' && s[j] <= '9') == num {
			j++
		}
		out = append(out, verToken{text: s[i:j], isNum: num})
		i = j
	}
	return out
}
