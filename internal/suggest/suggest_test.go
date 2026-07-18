package suggest

import "testing"

func TestClosestTypo(t *testing.T) {
	got, ok := Closest("jdk-2", []string{"jdk-21", "jdk-25", "maven"})
	if !ok || got != "jdk-21" {
		t.Fatalf("Closest = %q,%v want jdk-21,true", got, ok)
	}
}

func TestClosestNoMatch(t *testing.T) {
	if got, ok := Closest("zzzzzz", []string{"jdk-21", "maven"}); ok {
		t.Fatalf("Closest = %q,%v want _,false", got, ok)
	}
}

func TestClosestShortStringNoFalsePositive(t *testing.T) {
	// "jq" (2 chars) is edit-distance 2 from "jmc" but that's a wild guess for
	// such a short string — must not match.
	if got, ok := Closest("jq", []string{"jmc", "jdk-21", "maven"}); ok {
		t.Fatalf("Closest(jq) = %q,%v want _,false (no short-string false positive)", got, ok)
	}
	// A genuine near-typo of a longer id still matches.
	if got, ok := Closest("gradl", []string{"gradle", "maven"}); !ok || got != "gradle" {
		t.Fatalf("Closest(gradl) = %q,%v want gradle,true", got, ok)
	}
}
