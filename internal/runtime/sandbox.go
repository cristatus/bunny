package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/log"
	"golang.org/x/sys/unix"

	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"
)

// legacySandboxContextEnv carried the inherited sandbox context before the
// context moved to a read-only mounted file. The variable is never read:
// environment inheritance is suitable for hints, never for authorization. It
// is still stripped from child environments so a stale value cannot confuse
// an older Bunny binary.
const legacySandboxContextEnv = "BUNNY_SANDBOX_CONTEXT"

// sandboxContextFile is where each sandbox layer mounts the resolved
// effective policy, read-only, for nested Bunny invocations to clamp against.
// The location is fixed under the user's runtime directory and deliberately
// not influenced by the environment. Inside a private-network sandbox the
// process sees itself as uid 0, so a nested Bunny there resolves a path that
// holds no context and takes the conservative full-layer path — safe, merely
// without layer elision. A variable only so tests can point it at a scratch
// file.
var sandboxContextFile = fmt.Sprintf("/run/user/%d/bunny/sandbox-context.json", os.Getuid())

// sandboxContext describes the kernel restrictions already in effect around a
// process. It is installed as a read-only file over a private tmpfs by the
// bubblewrap layer that created those restrictions, so a sandboxed process
// cannot forge or unset it the way it could an environment variable. A child
// Bunny invocation clamps its requested policy against it; an absent file
// permits only the conservative full-layer path.
type sandboxContext struct {
	Packages         []string  `json:"packages"`
	HostHome         string    `json:"hostHome"`
	Boundary         string    `json:"boundary,omitempty"` // "" means scoped
	Hidden           []string  `json:"hidden,omitempty"`
	DisabledFeatures []string  `json:"disabledFeatures,omitempty"`
	NetMode          string    `json:"netMode,omitempty"` // "" means host
	Inbound          *[]string `json:"inbound,omitempty"` // concrete when NetMode is private
	Egress           *[]string `json:"egress,omitempty"`  // nil means unrestricted
	FSRead           []string  `json:"fsRead,omitempty"`  // effective hardened grants
	FSWrite          []string  `json:"fsWrite,omitempty"`
}

type sandboxPlan struct {
	args       []string // bwrap mounts, namespaces, and env operations
	tail       []string // --chdir, --, binary, argv
	env        []string
	needsLayer bool
	context    sandboxContext // effective state a new layer must mount

	// Composition beyond plain bwrap. pasta is set when this launch creates a
	// private network namespace; proxy when a hardened policy selects the
	// filtered portal bus. The two never co-occur: the proxy requires host
	// networking because portals are themselves an escape from network
	// isolation.
	pasta *pastaSpec
	proxy *dbusProxySpec

	// isolatedHome is created by the exec path just before launch; planning
	// stays free of host mutation so --explain can share it.
	isolatedHome string
	// forcedDBus records D-Bus cut off by a non-host network mode rather than
	// by policy, for --explain.
	forcedDBus bool
}

// PackageSandbox is the effective run-time policy after merging the selected
// profile and the package's inline override. Activation is evaluated
// separately so the same policy can be applied automatically (a
// sandbox.packages entry) or for one launch (--sandbox/--sandbox-profile).
type PackageSandbox struct {
	Boundary string
	Home     string
	Hide     []string
	Persist  []string
	Features map[string]bool
	FS       FSPolicy
	Net      NetPolicy
}

// FSPolicy is the resolved hardened filesystem grant set. The Set flags keep
// the difference between an absent field (inherit) and an explicit empty list
// (grant nothing); pointers do not escape the decoding layer.
type FSPolicy struct {
	Read     []string
	Write    []string
	ReadSet  bool
	WriteSet bool
	Cwd      string // read | write | hidden
}

// NetPolicy is the resolved network policy. Mode is concrete after
// finalization; the Set flags mirror FSPolicy's.
type NetPolicy struct {
	Mode       string // host | private | none
	Inbound    []string
	InboundSet bool
	Egress     []string
	EgressSet  bool
}

// netRank orders modes by restriction for monotonic inheritance.
func netRank(mode string) int {
	switch mode {
	case "private":
		return 1
	case "none":
		return 2
	default: // host and unset
		return 0
	}
}

// endpointFeatureNames are the integration toggles described by
// featureEndpoints; tty controls process/session isolation, and network is
// NetPolicy's alone rather than a feature key.
var endpointFeatureNames = []string{"x11", "wayland", "dbus", "audio", "agents"}

// ResolvePackageSandbox computes policy independently of activation, so
// `bunny run --sandbox <id>` works on a package with no config entry. Two
// layers merge over the built-in defaults: the selected profile, then the
// package's inline override. profileOverride (from --sandbox-profile)
// replaces the configured profile for this invocation; pass "" to use it.
func ResolvePackageSandbox(cfg *config.Config, id string, profileOverride string) (*PackageSandbox, error) {
	var pkg config.SandboxPackage
	if cfg != nil {
		pkg = cfg.Sandbox.Packages[id]
	}

	effective := &PackageSandbox{Home: "isolated", Features: map[string]bool{}}

	profile := pkg.Profile
	if profileOverride != "" {
		profile = profileOverride
	}
	if profile != "" {
		policy, ok := cfg.SandboxProfile(profile)
		if !ok {
			return nil, fmt.Errorf("sandbox package %q selects unknown profile %q", id, profile)
		}
		effective.mergeLayer(policy.AsManifest())
	}
	effective.mergeLayer(pkg.AsManifest())
	effective.Hide = dedupSorted(effective.Hide)
	effective.Persist = dedupSorted(effective.Persist)
	if err := effective.finalize(id); err != nil {
		return nil, err
	}
	return effective, nil
}

// mergeLayer applies one policy layer. Home, Boundary, cwd, and net.mode use
// the last explicitly specified value; Hide appends; present grant and
// allowlist fields replace inherited ones.
func (dst *PackageSandbox) mergeLayer(src *manifest.SandboxPolicy) {
	if src == nil {
		return
	}
	if src.Boundary != "" {
		dst.Boundary = src.Boundary
	}
	if src.Home != "" {
		dst.Home = src.Home
	}
	dst.Hide = append(dst.Hide, src.Hide...)
	dst.Persist = append(dst.Persist, src.Persist...)
	maps.Copy(dst.Features, src.Features)

	if src.Net != nil {
		if src.Net.Mode != "" {
			dst.Net.Mode = src.Net.Mode
		}
		if src.Net.Inbound != nil {
			dst.Net.Inbound = slices.Clone(*src.Net.Inbound)
			dst.Net.InboundSet = true
		}
		if src.Net.Egress != nil {
			dst.Net.Egress = slices.Clone(*src.Net.Egress)
			dst.Net.EgressSet = true
		}
	}
	if src.FS != nil {
		if src.FS.Read != nil {
			dst.FS.Read = slices.Clone(*src.FS.Read)
			dst.FS.ReadSet = true
		}
		if src.FS.Write != nil {
			dst.FS.Write = slices.Clone(*src.FS.Write)
			dst.FS.WriteSet = true
		}
		if src.FS.Cwd != "" {
			dst.FS.Cwd = src.FS.Cwd
		}
	}
}

// finalize resolves boundary-sensitive defaults and enforces the cross-layer
// rules parse-time validation cannot see. Scoped features default enabled,
// preserving compatibility; hardened integrations default disabled, and
// process/session isolation is mandatory there, so an inherited or explicit
// tty: true is forced off rather than rejected (--explain reports it).
func (p *PackageSandbox) finalize(id string) error {
	if p.Boundary == "" {
		p.Boundary = "scoped"
	}
	hardened := p.Boundary == "hardened"
	if hardened && p.Home == "shared" {
		return fmt.Errorf("sandbox package %q: hardened boundary cannot share the host home", id)
	}
	if !hardened && (p.FS.ReadSet || p.FS.WriteSet || p.FS.Cwd != "") {
		return fmt.Errorf("sandbox package %q: filesystem grants require boundary: hardened", id)
	}
	if len(p.Persist) > 0 && p.Home != "ephemeral" {
		return fmt.Errorf("sandbox package %q: persist is meaningful only with home: ephemeral (got %q)", id, p.Home)
	}
	// Cleaned-path compare: the two can be set in different layers, and
	// persist binds mount after mask mounts, so a missed conflict would
	// re-expose through persist exactly what hide masked.
	for _, path := range p.Persist {
		if manifest.PathConflictsWithHide(p.Hide, path) {
			return fmt.Errorf("sandbox package %q: %q cannot be both hidden and persisted", id, path)
		}
	}
	if p.Net.Mode == "" {
		p.Net.Mode = "host"
		if hardened {
			p.Net.Mode = "none"
		}
	}
	if p.Net.Mode != "private" && (p.Net.InboundSet || p.Net.EgressSet) {
		return fmt.Errorf("sandbox package %q: inbound/egress lists are meaningful only with net.mode private (got %q)", id, p.Net.Mode)
	}
	for _, name := range endpointFeatureNames {
		if _, set := p.Features[name]; !set {
			p.Features[name] = !hardened
		}
	}
	if hardened {
		p.Features["tty"] = false
		if p.FS.Cwd == "" {
			p.FS.Cwd = "read"
		}
	} else if _, set := p.Features["tty"]; !set {
		p.Features["tty"] = true
	}
	return nil
}

// sandboxActivated reports whether ordinary launches use the effective
// policy: presence under sandbox.packages is the whole rule.
func sandboxActivated(cfg *config.Config, id string) bool {
	if cfg == nil {
		return false
	}
	_, present := cfg.Sandbox.Packages[id]
	return present
}

// feature reads the finalized toggle map; finalize made every value concrete.
func (p *PackageSandbox) feature(name string) bool {
	enabled, set := p.Features[name]
	return !set || enabled
}

// featureEndpoints is the single descriptor for each integration toggle: the
// variables removed from the environment and the documented filesystem
// endpoints masked when the feature is disabled (or bound back when a
// hardened policy enables it). Masking enforces exactly these endpoints, not
// arbitrary socket paths, abstract Unix addresses, or alternate transports;
// protection from hostile code needs the hardened boundary or a VM.
// Endpoints are resolved at launch: one that does not exist is skipped,
// unlike a user hide path, and one created afterwards is not retroactively
// masked.
var featureEndpoints = []struct {
	name  string
	env   []string
	paths func(runtimeDir, hostHome string, env map[string]string) []string
}{
	{
		name: "x11",
		env:  []string{"DISPLAY"},
		paths: func(_, hostHome string, _ map[string]string) []string {
			return []string{"/tmp/.X11-unix", filepath.Join(hostHome, ".Xauthority")}
		},
	},
	{
		name: "wayland",
		env:  []string{"WAYLAND_DISPLAY"},
		paths: func(runtimeDir, _ string, _ map[string]string) []string {
			// The glob also matches the compositor's wayland-*.lock files.
			return globPaths(filepath.Join(runtimeDir, "wayland-*"))
		},
	},
	{
		name: "dbus",
		env:  []string{"DBUS_SESSION_BUS_ADDRESS"},
		paths: func(runtimeDir, _ string, _ map[string]string) []string {
			return []string{filepath.Join(runtimeDir, "bus"), "/run/dbus/system_bus_socket"}
		},
	},
	{
		name: "audio",
		env:  []string{"PULSE_SERVER", "PIPEWIRE_REMOTE"},
		paths: func(runtimeDir, _ string, _ map[string]string) []string {
			return append([]string{filepath.Join(runtimeDir, "pulse")},
				globPaths(filepath.Join(runtimeDir, "pipewire-*"))...)
		},
	},
	{
		name: "agents",
		env:  []string{"SSH_AUTH_SOCK", "GPG_AGENT_INFO"},
		paths: func(runtimeDir, _ string, env map[string]string) []string {
			paths := []string{filepath.Join(runtimeDir, "gnupg"), filepath.Join(runtimeDir, "keyring")}
			if sock := env["SSH_AUTH_SOCK"]; sock != "" {
				paths = append(paths, sock)
			}
			return paths
		},
	},
}

func globPaths(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

// maskEntry is one path to hide, pre-stat'd so emission needs no I/O.
type maskEntry struct {
	path string
	dir  bool
}

// args renders the kernel-enforced mask: directories are covered with an
// empty tmpfs, files with /dev/null.
func (m maskEntry) args() []string {
	if m.dir {
		return []string{"--tmpfs", m.path}
	}
	return []string{"--ro-bind", "/dev/null", m.path}
}

func maskMountArgs(masks []maskEntry) []string {
	var args []string
	for _, m := range masks {
		args = append(args, m.args()...)
	}
	return args
}

// payloadEnvArgs delivers the sandboxed process's environment through
// bubblewrap rather than through the helper's own process environment: the
// helper (bwrap, pasta, the proxy) runs with a trusted, loader-sanitized
// environment (trustedHelperEnv), and --clearenv drops it before the child so
// only these explicit variables — the resolved payload set, which may legally
// include the package's own LD_* — reach the payload. env is pre-sorted by
// sandboxEnv, so the argument list is deterministic.
func payloadEnvArgs(env []string) []string {
	args := make([]string, 0, 1+2*len(env))
	args = append(args, "--clearenv")
	for _, assignment := range env {
		if name, value, ok := strings.Cut(assignment, "="); ok {
			args = append(args, "--setenv", name, value)
		}
	}
	return args
}

// helperUnsafeVars are the environment variables the dynamic loader and glibc
// treat as unsafe to inherit across a trust boundary: they make a process
// load attacker-chosen code or data files before it runs any of its own code.
// A sandbox helper runs before any isolation exists, so a package must not
// reach its loader through the launch environment. This mirrors glibc's own
// unsecvars list; every LD_* variable is dropped by prefix in addition.
var helperUnsafeVars = map[string]bool{
	"GCONV_PATH": true, "GETCONF_DIR": true, "GLIBC_TUNABLES": true,
	"HOSTALIASES": true, "LOCALDOMAIN": true, "LOCPATH": true,
	"MALLOC_TRACE": true, "NIS_PATH": true, "NLSPATH": true,
	"RESOLV_HOST_CONF": true, "RES_OPTIONS": true, "TMPDIR": true, "TZDIR": true,
}

// trustedHelperEnv returns Bunny's own environment with loader- and
// glibc-sensitive variables removed, for exec'ing a sandbox helper before it
// has established any boundary. Package-controlled variables live only in the
// payload environment (delivered via payloadEnvArgs), never here; the LD_*
// sweep is defence in depth against a value inherited from a parent process.
func trustedHelperEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, assignment := range src {
		name, _, _ := strings.Cut(assignment, "=")
		if strings.HasPrefix(name, "LD_") || helperUnsafeVars[name] {
			continue
		}
		out = append(out, assignment)
	}
	return out
}

func execTail(cwd string, p *Prepared) []string {
	return append([]string{"--chdir", cwd, "--", p.BinPath}, p.CmdArgs...)
}

// bindIfExists emits a self-bind for optional paths (integration sockets,
// automatic grants); required paths stat with their own errors instead.
func bindIfExists(args []string, flag, path string) []string {
	if _, err := os.Stat(path); err == nil {
		args = append(args, flag, path, path)
	}
	return args
}

// sandboxArgs builds the bwrap argument list for tests and simple callers;
// the full composition (context mount, pasta, proxy) lives in
// execPackageSandboxed.
func sandboxArgs(p *Prepared, policy *PackageSandbox, cwd, hostHome string) ([]string, error) {
	plan, err := buildSandboxPlan(p, policy, cwd, hostHome, sandboxContext{})
	if err != nil {
		return nil, err
	}
	return append(plan.args, plan.tail...), nil
}

func buildSandboxPlan(p *Prepared, policy *PackageSandbox, cwd, hostHome string, current sandboxContext) (sandboxPlan, error) {
	if current.HostHome != "" {
		hostHome = current.HostHome
	}
	nested := len(current.Packages) > 0
	packages := slices.Clone(current.Packages)
	if !slices.Contains(packages, p.Manifest.ID) {
		packages = append(packages, p.Manifest.ID)
	}

	envValues := envMap(p.Env)
	runtimeDir := envValues["XDG_RUNTIME_DIR"]
	if runtimeDir == "" {
		runtimeDir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}

	// Boundary is monotonic: a child of a hardened parent remains hardened.
	boundary := policy.Boundary
	if nested && current.Boundary == "hardened" {
		boundary = "hardened"
	}
	hardened := boundary == "hardened"

	net, err := clampNetwork(policy, current, nested)
	if err != nil {
		return sandboxPlan{}, err
	}

	// Feature restrictions are monotonic too: the union of everything any
	// enclosing layer disabled with what this policy disables.
	parentDisabled := stringSet(current.DisabledFeatures)
	disabled := maps.Clone(parentDisabled)
	for name, enabled := range policy.Features {
		if !enabled {
			disabled[name] = true
		}
	}
	// A non-host network mode forces the D-Bus masks, but in scoped mode the
	// variable itself stays: the masks are the enforcement, and removing the
	// address on top would misreport a policy that left dbus enabled. In
	// hardened mode there is no bus at all without the portal proxy, so the
	// variable goes too.
	if hardened && net.forcedDBus {
		disabled["dbus"] = true
	}

	next := sandboxContext{
		Packages:         packages,
		HostHome:         hostHome,
		Boundary:         boundary,
		DisabledFeatures: sortedMapKeys(disabled),
		NetMode:          net.mode,
	}
	if net.mode == "private" {
		// Concrete (possibly empty, never null) so a child reads the effective
		// sets rather than guessing.
		inbound := append([]string{}, net.inbound...)
		next.Inbound = &inbound
		if net.egressSet {
			egress := append([]string{}, net.egress...)
			next.Egress = &egress
		}
	}

	hidden := stringSet(current.Hidden)
	var masks []maskEntry
	addMask := func(path string, required bool) error {
		if hidden[path] {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				if required {
					return fmt.Errorf("sandbox hide path %s does not exist: create the intended file or directory first, or remove it from hide", path)
				}
				return nil
			}
			return fmt.Errorf("inspect sandbox mask path %s: %w", path, err)
		}
		hidden[path] = true
		masks = append(masks, maskEntry{path: path, dir: info.IsDir()})
		return nil
	}
	// User hide paths fail closed: Bunny cannot mount over a nonexistent
	// target without mutating the host, and cannot infer the target kind.
	for _, raw := range policy.Hide {
		if err := addMask(expandHome(raw, hostHome), true); err != nil {
			return sandboxPlan{}, err
		}
	}
	if !hardened {
		// Endpoint masks for newly disabled integrations; a feature the parent
		// layer already disabled had its endpoints masked (or deliberately
		// skipped) there. A newly non-host network also masks both D-Bus
		// endpoints even when dbus was left enabled: network isolation cannot
		// safely expose a bus that can launch processes outside the network
		// namespace. Hardened mode needs none of this: its private /run,
		// /tmp, and hidden home cover every documented endpoint.
		for _, feature := range featureEndpoints {
			newlyDisabled := disabled[feature.name] && !parentDisabled[feature.name]
			forcedDBus := feature.name == "dbus" && net.newlyRestricted && !parentDisabled["dbus"]
			if !newlyDisabled && !forcedDBus {
				continue
			}
			for _, path := range feature.paths(runtimeDir, hostHome, envValues) {
				if err := addMask(path, false); err != nil {
					return sandboxPlan{}, err
				}
			}
		}
		// Any lookup is an unfiltered side channel: nss-resolve reaches
		// systemd-resolved over a varlink socket netfilter never sees, so
		// every restricted network mode masks the resolver IPC directory.
		if net.newlyRestricted {
			if err := addMask("/run/systemd/resolve", false); err != nil {
				return sandboxPlan{}, err
			}
		}
	}
	next.Hidden = sortedMapKeys(hidden)

	overrides := envMap(p.BunnyEnv)
	isolatedHome := ""
	if policy.Home == "isolated" || policy.Home == "ephemeral" || policy.Home == "clean" || hardened {
		isolatedHome = filepath.Join(p.Vars["data"], "home")
		overrides["HOME"] = isolatedHome
		overrides["XDG_CONFIG_HOME"] = filepath.Join(isolatedHome, ".config")
		overrides["XDG_CACHE_HOME"] = filepath.Join(isolatedHome, ".cache")
		overrides["XDG_DATA_HOME"] = filepath.Join(isolatedHome, ".local", "share")
		// Redirecting HOME is what costs the package its Git identity, so the
		// replacement belongs here rather than in one profile. An identity
		// already in the environment was chosen deliberately and wins.
		payload := envMap(p.Env)
		for name, value := range gitIdentityOverrides(cwd) {
			if _, ok := payload[name]; !ok {
				overrides[name] = value
			}
		}
	}

	plan := sandboxPlan{
		context:      next,
		isolatedHome: isolatedHome,
		forcedDBus:   net.forcedDBus,
	}
	if net.createsNS {
		plan.pasta = &pastaSpec{
			inbound:   net.inbound,
			egress:    net.egress,
			egressSet: net.egressSet,
			dns:       !(net.egressSet && len(net.egress) == 0),
		}
	}

	if hardened {
		return buildHardenedPlan(p, policy, plan, current, net, hardenedEnv{
			cwd: cwd, hostHome: hostHome, runtimeDir: runtimeDir,
			isolatedHome: isolatedHome, envValues: envValues,
			overrides: overrides, disabled: disabled, masks: masks,
		})
	}

	newTTYRestriction := disabled["tty"] && !parentDisabled["tty"]
	plan.env = sandboxEnv(p.Env, overrides, disabled)
	ephemeral := policy.Home == "ephemeral"
	clean := policy.Home == "clean"
	if nested && !net.newlyUnshared && plan.pasta == nil && !newTTYRestriction && len(masks) == 0 && !ephemeral && !clean {
		return plan, nil
	}

	args := []string{"--dev-bind", "/", "/", "--die-with-parent"}
	args = append(args, ephemeralOverlayArgs(policy, isolatedHome)...)
	args = append(args, cleanHomeArgs(policy, isolatedHome)...)
	if net.newlyUnshared {
		args = append(args, "--unshare-net")
	}
	if newTTYRestriction {
		args = append(args, "--new-session", "--unshare-pid", "--proc", "/proc")
	}
	if plan.pasta != nil {
		// pasta drops the process into a user namespace with full capabilities;
		// the payload must not keep them, and must not be able to rewrite the
		// egress ruleset installed in this namespace.
		args = append(args, "--cap-drop", "ALL")
	}
	// The sandbox must not protect its own policy less than any other user
	// state: config.yaml is bound read-only wherever it exists. An absent
	// optional config needs no bind, and Bunny must not create one merely to
	// satisfy the mount.
	if !nested && p.ConfigFile != "" {
		if _, err := os.Stat(p.ConfigFile); err == nil {
			args = append(args, "--ro-bind", p.ConfigFile, p.ConfigFile)
		} else if !os.IsNotExist(err) {
			return sandboxPlan{}, fmt.Errorf("inspect config %s: %w", p.ConfigFile, err)
		}
	}
	args = append(args, maskMountArgs(masks)...)
	persistArgs, err := ephemeralPersistArgs(policy, isolatedHome)
	if err != nil {
		return sandboxPlan{}, err
	}
	args = append(args, persistArgs...)
	// After the resolver mask, so a target under /run/systemd/resolve lands
	// inside the bwrap-owned tmpfs rather than the unwritable host directory.
	if plan.pasta != nil {
		args = append(args, "--ro-bind", resolvConfPath(p.Manifest.ID), resolvConfBindTarget())
	}
	args = append(args, payloadEnvArgs(plan.env)...)

	plan.args = args
	plan.tail = execTail(cwd, p)
	plan.needsLayer = true
	return plan, nil
}

// clampedNet is the network outcome of monotonic inheritance.
type clampedNet struct {
	mode            string
	inbound         []string
	egress          []string
	egressSet       bool
	createsNS       bool // this launch runs pasta
	newlyUnshared   bool // this launch adds --unshare-net
	newlyRestricted bool // host -> private/none transition happens here
	forcedDBus      bool // non-host mode cut D-Bus off despite policy
}

// clampNetwork resolves the effective mode as the most restrictive of the
// policy's and the inherited one (host < private < none), and clamps
// allowlists. Narrowing an existing private namespace needs capabilities the
// inner bubblewrap already dropped and stacking a second pasta is not a
// supported topology, so a nested narrower list is an error naming the parent
// rather than a silent wish.
func clampNetwork(policy *PackageSandbox, current sandboxContext, nested bool) (clampedNet, error) {
	inheritedMode := "host"
	if nested && current.NetMode != "" {
		inheritedMode = current.NetMode
	}
	mode := policy.Net.Mode
	if mode == "" {
		mode = "host"
	}
	if netRank(inheritedMode) > netRank(mode) {
		mode = inheritedMode
	}

	out := clampedNet{
		mode:            mode,
		newlyRestricted: mode != "host" && inheritedMode == "host",
		forcedDBus:      mode != "host" && policy.feature("dbus"),
	}
	switch {
	case mode == "none":
		out.newlyUnshared = inheritedMode != "none"
	case mode == "private" && inheritedMode == "host":
		out.createsNS = true
		out.inbound = slices.Clone(policy.Net.Inbound)
		out.egress = slices.Clone(policy.Net.Egress)
		out.egressSet = policy.Net.EgressSet
	case mode == "private": // inherited private namespace
		parent := parentName(current)
		var parentInbound []string
		if current.Inbound != nil {
			parentInbound = *current.Inbound
		}
		if policy.Net.InboundSet && !manifest.InboundCovers(policy.Net.Inbound, parentInbound) {
			return clampedNet{}, narrowingError(parent, "inbound")
		}
		out.inbound = slices.Clone(parentInbound)
		if policy.Net.EgressSet {
			if current.Egress == nil || !manifest.EgressCovers(policy.Net.Egress, *current.Egress) {
				return clampedNet{}, narrowingError(parent, "egress")
			}
		}
		if current.Egress != nil {
			out.egress = slices.Clone(*current.Egress)
			out.egressSet = true
		}
	}
	return out, nil
}

func parentName(current sandboxContext) string {
	if len(current.Packages) > 0 {
		return current.Packages[len(current.Packages)-1]
	}
	return "parent"
}

func narrowingError(parent, list string) error {
	return fmt.Errorf("cannot narrow the %s allowlist of the private network inherited from %q: narrowing an existing namespace is not enforceable once capabilities are dropped; put the network policy on the top-level application", list, parent)
}

func sandboxEnv(base []string, overrides map[string]string, disabled map[string]bool) []string {
	values := envMap(base)
	maps.Copy(values, overrides)
	delete(values, legacySandboxContextEnv)
	for _, feature := range featureEndpoints {
		if disabled[feature.name] {
			for _, name := range feature.env {
				delete(values, name)
			}
		}
	}
	names := sortedMapKeys(values)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+values[name])
	}
	return out
}

func envMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, assignment := range env {
		name, value, ok := strings.Cut(assignment, "=")
		if ok {
			values[name] = value
		}
	}
	return values
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

// readMountedContext reads the context installed by an enclosing sandbox
// layer. An absent file means no verified restrictions: the caller must build
// its full layer rather than assume anything about the parent. A malformed
// file is a launch error, never a fallback to a weaker assumption.
func readMountedContext() (sandboxContext, error) {
	return readSandboxContextFile(sandboxContextFile)
}

func readSandboxContextFile(path string) (sandboxContext, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return sandboxContext{}, nil
		}
		return sandboxContext{}, fmt.Errorf("read sandbox context %s: %w", path, err)
	}
	var context sandboxContext
	if err := json.Unmarshal(data, &context); err != nil {
		return sandboxContext{}, fmt.Errorf("decode mounted sandbox context %s: %w", path, err)
	}
	return context, nil
}

// inheritSandboxEnv reapplies environment-based restrictions after manifest
// and user overlays, based on the mounted context. This also covers an
// on-demand or unconfigured package: it does not get its own isolated home,
// but it cannot loosen its parent's sandbox merely by setting DISPLAY or
// another integration variable. The masked endpoints are enforced by the
// kernel either way; the environment handling is hygiene, not the boundary.
func inheritSandboxEnv(env []string) ([]string, error) {
	context, err := readMountedContext()
	if err != nil {
		return nil, err
	}
	return inheritedSandboxEnv(env, context), nil
}

func inheritedSandboxEnv(env []string, context sandboxContext) []string {
	if len(context.Packages) == 0 {
		return env
	}
	return sandboxEnv(env, nil, stringSet(context.DisabledFeatures))
}

// ExecPackage runs a prepared package directly unless the user explicitly
// enabled it in sandbox.packages. Sandboxed launches still use syscall.Exec,
// preserving normal signals and exit status exactly like the direct path.
func ExecPackage(p *Prepared, cfg *config.Config) error {
	if !sandboxActivated(cfg, p.Manifest.ID) {
		return directExec(p)
	}
	return execPackageSandboxed(p, cfg, "")
}

// ExecPackageSandboxed forces one package-aware sandbox launch regardless of
// its normal activation. Manifest recommendations and any configured profile
// or package override still compose exactly as they do for always activation.
// profileOverride, from `bunny run --sandbox <id> --sandbox-profile <name>`, replaces the
// package's configured profile for this invocation only; pass "" to use it.
func ExecPackageSandboxed(p *Prepared, cfg *config.Config, profileOverride string) error {
	return execPackageSandboxed(p, cfg, profileOverride)
}

func execPackageSandboxed(p *Prepared, cfg *config.Config, profileOverride string) error {
	plan, policy, err := planPackageSandbox(p, cfg, profileOverride)
	if err != nil {
		return err
	}
	// Unprivileged overlayfs is not universal; a silent fallback to isolated
	// would persist what the user asked to discard, so an ephemeral launch
	// fails closed here rather than degrading.
	if policy.Home == "ephemeral" {
		if err := CheckOverlaySupport(); err != nil {
			return err
		}
	}
	if err := ensureIsolatedHome(plan.isolatedHome); err != nil {
		return err
	}
	log.Debug("Sandboxed exec", "package", p.Manifest.ID, "boundary", plan.context.Boundary,
		"net", plan.context.NetMode, "nestedLayer", plan.needsLayer)
	if !plan.needsLayer {
		args := append([]string{p.BinPath}, p.CmdArgs...)
		return syscall.Exec(p.BinPath, args, plan.env)
	}
	bwrapPath, err := FindBwrap()
	if err != nil {
		return err
	}
	contextArgs := mountContextArgs(bwrapPath, plan.context)
	if plan.pasta != nil {
		// pasta closes inherited descriptors, so the memfd cannot reach the
		// inner bubblewrap. The context travels as a host file instead; the
		// private tmpfs over the runtime directory still prevents the payload
		// from replacing what bubblewrap mounted.
		contextArgs, err = fileContextArgs(plan.context, p.Manifest.ID)
		if err != nil {
			return err
		}
	}
	argv := append([]string{bwrapPath}, plan.args...)
	argv = append(argv, contextArgs...)
	argv = append(argv, plan.tail...)

	// Helpers run with a trusted, loader-sanitized environment; the payload's
	// own environment is delivered inside the sandbox via payloadEnvArgs.
	if plan.pasta != nil {
		return execUnderPasta(p, plan, argv)
	}
	if plan.proxy != nil {
		return runSupervised(plan.proxy, argv, trustedHelperEnv())
	}
	return syscall.Exec(bwrapPath, argv, trustedHelperEnv())
}

// planPackageSandbox resolves policy, reads the mounted context, and builds
// the launch plan without touching the host; shared by execution and
// --explain so what is shown is what runs.
func planPackageSandbox(p *Prepared, cfg *config.Config, profileOverride string) (sandboxPlan, *PackageSandbox, error) {
	policy, err := ResolvePackageSandbox(cfg, p.Manifest.ID, profileOverride)
	if err != nil {
		return sandboxPlan{}, nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return sandboxPlan{}, nil, fmt.Errorf("get working directory: %w", err)
	}
	hostHome, err := os.UserHomeDir()
	if err != nil {
		return sandboxPlan{}, nil, fmt.Errorf("resolve host home: %w", err)
	}
	context, err := readMountedContext()
	if err != nil {
		return sandboxPlan{}, nil, err
	}
	plan, err := buildSandboxPlan(p, policy, cwd, hostHome, context)
	return plan, policy, err
}

func ensureIsolatedHome(home string) error {
	if home == "" {
		return nil
	}
	for _, dir := range []string{
		filepath.Join(home, ".config"),
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".local", "share"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create sandbox data directory %s: %w", dir, err)
		}
	}
	return nil
}

// stageContext prepares the encoded context and its runtime directory. When
// any step is unavailable the context is simply not installed: a child that
// finds no context builds its full layer against the already restricted root
// instead of trusting the environment.
func stageContext(context sandboxContext) ([]byte, bool) {
	if _, err := ensureRuntimeStateDir(); err != nil {
		log.Debug("Cannot create sandbox context directory", "error", err)
		return nil, false
	}
	encoded, err := json.Marshal(context)
	if err != nil {
		log.Debug("Cannot encode sandbox context", "error", err)
		return nil, false
	}
	return encoded, true
}

// mountContextArgs installs the effective policy as a read-only file inside
// the layer being created, from a memfd inherited across the exec. The
// private tmpfs over Bunny's runtime directory prevents the payload from
// replacing the file through the read-write host view; mounts inherited into
// a nested unprivileged user namespace are locked.
func mountContextArgs(bwrapPath string, context sandboxContext) []string {
	if !bwrapSupportsRoBindData(bwrapPath) {
		log.Debug("bwrap lacks --ro-bind-data; nested launches lose layer elision")
		return nil
	}
	encoded, ok := stageContext(context)
	if !ok {
		return nil
	}
	fd, err := contextMemfd(encoded)
	if err != nil {
		log.Debug("Cannot stage sandbox context", "error", err)
		return nil
	}
	return []string{"--tmpfs", runtimeStateDir(), "--ro-bind-data", strconv.Itoa(fd), sandboxContextFile}
}

// fileContextArgs is the pasta-path variant of mountContextArgs: the context
// is staged as a mode-0600 host file that bubblewrap reads at setup (bind
// sources resolve against the host view, so the tmpfs mounted over the same
// directory does not hide it from setup, only from the payload). Works on any
// bubblewrap, no --ro-bind-data required.
func fileContextArgs(context sandboxContext, id string) ([]string, error) {
	encoded, ok := stageContext(context)
	if !ok {
		return nil, nil
	}
	path := filepath.Join(runtimeStateDir(), "context-"+id+".json")
	if err := fsutil.WriteFile(path, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("stage sandbox context: %w", err)
	}
	return []string{"--tmpfs", runtimeStateDir(), "--ro-bind", path, sandboxContextFile}, nil
}

// contextMemfd stages the encoded context in a memfd that survives the exec
// into bwrap (no MFD_CLOEXEC), rewound so bwrap reads it from the start.
func contextMemfd(data []byte) (int, error) {
	fd, err := unix.MemfdCreate("bunny-sandbox-context", 0)
	if err != nil {
		return 0, fmt.Errorf("memfd_create: %w", err)
	}
	for off := 0; off < len(data); {
		n, err := unix.Write(fd, data[off:])
		if err != nil {
			unix.Close(fd)
			return 0, fmt.Errorf("write context memfd: %w", err)
		}
		off += n
	}
	if _, err := unix.Seek(fd, 0, 0); err != nil {
		unix.Close(fd)
		return 0, fmt.Errorf("rewind context memfd: %w", err)
	}
	return fd, nil
}

// bwrapSupportsRoBindData probes for the data-bind primitive the mounted
// context needs; the process execs away right after, so no caching is worth
// carrying. Old bubblewrap keeps working; it only loses nested-layer elision,
// never falls back to trusting an environment value.
func bwrapSupportsRoBindData(bwrapPath string) bool {
	out, _ := exec.Command(bwrapPath, "--help").CombinedOutput()
	return bytes.Contains(out, []byte("--ro-bind-data"))
}

func expandHome(path, home string) string {
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	case filepath.IsAbs(path):
		return filepath.Clean(path)
	default:
		return filepath.Join(home, path)
	}
}

func dedupSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}
