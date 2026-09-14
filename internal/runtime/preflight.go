package runtime

// readinessHeading names the section `--explain` appends after
// "Enforcement": whether the host can actually run the resolved plan, not
// just what the plan asks for.
const readinessHeading = "Host readiness"

// Row statuses. They render as the same glyphs `bunny doctor` uses, plus a
// faint marker for a component this launch never reaches.
const (
	statusOK   = "ok"
	statusSkip = "skip"
	statusWarn = "warn"
	statusFail = "fail"
)

// preflightRow is one probed component: its status, the name `bunny doctor`
// gives the same component, and what the probe found.
type preflightRow struct{ status, name, detail string }

// probeReadiness verifies only the optional kernel features and helper
// programs the resolved plan actually uses. It does not launch the package.
func probeReadiness(plan sandboxPlan, policy *PackageSandbox) ([]preflightRow, []error) {
	rows := []preflightRow{{statusOK, "policy", "resolved and enforceable in the current context"}}
	var failures []error
	check := func(name string, needed bool, fn func() (string, error)) {
		if !needed {
			rows = append(rows, preflightRow{statusSkip, name, "not required by this launch"})
			return
		}
		detail, err := fn()
		if err != nil {
			rows = append(rows, preflightRow{statusFail, name, err.Error()})
			failures = append(failures, err)
			return
		}
		rows = append(rows, preflightRow{statusOK, name, detail})
	}
	check("bwrap", plan.needsLayer, FindBwrap)
	check("overlay", needsOverlayProbe(plan, policy), func() (string, error) {
		if err := CheckOverlaySupport(); err != nil {
			return "", err
		}
		return "unprivileged ephemeral overlay available", nil
	})
	check("pasta", plan.pasta != nil, FindPasta)
	check("nft", plan.pasta != nil && plan.pasta.egressSet, FindNft)
	check("dbus-proxy", plan.proxy != nil, FindXDGDBusProxy)
	if plan.needsLayer && contextAvailable(plan) {
		rows = append(rows, preflightRow{statusOK, "nested context", "immutable context propagation available"})
	} else if plan.needsLayer {
		rows = append(rows, preflightRow{statusWarn, "nested context", "unavailable; child Bunny launches may be rejected or attempt another layer"})
	} else {
		rows = append(rows, preflightRow{statusOK, "nested context", "already inherited from " + plan.nestedUnder})
	}
	return rows, failures
}
