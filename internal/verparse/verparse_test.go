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
