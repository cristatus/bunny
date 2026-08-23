package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// A config that says nothing about catalogs wires the public catalog, and only
// that: no checkout is implied.
func TestCatalogEntriesDefaultChain(t *testing.T) {
	p := paths.At(t.TempDir())
	entries := catalogEntries(&config.Config{}, p)
	if len(entries) != 1 {
		t.Fatalf("expected one catalog, got %d", len(entries))
	}
	if entries[0].remote == nil || entries[0].remote.URL() != catalog.DefaultRemoteURL {
		t.Errorf("the only catalog should be the default remote, got %+v", entries[0])
	}
	if entries[0].src.Name != config.DefaultCatalog {
		t.Errorf("name = %q, want %q", entries[0].src.Name, config.DefaultCatalog)
	}
}

func TestCatalogEntriesFromConfiguredCatalogs(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Catalogs: []config.Catalog{
		{Name: "axelor", Local: dir},
		{Name: "org", Remote: "https://example.com/org"},
		{Name: "upstream", Remote: "https://example.com/upstream"},
	}}
	entries := catalogEntries(cfg, paths.At(t.TempDir()))
	if len(entries) != 3 {
		t.Fatalf("expected three catalogs, got %d", len(entries))
	}
	for i, want := range []string{"axelor", "org", "upstream"} {
		if entries[i].src.Name != want {
			t.Errorf("catalog %d = %q, want %q (configuration order is the tie-break)", i, entries[i].src.Name, want)
		}
	}
	if !entries[0].present {
		t.Error("an existing checkout should be present")
	}
	if entries[1].remote.IndexPath() == entries[2].remote.IndexPath() {
		t.Errorf("two remotes share an index cache: %s", entries[1].remote.IndexPath())
	}
}

// A configured checkout that is not on disk stays in the list — doctor reports
// it — but has nothing to answer with.
func TestCatalogEntriesKeepsAbsentCheckout(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	cfg := &config.Config{Catalogs: []config.Catalog{
		{Name: "axelor", Local: missing},
	}}
	entries := catalogEntries(cfg, paths.At(t.TempDir()))
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].present {
		t.Error("a checkout that is not on disk must not report itself present")
	}
	if entries[0].location != missing {
		t.Errorf("location = %q, want %q", entries[0].location, missing)
	}
}

func TestLocalCatalogSelection(t *testing.T) {
	present := func(t *testing.T, name string) catalogEntry {
		t.Helper()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, catalog.PackagesDir), 0755); err != nil {
			t.Fatal(err)
		}
		l := catalog.NewLocal(dir)
		return catalogEntry{
			src: catalog.Source{Name: name, Loader: l}, local: l,
			present: true, location: dir,
		}
	}
	remote := func(name string) catalogEntry {
		r := catalog.NewRemote("https://example.com/c", t.TempDir())
		return catalogEntry{
			src: catalog.Source{Name: name, Loader: r}, remote: r,
			present: true, location: r.URL(),
		}
	}

	t.Run("the only checkout needs no name", func(t *testing.T) {
		a := &App{Paths: paths.At(t.TempDir()), catalogs: []catalogEntry{present(t, "axelor"), remote("upstream")}}
		got, err := a.localCatalog("")
		if err != nil {
			t.Fatal(err)
		}
		if got != a.catalogs[0].local {
			t.Error("wrong checkout")
		}
	})

	t.Run("several checkouts need one", func(t *testing.T) {
		a := &App{Paths: paths.At(t.TempDir()), catalogs: []catalogEntry{present(t, "axelor"), present(t, "vendored")}}
		_, err := a.localCatalog("")
		if err == nil || !strings.Contains(err.Error(), "--catalog") {
			t.Errorf("an ambiguous rewrite target must say how to choose: %v", err)
		}
		got, err := a.localCatalog("vendored")
		if err != nil {
			t.Fatal(err)
		}
		if got != a.catalogs[1].local {
			t.Error("--catalog picked the wrong checkout")
		}
	})

	t.Run("unknown name lists what there is", func(t *testing.T) {
		a := &App{Paths: paths.At(t.TempDir()), catalogs: []catalogEntry{present(t, "axelor")}}
		_, err := a.localCatalog("nope")
		if err == nil || !strings.Contains(err.Error(), "axelor") {
			t.Errorf("got %v", err)
		}
	})

	t.Run("remotes cannot be rewritten", func(t *testing.T) {
		a := &App{Paths: paths.At(t.TempDir()), catalogs: []catalogEntry{remote("upstream")}}
		if _, err := a.localCatalog(""); err == nil {
			t.Error("expected an error: there is no checkout to rewrite")
		}
		if _, err := a.localCatalog("upstream"); err == nil {
			t.Error("naming a remote must not pass for a checkout")
		}
	})
}

// Configured is not usable: a checkout that is not on disk is not a second
// catalog.
func TestMultiCatalogCountsUsableCatalogs(t *testing.T) {
	p := paths.At(t.TempDir())
	cloned := t.TempDir()
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"default config: the public catalog alone", &config.Config{}, false},
		{
			"a cloned checkout listed with a remote",
			&config.Config{Catalogs: []config.Catalog{
				{Name: "local", Local: cloned},
				{Name: "upstream", Remote: "https://example.com/c"},
			}},
			true,
		},
		{
			"one remote listed on its own",
			&config.Config{Catalogs: []config.Catalog{
				{Name: "upstream", Remote: "https://example.com/c"},
			}},
			false,
		},
		{
			"a checkout that is not there plus one remote",
			&config.Config{Catalogs: []config.Catalog{
				{Name: "axelor", Local: filepath.Join(cloned, "nope")},
				{Name: "upstream", Remote: "https://example.com/c"},
			}},
			false,
		},
		{
			"two remotes",
			&config.Config{Catalogs: []config.Catalog{
				{Name: "org", Remote: "https://example.com/org"},
				{Name: "upstream", Remote: "https://example.com/c"},
			}},
			true,
		},
	}
	for _, c := range cases {
		a := &App{Paths: p, catalogs: catalogEntries(c.cfg, p)}
		if got := a.multiCatalog(); got != c.want {
			t.Errorf("%s: multiCatalog = %v, want %v", c.name, got, c.want)
		}
	}
}

// The visible half of "any catalog can take over a package id": the switch is
// reported, never silent.
// noteApp builds an App whose rg was installed from `from`, with the given
// catalogs configured.
func noteApp(t *testing.T, from string, configured ...string) *App {
	t.Helper()
	p := paths.At(t.TempDir())
	entries := make([]catalogEntry, 0, len(configured))
	for _, name := range configured {
		r := catalog.NewRemote("https://example.com/"+name, t.TempDir())
		entries = append(entries, catalogEntry{src: catalog.Source{Name: name, Loader: r}, remote: r})
	}
	a := &App{Paths: p, State: state.Empty(), catalogs: entries}
	a.State.SetInstalled("rg", "1.0.0", "", "cli", "")
	a.State.SetSource("rg", from)
	return a
}

func TestSourceChangeNote(t *testing.T) {
	a := noteApp(t, "axelor", "upstream", "axelor")
	if got := a.sourceChangeNote("rg", "upstream"); !strings.Contains(got, "axelor") ||
		!strings.Contains(got, "upstream") || !strings.Contains(got, "rg") {
		t.Errorf("a changed source must name the package and both catalogs: %q", got)
	}
	for _, c := range []struct{ name, from, before string }{
		{"same catalog", "upstream", "upstream"},
		{"nothing recorded before", "axelor", ""},
		{"catalog cannot name itself now", "", "upstream"},
	} {
		a := noteApp(t, c.from, "upstream", "axelor")
		if got := a.sourceChangeNote("rg", c.before); got != "" {
			t.Errorf("%s: expected no note, got %q", c.name, got)
		}
	}
}

// Dropping the catalog that owned a package moves it to whoever is left, which
// is the same handover by another route.
func TestSourceChangeNoteReportsARemovedCatalog(t *testing.T) {
	a := noteApp(t, "upstream", "upstream")
	got := a.sourceChangeNote("rg", "company")
	if !strings.Contains(got, "no longer configured") || !strings.Contains(got, "company") {
		t.Errorf("got %q, want a note naming the catalog that is gone", got)
	}
}

func TestPackageSourcePrefersWhatWasInstalled(t *testing.T) {
	a := &App{State: state.Empty()}
	a.State.SetInstalled("rg", "14.1.0", "", "cli", "")
	a.State.SetSource("rg", "axelor")
	// What a package resolves to now can differ from where it came from; for a
	// package already on disk the recorded catalog is the true answer.
	if got := a.packageSource("rg", "upstream"); got != "axelor" {
		t.Errorf("got %q, want the recorded axelor", got)
	}
	if got := a.packageSource("absent", "upstream"); got != "upstream" {
		t.Errorf("a package not installed should report where it resolves: %q", got)
	}
}

// `dev --catalog` rewrites a checkout, so a remote's name and an absent
// checkout's name are both offers the command would then reject.
func TestCompletionCatalogsOffersOnlyUsableCheckouts(t *testing.T) {
	present, alsoPresent := t.TempDir(), t.TempDir()
	cfg := &config.Config{Catalogs: []config.Catalog{
		{Name: "axelor", Local: present},
		{Name: "upstream", Remote: "https://example.com/c"},
		{Name: "vendored", Local: alsoPresent},
		{Name: "uncloned", Local: filepath.Join(present, "nope")},
	}}
	a := &App{Paths: paths.At(t.TempDir()), catalogs: catalogEntries(cfg, paths.At(t.TempDir()))}
	got := a.completionCatalogs()
	if strings.Join(got, ",") != "axelor,vendored" {
		t.Errorf("completionCatalogs = %v, want [axelor vendored]", got)
	}
}
