package main

import (
	"os"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/state"
)

func TestUseSwitchesProviderCommandShims(t *testing.T) {
	p := paths.At(t.TempDir())
	if err := os.MkdirAll(p.Bin(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.BunnyBinary(), []byte("binary"), 0755); err != nil {
		t.Fatal(err)
	}

	st := state.Empty()
	st.SetInstalled("node-24", "24", "node")
	st.SetInstalled("node-22", "22", "node")
	if err := st.SetProviderCommands("node", "node-22", []string{"node", "old-only"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(p.StateFile()); err != nil {
		t.Fatal(err)
	}
	if err := shim.Install(p.Bin(), []string{"node", "old-only"}, p.BunnyBinary()); err != nil {
		t.Fatal(err)
	}

	cat := reportCatalog{manifests: map[string]*manifest.Manifest{
		"node-22": {
			ID: "node-22", Name: "Node 22", Version: "22", Provides: "node",
			Bin: []manifest.Binary{{Name: "node"}, {Name: "old-only"}},
		},
		"node-24": {
			ID: "node-24", Name: "Node 24", Version: "24", Provides: "node",
			Bin: []manifest.Binary{{Name: "node"}, {Name: "new-only"}},
		},
	}}
	a := &App{Paths: p, State: st, Catalog: cat, Installed: cat}
	if err := (&UseCmd{ID: "node-24"}).Run(a); err != nil {
		t.Fatal(err)
	}

	if got := a.State.ResolveProvider("node"); got != "node-24" {
		t.Fatalf("provider = %q, want node-24", got)
	}
	if _, err := os.Lstat(p.Shim("old-only")); !os.IsNotExist(err) {
		t.Fatalf("old provider shim should be removed, err=%v", err)
	}
	if _, err := os.Lstat(p.Shim("new-only")); err != nil {
		t.Fatalf("new provider shim missing: %v", err)
	}
	if owner, ok := a.State.CommandOwner("new-only"); !ok || owner != "node-24" {
		t.Fatalf("new provider command owner = %q, %v", owner, ok)
	}
}
