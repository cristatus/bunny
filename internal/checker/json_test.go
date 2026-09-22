package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
)

// An API publishing its version as a JSON number decoded it to float64, so
// 3.10 came out as "3.1", compared below the installed 3.9, and the update
// was missed.
func TestJSONCheckerKeepsANumericVersionsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"releases": [{"stable": 1, "version": 3.10}]}`)
	}))
	defer srv.Close()

	cfg := &manifest.UpdateConfig{Type: "json", URL: srv.URL, VersionQuery: "releases[?(@.stable==1)].version"}
	r, err := (&JSON{}).Check(context.Background(), cfg, "3.9", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.LatestVersion != "3.10" || !r.HasUpdate {
		t.Errorf("got %q (update %v), want 3.10 as an update over 3.9", r.LatestVersion, r.HasUpdate)
	}
}
