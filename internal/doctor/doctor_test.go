package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/shim"
)

type stubPinState struct{ installed map[string]bool }

func (s *stubPinState) IsInstalled(id string) bool { return s.installed[id] }

func TestHomeDirCheckOK(t *testing.T) {
	r := homeDirCheck(t.TempDir())
	if r.Severity != OK {
		t.Errorf("expected OK, got %+v", r)
	}
}

func TestHomeDirCheckMissing(t *testing.T) {
	r := homeDirCheck(filepath.Join(t.TempDir(), "missing"))
	if r.Severity != Fail {
		t.Errorf("expected Fail, got %+v", r)
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

func TestRunAllProducesAllChecks(t *testing.T) {
	results := RunAll(paths.At(t.TempDir()))
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
