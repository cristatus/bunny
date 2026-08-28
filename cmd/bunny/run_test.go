package main

import (
	"slices"
	"testing"

	"github.com/alecthomas/kong"
)

func TestRunCommandParsesPackageCommandAndArgs(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "vscode", "--command", "code", "--", "--new-window", "."}); err != nil {
		t.Fatal(err)
	}
	// The literal "--" survives kong's passthrough positional (it is not
	// stripped like it is for a non-passthrough one); trimLeadingDashDash,
	// not the parser, is what removes it before exec.
	if cli.Run.ID != "vscode" || cli.Run.Command != "code" ||
		!slices.Equal(cli.Run.Args, []string{"--", "--new-window", "."}) {
		t.Fatalf("unexpected run command: %+v", cli.Run)
	}
}

// An unrecognized flag reaches the binary untouched, no "--" required.
func TestRunCommandPassthroughUnrecognizedFlag(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "node", "--inspect", "v8"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cli.Run.Args, []string{"--inspect", "v8"}) {
		t.Fatalf("unexpected run command: %+v", cli.Run)
	}
}

// A tool flag that happens to share a name with one of bunny's own
// (--sandbox-profile here) is claimed by bunny regardless of position;
// passthrough only rescues names bunny does not define. "--" is the escape
// for that case, same as any other CLI wrapper.
func TestRunCommandCollidingFlagNameNeedsDashDash(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "node", "--sandbox-profile", "v8"}); err != nil {
		t.Fatal(err)
	}
	if cli.Run.SandboxProfile != "v8" || len(cli.Run.Args) != 0 {
		t.Fatalf("expected --sandbox-profile claimed by bunny without --: %+v", cli.Run)
	}

	cli = CLI{}
	parser, err = kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "node", "--", "--sandbox-profile", "v8"}); err != nil {
		t.Fatal(err)
	}
	if cli.Run.SandboxProfile != "" || !slices.Equal(trimLeadingDashDash(cli.Run.Args), []string{"--sandbox-profile", "v8"}) {
		t.Fatalf("expected -- to force --sandbox-profile through to the binary: %+v", cli.Run)
	}
}

// --sandbox as a bare bool flag, with the profile value split into its own
// --sandbox-profile flag, means a bare package id right after --sandbox is
// never mistaken for a value: "--sandbox profile" would otherwise read as
// one two-word phrase, since "sandbox profile" is already an established
// term (a named policy under sandbox.profiles) elsewhere in the config.
func TestRunCommandSandboxFlagTakesNoValue(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "--sandbox", "profile", "codex"}); err != nil {
		t.Fatal(err)
	}
	if !cli.Run.Sandbox || cli.Run.ID != "profile" || !slices.Equal(cli.Run.Args, []string{"codex"}) {
		t.Fatalf("expected --sandbox to take no value, leaving \"profile\" as the id: %+v", cli.Run)
	}
}

// A tool's own --profile flag (e.g. an AWS-style CLI) no longer collides
// with bunny's: the sandbox-profile override is spelled --sandbox-profile,
// so --profile passes through untouched without needing "--".
func TestRunCommandPlainProfileFlagPassesThrough(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "aws-cli", "--profile", "prod"}); err != nil {
		t.Fatal(err)
	}
	if cli.Run.SandboxProfile != "" || !slices.Equal(cli.Run.Args, []string{"--profile", "prod"}) {
		t.Fatalf("expected --profile to pass through untouched: %+v", cli.Run)
	}
}

func TestRunCommandForcesSandboxAndExplain(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "--sandbox", "--sandbox-profile", "offline-cli", "--explain", "node"}); err != nil {
		t.Fatal(err)
	}
	if !cli.Run.Sandbox || cli.Run.SandboxProfile != "offline-cli" || !cli.Run.Explain || cli.Run.ID != "node" {
		t.Fatalf("unexpected run command: %+v", cli.Run)
	}
}

func TestTrimLeadingDashDash(t *testing.T) {
	if got := trimLeadingDashDash([]string{"--", "a", "b"}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if got := trimLeadingDashDash([]string{"a", "b"}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if got := trimLeadingDashDash(nil); got != nil {
		t.Fatalf("got %v", got)
	}
}
