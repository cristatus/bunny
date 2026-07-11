package checker

import "testing"

func TestExpandTemplate(t *testing.T) {
	cases := []struct {
		tmpl, version, want string
	}{
		{"https://x/{version}/foo.tar.gz", "1.2.3", "https://x/1.2.3/foo.tar.gz"},
		{"https://x/{major}.{minor}/{version}", "14.1.0", "https://x/14.1/14.1.0"},
		{"https://x/v{version0}/release", "22.10.0", "https://x/v22/release"},
		{"https://x/{patch}", "1.2", "https://x/"}, // no patch component
		{"", "1.0", ""},
	}
	for _, c := range cases {
		got := ExpandTemplate(c.tmpl, c.version)
		if got != c.want {
			t.Errorf("ExpandTemplate(%q, %q) = %q, want %q", c.tmpl, c.version, got, c.want)
		}
	}
}

func TestParseVersionParts(t *testing.T) {
	v := ParseVersion("1.2.3")
	if v.Major != "1" || v.Minor != "2" || v.Patch != "3" {
		t.Errorf("got %+v", v)
	}
	if len(v.Parts) != 3 {
		t.Errorf("parts: %v", v.Parts)
	}
}
