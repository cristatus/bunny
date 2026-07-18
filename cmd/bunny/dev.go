package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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

// devCheckConcurrency bounds how many upstream source checks run at once.
const devCheckConcurrency = 8

// devJob is one (package, updatable source) to check and, if it advanced,
// rewrite. The check phase fills result/srcUpdate/err; the write phase reads
// them.
type devJob struct {
	pkg          catalog.PackageInfo
	m            *manifest.Manifest
	manifestPath string
	indexPath    string
	sourceIdx    int
	source       manifest.Source
	currentVer   string
	updateCfg    *manifest.UpdateConfig

	result    *checker.Result
	srcUpdate catalog.SourceUpdate
	err       error
}

// writeUpdates walks every manifest with an update block, checks each source
// against upstream (concurrently), and rewrites the on-disk manifest +
// index.json for any package whose upstream tag has advanced. Primary source
// (sources[0]) bumps the manifest version and the index entry; secondary
// sources rewrite in place. Checks run in parallel; the file writes run
// sequentially so index.json is never raced and output stays deterministic.
func writeUpdates(ctx context.Context, a *App, id string) error {
	if !a.local.Exists() {
		return fmt.Errorf("no local catalog at %s; 'bunny dev update' requires a local catalog to rewrite", a.Paths.Catalog())
	}

	pkgs, err := a.local.List()
	if err != nil {
		return err
	}

	start := time.Now()
	var jobs []*devJob
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
		for i, s := range m.Sources {
			if s.Update == nil {
				continue
			}
			currentVer := m.Version
			if i > 0 {
				currentVer = extractURLVersion(s.URL, s.Update.TagPattern)
			}
			jobs = append(jobs, &devJob{
				pkg: p, m: m, manifestPath: manifestPath, indexPath: indexPath,
				sourceIdx: i, source: s, currentVer: currentVer, updateCfg: s.Update,
			})
		}
	}

	out := ui.New(os.Stdout)
	out.Println() // leading blank, then the live counter sits below it

	// Phase 1: check upstream concurrently (bounded), with a live counter.
	runDevChecks(ctx, jobs)

	// Phase 2: apply rewrites sequentially, in order, collecting a row per
	// rewritten package so the whole set aligns.
	type row struct{ id, change, note string }
	var rows []row
	for _, j := range jobs {
		if j.err != nil {
			errs = append(errs, fmt.Errorf("%s sources[%d]: %w", j.pkg.ID, j.sourceIdx, j.err))
			failed++
			continue
		}
		r := j.result
		if r == nil || !r.HasUpdate {
			continue
		}
		change := ""
		if j.sourceIdx == 0 {
			mw, err := catalog.PrepareManifestVersion(j.manifestPath, r.LatestVersion, j.srcUpdate)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: prepare manifest: %w", j.pkg.ID, err))
				failed++
				continue
			}
			iw, err := catalog.PrepareIndexEntry(j.indexPath, j.pkg.ID, catalog.IndexEntry{
				Name:        j.m.Name,
				Version:     r.LatestVersion,
				Category:    j.pkg.Category,
				Description: j.m.Description,
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: prepare index: %w", j.pkg.ID, err))
				failed++
				continue
			}
			if err := catalog.Commit([]catalog.PreparedWrite{mw, iw}); err != nil {
				errs = append(errs, fmt.Errorf("%s: commit manifest+index: %w", j.pkg.ID, err))
				failed++
				continue
			}
			change = fmt.Sprintf("%s → %s", r.CurrentVersion, r.LatestVersion)
		} else {
			if err := catalog.RewriteSource(j.manifestPath, j.sourceIdx, j.srcUpdate); err != nil {
				errs = append(errs, fmt.Errorf("%s sources[%d]: rewrite: %w", j.pkg.ID, j.sourceIdx, err))
				failed++
				continue
			}
			change = fmt.Sprintf("%s → %s", j.currentVer, r.LatestVersion)
		}
		rw := row{id: j.pkg.ID, change: change}
		if j.sourceIdx > 0 {
			rw.note = fmt.Sprintf("(source %d)", j.sourceIdx+1)
		}
		rows = append(rows, rw)
	}

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

// runDevChecks resolves every job's upstream state concurrently, bounded to
// devCheckConcurrency, updating a live "checking (done/total)" counter. Each
// job's result/srcUpdate/err is filled in place; there are no cross-job writes,
// so no locking beyond the counter is needed.
func runDevChecks(ctx context.Context, jobs []*devJob) {
	status := progress.NewStatus(os.Stderr)
	defer status.Clear()

	sem := make(chan struct{}, devCheckConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j *devJob) {
			defer wg.Done()
			defer func() { <-sem }()
			j.result, j.srcUpdate, j.err = resolveSourceUpdate(ctx, j.pkg.ID, j.currentVer, j.source, j.updateCfg)
			mu.Lock()
			done++
			status.Update(fmt.Sprintf("checking sources… (%d/%d)", done, len(jobs)))
			mu.Unlock()
		}(j)
	}
	wg.Wait()
}

// resolveSourceUpdate runs the checker, picks a download URL, and produces
// a SourceUpdate ready for catalog.RewriteSource / RewriteManifestVersion.
// Hashes must come from an upstream-published checksum discovered by the
// checker. Hashing the payload merely pins the first download; it does not
// authenticate it.
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
	if (needSHA256 && sha256Hash == "") || (needSHA512 && sha512Hash == "") {
		return nil, catalog.SourceUpdate{}, fmt.Errorf(
			"upstream did not publish the required checksum for %s", downloadURL)
	}
	if src.Size > 0 && r.Size == 0 {
		return nil, catalog.SourceUpdate{}, fmt.Errorf(
			"upstream did not report the size for %s", downloadURL)
	}

	urlUpdate := ""
	if !strings.Contains(src.URL, "{version}") {
		urlUpdate = downloadURL
	}
	return r, catalog.SourceUpdate{
		URL:    urlUpdate,
		SHA256: sha256Hash,
		SHA512: sha512Hash,
		Size:   r.Size,
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
