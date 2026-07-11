package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
)

func foojayTestServer(t *testing.T, idsBody string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/packages", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("version") != "21" || r.URL.Query().Get("distribution") != "temurin" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"result":[{"id":"abc","java_version":"21.0.11+10","size":207513939}]}`)
	})
	mux.HandleFunc("/ids/abc", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, idsBody)
	})
	return httptest.NewServer(mux)
}

func withFoojayBase(t *testing.T, base string) {
	t.Helper()
	old := foojayBaseURL
	foojayBaseURL = base
	t.Cleanup(func() { foojayBaseURL = old })
}

func TestFoojayCheckSha256(t *testing.T) {
	sum := strings.Repeat("a", 64)
	srv := foojayTestServer(t, `{"result":[{"direct_download_uri":"https://x/jdk21.tar.gz","checksum":"`+sum+`","checksum_type":"sha256"}]}`)
	defer srv.Close()
	withFoojayBase(t, srv.URL)

	r, err := (&Foojay{}).Check(context.Background(), &manifest.UpdateConfig{Distribution: "temurin"}, "21.0.10+7", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.LatestVersion != "21.0.11+10" || r.DownloadURL != "https://x/jdk21.tar.gz" || r.Size != 207513939 {
		t.Errorf("got %+v", r)
	}
	if r.HashAlgorithm != "sha256" || r.Hash != sum {
		t.Errorf("checksum: %+v", r)
	}
	if !r.HasUpdate {
		t.Error("expected HasUpdate (21.0.11 != 21.0.10)")
	}
}

func TestFoojayCheckNonSha256NoHash(t *testing.T) {
	srv := foojayTestServer(t, `{"result":[{"direct_download_uri":"https://x/jdk21.tar.gz","checksum":"abc","checksum_type":"sha1"}]}`)
	defer srv.Close()
	withFoojayBase(t, srv.URL)

	r, err := (&Foojay{}).Check(context.Background(), &manifest.UpdateConfig{Distribution: "temurin"}, "21.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Hash != "" || r.HashAlgorithm != "" {
		t.Errorf("sha1 must not be used as hash, got %+v", r)
	}
	if r.DownloadURL == "" {
		t.Error("DownloadURL should still be set")
	}
}

func TestFoojayEmptyResultErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/packages", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"result":[]}`) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	withFoojayBase(t, srv.URL)

	if _, err := (&Foojay{}).Check(context.Background(), &manifest.UpdateConfig{Distribution: "temurin"}, "21", ""); err == nil {
		t.Error("expected error on empty result")
	}
}

func TestFoojayRequiresDistribution(t *testing.T) {
	if _, err := (&Foojay{}).Check(context.Background(), &manifest.UpdateConfig{}, "21", ""); err == nil {
		t.Error("expected error when distribution is empty")
	}
}
