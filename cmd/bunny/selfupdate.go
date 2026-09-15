package main

import (
	"fmt"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/selfupdate"
)

// SelfUpdateCmd checks GitHub for a newer bunny release and, if one exists,
// downloads, verifies, and installs it in place. Independent of the
// catalog/install machinery every other package goes through: bunny is not a
// catalog package and has no state.json entry, so plain `bunny update` never
// mentions it.
type SelfUpdateCmd struct{}

func (c *SelfUpdateCmd) Run(a *App) error {
	p := a.status()
	ctx := a.context()

	r, err := selfupdate.Check(ctx, version)
	if err != nil {
		return fmt.Errorf("check %s for updates: %w", selfupdate.Repo, err)
	}
	p.Println()
	if !r.HasUpdate {
		p.Printf("bunny %s is already up to date\n", version)
		return nil
	}

	if err := selfupdate.Apply(ctx, r, a.Paths.BunnyBinary()); err != nil {
		return err
	}
	log.Info("Updated bunny", "from", version, "to", r.LatestVersion)
	p.Printf("updated bunny %s → %s\n", version, r.LatestVersion)
	return nil
}
