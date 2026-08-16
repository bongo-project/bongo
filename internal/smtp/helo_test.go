package smtp

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveHELOName(t *testing.T) {
	noHostname := func() (string, error) { return "", errors.New("no hostname") }
	dotlessHostname := func() (string, error) { return "web01", nil }
	qualifiedHostname := func() (string, error) { return "web01.internal.example.com", nil }

	tests := []struct {
		name                string
		smtpHostname        string
		dkimDomain          string
		hostnameFn          func() (string, error)
		resolveServerDomain func(string, string) string
		wantName            string
		wantOK              bool
	}{
		{
			name:                "SMTP.Hostname wins when qualified",
			smtpHostname:        "mail.example.test",
			dkimDomain:          "example.test",
			hostnameFn:          dotlessHostname,
			resolveServerDomain: func(string, string) string { return "should-not-be-used.example.test" },
			wantName:            "mail.example.test",
			wantOK:              true,
		},
		{
			name:                "SMTP.Hostname lower-cased and trimmed",
			smtpHostname:        "  Mail.Example.TEST  ",
			dkimDomain:          "",
			hostnameFn:          noHostname,
			resolveServerDomain: func(string, string) string { return "" },
			wantName:            "mail.example.test",
			wantOK:              true,
		},
		{
			name:                "falls back to os.Hostname() when it contains a dot",
			smtpHostname:        "",
			dkimDomain:          "",
			hostnameFn:          qualifiedHostname,
			resolveServerDomain: func(string, string) string { return "should-not-be-used.example.test" },
			wantName:            "web01.internal.example.com",
			wantOK:              true,
		},
		{
			name:                "dotless os.Hostname() is skipped in favour of ResolveServerDomain",
			smtpHostname:        "",
			dkimDomain:          "example.test",
			hostnameFn:          dotlessHostname,
			resolveServerDomain: func(dkim, smtp string) string { return dkim },
			wantName:            "example.test",
			wantOK:              true,
		},
		{
			name:                "ResolveServerDomain returning localhost is rejected, not sent",
			smtpHostname:        "",
			dkimDomain:          "",
			hostnameFn:          noHostname,
			resolveServerDomain: func(string, string) string { return "localhost" },
			wantName:            "",
			wantOK:              false,
		},
		{
			name:                "ResolveServerDomain returning a dotless hostname is rejected",
			smtpHostname:        "",
			dkimDomain:          "",
			hostnameFn:          noHostname,
			resolveServerDomain: func(string, string) string { return "web01" },
			wantName:            "",
			wantOK:              false,
		},
		{
			name:                "ResolveServerDomain returning a qualified name is used",
			smtpHostname:        "",
			dkimDomain:          "",
			hostnameFn:          noHostname,
			resolveServerDomain: func(string, string) string { return "mail.example.test" },
			wantName:            "mail.example.test",
			wantOK:              true,
		},
		{
			name:                "nothing qualified anywhere in the chain -> not ok",
			smtpHostname:        "",
			dkimDomain:          "",
			hostnameFn:          dotlessHostname,
			resolveServerDomain: func(string, string) string { return "" },
			wantName:            "",
			wantOK:              false,
		},
		{
			name:                "SMTP.Hostname of literal localhost is rejected",
			smtpHostname:        "localhost",
			dkimDomain:          "",
			hostnameFn:          qualifiedHostname,
			resolveServerDomain: func(string, string) string { return "" },
			wantName:            "web01.internal.example.com",
			wantOK:              true,
		},
		{
			name:                "*.local suffix is rejected as unqualified",
			smtpHostname:        "bongo.local",
			dkimDomain:          "",
			hostnameFn:          noHostname,
			resolveServerDomain: func(string, string) string { return "" },
			wantName:            "",
			wantOK:              false,
		},
		{
			name:                "name containing whitespace is rejected",
			smtpHostname:        "mail example.test",
			dkimDomain:          "",
			hostnameFn:          noHostname,
			resolveServerDomain: func(string, string) string { return "" },
			wantName:            "",
			wantOK:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotOK := resolveHELOName(tt.smtpHostname, tt.dkimDomain, tt.hostnameFn, tt.resolveServerDomain)
			if gotName != tt.wantName || gotOK != tt.wantOK {
				t.Errorf("resolveHELOName() = (%q, %v), want (%q, %v)", gotName, gotOK, tt.wantName, tt.wantOK)
			}
			if gotOK && gotName == "localhost" {
				t.Error("resolveHELOName must never return the literal localhost when ok is true")
			}
		})
	}
}

func TestIsQualifiedHELO(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"dotless", "web01", false},
		{"localhost", "localhost", false},
		{"localhost.localdomain", "localhost.localdomain", false},
		{"dot-local", "bongo.local", false},
		{"dot-localdomain", "bongo.localdomain", false},
		{"qualified", "mail.example.test", true},
		{"whitespace", "mail example.test", false},
		{"control char", "mail.example.test\r\n", false},
		// A bare IP has a dot but is not a qualified domain name: RFC 5321
		// 4.1.3 requires the bracketed literal, and strict receivers reject
		// the bare form. qualifyHELOCandidate brackets it instead.
		{"bare IPv4", "192.0.2.1", false},
		{"bare IPv6", "2001:db8::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isQualifiedHELO(tt.in); got != tt.want {
				t.Errorf("isQualifiedHELO(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestAddressLiteral(t *testing.T) {
	tests := []struct {
		name string
		addr fakeAddr
		want string
	}{
		{"ipv4 with port", fakeAddr("203.0.113.5:25"), "[203.0.113.5]"},
		{"ipv6 with port", fakeAddr("[2001:db8::1]:25"), "[IPv6:2001:db8::1]"},
		{"unparseable falls back", fakeAddr("not-an-ip"), "[127.0.0.1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addressLiteral(tt.addr)
			if got != tt.want {
				t.Errorf("addressLiteral(%q) = %q, want %q", tt.addr, got, tt.want)
			}
			if got == "localhost" {
				t.Error("addressLiteral must never return the literal localhost")
			}
		})
	}
}

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

// TestQualifyHELOCandidateBracketsIPAddresses covers the case an operator hits
// when smtp.hostname is set to a bare IP: the relay must send the RFC 5321
// 4.1.3 address-literal form, never the bare address, which strict receivers
// reject for the same reason they reject "localhost".
func TestQualifyHELOCandidateBracketsIPAddresses(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"IPv4 is bracketed", "192.0.2.1", "[192.0.2.1]", true},
		{"IPv4 already bracketed", "[192.0.2.1]", "[192.0.2.1]", true},
		{"IPv6 gets the IPv6 tag", "2001:db8::1", "[IPv6:2001:db8::1]", true},
		{"qualified name passes through", "mail.example.test", "mail.example.test", true},
		{"uppercase is normalised", "MAIL.Example.TEST", "mail.example.test", true},
		{"localhost is still rejected", "localhost", "", false},
		{"dotless is still rejected", "web01", "", false},
		{"empty is rejected", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := qualifyHELOCandidate(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("qualifyHELOCandidate(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestResolveHELONameBracketsConfiguredIP proves the IP path survives the full
// resolution chain, not just the candidate helper.
func TestResolveHELONameBracketsConfiguredIP(t *testing.T) {
	got, ok := resolveHELOName("192.0.2.1", "",
		func() (string, error) { return "workstation", nil },
		func(dkimDomain, smtpHostname string) string { return "localhost" })
	if !ok || got != "[192.0.2.1]" {
		t.Errorf("resolveHELOName with IP hostname = (%q, %v), want (\"[192.0.2.1]\", true)", got, ok)
	}
}

// TestHELONotQualifiedWarningNamesConfigKey covers the acceptance criterion
// that the startup warning tells the operator which key to set. The message is
// the actionable half of the fallback path, so it is asserted rather than left
// to drift.
func TestHELONotQualifiedWarningNamesConfigKey(t *testing.T) {
	if !strings.Contains(HELONotQualifiedWarning, "smtp.hostname") {
		t.Errorf("HELONotQualifiedWarning = %q, must name the smtp.hostname config key", HELONotQualifiedWarning)
	}
	if !strings.Contains(HELONotQualifiedWarning, "address literal") {
		t.Errorf("HELONotQualifiedWarning = %q, must say what happens instead", HELONotQualifiedWarning)
	}
}
