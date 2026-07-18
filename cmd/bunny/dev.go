package main

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/checker"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/progress"
	"github.com/cristatus/bunny/internal/ui"
)

// DevCmd groups maintainer/CI subcommands. They act on the local catalog
// directory and aren't part of the day-to-day install/update workflow
// regular bunny users see.
type DevCmd struct {
	Update DevUpdateCmd `cmd:"" help:"Rewrite local manifests and index.json with newer upstream versions"`
}

// DevUpdateCmd rewrites local manifests with newer upstream versions and
// updates index.json. Intended for catalog maintainers and CI; requires a
// local catalog at $BUNNY_HOME/catalog (or wherever the catalog repo is
// checked out).
type DevUpdateCmd struct {
	ID string `arg:"" optional:"" help:"Package ID (default: every package with an update)"`
}

func (c *DevUpdateCmd) Run(a *App) error {
	return a.withMutation(a.context(), func() error {
		return writeUpdates(a.context(), a, c.ID)
	})
}

// writeUpdates walks every manifest with an update block, checks each source
// against upstream, and rewrites the on-disk manifest + index.json for any
// package whose upstream tag has advanced. Primary source (sources[0]) bumps
// the manifest version and the index entry; secondary sources rewrite in
// place. Rewritten packages are collected and rendered together so the columns
// align, closed by a summary line.
func writeUpdates(ctx context.Context, a *App, id string) error {
	if !a.local.Exists() {
		return fmt.Errorf("no local catalog at %s; 'bunny dev update' requires a local catalog to rewrite", a.Paths.Catalog())
	}

	pkgs, err := a.local.List()
	if err != nil {
		return err
	}

	out := ui.New(os.Stdout)
	out.Println() // leading blank, then the live status sits below it
	status := progress.NewStatus(os.Stderr)

	start := time.Now()
	type row struct{ id, change, note string }
	var rows []row
	var errs []error
	failed := 0
	for _, p := range pkgs {
		if id != "" && p.ID != id {
			continue
		}
		m, err := a.local.Load(p.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: load manifest: %w", p.ID, err))
			failed++
			continue
		}
		if len(m.Sources) == 0 {
			continue
		}
		manifestPath := filepath.Join(a.local.Root(), p.Category, p.ID, "manifest.yaml")
		indexPath := filepath.Join(a.local.Root(), "index.json")
		status.Update("checking " + p.ID + "…")
		for i, s := range m.Sources {
			if s.Update == nil {
				continue
			}
			currentVer := m.Version
			if i > 0 {
				currentVer = extractURLVersion(s.URL, s.Update.TagPattern)
			}
			r, src, err := resolveSourceUpdate(ctx, p.ID, currentVer, s, s.Update)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s sources[%d]: %w", p.ID, i, err))
				failed++
				continue
			}
			if r == nil || !r.HasUpdate {
				continue
			}
			change := ""
			if i == 0 {
				mw, err := catalog.PrepareManifestVersion(manifestPath, r.LatestVersion, src)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: prepare manifest: %w", p.ID, err))
					failed++
					continue
				}
				iw, err := catalog.PrepareIndexEntry(indexPath, p.ID, catalog.IndexEntry{
					Name:        m.Name,
					Version:     r.LatestVersion,
					Category:    p.Category,
					Description: m.Description,
				})
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: prepare index: %w", p.ID, err))
					failed++
					continue
				}
				if err := catalog.Commit([]catalog.PreparedWrite{mw, iw}); err != nil {
					errs = append(errs, fmt.Errorf("%s: commit manifest+index: %w", p.ID, err))
					failed++
					continue
				}
				change = fmt.Sprintf("%s → %s", r.CurrentVersion, r.LatestVersion)
			} else {
				if err := catalog.RewriteSource(manifestPath, i, src); err != nil {
					errs = append(errs, fmt.Errorf("%s sources[%d]: rewrite: %w", p.ID, i, err))
					failed++
					continue
				}
				change = fmt.Sprintf("%s → %s", currentVer, r.LatestVersion)
			}
			rw := row{id: p.ID, change: change}
			if i > 0 {
				rw.note = fmt.Sprintf("(source %d)", i+1)
			}
			rows = append(rows, rw)
		}
	}
	status.Clear()

	if len(rows) == 0 && failed == 0 {
		out.Println("all packages up to date")
		return nil
	}

	idWidth := 0
	for _, rw := range rows {
		if w := utf8.RuneCountInString(rw.id); w > idWidth {
			idWidth = w
		}
	}
	for _, rw := range rows {
		line := padRight(rw.id, idWidth) + "  " + rw.change
		if rw.note != "" {
			line += "   " + out.PaintStatus(rw.note, ui.Faint)
		}
		out.Println(line)
	}

	out.Println()
	out.Print(installSummary(out, "rewrote", len(rows), failed, time.Since(start)))
	return errors.Join(errs...)
}

// padRight pads s with spaces to w display columns (rune-counted).
func padRight(s string, w int) string {
	if gap := w - utf8.RuneCountInString(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// resolveSourceUpdate runs the checker, picks a download URL, and produces
// a SourceUpdate ready for catalog.RewriteSource / RewriteManifestVersion.
// Hashes come from the checker if it computed them; otherwise we fetch.
func resolveSourceUpdate(ctx context.Context, id, currentVersion string, src manifest.Source, cfg *manifest.UpdateConfig) (*checker.Result, catalog.SourceUpdate, error) {
	r, err := checker.Check(ctx, id, currentVersion, src.URL, cfg)
	if err != nil {
		return nil, catalog.SourceUpdate{}, fmt.Errorf("check: %w", err)
	}
	if r == nil || !r.HasUpdate {
		return r, catalog.SourceUpdate{}, nil
	}

	downloadURL := r.DownloadURL
	if downloadURL == "" {
		downloadURL = strings.ReplaceAll(src.URL, "{version}", r.LatestVersion)
	}

	needSHA256 := src.SHA256 != ""
	needSHA512 := src.SHA512 != ""
	if !needSHA256 && !needSHA512 {
		needSHA256 = true
	}
	sha256Hash, sha512Hash := "", ""
	switch r.HashAlgorithm {
	case "sha256":
		sha256Hash = r.Hash
	case "sha512":
		sha512Hash = r.Hash
	}
	size := r.Size
	if (needSHA256 && sha256Hash == "") || (needSHA512 && sha512Hash == "") || size == 0 {
		h, err := downloadAndHashAll(ctx, downloadURL)
		if err != nil {
			return nil, catalog.SourceUpdate{}, fmt.Errorf("download %s: %w", downloadURL, err)
		}
		if needSHA256 {
			sha256Hash = h.sha256
		}
		if needSHA512 {
			sha512Hash = h.sha512
		}
		size = h.size
	}

	urlUpdate := ""
	if !strings.Contains(src.URL, "{version}") {
		urlUpdate = downloadURL
	}
	return r, catalog.SourceUpdate{
		URL:    urlUpdate,
		SHA256: sha256Hash,
		SHA512: sha512Hash,
		Size:   size,
	}, nil
}

// extractURLVersion applies tag-pattern to a source URL to recover its
// embedded version string — so the cron can compare "what we're shipping"
// against "what upstream calls latest" for secondary sources, which lack a
// dedicated version field.
func extractURLVersion(sourceURL, pattern string) string {
	if pattern == "" || sourceURL == "" {
		return ""
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	m := re.FindStringSubmatch(sourceURL)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

type hashes struct {
	sha256 string
	sha512 string
	size   int64
}

var devHTTPClient = &http.Client{Timeout: 30 * time.Minute}

func downloadAndHashAll(ctx context.Context, url string) (hashes, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return hashes{}, err
	}
	resp, err := devHTTPClient.Do(req)
	if err != nil {
		return hashes{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return hashes{}, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	s256 := sha256.New()
	s512 := sha512.New()
	n, err := io.Copy(io.MultiWriter(s256, s512), resp.Body)
	if err != nil {
		return hashes{}, err
	}
	return hashes{
		sha256: hex.EncodeToString(s256.Sum(nil)),
		sha512: hex.EncodeToString(s512.Sum(nil)),
		size:   n,
	}, nil
}
