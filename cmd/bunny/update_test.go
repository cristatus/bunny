package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/checker"
	"github.com/cristatus/bunny/internal/state"
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

// --apply with nothing to reinstall answers the question that was asked. An
// empty progress list would print its leading blank above the summary's own,
// and "updated 0 packages" does not tell the reader they are up to date.
func TestApplyWithoutUpdatesReportsUpToDate(t *testing.T) {
	st := state.Empty()
	st.SetInstalled("tool", "1.0", "", "", "")
	a := &App{State: st, Catalog: reportCatalog{
		packages: []catalog.PackageInfo{{ID: "tool", Version: "1.0"}},
	}}

	out := captureStdout(t, func() {
		if err := (&UpdateCmd{Apply: true}).apply(a); err != nil {
			t.Fatal(err)
		}
	})
	if out != "\nall packages are up to date\n" {
		t.Fatalf("apply output = %q", out)
	}
}

// captureStdout collects what fn writes to stdout, which the result printers
// bind directly.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()

	fn()
	w.Close()
	var b bytes.Buffer
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	r.Close()
	return b.String()
}
