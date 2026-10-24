package cluster

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestGossipSingleNode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.Name = "node-1"
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Cluster.Gossip.Port = freePort(t)

	node, err := NewGossipNode(cfg, "127.0.0.1:8080", "", logger)
	if err != nil {
		t.Fatalf("NewGossipNode: %v", err)
	}
	defer node.Shutdown()

	members := node.Members()
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	if members[0].Name != "node-1" {
		t.Errorf("name = %q, want %q", members[0].Name, "node-1")
	}
	if members[0].Role != "dev" {
		t.Errorf("role = %q, want %q", members[0].Role, "dev")
	}
	if members[0].Status != "alive" {
		t.Errorf("status = %q, want %q", members[0].Status, "alive")
	}
}

func TestGossipThreeNodes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ports := [3]int{freePort(t), freePort(t), freePort(t)}
	nodes := make([]*GossipNode, 3)

	for i := 0; i < 3; i++ {
		cfg := config.Defaults()
		cfg.Server.Role = "server"
		cfg.Server.Name = fmt.Sprintf("node-%d", i+1)
		cfg.Server.BindAddr = "127.0.0.1"
		cfg.Cluster.Gossip.Port = ports[i]

		node, err := NewGossipNode(cfg, fmt.Sprintf("127.0.0.1:%d", 8080+i), "", logger)
		if err != nil {
			t.Fatalf("NewGossipNode %d: %v", i, err)
		}
		nodes[i] = node
	}

	// Cleanup all nodes.
	defer func() {
		for _, n := range nodes {
			if n != nil {
				n.Shutdown()
			}
		}
	}()

	// Node 2 and 3 join node 1.
	joinAddr := fmt.Sprintf("127.0.0.1:%d", ports[0])
	for i := 1; i < 3; i++ {
		contacted, err := nodes[i].Join([]string{joinAddr})
		if err != nil {
			t.Fatalf("node %d join: %v", i+1, err)
		}
		if contacted == 0 {
			t.Errorf("node %d joined 0 nodes", i+1)
		}
	}

	// Wait for gossip to converge.
	time.Sleep(500 * time.Millisecond)

	// All nodes should see 3 members.
	for i, node := range nodes {
		members := node.Members()
		if len(members) != 3 {
			t.Errorf("node %d sees %d members, want 3", i+1, len(members))
		}
	}

	// Verify metadata is propagated.
	members := nodes[0].Members()
	roleCount := map[string]int{}
	for _, m := range members {
		roleCount[m.Role]++
	}
	if roleCount["server"] != 3 {
		t.Errorf("expected 3 server roles, got %d", roleCount["server"])
	}
}

func TestGossipLoopbackAdvertiseBindsLoopback(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.Name = "gossip-bind"
	cfg.Server.BindAddr = "0.0.0.0"
	cfg.Server.AdvertiseAddr = "127.0.0.1"
	port := freePort(t)
	cfg.Cluster.Gossip.Port = port

	node, err := NewGossipNode(cfg, "127.0.0.1:8080", "", logger)
	if err != nil {
		t.Fatalf("NewGossipNode: %v", err)
	}
	defer node.Shutdown()

	if !dialable(net.JoinHostPort("127.0.0.1", fmt.Sprint(port))) {
		t.Fatal("gossip not reachable on loopback")
	}
	if dialable(net.JoinHostPort(ext, fmt.Sprint(port))) {
		t.Errorf("gossip reachable on %s although advertise address is loopback", ext)
	}
}

func TestGossipBindsAdvertiseOrClusterBindAddr(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name        string
		clusterBind string
		onExt       bool
		onLoopback  bool
	}{
		{"unset follows advertise, ignores server.bind_addr", "", true, false},
		{"cluster.bind_addr wins", "127.0.0.1", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.Role = "server"
			cfg.Server.Name = "gossip-bind"
			cfg.Server.BindAddr = "0.0.0.0"
			cfg.Server.AdvertiseAddr = ext
			cfg.Cluster.BindAddr = tt.clusterBind
			port := freePort(t)
			cfg.Cluster.Gossip.Port = port
			node, err := NewGossipNode(cfg, "127.0.0.1:8080", "", logger)
			if err != nil {
				t.Fatalf("NewGossipNode: %v", err)
			}
			defer node.Shutdown()
			if got := dialable(net.JoinHostPort(ext, fmt.Sprint(port))); got != tt.onExt {
				t.Errorf("reachable on %s = %v, want %v", ext, got, tt.onExt)
			}
			if got := dialable(net.JoinHostPort("127.0.0.1", fmt.Sprint(port))); got != tt.onLoopback {
				t.Errorf("reachable on loopback = %v, want %v", got, tt.onLoopback)
			}
		})
	}
}

func TestGossipRejectsUnusableBind(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.BindAddr = "0.0.0.0"
	cfg.Server.AdvertiseAddr = "bongo-1"
	cfg.Cluster.Gossip.Port = freePort(t)
	if _, err := NewGossipNode(cfg, "127.0.0.1:8080", "", slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("hostname advertise without cluster.bind_addr must not start gossip")
	}
}

// The peer address travels in gossip metadata, built from the advertise
// address; a node without peers advertises none.
func TestGossipAdvertisesPeerAddr(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("no peers advertises nothing", func(t *testing.T) {
		cfg := config.Defaults()
		cfg.Server.Role = "server"
		cfg.Server.Name = "solo"
		cfg.Server.BindAddr = "0.0.0.0"
		cfg.Server.AdvertiseAddr = "127.0.0.1"
		cfg.Cluster.Gossip.Port = freePort(t)
		node, err := NewGossipNode(cfg, "0.0.0.0:443", "", logger)
		if err != nil {
			t.Fatal(err)
		}
		defer node.Shutdown()
		if got := node.Members()[0].PeerAddr; got != "" {
			t.Errorf("PeerAddr = %q, want empty", got)
		}
	})

	t.Run("peered node advertises advertise address and peer port", func(t *testing.T) {
		ext := nonLoopbackIPv4()
		if ext == "" {
			t.Skip("no non-loopback IPv4 interface")
		}
		cfg := config.Defaults()
		cfg.Server.Role = "server"
		cfg.Server.Name = "peered"
		cfg.Server.AdvertiseAddr = ext
		cfg.Cluster.PeerPort = 18302
		cfg.Cluster.Gossip.Port = freePort(t)
		node, err := NewGossipNode(cfg, "0.0.0.0:443", "", logger)
		if err != nil {
			t.Fatal(err)
		}
		defer node.Shutdown()
		want := net.JoinHostPort(ext, "18302")
		if got := node.Members()[0].PeerAddr; got != want {
			t.Errorf("PeerAddr = %q, want %q", got, want)
		}
	})
}
