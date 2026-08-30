package manifest

import (
	"fmt"
	"net/netip"
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
