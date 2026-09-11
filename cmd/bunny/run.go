package main

import (
	"errors"

	"github.com/cristatus/bunny/internal/runtime"
)

// RunCmd launches a package binary. Without --command, the first binary in
// the manifest's `bin:` list is used.
//
// --sandbox forces the effective sandbox policy for this launch even for a
// package normal launches would run directly; --sandbox-profile overrides
// the profile it resolves to (implies --sandbox: naming a profile only
// makes sense if the policy is actually applied). Splitting the value out to
// its own flag, rather than letting --sandbox itself take an optional
// profile name, avoids `--sandbox profile` reading as one two-word phrase —
// "sandbox profile" is already an established term (a named policy under
// sandbox.profiles) — so a bare package id right after --sandbox is never
// mistaken for a value. --explain prints what the launch would do — direct
// or sandboxed, matching --sandbox and the package's configured activation —
// without touching the host. --no-sandbox is the counterpart to --sandbox:
// it skips the policy a sandbox.packages entry would apply, so "is the
// sandbox what broke this?" is answerable in one command rather than by
// editing the config and putting it back.
//
// Args is passthrough: once kong starts consuming it, an unrecognized flag
// reaches the binary untouched instead of erroring, no "--" required. A tool
// flag that happens to share a name with one of bunny's own (-c,
// --sandbox-profile, --sandbox, --explain) is still claimed by bunny,
// though — passthrough only rescues names bunny does not define — so an
// explicit "--" is the escape for that case, same as any other CLI wrapper.
// Kong keeps that "--" in Args literally rather than consuming it, so it is
// stripped before exec; the binary never sees it.
type RunCmd struct {
	ID             string   `arg:"" help:"Package ID"`
	Command        string   `short:"c" help:"Specific command to run"`
	Sandbox        bool     `help:"Force the sandbox policy for this launch even if not configured"`
	NoSandbox      bool     `help:"Skip the configured sandbox policy for this launch"`
	SandboxProfile string   `help:"Override the configured sandbox profile for this launch (implies --sandbox)"`
	Explain        bool     `help:"Print what this launch would do without launching"`
	Args           []string `arg:"" optional:"" passthrough:"" help:"Arguments passed through to the binary"`
}

func (c *RunCmd) Run(a *App) error {
	activation, err := c.activation()
	if err != nil {
		return err
	}
	return a.runPackage(c.ID, c.Command, trimLeadingDashDash(c.Args), activation, c.SandboxProfile, c.Explain)
}

// activation folds the three sandbox flags into the one decision they
// describe. Asking for the policy and for no policy in the same command has
// no defensible reading, so it is refused rather than resolved by precedence
// a user would have to memorize.
func (c *RunCmd) activation() (runtime.Activation, error) {
	forced := c.Sandbox || c.SandboxProfile != ""
	switch {
	case c.NoSandbox && forced:
		return 0, errors.New("--no-sandbox cannot be combined with --sandbox or --sandbox-profile")
	case c.NoSandbox:
		return runtime.ActivationBypassed, nil
	case forced:
		return runtime.ActivationForced, nil
	}
	return runtime.ActivationDefault, nil
}

// trimLeadingDashDash drops one leading "--" a user typed to mark the end of
// bunny's own flags: kong's passthrough positional keeps it literally rather
// than consuming it, and the wrapped binary never asked to receive it.
func trimLeadingDashDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}
