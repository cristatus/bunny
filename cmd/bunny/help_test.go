package main

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

func TestHelpFlatListNoMascot(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	parser.Stdout = &sb
	_, _ = parser.Parse([]string{"--help"})
	out := sb.String()

	// Flat command list — no group headers.
	for _, g := range []string{"Packages", "Versions", "Maintenance", "Setup"} {
		if strings.Contains(out, g) {
			t.Errorf("help should have no group header %q:\n%s", g, out)
		}
	}
	// Commands are present, in workflow order (install before setup).
	for _, cmd := range []string{"install", "list", "update", "run", "doctor", "setup"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("help missing command %q:\n%s", cmd, out)
		}
	}
	if strings.Index(out, "install") > strings.Index(out, "setup") {
		t.Errorf("workflow order broken: 'install' should precede 'setup'\n%s", out)
	}
	// The maintainer 'dev' command is hidden; no mascot art.
	if strings.Contains(out, "dev update") {
		t.Errorf("maintainer 'dev' should be hidden from help:\n%s", out)
	}
	if strings.Contains(out, "(\\(\\") || strings.Contains(out, "ᴗ") {
		t.Errorf("help must not contain mascot art:\n%s", out)
	}
}
