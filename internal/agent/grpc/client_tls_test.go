package agentgrpc

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// rogueServerFiles is a server certificate issued by the rogue CA while the
// ca_file still trusts the good CA: the server would accept our client, so a
// failure can only come from the client rejecting the server certificate.
func (p *testPKI) rogueServerFiles(t *testing.T) TLSFiles {
	t.Helper()
	cert, key := testutil.WriteLeaf(t, p.dir, "rogue-server", p.rogue.Issue(t, "rogue server"))
	return TLSFiles{CertFile: cert, KeyFile: key, CAFile: p.server.CAFile}
}

func newClient(t *testing.T, serverAddr, listenAddr, id string, files TLSFiles) *AgentClient {
	t.Helper()
	cfg := DefaultAgentClientConfig()
	cfg.ServerAddr = serverAddr
	cfg.AgentID = id
	cfg.Roles = []string{"antispam"}
	cfg.ListenAddr = listenAddr
	cfg.DialTimeout = 3 * time.Second
	cfg.TLS = files
	c, err := NewAgentClient(cfg, quietLogger())
	if err != nil {
		t.Fatalf("NewAgentClient: %v", err)
	}
	return c
}

func TestAgentClientAndDispatchMutualTLS(t *testing.T) {
	for _, withLocal := range []bool{true, false} {
		name := "nil local processor"
		if withLocal {
			name = "local processor present"
		}
		t.Run(name, func(t *testing.T) {
			pki := newTestPKI(t)
			srv := startRealAgentServer(t, pki.server, 0)

			remote := &countingProcessor{}
			procSrv := NewAgentProcessorServer(remote, pki.server, quietLogger())
			if err := procSrv.Start("127.0.0.1:0"); err != nil {
				t.Fatalf("processor Start: %v", err)
			}
			defer procSrv.Stop()

			c := newClient(t, srv.ListenAddr(), procSrv.ListenAddr(), "a1", pki.server)
			if err := c.Start(context.Background()); err != nil {
				t.Fatalf("client Start: %v", err)
			}
			defer c.Stop()
			if got := srv.Registry().Count(); got != 1 {
				t.Fatalf("registry count = %d, want 1", got)
			}

			// DispatchOrLocal falls back to local processing on any dial or TLS
			// error, so the processor that ran is the evidence, not the result.
			local := &countingProcessor{}
			var lp pipeline.Processor
			if withLocal {
				lp = local
			}
			res, err := srv.DispatchOrLocal(context.Background(), "antispam", &bongov1.QueueEnvelope{Id: "m"}, []byte("x"), lp)
			if err != nil || res == nil {
				t.Fatalf("DispatchOrLocal = %v, %v", res, err)
			}
			if remote.n.Load() != 1 || local.n.Load() != 0 {
				t.Fatalf("remote ran %d, local ran %d; want 1 and 0", remote.n.Load(), local.n.Load())
			}
		})
	}
}

func TestAgentClientRejectsServerFromOtherCA(t *testing.T) {
	pki := newTestPKI(t)
	srv := startRealAgentServer(t, pki.rogueServerFiles(t), 0)

	c := newClient(t, srv.ListenAddr(), "127.0.0.1:1", "a1", pki.server)
	err := c.Start(context.Background())
	if err == nil {
		c.Stop()
		t.Fatal("Start succeeded against a server certificate from another CA")
	}
	if !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("err = %v, want an x509 unknown authority error", err)
	}
	if srv.Registry().Count() != 0 {
		t.Fatal("handler ran for a rejected connection")
	}
}

func TestDispatchRejectsAgentFromOtherCA(t *testing.T) {
	pki := newTestPKI(t)
	srv := startRealAgentServer(t, pki.server, 0)

	remote := &countingProcessor{}
	procSrv := NewAgentProcessorServer(remote, pki.rogueServerFiles(t), quietLogger())
	if err := procSrv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("processor Start: %v", err)
	}
	defer procSrv.Stop()
	srv.Registry().Register(&RegisteredAgent{ID: "rogue", Roles: []string{"antispam"}, Address: procSrv.ListenAddr()})

	_, err := srv.DispatchOrLocal(context.Background(), "antispam", &bongov1.QueueEnvelope{Id: "m"}, []byte("x"), nil)
	if err == nil || !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("err = %v, want an x509 unknown authority error", err)
	}
	local := &countingProcessor{}
	if _, err := srv.DispatchOrLocal(context.Background(), "antispam", &bongov1.QueueEnvelope{Id: "m"}, []byte("x"), local); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if remote.n.Load() != 0 || local.n.Load() != 1 {
		t.Fatalf("remote ran %d, local ran %d; want 0 and 1", remote.n.Load(), local.n.Load())
	}
}

func TestDispatchBeforeStartNeverDialsPlaintext(t *testing.T) {
	srv := NewAgentServer(nil, nil, quietLogger(), AgentServerConfig{})
	srv.Registry().Register(&RegisteredAgent{ID: "a", Roles: []string{"antispam"}, Address: "127.0.0.1:1"})
	// The registered address refuses connections, so any dial would also
	// fail; the error text proves no dial was attempted at all.
	_, err := srv.DispatchOrLocal(context.Background(), "antispam", &bongov1.QueueEnvelope{}, nil, nil)
	if err == nil {
		t.Fatal("DispatchOrLocal succeeded on a server that was never started")
	}
	if !strings.Contains(err.Error(), "not started") {
		t.Fatalf("DispatchOrLocal error = %v, want the not-started refusal before any dial", err)
	}
}

func TestClientTLSConfig(t *testing.T) {
	pki := newTestPKI(t)
	cfg, err := clientTLSConfig(pki.server, "127.0.0.1:8400")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerName != "127.0.0.1" {
		t.Errorf("ServerName = %q, want 127.0.0.1", cfg.ServerName)
	}
	if cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify must never be set")
	}
	if cfg.MinVersion < 0x0303 {
		t.Errorf("MinVersion = %#x, want TLS 1.2 or later", cfg.MinVersion)
	}
	if len(cfg.Certificates) != 1 || cfg.RootCAs == nil {
		t.Error("client must present its certificate and verify against ca_file")
	}
	if _, err := clientTLSConfig(pki.server, "no-port"); err == nil {
		t.Error("address without a port must be an error")
	}
}

func TestNewAgentClientMissingFilesFailNamingKey(t *testing.T) {
	pki := newTestPKI(t)
	rows := []struct {
		key string
		mut func(f *TLSFiles)
	}{
		{"agent_server.tls.cert_file", func(f *TLSFiles) { f.CertFile = "" }},
		{"agent_server.tls.key_file", func(f *TLSFiles) { f.KeyFile = "" }},
		{"agent_server.tls.ca_file", func(f *TLSFiles) { f.CAFile = "" }},
	}
	for _, row := range rows {
		t.Run(row.key, func(t *testing.T) {
			f := pki.server
			row.mut(&f)
			cfg := DefaultAgentClientConfig()
			cfg.ServerAddr = "127.0.0.1:8400"
			cfg.TLS = f
			c, err := NewAgentClient(cfg, quietLogger())
			if err == nil || !strings.Contains(err.Error(), row.key) {
				t.Fatalf("err = %v, want it to name %q", err, row.key)
			}
			if c != nil {
				t.Fatal("a usable client was returned")
			}
		})
	}
}

func TestListenersBindOnlyTheBindHost(t *testing.T) {
	if l, err := net.Listen("tcp", "[::1]:0"); err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	} else {
		l.Close()
	}
	pki := newTestPKI(t)
	srv := startRealAgentServer(t, pki.server, 0)
	proc := NewAgentProcessorServer(&countingProcessor{}, pki.server, quietLogger())
	if err := proc.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer proc.Stop()

	for name, addr := range map[string]string{"AgentServer": srv.ListenAddr(), "AgentProcessorServer": proc.ListenAddr()} {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || host != "127.0.0.1" {
			t.Errorf("%s ListenAddr = %q, want host 127.0.0.1 (err %v)", name, addr, err)
		}
		// A wildcard listener would also answer on the IPv6 loopback.
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("::1", port), time.Second)
		if err == nil {
			conn.Close()
			t.Errorf("%s answers on [::1]:%s; it must bind only the bind host", name, port)
		}
	}
}

func TestStartRefusesNonSpecificBindHost(t *testing.T) {
	pki := newTestPKI(t)
	for _, host := range []string{"", "0.0.0.0", "::", "not-an-ip", "localhost"} {
		t.Run("AgentServer/"+host, func(t *testing.T) {
			port := freePort(t)
			srv := NewAgentServer(nil, nil, quietLogger(), AgentServerConfig{Port: port, BindHost: host, AgentTTL: DefaultAgentTTL, ReapInterval: DefaultReapInterval, TLS: pki.server})
			if err := srv.Start(); err == nil {
				srv.Stop()
				t.Fatalf("Start accepted BindHost %q", host)
			}
			l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				t.Fatalf("port not free after refused Start: %v", err)
			}
			l.Close()
		})
		t.Run("AgentProcessorServer/"+host, func(t *testing.T) {
			port := freePort(t)
			srv := NewAgentProcessorServer(&countingProcessor{}, pki.server, quietLogger())
			if err := srv.Start(net.JoinHostPort(host, strconv.Itoa(port))); err == nil {
				srv.Stop()
				t.Fatalf("Start accepted host %q", host)
			}
			l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				t.Fatalf("port not free after refused Start: %v", err)
			}
			l.Close()
		})
	}
}
