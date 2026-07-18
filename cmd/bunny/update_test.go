package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/checker"
	"github.com/cristatus/bunny/internal/ui"
)

func TestBumpKind(t *testing.T) {
	cases := []struct{ cur, lat, want string }{
		{"1.106.0", "1.108.0", "minor"},
		{"3.6.1.85592", "3.6.2.85969", "patch"},
		{"21.0.11", "25.0.3", "major"},
	}
	for _, c := range cases {
		if got := bumpKind(c.cur, c.lat); got != c.want {
			t.Errorf("bumpKind(%q,%q) = %q, want %q", c.cur, c.lat, got, c.want)
		}
	}
}

func TestRenderUpdateTable(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	got := renderUpdateTable(p, []checker.Result{
		{ID: "glab", CurrentVersion: "1.106.0", LatestVersion: "1.108.0"},
	})
	if !strings.Contains(got, "glab") || !strings.Contains(got, "minor") {
		t.Fatalf("table = %q", got)
	}
}
