package smtp

import (
	"context"
	"testing"
	"time"
)

func TestParseMTASTSPolicy(t *testing.T) {
	body := "version: STSv1\nmode: enforce\nmx: mail.example.com\nmx: *.example.net\nmax_age: 86400\n"
	p, err := parseMTASTSPolicy(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Mode != "enforce" {
		t.Errorf("mode = %q, want enforce", p.Mode)
	}
	if len(p.MX) != 2 || p.MX[0] != "mail.example.com" {
		t.Errorf("mx = %v", p.MX)
	}
	if p.MaxAge != 86400 {
		t.Errorf("max_age = %d, want 86400", p.MaxAge)
	}
}

func TestParseMTASTSPolicy_Invalid(t *testing.T) {
	for _, body := range []string{
		"mode: enforce\nmx: a\n",             // no version
		"version: STSv1\nmode: bogus\nmx: a", // bad mode
	} {
		if _, err := parseMTASTSPolicy(body); err == nil {
			t.Errorf("expected error for %q", body)
		}
	}
}

func TestMXMatches(t *testing.T) {
	p := &MTASTSPolicy{MX: []string{"mail.example.com", "*.example.net"}}
	cases := []struct {
		host string
		want bool
	}{
		{"mail.example.com", true},
		{"MAIL.EXAMPLE.COM", true},   // case-insensitive
		{"mail.example.com.", true},  // trailing dot
		{"other.example.com", false}, // not listed, no wildcard for .com
		{"a.example.net", true},      // wildcard, one label
		{"a.b.example.net", false},   // wildcard matches only one leftmost label
		{"example.net", false},       // wildcard needs a label
	}
	for _, tc := range cases {
		if got := p.MXMatches(tc.host); got != tc.want {
			t.Errorf("MXMatches(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestMTASTSCache_NoRecordReturnsNil(t *testing.T) {
	c := NewMTASTSCache()
	c.lookupTXT = func(context.Context, string) ([]string, error) { return nil, nil }
	got, err := c.PolicyFor(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("PolicyFor: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil policy for a domain with no MTA-STS record")
	}
}

func TestMTASTSCache_FetchCacheAndInvalidate(t *testing.T) {
	fetches := 0
	id := "20260101T000000"
	c := NewMTASTSCache()
	c.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	c.lookupTXT = func(_ context.Context, name string) ([]string, error) {
		return []string{"v=STSv1; id=" + id + ";"}, nil
	}
	c.fetch = func(context.Context, string) (string, error) {
		fetches++
		return "version: STSv1\nmode: enforce\nmx: mail.example.com\nmax_age: 86400\n", nil
	}

	p, err := c.PolicyFor(context.Background(), "example.com")
	if err != nil || p == nil || p.Mode != "enforce" {
		t.Fatalf("first PolicyFor: p=%v err=%v", p, err)
	}
	// Second call within TTL and same id -> served from cache, no re-fetch.
	if _, err := c.PolicyFor(context.Background(), "example.com"); err != nil {
		t.Fatalf("second PolicyFor: %v", err)
	}
	if fetches != 1 {
		t.Errorf("fetches = %d, want 1 (cached)", fetches)
	}

	// Policy id changes -> cache invalidated, re-fetch.
	id = "20260202T000000"
	if _, err := c.PolicyFor(context.Background(), "example.com"); err != nil {
		t.Fatalf("third PolicyFor: %v", err)
	}
	if fetches != 2 {
		t.Errorf("fetches = %d, want 2 (id changed -> refetch)", fetches)
	}
}
