package main

import (
	"bytes"
	"github.com/cristatus/bunny/internal/shim"
	"github.com/cristatus/bunny/internal/state"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/ui"
)

func TestPinConfirmationLine(t *testing.T) {
	var b bytes.Buffer
	p := ui.NewWithColor(&b, false)
	p.Print(pinConfirmation("jdk", "21"))
	if got := b.String(); !strings.Contains(got, "pinned jdk to 21") {
		t.Fatalf("pin line = %q", got)
	}
}

func TestPinExactInstalledVendor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st := state.Empty()
	st.SetInstalled("corretto-21", "21.0.4+7", "jdk", "sdk", "")
	a := &App{State: st}
	if err := (&PinCmd{Capability: "jdk", Version: "corretto-21", Exact: true}).Run(a); err != nil {
		t.Fatal(err)
	}
	pin, err := shim.ResolveProjectVersion(dir, "jdk")
	if err != nil || pin.Value != "corretto-21@21.0.4+7" {
		t.Fatalf("pin: %+v %v", pin, err)
	}
	st.SetInstalled("corretto-21", "21.0.5+11", "jdk", "sdk", "")
	if _, err := a.resolveCapabilityProvider("jdk", dir); err == nil {
		t.Fatal("global commands must reject version drift")
	}
}

func TestPinFailurePreservesFile(t *testing.T) {
	for _, tc := range []PinCmd{
		{Capability: "jdk", Version: "17", Exact: true},
		{Capability: "jdk", Version: "node-22"},
		{Capability: "jdk", Version: "21\nnode 24"},
	} {
		t.Run(tc.Version, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			path := filepath.Join(dir, shim.ProjectVersionFile)
			original := "jdk 21\n"
			if err := os.WriteFile(path, []byte(original), 0644); err != nil {
				t.Fatal(err)
			}
			st := state.Empty()
			st.SetInstalled("node-22", "22.0.0", "node", "sdk", "")
			if err := tc.Run(&App{State: st}); err == nil {
				t.Fatal("expected rejected pin")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != original {
				t.Fatalf("failed pin changed file: %q %v", data, err)
			}
		})
	}
}
