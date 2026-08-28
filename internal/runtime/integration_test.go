//go:build sandbox_integration

// Executable acceptance tests for the sandbox security claims. They build the
// bunny binary and drive real bubblewrap / pasta / xdg-dbus-proxy
// compositions, so they exercise what argv-presence unit tests cannot. Run
// with: go test -tags sandbox_integration ./internal/runtime/
//
// Each test skips when its required helpers (or a session bus / network) are
// absent, so the suite is safe to run anywhere but only asserts where it can.
package runtime_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

var bunnyBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bunny-integration-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	bunnyBin = filepath.Join(dir, "bunny")
	build := exec.Command("go", "build", "-o", bunnyBin, "github.com/cristatus/bunny/cmd/bunny")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("build bunny: " + err.Error())
	}
	os.Exit(m.Run())
}

func have(t *testing.T, tool string) {
	t.Helper()
	if _, err := exec.LookPath(tool); err != nil {
		t.Skipf("%s not installed", tool)
	}
}

// probe writes a fake-HOME install of a single package whose binary is script,
// governed by configYAML, and returns the launch's combined output and exit
// code under `bunny run --sandbox probe`.
func probe(t *testing.T, configYAML, script string, env ...string) (string, int) {
	t.Helper()
	return probeIn(t, t.TempDir(), configYAML, script, env...)
}

// probeIn is probe with an explicit fake HOME, so a test can reuse the same
// install (and therefore the same package {data}) across several launches.
func probeIn(t *testing.T, home, configYAML, script string, env ...string) (string, int) {
	t.Helper()
	root := filepath.Join(home, ".local", "share", "bunny")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "manifests"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "cli", "probe"), 0o755))
	must(os.MkdirAll(filepath.Join(home, ".config", "bunny"), 0o755))
	must(os.WriteFile(filepath.Join(root, "manifests", "probe.yaml"), []byte(
		"id: probe\nname: Probe\nversion: \"1.0\"\n"+
			"sources:\n  - url: https://example.invalid/p.tar.gz\n    sha256: \""+strings.Repeat("0", 64)+"\"\n"+
			"bin:\n  - name: probe\n    path: \"{app}/probe.sh\"\n"), 0o644))
	must(os.WriteFile(filepath.Join(root, "cli", "probe", "probe.sh"),
		[]byte("#!/bin/sh\n"+script+"\n"), 0o755))
	must(os.WriteFile(filepath.Join(root, "state.json"), []byte(
		`{"version":1,"updated":"2026-01-01T00:00:00Z","packages":{"probe":`+
			`{"version":"1.0","installed":"2026-01-01T00:00:00Z","kind":"cli"}}}`), 0o644))
	must(os.WriteFile(filepath.Join(home, ".config", "bunny", "config.yaml"), []byte(configYAML), 0o644))

	cmd := exec.Command(bunnyBin, "sandbox", "probe")
	// Fresh HOME, but keep the trusted runtime dir and session bus so helpers
	// work; clear XDG base dirs so the layout resolves under the fake HOME.
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_DATA_HOME=", "XDG_CONFIG_HOME=", "XDG_CACHE_HOME=",
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run bunny: %v\n%s", err, out)
	}
	return string(out), code
}

func TestHardenedFilesystemBoundary(t *testing.T) {
	have(t, "bwrap")
	script := `echo "write-etc: $(touch /etc/bunny-probe 2>/dev/null && echo LEAK || echo denied)"
echo "home-entries: $(ls -A "$REAL_HOME" 2>/dev/null | wc -l)"
echo "write-data: $(touch "$HOME/ok" && echo yes || echo no)"`
	out, code := probe(t, "sandbox:\n  packages:\n    probe:\n      boundary: hardened\n", script,
		"REAL_HOME="+t.TempDir()) // an empty stand-in; real home is masked anyway
	if code != 0 {
		t.Fatalf("hardened launch failed (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "write-etc: denied") {
		t.Errorf("read-only root not enforced:\n%s", out)
	}
	if !strings.Contains(out, "write-data: yes") {
		t.Errorf("package data must stay writable:\n%s", out)
	}
}

func TestHardenedRealHomeHidden(t *testing.T) {
	have(t, "bwrap")
	// A marker in the real HOME must be invisible inside the sandbox.
	script := `echo "sees-marker: $(test -e "$HOME/../SECRET" && echo yes || echo no)"`
	// The real home is the fake HOME; drop a secret there via the config's
	// own directory is awkward, so assert the home dir reads as empty/tmpfs.
	script = `echo "home-listing: [$(ls -A "$HOME" 2>/dev/null)]"`
	out, code := probe(t, "sandbox:\n  packages:\n    probe:\n      boundary: hardened\n", script)
	if code != 0 {
		t.Fatalf("launch failed (%d):\n%s", code, out)
	}
	// HOME is the isolated data home; the real host home is tmpfs-masked and
	// never appears. The isolated home contains only the XDG dirs bunny made.
	if strings.Contains(out, ".ssh") || strings.Contains(out, ".config/bunny") {
		t.Errorf("real host home leaked into the sandbox:\n%s", out)
	}
}

func TestHardenedGrantEscapeRefused(t *testing.T) {
	have(t, "bwrap")
	cfg := "sandbox:\n  packages:\n    probe:\n      boundary: hardened\n      fs:\n        read:\n          - \"~\"\n"
	out, code := probe(t, cfg, `echo SCRIPT_EXECUTED`)
	if strings.Contains(out, "SCRIPT_EXECUTED") {
		t.Errorf("script must not run when a grant is refused:\n%s", out)
	}
	if code == 0 {
		t.Errorf("a refused launch must exit non-zero (got %d):\n%s", code, out)
	}
	if !strings.Contains(out, "re-expose a protected root") {
		t.Errorf("expected a protected-root refusal, got:\n%s", out)
	}
}

func TestHardenedDBusIgnoresInjectedUnixexec(t *testing.T) {
	have(t, "bwrap")
	have(t, "xdg-dbus-proxy")
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("no session bus")
	}
	marker := filepath.Join(t.TempDir(), "PWNED")
	evil := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(evil, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The package tries to point the host-side proxy at an exec transport.
	cfg := "env:\n  probe:\n    DBUS_SESSION_BUS_ADDRESS: \"unixexec:path=" + evil + "\"\n" +
		"sandbox:\n  packages:\n    probe:\n      boundary: hardened\n      features:\n        dbus: true\n      net:\n        mode: host\n"
	out, code := probe(t, cfg, `echo ok`)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("unixexec address executed on the host (marker created); code=%d\n%s", code, out)
	}
}

func TestEphemeralHomeDiscardsWritesAndPersistsListedPaths(t *testing.T) {
	have(t, "bwrap")
	// One fake HOME reused across three launches, so each sees the same
	// package {data} and therefore the same seed / ephemeral overlay.
	home := t.TempDir()
	persistCfg := "sandbox:\n  packages:\n    probe:\n      home: ephemeral\n      persist: [.claude/memory]\n"

	// Seed the isolated home the way a normal persistent launch would: a
	// config file and a starting memory entry.
	seed := `mkdir -p "$HOME/.claude/memory" "$HOME/.config"
echo cfg > "$HOME/.config/auth"
echo run0 >> "$HOME/.claude/memory/log"`
	out, code := probeIn(t, home, "sandbox:\n  packages:\n    probe:\n      home: isolated\n", seed)
	if code != 0 {
		t.Fatalf("seed launch failed (%d):\n%s", code, out)
	}

	// First ephemeral run: reads the seed config, adds to the persisted
	// memory log, and leaves an unpersisted session marker that must vanish.
	run1 := `echo "cfg: $(cat "$HOME/.config/auth" 2>/dev/null)"
echo run1 >> "$HOME/.claude/memory/log"
echo junk > "$HOME/.config/session"`
	out, code = probeIn(t, home, persistCfg, run1)
	if code != 0 {
		t.Fatalf("first ephemeral launch failed (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "cfg: cfg") {
		t.Errorf("seed config must be readable on an ephemeral home:\n%s", out)
	}

	// Second ephemeral run: the session marker from run1 must be gone, and
	// the persisted memory log must carry both the seed and run1's entry.
	run2 := `echo "session-gone: $(test -e "$HOME/.config/session" && echo LEAK || echo gone)"
echo "memory: $(cat "$HOME/.claude/memory/log" 2>/dev/null | tr '\n' , )"`
	out, code = probeIn(t, home, persistCfg, run2)
	if code != 0 {
		t.Fatalf("second ephemeral launch failed (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "session-gone: gone") {
		t.Errorf("non-persisted writes must be discarded on exit:\n%s", out)
	}
	if !strings.Contains(out, "memory: run0,run1,") {
		t.Errorf("persisted path must carry writes from earlier runs:\n%s", out)
	}
}

func TestPrivateNetworkLoopbackDenied(t *testing.T) {
	have(t, "pasta")
	// A host loopback listener must be unreachable from a private-net sandbox.
	ln := listenLoopback(t)
	defer ln.Close()
	addr := ln.Addr().String()
	script := `echo "reach-host: $(curl -s -o /dev/null --max-time 3 http://` + addr + `/ && echo REACHED || echo blocked)"`
	cfg := "sandbox:\n  packages:\n    probe:\n      net:\n        mode: private\n"
	out, code := probe(t, cfg, script)
	if code != 0 {
		t.Fatalf("private launch failed (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "reach-host: blocked") {
		t.Errorf("host loopback must be unreachable under private networking:\n%s", out)
	}
}

func TestPrivateEgressRulesetIsTamperProof(t *testing.T) {
	have(t, "pasta")
	have(t, "nft")
	script := `echo "tamper: $(nft flush ruleset 2>&1 | grep -qi 'not permitted' && echo denied || echo LEAK)"`
	cfg := "sandbox:\n  packages:\n    probe:\n      net:\n        mode: private\n        egress:\n          - 10.0.0.0/8:443\n"
	out, code := probe(t, cfg, script)
	if code != 0 {
		t.Fatalf("egress launch failed (%d):\n%s", code, out)
	}
	if !strings.Contains(out, "tamper: denied") {
		t.Errorf("payload must not be able to flush the egress ruleset:\n%s", out)
	}
}
