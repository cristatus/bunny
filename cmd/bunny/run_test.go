package main

import (
	"slices"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/cristatus/bunny/internal/runtime"
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
	if _, err := parser.Parse([]string{"run", "--sandbox", "--sandbox-profile", "offline", "--explain", "node"}); err != nil {
		t.Fatal(err)
	}
	if !cli.Run.Sandbox || cli.Run.SandboxProfile != "offline" || !cli.Run.Explain || cli.Run.ID != "node" {
		t.Fatalf("unexpected run command: %+v", cli.Run)
	}
}

// The point of --no-sandbox is answering "is the sandbox what broke this?"
// without editing config.yaml, so it has to resolve to the direct path even
// for a package sandbox.packages arms.
func TestRunCommandBypassesTheSandbox(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "--no-sandbox", "code"}); err != nil {
		t.Fatal(err)
	}
	activation, err := cli.Run.activation()
	if err != nil {
		t.Fatal(err)
	}
	if activation != runtime.ActivationBypassed {
		t.Fatalf("--no-sandbox must bypass the policy, got %v", activation)
	}
}

// Asking for the policy and for no policy at once has no defensible reading;
// resolving it by precedence would mean one of the two flags silently does
// nothing.
func TestRunCommandRejectsConflictingSandboxFlags(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--no-sandbox", "--sandbox", "code"},
		{"run", "--no-sandbox", "--sandbox-profile", "offline", "code"},
	} {
		var cli CLI
		parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := parser.Parse(args); err != nil {
			t.Fatal(err)
		}
		if _, err := cli.Run.activation(); err == nil {
			t.Errorf("%v must be refused", args)
		}
	}
}

// Without the flag nothing changes: the package's own entry decides.
func TestRunCommandDefaultsToConfiguredActivation(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"run", "code"}); err != nil {
		t.Fatal(err)
	}
	if activation, err := cli.Run.activation(); err != nil || activation != runtime.ActivationDefault {
		t.Fatalf("plain run must leave activation to the config: %v, %v", activation, err)
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

func TestSandboxCheckCommandParses(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("bunny"), kong.Exit(func(int) {}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Parse([]string{"sandbox", "check", "codex", "--profile", "agent", "--command", "codex"}); err != nil {
		t.Fatal(err)
	}
	if cli.Sandbox.Check.ID != "codex" || cli.Sandbox.Check.Profile != "agent" || cli.Sandbox.Check.Command != "codex" {
		t.Fatalf("unexpected sandbox check: %+v", cli.Sandbox.Check)
	}
}
