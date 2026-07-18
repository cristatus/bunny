package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/ui"
)

// PinCmd writes a project-local version pin to ./.bunny-version so the given
// capability resolves to a specific version in this directory tree.
type PinCmd struct {
	Capability string `arg:"" help:"Capability to pin (e.g. node, jdk)"`
	Version    string `arg:"" help:"Version to pin (e.g. 22)"`
}

func (c *PinCmd) Run(a *App) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if err := shim.WriteProjectVersion(cwd, c.Capability, c.Version); err != nil {
		return fmt.Errorf("write pin: %w", err)
	}
	p := ui.New(os.Stdout)
	p.Println()
	p.Print(pinConfirmation(c.Capability, c.Version))
	// The pin resolves to "<capability>-<version>"; warn if it isn't installed.
	if candidate := c.Capability + "-" + c.Version; !a.State.IsInstalled(candidate) {
		log.Warn("pinned version is not installed", "package", candidate)
	}
	return nil
}

// pinConfirmation is the one-line result of a successful pin.
func pinConfirmation(capability, version string) string {
	return fmt.Sprintf("pinned %s to %s in ./%s\n", capability, version, shim.ProjectVersionFile)
}

// UnpinCmd removes a capability's pin from ./.bunny-version.
type UnpinCmd struct {
	Capability string `arg:"" help:"Capability to unpin (e.g. node, jdk)"`
}

func (c *UnpinCmd) Run(_ *App) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	removed, err := shim.RemoveProjectVersion(cwd, c.Capability)
	if err != nil {
		return fmt.Errorf("remove pin: %w", err)
	}
	p := ui.New(os.Stdout)
	p.Println()
	if !removed {
		p.Println("no pin to remove for " + c.Capability)
		return nil
	}
	p.Println("unpinned " + c.Capability)
	return nil
}
