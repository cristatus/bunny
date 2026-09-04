package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/charmbracelet/log"

	"github.com/cristatus/bunny/internal/fsutil"
	"github.com/cristatus/bunny/internal/manifest"
)

// pastaSpec is the private-network composition this launch must establish:
// pasta outside, bubblewrap inside. dns is false only for an explicit empty
// egress list, which promises zero egress and therefore gets no resolver and
// no DNS exception.
type pastaSpec struct {
	inbound    []string
	egress     []string
	egressSet  bool
	dns        bool
	resolvPath string
}

// dnsForwardAddr is the address pasta maps to the host's resolver inside the
// namespace. TEST-NET-1 space: never globally routed, but shaped so it
// travels the default route through pasta instead of bypassing it the way a
// link-local address could.
const dnsForwardAddr = "192.0.2.53"

// FindPasta locates the pasta binary (shipped in the passt package). A policy
// that selects private networking fails closed with this install hint; it
// never falls back to host networking.
func FindPasta() (string, error) {
	return findTool("pasta", "required for net.mode: private", "passt", "passt")
}

// FindNft locates the nftables client used to install the egress ruleset.
func FindNft() (string, error) {
	return findTool("nft", "required for sandbox egress filtering", "nftables", "nftables")
}

// pastaArgs builds the pasta invocation. -T none -U none matter: their
// default is auto, which would re-expose listening host services inside the
// sandbox; --no-map-gw denies the host's loopback services through the
// gateway address.
func pastaArgs(spec *pastaSpec) ([]string, error) {
	tcp, udp, err := inboundPortLists(spec.inbound)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-q", "--config-net",
		"-t", tcp, "-u", udp,
		"-T", "none", "-U", "none",
		"--no-map-gw",
	}
	if spec.dns {
		args = append(args, "--dns-forward", dnsForwardAddr)
	}
	return args, nil
}

// inboundPortLists renders the parsed inbound rules as pasta -t/-u values;
// "none" means no mapping at all.
func inboundPortLists(entries []string) (tcp, udp string, err error) {
	var tcpPorts, udpPorts []string
	for _, entry := range entries {
		rule, err := manifest.ParseInboundRule(entry)
		if err != nil {
			return "", "", err
		}
		if rule.TCP {
			tcpPorts = append(tcpPorts, rule.Ports.String())
		}
		if rule.UDP {
			udpPorts = append(udpPorts, rule.Ports.String())
		}
	}
	format := func(ports []string) string {
		if len(ports) == 0 {
			return "none"
		}
		return strings.Join(ports, ",")
	}
	return format(tcpPorts), format(udpPorts), nil
}

// nftRuleset renders the egress allowlist as a default-drop output chain.
// Loopback and established return traffic stay allowed; the DNS forwarder
// exception is a documented part of the policy, present only when the
// allowlist is non-empty (dns true).
func nftRuleset(egress []string, dns bool) (string, error) {
	var b strings.Builder
	b.WriteString("table inet bunny {\n")
	b.WriteString("  chain output {\n")
	b.WriteString("    type filter hook output priority filter; policy drop;\n")
	b.WriteString("    oifname \"lo\" accept\n")
	b.WriteString("    ct state established,related accept\n")
	if dns {
		fmt.Fprintf(&b, "    ip daddr %s udp dport 53 accept\n", dnsForwardAddr)
		fmt.Fprintf(&b, "    ip daddr %s tcp dport 53 accept\n", dnsForwardAddr)
	}
	for _, entry := range egress {
		rule, err := manifest.ParseEgressRule(entry)
		if err != nil {
			return "", err
		}
		b.WriteString("    " + nftRuleLine(rule) + "\n")
	}
	b.WriteString("  }\n}\n")
	return b.String(), nil
}

func nftRuleLine(rule manifest.EgressRule) string {
	family := "ip"
	if rule.Prefix.Addr().Is6() {
		family = "ip6"
	}
	parts := []string{fmt.Sprintf("%s daddr %s", family, rule.Prefix)}
	switch {
	case rule.Ports != nil && rule.Proto != "":
		parts = append(parts, fmt.Sprintf("%s dport %s", rule.Proto, rule.Ports))
	case rule.Ports != nil:
		parts = append(parts, fmt.Sprintf("meta l4proto { tcp, udp } th dport %s", rule.Ports))
	case rule.Proto != "":
		parts = append(parts, "meta l4proto "+rule.Proto)
	}
	parts = append(parts, "accept")
	return strings.Join(parts, " ")
}

// runtimeStateDir is Bunny's own directory under the user runtime dir, used
// for the mounted context, generated resolver files, egress rulesets, and
// proxy sockets.
func runtimeStateDir() string {
	return filepath.Dir(sandboxContextFile)
}

func ensureRuntimeStateDir() (string, error) {
	dir := runtimeStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create sandbox runtime directory: %w", err)
	}
	return dir, nil
}

var uniqueStateCounter atomic.Uint64

// uniqueRuntimePath gives launch-scoped helpers a pathname without creating
// it during planning. Randomness prevents collisions across Bunny processes;
// the counter is only a fallback for systems whose random source is broken.
func uniqueRuntimePath(kind, id, ext string) string {
	// Keep AF_UNIX users (notably the D-Bus proxy) below sockaddr_un's short
	// path limit even when a manifest uses the maximum-length package ID.
	if len(id) > 32 {
		id = id[:32]
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err == nil {
		return filepath.Join(runtimeStateDir(), kind+"-"+id+"-"+hex.EncodeToString(random[:])+ext)
	}
	return filepath.Join(runtimeStateDir(), fmt.Sprintf("%s-%s-%d-%d%s", kind, id, os.Getpid(), uniqueStateCounter.Add(1), ext))
}

// resolvConfBindTarget resolves where /etc/resolv.conf actually lives.
// Binding over the symlink itself fails when its target sits in a directory
// bubblewrap does not own (systemd-resolved's /run/systemd/resolve), so the
// generated file is bound over the final target instead — which, with the
// resolver directory masked first, lands inside a bwrap-owned tmpfs.
func resolvConfBindTarget() string {
	const path = "/etc/resolv.conf"
	if target, err := filepath.EvalSymlinks(path); err == nil {
		return target
	}
	if link, err := os.Readlink(path); err == nil {
		if !filepath.IsAbs(link) {
			link = filepath.Join("/etc", link)
		}
		return filepath.Clean(link)
	}
	return path
}

// writeResolvConf generates the pinned resolver configuration bound over
// /etc/resolv.conf in private mode. With dns false (an explicit empty egress
// list) it names no nameserver at all, preserving the promise of zero egress.
func writeResolvConf(path string, dns bool) error {
	content := "# generated by bunny sandbox: egress policy permits no DNS\n"
	if dns {
		content = "# generated by bunny sandbox\nnameserver " + dnsForwardAddr + "\n"
	}
	// fsutil.WriteFile creates the target's parent directory; no need to
	// touch the global runtime state dir, which keeps this testable off /run.
	if err := fsutil.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write sandbox resolv.conf: %w", err)
	}
	return nil
}

// execUnderPasta composes pasta outside, bubblewrap inside. With an egress
// allowlist the process re-enters itself (`bunny netsetup`) inside pasta's
// namespace, where it still holds full capabilities, installs the nftables
// ruleset, and only then drops into bubblewrap.
func execUnderPasta(p *Prepared, plan sandboxPlan, bwrapArgv []string) error {
	pastaPath, err := FindPasta()
	if err != nil {
		return err
	}
	args, err := pastaArgs(plan.pasta)
	if err != nil {
		return err
	}
	if err := writeResolvConf(plan.pasta.resolvPath, plan.pasta.dns); err != nil {
		return err
	}

	inner := bwrapArgv
	if plan.pasta.egressSet {
		// Resolve nft here, in the trusted parent, and pass the absolute path
		// to netsetup: the re-exec runs inside pasta and must not resolve a
		// helper through any package-influenced PATH.
		nftPath, err := FindNft()
		if err != nil {
			return err
		}
		ruleset, err := nftRuleset(plan.pasta.egress, plan.pasta.dns)
		if err != nil {
			return err
		}
		rulesPath := uniqueRuntimePath("egress", p.Manifest.ID, ".nft")
		if err := fsutil.WriteFile(rulesPath, []byte(ruleset), 0o644); err != nil {
			return fmt.Errorf("write sandbox egress ruleset: %w", err)
		}
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve bunny executable for netsetup: %w", err)
		}
		inner = append([]string{self, "netsetup", "--rules", rulesPath, "--nft", nftPath, "--"}, bwrapArgv...)
	}

	argv := append([]string{pastaPath}, args...)
	argv = append(argv, "--")
	argv = append(argv, inner...)
	// pasta and the inner bubblewrap run with a trusted, loader-sanitized
	// environment; the payload's own environment is delivered by bwrap
	// --clearenv/--setenv baked into bwrapArgv.
	log.Debug("pasta exec", "argv", strings.Join(argv, " "))
	return syscall.Exec(pastaPath, argv, trustedHelperEnv())
}

// ApplyNetsetup is the inside-the-namespace half of the egress composition:
// install the ruleset while capabilities remain, then exec the sandbox
// command line. Run by the hidden `bunny netsetup` command. nftPath is
// resolved by the trusted parent and passed in so this re-exec never resolves
// a helper through a package-influenced PATH.
func ApplyNetsetup(rulesPath, nftPath string, argv []string) error {
	// kong's passthrough hands the explicit "--" separator through verbatim.
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return fmt.Errorf("netsetup: no command to exec")
	}
	if nftPath == "" {
		var err error
		if nftPath, err = FindNft(); err != nil {
			return err
		}
	}
	out, err := exec.Command(nftPath, "-f", rulesPath).CombinedOutput()
	_ = os.Remove(rulesPath)
	if err != nil {
		return fmt.Errorf("install egress ruleset: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return syscall.Exec(argv[0], argv, os.Environ())
}
