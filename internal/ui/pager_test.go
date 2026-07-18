package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestPageNeverWritesDirectly(t *testing.T) {
	var out bytes.Buffer
	if err := Page(&out, "one\ntwo\n", "never"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "one\ntwo\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPageAutoDoesNotPageNonTerminal(t *testing.T) {
	t.Setenv("BUNNY_PAGER", "false")
	var out bytes.Buffer
	input := strings.Repeat("line\n", 100)
	if err := Page(&out, input, "auto"); err != nil {
		t.Fatal(err)
	}
	if out.String() != input {
		t.Fatalf("auto output was not written directly")
	}
}

func TestPageAlwaysDoesNotPageNonTerminal(t *testing.T) {
	t.Setenv("BUNNY_PAGER", "false")
	var out bytes.Buffer
	if err := Page(&out, "paged\n", "always"); err != nil {
		t.Fatal(err)
	}
	if out.String() != "paged\n" {
		t.Fatalf("redirected output = %q", out.String())
	}
}

func TestPagerCommandPrefersBunnyPager(t *testing.T) {
	t.Setenv("BUNNY_PAGER", "less -SR")
	t.Setenv("PAGER", "more")
	command, custom, ok := pagerCommand()
	if command != "less -SR" || !custom || !ok {
		t.Fatalf("pagerCommand() = %q, %v, %v", command, custom, ok)
	}
}
