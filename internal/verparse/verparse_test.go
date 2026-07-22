package verparse

import "testing"

func TestMajor(t *testing.T) {
	cases := map[string]string{
		"21.0.11+10":     "21",
		"temurin-21.0.2": "21",
		"8u492-b09":      "8",
		"25.0.3+9":       "25",
		"21":             "21",
		"latest":         "",
		"":               "",
	}
	for in, want := range cases {
		if got := Major(in); got != want {
			t.Errorf("Major(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMajorInt(t *testing.T) {
	if got := MajorInt("21.0.2"); got != 21 {
		t.Errorf("MajorInt(21.0.2) = %d, want 21", got)
	}
	if got := MajorInt("nope"); got != 0 {
		t.Errorf("MajorInt(nope) = %d, want 0", got)
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// the reported bug: a Semeru respin is newer, not older
		{"8u482-b08.1", "8u482-b08", 1},
		{"8u482-b08", "8u482-b08.1", -1},
		// JDK build-number bumps must compare numerically
		{"25.0.3+10", "25.0.3+9", 1},
		{"25.0.3+329124", "25.0.3", 1},
		{"21.0.11+11", "21.0.11+10", 1},
		{"21.0.11+10", "21.0.11+10", 0},
		// zero-padding is irrelevant: b08 == b8
		{"8u482-b08", "8u482-b8", 0},
		// build numbers that would misorder under lexical compare
		{"8u482-b10", "8u482-b8", 1},
		// plain semver
		{"1.109.0", "1.108.0", 1},
		{"1.15.1", "1.15.0", 1},
		{"1.15.0", "1.15.0", 0},
		// base version dominates the build suffix
		{"11.0.32+9", "11.0.31+11", 1},
		// legacy jdk update level
		{"8u492-b09", "8u482-b08", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
		// antisymmetry
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d (antisymmetry)", c.b, c.a, got, -c.want)
		}
	}
}
