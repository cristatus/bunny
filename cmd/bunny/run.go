package main

// RunCmd launches a package binary. Without --command, the first binary in
// the manifest's `bin:` list is used.
type RunCmd struct {
	ID      string   `arg:"" help:"Package ID"`
	Command string   `short:"c" help:"Specific command to run"`
	Args    []string `arg:"" optional:"" help:"Arguments passed through to the binary"`
}

func (c *RunCmd) Run(a *App) error { return a.run(c.ID, c.Command, c.Args) }
