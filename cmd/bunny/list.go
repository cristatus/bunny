package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/ui"
)

// catalogFilter is the filter set every listing shares, so `bunny list` and
// `bunny search` cannot diverge on what a filter selects.
type catalogFilter struct {
	Tag        string `short:"t" help:"Filter by tag"`
	Capability string `help:"Filter by provided capability"`
	Kind       string `enum:"app,cli,sdk," default:"" placeholder:"kind" help:"Filter by install kind: app, cli, or sdk"`
}

// filterable is everything the filters read from one row.
type filterable struct {
	tags       []string
	kind       string
	capability string
}

// matches reports whether a row passes every filter; an unset filter passes
// everything.
func (f *catalogFilter) matches(r filterable) bool {
	switch {
	case f.Tag != "" && !slices.Contains(r.tags, f.Tag):
		return false
	case f.Capability != "" && r.capability != f.Capability:
		return false
	case f.Kind != "" && r.kind != f.Kind:
		return false
	}
	return true
}

// unset reports whether no filter is set at all: how search tells a
// filter-only browse from a bare invocation with nothing to look for.
func (f *catalogFilter) unset() bool {
	return f.Tag == "" && f.Capability == "" && f.Kind == ""
}

// ListCmd prints installed packages; `bunny search` answers the catalog side of
// the same question with the same filters. --active stays here: which package
// answers for a capability is a property of what is installed.
type ListCmd struct {
	catalogFilter
	Active bool `help:"Show only active capability providers"`
}

func (c *ListCmd) Run(a *App) error {
	return c.listInstalled(a)
}

func (c *ListCmd) listInstalled(a *App) error {
	// The catalog fills in what state does not record: tags for --tag, and a
	// kind for a package installed before its manifest declared one.
	catalogInfo := map[string]catalog.PackageInfo{}
	if info, err := a.Catalog.List(); err == nil {
		for _, p := range info {
			catalogInfo[p.ID] = p
		}
	}
	// Tags live only in the catalog; without it a --tag filter would silently
	// drop every row. Surface that instead of printing nothing.
	if c.Tag != "" && len(catalogInfo) == 0 {
		return fmt.Errorf("cannot filter by --tag %q: catalog data is unavailable", c.Tag)
	}
	var rows []installedRow
	for id, pkg := range a.State.Packages {
		// Zero value when the catalog cannot say: state still carries enough
		// to list the package, just not its tags.
		info := catalogInfo[id]
		provides, kind := pkg.Provides, pkg.Kind
		if provides == "" {
			provides = info.Provides
		}
		if kind == "" {
			kind = info.Kind
		}
		active := provides != "" && a.State.Providers[provides] == id
		if !c.matches(filterable{tags: info.Tags, kind: kind, capability: provides}) ||
			(c.Active && !active) {
			continue
		}
		rows = append(rows, installedRow{
			id: id, kind: kind, version: pkg.Version, provides: provides, active: active,
		})
	}
	slices.SortFunc(rows, func(a, b installedRow) int { return strings.Compare(a.id, b.id) })
	p := ui.New(os.Stdout)
	p.Print("\n" + renderInstalled(p, rows))
	return nil
}

// installedRow is one line of the installed listing.
type installedRow struct {
	id, kind, version, provides string
	active                      bool // active provider for its capability
}

// renderInstalled formats the installed listing (no human-name column).
func renderInstalled(p *ui.Printer, rows []installedRow) string {
	cells := make([][]ui.Cell, 0, len(rows))
	for _, r := range rows {
		active, style := "", ui.Plain
		if r.active {
			active, style = "yes", ui.Good
		}
		cells = append(cells, []ui.Cell{
			{Text: r.id},
			{Text: r.kind},
			{Text: r.provides},
			{Text: r.version},
			{Text: active, Style: style},
		})
	}
	out := p.Table([]string{"Package", "Kind", "Provides", "Version", "Active"}, cells)
	return out + "\n" + fmt.Sprintf("%d packages\n", len(rows))
}

// InfoCmd prints details about a single package.
type InfoCmd struct {
	ID string `arg:"" help:"Package ID"`
}

func (c *InfoCmd) Run(a *App) error {
	pkg, err := catalog.ResolvePackage(a.Catalog, c.ID)
	if err != nil {
		return err
	}
	m, err := pkg.LoadManifest()
	if err != nil {
		return err
	}
	// Which catalog answered only tells the reader something when there is
	// more than one that could have.
	source := ""
	if a.multiCatalog() {
		source = a.packageSource(c.ID, pkg.Source.Name)
	}
	installedVersion, installed := "", false
	if info, ok := a.State.Packages[m.ID]; ok {
		installedVersion, installed = info.Version, true
	}
	detail := infoDetail{installedVersion: installedVersion, installed: installed, source: source}
	if m.Provides != "" {
		detail.activeProvider = a.State.Providers[m.Provides]
		if cwd, err := os.Getwd(); err == nil {
			if pin, err := shim.ResolveProjectVersion(cwd, m.Provides); err == nil && pin != nil {
				detail.projectPin = pin.Version + " (" + filepath.Base(pin.Source) + ")"
			}
		}
	}
	if pkgs, err := a.Catalog.List(); err == nil {
		for _, pkg := range pkgs {
			for _, req := range pkg.Requires {
				capability, _, _ := manifest.ParseRequirement(req)
				if capability == m.ID || (m.Provides != "" && capability == m.Provides) {
					detail.dependents = append(detail.dependents, pkg.ID)
					break
				}
			}
		}
		slices.Sort(detail.dependents)
	}
	p := ui.New(os.Stdout)
	p.Println()
	p.Print(renderInfo(p, m, detail))
	return nil
}

type infoDetail struct {
	installedVersion string
	installed        bool
	activeProvider   string
	projectPin       string
	dependents       []string
	// source names the catalog the package came from, set only when several
	// are configured.
	source string
}

// renderInfo prints a single aligned key/value block for a package. Version
// carries the install status and an "update available (<latest>)" note when the
// catalog version differs from the installed one (inequality, no network).
// Rows for optional metadata appear only when the manifest carries them; a
// not-installed package gets a trailing install hint.
func renderInfo(p *ui.Printer, m *manifest.Manifest, detail infoDetail) string {
	version := m.Version
	status := "not installed"
	if detail.installed {
		version = detail.installedVersion
		status = p.PaintStatus("installed", ui.Good)
		if detail.installedVersion != m.Version {
			status += "  ·  " + p.Faint("update available ("+m.Version+")")
		}
	}

	rows := []ui.KVRow{{Key: "Id", Value: m.ID}}
	if m.Name != "" {
		rows = append(rows, ui.KVRow{Key: "Name", Value: m.Name})
	}
	if m.Description != "" {
		rows = append(rows, ui.KVRow{Key: "Description", Value: m.Description})
	}
	if len(m.Tags) > 0 {
		rows = append(rows, ui.KVRow{Key: "Tags", Value: strings.Join(m.Tags, ", ")})
	}
	rows = append(rows, ui.KVRow{Key: "Version", Value: version + "  " + status})
	if detail.source != "" {
		rows = append(rows, ui.KVRow{Key: "Catalog", Value: detail.source})
	}
	if m.Provides != "" {
		rows = append(rows, ui.KVRow{Key: "Provides", Value: m.Provides})
		active := "no"
		if detail.activeProvider == m.ID {
			active = p.PaintStatus("yes", ui.Good)
		} else if detail.activeProvider != "" {
			active += " (" + detail.activeProvider + ")"
		}
		rows = append(rows, ui.KVRow{Key: "Active", Value: active})
		if detail.projectPin != "" {
			rows = append(rows, ui.KVRow{Key: "Project pin", Value: detail.projectPin})
		}
	}
	if len(m.Requires) > 0 {
		rows = append(rows, ui.KVRow{Key: "Requires", Value: strings.Join(m.Requires, ", ")})
	}
	if len(m.Bin) > 0 {
		names := make([]string, 0, len(m.Bin))
		for _, bin := range m.Bin {
			names = append(names, bin.Name)
		}
		rows = append(rows, ui.KVRow{Key: "Binaries", Value: strings.Join(names, ", ")})
	}
	if len(detail.dependents) > 0 {
		rows = append(rows, ui.KVRow{Key: "Used by", Value: strings.Join(detail.dependents, ", ")})
	}
	if m.Homepage != "" {
		rows = append(rows, ui.KVRow{Key: "Homepage", Value: m.Homepage})
	}
	if m.License != "" {
		rows = append(rows, ui.KVRow{Key: "License", Value: m.License})
	}

	out := p.KV(rows)
	if !detail.installed {
		out += "\n" + "run 'bunny install " + m.ID + "' to add\n"
	}
	return out
}

// SearchCmd queries the catalog. Terms match as substrings against ids, names,
// descriptions, tags, provided capabilities, and runtime requirements, ranked by
// which field matched. Filters alone are a valid query, so `bunny search
// --tag ai` browses that slice of the catalog.
type SearchCmd struct {
	catalogFilter
	Installed bool     `xor:"status" help:"Show only packages that are installed"`
	Available bool     `xor:"status" help:"Show only packages that are not installed"`
	Query     []string `arg:"" optional:"" help:"Search terms; a package must match every one"`
}

// Match ranks, weakest first. Requirements rank last because every Java app
// requires jdk, so matching there says almost nothing about the package.
const (
	scoreNone = iota
	scoreRequires
	scoreDescription
	scoreName
	scoreFacet // a tag, or part of the provided capability
	scoreIDPart
	scoreIDPrefix
	scoreExact // the id itself, or the capability it provides
)

// searchScore ranks one already-lowercased term against a package, returning
// scoreNone when it does not match. An empty term matches nothing: as a
// substring it would match everything.
func searchScore(pkg catalog.PackageInfo, q string) int {
	if q == "" {
		return scoreNone
	}
	id, provides := strings.ToLower(pkg.ID), strings.ToLower(pkg.Provides)
	switch {
	case id == q, provides == q:
		return scoreExact
	case strings.HasPrefix(id, q):
		return scoreIDPrefix
	case strings.Contains(id, q):
		return scoreIDPart
	case containsFold(pkg.Tags, q), strings.Contains(provides, q):
		return scoreFacet
	case strings.Contains(strings.ToLower(pkg.Name), q):
		return scoreName
	case strings.Contains(strings.ToLower(pkg.Description), q):
		return scoreDescription
	case containsFold(pkg.Requires, q):
		return scoreRequires
	}
	return scoreNone
}

// scoreQuery ranks a package against every term, reporting false as soon as one
// misses: terms narrow, they do not accumulate. The weakest term sets the rank,
// so a package matching both terms in its id beats one matching a term only in
// its description. No terms is a filter-only browse: everything ties at
// scoreNone and the listing falls back to id order.
func scoreQuery(pkg catalog.PackageInfo, terms []string) (int, bool) {
	if len(terms) == 0 {
		return scoreNone, true
	}
	score := scoreExact
	for _, t := range terms {
		s := searchScore(pkg, t)
		if s == scoreNone {
			return scoreNone, false
		}
		score = min(score, s)
	}
	return score, true
}

// terms lowercases the query, dropping blanks so a quoted empty argument
// cannot turn into a match-everything substring.
func (c *SearchCmd) terms() []string {
	out := make([]string, 0, len(c.Query))
	for _, q := range c.Query {
		if q = strings.ToLower(strings.TrimSpace(q)); q != "" {
			out = append(out, q)
		}
	}
	return out
}

// collect returns the rows to print, ranked. Filters run before scoring: they
// are a field compare, ranking walks every searchable field.
func (c *SearchCmd) collect(a *App, pkgs []catalog.PackageInfo, terms []string) []catalogRow {
	var rows []catalogRow
	for _, pkg := range pkgs {
		if !c.matches(filterable{tags: pkg.Tags, kind: pkg.Kind, capability: pkg.Provides}) {
			continue
		}
		info, installed := a.State.Packages[pkg.ID]
		if (c.Installed && !installed) || (c.Available && installed) {
			continue
		}
		score, ok := scoreQuery(pkg, terms)
		if !ok {
			continue
		}
		active := pkg.Provides != "" && a.State.Providers[pkg.Provides] == pkg.ID
		status, style := catalogStatus(info.Version, pkg.Version, installed, active)
		rows = append(rows, catalogRow{pkg: pkg, score: score, status: status, statusStyle: style})
	}
	// Rank first, then id: within one rank the catalog's own order is priority
	// order, which says nothing to someone scanning the column.
	slices.SortFunc(rows, func(x, y catalogRow) int {
		if x.score != y.score {
			return y.score - x.score // best match first
		}
		return strings.Compare(x.pkg.ID, y.pkg.ID)
	})
	return rows
}

func (c *SearchCmd) Run(a *App) error {
	terms := c.terms()
	if len(terms) == 0 && c.unset() && !c.Installed && !c.Available {
		return errors.New("nothing to search for: pass a query, or a filter such as --tag, --kind, or --capability")
	}
	pkgs, err := a.Catalog.List()
	if err != nil {
		return err
	}
	rows := c.collect(a, pkgs, terms)

	p := ui.New(os.Stdout)
	p.Println()
	if len(rows) == 0 {
		if len(terms) == 0 {
			p.Println("no packages match these filters")
			return nil
		}
		p.Printf("no matches for %q\n", strings.Join(terms, " "))
		return nil
	}
	// A query counts matches; filters alone are a listing of packages.
	noun := "packages"
	if len(terms) > 0 {
		noun = "matches"
	}
	p.Print(renderCatalog(p, rows, a.multiCatalog(), noun))
	return nil
}

// containsFold reports whether any value contains the already-lowercased query.
func containsFold(values []string, query string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

// catalogRow is one line of a catalog listing.
type catalogRow struct {
	pkg         catalog.PackageInfo
	score       int
	status      string
	statusStyle ui.Style
}

// catalogStatus describes a row's relation to what is on disk. "active" implies
// installed, so it replaces rather than joins it; a parenthesized version is
// the differing one on disk.
func catalogStatus(installedVersion, catalogVersion string, installed, active bool) (string, ui.Style) {
	if !installed {
		return "", ui.Plain
	}
	status := "installed"
	if active {
		status = "active"
	}
	if installedVersion != catalogVersion {
		status += " (" + installedVersion + ")"
	}
	return status, ui.Good
}

// renderCatalog formats catalog results in rank order, carrying the Catalog
// column only when more than one catalog could have answered. Tags are a filter
// dimension, not a column: `bunny info` prints them in full.
func renderCatalog(p *ui.Printer, rows []catalogRow, showSource bool, noun string) string {
	cells := make([][]ui.Cell, 0, len(rows))
	for _, r := range rows {
		row := []ui.Cell{
			{Text: r.pkg.ID}, {Text: r.pkg.Kind}, {Text: r.pkg.Provides},
			{Text: r.pkg.Version},
		}
		if showSource {
			row = append(row, ui.Cell{Text: r.pkg.Source})
		}
		cells = append(cells, append(row,
			ui.Cell{Text: r.status, Style: r.statusStyle},
			ui.Cell{Text: r.pkg.Description},
		))
	}
	headers := []string{"Package", "Kind", "Provides", "Version"}
	if showSource {
		headers = append(headers, "Catalog")
	}
	headers = append(headers, "Status", "Description")
	out := p.Table(headers, cells)
	return out + "\n" + fmt.Sprintf("%d %s\n", len(rows), noun)
}
