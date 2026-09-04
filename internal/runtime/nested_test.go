package runtime

import (
	"slices"
	"strings"
	"testing"
)

// The outermost sandbox owns the boundary. A launch inside one never builds a
// second layer, whatever its own policy asks for, because a nested layer buys
// coverage only for two separately sandboxed packages launching one another
// and cannot be built at all inside a private network namespace.
func TestNestedLaunchNeverBuildsALayer(t *testing.T) {
	parent := outerContext()
	for name, policy := range map[string]*PackageSandbox{
		"same policy":      {Home: "isolated"},
		"wants hardened":   {Boundary: "hardened"},
		"wants no network": {Home: "isolated", Net: NetPolicy{Mode: "none"}},
		"wants private":    {Home: "isolated", Net: NetPolicy{Mode: "private"}},
		"wants ephemeral":  {Home: "ephemeral"},
		"wants clean":      {Home: "clean"},
		"hides a path":     {Home: "isolated", Hide: []string{"~/.ssh"}},
		"disables audio":   {Home: "isolated", Features: map[string]bool{"audio": false}},
	} {
		p, _ := hardenedPrepared(t)
		resolved := finalized(t, policy)
		plan, err := buildSandboxPlan(p, resolved, "/work", "/home/u", parent)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if plan.needsLayer {
			t.Errorf("%s: nested launch must run directly", name)
		}
		if len(plan.args) != 0 {
			t.Errorf("%s: nested launch must build no bwrap args, got %v", name, plan.args)
		}
		if plan.pasta != nil || plan.proxy != nil {
			t.Errorf("%s: nested launch must start no helpers", name)
		}
		if needsOverlayProbe(plan, resolved) {
			t.Errorf("%s: direct nested launch must not probe overlay support", name)
		}
	}
}

// Restrictions already in force are inherited, so the context a nested launch
// reports stays monotonic: a hardened parent keeps a scoped child hardened,
// and a feature the parent disabled stays disabled.
func TestNestedLaunchInheritsRestrictions(t *testing.T) {
	p, _ := hardenedPrepared(t)
	parent := sandboxContext{
		Packages: []string{"outer"}, HostHome: "/home/u",
		Boundary: "hardened", NetMode: "private",
		DisabledFeatures: []string{"agents"},
		Hidden:           []string{"/home/u/.ssh"},
	}
	plan, err := buildSandboxPlan(p, finalized(t, &PackageSandbox{Home: "shared"}), "/work", "/home/u", parent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.context.Boundary != "hardened" {
		t.Errorf("boundary must stay hardened, got %q", plan.context.Boundary)
	}
	if plan.context.NetMode != "private" {
		t.Errorf("network mode must be inherited, got %q", plan.context.NetMode)
	}
	if !slices.Contains(plan.context.DisabledFeatures, "agents") {
		t.Errorf("a disabled feature must stay disabled: %v", plan.context.DisabledFeatures)
	}
	if !slices.Contains(plan.context.Hidden, "/home/u/.ssh") {
		t.Errorf("hidden paths must be inherited: %v", plan.context.Hidden)
	}
	if !slices.Contains(plan.context.Packages, "outer") || !slices.Contains(plan.context.Packages, p.Manifest.ID) {
		t.Errorf("context must accumulate packages: %v", plan.context.Packages)
	}
}

func TestNestedSharedHomeKeepsHardenedParentsHome(t *testing.T) {
	p, _ := hardenedPrepared(t)
	p.Env = append(p.Env, "HOME=/parent/home")
	parent := sandboxContext{Packages: []string{"outer"}, HostHome: "/home/u", Boundary: "hardened"}
	plan, err := buildSandboxPlan(p, finalized(t, &PackageSandbox{Home: "shared"}), "/work", "/home/u", parent)
	if err != nil {
		t.Fatal(err)
	}
	if got := envMap(plan.env)["HOME"]; got != "/parent/home" {
		t.Errorf("nested shared HOME = %q, want inherited parent home", got)
	}
	if plan.isolatedHome != "" {
		t.Errorf("nested shared home unexpectedly redirects to %q", plan.isolatedHome)
	}
}

func TestNestedRedirectedHomeRejectedUnderHardenedParent(t *testing.T) {
	p, _ := hardenedPrepared(t)
	parent := sandboxContext{Packages: []string{"outer"}, HostHome: "/home/u", Boundary: "hardened"}
	for _, mode := range []string{"isolated", "ephemeral", "clean"} {
		if _, err := buildSandboxPlan(p, finalized(t, &PackageSandbox{Home: mode}), "/work", "/home/u", parent); err == nil {
			t.Errorf("nested home %q must fail when its data directory is unavailable", mode)
		}
	}
}

// What a nested policy asked for and cannot get is named, so it is reported
// rather than silently dropped.
func TestNestedIgnoredNamesWhatCannotApply(t *testing.T) {
	parent := outerContext()
	policy := finalized(t, &PackageSandbox{
		Boundary: "hardened",
		Home:     "clean",
		Hide:     []string{"~/.ssh", "~/.aws"},
		Net:      NetPolicy{Mode: "none"},
		FS:       FSPolicy{Cwd: "write"},
	})
	got := nestedIgnored(policy, parent, map[string]bool{"audio": true})
	joined := strings.Join(got, "; ")
	for _, want := range []string{"boundary: hardened", "net: none", "home: clean", "hide: 2 path(s)", "features off: audio", "fs grants"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ignored list %q missing %q", joined, want)
		}
	}
}

// Nothing to report when the child asks for nothing the parent lacks, so an
// ordinary nested launch stays quiet.
func TestNestedIgnoredSilentWhenNothingIsLost(t *testing.T) {
	parent := sandboxContext{
		Packages: []string{"outer"}, HostHome: "/home/u",
		Boundary: "hardened", NetMode: "none",
		DisabledFeatures: []string{"audio"}, Hidden: []string{"/home/u/.ssh"},
	}
	policy := finalized(t, &PackageSandbox{
		Boundary: "hardened", Home: "isolated",
		Net:      NetPolicy{Mode: "none"},
		Features: map[string]bool{"audio": false},
	})
	disabled := map[string]bool{"audio": true, "tty": true}
	if got := nestedIgnored(policy, parent, disabled); len(got) > 0 {
		t.Errorf("nothing should be reported, got %v", got)
	}
}

// outerContext is the minimum an enclosing sandbox reports: it launched
// something, and it knows the real host home.
func outerContext() sandboxContext {
	return sandboxContext{Packages: []string{"outer"}, HostHome: "/home/u"}
}
