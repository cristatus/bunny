package main

import (
	"github.com/cristatus/bunny/internal/runtime"
)

// NetsetupCmd is the inside-the-namespace half of the egress composition: it
// re-enters Bunny under pasta while capabilities remain, installs the
// nftables ruleset, and execs the sandbox command line. Internal.
type NetsetupCmd struct {
	Rules string   `required:"" help:"nftables ruleset file to install"`
	Nft   string   `help:"Absolute path to nft, resolved by the trusted parent"`
	Argv  []string `arg:"" passthrough:"" help:"Command to exec after ruleset installation"`
}

func (c *NetsetupCmd) Run(a *App) error {
	return runtime.ApplyNetsetup(c.Rules, c.Nft, c.Argv)
}
