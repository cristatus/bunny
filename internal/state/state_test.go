package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Packages) != 0 || len(s.Commands) != 0 || len(s.Providers) != 0 {
		t.Errorf("expected empty maps, got %+v", s)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := Empty()
	s.SetInstalled("node-22", "22.10.0", "node")
	s.SetInstalled("vscode", "1.98.2", "")
	s.SetCommand("node", "node-22")
	s.SetCommand("code", "vscode")

	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.IsInstalled("node-22") || !loaded.IsInstalled("vscode") {
		t.Error("missing packages")
	}
	if loaded.Providers["node"] != "node-22" {
		t.Errorf("provider node: got %q", loaded.Providers["node"])
	}
	if id, _ := loaded.CommandOwner("code"); id != "vscode" {
		t.Errorf("command code: got %q", id)
	}
}

func TestSaveAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := Empty()
	s.SetInstalled("aa", "1", "")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".state.json.*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("expected tmp files to be cleaned up, got %v", matches)
	}
}

// Load repairs recoverable inconsistencies so read-only commands still work on
// a slightly-corrupt state file, rather than bricking every command.
func TestLoadRepairsInconsistentState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data := []byte(`{"version":1,"packages":{},"commands":{"node":"node-22"}}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load should repair, not reject: %v", err)
	}
	if _, ok := s.CommandOwner("node"); ok {
		t.Error("dangling command referencing a missing package should be pruned")
	}
	if err := s.Validate(); err != nil {
		t.Errorf("repaired state should be internally consistent: %v", err)
	}
}

func TestLoadRepairsUnsafeCommandName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data := []byte(`{"version":1,"packages":{"tool":{"version":"1"}},"commands":{"../../escape":"tool"}}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load should repair, not reject: %v", err)
	}
	if _, ok := s.CommandOwner("../../escape"); ok {
		t.Error("unsafe command name should be pruned")
	}
}

// A state written by a newer bunny cannot be safely interpreted, so it is the
// one load-time failure kept hard rather than repaired.
func TestLoadRejectsFutureSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":999,"packages":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected a future schema version to be rejected")
	}
}

// Writes stay strict: Save must refuse to persist an inconsistent state even
// though Load tolerates one.
func TestSaveRejectsInconsistentState(t *testing.T) {
	s := Empty()
	s.Commands["node"] = "node-22" // no such package
	if err := s.Save(filepath.Join(t.TempDir(), "state.json")); err == nil {
		t.Fatal("Save must reject inconsistent state")
	}
}

func TestSetUninstalledClearsCommandsAndProviders(t *testing.T) {
	s := Empty()
	s.SetInstalled("node-22", "22", "node")
	s.SetCommand("node", "node-22")
	s.SetCommand("npm", "node-22")

	s.SetUninstalled("node-22")

	if s.IsInstalled("node-22") {
		t.Error("still installed after uninstall")
	}
	if _, ok := s.Providers["node"]; ok {
		t.Error("provider not cleared")
	}
	if _, ok := s.Commands["node"]; ok {
		t.Error("command not cleared")
	}
	if _, ok := s.Commands["npm"]; ok {
		t.Error("command npm not cleared")
	}
}

func TestSetUninstalledSelectsFallbackProvider(t *testing.T) {
	s := Empty()
	s.SetInstalled("node-22", "22.0.0", "node")
	s.SetInstalled("node-24", "24.0.0", "node")
	s.SetUninstalled("node-24")
	if got := s.ResolveProvider("node"); got != "node-22" {
		t.Errorf("fallback provider = %q, want node-22", got)
	}
}

func TestSetCommandsReplacesOwnedCommands(t *testing.T) {
	s := Empty()
	s.SetCommand("old", "pkg")
	s.SetCommand("keep", "other")

	s.SetCommands("pkg", []string{"new", "newer"})

	if _, ok := s.CommandOwner("old"); ok {
		t.Error("old command should be removed")
	}
	if owner, ok := s.CommandOwner("new"); !ok || owner != "pkg" {
		t.Errorf("new owner: got %q %v", owner, ok)
	}
	if owner, ok := s.CommandOwner("keep"); !ok || owner != "other" {
		t.Errorf("other package command should be preserved, got %q %v", owner, ok)
	}
}

func TestSetProviderRequiresInstalled(t *testing.T) {
	s := Empty()
	if err := s.SetProvider("node", "node-22"); err == nil {
		t.Error("expected error when setting provider for uninstalled pkg")
	}
	s.SetInstalled("node-22", "22", "node")
	if err := s.SetProvider("node", "node-22"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSetProviderCommandsRemovesPreviousProviderCommands(t *testing.T) {
	s := Empty()
	s.SetInstalled("node-22", "22", "node")
	s.SetCommands("node-22", []string{"node", "old-only"})
	s.SetInstalled("node-24", "24", "node")
	if err := s.SetProviderCommands("node", "node-24", []string{"node", "new-only"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.CommandOwner("old-only"); ok {
		t.Fatal("command unique to previous provider remained active")
	}
	if owner, ok := s.CommandOwner("new-only"); !ok || owner != "node-24" {
		t.Fatalf("new command owner = %q, %v", owner, ok)
	}
}

func TestResolveProviderHandlesIDAndCapability(t *testing.T) {
	s := Empty()
	s.SetInstalled("node-22", "22", "node")

	if got := s.ResolveProvider("node-22"); got != "node-22" {
		t.Errorf("by ID: got %q", got)
	}
	if got := s.ResolveProvider("node"); got != "node-22" {
		t.Errorf("by capability: got %q", got)
	}
	if got := s.ResolveProvider("missing"); got != "" {
		t.Errorf("missing: got %q", got)
	}
}

func TestGlobalCommands(t *testing.T) {
	s := Empty()
	s.SetGlobalCommand("tsc", "node")
	s.SetGlobalCommand("prettier", "node")
	if cap, ok := s.GlobalCommandCapability("tsc"); !ok || cap != "node" {
		t.Fatalf("GlobalCommandCapability(tsc) = %q,%v", cap, ok)
	}
	if got := s.GlobalCommandNames(); len(got) != 2 || got[0] != "prettier" || got[1] != "tsc" {
		t.Errorf("GlobalCommandNames = %v (want sorted [prettier tsc])", got)
	}
	s.RemoveGlobalCommand("tsc")
	if _, ok := s.GlobalCommandCapability("tsc"); ok {
		t.Error("tsc should be removed")
	}
}

func TestResolveProviderMin(t *testing.T) {
	s := Empty()
	s.SetInstalled("jdk-11", "11.0.31+11", "jdk")
	s.SetInstalled("jdk-21", "21.0.11+10", "jdk")
	s.SetInstalled("jdk-25", "25.0.3+9", "jdk")

	// active satisfies → active
	_ = s.SetProvider("jdk", "jdk-21")
	if got := s.ResolveProviderMin("jdk", 17); got != "jdk-21" {
		t.Errorf("active satisfies: got %q, want jdk-21", got)
	}

	// active too old → highest-major satisfying installed
	_ = s.SetProvider("jdk", "jdk-11")
	if got := s.ResolveProviderMin("jdk", 17); got != "jdk-25" {
		t.Errorf("active too old: got %q, want jdk-25", got)
	}

	// none satisfies → ""
	if got := s.ResolveProviderMin("jdk", 99); got != "" {
		t.Errorf("none satisfies: got %q, want \"\"", got)
	}

	// unknown capability → ""
	if got := s.ResolveProviderMin("node", 1); got != "" {
		t.Errorf("unknown capability: got %q, want \"\"", got)
	}
}

func TestGlobalCommandsSurviveSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"
	s := Empty()
	s.SetGlobalCommand("tsc", "node")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cap, ok := loaded.GlobalCommandCapability("tsc"); !ok || cap != "node" {
		t.Errorf("after reload: %q,%v", cap, ok)
	}
}
