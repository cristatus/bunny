package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
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

func TestRenderRemoteShowsCapabilityAndActive(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	rows := []remoteRow{{
		pkg:    catalog.PackageInfo{ID: "zulu-21", Tags: []string{"java", "jdk"}, Kind: "sdk", Provides: "jdk", Version: "21"},
		active: true, status: "installed", statusStyle: ui.Good,
	}}
	out := renderRemote(p, rows, false)
	// Tags are a filter dimension, not a column: they belong to `bunny info`.
	if strings.Contains(out, "Tags") || strings.Contains(out, "java") {
		t.Errorf("listing should not print tags: %q", out)
	}
	for _, want := range []string{"Kind", "sdk", "Provides", "Active", "jdk", "yes", "installed", "1 packages"} {
		if !strings.Contains(out, want) {
			t.Errorf("remote output missing %q: %q", want, out)
		}
	}
	// One catalog cannot answer "which one?", so the column stays off.
	if strings.Contains(out, "Catalog") {
		t.Errorf("single-catalog listing should not carry a catalog column: %q", out)
	}
}

// With several catalogs configured, which one a package came from is part of
// the listing.
func TestRenderRemoteShowsCatalogWhenSeveralConfigured(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	rows := []remoteRow{{
		pkg: catalog.PackageInfo{ID: "spring-boot", Kind: "sdk", Version: "3.0.0", Source: "axelor"},
	}}
	out := renderRemote(p, rows, true)
	for _, want := range []string{"Catalog", "axelor", "spring-boot"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

// Tags are absent from every listing, so search is the only thing that makes
// them reachable.
func TestSearchMatchesTags(t *testing.T) {
	pkg := catalog.PackageInfo{
		ID: "mvnd", Name: "Maven Daemon", Description: "Fast Maven client",
		Tags: []string{"java", "build-tool"},
	}
	if !searchMatches(pkg, "build-tool") {
		t.Error("a tag should be searchable")
	}
	if !searchMatches(pkg, "maven") {
		t.Error("name should still match")
	}
	if searchMatches(pkg, "node") {
		t.Error("unrelated query should not match")
	}
}

func TestListFilters(t *testing.T) {
	c := &ListCmd{Tag: "java", Capability: "jdk"}
	if !c.matchesTag([]string{"java", "jdk"}) || c.matchesTag([]string{"cli"}) {
		t.Fatal("tag filter mismatch")
	}
	if !c.matchesCapability("jdk") || c.matchesCapability("node") {
		t.Fatal("capability filter mismatch")
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
