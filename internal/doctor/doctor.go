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
	Fix      string // suggested command to remedy a Warn/Fail, if any
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
		return Result{Name: "BUNNY_HOME", Detail: fmt.Sprintf("missing: %s", home), Severity: Fail}
	}
	if !info.IsDir() {
		return Result{Name: "BUNNY_HOME", Detail: fmt.Sprintf("not a directory: %s", home), Severity: Fail}
	}
	return Result{Name: "BUNNY_HOME", Detail: home, Severity: OK}
}

func pathOnPathCheck(binDir string) Result {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p == binDir {
			return Result{Name: "PATH", Detail: fmt.Sprintf("contains %s", binDir), Severity: OK}
		}
	}
	return Result{
		Name:     "PATH",
		Detail:   fmt.Sprintf("does not contain %s", binDir),
		Severity: Warn,
		Fix:      "bunny setup",
	}
}

func bwrapCheck() Result {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return Result{Name: "bwrap", Detail: "not found — install: sudo pacman -S bubblewrap (Arch) or sudo apt install bubblewrap (Debian/Ubuntu)", Severity: Fail}
	}
	out, err := exec.Command("bwrap", "--version").Output()
	if err != nil {
		return Result{Name: "bwrap", Detail: path + " found but --version failed: " + err.Error(), Severity: Warn}
	}
	return Result{Name: "bwrap", Detail: strings.TrimSpace(string(out)), Severity: OK}
}

func waylandCheck() Result {
	disp := os.Getenv("WAYLAND_DISPLAY")
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if disp == "" || rt == "" {
		return Result{Name: "Wayland", Detail: "WAYLAND_DISPLAY or XDG_RUNTIME_DIR unset", Severity: Warn}
	}
	sock := filepath.Join(rt, disp)
	if _, err := os.Stat(sock); err != nil {
		return Result{Name: "Wayland", Detail: fmt.Sprintf("socket %s not found", sock), Severity: Warn}
	}
	return Result{Name: "Wayland", Detail: sock, Severity: OK}
}

func x11Check() Result {
	if _, err := os.Stat("/tmp/.X11-unix"); err != nil {
		return Result{Name: "X11", Detail: "/tmp/.X11-unix not found", Severity: Warn}
	}
	return Result{Name: "X11", Detail: "/tmp/.X11-unix", Severity: OK}
}

func audioCheck() Result {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		return Result{Name: "Audio", Detail: "XDG_RUNTIME_DIR unset", Severity: Warn}
	}
	for _, name := range []string{"pipewire-0", "pulse"} {
		if _, err := os.Stat(filepath.Join(rt, name)); err == nil {
			return Result{Name: "Audio", Detail: filepath.Join(rt, name), Severity: OK}
		}
	}
	return Result{Name: "Audio", Detail: "no PipeWire or PulseAudio socket in " + rt, Severity: Warn}
}

func gpuCheck() Result {
	if _, err := os.Stat("/dev/dri"); err != nil {
		return Result{Name: "GPU", Detail: "/dev/dri not present", Severity: Warn}
	}
	entries, err := os.ReadDir("/dev/dri")
	if err != nil || len(entries) == 0 {
		return Result{Name: "GPU", Detail: "/dev/dri empty", Severity: Warn}
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return Result{Name: "GPU", Detail: "/dev/dri: " + strings.Join(names, ", "), Severity: OK}
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
		{Name: ".bunny-version", Detail: source, Severity: OK},
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
			out = append(out, Result{Name: name, Detail: fmt.Sprintf("%s → %s", ver, candidate), Severity: OK})
		} else {
			out = append(out, Result{
				Name:     name,
				Detail:   fmt.Sprintf("%s → %s not installed", ver, candidate),
				Severity: Fail,
				Fix:      "bunny install " + candidate,
			})
		}
	}
	return out
}

func shimsCheck(p *paths.Paths) Result {
	entries, err := os.ReadDir(p.Bin())
	if err != nil {
		return Result{Name: "Shims", Detail: "no bin dir yet — install something first", Severity: Warn}
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
		return Result{
			Name:     "Shims",
			Detail:   fmt.Sprintf("%d broken: %s", len(broken), strings.Join(broken, ", ")),
			Severity: Fail,
			Fix:      "bunny reshim",
		}
	}
	if count == 0 {
		return Result{Name: "Shims", Detail: "no shims installed yet", Severity: OK}
	}
	return Result{Name: "Shims", Detail: fmt.Sprintf("%d shim(s) resolve", count), Severity: OK}
}
