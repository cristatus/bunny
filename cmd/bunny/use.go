package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/state"
	"github.com/cristatus/bunny/internal/ui"
)

// UseCmd switches the active provider for a capability and re-points every
// shared command shim at the chosen package.
type UseCmd struct {
	ID string `arg:"" help:"Package ID"`
}

func (c *UseCmd) Run(a *App) error {
	return a.withMutation(a.context(), func() error {
		if !a.State.IsInstalled(c.ID) {
			return fmt.Errorf("package %q is not installed", c.ID)
		}
		m, err := a.loadInstalledManifest(c.ID)
		if err != nil {
			return fmt.Errorf("load manifest: %w", err)
		}
		if m.Provides == "" {
			return fmt.Errorf("package %q has no `provides:` capability — nothing to switch", c.ID)
		}
		names := make([]string, 0, len(m.Bin))
		for _, b := range m.Bin {
			names = append(names, b.Name)
		}
		oldNames := a.State.CommandsForCapability(m.Provides)
		stateBefore := a.State.Clone()
		bunnyPath, err := shim.BunnyBinaryPath(a.Paths.Bin())
		if err != nil {
			return fmt.Errorf("locate bunny binary: %w", err)
		}
		if err := shim.Install(a.Paths.Bin(), names, bunnyPath); err != nil {
			return fmt.Errorf("install provider shims: %w", err)
		}
		if err := shim.Remove(a.Paths.Bin(), shim.Difference(oldNames, names)); err != nil {
			rollbackProviderSwitch(a, stateBefore, oldNames, names, bunnyPath)
			return fmt.Errorf("remove previous provider shims: %w", err)
		}
		if err := a.State.SetProviderCommands(m.Provides, c.ID, names); err != nil {
			rollbackProviderSwitch(a, stateBefore, oldNames, names, bunnyPath)
			return err
		}
		// State is persisted once, by reshimCapabilities below, which also
		// records any global-tool shim changes — avoiding a redundant write and
		// a window where the provider switch is saved but the reshim is not.
		if added, removed, err := a.reshimCapabilities(m.Provides); err != nil {
			rollbackProviderSwitch(a, stateBefore, oldNames, names, bunnyPath)
			saveErr := a.State.Save(a.Paths.StateFile())
			return errors.Join(fmt.Errorf("reshim after provider switch: %w", err), saveErr)
		} else if len(added)+len(removed) > 0 {
			log.Debug("Reshimmed global tools", "capability", m.Provides, "added", len(added), "removed", len(removed))
		}
		p := ui.New(os.Stdout)
		p.Println()
		p.Println(fmt.Sprintf("switched %s to %s", m.Provides, c.ID))
		return nil
	})
}

func rollbackProviderSwitch(a *App, before *state.State, oldNames, newNames []string, bunnyPath string) {
	*a.State = *before
	if err := shim.Remove(a.Paths.Bin(), shim.Difference(newNames, oldNames)); err != nil {
		log.Warn("Failed to remove provider shims during rollback", "error", err)
	}
	if err := shim.Install(a.Paths.Bin(), oldNames, bunnyPath); err != nil {
		log.Warn("Failed to restore provider shims during rollback", "error", err)
	}
}
