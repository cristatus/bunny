package checker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/manifest"
)

func init() { Register(&GitHub{}) }

// GitHub fetches the latest release tag and matching asset from a repo.
type GitHub struct{}

func (g *GitHub) Type() string { return "github" }

func (g *GitHub) Check(ctx context.Context, cfg *manifest.UpdateConfig, currentVersion, sourceURL string) (*Result, error) {
	if cfg.Repo == "" {
		return nil, fmt.Errorf("github checker requires 'repo'")
	}

	tag, err := g.latestTag(ctx, cfg.Repo)
	if err != nil {
		return nil, err
	}

	// When tag-pattern is set, /releases/latest can return a tag from outside
	// the constrained range — e.g., graalvm-ce-builds holds every JDK series
	// in one repo, so a manifest pinned to 'jdk-21\.…' must reject the
	// jdk-25.x latest. Search older releases for the most recent matching tag,
	// and if nothing matches (the constrained major hasn't shipped recently
	// enough to appear on the first page of /releases) signal "no update
	// available" instead of falling through with the unrelated tag — without
	// this guard, extractVersion's fallback would emit the raw upstream tag
	// as if it were the new version.
	var assets []ghAsset
	if cfg.TagPattern != "" {
		if re, err := regexp.Compile(cfg.TagPattern); err == nil && !re.MatchString(tag) {
			altTag, altAssets := g.searchReleases(ctx, cfg.Repo, cfg.TagPattern, cfg.Asset)
			if altTag == "" {
				return &Result{
					CurrentVersion: currentVersion,
					LatestVersion:  currentVersion,
					HasUpdate:      false,
				}, nil
			}
			tag = altTag
			assets = altAssets
		}
	}

	version := g.extractVersion(tag, cfg.TagPattern)
	log.Debug("GitHub version", "tag", tag, "version", version)

	r := &Result{LatestVersion: version, HasUpdate: version != currentVersion}

	if assets == nil {
		assets, err = g.fetchAssets(ctx, cfg.Repo, tag)
		if err != nil {
			return r, nil
		}
	}
	asset := g.findAsset(assets, cfg.Asset, version)

	// Search older releases when latest doesn't have the asset we want.
	if asset == nil && cfg.TagPattern != "" && cfg.Asset != "" {
		if altTag, altAssets := g.searchReleases(ctx, cfg.Repo, cfg.TagPattern, cfg.Asset); altTag != "" {
			tag = altTag
			version = g.extractVersion(tag, cfg.TagPattern)
			r.LatestVersion = version
			r.HasUpdate = version != currentVersion
			asset = g.findAsset(altAssets, cfg.Asset, version)
		}
	}

	if asset != nil {
		r.DownloadURL = asset.URL
		if asset.Digest != "" {
			r.Hash = asset.Digest
			r.HashAlgorithm = "sha256"
		} else {
			if hash, algo := g.findChecksum(ctx, asset.Filename, assets); hash != "" {
				r.Hash = hash
				r.HashAlgorithm = algo
			}
		}
	}

	if cfg.URLTemplate != "" {
		r.DownloadURL = ExpandTemplate(cfg.URLTemplate, version)
	}
	if r.Hash == "" && cfg.HashURL != "" {
		hashURL := ExpandTemplate(cfg.HashURL, version)
		var target string
		if r.DownloadURL != "" {
			target = filepath.Base(r.DownloadURL)
		} else if sourceURL != "" {
			target = filepath.Base(ExpandTemplate(sourceURL, version))
		}
		if target != "" {
			if hash, algo, err := FetchChecksumFromURL(ctx, hashURL, target); err == nil {
				r.Hash = hash
				r.HashAlgorithm = algo
			}
		}
	}
	if r.DownloadURL != "" {
		if size, err := FetchFileSize(ctx, r.DownloadURL); err == nil && size > 0 {
			r.Size = size
		}
	}
	return r, nil
}

func (g *GitHub) latestTag(ctx context.Context, repo string) (string, error) {
	u := fmt.Sprintf("https://github.com/%s/releases/latest", repo)
	var finalURL string
	client := &http.Client{
		Timeout: httpClient.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			finalURL = req.URL.String()
			return nil
		},
	}
	resp, err := doRequestWithClient(ctx, client, http.MethodGet, u)
	if err != nil {
		return "", fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github status %d", resp.StatusCode)
	}
	if finalURL == "" {
		finalURL = resp.Request.URL.String()
	}
	return tagFromURL(finalURL)
}

func (g *GitHub) extractVersion(tag, pattern string) string {
	if pattern != "" {
		if re, err := regexp.Compile(pattern); err == nil {
			if m := re.FindStringSubmatch(tag); len(m) >= 2 {
				return m[1]
			}
		}
	}
	return strings.TrimPrefix(tag, "v")
}

type ghAsset struct {
	URL, Filename, Digest string
}

func (g *GitHub) fetchAssets(ctx context.Context, repo, tag string) ([]ghAsset, error) {
	encoded := strings.ReplaceAll(url.PathEscape(tag), "+", "%2B")
	u := fmt.Sprintf("https://github.com/%s/releases/expanded_assets/%s", repo, encoded)
	resp, err := doRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := readBodyLimited(resp.Body)
	if err != nil {
		return nil, err
	}
	return g.parseAssets(string(body), tag), nil
}

func (g *GitHub) parseAssets(html, tag string) []ghAsset {
	encoded := strings.ReplaceAll(url.PathEscape(tag), "+", "%2B")
	tagEsc := regexp.QuoteMeta(encoded)
	ghRe := regexp.MustCompile(`<a[^>]*href="(/[^/]+/[^/]+/releases/download/` + tagEsc + `/([^"]+))"`)
	extRe := regexp.MustCompile(`<a[^>]*href="(https?://[^"]+/([^"/]+))"`)
	sumRe := regexp.MustCompile(`<span[^>]*class="[^"]*Truncate-text[^"]*"[^>]*>sha256:([a-fA-F0-9]{64})</span>`)

	var out []ghAsset
	for _, block := range strings.Split(html, "<li")[1:] {
		var a ghAsset
		if m := ghRe.FindStringSubmatch(block); len(m) >= 3 {
			a.URL = "https://github.com" + m[1]
			if d, err := url.PathUnescape(m[2]); err == nil {
				a.Filename = d
			} else {
				continue
			}
		} else if m := extRe.FindStringSubmatch(block); len(m) >= 3 {
			a.URL = m[1]
			a.Filename = m[2]
		} else {
			continue
		}
		if m := sumRe.FindStringSubmatch(block); len(m) >= 2 {
			a.Digest = strings.ToLower(m[1])
		}
		out = append(out, a)
	}
	return out
}

func (g *GitHub) findAsset(assets []ghAsset, pattern, version string) *ghAsset {
	if len(assets) == 0 {
		return nil
	}
	if pattern == "" {
		return &assets[0]
	}
	p := strings.ReplaceAll(pattern, "{version}", regexp.QuoteMeta(version))
	re, err := regexp.Compile(p)
	if err != nil {
		return nil
	}
	for i := range assets {
		if re.MatchString(assets[i].Filename) {
			return &assets[i]
		}
	}
	return nil
}

func (g *GitHub) findChecksum(ctx context.Context, filename string, assets []ghAsset) (string, string) {
	candidates := []string{
		filename + ".sha256", filename + ".sha256sum",
		"SHA256SUMS", "sha256sums.txt", "checksums.txt",
	}
	for _, name := range candidates {
		for _, a := range assets {
			if a.Filename != name {
				continue
			}
			if hash, err := fetchChecksumLine(ctx, a.URL, filename); err == nil {
				return hash, "sha256"
			}
		}
	}
	return "", ""
}

func (g *GitHub) searchReleases(ctx context.Context, repo, tagPattern, assetPattern string) (string, []ghAsset) {
	resp, err := doRequest(ctx, http.MethodGet, "https://github.com/"+repo+"/releases")
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	body, err := readBodyLimited(resp.Body)
	if err != nil {
		return "", nil
	}
	tagRe := regexp.MustCompile(`href="/[^/]+/[^/]+/releases/tag/([^"]+)"`)
	patternRe, err := regexp.Compile(tagPattern)
	if err != nil {
		return "", nil
	}
	assetRe, err := regexp.Compile(assetPattern)
	if err != nil {
		return "", nil
	}
	seen := map[string]bool{}
	for _, m := range tagRe.FindAllStringSubmatch(string(body), -1) {
		if len(m) < 2 {
			continue
		}
		tag := m[1]
		if seen[tag] || !patternRe.MatchString(tag) {
			continue
		}
		seen[tag] = true
		assets, err := g.fetchAssets(ctx, repo, tag)
		if err != nil {
			continue
		}
		for _, a := range assets {
			if assetRe.MatchString(a.Filename) {
				return tag, assets
			}
		}
	}
	return "", nil
}

func tagFromURL(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 5 || parts[2] != "releases" || parts[3] != "tag" {
		return "", fmt.Errorf("unexpected URL: %s", s)
	}
	return url.PathUnescape(strings.Join(parts[4:], "/"))
}

func fetchChecksumLine(ctx context.Context, url, target string) (string, error) {
	resp, err := doRequest(ctx, http.MethodGet, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := readBodyLimited(resp.Body)
	if err != nil {
		return "", err
	}
	hash, _, err := ParseChecksumFile(string(body), target, "sha256", IsValidSHA256)
	return hash, err
}
