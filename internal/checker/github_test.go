package checker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
)

// githubPages serves canned github.com pages to every checker request.
type githubPages map[string]string

func (p githubPages) RoundTrip(r *http.Request) (*http.Response, error) {
	body, ok := p[r.URL.Path]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
		Request:    r,
	}, nil
}

func withGitHubPages(t *testing.T, pages http.RoundTripper) {
	t.Helper()
	prev := httpClient
	httpClient = &http.Client{Transport: pages}
	t.Cleanup(func() { httpClient = prev })
}

// When /releases/latest is outside tag-pattern, older releases are searched
// with the manifest's asset pattern. Six catalog manifests template that
// pattern with {version}, which the search compiled literally, so it never
// matched and the checker reported "no update".
func TestSearchReleasesExpandsTheVersionInTheAssetPattern(t *testing.T) {
	withGitHubPages(t, githubPages{
		"/scala/scala3/releases": `<a href="/scala/scala3/releases/tag/3.8.0-RC1">` +
			`<a href="/scala/scala3/releases/tag/3.7.2">`,
		"/scala/scala3/releases/expanded_assets/3.8.0-RC1": `<li><a href="/scala/scala3/releases/download/3.8.0-RC1/scala3-3.8.0-RC1.tar.gz">`,
		"/scala/scala3/releases/expanded_assets/3.7.2":     `<li><a href="/scala/scala3/releases/download/3.7.2/scala3-3.7.2.tar.gz">`,
	})
	tag, assets := (&GitHub{}).searchReleases(context.Background(), "scala/scala3",
		`^(\d+\.\d+\.\d+)$`, `^scala3-{version}\.tar\.gz$`)
	if tag != "3.7.2" || len(assets) != 1 || assets[0].Filename != "scala3-3.7.2.tar.gz" {
		t.Errorf("searchReleases = %q, %v; want the 3.7.2 release", tag, assets)
	}
}

// When the latest release lacks the asset and an older one supplies it, the
// checksum has to come from that older release too. The lookup searched the
// latest release's assets, whose SHA256SUMS describes other files: with a
// single entry, its one hash was taken for the older asset.
func TestGitHubChecksumComesFromTheReleaseThatHasTheAsset(t *testing.T) {
	want, other := strings.Repeat("a", 64), strings.Repeat("b", 64)
	pages := githubPages{
		"/o/tool/releases": `<a href="/o/tool/releases/tag/v4.0"><a href="/o/tool/releases/tag/v3.9">`,
		"/o/tool/releases/expanded_assets/v4.0": `<li><a href="/o/tool/releases/download/v4.0/tool-4.0-arm64.tgz">` +
			`<li><a href="/o/tool/releases/download/v4.0/SHA256SUMS">`,
		"/o/tool/releases/expanded_assets/v3.9": `<li><a href="/o/tool/releases/download/v3.9/tool-3.9.tgz">` +
			`<li><a href="/o/tool/releases/download/v3.9/SHA256SUMS">`,
		"/o/tool/releases/download/v4.0/SHA256SUMS":   other + "  tool-4.0-arm64.tgz\n",
		"/o/tool/releases/download/v3.9/SHA256SUMS":   want + "  tool-3.9.tgz\n",
		"/o/tool/releases/download/v3.9/tool-3.9.tgz": "x",
		"/o/tool/releases/tag/v4.0":                   "",
	}
	withGitHubPages(t, latestRedirect{pages, "/o/tool/releases/tag/v4.0"})

	cfg := &manifest.UpdateConfig{Type: "github", Repo: "o/tool", TagPattern: `^v(\d+\.\d+)$`, Asset: `^tool-{version}\.tgz$`}
	r, err := (&GitHub{}).Check(context.Background(), cfg, "3.8", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.LatestVersion != "3.9" || r.Hash != want {
		t.Errorf("got version %q hash %q, want 3.9 with the hash from its own release", r.LatestVersion, r.Hash)
	}
}

// latestRedirect answers /releases/latest the way github.com does, with a
// redirect to the latest tag's page.
type latestRedirect struct {
	pages githubPages
	tag   string
}

func (l latestRedirect) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/releases/latest") {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://github.com" + l.tag}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    r,
		}, nil
	}
	return l.pages.RoundTrip(r)
}
