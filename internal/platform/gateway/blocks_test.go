package gateway

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeBlockCIDR(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"203.0.113.7", "203.0.113.7/32", true},
		{" 203.0.113.0/24 ", "203.0.113.0/24", true},
		{"203.0.113.99/24", "203.0.113.0/24", true}, // canonicalised to the network
		{"2001:db8::1", "2001:db8::1/128", true},
		{"2001:db8::/48", "2001:db8::/48", true},
		{"0.0.0.0/0", "", false},  // too wide
		{"10.0.0.0/7", "", false}, // too wide
		{"2001:db8::/16", "", false},
		{"not-an-ip", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := NormalizeBlockCIDR(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("NormalizeBlockCIDR(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("NormalizeBlockCIDR(%q) accepted, want error", c.in)
		}
	}
}

// Blocks must render as top-priority deny routers on both entrypoints, and an
// expired block must vanish from the rule.
func TestRenderBlocklist(t *testing.T) {
	cfg := Config{Enabled: true, Mode: ModeManaged}
	routes := []Route{{
		RouteID: "aaa111", Domain: "app.example.com", TargetScheme: "http",
		TargetHost: "10.0.0.5", TargetPort: 8080, Enabled: true,
	}}
	past := time.Now().UTC().Add(-time.Hour)
	blocks := []Block{
		{BlockID: "b1", CIDR: "203.0.113.7/32"},
		{BlockID: "b2", CIDR: "198.51.100.0/24"},
		{BlockID: "b3", CIDR: "192.0.2.9/32", ExpiresAt: &past}, // expired
	}
	out, err := Render(cfg, routes, blocks)
	if err != nil {
		t.Fatal(err)
	}
	y := string(out)
	for _, want := range []string{
		"ns-blocklist-http", "ns-blocklist-https",
		"ClientIP(`203.0.113.7/32`)", "ClientIP(`198.51.100.0/24`)",
		"priority: 1000000", "255.255.255.255/32",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("rendered config missing %q\n%s", want, y)
		}
	}
	if strings.Contains(y, "192.0.2.9") {
		t.Errorf("expired block still rendered:\n%s", y)
	}

	// No routes → no file at all, blocks alone must not force one.
	out, err = Render(cfg, nil, blocks)
	if err != nil || out != nil {
		t.Errorf("blocks without routes rendered a file: %v %v", out, err)
	}
}
