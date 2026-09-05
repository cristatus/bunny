package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cristatus/bunny/internal/config"
	"github.com/cristatus/bunny/internal/ui"
)

// preflightHeading names the report. Both exits print it, so the reader sees
// one report shape whether or not the policy could be planned.
const preflightHeading = "Sandbox preflight"

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

// CheckSandbox resolves the exact launch plan and verifies only the optional
// kernel features and helper programs that plan will use. It does not launch
// the package.
func CheckSandbox(p *Prepared, cfg *config.Config, profileOverride string, out *ui.Printer) (string, error) {
	plan, policy, err := planPackageSandbox(p, cfg, profileOverride)
	if err != nil {
		blocked := ""
		if policy != nil {
			blocked = explainBlocked(policy, out) + "\n"
		}
		rows := []preflightRow{{statusFail, "policy", err.Error()}}
		return blocked + renderPreflight(out, rows, 1), err
	}
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
	return renderPreflight(out, rows, len(failures)), errors.Join(failures...)
}

// renderPreflight lays the rows out the way `bunny doctor` lays its checks
// out — status glyph, name padded to the widest, detail — and closes with the
// one-line verdict.
func renderPreflight(p *ui.Printer, rows []preflightRow, failures int) string {
	nameW := 0
	for _, r := range rows {
		nameW = max(nameW, len(r.name))
	}
	var b strings.Builder
	b.WriteString(p.Faint(preflightHeading) + "\n")
	for _, r := range rows {
		glyph, style := preflightGlyph(r.status)
		fmt.Fprintf(&b, "%s %-*s  %s\n", p.PaintStatus(glyph, style), nameW, r.name, r.detail)
	}
	if failures == 0 {
		b.WriteString("\nready to launch\n")
	} else {
		fmt.Fprintf(&b, "\n%d required check(s) failed\n", failures)
	}
	return b.String()
}

func preflightGlyph(status string) (string, ui.Style) {
	switch status {
	case statusSkip:
		return "·", ui.Faint
	case statusWarn:
		return "⚠", ui.Plain
	case statusFail:
		return "✗", ui.Bad
	default:
		return "✓", ui.Good
	}
}
