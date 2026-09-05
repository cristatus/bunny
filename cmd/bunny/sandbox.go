package main

import (
	"os"

	"github.com/cristatus/bunny/internal/runtime"
	"github.com/cristatus/bunny/internal/ui"
)

// SandboxCmd groups sandbox diagnostics separately from package execution.
type SandboxCmd struct {
	Check SandboxCheckCmd `cmd:"" help:"Check whether a package's sandbox can run"`
}

type SandboxCheckCmd struct {
	ID      string `arg:"" help:"Installed package ID"`
	Command string `short:"c" help:"Specific package command to resolve"`
	Profile string `help:"Sandbox profile to check; defaults to the package's configured policy"`
}

func (c *SandboxCheckCmd) Run(a *App) error {
	prep, err := a.preparePackage(c.ID, c.Command, nil)
	if err != nil {
		return err
	}
	p := ui.New(os.Stdout)
	out, err := runtime.CheckSandbox(prep, a.Config, c.Profile, p)
	if out != "" {
		p.Println()
		p.Print(out)
	}
	return err
}
