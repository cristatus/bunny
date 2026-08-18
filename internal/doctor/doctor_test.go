package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/shim"
)

// requireBwrap skips when bwrap is unavailable, as it is in minimal CI images
// and inside containers without user namespaces.
func requireBwrap(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap unavailable:", err)
	}
}

type stubPinState struct{ installed map[string]bool }

func (s *stubPinState) IsInstalled(id string) bool { return s.installed[id] }

func TestLayoutCheckOK(t *testing.T) {
	r := layoutCheck(paths.At(t.TempDir()))
	if r.Severity != OK {
		t.Errorf("expected OK, got %+v", r)
	}
}

// Roots are created lazily, so an absent one is not yet a problem.
func TestLayoutCheckMissingRootIsOK(t *testing.T) {
	r := layoutCheck(paths.At(filepath.Join(t.TempDir(), "missing")))
	if r.Severity != OK {
		t.Errorf("expected OK for a not-yet-created root, got %+v", r)
	}
}

// A root occupied by a regular file is a real problem, and the check should
// say which one rather than reporting a single opaque root.
func TestLayoutCheckRootIsAFile(t *testing.T) {
	p := paths.At(t.TempDir())
	if err := os.WriteFile(p.Cache(), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	r := layoutCheck(p)
	if r.Severity != Fail {
		t.Fatalf("expected Fail, got %+v", r)
	}
	if !strings.Contains(r.Detail, p.Cache()) {
		t.Errorf("detail should name the offending root, got %q", r.Detail)
	}
}

func TestPathCheckContains(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin")
	r := pathOnPathCheck(bin)
	if r.Severity != OK {
		t.Errorf("expected OK, got %+v", r)
	}
}

func TestPathCheckMissing(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	r := pathOnPathCheck("/some/other/dir")
	if r.Severity != Warn {
		t.Errorf("expected Warn, got %+v", r)
	}
}

func TestShimsCheck(t *testing.T) {
	root := t.TempDir()
	p := paths.At(root)
	if err := os.MkdirAll(p.Bin(), 0755); err != nil {
		t.Fatal(err)
	}
	bunny := filepath.Join(p.Bin(), "bunny")
	os.WriteFile(bunny, []byte{}, 0755)

	// Good symlink
	good := filepath.Join(p.Bin(), "node")
	os.Symlink(bunny, good)
	// Broken symlink
	broken := filepath.Join(p.Bin(), "java")
	os.Symlink("/nowhere/bunny", broken)

	r := shimsCheck(p)
	if r.Severity != Fail {
		t.Errorf("expected Fail due to broken shim, got %+v", r)
	}

	// Remove broken; should be OK
	os.Remove(broken)
	r = shimsCheck(p)
	if r.Severity != OK {
		t.Errorf("expected OK, got %+v", r)
	}
}

// withExecutable points the check at a binary of the test's choosing.
func withExecutable(t *testing.T, path string) {
	t.Helper()
	prev := executable
	executable = func() (string, error) { return path, nil }
	t.Cleanup(func() { executable = prev })
}

func TestStrayBinaryCheckInResolvedBinIsOK(t *testing.T) {
	p := paths.At(t.TempDir())
	withExecutable(t, p.BunnyBinary())
	if r := strayBinaryCheck(p); r.Severity != OK {
		t.Errorf("a binary in the resolved bin dir is fine, got %+v", r)
	}
}

// A build in a source tree sits outside every layout, which is not a problem.
func TestStrayBinaryCheckOutsideAnyLayoutIsOK(t *testing.T) {
	build := filepath.Join(t.TempDir(), "bin", "bunny")
	if err := os.MkdirAll(filepath.Dir(build), 0755); err != nil {
		t.Fatal(err)
	}
	withExecutable(t, build)
	if r := strayBinaryCheck(paths.At(t.TempDir())); r.Severity != OK {
		t.Errorf("a build outside any layout should not warn, got %+v", r)
	}
}

// The failure this exists for: a single-root install reached from a shell with
// no $BUNNY_HOME, where every other check passes against an empty XDG layout.
func TestStrayBinaryCheckWarnsForAnotherLayout(t *testing.T) {
	root := t.TempDir()
	other := paths.At(root)
	if err := os.MkdirAll(other.Bin(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other.StateFile(), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	withExecutable(t, other.BunnyBinary())

	// Resolved layout: somewhere else entirely, as an unset $BUNNY_HOME gives.
	r := strayBinaryCheck(paths.At(t.TempDir()))
	if r.Severity != Warn {
		t.Fatalf("expected a warning, got %+v", r)
	}
	if !strings.Contains(r.Detail, root) {
		t.Errorf("detail should name the install it belongs to: %q", r.Detail)
	}
	if !strings.Contains(r.Fix, paths.EnvHome+"="+root) {
		t.Errorf("fix should point at the root: %q", r.Fix)
	}
}

func TestUserNamespaceCheckOK(t *testing.T) {
	requireBwrap(t)
	r := userNamespaceCheck()
	if r.Severity != OK {
		t.Errorf("expected OK when bwrap can create an unprivileged sandbox, got %+v", r)
	}
}

func TestRunAllProducesAllChecks(t *testing.T) {
	results := RunAll(paths.At(t.TempDir()), t.TempDir(), "")
	if len(results) < 5 {
		t.Errorf("expected several checks, got %d", len(results))
	}
}

func TestPinResolutionNoFile(t *testing.T) {
	if got := PinResolution(&stubPinState{}, t.TempDir()); got != nil {
		t.Errorf("expected nil for no .bunny-version, got %+v", got)
	}
}

func TestPinResolutionMixedSatisfaction(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, shim.ProjectVersionFile), []byte("jdk 21\nnode 23\n"), 0644)
	state := &stubPinState{installed: map[string]bool{"jdk-21": true}}

	results := PinResolution(state, dir)
	if len(results) != 3 {
		t.Fatalf("want 3 results (header + 2 pins), got %d: %+v", len(results), results)
	}
	// Header: .bunny-version path
	if results[0].Name != ".bunny-version" || results[0].Severity != OK {
		t.Errorf("header row off: %+v", results[0])
	}
	// jdk pin: installed → OK
	if results[1].Name != "Pin (jdk)" || results[1].Severity != OK {
		t.Errorf("jdk pin should be OK: %+v", results[1])
	}
	// node pin: not installed → Fail with install hint
	if results[2].Severity != Fail {
		t.Errorf("node pin should be Fail: %+v", results[2])
	}
	if !strings.Contains(results[2].Fix, "bunny install node-23") {
		t.Errorf("missing install hint in Fix: %q", results[2].Fix)
	}
}

func TestPathCheckCarriesFix(t *testing.T) {
	// A bin dir guaranteed not on PATH.
	r := pathOnPathCheck("/definitely/not/on/path/bunny-xyz")
	if r.Severity == OK {
		t.Skip("bin dir unexpectedly on PATH")
	}
	if !strings.Contains(r.Fix, "bunny setup") {
		t.Fatalf("PATH check Fix = %q, want it to mention 'bunny setup'", r.Fix)
	}
}

// Reporting the effective roots is what makes "did my config take effect?"
// answerable without installing something and going to look for it.
func TestInstallRootsCheckReportsConfiguredRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	p := paths.At(filepath.Join(home, "b"))
	if got := installRootsCheck(p).Detail; !strings.Contains(got, "sdk=~/b/sdk") {
		t.Errorf("default roots not reported: %q", got)
	}

	custom := p.WithLayout(map[string]string{manifest.KindSDK: filepath.Join(home, "opt")}, nil)
	got := installRootsCheck(custom).Detail
	if !strings.Contains(got, "sdk=~/opt") {
		t.Errorf("configured root not reported: %q", got)
	}
	if !strings.Contains(got, "cli=~/b/cli") {
		t.Errorf("unconfigured kinds should still show their default: %q", got)
	}
}

// A catalog.local pointing somewhere absent falls through to the remote
// silently, so doctor states which one is actually in play.
func TestCatalogCheckReportsSource(t *testing.T) {
	present := t.TempDir()
	if got := catalogCheck(present, "https://example.com/cat").Detail; !strings.HasPrefix(got, "local:") {
		t.Errorf("an existing local catalog wins: %q", got)
	}
	absent := filepath.Join(t.TempDir(), "nope")
	if got := catalogCheck(absent, "https://example.com/cat").Detail; got != "remote: https://example.com/cat" {
		t.Errorf("missing local catalog should report the remote: %q", got)
	}
	if got := catalogCheck(absent, "").Detail; got != "remote: default" {
		t.Errorf("unset remote should be named: %q", got)
	}
}
