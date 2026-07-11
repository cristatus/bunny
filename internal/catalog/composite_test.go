package catalog

import (
	"errors"
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

func TestCompositeListDedupsAndPrefersFirst(t *testing.T) {
	first := &stubLoader{listed: []PackageInfo{{ID: "a", Name: "a-first"}, {ID: "b"}}}
	second := &stubLoader{listed: []PackageInfo{{ID: "a", Name: "a-second"}, {ID: "c"}}}
	c := NewComposite(first, second)
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
	c := NewComposite(&stubLoader{err: firstErr}, &stubLoader{err: secondErr})
	if _, err := c.List(); !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("expected joined loader errors, got %v", err)
	}
}

func TestCompositeLoadFallsThroughOnNotFound(t *testing.T) {
	want := &manifest.Manifest{ID: "x"}
	first := &stubLoader{err: ErrNotFound}
	second := &stubLoader{manifest: want}
	c := NewComposite(first, second)
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
	first := &stubLoader{err: parseErr}
	// A second loader that *would* succeed must not be consulted; otherwise a
	// corrupt local manifest could be silently replaced by remote content.
	second := &stubLoader{manifest: &manifest.Manifest{ID: "should-not-be-returned"}}
	c := NewComposite(first, second)
	_, err := c.Load("x")
	if !errors.Is(err, parseErr) {
		t.Errorf("expected parseErr to surface, got %v", err)
	}
}

func TestCompositeLoadFileFallsThroughOnNotFound(t *testing.T) {
	first := &stubLoader{err: ErrNotFound}
	second := &stubLoader{file: []byte("ok")}
	c := NewComposite(first, second)
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
	writeTestManifest(t, filepath.Join(root, "cli", "foo"), "foo", "Foo")

	local := NewLocal(root)
	remote := &stubLoader{file: []byte("remote-content-that-should-not-be-served")}
	c := NewComposite(local, remote)

	_, err := c.LoadFile("foo", "prepare.sh")
	if err == nil {
		t.Fatal("expected missing local sibling file error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("missing sibling file in an existing local package must not be ErrNotFound: %v", err)
	}
}

func TestCompositeAllNotFoundReturnsLast(t *testing.T) {
	c := NewComposite(&stubLoader{err: ErrNotFound}, &stubLoader{err: ErrNotFound})
	_, err := c.Load("x")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v", err)
	}
}
