package checker

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestParseChecksumPattern(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	formula := `require 'formula'

class Example < Formula
  url 'https://example.com/example.tar.gz'
  sha256 '` + hash + `'
end`

	got, algorithm, err := ParseChecksumPattern(formula, `sha256 '([a-f0-9]{64})'`)
	if err != nil {
		t.Fatal(err)
	}
	if got != hash || algorithm != "sha256" {
		t.Fatalf("got (%q, %q), want (%q, sha256)", got, algorithm, hash)
	}
}

func TestParseChecksumPatternRejectsUnmatchedDigest(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, _, err := ParseChecksumPattern("dependency "+hash, `sha256 '([a-f0-9]{64})'`); err == nil {
		t.Fatal("expected a digest outside the declared pattern to be rejected")
	}
}

// Electron update feeds (latest-*.yml) state the digest in base64, and it is
// mixed case, so the hex path's lowercasing has to not run first.
func TestParseChecksumPatternDecodesBase64(t *testing.T) {
	feed := `version: 2.8.1
files:
  - url: https://example.com/App.deb
    sha512: Q2ThRCZ/Bs6UVAujM+zyyNRyfjjBfzmM/j2ne5mHPFwXkt9SlzvTJsYdoT+7Hj+iyfmXTzmMhpMLS1BLv1QE+A==
    size: 122979528`
	const want = "4364e144267f06ce94540ba333ecf2c8d4727e38c17f398cfe3da77b99873c5c" +
		"1792df52973bd326c61da13fbb1e3fa2c9f9974f398c86930b4b504bbf5404f8"

	got, algorithm, err := ParseChecksumPattern(feed, `App\.deb\s+sha512: (\S+)`)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || algorithm != "sha512" {
		t.Fatalf("got (%q, %q), want (%q, sha512)", got, algorithm, want)
	}
}

func TestDecodeBase64DigestRejectsWrongLength(t *testing.T) {
	// Valid base64, but decodes to neither 32 nor 64 bytes.
	if _, _, ok := decodeBase64Digest("aGVsbG8="); ok {
		t.Fatal("expected a non-digest-length payload to be rejected")
	}
}

// A sums file lists every artifact of a release. When none of its lines names
// the target, the first line's hash belongs to some other file, and writing it
// into a manifest as the upstream checksum breaks every install.
func TestParseChecksumFileNeverBorrowsAnotherFilesHash(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	sums := a + "  tool-arm64.tar.gz\n" + b + "  tool-amd64.tar.gz\n"

	if hash, _, err := ParseChecksumFile(sums, "tool-amd64.tar.gz", "", nil); err != nil || hash != b {
		t.Errorf("named entry: got %q, %v; want %q", hash, err, b)
	}
	if hash, _, err := ParseChecksumFile(sums, "tool-amd64.tar.gz?raw=true", "", nil); err == nil {
		t.Errorf("no entry names the target, so no hash may be returned: got %q", hash)
	}
	// A per-file checksum naming the file differently still has only one
	// hash to give.
	if hash, _, err := ParseChecksumFile(a+"  renamed.tar.gz\n", "tool.tar.gz", "", nil); err != nil || hash != a {
		t.Errorf("single entry: got %q, %v; want %q", hash, err, a)
	}
}

// The npm registry answers HEAD without a Content-Length but reports the
// file's size in the Content-Range of a one-byte ranged GET.
func TestFetchFileSizeFallsBackToARangedGet(t *testing.T) {
	const size = 98152331
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Transfer-Encoding", "chunked")
			return
		}
		if r.Header.Get("Range") != "bytes=0-0" {
			t.Errorf("GET without a one-byte range: %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", size))
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte{0})
	}))
	defer srv.Close()

	got, err := FetchFileSize(t.Context(), srv.URL)
	if err != nil || got != size {
		t.Errorf("FetchFileSize = %d, %v; want %d", got, err, size)
	}
}

// A HEAD that carries Content-Length answers on its own, so no follow-up GET
// is made.
func TestFetchFileSizeUsesHeadContentLength(t *testing.T) {
	const size = 1234
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(size))
			return
		}
		gets++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	got, err := FetchFileSize(t.Context(), srv.URL)
	if err != nil || got != size {
		t.Errorf("FetchFileSize = %d, %v; want %d", got, err, size)
	}
	if gets != 0 {
		t.Errorf("follow-up GETs = %d, want 0", gets)
	}
}

// A server that ignores the range serves the whole file; its Content-Length is
// still the size.
func TestFetchFileSizeRangedGetThatIgnoresTheRange(t *testing.T) {
	const size = 512
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return // no Content-Length
		}
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.Write(make([]byte, size))
	}))
	defer srv.Close()

	got, err := FetchFileSize(t.Context(), srv.URL)
	if err != nil || got != size {
		t.Errorf("FetchFileSize = %d, %v; want %d", got, err, size)
	}
}

// An empty file rejects bytes=0-0 with 416; that is a size of 0, not an error.
func TestFetchFileSizeEmptyFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return // no Content-Length
		}
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer srv.Close()

	got, err := FetchFileSize(t.Context(), srv.URL)
	if err != nil || got != 0 {
		t.Errorf("FetchFileSize = %d, %v; want 0, nil", got, err)
	}
}

// A 206 with no readable total reports no size rather than failing.
func TestFetchFileSizeWithoutATotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return // no Content-Length
		}
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte{0})
	}))
	defer srv.Close()

	got, err := FetchFileSize(t.Context(), srv.URL)
	if err != nil || got != 0 {
		t.Errorf("FetchFileSize = %d, %v; want 0, nil", got, err)
	}
}
