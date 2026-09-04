package runtime

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/cristatus/bunny/internal/config"
)

// Explain reports what launching p would actually do, mirroring the same
// branch ExecPackage and ExecPackageSandboxed take: a package that would run
// directly (no forced sandbox and no configured "always" activation) reports
// that fact instead of the plan a forced launch would produce, so --explain
// without --sandbox shows exactly what a plain `bunny run` does.
func Explain(p *Prepared, cfg *config.Config, forceSandbox bool, profileOverride string) (string, error) {
	if !forceSandbox && !sandboxActivated(cfg, p.Manifest.ID) {
		return fmt.Sprintf("%s runs directly: no sandbox policy is active for this launch (add it to sandbox.packages, or pass --sandbox for this launch only)\n", p.Manifest.ID), nil
	}
	return ExplainSandbox(p, cfg, profileOverride)
}

// ExplainSandbox prints the effective policy with each control's enforcement
// level and what it produces, without launching anything or touching the
// host. It runs the same resolution and planning as a real launch, so what is
// shown is what runs — including restrictions forced by the network mode and
// anything inherited from an enclosing sandbox.
func ExplainSandbox(p *Prepared, cfg *config.Config, profileOverride string) (string, error) {
	plan, policy, err := planPackageSandbox(p, cfg, profileOverride)
	if err != nil {
		// A policy that resolved but cannot be built here is the case --explain
		// exists for: report what was asked and what blocks it, rather than
		// leaving a reader with less than an attempted launch would tell them.
		// The error still travels, so the exit status says the launch would fail.
		if policy == nil {
			return "", err
		}
		return explainBlocked(policy), err
	}

	hardened := plan.context.Boundary == "hardened"
	var rows [][3]string
	add := func(name, level, detail string) { rows = append(rows, [3]string{name, level, detail}) }

	// A launch inside an existing sandbox adds no layer, so reporting the
	// policy it asked for would describe controls this launch does not apply.
	// Report what is actually in force, and what the policy did not get.
	if !plan.needsLayer {
		boundary := plan.context.Boundary
		if boundary == "" {
			boundary = "scoped"
		}
		add("nested", "none", "runs directly inside "+plan.nestedUnder+": the outermost sandbox owns the boundary")
		add("boundary", "inherited", boundary)
		if plan.isolatedHome != "" {
			add("home", "env", "isolated: "+plan.isolatedHome)
		}
		netMode := plan.context.NetMode
		if netMode == "" {
			netMode = "host"
		}
		add("network", "inherited", netMode)
		if len(plan.ignored) > 0 {
			add("ignored", "none", strings.Join(plan.ignored, ", "))
		}
		return renderExplainReport(plan, policy, rows), nil
	}

	if hardened {
		add("boundary", "mount+ns", "hardened: read-only root, hidden host home, private /run and /tmp")
	} else {
		add("boundary", "scoped", "read-write host view; masks and namespaces reported per control")
	}

	switch {
	case plan.isolatedHome == "":
		add("home", "none", "shared host home")
	case policy.Home == "ephemeral":
		add("home", "mount", "ephemeral: seed "+plan.isolatedHome+", writes discarded on exit")
	case policy.Home == "clean":
		add("home", "mount", "clean: blank tmpfs at "+plan.isolatedHome+", never seeded")
	case hardened:
		add("home", "mount", "isolated: "+plan.isolatedHome)
	default:
		add("home", "env", "isolated: "+plan.isolatedHome)
	}
	if len(policy.Persist) > 0 {
		add("persist", "mount", fmt.Sprintf("%d path(s): %s (survive the discard)",
			len(policy.Persist), strings.Join(policy.Persist, ", ")))
	}

	if hardened {
		detail := fmt.Sprintf("%d read, %d write grants; cwd %s", len(plan.context.FSRead), len(plan.context.FSWrite), policy.FS.Cwd)
		if grants := slices.Concat(plan.context.FSRead, plan.context.FSWrite); len(grants) > 0 {
			detail += ": " + strings.Join(grants, ", ")
		}
		add("fs", "mount", detail)
		// The grant counts above cover the policy only. Bunny's layout is
		// bound read-only regardless, and it is what keeps a shim working
		// inside the boundary, so a reader auditing access has to see it.
		add("layout", "mount", "read-only: shims, state, manifests, install roots")
	}

	switch plan.context.NetMode {
	case "host":
		add("network", "none", "host namespace, unrestricted")
	case "none":
		add("network", "namespace", "no network stack")
	case "private":
		inbound := "none"
		if plan.context.Inbound != nil && len(*plan.context.Inbound) > 0 {
			inbound = strings.Join(*plan.context.Inbound, ", ")
		}
		egress := "unrestricted (until an egress list is set)"
		if plan.context.Egress != nil {
			if len(*plan.context.Egress) == 0 {
				egress = "none (default-drop, no DNS)"
			} else {
				egress = strings.Join(*plan.context.Egress, ", ") + " (+DNS forwarder)"
			}
		}
		add("network", "namespace+filter", "pasta, inbound: "+inbound+", egress: "+egress)
	}

	switch {
	case plan.proxy != nil:
		add("dbus", "filter", "portal-only via xdg-dbus-proxy; raw buses unreachable")
	case plan.forcedDBus:
		add("dbus", "mount", "forced off by network mode; session and system endpoints masked")
	case policy.feature("dbus") && !hardened:
		add("dbus", "none", "host bus available")
	default:
		add("dbus", "mount", "session and system endpoints masked")
	}

	for _, name := range endpointFeatureNames {
		if name == "dbus" {
			continue // reported above with its network implication
		}
		switch {
		case policy.feature(name):
			add(name, "none", "enabled")
		case hardened:
			add(name, "mount", "excluded by the boundary: private /run and /tmp, hidden home")
		case name == "x11" && plan.context.NetMode == "host":
			// X clients prefer the abstract socket @/tmp/.X11-unix/X0, which
			// lives in the network namespace, not the filesystem; masking the
			// filesystem socket does not reach it under host networking.
			add(name, "env+mount", "variable removed, filesystem socket masked; abstract X11 socket still reachable (host networking)")
		default:
			add(name, "env+mount", "variables removed, documented endpoints masked")
		}
	}

	switch {
	case hardened:
		add("tty", "namespace", "forced off: new session, PID/IPC/UTS namespaces, fresh /proc")
	case !policy.feature("tty"):
		add("tty", "namespace", "new session, PID namespace, fresh /proc")
	default:
		add("tty", "none", "controlling terminal retained")
	}

	if len(policy.Hide) > 0 {
		add("hide", "mount", fmt.Sprintf("%d paths: %s", len(policy.Hide), strings.Join(policy.Hide, ", ")))
	}
	level, detail := contextRow(contextAvailable(plan))
	add("context", level, detail)

	return renderExplainReport(plan, policy, rows), nil
}

func renderExplainReport(plan sandboxPlan, policy *PackageSandbox, rows [][3]string) string {
	hardened := plan.context.Boundary == "hardened"
	project := "read-write (scoped host view)"
	hostHome := "readable (scoped boundary)"
	if pathCoveredBy(plan.cwd, plan.context.Hidden) {
		project = "hidden"
	}
	if pathCoveredBy(plan.context.HostHome, plan.context.Hidden) {
		hostHome = "hidden by policy"
	}
	if hardened {
		project = "read-only"
		if pathCoveredBy(plan.cwd, plan.context.WritableRoots) {
			project = "read-write"
		} else if pathCoveredBy(plan.cwd, plan.context.Hidden) || policy.FS.Cwd == "hidden" {
			project = "hidden"
		}
		hostHome = "hidden"
	}
	packageHome := "shared"
	switch policy.Home {
	case "isolated":
		packageHome = "private and persistent"
	case "ephemeral":
		packageHome = "seeded; writes discarded except persist paths"
	case "clean":
		packageHome = "blank; writes discarded"
	}
	network := plan.context.NetMode
	if network == "" {
		network = "host (unrestricted)"
	} else if network == "private" && plan.context.Egress != nil {
		if len(*plan.context.Egress) == 0 {
			network = "private; no egress"
		} else {
			network = "private; allowlisted egress"
		}
	}
	credentials := "credential-agent sockets available"
	if !policy.feature("agents") || slices.Contains(plan.context.DisabledFeatures, "agents") {
		credentials = "SSH/GnuPG agent sockets unavailable"
	}
	nesting := "new sandbox boundary"
	if !plan.needsLayer {
		nesting = "direct inside " + plan.nestedUnder
	}
	summary := [][3]string{
		{"project", "", project},
		{"host home", "", hostHome},
		{"package home", "", packageHome},
		{"network", "", network},
		{"credentials", "", credentials},
		{"nesting", "", nesting},
	}
	return "Risk summary\n" + renderExplain(summary) + "\nEnforcement\n" + renderExplain(rows)
}

func pathCoveredBy(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || isAncestor(root, path) {
			return true
		}
	}
	return false
}

// renderExplain aligns the rows into the name/level/detail columns.
func renderExplain(rows [][3]string) string {
	nameW, levelW := 0, 0
	for _, row := range rows {
		nameW = max(nameW, len(row[0]))
		levelW = max(levelW, len(row[1]))
	}
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "%-*s  %-*s  %s\n", nameW, row[0], levelW, row[1], row[2])
	}
	return b.String()
}

// contextRow reports whether the immutable mounted context will actually be
// installed. The pasta path binds it as a host file (works on any bubblewrap);
// otherwise it needs --ro-bind-data. Without the context a launch inside this
// sandbox cannot tell it is nested, so it would try to build a layer of its
// own. Reporting "immutable" unconditionally would overstate what is enforced.
//
// The availability probe forks bwrap, so it is passed in and taken once.
func contextRow(available bool) (level, detail string) {
	if available {
		return "mount", "immutable: " + sandboxContextFile
	}
	return "none", "unavailable (bubblewrap lacks --ro-bind-data); a launch inside this sandbox cannot detect it is nested"
}

func contextAvailable(plan sandboxPlan) bool {
	if plan.pasta != nil {
		return true // file-backed bind, no --ro-bind-data needed
	}
	path, err := exec.LookPath("bwrap")
	return err == nil && bwrapSupportsRoBindData(path)
}

// explainBlocked reports a resolved policy that cannot be planned in this
// context. Only the policy is available — there is no plan to read effective
// mounts from — so every row is labelled as requested rather than in force.
func explainBlocked(policy *PackageSandbox) string {
	var rows [][3]string
	add := func(name, level, detail string) { rows = append(rows, [3]string{name, level, detail}) }

	if policy.Profile != "" {
		add("profile", "requested", policy.Profile)
	}
	boundary := policy.Boundary
	if boundary == "" {
		boundary = "scoped"
	}
	add("boundary", "requested", boundary)
	add("home", "requested", policy.Home)
	add("network", "requested", policy.Net.Mode)
	if policy.FS.Cwd != "" {
		add("fs", "requested", fmt.Sprintf("cwd %s; %d read, %d write grants",
			policy.FS.Cwd, len(policy.FS.Read), len(policy.FS.Write)))
	}
	// The blocker itself is not a row: the caller prints the error, and
	// repeating it here would say the same sentence twice.
	return renderExplain(rows)
}
