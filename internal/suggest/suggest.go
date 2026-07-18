// Package suggest offers "did you mean?" candidates for mistyped identifiers
// (package ids). It mirrors Kong's own command-suggestion threshold so the CLI
// behaves consistently between unknown commands and unknown package ids.
package suggest

// Distance returns the Levenshtein edit distance between a and b.
func Distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// Closest returns the best candidate within a length-scaled edit distance of
// target, or one that begins with target. Returns ("", false) when nothing
// qualifies. The distance may be at most 2 and at most half the typed length,
// so short strings don't match wildly different candidates (e.g. "jq"→"jmc").
func Closest(target string, candidates []string) (string, bool) {
	best, bestDist, found := "", 0, false
	for _, c := range candidates {
		d := Distance(target, c)
		qualifies := d <= 2 && 2*d <= len(target)
		if !qualifies && len(target) > 0 && len(c) >= len(target) && c[:len(target)] == target {
			qualifies = true
			d = 3 // prefix matches rank just below close edits
		}
		if !qualifies {
			continue
		}
		if !found || d < bestDist || (d == bestDist && c < best) {
			best, bestDist, found = c, d, true
		}
	}
	return best, found
}
