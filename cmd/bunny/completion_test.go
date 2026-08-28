package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/catalog"
	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/state"
)

func TestCompletionIDs(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}

	// A local catalog dir with one manifest → a local-only catalog source.
	checkout := filepath.Join(root, "catalog")
	mdir := filepath.Join(checkout, catalog.PackagesDir, "jdk-21")
	if err := os.MkdirAll(mdir, 0755); err != nil {
		t.Fatal(err)
	}
	man := `id: jdk-21
name: JDK 21
version: "21"
tags: [java, jdk]
sources:
  - {url: "https://x/y.tar.gz", sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
bin:
  - {name: java, path: "{app}/bin/java"}
`
	if err := os.WriteFile(filepath.Join(mdir, "manifest.yaml"), []byte(man), 0644); err != nil {
		t.Fatal(err)
	}
	local := catalog.NewLocal(checkout)
	a.catalogs = []catalogEntry{{src: catalog.Source{Name: "local", Loader: local}, local: local}}

	// Two installed packages: node-22 provides a capability, bat provides nothing.
	a.State.SetInstalled("node-22", "22.0.0", "node", "", "")
	a.State.SetInstalled("bat", "1.0.0", "", "", "")

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
	checkout := filepath.Join(root, "catalog")
	mdir := filepath.Join(checkout, catalog.PackagesDir, "jdk-21")
	if err := os.MkdirAll(mdir, 0755); err != nil {
		t.Fatal(err)
	}
	man := `id: jdk-21
name: JDK 21
version: "21"
tags: [java, jdk]
provides: jdk
sources:
  - {url: "https://x/y.tar.gz", sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
bin:
  - {name: java, path: "{app}/bin/java"}
`
	if err := os.WriteFile(filepath.Join(mdir, "manifest.yaml"), []byte(man), 0644); err != nil {
		t.Fatal(err)
	}
	local := catalog.NewLocal(checkout)
	a.catalogs = []catalogEntry{{src: catalog.Source{Name: "local", Loader: local}, local: local}}
	a.State.SetInstalled("node-22", "22", "node", "", "")
	got := a.completionCapabilities()
	if strings.Join(got, ",") != "jdk,node" {
		t.Fatalf("capabilities = %v, want [jdk node]", got)
	}
}

func TestCompletionProfiles(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}

	// No config file at all: only the built-ins.
	builtinOnly := a.completionProfiles()
	if strings.Join(builtinOnly, ",") != "agent,clean,desktop,ephemeral,offline" {
		t.Fatalf("builtin-only profiles = %v", builtinOnly)
	}

	// A configured custom profile is appended; built-in names stay reserved
	// so there is no collision to dedupe.
	a.Config = &config.Config{Sandbox: config.Sandbox{
		Profiles: map[string]config.SandboxPolicy{"claude-scratch": {Home: "ephemeral"}},
	}}
	got := a.completionProfiles()
	found := false
	for _, name := range got {
		if name == "claude-scratch" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected custom profile %q in %v", "claude-scratch", got)
	}
}

func TestCompletionBinaries(t *testing.T) {
	root := t.TempDir()
	a := &App{Paths: paths.At(root), State: state.Empty()}
	// completionBinaries reads the install-time manifest snapshot (what
	// `bunny run` itself resolves against), not the live catalog.
	if err := os.MkdirAll(a.Paths.Manifests(), 0755); err != nil {
		t.Fatal(err)
	}
	man := `id: jdk-21
name: JDK 21
version: "21"
tags: [java, jdk]
provides: jdk
sources:
  - {url: "https://x/y.tar.gz", sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
bin:
  - {name: java, path: "{app}/bin/java"}
  - {name: javac, path: "{app}/bin/javac"}
`
	if err := os.WriteFile(a.Paths.ManifestFile("jdk-21"), []byte(man), 0644); err != nil {
		t.Fatal(err)
	}
	a.State.SetInstalled("jdk-21", "21", "jdk", "", "")

	got := a.completionBinaries("jdk-21")
	if strings.Join(got, ",") != "java,javac" {
		t.Errorf("binaries = %v, want [java javac]", got)
	}
	// An id that isn't installed (or isn't yet fully typed) yields no
	// completions rather than an error — same contract as the other helpers.
	if got := a.completionBinaries("does-not-exist"); got != nil {
		t.Errorf("expected nil for an unresolvable id, got %v", got)
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
		for _, f := range []string{"log-level", "no-progress", "version", "help"} {
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
		// Per-subcommand flags + the value-completion helpers.
		for _, f := range []string{
			"force", "purge", "tag", "capability", "active", "command", "sandbox-profile",
			"complete-tags", "complete-capabilities", "complete-profiles", "complete-binaries",
		} {
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
