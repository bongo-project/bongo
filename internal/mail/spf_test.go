package mail

import (
	"context"
	"net"
	"testing"
)

type mockResolver struct {
	txt map[string][]string
	ips map[string][]net.IP
	mx  map[string][]*net.MX
}

func (m *mockResolver) LookupTXT(_ context.Context, domain string) ([]string, error) {
	if records, ok := m.txt[domain]; ok {
		return records, nil
	}
	return nil, nil
}

func (m *mockResolver) LookupIP(_ context.Context, host string) ([]net.IP, error) {
	if ips, ok := m.ips[host]; ok {
		return ips, nil
	}
	return nil, nil
}

func (m *mockResolver) LookupMX(_ context.Context, domain string) ([]*net.MX, error) {
	if records, ok := m.mx[domain]; ok {
		return records, nil
	}
	return nil, nil
}

func TestSPFIP4Pass(t *testing.T) {
	resolver := &mockResolver{
		txt: map[string][]string{
			"example.com": {"v=spf1 ip4:192.168.1.0/24 -all"},
		},
	}
	checker := NewSPFCheckerWithResolver(resolver)

	result, err := checker.Check(context.Background(), net.ParseIP("192.168.1.100"), "example.com")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result != "pass" {
		t.Errorf("result = %q, want pass", result)
	}
}

func TestSPFIP4Fail(t *testing.T) {
	resolver := &mockResolver{
		txt: map[string][]string{
			"example.com": {"v=spf1 ip4:192.168.1.0/24 -all"},
		},
	}
	checker := NewSPFCheckerWithResolver(resolver)

	result, err := checker.Check(context.Background(), net.ParseIP("10.0.0.1"), "example.com")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result != "fail" {
		t.Errorf("result = %q, want fail", result)
	}
}

func TestSPFMXPass(t *testing.T) {
	resolver := &mockResolver{
		txt: map[string][]string{
			"example.com": {"v=spf1 mx -all"},
		},
		mx: map[string][]*net.MX{
			"example.com": {{Host: "mail.example.com.", Pref: 10}},
		},
		ips: map[string][]net.IP{
			"mail.example.com": {net.ParseIP("203.0.113.5")},
		},
	}
	checker := NewSPFCheckerWithResolver(resolver)

	result, err := checker.Check(context.Background(), net.ParseIP("203.0.113.5"), "example.com")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result != "pass" {
		t.Errorf("result = %q, want pass", result)
	}
}

func TestSPFNoRecord(t *testing.T) {
	resolver := &mockResolver{
		txt: map[string][]string{},
	}
	checker := NewSPFCheckerWithResolver(resolver)

	result, err := checker.Check(context.Background(), net.ParseIP("10.0.0.1"), "example.com")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result != "none" {
		t.Errorf("result = %q, want none", result)
	}
}

func TestSPFSoftfail(t *testing.T) {
	resolver := &mockResolver{
		txt: map[string][]string{
			"example.com": {"v=spf1 ip4:10.0.0.0/8 ~all"},
		},
	}
	checker := NewSPFCheckerWithResolver(resolver)

	result, err := checker.Check(context.Background(), net.ParseIP("192.168.1.1"), "example.com")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result != "softfail" {
		t.Errorf("result = %q, want softfail", result)
	}
}

// countingResolver wraps a mockResolver and counts TXT lookups so tests can
// assert the RFC 7208 DNS-lookup budget is enforced.
type countingResolver struct {
	*mockResolver
	txtLookups int
}

func (c *countingResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	c.txtLookups++
	return c.mockResolver.LookupTXT(ctx, domain)
}

// TestSPFIncludeLoopIsPermerror ensures a self-referential include tree does not
// recurse forever and resolves to permerror.
func TestSPFIncludeLoopIsPermerror(t *testing.T) {
	resolver := &mockResolver{
		txt: map[string][]string{
			"a.example": {"v=spf1 include:b.example -all"},
			"b.example": {"v=spf1 include:a.example -all"},
		},
	}
	checker := NewSPFCheckerWithResolver(resolver)

	result, err := checker.Check(context.Background(), net.ParseIP("192.168.1.1"), "a.example")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result != "permerror" {
		t.Errorf("result = %q, want permerror", result)
	}
}

// TestSPFExceedsLookupLimit ensures a deep include chain is capped at
// maxSPFDNSLookups DNS-querying mechanisms and returns permerror.
func TestSPFExceedsLookupLimit(t *testing.T) {
	txt := map[string][]string{}
	// Build a 20-deep include chain: each domain includes the next.
	for i := 0; i < 20; i++ {
		txt[spfChainDomain(i)] = []string{"v=spf1 include:" + spfChainDomain(i+1) + " -all"}
	}
	txt[spfChainDomain(20)] = []string{"v=spf1 -all"}

	base := &countingResolver{mockResolver: &mockResolver{txt: txt}}
	checker := NewSPFCheckerWithResolver(base)

	result, err := checker.Check(context.Background(), net.ParseIP("192.168.1.1"), spfChainDomain(0))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if result != "permerror" {
		t.Errorf("result = %q, want permerror", result)
	}
	// Root TXT (not charged) + at most maxSPFDNSLookups charged include lookups.
	if base.txtLookups > maxSPFDNSLookups+1 {
		t.Errorf("txtLookups = %d, want <= %d (budget not enforced)", base.txtLookups, maxSPFDNSLookups+1)
	}
}

func spfChainDomain(i int) string {
	return "d" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".example"
}
