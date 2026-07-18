package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/ui"
)

func TestRenderInfoInstalledWithUpdate(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	m := &manifest.Manifest{
		ID: "jdk-21", Name: "Eclipse Temurin JDK 21", Version: "21.0.12",
		Description: "OpenJDK 21 LTS", Provides: "jdk",
	}
	got := renderInfo(p, m, "21.0.11", true)
	if !strings.Contains(got, "jdk-21") || !strings.Contains(got, "installed") {
		t.Fatalf("header wrong: %q", got)
	}
	if !strings.Contains(got, "update available") {
		t.Fatalf("expected update-available (catalog 21.0.12 > installed 21.0.11): %q", got)
	}
	if !strings.Contains(got, "Provides: jdk") || !strings.Contains(got, "Id: jdk-21") {
		t.Fatalf("aligned key rows missing: %q", got)
	}
}

func TestRenderInfoNotInstalled(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	m := &manifest.Manifest{ID: "maven", Name: "Maven", Version: "3.9.6"}
	got := renderInfo(p, m, "", false)
	if !strings.Contains(got, "not installed") {
		t.Fatalf("expected not-installed header: %q", got)
	}
	if strings.Contains(got, "update available") {
		t.Fatalf("must not claim update for uninstalled pkg: %q", got)
	}
	if !strings.Contains(got, "run 'bunny install maven' to add") {
		t.Fatalf("not-installed package should get an install hint: %q", got)
	}
}
