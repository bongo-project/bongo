package agent

import (
	"context"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

// readyTestConfig returns a config for role with every listener on a free
// loopback port, so the test controls which port is contended.
func readyTestConfig(t *testing.T, role string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = role
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.Server.Agents = nil
	if role == "agent" {
		// No agent types, so Run starts no gRPC client or processor.
		cfg.AgentServer = &config.AgentConfig{ServerAddress: "127.0.0.1:1"}
	} else {
		cfg.Storage.DataDir = t.TempDir()
	}
	cfg.Web.Port = freePort(t)
	cfg.SMTP.Port = freePort(t)
	cfg.SMTP.SubmissionPort = freePort(t)
	cfg.IMAP.Port = freePort(t)
	cfg.IMAP.PortTLS = freePort(t)
	cfg.POP3.Port = freePort(t)
	cfg.POP3.PortTLS = freePort(t)
	cfg.Cluster.Gossip.Port = freePort(t)
	return cfg
}

func newReadyTestAgent(t *testing.T, cfg *config.Config) *Agent {
	t.Helper()
	a, err := NewAgent(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	a.shutdownGrace = 5 * time.Second
	return a
}

// TestRunWebPortTakenNeverReady holds the web port and checks that Run fails
// with a bind error and that Ready() is never observed true.
func TestRunWebPortTakenNeverReady(t *testing.T) {
	for _, role := range []string{"dev", "agent"} {
		t.Run(role, func(t *testing.T) {
			cfg := readyTestConfig(t, role)
			held, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port)))
			if err != nil {
				t.Fatalf("hold web port: %v", err)
			}
			defer held.Close()

			a := newReadyTestAgent(t, cfg)
			t.Cleanup(func() { _ = a.Shutdown() })

			var sawReady atomic.Bool
			stopPoll := make(chan struct{})
			pollDone := make(chan struct{})
			go func() {
				defer close(pollDone)
				for {
					select {
					case <-stopPoll:
						return
					default:
					}
					if a.Ready() {
						sawReady.Store(true)
					}
					runtime.Gosched()
				}
			}()

			runDone := make(chan error, 1)
			go func() { runDone <- a.Run(context.Background()) }()

			var runErr error
			select {
			case runErr = <-runDone:
			case <-time.After(20 * time.Second):
				close(stopPoll)
				<-pollDone
				t.Fatal("Run did not return with the web port taken")
			}
			close(stopPoll)
			<-pollDone

			if runErr == nil || !strings.Contains(runErr.Error(), "address already in use") {
				t.Fatalf("Run error = %v, want one containing %q", runErr, "address already in use")
			}
			if sawReady.Load() {
				t.Error("Ready() was observed true although the web listener never bound")
			}
			if a.Ready() {
				t.Error("Ready() is true after Run failed to bind")
			}
		})
	}
}

// dialAfterReady starts one agent, dials the web address the moment Ready()
// first returns true, and shuts it down.
func dialAfterReady(t *testing.T, role string) {
	t.Helper()
	cfg := readyTestConfig(t, role)
	a := newReadyTestAgent(t, cfg)
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(context.Background()) }()

	deadline := time.Now().Add(30 * time.Second)
	for !a.Ready() {
		if time.Now().After(deadline) {
			_ = a.Shutdown()
			t.Fatal("agent not ready")
		}
		select {
		case err := <-runDone:
			t.Fatalf("Run exited before ready: %v", err)
		default:
		}
		runtime.Gosched()
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port))
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		_ = a.Shutdown()
		<-runDone
		t.Fatalf("dial %s immediately after Ready: %v", addr, err)
	}
	conn.Close()
	_ = a.Shutdown()
	select {
	case <-runDone:
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return after Shutdown")
	}
}

// TestReadyImpliesWebBound checks that a TCP dial made immediately after
// Ready() first reports true always succeeds.
func TestReadyImpliesWebBound(t *testing.T) {
	t.Run("agent", func(t *testing.T) {
		for i := range 50 {
			t.Run(strconv.Itoa(i), func(t *testing.T) { dialAfterReady(t, "agent") })
		}
	})
	t.Run("dev", func(t *testing.T) {
		n := 50
		if testing.Short() {
			n = 5
		}
		for i := range n {
			t.Run(strconv.Itoa(i), func(t *testing.T) { dialAfterReady(t, "dev") })
		}
	})
}
