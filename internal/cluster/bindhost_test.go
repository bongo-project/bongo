package cluster

import (
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
)

func TestClusterBindHost(t *testing.T) {
	tests := []struct {
		name      string
		role      string
		bind, adv string
		join      []string
		want      string
	}{
		{"loopback advertise narrows wildcard bind", "server", "0.0.0.0", "127.0.0.1", nil, "127.0.0.1"},
		{"ipv6 loopback advertise", "server", "0.0.0.0", "::1", nil, "::1"},
		{"localhost advertise", "server", "0.0.0.0", "localhost", nil, "127.0.0.1"},
		{"loopback bind with no advertise", "server", "127.0.0.1", "", nil, "127.0.0.1"},
		{"routable advertise keeps bind (seed node)", "server", "0.0.0.0", "10.0.0.5", nil, "0.0.0.0"},
		{"routable bind, no advertise", "server", "10.0.0.5", "", nil, "10.0.0.5"},
		{"join with routable advertise keeps bind", "server", "0.0.0.0", "10.0.0.5", []string{"10.0.0.2:8301"}, "0.0.0.0"},
		{"hostname advertise is not resolved", "server", "0.0.0.0", "bongo-1", nil, "0.0.0.0"},
		{"dev wildcard bind, no advertise, binds loopback", "dev", "0.0.0.0", "", nil, "127.0.0.1"},
		{"dev empty bind binds loopback", "dev", "", "", nil, "127.0.0.1"},
		{"dev ipv6 wildcard binds loopback", "dev", "::", "", nil, "127.0.0.1"},
		{"dev routable advertise keeps bind", "dev", "0.0.0.0", "10.0.0.5", nil, "0.0.0.0"},
		{"dev routable bind honoured", "dev", "10.0.0.5", "", nil, "10.0.0.5"},
		{"dev join keeps bind", "dev", "0.0.0.0", "", []string{"10.0.0.2:8301"}, "0.0.0.0"},
		{"server wildcard bind unchanged", "server", "0.0.0.0", "", nil, "0.0.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.Role = tt.role
			cfg.Server.BindAddr = tt.bind
			cfg.Server.AdvertiseAddr = tt.adv
			cfg.Cluster.Join = tt.join
			if got := clusterBindHost(cfg); got != tt.want {
				t.Errorf("clusterBindHost = %q, want %q", got, tt.want)
			}
		})
	}
}

// nonLoopbackIPv4 returns a local non-loopback IPv4 address, or "" if none.
func nonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
			return ipn.IP.String()
		}
	}
	return ""
}

func dialable(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func startServerRaft(t *testing.T, advertise string, port int) {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mm.Close() })

	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.Name = "bind-test"
	cfg.Storage.DataDir = dir
	cfg.Server.BindAddr = "0.0.0.0"
	cfg.Server.AdvertiseAddr = advertise
	cfg.Cluster.Join = nil
	cfg.Cluster.Raft.Port = port

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	node, err := NewRaftNode(cfg, ps, bs, mm, logger)
	if err != nil {
		t.Fatalf("NewRaftNode: %v", err)
	}
	t.Cleanup(func() { node.Shutdown() })
}

func TestNewRaftNodeServerLoopbackAdvertiseBindsLoopback(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	port := freePort(t)
	startServerRaft(t, "127.0.0.1", port)

	if !dialable(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
		t.Fatal("raft not reachable on loopback")
	}
	if dialable(net.JoinHostPort(ext, strconv.Itoa(port))) {
		t.Errorf("raft transport reachable on %s although advertise address is loopback", ext)
	}
}

func TestNewRaftNodeServerRoutableAdvertiseKeepsBind(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	port := freePort(t)
	startServerRaft(t, ext, port)

	if !dialable(net.JoinHostPort(ext, strconv.Itoa(port))) {
		t.Errorf("raft not reachable on %s with routable advertise address", ext)
	}
}
