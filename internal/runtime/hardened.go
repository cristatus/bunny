package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charmbracelet/log"
)

// hardenedEnv carries the launch facts the hardened planner needs beyond the
// policy itself.
type hardenedEnv struct {
	cwd, hostHome, runtimeDir, isolatedHome string
	envValues                               map[string]string
	overrides                               map[string]string
	disabled                                map[string]bool
	masks                                   []maskEntry
}

// buildHardenedPlan turns the filesystem model from a blacklist into an
// allowlist: read-only host root, hidden host home and mount roots, private
// /run, /tmp, and /var/tmp, and explicit grants. PID, IPC, and UTS isolation,
// a new session, fresh procfs, a minimal /dev, and a full capability drop are
// part of the boundary, not optional toggles. Descendant user namespaces stay
// permitted so Chromium can build its own zygote sandbox.
func buildHardenedPlan(p *Prepared, policy *PackageSandbox, plan sandboxPlan, current sandboxContext, net clampedNet, env hardenedEnv) (sandboxPlan, error) {
	nested := len(current.Packages) > 0 && current.Boundary == "hardened"
	read, write, err := effectiveGrants(policy, current, env.hostHome, nested)
	if err != nil {
		return sandboxPlan{}, err
	}
	plan.context.FSRead = read
	plan.context.FSWrite = write
	netUnshare := net.mode == "none" && net.newlyUnshared

	if nested {
		return buildNestedHardenedPlan(p, policy, plan, current, env, read, write, netUnshare)
	}

	// The filtered portal bus exists only at the boundary-establishing layer:
	// inside an existing hardened sandbox the raw bus is already unreachable,
	// so a nested proxy has nothing to connect to. Non-host network modes
	// exclude the proxy too — portals execute on the host side with host
	// network access, which is an escape from network isolation.
	if policy.feature("dbus") && net.mode == "host" {
		plan.proxy = newDBusProxySpec(p.Manifest.ID)
		env.overrides["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + filepath.Join(env.runtimeDir, "bus")
		delete(env.disabled, "dbus")
	}

	roots := protectedRoots(env.hostHome)

	args := []string{
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--tmpfs", "/var/tmp",
		"--tmpfs", "/run",
	}
	for _, root := range []string{"/mnt", "/media"} {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			args = append(args, "--tmpfs", root)
		}
	}
	args = append(args, "--tmpfs", env.hostHome)

	// The default read-only working directory mounts before package state and
	// explicit grants, so a cwd that happens to contain them cannot demote
	// their access; an explicit cwd: write or hidden is deliberate policy and
	// mounts after them instead. A cwd that is a protected root or an
	// ancestor of one (launching from $HOME or /) would re-expose what the
	// baseline hid, so the default read simply leaves it masked rather than
	// binding it back.
	cwdEscapes := grantEscapesBaseline(resolveReal(env.cwd), roots)
	if policy.FS.Cwd == "read" && !cwdEscapes {
		args = append(args, "--ro-bind", env.cwd, env.cwd)
	}

	// Writable state: the package's own data tree (which contains its
	// isolated home) and nothing else of Bunny's. Bunny's control state is
	// bound read-only further down, so a shim can resolve a package but no
	// mutation command works inside.
	if dataDir := p.Vars["data"]; dataDir != "" {
		args = append(args, "--bind", dataDir, dataDir)
	}
	// An ephemeral home overlays its own subtree of the data bind above:
	// reads see the seed, writes land in a discardable tmpfs upper. A clean
	// home instead replaces it outright with an empty tmpfs, no seed at all.
	// Both are layered after the data bind so they win for that specific path.
	args = append(args, ephemeralOverlayArgs(policy, env.isolatedHome)...)
	args = append(args, cleanHomeArgs(policy, env.isolatedHome)...)

	// Automatic read-only grants derive from the prepared package, its
	// resolved providers, and Bunny's own layout, so a shim keeps working
	// inside the boundary. They mount after the writable data bind and before
	// explicit grants: read-only on Bunny's install roots, and a policy can
	// still widen a specific path to write.
	for _, grant := range autoReadGrants(p, roots) {
		args = bindIfExists(args, "--ro-bind", grant)
	}
	for _, grant := range read {
		args = append(args, "--ro-bind", grant, grant)
	}
	for _, grant := range write {
		args = append(args, "--bind", grant, grant)
	}
	switch policy.FS.Cwd {
	case "write":
		if cwdEscapes {
			return sandboxPlan{}, fmt.Errorf("sandbox cwd %s is a protected root or an ancestor of one; run from a specific subdirectory to grant it write access", env.cwd)
		}
		args = append(args, "--bind", env.cwd, env.cwd)
	case "hidden":
		args = append(args, "--tmpfs", env.cwd)
	}

	// The baseline's private /run swallows the resolver configuration: on a
	// systemd-resolved host /etc/resolv.conf is a symlink into /run, so it
	// dangles inside and every name lookup fails. Host networking has nothing
	// to gain from hiding it — the package can reach the network regardless —
	// so bind the real file back. Restricted modes deliberately keep it
	// masked, and the pasta path installs its own resolv.conf instead.
	if net.mode == "host" {
		args = append(args, hostResolverBinds()...)
	}

	args = append(args, "--perms", "0700", "--dir", env.runtimeDir)
	env.overrides["XDG_RUNTIME_DIR"] = env.runtimeDir
	args = append(args, hardenedIntegrationBinds(policy, env, env.overrides)...)
	if plan.proxy != nil {
		args = append(args, "--bind", plan.proxy.socketPath, filepath.Join(env.runtimeDir, "bus"))
	}
	if plan.pasta != nil {
		args = append(args, "--ro-bind", resolvConfPath(p.Manifest.ID), resolvConfBindTarget())
	}
	args = append(args, maskMountArgs(env.masks)...)
	persistArgs, err := ephemeralPersistArgs(policy, env.isolatedHome)
	if err != nil {
		return sandboxPlan{}, err
	}
	args = append(args, persistArgs...)
	if netUnshare {
		args = append(args, "--unshare-net")
	}
	args = append(args,
		"--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--new-session", "--cap-drop", "ALL", "--die-with-parent",
	)
	// Compute the payload env last: hardenedIntegrationBinds and the proxy
	// block above may have added XAUTHORITY, XDG_RUNTIME_DIR, or the bus
	// address to env.overrides.
	plan.env = sandboxEnv(p.Env, env.overrides, env.disabled)
	args = append(args, payloadEnvArgs(plan.env)...)

	plan.args = args
	plan.tail = execTail(env.cwd, p)
	plan.needsLayer = true
	return plan, nil
}

// buildNestedHardenedPlan handles a hardened child inside an already hardened
// parent: the allowlist root already exists in the kernel, so the child only
// removes access — masking revoked grants and demoting revoked writes to
// read-only — and never rebuilds or widens the boundary. A child that changes
// nothing runs directly under the inherited boundary.
//
// An ephemeral or clean home is the exception to "only removes access": it
// is a property of this package's own home, which the parent's boundary never
// established, so the overlay or tmpfs is always added here too.
//
// Persist cannot follow: the parent only ever exposes its own package's data
// directory as writable, so a persist bind here would land read-only and its
// writes would silently fail to survive. It is rejected instead. Clean needs
// no writable host access, so it carries no such caveat.
func buildNestedHardenedPlan(p *Prepared, policy *PackageSandbox, plan sandboxPlan, current sandboxContext, env hardenedEnv, read, write []string, netUnshare bool) (sandboxPlan, error) {
	masks := slices.Clone(env.masks)
	var demoted []string
	for _, grant := range current.FSRead {
		if !slices.Contains(read, grant) && !slices.Contains(write, grant) {
			masks = append(masks, statMaskEntry(grant))
		}
	}
	for _, grant := range current.FSWrite {
		switch {
		case slices.Contains(write, grant):
		case slices.Contains(read, grant):
			demoted = append(demoted, grant)
		default:
			masks = append(masks, statMaskEntry(grant))
		}
	}

	plan.env = sandboxEnv(p.Env, env.overrides, env.disabled)
	ephemeral := policy.Home == "ephemeral"
	clean := policy.Home == "clean"
	if ephemeral && len(policy.Persist) > 0 {
		return sandboxPlan{}, fmt.Errorf("sandbox persist is not supported for %s: its hardened boundary is nested inside another hardened package, which cannot expose this package's data directory as writable durable storage", p.Manifest.ID)
	}
	if len(masks) == 0 && len(demoted) == 0 && !netUnshare && plan.pasta == nil && !ephemeral && !clean {
		return plan, nil // nothing new to enforce: inherit the boundary
	}
	args := []string{"--ro-bind", "/", "/", "--die-with-parent", "--cap-drop", "ALL"}
	args = append(args, ephemeralOverlayArgs(policy, env.isolatedHome)...)
	args = append(args, cleanHomeArgs(policy, env.isolatedHome)...)
	for _, grant := range demoted {
		args = append(args, "--ro-bind", grant, grant)
	}
	args = append(args, maskMountArgs(masks)...)
	if netUnshare {
		args = append(args, "--unshare-net")
	}
	args = append(args, payloadEnvArgs(plan.env)...)
	plan.args = args
	plan.tail = execTail(env.cwd, p)
	plan.needsLayer = true
	return plan, nil
}

// hostResolverBinds re-exposes the DNS configuration the private /run hides.
// Only a resolv.conf that resolves into /run needs it; anywhere else the
// read-only root bind already covers it.
func hostResolverBinds() []string {
	target, err := filepath.EvalSymlinks("/etc/resolv.conf")
	if err != nil || !strings.HasPrefix(target, "/run/") {
		return nil
	}
	return []string{"--ro-bind", target, target}
}

// statMaskEntry masks a revocation target, treating an unreadable path as a
// file: the /dev/null bind hides either kind.
func statMaskEntry(path string) maskEntry {
	info, err := os.Stat(path)
	return maskEntry{path: path, dir: err == nil && info.IsDir()}
}

// protectedRoots are the mount points the hardened baseline establishes as
// hidden or private. A grant or cwd bound back over one of these, or over an
// ancestor of one, would re-expose the real host content the baseline hid, so
// such paths are refused; a proper descendant (~/Projects, /run/foo) is an
// explicit, scoped grant and is allowed.
func protectedRoots(hostHome string) []string {
	return []string{hostHome, "/run", "/tmp", "/var/tmp", "/mnt", "/media", "/dev", "/proc"}
}

// grantEscapesBaseline reports whether binding path back would uncover a
// protected root: path is "/", equals a protected root, or is an ancestor of
// one.
func grantEscapesBaseline(path string, roots []string) bool {
	if path == "/" {
		return true
	}
	for _, root := range roots {
		if path == root || isAncestor(path, root) {
			return true
		}
	}
	return false
}

func isAncestor(ancestor, path string) bool {
	rel, err := filepath.Rel(ancestor, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../")
}

// resolveReal returns path with symlinks resolved, so a link pointing at a
// protected root cannot slip a grant past grantEscapesBaseline. It falls back
// to the original path when resolution fails (e.g. a not-yet-existing cwd).
func resolveReal(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

// effectiveGrants expands, verifies, and clamps the filesystem grants. Grant
// targets must already exist and resolve against the real host home; a
// missing path fails closed rather than causing Bunny to mutate the host. A
// grant that would re-expose a protected root (via its literal path or a
// symlink) is refused. A write grant implies read access. A nested child may
// remove grants but never add access absent from the inherited hardened
// context.
func effectiveGrants(policy *PackageSandbox, current sandboxContext, hostHome string, nested bool) (read, write []string, err error) {
	roots := protectedRoots(hostHome)
	expand := func(raw []string, kind string) ([]string, error) {
		out := make([]string, 0, len(raw))
		for _, entry := range raw {
			path := expandHome(entry, hostHome)
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("sandbox fs.%s path %s does not exist: grants fail closed rather than creating host paths", kind, path)
				}
				return nil, fmt.Errorf("inspect sandbox fs.%s path %s: %w", kind, path, err)
			}
			if grantEscapesBaseline(path, roots) || grantEscapesBaseline(resolveReal(path), roots) {
				return nil, fmt.Errorf("sandbox fs.%s path %s would re-expose a protected root; grant a specific subdirectory instead", kind, path)
			}
			out = append(out, path)
		}
		return dedupSorted(out), nil
	}
	if policy.FS.ReadSet {
		if read, err = expand(policy.FS.Read, "read"); err != nil {
			return nil, nil, err
		}
	}
	if policy.FS.WriteSet {
		if write, err = expand(policy.FS.Write, "write"); err != nil {
			return nil, nil, err
		}
	}
	if !nested {
		return read, write, nil
	}
	// Intersect with the mounted parent context; path lists supplied by the
	// child process are never trusted to widen it.
	if policy.FS.ReadSet {
		read = intersectStrings(read, slices.Concat(current.FSRead, current.FSWrite))
	} else {
		read = slices.Clone(current.FSRead)
	}
	if policy.FS.WriteSet {
		write = intersectStrings(write, current.FSWrite)
	} else {
		write = slices.Clone(current.FSWrite)
	}
	return read, write, nil
}

func intersectStrings(values, allowed []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if slices.Contains(allowed, value) {
			out = append(out, value)
		}
	}
	return out
}

// autoReadGrants lists the read-only paths a hardened package needs to run at
// all: its install tree, its resolved providers' trees, the Bunny executable,
// and Bunny's own layout so a shim inside the sandbox can resolve a package
// the policy never named. Deriving the layout beats asking for it in `fs.read`
// — the install roots are configurable, so a hand-written grant is a copy of
// Bunny's own settings that breaks when they change or when the config moves
// to another machine.
//
// A layout path that would re-expose a protected root is dropped rather than
// refused: an install root of $HOME is a legal `install:` setting, and losing
// shim re-entry is the containable failure. Explicit grants still error,
// because there the user named the path and means it.
func autoReadGrants(p *Prepared, roots []string) []string {
	grants := make([]string, 0, len(p.DepRoots)+len(p.LayoutRoots)+2)
	if app := p.Vars["app"]; app != "" {
		grants = append(grants, app)
	}
	grants = append(grants, p.DepRoots...)
	if exe, err := os.Executable(); err == nil {
		grants = append(grants, exe)
	}
	for _, path := range p.LayoutRoots {
		if grantEscapesBaseline(path, roots) || grantEscapesBaseline(resolveReal(path), roots) {
			log.Debug("Skipping sandbox layout grant that would re-expose a protected root", "path", path)
			continue
		}
		grants = append(grants, path)
	}
	return dedupSorted(grants)
}

// hardenedIntegrationBinds binds the enabled integrations' documented
// endpoints (the same catalog masking uses) back into the private /run and
// /tmp, and records extra variables in overrides. Socket binds are plain
// read-write binds: connect() on a Unix socket needs write permission on the
// inode, so a read-only bind would fail with EROFS. Endpoints under the
// hidden host home cannot be bound in place; the only such file, .Xauthority,
// is redirected into the isolated home instead.
func hardenedIntegrationBinds(policy *PackageSandbox, env hardenedEnv, overrides map[string]string) []string {
	var args []string
	for _, feature := range featureEndpoints {
		if feature.name == "dbus" || !policy.feature(feature.name) {
			continue
		}
		for _, path := range feature.paths(env.runtimeDir, env.hostHome, env.envValues) {
			if !strings.HasPrefix(path, env.hostHome+"/") {
				args = bindIfExists(args, "--bind", path)
			}
		}
	}
	if policy.feature("x11") && env.isolatedHome != "" {
		xauth := filepath.Join(env.hostHome, ".Xauthority")
		if _, err := os.Stat(xauth); err == nil {
			target := filepath.Join(env.isolatedHome, ".Xauthority")
			args = append(args, "--ro-bind", xauth, target)
			overrides["XAUTHORITY"] = target
		}
	}
	return args
}
