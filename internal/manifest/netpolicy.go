package manifest

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// PortRange is an inclusive port span; Lo == Hi for a single port.
type PortRange struct {
	Lo, Hi int
}

func (r PortRange) String() string {
	if r.Lo == r.Hi {
		return strconv.Itoa(r.Lo)
	}
	return fmt.Sprintf("%d-%d", r.Lo, r.Hi)
}

// InboundRule is one parsed `inbound` entry: the ports a private-network
// package may be reached on. Without an explicit protocol suffix the rule
// applies to both TCP and UDP.
type InboundRule struct {
	Ports PortRange
	TCP   bool
	UDP   bool
}

// ParseInboundRule parses `port[-port][/tcp|/udp]`.
func ParseInboundRule(s string) (InboundRule, error) {
	rule := InboundRule{TCP: true, UDP: true}
	ports := s
	if body, proto, ok := cutProtoSuffix(s); ok {
		ports = body
		rule.TCP = proto == "tcp"
		rule.UDP = proto == "udp"
	}
	span, err := parsePortRange(ports)
	if err != nil {
		return InboundRule{}, fmt.Errorf("invalid inbound entry %q: %w", s, err)
	}
	rule.Ports = span
	return rule, nil
}

// EgressRule is one parsed `egress` entry: a destination a private-network
// package may open connections to. Proto is "" (both), "tcp", or "udp";
// Ports is nil when every port is allowed.
type EgressRule struct {
	Prefix netip.Prefix
	Ports  *PortRange
	Proto  string
}

// ParseEgressRule parses `CIDR[:port[-port]][/proto]`. A bare address means a
// single-host prefix; an IPv6 address takes brackets when a port is given.
// Names are rejected: address-based filtering is the only honest offer, so
// there is no hostname form to fall back to.
func ParseEgressRule(s string) (EgressRule, error) {
	rule := EgressRule{}
	rest := s
	if body, proto, ok := cutProtoSuffix(rest); ok {
		rest = body
		rule.Proto = proto
	}

	host := rest
	if ports, ok := splitHostPorts(&host); ok {
		span, err := parsePortRange(ports)
		if err != nil {
			return EgressRule{}, fmt.Errorf("invalid egress entry %q: %w", s, err)
		}
		rule.Ports = &span
	}

	prefix, err := parseHostPrefix(host)
	if err != nil {
		return EgressRule{}, fmt.Errorf("invalid egress entry %q: %w", s, err)
	}
	rule.Prefix = prefix
	return rule, nil
}

// cutProtoSuffix strips a trailing /tcp or /udp. Only those two words are
// protocols; a trailing /8 stays part of the CIDR.
func cutProtoSuffix(s string) (body, proto string, ok bool) {
	for _, p := range []string{"tcp", "udp"} {
		if strings.HasSuffix(s, "/"+p) {
			return s[:len(s)-len(p)-1], p, true
		}
	}
	return s, "", false
}

// splitHostPorts splits a trailing :port[-port] off *host when present. IPv6
// addresses carry colons of their own, so a port on an IPv6 host requires the
// bracketed [addr]:port form; unbracketed colon-bearing hosts are left whole.
func splitHostPorts(host *string) (ports string, ok bool) {
	s := *host
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return "", false
	}
	head, tail := s[:i], s[i+1:]
	if strings.Contains(head, ":") && !strings.HasSuffix(head, "]") {
		return "", false // unbracketed IPv6, the colon belongs to the address
	}
	if _, err := parsePortRange(tail); err != nil {
		return "", false // not a port; let the address parser report it
	}
	*host = head
	return tail, true
}

func parseHostPrefix(host string) (netip.Prefix, error) {
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if strings.ContainsRune(host, '/') {
		prefix, err := netip.ParsePrefix(host)
		if err != nil {
			return netip.Prefix{}, addrError(host, err)
		}
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Prefix{}, addrError(host, err)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// addrError points hostname-shaped input at the design decision instead of a
// bare parse failure: name-based egress filtering is rejected, not deferred.
func addrError(host string, err error) error {
	letter := func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }
	if strings.ContainsFunc(host, letter) && !strings.ContainsRune(host, ':') {
		return fmt.Errorf("hostnames are not accepted: egress filtering is address-based (CIDR) because name-based filtering cannot be enforced")
	}
	return err
}

func parsePortRange(s string) (PortRange, error) {
	lo, hi, isRange := strings.Cut(s, "-")
	span := PortRange{}
	var err error
	if span.Lo, err = parsePort(lo); err != nil {
		return PortRange{}, err
	}
	span.Hi = span.Lo
	if isRange {
		if span.Hi, err = parsePort(hi); err != nil {
			return PortRange{}, err
		}
		if span.Hi < span.Lo {
			return PortRange{}, fmt.Errorf("port range %s is reversed", s)
		}
	}
	return span, nil
}

func parsePort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be 1..65535, got %q", s)
	}
	return port, nil
}

// InboundCovers reports whether the child inbound allowlist permits at least
// everything the parent's does — the "same or broader" test a nested private
// network must pass, compared on parsed port ranges and protocols rather than
// textual entries (so 1-65535/tcp covers 80-90/tcp). Coverage is per parent
// rule: a parent rule split across several child rules is conservatively
// treated as uncovered, erring toward the safe "cannot narrow" result. An
// unparseable entry is likewise treated as uncovered.
func InboundCovers(child, parent []string) bool {
	c, err := parseInboundRules(child)
	if err != nil {
		return false
	}
	p, err := parseInboundRules(parent)
	if err != nil {
		return false
	}
	for _, pr := range p {
		if pr.TCP && !inboundPortCovered(c, true, pr.Ports) {
			return false
		}
		if pr.UDP && !inboundPortCovered(c, false, pr.Ports) {
			return false
		}
	}
	return true
}

func inboundPortCovered(child []InboundRule, tcp bool, ports PortRange) bool {
	for _, cr := range child {
		enabled := cr.UDP
		if tcp {
			enabled = cr.TCP
		}
		if enabled && cr.Ports.Lo <= ports.Lo && cr.Ports.Hi >= ports.Hi {
			return true
		}
	}
	return false
}

// EgressCovers reports whether the child egress allowlist permits at least
// everything the parent's does, compared on CIDR containment, port ranges,
// and protocols (so 0.0.0.0/0 covers 10.0.0.0/8). Same per-rule conservatism
// as InboundCovers.
func EgressCovers(child, parent []string) bool {
	c, err := parseEgressRules(child)
	if err != nil {
		return false
	}
	p, err := parseEgressRules(parent)
	if err != nil {
		return false
	}
	for _, pr := range p {
		if !slices.ContainsFunc(c, func(cr EgressRule) bool { return egressRuleCovers(cr, pr) }) {
			return false
		}
	}
	return true
}

// egressRuleCovers reports whether child rule c permits everything parent rule
// p does: same address family, c's prefix contains p's block, c's protocol is
// unrestricted or equal, and c's port range (if any) spans p's.
func egressRuleCovers(c, p EgressRule) bool {
	if c.Prefix.Addr().Is4() != p.Prefix.Addr().Is4() {
		return false
	}
	if c.Prefix.Bits() > p.Prefix.Bits() || !c.Prefix.Contains(p.Prefix.Addr()) {
		return false
	}
	if c.Proto != "" && c.Proto != p.Proto {
		return false
	}
	if c.Ports != nil {
		if p.Ports == nil || c.Ports.Lo > p.Ports.Lo || c.Ports.Hi < p.Ports.Hi {
			return false
		}
	}
	return true
}

func parseInboundRules(raw []string) ([]InboundRule, error) {
	out := make([]InboundRule, 0, len(raw))
	for _, entry := range raw {
		rule, err := ParseInboundRule(entry)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

func parseEgressRules(raw []string) ([]EgressRule, error) {
	out := make([]EgressRule, 0, len(raw))
	for _, entry := range raw {
		rule, err := ParseEgressRule(entry)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}
