package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
)

// TestDebianCheckFlatRepoPrefersSupportedArch guards against a flat repo
// (root+dist, no component) that serves every architecture from one
// Packages index. Sublime's apt/stable repo lists arm64 before amd64 for
// the same version; without an architecture filter the tied-version
// comparison keeps whichever block scanned first, silently shipping an
// arm64 sha256/filename for what must be an amd64 manifest.
func TestDebianCheckFlatRepoPrefersSupportedArch(t *testing.T) {
	packages := `Package: sublime-text
Architecture: arm64
Version: 4213
Filename: files/sublime-text_build-4213_arm64.deb
SHA256: ` + wrongArchSHA + `
Size: 1111

Package: sublime-text
Architecture: amd64
Version: 4213
Filename: files/sublime-text_build-4213_amd64.deb
SHA256: ` + rightArchSHA + `
Size: 2222

`

	mux := http.NewServeMux()
	mux.HandleFunc("/apt/stable/Packages.gz", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/apt/stable/Packages", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, packages)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &manifest.UpdateConfig{Root: srv.URL, Dist: "apt/stable", PackageName: "sublime-text"}
	r, err := (&Debian{}).Check(context.Background(), cfg, "4200", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Hash != rightArchSHA {
		t.Errorf("Hash = %q, want amd64 sha %q", r.Hash, rightArchSHA)
	}
	if r.DownloadURL != srv.URL+"/files/sublime-text_build-4213_amd64.deb" {
		t.Errorf("DownloadURL = %q, want the amd64 asset", r.DownloadURL)
	}
	if r.Size != 2222 {
		t.Errorf("Size = %d, want 2222 (amd64 entry)", r.Size)
	}
}

const (
	wrongArchSHA = "1111111111111111111111111111111111111111111111111111111111111a"
	rightArchSHA = "2222222222222222222222222222222222222222222222222222222222222b"
)

// Any segment that was not purely numeric used to compare as a string, so
// 1.9 beat 1.10~rc1, an rc outranked its release, and epochs 9 and 10 came
// out backwards. The expectations are dpkg's (dpkg --compare-versions).
func TestCompareDebVersionsFollowsDpkg(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.10~rc1", "1.9", 1},
		{"1.2~rc1", "1.2", -1},
		{"~~", "~", -1},
		{"5:10.0", "5:9.0", 1},
		{"1:1.0", "2.0", 1},
		{"1.0-1", "1.0-2", -1},
		{"1.0a", "1.0", 1},
		{"1.0", "1.0+dfsg", -1},
		{"1.0.00", "1.0.0", 0},
		{"2.30.0-1ubuntu1", "2.30.0-1", 1},
	} {
		if got := compareDebVersions(c.a, c.b); sign(got) != c.want {
			t.Errorf("compareDebVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := compareDebVersions(c.b, c.a); sign(got) != -c.want {
			t.Errorf("compareDebVersions(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
