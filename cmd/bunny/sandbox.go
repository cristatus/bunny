package main

import (
	"fmt"
	"os"

	"github.com/cristatus/bunny/internal/runtime"
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
	out, err := runtime.CheckSandbox(prep, a.Config, c.Profile)
	if out != "" {
		fmt.Fprint(os.Stdout, out)
	}
	return err
}
