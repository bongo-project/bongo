package netx

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestMaybeProxyProtocolDisabledReturnsSame(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if got := MaybeProxyProtocol(raw, false); got != raw {
		t.Fatal("disabled should return the original listener unchanged")
	}
}

// A loopback peer is a trusted proxy, so its PROXY header is honoured and the
// accepted connection reports the real client address it declares.
func TestProxyProtocolReportsRealClient(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := MaybeProxyProtocol(raw, true)
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
	// PROXY v1 header declaring the real client, then some payload.
	fmt.Fprint(conn, "PROXY TCP4 203.0.113.7 10.0.0.1 12345 25\r\n")
	fmt.Fprint(conn, "EHLO x\r\n")
	conn.Close()

	select {
	case got := <-result:
		if !strings.HasPrefix(got, "203.0.113.7:") {
			t.Fatalf("expected real client 203.0.113.7, got %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for accepted connection")
	}
}

func TestIsTrustedProxy(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:5000", true},
		{"10.1.2.3:5000", true},
		{"172.18.0.5:5000", true},
		{"192.168.1.1:5000", true},
		{"203.0.113.7:5000", false},
		{"8.8.8.8:5000", false},
	}
	for _, c := range cases {
		got := isTrustedProxy(&net.TCPAddr{IP: net.ParseIP(strings.Split(c.addr, ":")[0]), Port: 5000})
		if got != c.want {
			t.Errorf("isTrustedProxy(%s) = %v, want %v", c.addr, got, c.want)
		}
	}
}
