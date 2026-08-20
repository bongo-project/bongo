package smtp

import (
	"io"
	"log/slog"
	"net"
	"testing"
)

func mustParseCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("mustParseCIDR(%q): %v", s, err)
	}
	return n
}

func trustedNetworksTestBackend(nets []*net.IPNet) *Backend {
	return &Backend{
		perIP:       map[string]int{},
		logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		trustedNets: nets,
	}
}

// TestIsTrustedSource covers the fail-closed contract of Backend.isTrustedSource:
// only an IP that parses and falls inside a configured CIDR is trusted; every
// other input (no networks configured, empty/unparseable IP, out-of-range IP)
// must report false.
func TestIsTrustedSource(t *testing.T) {
	v4Net := mustParseCIDR(t, "10.0.0.0/8")
	v6Net := mustParseCIDR(t, "2001:db8::/32")

	tests := []struct {
		name string
		nets []*net.IPNet
		ip   string
		want bool
	}{
		{
			name: "in-CIDR IPv4 address is trusted",
			nets: []*net.IPNet{v4Net},
			ip:   "10.1.2.3",
			want: true,
		},
		{
			name: "out-of-CIDR IPv4 address is untrusted",
			nets: []*net.IPNet{v4Net},
			ip:   "203.0.113.5",
			want: false,
		},
		{
			name: "no networks configured: loopback IPv4 is untrusted",
			nets: nil,
			ip:   "127.0.0.1",
			want: false,
		},
		{
			name: "no networks configured: loopback IPv6 is untrusted",
			nets: nil,
			ip:   "::1",
			want: false,
		},
		{
			name: "no networks configured: RFC 1918 10.0.0.1 is untrusted",
			nets: nil,
			ip:   "10.0.0.1",
			want: false,
		},
		{
			name: "no networks configured: RFC 1918 172.19.0.1 is untrusted",
			nets: nil,
			ip:   "172.19.0.1",
			want: false,
		},
		{
			name: "no networks configured: RFC 1918 192.168.1.1 is untrusted",
			nets: nil,
			ip:   "192.168.1.1",
			want: false,
		},
		{
			name: "empty source IP is untrusted even with networks configured",
			nets: []*net.IPNet{v4Net},
			ip:   "",
			want: false,
		},
		{
			name: "garbage source IP 'not-an-ip' is untrusted",
			nets: []*net.IPNet{v4Net},
			ip:   "not-an-ip",
			want: false,
		},
		{
			name: "garbage source IP '1.2.3' is untrusted",
			nets: []*net.IPNet{v4Net},
			ip:   "1.2.3",
			want: false,
		},
		{
			name: "source IP with port suffix is untrusted (not a bare IP)",
			nets: []*net.IPNet{v4Net},
			ip:   "10.0.0.1:25",
			want: false,
		},
		{
			name: "IPv6 CIDR matches an in-range IPv6 address",
			nets: []*net.IPNet{v6Net},
			ip:   "2001:db8::1",
			want: true,
		},
		{
			name: "IPv6 CIDR does not match an out-of-range IPv6 address",
			nets: []*net.IPNet{v6Net},
			ip:   "2001:db9::1",
			want: false,
		},
		{
			name: "IPv4 address does not match an IPv6-only CIDR",
			nets: []*net.IPNet{v6Net},
			ip:   "10.1.2.3",
			want: false,
		},
		{
			name: "IPv4-mapped IPv6 form matches the equivalent IPv4 CIDR",
			nets: []*net.IPNet{v4Net},
			ip:   "::ffff:10.1.2.3",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := trustedNetworksTestBackend(tt.nets)
			if got := b.isTrustedSource(tt.ip); got != tt.want {
				t.Errorf("isTrustedSource(%q) with nets=%v = %v, want %v", tt.ip, tt.nets, got, tt.want)
			}
		})
	}
}

// TestInboundMetaTrustedNetworkOR proves the trust decision in inboundMeta is
// an OR of session auth state and trusted-network membership, not an AND: an
// unauthenticated session from a trusted-CIDR address must still be trusted,
// and an authenticated session must still be trusted even with no trusted
// networks configured at all.
func TestInboundMetaTrustedNetworkOR(t *testing.T) {
	trustedNet := mustParseCIDR(t, "10.0.0.0/8")

	tests := []struct {
		name     string
		nets     []*net.IPNet
		authed   bool
		sourceIP string
		want     bool
	}{
		{
			name:     "unauthenticated + in-CIDR source is trusted",
			nets:     []*net.IPNet{trustedNet},
			authed:   false,
			sourceIP: "10.1.2.3",
			want:     true,
		},
		{
			name:     "authenticated + no networks configured is trusted",
			nets:     nil,
			authed:   true,
			sourceIP: "203.0.113.5",
			want:     true,
		},
		{
			name:     "unauthenticated + out-of-CIDR source is untrusted",
			nets:     []*net.IPNet{trustedNet},
			authed:   false,
			sourceIP: "203.0.113.5",
			want:     false,
		},
		{
			name:     "unauthenticated + no networks configured is untrusted",
			nets:     nil,
			authed:   false,
			sourceIP: "127.0.0.1",
			want:     false,
		},
		{
			name:     "unauthenticated + empty source IP is untrusted",
			nets:     []*net.IPNet{trustedNet},
			authed:   false,
			sourceIP: "",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := trustedNetworksTestBackend(tt.nets)
			s := &Session{backend: b, logger: b.logger, authed: tt.authed}
			meta := s.inboundMeta(tt.sourceIP)
			if meta.Trusted != tt.want {
				t.Errorf("inboundMeta(%q).Trusted = %v, want %v (authed=%v, nets=%v)",
					tt.sourceIP, meta.Trusted, tt.want, tt.authed, tt.nets)
			}
			if meta.SourceIP != tt.sourceIP {
				t.Errorf("inboundMeta(%q).SourceIP = %q, want %q", tt.sourceIP, meta.SourceIP, tt.sourceIP)
			}
		})
	}
}

// TestSetTrustedNetworks proves the setter actually assigns the field the
// helper reads, guarding against a copy/typo that would leave trust decisions
// silently disabled regardless of config.
func TestSetTrustedNetworks(t *testing.T) {
	b := trustedNetworksTestBackend(nil)
	if b.isTrustedSource("10.1.2.3") {
		t.Fatal("expected untrusted before SetTrustedNetworks")
	}
	b.SetTrustedNetworks([]*net.IPNet{mustParseCIDR(t, "10.0.0.0/8")})
	if !b.isTrustedSource("10.1.2.3") {
		t.Fatal("expected trusted after SetTrustedNetworks with a matching CIDR")
	}
}
