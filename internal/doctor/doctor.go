// Package doctor implements the `bunny doctor` health check.
//
// Each check returns a Result; the caller renders them as a table. Checks
// are pure-ish (no globals beyond the actual environment they probe), so
// they're easy to add and easy to read.
package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/shim"
)

// PinState is the slice of state.State PinResolution needs.
type PinState interface {
	IsInstalled(id string) bool
}

// Severity classifies a check outcome.
type Severity int

const (
	OK Severity = iota
	Warn
	Fail
)

// Result is one row in the doctor table.
type Result struct {
	Name     string
	Detail   string
	Severity Severity
}

// RunAll runs the standard set of checks and returns one Result per check.
func RunAll(p *paths.Paths) []Result {
	return []Result{
		homeDirCheck(p.Home),
		pathOnPathCheck(p.Bin()),
		bwrapCheck(),
		waylandCheck(),
		x11Check(),
		audioCheck(),
		gpuCheck(),
		shimsCheck(p),
	}
}

func homeDirCheck(home string) Result {
	info, err := os.Stat(home)
	if err != nil {
		return Result{"BUNNY_HOME", fmt.Sprintf("missing: %s", home), Fail}
	}
	if !info.IsDir() {
		return Result{"BUNNY_HOME", fmt.Sprintf("not a directory: %s", home), Fail}
	}
	return Result{"BUNNY_HOME", home, OK}
}

func pathOnPathCheck(binDir string) Result {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == binDir {
			return Result{"PATH", fmt.Sprintf("contains %s", binDir), OK}
		}
	}
	return Result{"PATH", fmt.Sprintf("does not contain %s — run `eval \"$(bunny init)\"`", binDir), Warn}
}

func bwrapCheck() Result {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return Result{"bwrap", "not found — install: sudo pacman -S bubblewrap (Arch) or sudo apt install bubblewrap (Debian/Ubuntu)", Fail}
	}
	out, err := exec.Command("bwrap", "--version").Output()
	if err != nil {
		return Result{"bwrap", path + " found but --version failed: " + err.Error(), Warn}
	}
	return Result{"bwrap", strings.TrimSpace(string(out)), OK}
}

func waylandCheck() Result {
	disp := os.Getenv("WAYLAND_DISPLAY")
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if disp == "" || rt == "" {
		return Result{"Wayland", "WAYLAND_DISPLAY or XDG_RUNTIME_DIR unset", Warn}
	}
	sock := filepath.Join(rt, disp)
	if _, err := os.Stat(sock); err != nil {
		return Result{"Wayland", fmt.Sprintf("socket %s not found", sock), Warn}
	}
	return Result{"Wayland", sock, OK}
}

func x11Check() Result {
	if _, err := os.Stat("/tmp/.X11-unix"); err != nil {
		return Result{"X11", "/tmp/.X11-unix not found", Warn}
	}
	return Result{"X11", "/tmp/.X11-unix", OK}
}

func audioCheck() Result {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		return Result{"Audio", "XDG_RUNTIME_DIR unset", Warn}
	}
	for _, name := range []string{"pipewire-0", "pulse"} {
		if _, err := os.Stat(filepath.Join(rt, name)); err == nil {
			return Result{"Audio", filepath.Join(rt, name), OK}
		}
	}
	return Result{"Audio", "no PipeWire or PulseAudio socket in " + rt, Warn}
}

func gpuCheck() Result {
	if _, err := os.Stat("/dev/dri"); err != nil {
		return Result{"GPU", "/dev/dri not present", Warn}
	}
	entries, err := os.ReadDir("/dev/dri")
	if err != nil || len(entries) == 0 {
		return Result{"GPU", "/dev/dri empty", Warn}
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return Result{"GPU", "/dev/dri: " + strings.Join(names, ", "), OK}
}

// PinResolution probes for `.bunny-version` walking up from cwd and reports
// how each pinned capability resolves to an installed package. Returns nil
// when no pin file is found — `bunny doctor` then stays silent on pinning.
func PinResolution(state PinState, cwd string) []Result {
	pins, source, err := shim.ResolveAllPins(cwd)
	if err != nil || pins == nil {
		return nil
	}
	out := []Result{
		{".bunny-version", source, OK},
	}
	caps := make([]string, 0, len(pins))
	for c := range pins {
		caps = append(caps, c)
	}
	sort.Strings(caps)
	for _, cap := range caps {
		ver := pins[cap]
		candidate := cap + "-" + ver
		name := "Pin (" + cap + ")"
		if state.IsInstalled(candidate) {
			out = append(out, Result{name, fmt.Sprintf("%s → %s", ver, candidate), OK})
		} else {
			out = append(out, Result{
				name,
				fmt.Sprintf("%s → %s NOT INSTALLED — run: bunny install %s", ver, candidate, candidate),
				Fail,
			})
		}
	}
	return out
}

func shimsCheck(p *paths.Paths) Result {
	entries, err := os.ReadDir(p.Bin())
	if err != nil {
		return Result{"Shims", "no bin dir yet — install something first", Warn}
	}
	var broken []string
	count := 0
	for _, e := range entries {
		if e.Name() == "bunny" {
			continue
		}
		if _, err := os.Stat(filepath.Join(p.Bin(), e.Name())); err != nil {
			broken = append(broken, e.Name())
			continue
		}
		count++
	}
	if len(broken) > 0 {
		return Result{"Shims", fmt.Sprintf("%d broken: %s", len(broken), strings.Join(broken, ", ")), Fail}
	}
	if count == 0 {
		return Result{"Shims", "no shims installed yet", OK}
	}
	return Result{"Shims", fmt.Sprintf("%d shim(s) resolve", count), OK}
}
