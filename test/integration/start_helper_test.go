//go:build integration

package integration

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

// TestSharedStartAgentFailsFastOnRunError proves a Run that returns before
// Ready fails the start immediately with Run's own error, instead of the
// caller polling Ready() until the readiness deadline.
//
// The gossip port is held on both TCP and UDP (memberlist binds both) so the
// agent's gossip step fails after raft leadership and before Ready, which is
// the exact shape of the stalls seen under the full suite.
func TestSharedStartAgentFailsFastOnRunError(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Cluster.Gossip.Port)
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("hold gossip tcp port: %v", err)
	}
	defer tcp.Close()
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("resolve udp: %v", err)
	}
	udp, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("hold gossip udp port: %v", err)
	}
	defer udp.Close()

	const timeout = 15 * time.Second
	start := time.Now()
	a, _, _, err := sharedStartAgentErr(cfg, sharedTestLogger(), timeout)
	elapsed := time.Since(start)
	if a != nil {
		// Run's early-return paths skip Shutdown; release what it opened so
		// the leaked agent cannot poison later tests.
		defer func() { _ = a.Shutdown() }()
	}

	if err == nil {
		t.Fatal("expected start to fail with the gossip port occupied, got nil")
	}
	if !strings.Contains(err.Error(), "exited before ready") {
		t.Errorf("error should report Run exited before ready, got: %v", err)
	}
	if !strings.Contains(err.Error(), "address already in use") {
		t.Errorf("error should carry Run's bind error, got: %v", err)
	}
	if elapsed >= 5*time.Second {
		t.Errorf("start took %s; a Run error must fail fast, not wait for the %s readiness deadline", elapsed, timeout)
	}
}

// TestSharedStartAgentRetriesOnPortRace proves a port taken between selection
// and bind is retried on fresh ports rather than failing the test: the first
// attempt hits an occupied gossip port, the second draws a new one.
func TestSharedStartAgentRetriesOnPortRace(t *testing.T) {
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.BindAddr = "127.0.0.1"
	sharedAssignFreePorts(t, cfg)

	held := cfg.Cluster.Gossip.Port
	addr := fmt.Sprintf("127.0.0.1:%d", held)
	tcp, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("hold gossip tcp port: %v", err)
	}
	defer tcp.Close()
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("resolve udp: %v", err)
	}
	udp, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("hold gossip udp port: %v", err)
	}
	defer udp.Close()

	a, _, _ := sharedStartAgent(t, cfg)
	if a == nil || !a.Ready() {
		t.Fatal("agent should be ready after retrying on new ports")
	}
	if cfg.Cluster.Gossip.Port == held {
		t.Errorf("gossip port still %d; the retry did not draw fresh ports", held)
	}
}
