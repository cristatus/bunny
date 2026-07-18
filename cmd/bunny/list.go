package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/ui"
)

// ListCmd prints installed packages by default. Pass --remote to see the full
// catalog with install status.
type ListCmd struct {
	Category   string `short:"c" help:"Filter by category"`
	Capability string `help:"Filter by provided capability"`
	Active     bool   `help:"Show only active capability providers"`
	Remote     bool   `help:"List all packages in the catalog, not just installed"`
}

func (c *ListCmd) Run(a *App) error {
	if c.Remote {
		return c.listRemote(a)
	}
	return c.listInstalled(a)
}

// matchesCategory reports whether a package in the given category passes the
// --category filter (no filter set → always true). Kept in one place so the
// installed and remote listings can never diverge on how they filter.
func (c *ListCmd) matchesCategory(category string) bool {
	return c.Category == "" || category == c.Category
}

func (c *ListCmd) matchesCapability(capability string) bool {
	return c.Capability == "" || capability == c.Capability
}

func (c *ListCmd) listInstalled(a *App) error {
	// Build a lookup map from catalog so we can show category.
	catalogInfo := map[string]catalog.PackageInfo{}
	if info, err := a.Catalog.List(); err == nil {
		for _, p := range info {
			catalogInfo[p.ID] = p
		}
	}
	// Category is only known from the catalog; without it a --category filter
	// would silently drop every row. Surface that instead of printing nothing.
	if c.Category != "" && len(catalogInfo) == 0 {
		return fmt.Errorf("cannot filter by --category %q: catalog data is unavailable", c.Category)
	}
	var rows []installedRow
	for id, pkg := range a.State.Packages {
		category, provides := "", pkg.Provides
		if p, ok := catalogInfo[id]; ok {
			category = p.Category
			if provides == "" {
				provides = p.Provides
			}
		}
		active := provides != "" && a.State.Providers[provides] == id
		if !c.matchesCategory(category) || !c.matchesCapability(provides) || (c.Active && !active) {
			continue
		}
		rows = append(rows, installedRow{
			id: id, category: category, version: pkg.Version, provides: provides, active: active,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].category != rows[j].category {
			return rows[i].category < rows[j].category
		}
		return rows[i].id < rows[j].id
	})
	p := ui.New(os.Stdout)
	return a.pageOutput("\n" + renderInstalled(p, rows))
}

// installedRow is one line of the installed listing.
type installedRow struct {
	id, category, version, provides string
	active                          bool // active provider for its capability
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
			{Text: r.category},
			{Text: r.provides},
			{Text: r.version},
			{Text: active, Style: style},
		})
	}
	out := p.Table([]string{"Package", "Category", "Provides", "Version", "Active"}, cells)
	return out + "\n" + fmt.Sprintf("%d packages\n", len(rows))
}

func (c *ListCmd) listRemote(a *App) error {
	pkgs, err := a.Catalog.List()
	if err != nil {
		return err
	}
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Category != pkgs[j].Category {
			return pkgs[i].Category < pkgs[j].Category
		}
		return pkgs[i].ID < pkgs[j].ID
	})
	var rows []remoteRow
	for _, pkg := range pkgs {
		active := pkg.Provides != "" && a.State.Providers[pkg.Provides] == pkg.ID
		if !c.matchesCategory(pkg.Category) || !c.matchesCapability(pkg.Provides) || (c.Active && !active) {
			continue
		}
		status, style := "", ui.Plain
		if info, ok := a.State.Packages[pkg.ID]; ok {
			status, style = "installed", ui.Good
			if info.Version != pkg.Version {
				status = fmt.Sprintf("installed (%s)", info.Version)
			}
		}
		rows = append(rows, remoteRow{pkg: pkg, active: active, status: status, statusStyle: style})
	}
	p := ui.New(os.Stdout)
	return a.pageOutput("\n" + renderRemote(p, rows))
}

type remoteRow struct {
	pkg         catalog.PackageInfo
	active      bool
	status      string
	statusStyle ui.Style
}

func renderRemote(p *ui.Printer, rows []remoteRow) string {
	cells := make([][]ui.Cell, 0, len(rows))
	for _, r := range rows {
		active, activeStyle := "", ui.Plain
		if r.active {
			active, activeStyle = "yes", ui.Good
		}
		cells = append(cells, []ui.Cell{
			{Text: r.pkg.ID}, {Text: r.pkg.Category}, {Text: r.pkg.Provides},
			{Text: r.pkg.Version}, {Text: active, Style: activeStyle},
			{Text: r.status, Style: r.statusStyle},
		})
	}
	out := p.Table([]string{"Package", "Category", "Provides", "Version", "Active", "Status"}, cells)
	return out + "\n" + fmt.Sprintf("%d packages\n", len(rows))
}

// InfoCmd prints details about a single package.
type InfoCmd struct {
	ID string `arg:"" help:"Package ID"`
}

func (c *InfoCmd) Run(a *App) error {
	m, err := a.Catalog.Load(c.ID)
	if err != nil {
		return err
	}
	installedVersion, installed := "", false
	if info, ok := a.State.Packages[m.ID]; ok {
		installedVersion, installed = info.Version, true
	}
	detail := infoDetail{installedVersion: installedVersion, installed: installed}
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
		sort.Strings(detail.dependents)
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
	if m.Category != "" {
		rows = append(rows, ui.KVRow{Key: "Category", Value: m.Category})
	}
	rows = append(rows, ui.KVRow{Key: "Version", Value: version + "  " + status})
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

// SearchCmd does a substring match on catalog summary metadata, including
// provided capabilities and runtime requirements.
type SearchCmd struct {
	Query string `arg:"" help:"Search query"`
}

func (c *SearchCmd) Run(a *App) error {
	pkgs, err := a.Catalog.List()
	if err != nil {
		return err
	}
	q := strings.ToLower(c.Query)
	var matches []catalog.PackageInfo
	for _, pkg := range pkgs {
		if strings.Contains(strings.ToLower(pkg.ID), q) ||
			strings.Contains(strings.ToLower(pkg.Name), q) ||
			strings.Contains(strings.ToLower(pkg.Description), q) ||
			strings.Contains(strings.ToLower(pkg.Provides), q) ||
			containsFold(pkg.Requires, q) {
			matches = append(matches, pkg)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	installed := map[string]bool{}
	for id := range a.State.Packages {
		installed[id] = true
	}
	p := ui.New(os.Stdout)
	return a.pageOutput("\n" + renderSearch(p, c.Query, matches, installed))
}

func containsFold(values []string, query string) bool {
	query = strings.ToLower(query)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

// renderSearch prints a match table (Package / Version / Status / Description),
// green "installed" on installed rows, then a count. Zero matches print a plain
// message instead of an empty table.
func renderSearch(p *ui.Printer, query string, pkgs []catalog.PackageInfo, installed map[string]bool) string {
	if len(pkgs) == 0 {
		return fmt.Sprintf("no matches for %q\n", query)
	}
	var cells [][]ui.Cell
	for _, pkg := range pkgs {
		status, style := "", ui.Plain
		if installed[pkg.ID] {
			status, style = "installed", ui.Good
		}
		cells = append(cells, []ui.Cell{
			{Text: pkg.ID}, {Text: pkg.Provides}, {Text: pkg.Version},
			{Text: status, Style: style},
			{Text: pkg.Description},
		})
	}
	out := p.Table([]string{"Package", "Provides", "Version", "Status", "Description"}, cells)
	return out + "\n" + fmt.Sprintf("%d matches\n", len(pkgs))
}
