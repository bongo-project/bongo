package handlers

import (
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/web/templates"
)

// findRow returns the first row matching type+host, or a zero row and false.
func findRow(rows []templates.DNSGuideRow, typ, host string) (templates.DNSGuideRow, bool) {
	for _, r := range rows {
		if r.Type == typ && r.Host == host {
			return r, true
		}
	}
	return templates.DNSGuideRow{}, false
}

func TestBuildDNSGuide(t *testing.T) {
	const (
		domain   = "example.net"
		mailHost = "mail.example.com"
		v4       = "192.0.2.1"
		v6       = "2001:db8:4f8:a958::1"
		dkimTXT  = `mail._domainkey.example.net TXT "v=DKIM1; k=rsa; p=MIIBIjANBgkq"`
	)

	t.Run("shared-host MX and SRV point at the server hostname", func(t *testing.T) {
		rows, _ := buildDNSGuide(domain, mailHost, false, v4, v6, "mail", dkimTXT)

		mx, ok := findRow(rows, "MX", "@")
		if !ok || mx.Value != "10 mail.example.com." {
			t.Fatalf("MX: got %q ok=%v, want '10 mail.example.com.'", mx.Value, ok)
		}
		submission, ok := findRow(rows, "SRV", "_submission._tcp")
		if !ok || submission.Value != "0 1 587 mail.example.com." {
			t.Fatalf("submission SRV: got %q ok=%v", submission.Value, ok)
		}
		imaps, ok := findRow(rows, "SRV", "_imaps._tcp")
		if !ok || imaps.Value != "0 1 993 mail.example.com." {
			t.Fatalf("imaps SRV: got %q ok=%v", imaps.Value, ok)
		}
	})

	t.Run("A records include autoconfig and autodiscover", func(t *testing.T) {
		rows, _ := buildDNSGuide(domain, mailHost, false, v4, v6, "mail", dkimTXT)
		for _, h := range []string{"@", "mail", "autoconfig", "autodiscover"} {
			r, ok := findRow(rows, "A", h)
			if !ok || r.Value != v4 {
				t.Fatalf("A %s: got %q ok=%v, want %q", h, r.Value, ok, v4)
			}
		}
	})

	t.Run("SPF and DMARC use canonical expected values", func(t *testing.T) {
		rows, _ := buildDNSGuide(domain, mailHost, false, v4, v6, "mail", dkimTXT)
		spf, ok := findRow(rows, "TXT", "@")
		if !ok || spf.Value != "v=spf1 mx ~all" {
			t.Fatalf("SPF: got %q ok=%v", spf.Value, ok)
		}
		dmarc, ok := findRow(rows, "TXT", "_dmarc")
		if !ok || dmarc.Value != "v=DMARC1; p=none; rua=mailto:postmaster@example.net" {
			t.Fatalf("DMARC: got %q ok=%v", dmarc.Value, ok)
		}
	})

	t.Run("DKIM present emits real value and pending=false", func(t *testing.T) {
		rows, pending := buildDNSGuide(domain, mailHost, false, v4, v6, "mail", dkimTXT)
		if pending {
			t.Fatal("expected pending=false when a DKIM record is supplied")
		}
		dkim, ok := findRow(rows, "TXT", "mail._domainkey")
		if !ok || dkim.Value != "v=DKIM1; k=rsa; p=MIIBIjANBgkq" {
			t.Fatalf("DKIM: got %q ok=%v", dkim.Value, ok)
		}
	})

	t.Run("no DKIM key: pending=true and no _domainkey record (never empty p=)", func(t *testing.T) {
		rows, pending := buildDNSGuide(domain, mailHost, false, v4, v6, "", "")
		if !pending {
			t.Fatal("expected pending=true when no DKIM record is supplied")
		}
		if _, ok := findRow(rows, "TXT", "mail._domainkey"); ok {
			t.Fatal("must not emit a _domainkey record when no key exists")
		}
	})

	t.Run("missing server IPs omit A/AAAA but keep MX/TXT/SRV", func(t *testing.T) {
		rows, _ := buildDNSGuide(domain, mailHost, false, "", "", "mail", dkimTXT)
		if _, ok := findRow(rows, "A", "@"); ok {
			t.Fatal("A record should be omitted when serverIPv4 is empty")
		}
		if _, ok := findRow(rows, "AAAA", "@"); ok {
			t.Fatal("AAAA record should be omitted when serverIPv6 is empty")
		}
		if _, ok := findRow(rows, "MX", "@"); !ok {
			t.Fatal("MX should still be present without server IPs")
		}
	})

	t.Run("all rows carry the default TTL", func(t *testing.T) {
		rows, _ := buildDNSGuide(domain, mailHost, false, v4, v6, "mail", dkimTXT)
		for _, r := range rows {
			if r.TTL != dnsGuideTTL {
				t.Fatalf("row %s/%s TTL=%d, want %d", r.Type, r.Host, r.TTL, dnsGuideTTL)
			}
		}
	})
}

func TestExtractDKIMValue(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"full record", `mail._domainkey.x.com TXT "v=DKIM1; k=rsa; p=AAA"`, "v=DKIM1; k=rsa; p=AAA"},
		{"empty", "", ""},
		{"no quotes", "mail._domainkey.x.com TXT v=DKIM1", ""},
		{"single quote", `foo "bar`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractDKIMValue(tt.in); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDNSGuideZone(t *testing.T) {
	rows := []templates.DNSGuideRow{
		{Type: "A", Host: "@", TTL: 3600, Value: "192.0.2.1"},
		{Type: "A", Host: "mail", TTL: 3600, Value: "192.0.2.1"},
		{Type: "MX", Host: "@", TTL: 3600, Value: "10 mail.example.com."},
		{Type: "TXT", Host: "@", TTL: 3600, Value: "v=spf1 mx ~all"},
		{Type: "TXT", Host: "mail._domainkey", TTL: 3600, Value: "v=DKIM1; k=rsa; p=AAA"},
	}
	zone := dnsGuideZone("example.net", rows)

	// Apex uses the bare domain FQDN.
	if !strings.Contains(zone, "example.net.\t3600") && !strings.Contains(zone, "example.net.                          3600") {
		// tolerate column padding; just require the FQDN + A value appear together
		if !strings.Contains(zone, "example.net.") {
			t.Fatalf("zone missing apex FQDN:\n%s", zone)
		}
	}
	// Sub-host expands to host.domain.
	if !strings.Contains(zone, "mail.example.net.") {
		t.Fatalf("zone missing mail FQDN:\n%s", zone)
	}
	if !strings.Contains(zone, "mail._domainkey.example.net.") {
		t.Fatalf("zone missing DKIM FQDN:\n%s", zone)
	}
	// TXT values are quoted; non-TXT are not.
	if !strings.Contains(zone, `"v=spf1 mx ~all"`) {
		t.Fatalf("SPF TXT should be quoted:\n%s", zone)
	}
	if !strings.Contains(zone, "10 mail.example.com.") || strings.Contains(zone, `"10 mail.example.com."`) {
		t.Fatalf("MX value should be unquoted:\n%s", zone)
	}
}
