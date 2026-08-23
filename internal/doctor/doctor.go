// Package doctor implements the `bunny doctor` health check.
//
// Each check returns a Result; the caller renders them as a table. Checks
// are pure-ish (no globals beyond the actual environment they probe), so
// they're easy to add and easy to read.
package doctor

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cristatus/bunny/internal/manifest"
	"github.com/cristatus/bunny/internal/paths"
	"github.com/cristatus/bunny/internal/runtime"
	"github.com/cristatus/bunny/internal/shim"
)

// executable reports the running binary. A var so tests can place it inside a
// layout they control.
var executable = os.Executable

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

// CatalogSource is one configured catalog for catalogChecks to report on.
type CatalogSource struct {
	Name string
	// Location is the checkout path or the catalog URL, already resolved.
	Location string
	Checkout bool
	Present  bool
}

// RunAll runs the standard set of checks and returns one Result per check.
// cats are the configured catalogs, in the order they resolve.
func RunAll(p *paths.Paths, cats []CatalogSource) []Result {
	return slices.Concat([]Result{
		layoutCheck(p),
		strayBinaryCheck(p),
		configCheck(p),
	}, catalogChecks(cats), []Result{
		installRootsCheck(p),
		pathOnPathCheck(p.Bin()),
		bwrapCheck(),
		userNamespaceCheck(),
		waylandCheck(),
		x11Check(),
		audioCheck(),
		gpuCheck(),
		shimsCheck(p),
	})
}

// layoutCheck reports which layout is active and verifies the roots bunny
// writes to. Under XDG those are several directories, so naming the one that
// is broken matters more than reporting a single root.
func layoutCheck(p *paths.Paths) Result {
	name := "BUNNY_HOME"
	detail := p.Root
	if p.XDG() {
		name = "layout"
		detail = "xdg: " + p.Data()
	}
	for _, dir := range []string{p.Data(), p.Cache(), p.Bin()} {
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue // created lazily on first install
		}
		if err != nil {
			return Result{Name: name, Detail: fmt.Sprintf("unreadable: %s", dir), Severity: Fail}
		}
		if !info.IsDir() {
			return Result{Name: name, Detail: fmt.Sprintf("not a directory: %s", dir), Severity: Fail}
		}
	}
	return Result{Name: name, Detail: detail, Severity: OK}
}

// strayBinaryCheck reports a running binary that belongs to a layout other than
// the one bunny resolved. $BUNNY_HOME selects the layout on every invocation, so
// a single-root install reached from a shell missing that variable resolves to
// the XDG layout while its binary, shims, and packages all sit under the root.
// No other check can see it: PATH holds the root's bin dir, and every check
// passes against an empty XDG layout.
//
// A binary outside the resolved bin dir is ordinary otherwise, a build in a
// source tree being the common case, so this only warns when the binary sits in
// another layout's bin dir. A sibling state file is what identifies one, and it
// also means there is something there to be cut off from.
func strayBinaryCheck(p *paths.Paths) Result {
	const name = "binary"
	exe, err := executable()
	if err != nil {
		return Result{Name: name, Detail: "cannot determine the running binary: " + err.Error(), Severity: Warn}
	}
	dir := filepath.Dir(exe)
	if dir == p.Bin() {
		return Result{Name: name, Detail: tilde(exe), Severity: OK}
	}
	root := filepath.Dir(dir)
	if _, err := os.Stat(paths.At(root).StateFile()); err != nil {
		return Result{Name: name, Detail: tilde(exe), Severity: OK}
	}
	return Result{
		Name:     name,
		Detail:   fmt.Sprintf("%s belongs to the install at %s, which is not the active layout", tilde(exe), tilde(root)),
		Severity: Warn,
		Fix:      "export " + paths.EnvHome + "=" + root,
	}
}

// configCheck names the user config path whether or not it exists. Absence is
// not a problem (bunny's defaults are the no-config behaviour), but the file
// is the only place install locations and data isolation are set, so doctor
// is where someone finds out where to put it.
func configCheck(p *paths.Paths) Result {
	path := p.UserConfigFile()
	if _, err := os.Stat(path); err != nil {
		return Result{Name: "config", Detail: "using defaults, none at " + path, Severity: OK}
	}
	return Result{Name: "config", Detail: path, Severity: OK}
}

// catalogChecks reports one row per catalog, in resolution order. An absent
// checkout is normal, so it warns only when nothing is left to serve a package.
func catalogChecks(cats []CatalogSource) []Result {
	usable := slices.ContainsFunc(cats, func(c CatalogSource) bool { return c.Present })
	results := make([]Result, 0, len(cats))
	for _, c := range cats {
		r := Result{Name: "catalog:" + c.Name, Detail: "remote: " + c.Location}
		switch {
		case !c.Checkout:
		case c.Present:
			r.Detail = "local: " + tilde(c.Location)
		default:
			r.Detail = "local: " + tilde(c.Location) + " (absent)"
			if !usable {
				r.Severity = Warn
			}
		}
		results = append(results, r)
	}
	return results
}

// installRootsCheck reports where each kind of package actually installs.
// Config is easy to get subtly wrong (a setting left commented, a typo in a
// kind), and until this existed the only way to find out was to install
// something and go looking for it.
func installRootsCheck(p *paths.Paths) Result {
	var parts []string
	for _, kind := range manifest.Kinds {
		parts = append(parts, kind+"="+tilde(p.InstallRoot(kind)))
	}
	return Result{Name: "install", Detail: strings.Join(parts, "  "), Severity: OK}
}

// tilde abbreviates $HOME so the roots stay readable on one line.
func tilde(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if after, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~" + string(os.PathSeparator) + after
	}
	return path
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
	path, err := runtime.FindBwrap()
	if err != nil {
		return Result{Name: "bwrap", Detail: err.Error(), Severity: Fail}
	}
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return Result{Name: "bwrap", Detail: path + " found but --version failed: " + err.Error(), Severity: Warn}
	}
	return Result{Name: "bwrap", Detail: strings.TrimSpace(string(out)), Severity: OK}
}

// userNamespaceCheck confirms bwrap can actually create an unprivileged
// sandbox: a present binary is not enough on kernels with unprivileged user
// namespaces disabled (some hardened/CI kernels), which is exactly the
// failure mode an enabled per-package sandbox needs surfaced, since Bunny
// must not silently fall back to unsandboxed execution.
func userNamespaceCheck() Result {
	const name = "sandbox"
	path, err := runtime.FindBwrap()
	if err != nil {
		return Result{Name: name, Detail: "bwrap not found", Severity: Fail}
	}
	cmd := exec.Command(path, "--unshare-user", "--unshare-pid", "--ro-bind", "/", "/", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return Result{
			Name:     name,
			Detail:   "unprivileged user namespaces unavailable: " + strings.TrimSpace(string(out)),
			Severity: Fail,
			Fix:      "check /proc/sys/kernel/unprivileged_userns_clone or your distro's AppArmor/sysctl policy",
		}
	}
	return Result{Name: name, Detail: "unprivileged user namespaces OK", Severity: OK}
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
	caps := slices.Sorted(maps.Keys(pins))
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
