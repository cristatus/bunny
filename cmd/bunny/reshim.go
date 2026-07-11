package main

import "github.com/charmbracelet/log"

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
			} else {
				capability = c.Target
			}
		}
		added, removed, err := a.reshimCapabilities(capability)
		if err != nil {
			return err
		}
		log.Info("Reshim complete", "added", len(added), "removed", len(removed))
		return nil
	})
}
