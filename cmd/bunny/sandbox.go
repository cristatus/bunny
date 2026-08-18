package main

// SandboxCmd launches one installed package with its effective sandbox policy,
// regardless of whether normal activation is always, on-demand, or absent.
// Without --command, the manifest's first binary is used, matching bunny run.
type SandboxCmd struct {
	ID      string   `arg:"" help:"Installed package ID"`
	Command string   `short:"c" help:"Specific command to run"`
	Args    []string `arg:"" optional:"" help:"Arguments passed through to the binary"`
}

func (c *SandboxCmd) Run(a *App) error {
	return a.runSandboxed(c.ID, c.Command, c.Args)
}
