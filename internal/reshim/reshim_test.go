package reshim

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanAddsAndRemoves(t *testing.T) {
	providers := []Provider{{Capability: "node", Tools: []string{"tsc", "prettier"}}}
	current := map[string]string{"prettier": "node", "stale": "node"}
	add, remove, conflicts := Plan(providers, map[string]bool{}, current)

	if !reflect.DeepEqual(add, map[string]string{"tsc": "node"}) {
		t.Errorf("add = %v, want {tsc:node}", add)
	}
	if !reflect.DeepEqual(remove, []string{"stale"}) {
		t.Errorf("remove = %v, want [stale]", remove)
	}
	if len(conflicts) != 0 {
		t.Errorf("conflicts = %v", conflicts)
	}
}

func TestPlanProtectsSDKShims(t *testing.T) {
	providers := []Provider{{Capability: "node", Tools: []string{"node", "tsc"}}}
	add, _, _ := Plan(providers, map[string]bool{"node": true}, map[string]string{})
	if _, ok := add["node"]; ok {
		t.Error("protected SDK command 'node' must not be added as a global shim")
	}
	if add["tsc"] != "node" {
		t.Errorf("tsc should still be added, add=%v", add)
	}
}

func TestPlanFirstWinsOnCollision(t *testing.T) {
	providers := []Provider{
		{Capability: "node", Tools: []string{"foo"}},
		{Capability: "python", Tools: []string{"foo"}},
	}
	add, _, conflicts := Plan(providers, map[string]bool{}, map[string]string{})
	if add["foo"] != "node" {
		t.Errorf("first-wins: foo should be node, got %q", add["foo"])
	}
	if len(conflicts) != 1 || conflicts[0].Command != "foo" {
		t.Errorf("expected 1 conflict for foo, got %v", conflicts)
	}
}

func TestExecutables(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tsc"), []byte("#!/bin/sh\n"), 0755)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0644)
	got, err := Executables(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "tsc" {
		t.Errorf("Executables = %v, want [tsc]", got)
	}
}

func TestExecutablesMissingDir(t *testing.T) {
	got, err := Executables(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}
