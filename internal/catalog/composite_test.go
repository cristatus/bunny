package catalog

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
)

type stubLoader struct {
	listed   []PackageInfo
	manifest *manifest.Manifest
	file     []byte
	err      error
}

func (s *stubLoader) List() ([]PackageInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.listed, nil
}
func (s *stubLoader) Load(id string) (*manifest.Manifest, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.manifest, nil
}
func (s *stubLoader) LoadFile(id, rel string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.file, nil
}

// Lookup answers from the stub's manifest when it has one, so a version-ranked
// test can set versions the way a real catalog would.
func (s *stubLoader) Lookup(id string) (PackageInfo, error) {
	if s.err != nil {
		return PackageInfo{}, s.err
	}
	if s.manifest != nil {
		return InfoOf(s.manifest), nil
	}
	return PackageInfo{ID: id}, nil
}

func src(name string, l Loader) Source { return Source{Name: name, Loader: l} }

func pkgStub(id, version string) *stubLoader {
	return &stubLoader{
		manifest: &manifest.Manifest{ID: id, Version: version},
		listed:   []PackageInfo{{ID: id, Version: version}},
	}
}

// mustNotAsk fails resolution if it is consulted at all: its error is neither
// ErrNotFound nor ErrUnavailable, so reaching it surfaces instead of being
// swallowed as a fall-through.
func mustNotAsk() *stubLoader { return &stubLoader{err: errors.New("must not be consulted")} }

// --- precedence ---

// Order is the whole answer: the first catalog carrying the package serves it,
// and the catalogs below it are never even asked.
func TestPriorityTakesTheFirstCatalogCarryingThePackage(t *testing.T) {
	c := NewComposite(src("company", pkgStub("rg", "13.0.0")), src("upstream", mustNotAsk()))
	m, err := c.Load("rg")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "13.0.0" {
		t.Errorf("got %s, want company's 13.0.0", m.Version)
	}
}

// The safety property: a lower-priority catalog cannot take over a package id
// by publishing a higher version, and listing agrees with loading about it.
func TestPriorityRefusesTakeoverByVersion(t *testing.T) {
	c := NewComposite(src("company", pkgStub("rg", "1.0.0")),
		src("upstream", pkgStub("rg", "99.0.0")),
	)
	m, err := c.Load("rg")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.0.0" {
		t.Errorf("Load got %s: a catalog below company must not win by version", m.Version)
	}
	pkgs, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected one entry, got %+v", pkgs)
	}
	if pkgs[0].Version != "1.0.0" || pkgs[0].Source != "company" {
		t.Errorf("List got %s from %q, want 1.0.0 from company", pkgs[0].Version, pkgs[0].Source)
	}
}

// A catalog that carries the package and then cannot produce it hands off rather
// than failing the install — the catalogs below it were never asked, so they are
// exactly the fallbacks — and the handle names who served.
func TestPriorityFallsBackWhenTheOwnerCannotServe(t *testing.T) {
	c := NewComposite(src("company", lookupOnly{pkgStub("rg", "1.0.0")}),
		src("upstream", pkgStub("rg", "99.0.0")),
	)
	pkg, err := c.Resolve("rg")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Source.Name != "company" {
		t.Errorf("resolution chose %q, want company", pkg.Source.Name)
	}
	m, err := pkg.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "99.0.0" {
		t.Errorf("got %s, want upstream's 99.0.0", m.Version)
	}
	if pkg.Source.Name != "upstream" {
		t.Errorf("served by %q, want upstream: provenance must follow the fallback", pkg.Source.Name)
	}
}

// Every catalog failing still reports the failure rather than a bare not-found.
func TestPriorityFallbackExhausted(t *testing.T) {
	c := NewComposite(src("company", lookupOnly{pkgStub("rg", "1.0.0")}),
		src("upstream", &stubLoader{err: fmt.Errorf("%w: https://down", ErrUnavailable)}),
	)
	if _, err := c.Load("rg"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("got %v, want ErrUnavailable", err)
	}
}

// --- listing ---

func TestCompositeListDedupsAndPrefersTheFirstCatalog(t *testing.T) {
	first := &stubLoader{listed: []PackageInfo{{ID: "a", Name: "a-first"}, {ID: "b"}}}
	second := &stubLoader{listed: []PackageInfo{{ID: "a", Name: "a-second"}, {ID: "c"}}}
	c := NewComposite(src("first", first), src("second", second))
	pkgs, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, p := range pkgs {
		byID[p.ID] = p.Name
	}
	if byID["a"] != "a-first" {
		t.Errorf("first should win for duplicate IDs, got %q", byID["a"])
	}
	if _, ok := byID["b"]; !ok {
		t.Error("missing b")
	}
	if _, ok := byID["c"]; !ok {
		t.Error("missing c")
	}
}

func TestCompositeListReturnsErrorWhenEveryLoaderFails(t *testing.T) {
	firstErr := errors.New("local unreadable")
	secondErr := errors.New("remote unavailable")
	c := NewComposite(src("first", &stubLoader{err: firstErr}),
		src("second", &stubLoader{err: secondErr}),
	)
	if _, err := c.List(); !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("expected joined loader errors, got %v", err)
	}
}

// --- falling through ---

func TestCompositeLoadFallsThroughOnNotFound(t *testing.T) {
	want := &manifest.Manifest{ID: "x"}
	c := NewComposite(src("first", &stubLoader{err: ErrNotFound}),
		src("second", &stubLoader{manifest: want}),
	)
	got, err := c.Load("x")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCompositeLoadStopsOnNonNotFoundError(t *testing.T) {
	parseErr := errors.New("yaml: unmarshal error")
	// A second loader that *would* succeed must not be consulted; otherwise a
	// corrupt local manifest could be silently replaced by remote content.
	c := NewComposite(src("first", &stubLoader{err: parseErr}),
		src("second", &stubLoader{manifest: &manifest.Manifest{ID: "should-not-be-returned"}}),
	)
	if _, err := c.Load("x"); !errors.Is(err, parseErr) {
		t.Errorf("expected parseErr to surface, got %v", err)
	}
}

func TestCompositeLoadFileFallsThroughOnNotFound(t *testing.T) {
	c := NewComposite(src("first", &stubLoader{err: ErrNotFound}),
		src("second", &stubLoader{file: []byte("ok")}),
	)
	got, err := c.LoadFile("x", "y")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ok" {
		t.Errorf("got %q", got)
	}
}

func TestCompositeLoadFileStopsWhenLocalPackageMissingSiblingFile(t *testing.T) {
	root := t.TempDir()
	writeTestManifest(t, filepath.Join(root, PackagesDir, "foo"), "foo", "Foo")

	local := NewLocal(root)
	remote := &stubLoader{file: []byte("remote-content-that-should-not-be-served")}
	c := NewComposite(src("local", local), src("remote", remote))

	_, err := c.LoadFile("foo", "prepare.sh")
	if err == nil {
		t.Fatal("expected missing local sibling file error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("missing sibling file in an existing local package must not be ErrNotFound: %v", err)
	}
}

func TestCompositeAllNotFoundReturnsLast(t *testing.T) {
	c := NewComposite(src("first", &stubLoader{err: ErrNotFound}),
		src("second", &stubLoader{err: ErrNotFound}),
	)
	if _, err := c.Load("x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

// The whole point of separating ErrUnavailable from ErrNotFound: a catalog that
// cannot answer must not hide a package a lower-priority one carries.
func TestCompositeUnavailableSourceFallsThrough(t *testing.T) {
	down := &stubLoader{err: fmt.Errorf("%w: https://down: dial tcp", ErrUnavailable)}
	c := NewComposite(src("down", down), src("up", pkgStub("rg", "14.1.0")))
	m, err := c.Load("rg")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "14.1.0" {
		t.Errorf("got %s", m.Version)
	}
}

func TestCompositeReportsUnavailableWhenNoSourceCanAnswer(t *testing.T) {
	down := &stubLoader{err: fmt.Errorf("%w: https://down: dial tcp", ErrUnavailable)}
	c := NewComposite(src("down", down))
	if _, err := c.Load("rg"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("got %v, want ErrUnavailable", err)
	}
}

// --- the resolved handle ---

// Binding is what keeps a manifest and its sibling files together: once a
// catalog has served, later loads do not walk the list again and land elsewhere.
func TestResolvedPackageBindsFilesToTheServingCatalog(t *testing.T) {
	steady := pkgStub("rg", "14.1.0")
	steady.file = []byte("steady-script")
	c := NewComposite(src("flaky", lookupOnly{pkgStub("rg", "14.2.0")}),
		src("steady", steady),
	)
	pkg, err := c.Resolve("rg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.LoadManifest(); err != nil {
		t.Fatal(err)
	}
	data, err := pkg.LoadFile("prepare.sh")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "steady-script" {
		t.Errorf("file came from %q, want the catalog that served the manifest", data)
	}
}

// ResolvePackage works on a catalog that reads one loader, leaving provenance
// empty because there is no configured name to report.
func TestResolvePackageOnASingleLoader(t *testing.T) {
	pkg, err := ResolvePackage(pkgStub("rg", "14.1.0"), "rg")
	if err != nil {
		t.Fatal(err)
	}
	m, err := pkg.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "14.1.0" {
		t.Errorf("got %s", m.Version)
	}
	if pkg.Source.Name != "" {
		t.Errorf("source = %q, want empty", pkg.Source.Name)
	}
}

// lookupOnly answers Lookup from its embedded stub but fails every fetch,
// standing in for a catalog that drops out after resolution.
type lookupOnly struct{ *stubLoader }

func (lookupOnly) Load(string) (*manifest.Manifest, error) {
	return nil, fmt.Errorf("%w: https://flaky: dial tcp", ErrUnavailable)
}
func (lookupOnly) LoadFile(string, string) ([]byte, error) {
	return nil, fmt.Errorf("%w: https://flaky: dial tcp", ErrUnavailable)
}
