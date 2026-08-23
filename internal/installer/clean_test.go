package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// setup builds a Cleaner over a temp $BUNNY_HOME with:
//   - app `keep` installed at v1.0, expects cache file "keep-1.0.tar.gz"
//   - app `gone` not installed but with a leftover cache dir
//   - the download cache for `keep` has both the current file and a stale older one
//   - a staged tree left behind by a crashed install
func setup(t *testing.T) (*Cleaner, *paths.Paths) {
	t.Helper()
	root := t.TempDir()
	p := paths.At(root)

	st := state.Empty()
	st.SetInstalled("keep", "1.0", "", "", "")

	cat := &fakeCatalog{
		manifests: map[string]*manifest.Manifest{
			"keep": {
				ID:      "keep",
				Version: "1.0",
				Sources: []manifest.Source{{File: "keep-{version}.tar.gz", URL: "https://x"}},
			},
		},
	}

	// Populate cache.
	must(t, os.MkdirAll(p.AppDownloadCache("keep"), 0755))
	must(t, os.WriteFile(filepath.Join(p.AppDownloadCache("keep"), "keep-1.0.tar.gz"), []byte("current"), 0644))
	must(t, os.WriteFile(filepath.Join(p.AppDownloadCache("keep"), "keep-0.9.tar.gz"), []byte("stale older"), 0644))

	must(t, os.MkdirAll(p.AppDownloadCache("gone"), 0755))
	must(t, os.WriteFile(filepath.Join(p.AppDownloadCache("gone"), "gone-1.0.tar.gz"), []byte("orphan"), 0644))

	// A catalog's own cache — kept unless --all, and not a package download dir
	// despite sharing the cache root.
	must(t, os.MkdirAll(p.CatalogCache("upstream"), 0755))
	must(t, os.WriteFile(filepath.Join(p.CatalogCache("upstream"), "index.json"), []byte("{}"), 0644))

	// A top-level cache file — left alone unless --all.
	must(t, os.WriteFile(filepath.Join(p.Cache(), "index.json"), []byte("{}"), 0644))

	// Tmp dir with leftover.
	must(t, os.MkdirAll(filepath.Join(p.Staging(manifest.KindCLI), "abandoned"), 0755))
	must(t, os.WriteFile(filepath.Join(p.Staging(manifest.KindCLI), "abandoned", "marker"), []byte("x"), 0644))

	return NewCleaner(p, cat, st), p
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestCleanDefaultPrunesStaleAndOrphans(t *testing.T) {
	c, p := setup(t)
	r, err := c.Clean("", false)
	if err != nil {
		t.Fatal(err)
	}

	// Stale older file gone, current kept.
	if exists(filepath.Join(p.AppDownloadCache("keep"), "keep-0.9.tar.gz")) {
		t.Error("expected stale file to be removed")
	}
	if !exists(filepath.Join(p.AppDownloadCache("keep"), "keep-1.0.tar.gz")) {
		t.Error("expected current file to be kept")
	}
	// Orphan dir removed entirely.
	if exists(p.AppDownloadCache("gone")) {
		t.Error("expected orphan cache dir to be removed")
	}
	// Tmp swept.
	if exists(filepath.Join(p.Staging(manifest.KindCLI), "abandoned")) {
		t.Error("expected tmp leftover to be removed")
	}
	// A live catalog cache is preserved (no --all): not a package to prune.
	if !exists(filepath.Join(p.CatalogCache("upstream"), "index.json")) {
		t.Error("expected the catalog index cache to be preserved without --all")
	}
	if !exists(filepath.Join(p.Cache(), "index.json")) {
		t.Error("expected a top-level cache file to be preserved without --all")
	}

	if r.Bytes <= 0 {
		t.Error("expected to free non-zero bytes")
	}
}

func TestCleanAllWipesEverything(t *testing.T) {
	c, p := setup(t)
	if _, err := c.Clean("", true); err != nil {
		t.Fatal(err)
	}

	if exists(filepath.Join(p.AppDownloadCache("keep"), "keep-1.0.tar.gz")) {
		t.Error("expected current file to also be removed under --all")
	}
	if exists(p.AppDownloadCache("gone")) {
		t.Error("expected orphan to be gone")
	}
	if exists(filepath.Join(p.Cache(), "index.json")) {
		t.Error("expected index.json to be removed under --all")
	}
	if exists(p.CatalogCache("upstream")) {
		t.Error("expected the catalog index cache to be removed under --all")
	}
}

func TestCleanScopedToOneApp(t *testing.T) {
	c, p := setup(t)
	if _, err := c.Clean("gone", false); err != nil {
		t.Fatal(err)
	}

	// Only `gone` and its tmp are touched.
	if exists(p.AppDownloadCache("gone")) {
		t.Error("expected gone cache to be removed")
	}
	if !exists(filepath.Join(p.AppDownloadCache("keep"), "keep-0.9.tar.gz")) {
		t.Error("expected keep's stale file to remain when scoped to gone")
	}
	if !exists(filepath.Join(p.Staging(manifest.KindCLI), "abandoned")) {
		t.Error("expected unrelated tmp to remain when scoped to gone")
	}
}

func TestCleanScopedAllForcesWipe(t *testing.T) {
	c, p := setup(t)
	if _, err := c.Clean("keep", true); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(p.AppDownloadCache("keep"), "keep-1.0.tar.gz")) {
		t.Error("expected current file to be wiped under --all even when installed")
	}
}

func TestCleanRejectsInvalidScopedID(t *testing.T) {
	c, _ := setup(t)
	if _, err := c.Clean("../escape", false); err == nil {
		t.Fatal("expected invalid package id error")
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{int64(1024 * 1024 * 1024), "1.0 GiB"},
	}
	for _, c := range cases {
		got := FormatBytes(c.in)
		if got != c.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
