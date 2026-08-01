package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	Validate DevValidateCmd `cmd:"" help:"Validate local catalog manifests and index.json"`
	Update   DevUpdateCmd   `cmd:"" help:"Rewrite local manifests and index.json with newer upstream versions"`
}

// DevValidateCmd validates every local manifest and its index without using
// the network. It is intended for catalog CI before publishing changes.
type DevValidateCmd struct{}

func (c *DevValidateCmd) Run(a *App) error {
	return validateCatalog(a.local.Root())
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

func validateCatalog(root string) error {
	if _, err := os.Stat(root); err != nil {
		return fmt.Errorf("catalog: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		return fmt.Errorf("read index.json: %w", err)
	}
	var index catalog.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("parse index.json: %w", err)
	}

	seen := make(map[string]bool)
	count := 0
	categories, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read catalog: %w", err)
	}
	for _, category := range categories {
		if !category.IsDir() || strings.HasPrefix(category.Name(), ".") {
			continue
		}
		packages, err := os.ReadDir(filepath.Join(root, category.Name()))
		if err != nil {
			return fmt.Errorf("read category %q: %w", category.Name(), err)
		}
		for _, pkg := range packages {
			if !pkg.IsDir() || strings.HasPrefix(pkg.Name(), ".") {
				continue
			}
			path := filepath.Join(root, category.Name(), pkg.Name(), "manifest.yaml")
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("%s: open manifest: %w", pkg.Name(), err)
			}
			m, parseErr := manifest.Parse(f)
			f.Close()
			if parseErr != nil {
				return fmt.Errorf("%s: %w", pkg.Name(), parseErr)
			}
			if m.ID != pkg.Name() {
				return fmt.Errorf("%s: manifest id %q does not match directory", pkg.Name(), m.ID)
			}
			if seen[m.ID] {
				return fmt.Errorf("duplicate package id %q", m.ID)
			}
			seen[m.ID] = true
			count++

			e, ok := index.Packages[m.ID]
			if !ok {
				return fmt.Errorf("%s: missing from index.json", m.ID)
			}
			if e.Name != m.Name || e.Version != m.Version || e.Category != category.Name() ||
				e.Description != m.Description || e.Provides != m.Provides || !slices.Equal(e.Requires, m.Requires) {
				return fmt.Errorf("%s: index.json metadata does not match manifest", m.ID)
			}
		}
	}
	if len(index.Packages) != count {
		return fmt.Errorf("index.json has %d packages, catalog has %d manifests", len(index.Packages), count)
	}
	for id := range index.Packages {
		if !seen[id] {
			return fmt.Errorf("index.json contains unknown package %q", id)
		}
	}
	fmt.Printf("catalog valid: %d packages\n", count)
	return nil
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

// writeUpdates walks every manifest with an update block, checks primary
// sources first, and only checks/rewrites secondary sources for packages whose
// primary source advanced. Primary source (sources[0]) bumps the manifest
// version and index entry; secondary sources rewrite in place. Checks run in
// parallel within each phase; writes run sequentially and output reports only
// primary package updates.
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

	// Phase 1: primary sources decide which packages are eligible for updates.
	var primaryJobs []*devJob
	for _, j := range jobs {
		if j.sourceIdx == 0 {
			primaryJobs = append(primaryJobs, j)
		}
	}
	runDevChecks(ctx, primaryJobs)

	primaryAdvanced := map[string]bool{}
	for _, j := range primaryJobs {
		if j.err == nil && j.result != nil && j.result.HasUpdate {
			primaryAdvanced[j.pkg.ID] = true
		}
	}

	// Phase 2: secondary sources are relevant only for packages with a
	// primary update. This avoids plugin/tool-only bumps and their confusing
	// package-level output when the main package did not move.
	var secondaryJobs []*devJob
	for _, j := range jobs {
		if j.sourceIdx > 0 && primaryAdvanced[j.pkg.ID] {
			secondaryJobs = append(secondaryJobs, j)
		}
	}
	runDevChecks(ctx, secondaryJobs)

	// Phase 2: apply rewrites sequentially, in order, collecting a row per
	// rewritten package so the whole set aligns.
	type row struct{ id, change, note string }
	var rows []row
	for _, j := range jobs {
		if j.sourceIdx > 0 && !primaryAdvanced[j.pkg.ID] {
			continue
		}
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
				Provides:    j.m.Provides,
				Requires:    append([]string(nil), j.m.Requires...),
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
		if j.sourceIdx == 0 {
			rows = append(rows, row{id: j.pkg.ID, change: change})
		}
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
