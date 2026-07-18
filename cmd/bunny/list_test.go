package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/ui"
)

func TestRenderInstalled(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	rows := []installedRow{
		{id: "bat", category: "cli", version: "1.0", provides: ""},
		{id: "jbr-21", category: "sdk", version: "21", provides: "jdk"},
		{id: "jdk-21", category: "sdk", version: "21", provides: "jdk", active: true},
	}

	out := renderInstalled(p, rows)
	// Title-case header, no human-name column.
	if !strings.Contains(out, "Provides") || strings.Contains(out, "NAME") || strings.Contains(out, "Name") {
		t.Errorf("header wrong (want title-case, no name col): %q", out)
	}
	if !strings.Contains(out, "jdk (active)") {
		t.Error("active provider should be marked (active)")
	}
	// The inactive sibling shows the bare capability, not the marker.
	jbrLine := lineWith(t, out, "jbr-21")
	if !strings.Contains(jbrLine, "jdk") || strings.Contains(jbrLine, "active") {
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
