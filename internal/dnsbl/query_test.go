package dnsbl

import (
	"net"
	"strings"
	"testing"
)

func TestQueryName(t *testing.T) {
	const zone = "zen.example-bl.org"

	tests := []struct {
		name   string
		ip     string
		zone   string
		want   string
		wantOK bool
		labels int // expected number of dot-separated labels before the zone, 0 = don't check
	}{
		{
			name:   "ipv4 basic",
			ip:     "203.0.113.5",
			zone:   zone,
			want:   "5.113.0.203." + zone,
			wantOK: true,
			labels: 4,
		},
		{
			name:   "ipv4 all zero",
			ip:     "0.0.0.0",
			zone:   zone,
			want:   "0.0.0.0." + zone,
			wantOK: true,
			labels: 4,
		},
		{
			name:   "ipv4 all ones",
			ip:     "255.255.255.255",
			zone:   zone,
			want:   "255.255.255.255." + zone,
			wantOK: true,
			labels: 4,
		},
		{
			name:   "ipv4-mapped ipv6 takes the ipv4 path",
			ip:     "::ffff:203.0.113.5",
			zone:   zone,
			want:   "5.113.0.203." + zone,
			wantOK: true,
			labels: 4,
		},
		{
			name:   "ipv6 loopback",
			ip:     "::1",
			zone:   "ip6-zone.example",
			want:   "1." + strings.Repeat("0.", 31) + "ip6-zone.example",
			wantOK: true,
			labels: 32,
		},
		{
			name:   "ipv6 compressed form",
			ip:     "2001:db8::1",
			zone:   zone,
			wantOK: true,
			labels: 32,
		},
		{
			name:   "ipv6 fully expanded equivalent produces identical name",
			ip:     "2001:0db8:0000:0000:0000:0000:0000:0001",
			zone:   zone,
			wantOK: true,
			labels: 32,
		},
		{
			name:   "nil ip",
			ip:     "",
			zone:   zone,
			wantOK: false,
		},
		{
			name:   "invalid ip",
			ip:     "not-an-ip",
			zone:   zone,
			wantOK: false,
		},
		{
			name:   "empty zone",
			ip:     "203.0.113.5",
			zone:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ip net.IP
			if tt.ip != "" {
				ip = net.ParseIP(tt.ip)
				if ip == nil && tt.ip != "not-an-ip" {
					t.Fatalf("test setup: %q did not parse", tt.ip)
				}
				if tt.ip == "not-an-ip" {
					ip = nil // net.ParseIP already returns nil for this; explicit for clarity
				}
			}

			got, ok := queryName(ip, tt.zone)
			if ok != tt.wantOK {
				t.Fatalf("queryName(%q, %q) ok = %v, want %v (name=%q)", tt.ip, tt.zone, ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			if tt.want != "" && got != tt.want {
				t.Errorf("queryName(%q, %q) = %q, want %q", tt.ip, tt.zone, got, tt.want)
			}
			if tt.labels > 0 {
				name := strings.TrimSuffix(got, "."+tt.zone)
				labels := strings.Split(name, ".")
				if len(labels) != tt.labels {
					t.Errorf("queryName(%q, %q) = %q, got %d labels before zone, want %d", tt.ip, tt.zone, got, len(labels), tt.labels)
				}
			}
		})
	}
}

func TestQueryNameIPv6CompressedMatchesExpanded(t *testing.T) {
	const zone = "zen.example-bl.org"
	compressed, ok1 := queryName(net.ParseIP("2001:db8::1"), zone)
	expanded, ok2 := queryName(net.ParseIP("2001:0db8:0000:0000:0000:0000:0000:0001"), zone)
	if !ok1 || !ok2 {
		t.Fatalf("queryName failed: ok1=%v ok2=%v", ok1, ok2)
	}
	if compressed != expanded {
		t.Errorf("compressed form %q != expanded form %q", compressed, expanded)
	}
}

func TestNormalizeZone(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"Zen.Example-BL.org", "zen.example-bl.org", true},
		{"zen.example-bl.org.", "zen.example-bl.org", true},
		{"  zen.example-bl.org  ", "zen.example-bl.org", true},
		{"", "", false},
		{"   ", "", false},
		{".", "", false},
	}
	for _, tt := range tests {
		got, ok := normalizeZone(tt.in)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("normalizeZone(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestClassifyAnswer(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want answerClass
	}{
		{"127.0.0.2 is listed", "127.0.0.2", classListed},
		{"127.0.0.10 is listed", "127.0.0.10", classListed},
		{"127.255.255.254 is a policy-error code, not listed", "127.255.255.254", classPolicyError},
		{"127.255.255.252 is a policy-error code, not listed", "127.255.255.252", classPolicyError},
		{"93.184.216.34 is outside 127/8, not listed", "93.184.216.34", classUninterpretable},
		{"127.0.0.1 (loopback-looking, but still a listing code) is listed", "127.0.0.1", classListed},
		{"127.255.254.255 just outside the policy sub-range is listed", "127.255.254.255", classListed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class, code := classifyAnswer(net.ParseIP(tt.ip))
			if class != tt.want {
				t.Errorf("classifyAnswer(%q) class = %v, want %v (code=%q)", tt.ip, class, tt.want, code)
			}
			if code != tt.ip {
				t.Errorf("classifyAnswer(%q) code = %q, want %q", tt.ip, code, tt.ip)
			}
		})
	}
}

func TestClassifyAnswerV4MappedV6(t *testing.T) {
	// A resolver can hand back a 16-byte v4-mapped value; classifyAnswer
	// must call To4() before inspecting octets rather than reading the
	// first byte of a 16-byte slice.
	answer := net.ParseIP("::ffff:127.0.0.2")
	class, code := classifyAnswer(answer)
	if class != classListed {
		t.Errorf("class = %v, want classListed", class)
	}
	if code != "127.0.0.2" {
		t.Errorf("code = %q, want 127.0.0.2", code)
	}
}
