package checker

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/verparse"
)

func init() { Register(&HTML{}) }

// HTML scrapes a regex from an HTML page.
type HTML struct{}

func (h *HTML) Type() string { return "html" }

func (h *HTML) Check(ctx context.Context, cfg *manifest.UpdateConfig, currentVersion, sourceURL string) (*Result, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("html checker requires url")
	}
	if cfg.VersionPattern == "" {
		return nil, fmt.Errorf("html checker requires version-pattern")
	}
	urlTemplate := cfg.URLTemplate
	if urlTemplate == "" && strings.Contains(sourceURL, "{version}") {
		urlTemplate = sourceURL
	}
	if urlTemplate == "" {
		return nil, fmt.Errorf("html checker requires url-template (or source url with {version})")
	}

	body, err := httpReadAll(ctx, cfg.URL)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(cfg.VersionPattern)
	if err != nil {
		return nil, fmt.Errorf("invalid version-pattern: %w", err)
	}
	version := newestMatch(re, body)
	if version == "" {
		return nil, fmt.Errorf("version-pattern did not match")
	}
	log.Debug("HTML version", "version", version)

	r := &Result{
		LatestVersion: version,
		HasUpdate:     verparse.Compare(version, currentVersion) > 0,
		DownloadURL:   ExpandTemplate(urlTemplate, version),
	}
	if r.DownloadURL != "" {
		target := filepath.Base(r.DownloadURL)
		if cfg.HashURL != "" {
			if h, a, err := FetchChecksumFromURL(ctx, ExpandTemplate(cfg.HashURL, version), target, cfg.HashPattern); err == nil {
				r.Hash = h
				r.HashAlgorithm = a
			}
		}
		if r.Hash == "" {
			if h, a, err := FetchChecksum(ctx, r.DownloadURL); err == nil {
				r.Hash = h
				r.HashAlgorithm = a
			}
		}
		if size, err := FetchFileSize(ctx, r.DownloadURL); err == nil && size > 0 {
			r.Size = size
		}
	}
	return r, nil
}

// newestMatch returns the newest version among every capture of re in body. A
// listing page orders its entries however it likes — oldest first, newest
// first, or by whatever a directory index sorts on — and the entry a vendor
// labels "latest" may be a milestone build, so the newest match the pattern
// admits is the only dependable pick. Excluding pre-releases is the pattern's
// job: verparse.Compare ranks "1.0.0-rc" above "1.0.0".
func newestMatch(re *regexp.Regexp, body string) string {
	var newest string
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		if len(m) < 2 || m[1] == "" {
			continue
		}
		if newest == "" || verparse.Compare(m[1], newest) > 0 {
			newest = m[1]
		}
	}
	return newest
}
