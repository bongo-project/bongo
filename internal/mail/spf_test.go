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
