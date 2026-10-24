package testutil

import (
	"net"
	"testing"
)

// FreePorts returns n distinct free loopback TCP ports.
//
// Every listener is held open until all n ports have been drawn and only then
// closed together. Drawing them one at a time (listen, read the port, close)
// lets the kernel hand the same port back twice, so two listeners in one agent
// config would race for it. A small window remains between the close and the
// caller's own bind, which is why tests that can bind ":0" and read the bound
// address should do that instead.
func FreePorts(t testing.TB, n int) []int {
	t.Helper()
	listeners := make([]net.Listener, 0, n)
	ports := make([]int, 0, n)
	for range n {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, held := range listeners {
				_ = held.Close()
			}
			t.Fatalf("FreePorts: %v", err)
		}
		listeners = append(listeners, l)
		ports = append(ports, l.Addr().(*net.TCPAddr).Port)
	}
	for _, l := range listeners {
		_ = l.Close()
	}
	return ports
}
