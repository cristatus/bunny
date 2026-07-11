package main

import (
	"strings"
	"testing"
)

func TestRenderInstalled(t *testing.T) {
	rows := []installedRow{
		{id: "bat", category: "cli", name: "bat", version: "1.0", provides: ""},
		{id: "jbr-21", category: "sdk", name: "JetBrains Runtime", version: "21", provides: "jdk"},
		{id: "jdk-21", category: "sdk", name: "Eclipse Temurin", version: "21", provides: "jdk", active: true},
	}

	out := renderInstalled(rows)
	if !strings.Contains(out, "PROVIDES") {
		t.Error("missing PROVIDES header")
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
