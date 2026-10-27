package agent

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentgrpc "github.com/bongo-project/bongo/internal/agent/grpc"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// agentTLSFixture is a started AgentServer standing in for the server role,
// plus the TLS material an agent-role node would be configured with.
type agentTLSFixture struct {
	server *agentgrpc.AgentServer
	files  config.TLSConfig
}

func newAgentTLSFixture(t *testing.T) *agentTLSFixture {
	t.Helper()
	dir := t.TempDir()
	ca := testutil.NewCA(t, "good CA")
	certFile, keyFile := testutil.WriteLeaf(t, dir, "node", ca.Issue(t, "node"))
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, ca.CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := agentgrpc.NewAgentServer(nil, nil, testLogger(), agentgrpc.AgentServerConfig{
		BindHost: "127.0.0.1",
		AgentTTL: agentgrpc.DefaultAgentTTL, ReapInterval: agentgrpc.DefaultReapInterval,
		TLS: agentgrpc.TLSFiles{CertFile: certFile, KeyFile: keyFile, CAFile: caFile},
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("server Start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return &agentTLSFixture{
		server: srv,
		files:  config.TLSConfig{CertFile: certFile, KeyFile: keyFile, CAFile: caFile},
	}
}

func (f *agentTLSFixture) agentConfig(roles ...string) *config.Config {
	cfg := config.Defaults()
	cfg.Server.Role = "agent"
	cfg.Server.Name = "node1"
	cfg.Server.Agents = roles
	// The default bind address is the wildcard; the listener must ignore it.
	cfg.Server.BindAddr = "0.0.0.0"
	cfg.Cluster.BindAddr = "127.0.0.1"
	cfg.AgentServer = &config.AgentConfig{ServerAddress: f.server.ListenAddr(), TLS: f.files}
	return cfg
}

func TestStartProcessorAgentMutualTLSAndPrivateBind(t *testing.T) {
	fx := newAgentTLSFixture(t)
	a := &Agent{Config: fx.agentConfig("antispam"), logger: testLogger()}

	if err := a.startProcessorAgent(context.Background(), "antispam"); err != nil {
		t.Fatalf("startProcessorAgent: %v", err)
	}
	t.Cleanup(func() {
		for _, c := range a.agentClients {
			c.Stop()
		}
		for _, p := range a.agentProcessors {
			p.Stop()
		}
	})

	if got := fx.server.Registry().AgentsForRole("antispam"); len(got) != 1 {
		t.Fatalf("registered antispam agents = %v, want 1", got)
	}
	if len(a.agentProcessors) != 1 || len(a.agentClients) != 1 {
		t.Fatalf("processors = %d, clients = %d, want 1 each", len(a.agentProcessors), len(a.agentClients))
	}

	addr := a.agentProcessors[0].ListenAddr()
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("processor ListenAddr = %q, want host 127.0.0.1 (cluster bind host, not server.bind_addr)", addr)
	}
	if l, lerr := net.Listen("tcp", "[::1]:0"); lerr == nil {
		l.Close()
		if c, derr := net.DialTimeout("tcp", net.JoinHostPort("::1", port), time.Second); derr == nil {
			c.Close()
			t.Errorf("processor answers on [::1]:%s; it must bind only the cluster bind host", port)
		}
	}

	// A remote result with no local processor proves the server dialled the
	// processor over mutual TLS: there is no fallback to hide behind.
	res, err := fx.server.DispatchOrLocal(context.Background(), "antispam",
		&bongov1.QueueEnvelope{Id: "m1", Sender: "s@example.com", Recipients: []string{"r@example.com"}},
		[]byte("From: s@example.com\r\nSubject: hi\r\n\r\nbody\r\n"), nil)
	if err != nil || res == nil {
		t.Fatalf("DispatchOrLocal = %v, %v", res, err)
	}
}

func TestStartProcessorAgentRefusesWildcardBind(t *testing.T) {
	fx := newAgentTLSFixture(t)
	cfg := fx.agentConfig("antispam")
	cfg.Cluster.BindAddr = ""
	a := &Agent{Config: cfg, logger: testLogger()}
	err := a.startProcessorAgent(context.Background(), "antispam")
	if err == nil {
		t.Fatal("startProcessorAgent succeeded with only a wildcard bind address")
	}
	for _, want := range []string{"agent grpc", "cluster.bind_addr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	if len(a.agentProcessors) != 0 {
		t.Error("a processor listener was started")
	}
}

func TestStartProcessorAgentMissingClientTLSNamesKey(t *testing.T) {
	for _, key := range []string{"cert_file", "key_file", "ca_file"} {
		t.Run(key, func(t *testing.T) {
			fx := newAgentTLSFixture(t)
			cfg := fx.agentConfig("antispam")
			switch key {
			case "cert_file":
				cfg.AgentServer.TLS.CertFile = ""
			case "key_file":
				cfg.AgentServer.TLS.KeyFile = ""
			case "ca_file":
				cfg.AgentServer.TLS.CAFile = ""
			}
			a := &Agent{Config: cfg, logger: testLogger()}
			err := a.startProcessorAgent(context.Background(), "antispam")
			if err == nil || !strings.Contains(err.Error(), "agent_server.tls."+key) {
				t.Fatalf("err = %v, want it to name agent_server.tls.%s", err, key)
			}
			if fx.server.Registry().Count() != 0 {
				t.Error("something registered without full TLS material")
			}
		})
	}
}

// TestRunAgentRegistersOverMutualTLS covers the non-processor construction
// site in runAgent: it must build its client through the same TLS wiring.
func TestRunAgentRegistersOverMutualTLS(t *testing.T) {
	fx := newAgentTLSFixture(t)
	cfg := fx.agentConfig("imap")
	cfg.Server.BindAddr = "127.0.0.1"
	isolatePorts(t, cfg)
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.runAgent(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for fx.server.Registry().Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := fx.server.Registry().AgentsForRole("imap"); len(got) != 1 {
		t.Errorf("registered imap agents = %v, want 1", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runAgent: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runAgent did not stop")
	}
}

// TestRunAgentCancelledDuringRegistrationIsCleanShutdown pins the shutdown
// race behind a CI flake: a cancellation that lands while Register is in
// flight must stop the agent without reporting a startup error.
func TestRunAgentCancelledDuringRegistrationIsCleanShutdown(t *testing.T) {
	fx := newAgentTLSFixture(t)
	cfg := fx.agentConfig("imap")
	cfg.Server.BindAddr = "127.0.0.1"
	isolatePorts(t, cfg)
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- a.runAgent(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runAgent after cancellation = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runAgent did not return")
	}
}

func TestRunAgentMissingTLSFailsNamingKey(t *testing.T) {
	fx := newAgentTLSFixture(t)
	cfg := fx.agentConfig("imap")
	cfg.Server.BindAddr = "127.0.0.1"
	isolatePorts(t, cfg)
	cfg.AgentServer.TLS.CAFile = ""
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	err = a.runAgent(context.Background())
	if err == nil || !strings.Contains(err.Error(), "agent_server.tls.ca_file") {
		t.Fatalf("runAgent err = %v, want it to name agent_server.tls.ca_file", err)
	}
}

// TestServerAndDevRolesBuildNoAgentGRPC guards the production fact: the
// server and dev roles construct no agent gRPC client or listener.
func TestServerAndDevRolesBuildNoAgentGRPC(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	deadline := time.Now().Add(20 * time.Second)
	for !a.ready.Load() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	ready := a.ready.Load()
	cancel()
	<-done
	if !ready {
		t.Fatal("dev agent never became ready")
	}
	// Read only after Run has returned, so the reads are ordered after
	// every write Run made.
	if len(a.agentClients) != 0 || len(a.agentProcessors) != 0 || a.AgentGRPC != nil {
		t.Errorf("dev role built agent gRPC state: clients=%d processors=%d", len(a.agentClients), len(a.agentProcessors))
	}
}
