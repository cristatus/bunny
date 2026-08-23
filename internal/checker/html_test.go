package checker

import (
	"regexp"
	"testing"
)

func TestNewestMatch(t *testing.T) {
	// A Maven repository metadata listing: oldest first, and <release> may name
	// a milestone the pattern deliberately skips.
	mavenMetadata := `<metadata>
  <versioning>
    <release>4.2.0-M1</release>
    <versions>
      <version>1.5.10.RELEASE</version>
      <version>4.0.8</version>
      <version>4.1.0</version>
      <version>4.1.1</version>
      <version>4.2.0-M1</version>
    </versions>
  </versioning>
</metadata>`

	cases := []struct {
		name, pattern, body, want string
	}{
		{
			"maven metadata skips milestones",
			`<version>([0-9]+(?:\.[0-9]+){2})</version>`,
			mavenMetadata,
			"4.1.1",
		},
		{
			"directory index, newest last",
			`href="v(10\.[0-9.]+)/"`,
			`<a href="v10.1.9/">10.1.9</a><a href="v10.1.10/">10.1.10</a>`,
			"10.1.10",
		},
		{
			"directory index, newest first",
			`href="([0-9.]+)/"`,
			`<a href="3.9.16/">3.9.16</a><a href="3.8.8/">3.8.8</a>`,
			"3.9.16",
		},
		{
			"no match",
			`<version>([0-9.]+)</version>`,
			`<p>nothing here</p>`,
			"",
		},
	}
	for _, c := range cases {
		got := newestMatch(regexp.MustCompile(c.pattern), c.body)
		if got != c.want {
			t.Errorf("%s: newestMatch = %q, want %q", c.name, got, c.want)
		}
	}
}
