package checker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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

func withGitHubPages(t *testing.T, pages githubPages) {
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
