package agentgrpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
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
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// testCA is a throwaway certificate authority generated inside a test.
type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	return &testCA{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// issue returns a leaf certificate signed by the CA with both EKUs, so the
// same pair can serve in either direction.
func (ca *testCA) issue(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func writePEM(t *testing.T, dir, name, blockType string, der []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// writeLeaf stores a leaf certificate and key as PEM files.
func writeLeaf(t *testing.T, dir, prefix string, c tls.Certificate) (certFile, keyFile string) {
	t.Helper()
	keyDER, err := x509.MarshalECPrivateKey(c.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return writePEM(t, dir, prefix+".crt", "CERTIFICATE", c.Certificate[0]),
		writePEM(t, dir, prefix+".key", "EC PRIVATE KEY", keyDER)
}

// testPKI is a good CA, a rogue CA, and server credentials on disk.
type testPKI struct {
	dir    string
	ca     *testCA
	rogue  *testCA
	server TLSFiles
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	dir := t.TempDir()
	ca := newTestCA(t, "good CA")
	rogue := newTestCA(t, "rogue CA")
	certFile, keyFile := writeLeaf(t, dir, "server", ca.issue(t, "server"))
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, ca.certPEM, 0o600); err != nil {
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
	pool.AddCert(p.ca.cert)
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
	c := p.ca.issue(t, "client")
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
		Port: port, AgentTTL: DefaultAgentTTL, ReapInterval: DefaultReapInterval, TLS: files,
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
				// Probe with the same bind form Start uses; a specific
				// address can bind beside a wildcard listener on BSD.
				probe := fmt.Sprintf("127.0.0.1:%d", port)
				if kind == "AgentServer" {
					probe = fmt.Sprintf(":%d", port)
					srv := NewAgentServer(nil, nil, logger, AgentServerConfig{Port: port, TLS: f})
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
	srv := NewAgentServer(nil, nil, nil, AgentServerConfig{Port: port, TLS: f})
	if err := srv.Start(); err == nil {
		t.Fatal("Start succeeded with unreadable CA file")
	}
	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
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
			c := p.rogue.issue(t, "rogue client")
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
