package netx

import (
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMaybeProxyProtocolDisabledReturnsSame(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if got := MaybeProxyProtocol(raw, false, nil); got != raw {
		t.Fatal("disabled should return the original listener unchanged")
	}
}

// The PROXY header is honoured only from a listed peer. A loopback peer that
// is not listed (or an empty list) is not trusted: the header is discarded and
// the connection reports the real peer address.
func TestProxyProtocolTrust(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		want    string
	}{
		{"listed peer honoured", []string{"127.0.0.1/32"}, "203.0.113.7:"},
		{"listed by CIDR honoured", []string{"127.0.0.0/8"}, "203.0.113.7:"},
		{"unlisted peer ignored", []string{"10.9.9.9/32"}, "127.0.0.1:"},
		{"empty list trusts nobody", nil, "127.0.0.1:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trust, err := ParseTrustedProxies(tt.trusted)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			l := MaybeProxyProtocol(raw, true, trust)
			defer l.Close()

			result := make(chan string, 1)
			go func() {
				c, err := l.Accept()
				if err != nil {
					result <- "accept-err: " + err.Error()
					return
				}
				defer c.Close()
				buf := make([]byte, 8)
				_, _ = c.Read(buf) // triggers PROXY header parsing
				result <- c.RemoteAddr().String()
			}()

			conn, err := net.Dial("tcp", raw.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprint(conn, "PROXY TCP4 203.0.113.7 10.0.0.1 12345 25\r\n")
			fmt.Fprint(conn, "EHLO x\r\n")
			conn.Close()

			select {
			case got := <-result:
				if !strings.HasPrefix(got, tt.want) {
					t.Fatalf("RemoteAddr = %q, want prefix %q", got, tt.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for accepted connection")
			}
		})
	}
}

func TestTrustedProxiesTrusts(t *testing.T) {
	tp, err := ParseTrustedProxies([]string{"10.0.0.5", "172.18.0.0/16", "fd00::1", "::ffff:192.168.7.7"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		host string
		want bool
	}{
		{"10.0.0.5", true},
		{"10.0.0.5:5000", true},
		{"::ffff:10.0.0.5", true},
		{"10.0.0.6", false},    // RFC1918 but not listed
		{"192.168.1.1", false}, // RFC1918 but not listed
		{"172.18.3.4", true},
		{"172.16.0.9", false},
		{"127.0.0.1", false}, // loopback is not implicitly trusted
		{"fd00::1", true},
		{"fd00::2", false},
		{"192.168.7.7", true}, // mapped entry matches plain peer
		{"not-an-ip", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := tp.TrustsHost(tt.host); got != tt.want {
			t.Errorf("TrustsHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
	if got := tp.Trusts(&net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 1}); !got {
		t.Error("Trusts(10.0.0.5:1) = false, want true")
	}
	if tp.Trusts(nil) {
		t.Error("Trusts(nil) = true")
	}

	var nilTP *TrustedProxies
	if nilTP.TrustsHost("127.0.0.1") || !nilTP.Empty() {
		t.Error("nil set must trust nobody")
	}
	empty, _ := ParseTrustedProxies(nil)
	if empty.TrustsHost("10.0.0.5") {
		t.Error("empty set must trust nobody")
	}
}

func TestParseTrustedProxiesInvalid(t *testing.T) {
	for _, bad := range []string{"nope", "10.0.0.0/33", "10.0.0.1/x", ""} {
		_, err := ParseTrustedProxies([]string{"10.0.0.1", bad})
		if err == nil {
			t.Errorf("ParseTrustedProxies(%q) succeeded, want error", bad)
		} else if bad != "" && !strings.Contains(err.Error(), bad) {
			t.Errorf("error %q does not name %q", err, bad)
		}
	}
}

// syncBuffer is a bytes.Buffer safe for the logger and the test to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestProxyProtocolRejectedHeaderWarns(t *testing.T) {
	const v1 = "PROXY TCP4 203.0.113.7 10.0.0.1 12345 25\r\n"
	const v2 = "\r\n\r\n\x00\r\nQUIT\n"
	tests := []struct {
		name     string
		trusted  []string
		sends    []string
		wantWarn int
	}{
		{"untrusted v1 header warns", []string{"10.9.9.9/32"}, []string{v1 + "EHLO x\r\n"}, 1},
		{"untrusted v2 header warns", nil, []string{v2}, 1},
		{"trusted peer does not warn", []string{"127.0.0.1/32"}, []string{v1 + "EHLO x\r\n"}, 0},
		{"untrusted peer without header does not warn", nil, []string{"EHLO x\r\n"}, 0},
		{"repeats are rate limited", nil, []string{v1 + "EHLO x\r\n", v1 + "EHLO x\r\n", v1 + "EHLO x\r\n"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trust, err := ParseTrustedProxies(tt.trusted)
			if err != nil {
				t.Fatal(err)
			}
			out := &syncBuffer{}
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			l := MaybeProxyProtocol(raw, true, trust, WithRejectLogger(slog.New(slog.NewTextHandler(out, nil))))
			defer l.Close()

			for _, payload := range tt.sends {
				served := make(chan struct{})
				go func() {
					defer close(served)
					c, err := l.Accept()
					if err != nil {
						return
					}
					defer c.Close()
					_, _ = c.Read(make([]byte, 8))
				}()
				conn, err := net.Dial("tcp", raw.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprint(conn, payload)
				conn.Close()
				select {
				case <-served:
				case <-time.After(3 * time.Second):
					t.Fatal("timed out waiting for accepted connection")
				}
			}

			got := strings.Count(out.String(), "level=WARN")
			if got != tt.wantWarn {
				t.Fatalf("WARN lines = %d, want %d; log:\n%s", got, tt.wantWarn, out.String())
			}
			if tt.wantWarn > 0 {
				log := out.String()
				if !strings.Contains(log, "peer=127.0.0.1") {
					t.Errorf("WARN does not name the peer IP: %s", log)
				}
				if strings.Contains(log, "203.0.113.7") || strings.Contains(log, "12345") {
					t.Errorf("WARN leaks header client data: %s", log)
				}
			}
		})
	}
}
