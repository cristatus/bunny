package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/shim"
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
	log.Info("Pinned", "capability", c.Capability, "version", c.Version,
		"file", filepath.Join(cwd, shim.ProjectVersionFile))
	// The pin resolves to "<capability>-<version>"; nudge if it isn't installed.
	if candidate := c.Capability + "-" + c.Version; !a.State.IsInstalled(candidate) {
		log.Info("Pinned version is not installed yet — run 'bunny install " + candidate + "'")
	}
	return nil
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
	if !removed {
		log.Info("No pin to remove", "capability", c.Capability)
		return nil
	}
	log.Info("Unpinned", "capability", c.Capability)
	return nil
}
