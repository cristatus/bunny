package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/ui"
)

func TestRenderInstalled(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	rows := []installedRow{
		{id: "bat", kind: "cli", version: "1.0", provides: ""},
		{id: "jbr-21", kind: "sdk", version: "21", provides: "jdk"},
		{id: "jdk-21", kind: "sdk", version: "21", provides: "jdk", active: true},
	}

	out := renderInstalled(p, rows)
	// Title-case header, no human-name column.
	if !strings.Contains(out, "Provides") || strings.Contains(out, "NAME") || strings.Contains(out, "Name") {
		t.Errorf("header wrong (want title-case, no name col): %q", out)
	}
	if !strings.Contains(out, "Active") || !strings.Contains(lineWith(t, out, "jdk-21"), "yes") {
		t.Error("active provider should have a separate active marker")
	}
	// The inactive sibling shows the bare capability, not the marker.
	jbrLine := lineWith(t, out, "jbr-21")
	if !strings.Contains(jbrLine, "jdk") || strings.Contains(jbrLine, "yes") {
		t.Errorf("inactive provider line wrong: %q", jbrLine)
	}
	// A non-provider has no capability listed.
	if batLine := lineWith(t, out, "bat"); strings.Contains(batLine, "jdk") {
		t.Errorf("non-provider line should have no capability: %q", batLine)
	}
	if !strings.Contains(out, "3 packages") {
		t.Errorf("count footer missing: %q", out)
	}
	// Plain text only — no ANSI styling.
	if strings.Contains(out, "\x1b[") {
		t.Error("list output must not contain ANSI escapes")
	}
}

func TestRenderCatalogShowsCapabilityAndStatus(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	rows := []catalogRow{{
		pkg:    catalog.PackageInfo{ID: "zulu-21", Tags: []string{"java", "jdk"}, Kind: "sdk", Provides: "jdk", Version: "21", Description: "Azul's OpenJDK 21"},
		status: "active", statusStyle: ui.Good,
	}}
	out := renderCatalog(p, rows, false, "matches")
	// Tags are a filter dimension, not a column: they belong to `bunny info`.
	if strings.Contains(out, "Tags") || strings.Contains(out, "java") {
		t.Errorf("listing should not print tags: %q", out)
	}
	for _, want := range []string{"Kind", "sdk", "Provides", "jdk", "active", "Azul's OpenJDK 21", "1 matches"} {
		if !strings.Contains(out, want) {
			t.Errorf("catalog output missing %q: %q", want, out)
		}
	}
	// One catalog cannot answer "which one?", so the column stays off.
	if strings.Contains(out, "Catalog") {
		t.Errorf("single-catalog listing should not carry a catalog column: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Error("catalog output must not contain ANSI escapes")
	}
}

// With several catalogs configured, which one a package came from is part of
// the listing.
func TestRenderCatalogShowsCatalogWhenSeveralConfigured(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	rows := []catalogRow{{
		pkg: catalog.PackageInfo{ID: "spring-boot", Kind: "sdk", Version: "3.0.0", Source: "axelor"},
	}}
	out := renderCatalog(p, rows, true, "packages")
	for _, want := range []string{"Catalog", "axelor", "spring-boot"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

// "active" already says the package is installed, so it replaces the weaker
// word rather than doubling up, and an older version on disk is called out.
func TestCatalogStatus(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		installedVersion, catalogVersion string
		installed, active                bool
		want                             string
	}{
		{name: "absent", catalogVersion: "21", want: ""},
		{name: "current", installedVersion: "21", catalogVersion: "21", installed: true, want: "installed"},
		{name: "outdated", installedVersion: "20", catalogVersion: "21", installed: true, want: "installed (20)"},
		{name: "active", installedVersion: "21", catalogVersion: "21", installed: true, active: true, want: "active"},
		{name: "active but outdated", installedVersion: "20", catalogVersion: "21", installed: true, active: true, want: "active (20)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, style := catalogStatus(tc.installedVersion, tc.catalogVersion, tc.installed, tc.active)
			if got != tc.want {
				t.Errorf("catalogStatus = %q, want %q", got, tc.want)
			}
			if want := ui.Good; tc.installed && style != want {
				t.Errorf("an installed package should be styled %v, got %v", want, style)
			}
		})
	}
}

// Tags are absent from every listing, so search is the only thing that makes
// them reachable.
func TestSearchMatchesTags(t *testing.T) {
	pkg := catalog.PackageInfo{
		ID: "mvnd", Name: "Maven Daemon", Description: "Fast Maven client",
		Tags: []string{"java", "build-tool"},
	}
	if searchScore(pkg, "build-tool") != scoreFacet {
		t.Error("a tag should be searchable")
	}
	if searchScore(pkg, "maven") != scoreName {
		t.Error("name should still match")
	}
	if searchScore(pkg, "node") != scoreNone {
		t.Error("unrelated query should not match")
	}
}

// --kind's enum has to be a literal in the struct tag, so nothing but this
// keeps it from drifting from the kinds that actually exist.
func TestKindFilterEnumMatchesManifestKinds(t *testing.T) {
	f, ok := reflect.TypeFor[catalogFilter]().FieldByName("Kind")
	if !ok {
		t.Fatal("catalogFilter has no Kind field")
	}
	// The trailing comma is the empty value: no --kind given filters nothing.
	if want, got := strings.Join(manifest.Kinds, ",")+",", f.Tag.Get("enum"); got != want {
		t.Errorf("--kind enum = %q, want %q", got, want)
	}
}

// Every filter narrows, and an unset one passes everything. The filter set is
// shared, so this covers `bunny list` and `bunny search` both.
func TestCatalogFilter(t *testing.T) {
	row := filterable{tags: []string{"java", "jdk"}, kind: "sdk", capability: "jdk"}
	if f := (&catalogFilter{}); !f.matches(row) || !f.unset() {
		t.Fatal("an unset filter must pass everything")
	}
	for name, f := range map[string]catalogFilter{
		"tag":        {Tag: "java"},
		"capability": {Capability: "jdk"},
		"kind":       {Kind: "sdk"},
	} {
		if !f.matches(row) {
			t.Errorf("--%s should match its own row", name)
		}
		if f.unset() {
			t.Errorf("--%s is set, so unset() must be false", name)
		}
	}
	for name, f := range map[string]catalogFilter{
		"tag":        {Tag: "node"},
		"capability": {Capability: "node"},
		"kind":       {Kind: "app"},
	} {
		if f.matches(row) {
			t.Errorf("--%s should reject a row it does not describe", name)
		}
	}
}

func lineWith(t *testing.T, s, needle string) string {
	t.Helper()
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	t.Fatalf("no line containing %q in:\n%s", needle, s)
	return ""
}
