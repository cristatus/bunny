package main

import (
	"fmt"

	"github.com/charmbracelet/log"
)

// ReshimCmd regenerates shims for runtime-installed global executables
// (e.g. tools added via `npm install -g`).
type ReshimCmd struct {
	Target string `arg:"" optional:"" help:"Capability or package id to reshim (default: all)"`
}

func (c *ReshimCmd) Run(a *App) error {
	return a.withMutation(a.context(), func() error {
		capability := ""
		if c.Target != "" {
			if a.State.IsInstalled(c.Target) {
				m, err := a.loadInstalledManifest(c.Target)
				if err != nil {
					return err
				}
				if m.Provides != "" {
					capability = m.Provides
				} else {
					capability = c.Target
				}
			} else if a.knownCapability(c.Target) {
				capability = c.Target
			} else {
				return fmt.Errorf("%q is neither an installed package nor a capability one provides", c.Target)
			}
		}
		added, removed, err := a.reshimCapabilities(capability)
		if err != nil {
			return err
		}
		log.Info("Reshimmed", "capability", capability, "added", len(added), "removed", len(removed))
		log.Debug("Reshim detail", "dir", a.Paths.Bin(), "addedNames", added, "removedNames", removed)
		p := a.status()
		p.Println()
		p.Printf("reshimmed: %d added, %d removed\n", len(added), len(removed))
		return nil
	})
}

// knownCapability reports whether reshim has anything to act on for
// capability: an installed provider, or global shims still recorded for it,
// which a reshim prunes once their provider is gone.
func (a *App) knownCapability(capability string) bool {
	if _, ok := a.State.Providers[capability]; ok {
		return true
	}
	for _, name := range a.State.GlobalCommandNames() {
		if c, _ := a.State.GlobalCommandCapability(name); c == capability {
			return true
		}
	}
	return false
}
