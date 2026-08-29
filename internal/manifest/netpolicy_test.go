package manifest

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseInboundRule(t *testing.T) {
	for _, tc := range []struct {
		in       string
		lo, hi   int
		tcp, udp bool
	}{
		{"80", 80, 80, true, true},
		{"8000-8100", 8000, 8100, true, true},
		{"443/tcp", 443, 443, true, false},
		{"53/udp", 53, 53, false, true},
		{"1000-1010/tcp", 1000, 1010, true, false},
	} {
		rule, err := ParseInboundRule(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if rule.Ports.Lo != tc.lo || rule.Ports.Hi != tc.hi || rule.TCP != tc.tcp || rule.UDP != tc.udp {
			t.Errorf("%q parsed as %+v", tc.in, rule)
		}
	}
	for _, bad := range []string{"", "0", "65536", "80-70", "80/icmp", "http", "-80"} {
		if _, err := ParseInboundRule(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestParseEgressRule(t *testing.T) {
	for _, tc := range []struct {
		in     string
		prefix string
		ports  string // "" means all
		proto  string
	}{
		{"10.0.0.0/8:443", "10.0.0.0/8", "443", ""},
		{"192.168.1.10:5432", "192.168.1.10/32", "5432", ""},
		{"10.0.0.0/8", "10.0.0.0/8", "", ""},
		{"1.2.3.4:80-90/tcp", "1.2.3.4/32", "80-90", "tcp"},
		{"1.2.3.4/udp", "1.2.3.4/32", "", "udp"},
		{"[2001:db8::1]:443", "2001:db8::1/128", "443", ""},
		{"2001:db8::/32", "2001:db8::/32", "", ""},
	} {
		rule, err := ParseEgressRule(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if rule.Prefix.String() != tc.prefix || rule.Proto != tc.proto {
			t.Errorf("%q parsed as %+v", tc.in, rule)
		}
		ports := ""
		if rule.Ports != nil {
			ports = rule.Ports.String()
		}
		if ports != tc.ports {
			t.Errorf("%q ports = %q, want %q", tc.in, ports, tc.ports)
		}
	}
}

func TestParseEgressRuleRejectsHostnames(t *testing.T) {
	for _, bad := range []string{"example.com", "example.com:443", "api.internal/tcp"} {
		_, err := ParseEgressRule(bad)
		if err == nil {
			t.Errorf("%q should not parse", bad)
			continue
		}
		if !strings.Contains(err.Error(), "address-based") {
			t.Errorf("%q error should point at address-based filtering: %v", bad, err)
		}
	}
	if _, err := ParseEgressRule("10.0.0.0/33"); err == nil {
		t.Error("invalid mask length should not parse")
	}
}

func stringsPtr(v ...string) *[]string { return &v }

func TestValidateSandboxPolicyBoundaryAndFS(t *testing.T) {
	for name, bad := range map[string]*SandboxPolicy{
		"invalid boundary":    {Boundary: "vm"},
		"hardened shared":     {Boundary: "hardened", Home: "shared"},
		"scoped fs":           {Boundary: "scoped", FS: &SandboxFS{Cwd: "read"}},
		"invalid cwd":         {Boundary: "hardened", FS: &SandboxFS{Cwd: "rw"}},
		"empty fs read entry": {Boundary: "hardened", FS: &SandboxFS{Read: stringsPtr("")}},
	} {
		if err := ValidateSandboxPolicy("sandbox", bad); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	good := &SandboxPolicy{Boundary: "hardened", FS: &SandboxFS{
		Read: stringsPtr("~/Projects"), Write: stringsPtr(), Cwd: "hidden",
	}}
	if err := ValidateSandboxPolicy("sandbox", good); err != nil {
		t.Errorf("valid hardened policy rejected: %v", err)
	}
}

func TestValidateSandboxPolicyNet(t *testing.T) {
	for name, bad := range map[string]*SandboxPolicy{
		"invalid mode":    {Net: &SandboxNet{Mode: "vpn"}},
		"inbound on host": {Net: &SandboxNet{Mode: "host", Inbound: stringsPtr("80")}},
		"egress on host":  {Net: &SandboxNet{Mode: "host", Egress: stringsPtr("10.0.0.0/8")}},
		"inbound on none": {Net: &SandboxNet{Mode: "none", Inbound: stringsPtr("80")}},
		"egress on none":  {Net: &SandboxNet{Mode: "none", Egress: stringsPtr("10.0.0.0/8")}},
		"bad inbound":     {Net: &SandboxNet{Mode: "private", Inbound: stringsPtr("http")}},
		"hostname egress": {Net: &SandboxNet{Mode: "private", Egress: stringsPtr("example.com:443")}},
		// features.network is not a feature toggle: network policy has one
		// spelling (net) so there are no two forms left to disagree.
		"network as a feature":          {Features: map[string]bool{"network": true}},
		"network as a feature with net": {Features: map[string]bool{"network": false}, Net: &SandboxNet{Mode: "none"}},
	} {
		if err := ValidateSandboxPolicy("sandbox", bad); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	for name, good := range map[string]*SandboxPolicy{
		"private lists": {Net: &SandboxNet{
			Mode: "private", Inbound: stringsPtr("8080/tcp"), Egress: stringsPtr("10.0.0.0/8:443"),
		}},
		"empty lists deny": {Net: &SandboxNet{Mode: "private", Inbound: stringsPtr(), Egress: stringsPtr()}},
	} {
		if err := ValidateSandboxPolicy("sandbox", good); err != nil {
			t.Errorf("%s: valid policy rejected: %v", name, err)
		}
	}
}

// `net: none` and `net: {mode: none}` are two syntaxes for one field, so the
// common on/off case does not need a nested mapping.
func TestSandboxNetAcceptsScalarOrMapping(t *testing.T) {
	for _, body := range []string{"net: none\n", "net:\n  mode: none\n"} {
		var policy SandboxPolicy
		if err := yaml.Unmarshal([]byte(body), &policy); err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if policy.Net == nil || policy.Net.Mode != "none" {
			t.Errorf("%q: got %+v, want mode none", body, policy.Net)
		}
	}
	// The mapping form still carries the private-mode lists.
	var policy SandboxPolicy
	if err := yaml.Unmarshal([]byte("net:\n  mode: private\n  inbound: [8080/tcp]\n"), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Net.Mode != "private" || policy.Net.Inbound == nil || (*policy.Net.Inbound)[0] != "8080/tcp" {
		t.Errorf("mapping form lost its lists: %+v", policy.Net)
	}
	// A bogus mode is still rejected, by validation rather than at decode.
	var bogus SandboxPolicy
	if err := yaml.Unmarshal([]byte("net: sideways\n"), &bogus); err != nil {
		t.Fatalf("scalar should decode, validation reports the bad mode: %v", err)
	}
	if err := ValidateSandboxPolicy("sandbox", &bogus); err == nil {
		t.Error("an unknown net mode must be rejected")
	}
}
