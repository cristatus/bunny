package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/ui"
)

func TestRenderSearch(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	rows := []catalogRow{
		{pkg: catalog.PackageInfo{ID: "jdk-21", Version: "21.0.11", Description: "Temurin 21", Provides: "jdk"},
			status: "installed", statusStyle: ui.Good},
		{pkg: catalog.PackageInfo{ID: "corretto-21", Version: "21.0.11", Description: "Amazon 21"}},
	}
	got := renderCatalog(p, rows, false, "matches")
	if !strings.Contains(got, "installed") {
		t.Fatalf("installed marker missing: %q", got)
	}
	if !strings.Contains(got, "2 matches") {
		t.Fatalf("count missing: %q", got)
	}
	if !strings.Contains(got, "Provides") || !strings.Contains(got, "jdk") {
		t.Fatalf("capability column missing: %q", got)
	}
	if !strings.Contains(got, "Temurin 21") {
		t.Fatalf("description column missing: %q", got)
	}
}

func TestContainsFoldMatchesRequirements(t *testing.T) {
	if !containsFold([]string{"jdk>=17"}, "jdk") {
		t.Fatal("a requirement should match case-insensitively")
	}
}

// Ranking is the difference between a useful result list and an alphabetical
// one: "node" must not put a package that merely mentions nodes in its
// description above the runtime itself.
func TestSearchScoreRanksFieldsByStrength(t *testing.T) {
	node := catalog.PackageInfo{ID: "node-22", Name: "Node.js", Provides: "node", Description: "JavaScript runtime"}
	kind := catalog.PackageInfo{ID: "kind", Description: "Kubernetes clusters with containers as nodes"}
	tomcat := catalog.PackageInfo{ID: "tomcat", Requires: []string{"jdk>=17"}}
	jdk := catalog.PackageInfo{ID: "jdk-21", Provides: "jdk"}

	if searchScore(node, "node") <= searchScore(kind, "node") {
		t.Error("an id and capability hit must outrank a description hit")
	}
	if searchScore(jdk, "jdk") <= searchScore(tomcat, "jdk") {
		t.Error("a provided capability must outrank a requirement on it")
	}
	if searchScore(node, "runtime") != scoreDescription {
		t.Error("a description-only hit should score as one")
	}
	if searchScore(node, "maven") != scoreNone {
		t.Error("an unrelated term must not match")
	}
}

// Terms narrow rather than accumulate, and the weakest one sets the rank.
func TestScoreQueryRequiresEveryTerm(t *testing.T) {
	pkg := catalog.PackageInfo{ID: "node-22", Provides: "node", Description: "Maintenance LTS"}
	if _, ok := scoreQuery(pkg, []string{"node", "lts"}); !ok {
		t.Fatal("a package matching both terms should match")
	}
	if _, ok := scoreQuery(pkg, []string{"node", "maven"}); ok {
		t.Fatal("one missing term should reject the package")
	}
	if score, _ := scoreQuery(pkg, []string{"node", "lts"}); score != scoreDescription {
		t.Errorf("the weakest term should set the rank, got %d", score)
	}
	// A filter-only browse: everything ties, so id order decides.
	if score, ok := scoreQuery(pkg, nil); !ok || score != scoreNone {
		t.Errorf("an empty query should tie at scoreNone, got %d,%v", score, ok)
	}
}

// A quoted empty argument used to match every package as a substring.
func TestSearchTermsDropBlanks(t *testing.T) {
	c := &SearchCmd{Query: []string{"", "  ", "JDK"}}
	got := c.terms()
	if len(got) != 1 || got[0] != "jdk" {
		t.Errorf("terms = %q, want [jdk] (blanks dropped, lowercased)", got)
	}
}
