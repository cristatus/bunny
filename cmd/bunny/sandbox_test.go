package main

import (
	"slices"
	"testing"

	"github.com/alecthomas/kong"
)

func TestSandboxCommandParsesPackageCommandAndArgs(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"sandbox", "vscode", "--command", "code", "--", "--new-window", "."}); err != nil {
		t.Fatal(err)
	}
	if cli.Sandbox.ID != "vscode" || cli.Sandbox.Command != "code" || !slices.Equal(cli.Sandbox.Args, []string{"--new-window", "."}) {
		t.Fatalf("unexpected sandbox command: %+v", cli.Sandbox)
	}
}
