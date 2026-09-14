package manifest

import "testing"

func TestManSection(t *testing.T) {
	cases := map[string]string{
		"{app}/share/man/man1/foo.1":     "1",
		"{app}/share/man/man1/foo.1.gz":  "1",
		"{app}/share/man/man3/foo.3p":    "3p",
		"{app}/share/man/man8/foo.8x.gz": "8x",
		"{app}/share/man/foo":            "",
		"{app}/share/man/foo.txt":        "",
		"{app}/share/man/foo.0":          "", // section "0" is not a real man section
	}
	for path, want := range cases {
		if got := ManSection(path); got != want {
			t.Errorf("ManSection(%q) = %q, want %q", path, got, want)
		}
	}
}
