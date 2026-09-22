package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

// When the primary rewrite is refused (here an upstream version a manifest
// cannot hold), the secondary sources must not be rewritten either. They
// used to be, leaving the manifest bumped against its old version, again on
// every run.
func TestDevUpdateSkipsSecondariesWhenThePrimaryRewriteFails(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := map[string]string{"/primary.json": "1:2.0", "/plugin.json": "2.0"}[r.URL.Path]
		if version == "" { // the download itself, for its size
			w.Header().Set("Content-Length", "1")
			return
		}
		fmt.Fprintf(w, `{"version": %q, "url": %q, "sha256": %q}`, version, srv.URL+"/file"+r.URL.Path, strings.Repeat("b", 64))
	}))
	defer srv.Close()

	checkout := filepath.Join(t.TempDir(), "catalog")
	dir := filepath.Join(checkout, catalog.PackagesDir, "tool")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	source := func(name string) string {
		return fmt.Sprintf(`  - url: "https://x/%[1]s-1.0.tgz"
    file: "%[1]s.tgz"
    sha256: "%[2]s"
    update: {type: json, url: "%[3]s/%[1]s.json", version-query: version, url-query: url, hash-query: sha256}
`, name, strings.Repeat("a", 64), srv.URL)
	}
	manifest := "id: tool\nname: Tool\nversion: \"1.0\"\nsources:\n" + source("primary") + source("plugin") +
		"bin:\n  - {name: tool, path: \"{app}/tool\"}\n"
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "index.json"), []byte(`{"version":1,"packages":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	a := &App{Paths: paths.At(t.TempDir()), State: state.Empty()}
	if err := writeUpdates(context.Background(), a, catalog.NewLocal(checkout), ""); err == nil {
		t.Error("the refused primary rewrite must be reported")
	}
	if got, _ := os.ReadFile(path); string(got) != manifest {
		t.Errorf("no source may be rewritten when the primary rewrite fails:\n%s", got)
	}
}
