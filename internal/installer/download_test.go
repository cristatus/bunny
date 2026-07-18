package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func sha256Of(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestFetchFileURL(t *testing.T) {
	cache := t.TempDir()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "source.txt")
	content := "hello world"
	if err := os.WriteFile(srcPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	d := NewDownloader()
	got, err := d.Fetch(cache, Source{
		URL:    "file://" + srcPath,
		SHA256: sha256Of(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(got)
	if string(data) != content {
		t.Errorf("got %q", data)
	}
}

func TestFetchSkipsCacheHit(t *testing.T) {
	cache := t.TempDir()
	d := NewDownloader()
	src := Source{
		URL:    "file:///tmp/never-read",
		File:   "out",
		SHA256: sha256Of("preloaded"),
	}
	if err := os.WriteFile(filepath.Join(cache, "out"), []byte("preloaded"), 0644); err != nil {
		t.Fatal(err)
	}
	// Should not even try to fetch — file exists with matching checksum.
	got, err := d.Fetch(cache, src)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(got)
	if string(data) != "preloaded" {
		t.Errorf("got %q", data)
	}
}

func TestFetchChecksumMismatchRejects(t *testing.T) {
	cache := t.TempDir()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "x")
	os.WriteFile(srcPath, []byte("real content"), 0644)

	d := NewDownloader()
	_, err := d.Fetch(cache, Source{
		URL:    "file://" + srcPath,
		SHA256: sha256Of("WRONG"),
	})
	if err == nil {
		t.Fatal("expected checksum error")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
	// File should be cleaned up after mismatch.
	if _, err := os.Stat(filepath.Join(cache, "x")); err == nil {
		t.Error("file should be removed after checksum mismatch")
	}
}

func TestFetchSizeMismatchRejects(t *testing.T) {
	cache := t.TempDir()
	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "x")
	os.WriteFile(srcPath, []byte("real content"), 0644)

	d := NewDownloader()
	_, err := d.Fetch(cache, Source{
		URL:  "file://" + srcPath,
		Size: 1,
	})
	if err == nil {
		t.Fatal("expected size error")
	}
	if !strings.Contains(err.Error(), "size mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "x")); err == nil {
		t.Error("file should be removed after size mismatch")
	}
}

func TestFetchSkipsCacheHitWithSizeOnly(t *testing.T) {
	cache := t.TempDir()
	d := NewDownloader()
	src := Source{
		URL:  "file:///tmp/never-read",
		File: "out",
		Size: int64(len("preloaded")),
	}
	if err := os.WriteFile(filepath.Join(cache, "out"), []byte("preloaded"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := d.Fetch(cache, src)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(got)
	if string(data) != "preloaded" {
		t.Errorf("got %q", data)
	}
}

func TestFetchHTTPInjectable(t *testing.T) {
	cache := t.TempDir()
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("payload")), Header: make(http.Header)}, nil
	})}}
	got, err := d.Fetch(cache, Source{
		URL:    "https://example.com/x",
		File:   "x",
		SHA256: sha256Of("payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(got)
	if string(data) != "payload" {
		t.Errorf("got %q", data)
	}
}

func TestFetchAllOrdering(t *testing.T) {
	cache := t.TempDir()
	srcDir := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		os.WriteFile(filepath.Join(srcDir, n), []byte(n), 0644)
	}
	d := NewDownloader()
	paths, err := d.FetchAll(cache, []Source{
		{URL: "file://" + filepath.Join(srcDir, "a"), File: "a-out"},
		{URL: "file://" + filepath.Join(srcDir, "b"), File: "b-out"},
		{URL: "file://" + filepath.Join(srcDir, "c"), File: "c-out"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(paths[0]) != "a-out" || filepath.Base(paths[2]) != "c-out" {
		t.Errorf("ordering wrong: %v", paths)
	}
}

func TestFileNameFallback(t *testing.T) {
	u, _ := url.Parse("https://example.com/path/release.tar.gz")
	got, err := safeFileName(Source{URL: u.String()})
	if err != nil {
		t.Fatal(err)
	}
	if got != "release.tar.gz" {
		t.Errorf("got %q", got)
	}
	if got, err := safeFileName(Source{URL: "https://x", Name: "n"}); err != nil || got != "n" {
		t.Errorf("got %q", got)
	}
	if got, err := safeFileName(Source{URL: "https://x", File: "f"}); err != nil || got != "f" {
		t.Errorf("got %q", got)
	}
}

func TestFetchRejectsUnsafeFileName(t *testing.T) {
	d := NewDownloader()
	_, err := d.Fetch(t.TempDir(), Source{
		URL:  "file:///tmp/never",
		File: "../escape",
	})
	if err == nil {
		t.Fatal("expected unsafe filename error")
	}
	if !strings.Contains(err.Error(), "must not contain path separators") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFetchRejectsOversizedResponseWhileStreaming(t *testing.T) {
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("payload")), Header: make(http.Header)}, nil
	})}}
	_, err := d.Fetch(t.TempDir(), Source{URL: "https://example.com/x", File: "x", Size: 3})
	if err == nil || !strings.Contains(err.Error(), "exceeds expected size") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestFetchContextCancelsHTTPRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	d := &Downloader{Client: client}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := d.FetchContext(ctx, t.TempDir(), Source{URL: "https://example.com/x", File: "x"})
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("expected cancellation error, got %v", err)
	}
}

func TestFetchResumesPartialDownload(t *testing.T) {
	cache := t.TempDir()
	part := filepath.Join(cache, "x.part")
	if err := os.WriteFile(part, []byte("pay"), 0644); err != nil {
		t.Fatal(err)
	}
	var rangeHeader string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rangeHeader = req.Header.Get("Range")
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			ContentLength: 4,
			Body:          io.NopCloser(strings.NewReader("load")),
			Header:        make(http.Header),
		}, nil
	})}
	d := &Downloader{Client: client}
	got, err := d.Fetch(cache, Source{URL: "https://example.com/x", File: "x", Size: 7, SHA256: sha256Of("payload")})
	if err != nil {
		t.Fatal(err)
	}
	if rangeHeader != "bytes=3-" {
		t.Fatalf("Range = %q, want bytes=3-", rangeHeader)
	}
	data, err := os.ReadFile(got)
	if err != nil || string(data) != "payload" {
		t.Fatalf("download = %q, %v", data, err)
	}
}

func TestFetchReportsByteProgress(t *testing.T) {
	body := "abcdefghij" // 10 bytes
	d := &Downloader{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	var lastDone, lastTotal int64
	_, err := d.FetchAllContext(context.Background(), t.TempDir(),
		[]Source{{URL: "https://example.com/x", File: "x", Size: int64(len(body)), SHA256: sha256Of(body)}},
		func(done, total int64) { lastDone, lastTotal = done, total },
	)
	if err != nil {
		t.Fatal(err)
	}
	if lastTotal != int64(len(body)) {
		t.Errorf("total = %d, want %d", lastTotal, len(body))
	}
	if lastDone != int64(len(body)) {
		t.Errorf("final done = %d, want %d (should reach 100%%)", lastDone, len(body))
	}
}
