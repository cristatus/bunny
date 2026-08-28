package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cristatus/bunny/internal/manifest"
)

func TestPastaArgsMandatoryIsolationFlags(t *testing.T) {
	args, err := pastaArgs(&pastaSpec{dns: true})
	if err != nil {
		t.Fatal(err)
	}
	// -T none -U none matter: their default is auto, which would re-expose
	// listening host services inside the sandbox.
	for _, want := range [][]string{
		{"--config-net"},
		{"-t", "none"}, {"-u", "none"},
		{"-T", "none"}, {"-U", "none"},
		{"--no-map-gw"},
		{"--dns-forward", dnsForwardAddr},
	} {
		if indexSequence(args, want) < 0 {
			t.Errorf("pasta args missing %v: %v", want, args)
		}
	}
}

func TestPastaArgsInboundMappingAndNoDNS(t *testing.T) {
	args, err := pastaArgs(&pastaSpec{inbound: []string{"8080/tcp", "53/udp", "9000-9010"}, dns: false})
	if err != nil {
		t.Fatal(err)
	}
	if indexSequence(args, []string{"-t", "8080,9000-9010"}) < 0 {
		t.Errorf("tcp mapping wrong: %v", args)
	}
	if indexSequence(args, []string{"-u", "53,9000-9010"}) < 0 {
		t.Errorf("udp mapping wrong: %v", args)
	}
	if slices.Contains(args, "--dns-forward") {
		t.Errorf("zero-egress policy must not forward DNS: %v", args)
	}
}

func TestNftRulesetDefaultDropAndDNSException(t *testing.T) {
	rules, err := nftRuleset([]string{"10.0.0.0/8:443", "192.168.1.10:5432/tcp", "2001:db8::/32"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"policy drop;",
		`oifname "lo" accept`,
		"ct state established,related accept",
		"ip daddr " + dnsForwardAddr + " udp dport 53 accept",
		"ip daddr " + dnsForwardAddr + " tcp dport 53 accept",
		"ip daddr 10.0.0.0/8 meta l4proto { tcp, udp } th dport 443 accept",
		"ip daddr 192.168.1.10/32 tcp dport 5432 accept",
		"ip6 daddr 2001:db8::/32 accept",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("ruleset missing %q:\n%s", want, rules)
		}
	}
}

func TestNftRulesetEmptyListHasNoExceptions(t *testing.T) {
	rules, err := nftRuleset(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rules, dnsForwardAddr) {
		t.Errorf("explicit empty egress must not open a DNS exception:\n%s", rules)
	}
	if !strings.Contains(rules, "policy drop;") {
		t.Errorf("empty egress must still default-drop:\n%s", rules)
	}
}

func TestWriteResolvConf(t *testing.T) {
	dir := t.TempDir()
	withDNS := filepath.Join(dir, "with.conf")
	if err := writeResolvConf(withDNS, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(withDNS)
	if !strings.Contains(string(data), "nameserver "+dnsForwardAddr) {
		t.Errorf("resolv.conf missing forwarder: %s", data)
	}
	noDNS := filepath.Join(dir, "none.conf")
	if err := writeResolvConf(noDNS, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(noDNS)
	if strings.Contains(string(data), "nameserver") {
		t.Errorf("zero-egress resolv.conf must name no resolver: %s", data)
	}
}

func TestPrivateModeCreatesPastaCompositionTopLevel(t *testing.T) {
	runtimeDir := t.TempDir()
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "svc"},
		BinPath:  "/opt/svc/svc",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + runtimeDir},
	}
	policy := finalized(t, &PackageSandbox{
		Home: "isolated",
		Net: NetPolicy{
			Mode:      "private",
			Inbound:   []string{"8080/tcp"},
			Egress:    []string{"10.0.0.0/8:443"},
			EgressSet: true,
		},
	})
	plan, err := buildSandboxPlan(p, policy, "/work", t.TempDir(), sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.pasta == nil {
		t.Fatal("top-level private mode must create the pasta composition")
	}
	if !plan.pasta.dns || !plan.pasta.egressSet || !slices.Equal(plan.pasta.inbound, []string{"8080/tcp"}) {
		t.Errorf("unexpected pasta spec: %+v", plan.pasta)
	}
	if indexSequence(plan.args, []string{"--cap-drop", "ALL"}) < 0 {
		t.Errorf("private mode must drop capabilities inside pasta's namespace: %v", plan.args)
	}
	if indexSequence(plan.args, []string{"--ro-bind", resolvConfPath("svc"), resolvConfBindTarget()}) < 0 {
		t.Errorf("private mode must pin the resolver: %v", plan.args)
	}
	if plan.context.NetMode != "private" || plan.context.Inbound == nil || plan.context.Egress == nil {
		t.Errorf("context must carry the effective network policy: %+v", plan.context)
	}
	if slices.Contains(plan.args, "--unshare-net") {
		t.Errorf("pasta owns the namespace; bwrap must not unshare again: %v", plan.args)
	}
}

func TestPrivateModeZeroEgressDisablesDNS(t *testing.T) {
	p := &Prepared{
		Manifest: &manifest.Manifest{ID: "svc"},
		BinPath:  "/opt/svc/svc",
		Vars:     map[string]string{"data": t.TempDir()},
		Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
	}
	policy := finalized(t, &PackageSandbox{
		Home: "isolated",
		Net:  NetPolicy{Mode: "private", Egress: []string{}, EgressSet: true},
	})
	plan, err := buildSandboxPlan(p, policy, "/work", t.TempDir(), sandboxContext{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.pasta == nil || plan.pasta.dns {
		t.Fatalf("explicit empty egress must not permit DNS: %+v", plan.pasta)
	}
}

func TestNestedNetworkClamping(t *testing.T) {
	newPrepared := func() *Prepared {
		return &Prepared{
			Manifest: &manifest.Manifest{ID: "child"},
			BinPath:  "/opt/child/child",
			Vars:     map[string]string{"data": t.TempDir()},
			Env:      []string{"XDG_RUNTIME_DIR=" + t.TempDir()},
		}
	}
	inbound := []string{"80/tcp"}
	privateParent := sandboxContext{
		Packages: []string{"parent"}, HostHome: t.TempDir(),
		NetMode: "private", Inbound: &inbound,
	}

	// Child of private requesting none adds its own empty namespace.
	plan, err := buildSandboxPlan(newPrepared(), finalized(t, &PackageSandbox{
		Home: "isolated", Net: NetPolicy{Mode: "none"},
	}), "/work", "", privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.needsLayer || !slices.Contains(plan.args, "--unshare-net") {
		t.Errorf("private->none child must unshare: %+v", plan.args)
	}

	// Child of private with an absent list inherits the parent namespace.
	plan, err = buildSandboxPlan(newPrepared(), finalized(t, &PackageSandbox{
		Home: "isolated", Net: NetPolicy{Mode: "private"},
	}), "/work", "", privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.needsLayer || plan.pasta != nil {
		t.Errorf("same private policy must inherit, not stack pasta: %+v", plan)
	}
	if plan.context.Inbound == nil || !slices.Equal(*plan.context.Inbound, inbound) {
		t.Errorf("child context must inherit the parent's effective inbound: %+v", plan.context)
	}

	// A broader child list is clamped to the parent's, silently.
	plan, err = buildSandboxPlan(newPrepared(), finalized(t, &PackageSandbox{
		Home: "isolated",
		Net:  NetPolicy{Mode: "private", Inbound: []string{"80/tcp", "443/tcp"}, InboundSet: true},
	}), "/work", "", privateParent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.context.Inbound == nil || !slices.Equal(*plan.context.Inbound, inbound) {
		t.Errorf("broader child list must clamp to parent: %+v", plan.context)
	}

	// A narrower child list is an error: it cannot be enforced now.
	_, err = buildSandboxPlan(newPrepared(), finalized(t, &PackageSandbox{
		Home: "isolated",
		Net:  NetPolicy{Mode: "private", Inbound: []string{"443/tcp"}, InboundSet: true},
	}), "/work", "", privateParent)
	if err == nil || !strings.Contains(err.Error(), "narrow") {
		t.Errorf("narrower inbound must error, got %v", err)
	}

	// A semantically broader child range clamps rather than erroring, even
	// though it shares no literal entry with the parent's "80/tcp".
	plan, err = buildSandboxPlan(newPrepared(), finalized(t, &PackageSandbox{
		Home: "isolated",
		Net:  NetPolicy{Mode: "private", Inbound: []string{"1-65535/tcp"}, InboundSet: true},
	}), "/work", "", privateParent)
	if err != nil {
		t.Fatalf("a broader range must clamp, not error: %v", err)
	}
	if plan.context.Inbound == nil || !slices.Equal(*plan.context.Inbound, inbound) {
		t.Errorf("broader range must clamp to the parent: %+v", plan.context)
	}

	// Adding egress filtering to an unrestricted inherited namespace is the
	// same unenforceable narrowing.
	_, err = buildSandboxPlan(newPrepared(), finalized(t, &PackageSandbox{
		Home: "isolated",
		Net:  NetPolicy{Mode: "private", Egress: []string{"10.0.0.0/8"}, EgressSet: true},
	}), "/work", "", privateParent)
	if err == nil || !strings.Contains(err.Error(), "narrow") {
		t.Errorf("late egress filtering must error, got %v", err)
	}

	// A child of none inherits the empty network regardless of its own lists.
	noneParent := sandboxContext{Packages: []string{"parent"}, HostHome: t.TempDir(), NetMode: "none"}
	plan, err = buildSandboxPlan(newPrepared(), finalized(t, &PackageSandbox{
		Home: "isolated", Net: NetPolicy{Mode: "private", Inbound: []string{"80"}, InboundSet: true},
	}), "/work", "", noneParent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.context.NetMode != "none" || plan.pasta != nil || slices.Contains(plan.args, "--unshare-net") {
		t.Errorf("child of none cannot widen: %+v", plan)
	}
}
