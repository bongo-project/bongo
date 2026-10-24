package agentgrpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// testPKI is a good CA, a rogue CA, and server credentials on disk.
type testPKI struct {
	dir    string
	ca     *testutil.CA
	rogue  *testutil.CA
	server TLSFiles
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	dir := t.TempDir()
	ca := testutil.NewCA(t, "good CA")
	rogue := testutil.NewCA(t, "rogue CA")
	certFile, keyFile := testutil.WriteLeaf(t, dir, "server", ca.Issue(t, "server"))
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, ca.CertPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return &testPKI{
		dir: dir, ca: ca, rogue: rogue,
		server: TLSFiles{CertFile: certFile, KeyFile: keyFile, CAFile: caFile},
	}
}

// clientCreds builds client transport credentials that trust the good CA.
// A nil cert means the client presents no certificate.
func (p *testPKI) clientCreds(cert *tls.Certificate) credentials.TransportCredentials {
	pool := x509.NewCertPool()
	pool.AddCert(p.ca.Cert)
	cfg := &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	if cert != nil {
		// GetClientCertificate forces the certificate to be sent even when it
		// does not chain to a CA the server advertised; with Certificates Go
		// would silently send none and the wrong-CA case would prove nothing.
		c := *cert
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &c, nil }
	}
	return credentials.NewTLS(cfg)
}

// goodClientCreds presents a certificate signed by the good CA.
func (p *testPKI) goodClientCreds(t *testing.T) credentials.TransportCredentials {
	c := p.ca.Issue(t, "client")
	return p.clientCreds(&c)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestServerTLSConfig(t *testing.T) {
	pki := newTestPKI(t)
	cfg, err := serverTLSConfig(pki.server)
	if err != nil {
		t.Fatalf("serverTLSConfig: %v", err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want TLS 1.2", cfg.MinVersion)
	}
	if cfg.ClientCAs == nil {
		t.Error("ClientCAs is nil")
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("Certificates = %d, want 1", len(cfg.Certificates))
	}
}

func TestServerTLSConfigErrors(t *testing.T) {
	pki := newTestPKI(t)
	missing := filepath.Join(pki.dir, "nope.pem")
	tests := []struct {
		name string
		mut  func(f *TLSFiles)
		want string
	}{
		{"cert unset", func(f *TLSFiles) { f.CertFile = "" }, "agent_server.tls.cert_file"},
		{"key unset", func(f *TLSFiles) { f.KeyFile = "" }, "agent_server.tls.key_file"},
		{"ca unset", func(f *TLSFiles) { f.CAFile = "" }, "agent_server.tls.ca_file"},
		{"cert missing file", func(f *TLSFiles) { f.CertFile = missing }, "nope.pem"},
		{"ca missing file", func(f *TLSFiles) { f.CAFile = missing }, "nope.pem"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := pki.server
			tt.mut(&f)
			_, err := serverTLSConfig(f)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// countingProcessor counts how many times the handler ran.
type countingProcessor struct{ n atomic.Int32 }

func (c *countingProcessor) Name() string { return "counting" }
func (c *countingProcessor) Process(_ context.Context, _ *bongov1.QueueEnvelope, _ io.Reader) (*bongov1.StageResult, error) {
	c.n.Add(1)
	return &bongov1.StageResult{Stage: "counting", Action: "pass"}, nil
}

// startRealAgentServer starts an AgentServer through Start (not a hand-built
// grpc.Server) with TLS files and an ephemeral port.
func startRealAgentServer(t *testing.T, files TLSFiles, port int) *AgentServer {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewAgentServer(nil, nil, logger, AgentServerConfig{
		Port: port, BindHost: "127.0.0.1", AgentTTL: DefaultAgentTTL, ReapInterval: DefaultReapInterval, TLS: files,
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func TestStartRequiresTLSKeysAndLeavesPortFree(t *testing.T) {
	pki := newTestPKI(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rows := []struct {
		key string
		mut func(f *TLSFiles)
	}{
		{"agent_server.tls.cert_file", func(f *TLSFiles) { f.CertFile = "" }},
		{"agent_server.tls.key_file", func(f *TLSFiles) { f.KeyFile = "" }},
		{"agent_server.tls.ca_file", func(f *TLSFiles) { f.CAFile = "" }},
	}
	for _, kind := range []string{"AgentServer", "AgentProcessorServer"} {
		for _, row := range rows {
			t.Run(kind+"/"+row.key, func(t *testing.T) {
				f := pki.server
				row.mut(&f)
				port := freePort(t)
				var err error
				probe := fmt.Sprintf("127.0.0.1:%d", port)
				if kind == "AgentServer" {
					srv := NewAgentServer(nil, nil, logger, AgentServerConfig{Port: port, BindHost: "127.0.0.1", TLS: f})
					err = srv.Start()
				} else {
					srv := NewAgentProcessorServer(&countingProcessor{}, f, logger)
					err = srv.Start(fmt.Sprintf("127.0.0.1:%d", port))
				}
				if err == nil || !strings.Contains(err.Error(), row.key) {
					t.Fatalf("Start err = %v, want it to name %q", err, row.key)
				}
				l, lerr := net.Listen("tcp", probe)
				if lerr != nil {
					t.Fatalf("port %d not free after failed Start: %v", port, lerr)
				}
				l.Close()
			})
		}
	}
}

func TestStartUnreadableTLSFileLeavesPortFree(t *testing.T) {
	pki := newTestPKI(t)
	f := pki.server
	f.CAFile = filepath.Join(pki.dir, "absent.pem")
	port := freePort(t)
	srv := NewAgentServer(nil, nil, nil, AgentServerConfig{Port: port, BindHost: "127.0.0.1", TLS: f})
	if err := srv.Start(); err == nil {
		t.Fatal("Start succeeded with unreadable CA file")
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port not free: %v", err)
	}
	l.Close()
}

type clientCase struct {
	name      string
	creds     func(t *testing.T, p *testPKI) credentials.TransportCredentials
	wantAllow bool
}

func clientMatrix() []clientCase {
	return []clientCase{
		{"CA-signed client cert", func(t *testing.T, p *testPKI) credentials.TransportCredentials { return p.goodClientCreds(t) }, true},
		{"no client cert", func(t *testing.T, p *testPKI) credentials.TransportCredentials { return p.clientCreds(nil) }, false},
		{"client cert from other CA", func(t *testing.T, p *testPKI) credentials.TransportCredentials {
			c := p.rogue.Issue(t, "rogue client")
			return p.clientCreds(&c)
		}, false},
		{"plaintext", func(t *testing.T, p *testPKI) credentials.TransportCredentials { return insecure.NewCredentials() }, false},
	}
}

func TestAgentServerMutualTLS(t *testing.T) {
	for _, tc := range clientMatrix() {
		t.Run(tc.name, func(t *testing.T) {
			pki := newTestPKI(t)
			srv := startRealAgentServer(t, pki.server, 0)
			conn, err := grpc.NewClient(srv.ListenAddr(), grpc.WithTransportCredentials(tc.creds(t, pki)))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = bongov1.NewAgentServiceClient(conn).Register(ctx, &bongov1.RegisterRequest{
				AgentId: "a1", Roles: []string{"antispam"}, Address: "127.0.0.1:1",
			})
			got := srv.Registry().Count()
			if tc.wantAllow {
				if err != nil || got != 1 {
					t.Fatalf("Register err = %v, handler runs = %d, want success and 1", err, got)
				}
				return
			}
			if err == nil {
				t.Fatal("Register succeeded, want failure")
			}
			if got != 0 {
				t.Fatalf("handler ran %d times for a rejected client", got)
			}
		})
	}
}

func TestAgentProcessorServerMutualTLS(t *testing.T) {
	for _, tc := range clientMatrix() {
		t.Run(tc.name, func(t *testing.T) {
			pki := newTestPKI(t)
			proc := &countingProcessor{}
			srv := NewAgentProcessorServer(proc, pki.server, nil)
			if err := srv.Start("127.0.0.1:0"); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer srv.Stop()
			conn, err := grpc.NewClient(srv.ListenAddr(), grpc.WithTransportCredentials(tc.creds(t, pki)))
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err = bongov1.NewAgentServiceClient(conn).ProcessMessage(ctx, &bongov1.ProcessRequest{
				Envelope: &bongov1.QueueEnvelope{Id: "m1"}, Message: []byte("Subject: x\r\n\r\nbody"),
			})
			got := int(proc.n.Load())
			if tc.wantAllow {
				if err != nil || got != 1 {
					t.Fatalf("ProcessMessage err = %v, handler runs = %d, want success and 1", err, got)
				}
				return
			}
			if err == nil {
				t.Fatal("ProcessMessage succeeded, want failure")
			}
			if got != 0 {
				t.Fatalf("handler ran %d times for a rejected client", got)
			}
		})
	}
}

var _ pipeline.Processor = (*countingProcessor)(nil)
