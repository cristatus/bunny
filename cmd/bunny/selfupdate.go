package main

import (
	"fmt"

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
	if !r.HasUpdate {
		p.Printf("bunny %s is already up to date\n", version)
		return nil
	}

	p.Printf("updating bunny %s → %s...\n", version, r.LatestVersion)
	if err := selfupdate.Apply(ctx, r, a.Paths.BunnyBinary()); err != nil {
		return err
	}
	p.Printf("bunny updated to %s\n", r.LatestVersion)
	return nil
}
