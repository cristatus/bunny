package main

import (
	"bytes"
	"context"
	"os"
	"slices"
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

// A package that is not installed has no update to report or apply. The check
// used to answer "all packages are up to date" for it while --apply refused.
func TestUpdateRefusesAPackageThatIsNotInstalled(t *testing.T) {
	st := state.Empty()
	st.SetInstalled("tool", "1.0", "", "", "")
	a := &App{State: st, Catalog: reportCatalog{
		packages: []catalog.PackageInfo{{ID: "tool", Version: "1.0"}, {ID: "idea", Version: "2"}},
	}}
	for _, apply := range []bool{false, true} {
		out := captureStdout(t, func() {
			err := (&UpdateCmd{ID: "idea", Apply: apply}).Run(a)
			if err == nil || !strings.Contains(err.Error(), "not installed") {
				t.Errorf("apply=%v: got %v, want a not-installed error", apply, err)
			}
		})
		if strings.Contains(out, "up to date") {
			t.Errorf("apply=%v: reported %q for a package that is not installed", apply, out)
		}
	}
}

// With one catalog down, packages from it are missing from the listing. The
// check used to skip them and could answer "all packages are up to date".
// They are failures now; a package its own, answering catalog dropped is not,
// and updates found elsewhere are still reported.
func TestUpdateCheckReportsPackagesItCouldNotCheck(t *testing.T) {
	st := state.Empty()
	for _, p := range []struct{ id, version, source string }{
		{"rg", "14.0", "upstream"},
		{"tsh", "17.0", "company"}, // its catalog is down
		{"legacy", "1.0", ""},      // installed before sources were recorded
		{"gone", "1.0", "upstream"},
	} {
		st.SetInstalled(p.id, p.version, "", "", "")
		st.SetSource(p.id, p.source)
	}
	partial := &catalog.PartialError{Catalogs: []string{"company"}, Err: catalog.ErrUnavailable}
	a := &App{State: st, Catalog: reportCatalog{
		packages: []catalog.PackageInfo{{ID: "rg", Version: "15.0"}},
		err:      partial,
	}}

	report, err := a.checkUpdates(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].ID != "rg" {
		t.Errorf("results = %v, want rg's update from the catalog that answered", report.Results)
	}
	var unchecked []string
	for _, f := range report.Failures {
		unchecked = append(unchecked, strings.SplitN(f.Error(), ":", 2)[0])
	}
	if !slices.Equal(unchecked, []string{"legacy", "tsh"}) {
		t.Errorf("unchecked = %v, want [legacy tsh]", unchecked)
	}

	out := captureStdout(t, func() {
		if err := (&UpdateCmd{}).check(a); err == nil {
			t.Error("the check must fail when packages could not be checked")
		}
	})
	if !strings.Contains(out, "rg") || strings.Contains(out, "up to date") {
		t.Errorf("check output = %q, want rg's update and no up-to-date claim", out)
	}
}

// With its own catalog down, a package listed only by a lower catalog was
// compared against that copy: 22.5 from B "updated" to C's 22.3, and --apply
// would install it and switch catalogs. It is unchecked instead.
func TestUpdateCheckIgnoresAnotherCatalogsCopyWhileTheOwnerIsDown(t *testing.T) {
	st := state.Empty()
	st.SetInstalled("node-22", "22.5", "", "", "")
	st.SetSource("node-22", "company")
	a := &App{State: st, Catalog: reportCatalog{
		packages: []catalog.PackageInfo{{ID: "node-22", Version: "22.3", Source: "upstream"}},
		err:      &catalog.PartialError{Catalogs: []string{"company"}, Err: catalog.ErrUnavailable},
	}}
	report, err := a.checkUpdates(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 0 {
		t.Errorf("results = %v, want no update offered from another catalog", report.Results)
	}
	if len(report.Failures) != 1 || !strings.HasPrefix(report.Failures[0].Error(), "node-22:") {
		t.Errorf("failures = %v, want node-22 reported as unchecked", report.Failures)
	}
}
