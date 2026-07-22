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
	m := re.FindStringSubmatch(body)
	if len(m) < 2 {
		return nil, fmt.Errorf("version-pattern did not match")
	}
	version := m[1]
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
