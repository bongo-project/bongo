package netx

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// listenLoopback starts a TCP listener on 127.0.0.1 that counts accepted
// connections, and returns it with its port.
func listenLoopback(t *testing.T) (*net.TCPListener, uint16, *atomic.Int32) {
	t.Helper()
	ln, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close()
		}
	}()
	return ln, uint16(ln.Addr().(*net.TCPAddr).Port), &accepted
}

func testMailDialer(opts MailDialerOptions, port uint16) *MailDialer {
	d := NewMailDialer(opts)
	d.extraPorts = map[uint16]bool{port: true}
	return d
}

func TestMailDialerRefusesLoopbackAndPrivate(t *testing.T) {
	_, port, accepted := listenLoopback(t)
	d := testMailDialer(MailDialerOptions{}, port)
	for _, host := range []string{
		"127.0.0.1", "::1", "10.1.2.3", "169.254.169.254", "::ffff:127.0.0.1",
		"192.168.0.1", "172.16.0.9", "100.64.0.1", "0.0.0.0", "fd00::1",
	} {
		_, err := d.DialContext(context.Background(), host, port)
		if !errors.Is(err, ErrForbiddenAddress) {
			t.Errorf("%s: want ErrForbiddenAddress, got %v", host, err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Fatalf("listener saw %d connections", n)
	}
}

func TestMailDialerRefusesNameResolvingToLoopback(t *testing.T) {
	_, port, accepted := listenLoopback(t)
	d := testMailDialer(MailDialerOptions{}, port)
	_, err := d.DialContext(context.Background(), "localhost", port)
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("want ErrForbiddenAddress, got %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if accepted.Load() != 0 {
		t.Fatal("listener was reached")
	}
}

func TestMailDialerPortRule(t *testing.T) {
	// The allow-private option must not lift the port rule, so use it to
	// reach the port check on a loopback address.
	d := NewMailDialer(MailDialerOptions{AllowPrivateNetworks: true})
	for _, port := range []uint16{25, 80, 8443, 22, 587, 465, 0} {
		_, err := d.DialContext(context.Background(), "127.0.0.1", port)
		if !errors.Is(err, ErrForbiddenPort) {
			t.Errorf("port %d: want ErrForbiddenPort, got %v", port, err)
		}
	}
	for _, port := range []uint16{993, 995, 143, 110} {
		if !d.portAllowed(port) {
			t.Errorf("port %d should be allowed", port)
		}
	}
	// With the address rule intact, a mail port on loopback reaches the
	// address check, not the port check.
	d = NewMailDialer(MailDialerOptions{})
	_, err := d.DialContext(context.Background(), "127.0.0.1", 993)
	if !errors.Is(err, ErrForbiddenAddress) {
		t.Fatalf("want ErrForbiddenAddress on 993, got %v", err)
	}
}

func TestMailDialerAllowPrivateKeepsPortRule(t *testing.T) {
	ln, port, accepted := listenLoopback(t)
	_ = ln
	d := testMailDialer(MailDialerOptions{AllowPrivateNetworks: true}, port)
	conn, err := d.DialContext(context.Background(), "127.0.0.1", port)
	if err != nil {
		t.Fatalf("allow private should reach loopback: %v", err)
	}
	_ = conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for accepted.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if accepted.Load() != 1 {
		t.Fatal("listener not reached")
	}
	if _, err := d.DialContext(context.Background(), "127.0.0.1", 25); !errors.Is(err, ErrForbiddenPort) {
		t.Fatalf("port 25 with allow private: %v", err)
	}
	if !d.AllowsPrivateNetworks() {
		t.Fatal("AllowsPrivateNetworks")
	}
}

// Both clients must call the one function, so a forbidden address or port is
// refused identically by each. The table covers every branch of the hook.
func TestHTTPClientAndMailDialerShareControl(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    error
	}{
		{"loopback", "127.0.0.1:443", ErrForbiddenAddress},
		{"mapped loopback", "[::ffff:127.0.0.1]:443", ErrForbiddenAddress},
		{"link local", "169.254.169.254:443", ErrForbiddenAddress},
		{"unparsable", "not-an-address", ErrForbiddenAddress},
		{"bad port", "8.8.8.8:25", ErrForbiddenPort},
		{"ok", "8.8.8.8:443", nil},
	}
	httpClient := NewClient(Options{})
	// A MailDialer shares only the hook, not the port list, so give both the
	// port 443 for this table.
	mail := NewMailDialer(MailDialerOptions{})
	mail.extraPorts = map[uint16]bool{443: true}
	for _, tt := range tests {
		for name, ctl := range map[string]func(context.Context, string, string) error{
			"http": func(ctx context.Context, n, a string) error {
				return httpClient.dialer().ControlContext(ctx, n, a, nil)
			},
			"mail": func(ctx context.Context, n, a string) error {
				return mail.dialer().ControlContext(ctx, n, a, nil)
			},
		} {
			err := ctl(context.Background(), "tcp", tt.address)
			if !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Errorf("%s/%s: got %v, want %v", tt.name, name, err, tt.want)
			}
		}
	}
}

func TestMailHostOK(t *testing.T) {
	tests := []struct {
		host    string
		want    string
		wantErr bool
	}{
		{"imap.example.com", "imap.example.com", false},
		{"IMAP.Example.COM", "imap.example.com", false},
		{"bücher.example", "xn--bcher-kva.example", false},
		{"8.8.8.8", "8.8.8.8", false},
		{"2001:db8::1", "2001:db8::1", false},
		{"", "", true},
		{strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61), "", false},
		{strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62), "", true}, // 254
		{strings.Repeat("a", 64) + ".example", "", true},
		{"exa\x00mple.com", "", true},
		{"exa\nmple.com", "", true},
		{"exa mple.com", "", true},
		{"example.com\t", "", true},
		{"fe80::1%25eth0", "", true},
		{"fe80::1%eth0", "", true},
		{"[::1]", "", true},
		{"example.com:993", "", true},
		{"example.com/path", "", true},
		{"user@example.com", "", true},
		{"-bad.example", "", true},
		{"bad-.example", "", true},
		{"a..b", "", true},
		{".example.com", "", true},
		{"example.com.", "", true},
		{"under_score.example", "", true},
		{"2130706433", "", true}, // numeric shorthand for 127.0.0.1
		{"127.1", "", true},
	}
	for _, tt := range tests {
		got, err := NormalizeMailHost(tt.host)
		if (err != nil) != tt.wantErr {
			t.Errorf("%q: err = %v, wantErr %v", tt.host, err, tt.wantErr)
			continue
		}
		if err == nil && tt.want != "" && got != tt.want {
			t.Errorf("%q: got %q, want %q", tt.host, got, tt.want)
		}
		if (MailHostOK(tt.host) != nil) != tt.wantErr {
			t.Errorf("%q: MailHostOK disagrees", tt.host)
		}
		if err != nil && tt.host != "" && strings.Contains(err.Error(), tt.host) {
			t.Errorf("%q: error echoes the host: %v", tt.host, err)
		}
	}
}

func TestMailDialerRefusesBadHostBeforeDialing(t *testing.T) {
	d := NewMailDialer(MailDialerOptions{AllowPrivateNetworks: true})
	_, err := d.DialContext(context.Background(), "exa\x00mple.com", 993)
	if !errors.Is(err, ErrInvalidMailHost) {
		t.Fatalf("want ErrInvalidMailHost, got %v", err)
	}
}
