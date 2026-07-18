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
	pkgs := []catalog.PackageInfo{
		{ID: "jdk-21", Version: "21.0.11", Description: "Temurin 21"},
		{ID: "corretto-21", Version: "21.0.11", Description: "Amazon 21"},
	}
	got := renderSearch(p, "jdk", pkgs, map[string]bool{"jdk-21": true})
	if !strings.Contains(got, "installed") {
		t.Fatalf("installed marker missing: %q", got)
	}
	if !strings.Contains(got, "2 matches") {
		t.Fatalf("count missing: %q", got)
	}
}

func TestRenderSearchZero(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	got := renderSearch(p, "zzz", nil, nil)
	if !strings.Contains(got, "no matches") {
		t.Fatalf("zero-match message missing: %q", got)
	}
}
