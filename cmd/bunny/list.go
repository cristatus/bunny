package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/catalog"
)

// ListCmd prints installed packages by default. Pass --remote to see the full
// catalog with install status.
type ListCmd struct {
	Category string `short:"c" help:"Filter by category"`
	Remote   bool   `help:"List all packages in the catalog, not just installed"`
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

// newTabWriter returns the tabwriter bunny uses for all column output, so the
// padding/format stays consistent across listings.
func newTabWriter() *tabwriter.Writer { return newTabWriterTo(os.Stdout) }

// newTabWriterTo is newTabWriter targeting an arbitrary writer — used when the
// installed listing needs to format into a buffer before styling.
func newTabWriterTo(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

func (c *ListCmd) listInstalled(a *App) error {
	// Build a lookup map from catalog so we can show name and category.
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
		name, category := id, ""
		if p, ok := catalogInfo[id]; ok {
			name = p.Name
			category = p.Category
		}
		if !c.matchesCategory(category) {
			continue
		}
		rows = append(rows, installedRow{
			id: id, category: category, name: name, version: pkg.Version,
			provides: pkg.Provides,
			active:   pkg.Provides != "" && a.State.Providers[pkg.Provides] == id,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].category != rows[j].category {
			return rows[i].category < rows[j].category
		}
		return rows[i].id < rows[j].id
	})
	_, err := io.WriteString(os.Stdout, renderInstalled(rows))
	return err
}

// installedRow is one line of the installed listing.
type installedRow struct {
	id, category, name, version, provides string
	active                                bool // active provider for its capability
}

// renderInstalled formats the installed listing. The active provider for each
// capability is tagged "(active)" in the PROVIDES column.
func renderInstalled(rows []installedRow) string {
	var buf bytes.Buffer
	w := newTabWriterTo(&buf)
	fmt.Fprintln(w, "ID\tCATEGORY\tNAME\tVERSION\tPROVIDES")
	for _, r := range rows {
		provides := r.provides
		if r.active {
			provides += " (active)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.id, r.category, r.name, r.version, provides)
	}
	w.Flush()
	return buf.String()
}

func (c *ListCmd) listRemote(a *App) error {
	pkgs, err := a.Catalog.List()
	if err != nil {
		return err
	}
	w := newTabWriter()
	fmt.Fprintln(w, "ID\tCATEGORY\tNAME\tVERSION\tSTATUS")
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Category != pkgs[j].Category {
			return pkgs[i].Category < pkgs[j].Category
		}
		return pkgs[i].ID < pkgs[j].ID
	})
	for _, pkg := range pkgs {
		if !c.matchesCategory(pkg.Category) {
			continue
		}
		status := ""
		if info, ok := a.State.Packages[pkg.ID]; ok {
			status = "installed"
			if info.Version != pkg.Version {
				status = fmt.Sprintf("installed (%s)", info.Version)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", pkg.ID, pkg.Category, pkg.Name, pkg.Version, status)
	}
	return w.Flush()
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
	fmt.Printf("Name:        %s\n", m.Name)
	fmt.Printf("ID:          %s\n", m.ID)
	fmt.Printf("Version:     %s\n", m.Version)
	if m.Description != "" {
		fmt.Printf("Description: %s\n", m.Description)
	}
	if m.Provides != "" {
		fmt.Printf("Provides:    %s\n", m.Provides)
	}
	if len(m.Requires) > 0 {
		fmt.Printf("Requires:    %s\n", strings.Join(m.Requires, ", "))
	}
	if info, ok := a.State.Packages[m.ID]; ok {
		fmt.Printf("Status:      installed (%s)\n", info.Version)
	} else {
		fmt.Printf("Status:      not installed\n")
	}
	if len(m.Bin) > 0 {
		names := make([]string, 0, len(m.Bin))
		for _, b := range m.Bin {
			names = append(names, b.Name)
		}
		fmt.Printf("Binaries:    %s\n", strings.Join(names, ", "))
	}
	return nil
}

// SearchCmd does a substring match on ID, name, and description.
type SearchCmd struct {
	Query string `arg:"" help:"Search query"`
}

func (c *SearchCmd) Run(a *App) error {
	pkgs, err := a.Catalog.List()
	if err != nil {
		return err
	}
	q := strings.ToLower(c.Query)
	w := newTabWriter()
	fmt.Fprintln(w, "ID\tNAME\tVERSION")
	found := 0
	for _, pkg := range pkgs {
		if !strings.Contains(strings.ToLower(pkg.ID), q) &&
			!strings.Contains(strings.ToLower(pkg.Name), q) &&
			!strings.Contains(strings.ToLower(pkg.Description), q) {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", pkg.ID, pkg.Name, pkg.Version)
		found++
	}
	w.Flush()
	if found == 0 {
		log.Info("No packages found", "query", c.Query)
	}
	return nil
}
