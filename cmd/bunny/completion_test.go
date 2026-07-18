package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

func TestCompletionIDs(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}

	// A local catalog dir with one manifest → a local-only catalog source.
	mdir := filepath.Join(a.Paths.Catalog(), "sdk", "jdk-21")
	if err := os.MkdirAll(mdir, 0755); err != nil {
		t.Fatal(err)
	}
	man := `id: jdk-21
name: JDK 21
version: "21"
category: sdk
sources:
  - {url: "https://x/y.tar.gz", sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
bin:
  - {name: java, path: "{app}/bin/java"}
`
	if err := os.WriteFile(filepath.Join(mdir, "manifest.yaml"), []byte(man), 0644); err != nil {
		t.Fatal(err)
	}
	a.local = catalog.NewLocal(a.Paths.Catalog())

	// Two installed packages: node-22 provides a capability, bat provides nothing.
	a.State.SetInstalled("node-22", "22.0.0", "node")
	a.State.SetInstalled("bat", "1.0.0", "")

	catalogIDs := a.completionIDs(false)
	if len(catalogIDs) != 1 || catalogIDs[0] != "jdk-21" {
		t.Errorf("catalog IDs = %v, want [jdk-21]", catalogIDs)
	}
	installedIDs := a.completionIDs(true)
	if len(installedIDs) != 2 || installedIDs[0] != "bat" || installedIDs[1] != "node-22" {
		t.Errorf("installed IDs = %v, want [bat node-22]", installedIDs)
	}
	// `bunny use` only makes sense for providers, so its completion excludes bat.
	providerIDs := a.completionProviderIDs()
	if len(providerIDs) != 1 || providerIDs[0] != "node-22" {
		t.Errorf("provider IDs = %v, want [node-22]", providerIDs)
	}
}

func TestCompletionIDsOfflineFallback(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}
	// No local catalog, no remote → empty, no panic, no error.
	if got := a.completionIDs(false); len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestCompletionCapabilities(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}
	mdir := filepath.Join(a.Paths.Catalog(), "sdk", "jdk-21")
	if err := os.MkdirAll(mdir, 0755); err != nil {
		t.Fatal(err)
	}
	man := `id: jdk-21
name: JDK 21
version: "21"
category: sdk
provides: jdk
sources:
  - {url: "https://x/y.tar.gz", sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
bin:
  - {name: java, path: "{app}/bin/java"}
`
	if err := os.WriteFile(filepath.Join(mdir, "manifest.yaml"), []byte(man), 0644); err != nil {
		t.Fatal(err)
	}
	a.local = catalog.NewLocal(a.Paths.Catalog())
	a.State.SetInstalled("node-22", "22", "node")
	got := a.completionCapabilities()
	if strings.Join(got, ",") != "jdk,node" {
		t.Fatalf("capabilities = %v, want [jdk node]", got)
	}
}

func TestCompletionScript(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		s := completionScript(shell)
		if s == "" {
			t.Fatalf("%s: empty script", shell)
		}
		for _, sc := range completionSubcommands {
			if !strings.Contains(s, sc) {
				t.Errorf("%s script missing subcommand %q", shell, sc)
			}
		}
		if !strings.Contains(s, "complete-ids") || !strings.Contains(s, "complete-ids --installed") {
			t.Errorf("%s script missing complete-ids calls", shell)
		}
		if strings.Contains(s, "__SUBCMDS__") || strings.Contains(s, "__GLOBALS__") || strings.Contains(s, "__LOGLEVELS__") {
			t.Errorf("%s script left an uninterpolated placeholder", shell)
		}
		// Global flags completable anywhere (bash/zsh as --flag, fish as -l flag).
		for _, f := range []string{"log-level", "pager", "no-pager", "version", "help"} {
			if !strings.Contains(s, f) {
				t.Errorf("%s script missing global flag %q", shell, f)
			}
		}
		// --log-level's enum values must be completable.
		for _, v := range completionLogLevels {
			if !strings.Contains(s, v) {
				t.Errorf("%s script missing log-level value %q", shell, v)
			}
		}
		// Per-subcommand flags + the --category value helper.
		for _, f := range []string{"force", "purge", "category", "capability", "active", "command", "complete-categories", "complete-capabilities"} {
			if !strings.Contains(s, f) {
				t.Errorf("%s script missing %q", shell, f)
			}
		}
	}
	// The hidden command must never be offered as a completable subcommand.
	for _, sc := range completionSubcommands {
		if sc == "complete-ids" {
			t.Error("complete-ids must not be in completionSubcommands (it is hidden)")
		}
	}
}
