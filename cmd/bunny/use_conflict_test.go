package main

import (
	"os"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/state"
)

func TestUseRefusesUnrelatedCommandOwnership(t *testing.T) {
	p := paths.At(t.TempDir())
	if err := os.MkdirAll(p.Bin(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.BunnyBinary(), []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}
	st := state.Empty()
	st.SetInstalled("node-24", "24", "node", "")
	st.SetInstalled("node-22", "22", "node", "")
	st.SetInstalled("corepack", "1.0", "", "")
	if err := st.SetProviderCommands("node", "node-24", []string{"node"}); err != nil {
		t.Fatal(err)
	}
	st.SetCommand("corepack", "corepack")
	if err := st.Save(p.StateFile()); err != nil {
		t.Fatal(err)
	}
	if err := shim.Install(p.Bin(), []string{"node", "corepack"}, p.BunnyBinary()); err != nil {
		t.Fatal(err)
	}
	cat := reportCatalog{manifests: map[string]*manifest.Manifest{
		"node-22":  {ID: "node-22", Name: "Node 22", Version: "22", Provides: "node", Bin: []manifest.Binary{{Name: "node"}, {Name: "corepack"}}},
		"node-24":  {ID: "node-24", Name: "Node 24", Version: "24", Provides: "node", Bin: []manifest.Binary{{Name: "node"}}},
		"corepack": {ID: "corepack", Name: "Corepack", Version: "1.0", Bin: []manifest.Binary{{Name: "corepack"}}},
	}}
	a := &App{Paths: p, State: st, Catalog: cat, Installed: cat}
	if err := (&UseCmd{ID: "node-22"}).Run(a); err == nil {
		t.Fatal("provider switch took unrelated command")
	}
	if owner, _ := a.State.CommandOwner("corepack"); owner != "corepack" {
		t.Fatalf("unrelated corepack command silently reassigned to %q", owner)
	}
}
