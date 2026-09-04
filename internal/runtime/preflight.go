package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cristatus/bunny/internal/config"
)

// CheckSandbox resolves the exact launch plan and verifies only the optional
// kernel features and helper programs that plan will use. It does not launch
// the package.
func CheckSandbox(p *Prepared, cfg *config.Config, profileOverride string) (string, error) {
	plan, policy, err := planPackageSandbox(p, cfg, profileOverride)
	if err != nil {
		out := ""
		if policy != nil {
			out = explainBlocked(policy) + "\n"
		}
		return out + fmt.Sprintf("Preflight\n  FAIL  policy  %v\n", err), err
	}
	type row struct{ status, name, detail string }
	rows := []row{{"OK", "policy", "resolved and enforceable in the current context"}}
	var failures []error
	check := func(name string, needed bool, fn func() (string, error)) {
		if !needed {
			rows = append(rows, row{"SKIP", name, "not required by this launch"})
			return
		}
		detail, err := fn()
		if err != nil {
			rows = append(rows, row{"FAIL", name, err.Error()})
			failures = append(failures, err)
			return
		}
		rows = append(rows, row{"OK", name, detail})
	}
	tool := func(find func() (string, error)) func() (string, error) {
		return func() (string, error) { return find() }
	}
	check("bubblewrap", plan.needsLayer, tool(FindBwrap))
	check("overlayfs", needsOverlayProbe(plan, policy), func() (string, error) {
		if err := CheckOverlaySupport(); err != nil {
			return "", err
		}
		return "unprivileged ephemeral overlay available", nil
	})
	check("pasta", plan.pasta != nil, tool(FindPasta))
	check("nftables", plan.pasta != nil && plan.pasta.egressSet, tool(FindNft))
	check("D-Bus proxy", plan.proxy != nil, tool(FindXDGDBusProxy))
	if plan.needsLayer && contextAvailable(plan) {
		rows = append(rows, row{"OK", "nested context", "immutable context propagation available"})
	} else if plan.needsLayer {
		rows = append(rows, row{"WARN", "nested context", "unavailable; child Bunny launches may be rejected or attempt another layer"})
	} else {
		rows = append(rows, row{"OK", "nested context", "already inherited from " + plan.nestedUnder})
	}

	var b strings.Builder
	b.WriteString("Sandbox preflight\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "  %-4s  %-15s %s\n", row.status, row.name, row.detail)
	}
	if len(failures) == 0 {
		b.WriteString("\nready to launch\n")
	} else {
		fmt.Fprintf(&b, "\n%d required check(s) failed\n", len(failures))
	}
	return b.String(), errors.Join(failures...)
}
