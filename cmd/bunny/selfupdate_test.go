package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cristatus/bunny/internal/paths"
)

// Self-update replaces the running binary, not <bin>/bunny: for a
// `go install` or a symlinked bin entry those differ, and writing <bin>/bunny
// left the shims on the old version.
func TestSelfUpdateTargetsTheRunningBinary(t *testing.T) {
	a := &App{Paths: paths.At(t.TempDir())}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.selfUpdateTarget()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("self-update target = %s, want the running binary %s (not %s)", got, want, a.Paths.BunnyBinary())
	}
}
