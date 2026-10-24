package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bongo-project/bongo/internal/config"
)

// testCA is a throwaway CA generated in the test; no key material is committed.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue returns PEM cert and key for a leaf valid as both server and client
// for the given IP.
func (ca *testCA) issue(t *testing.T, ip string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "node " + ip},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// writeNodeFiles writes cert, key and CA files into dir and returns the paths.
func (ca *testCA) writeNodeFiles(t *testing.T, ip string) (certFile, keyFile, caFile string) {
	t.Helper()
	dir := t.TempDir()
	c, k := ca.issue(t, ip)
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	caFile = filepath.Join(dir, "ca.pem")
	for p, b := range map[string][]byte{certFile: c, keyFile: k, caFile: ca.pem} {
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return certFile, keyFile, caFile
}

// mtlsConfig builds the config the raft listener is required to use: the CA is
// both the client and the server trust anchor.
func (ca *testCA) mtlsConfig(t *testing.T, ip string, trust *testCA) *tls.Config {
	t.Helper()
	c, k := ca.issue(t, ip)
	pair, err := tls.X509KeyPair(c, k)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(trust.cert)
	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		RootCAs:      pool,
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
}

// exchange dials from client to server and forces both handshakes, returning
// the first error from either side.
func exchange(t *testing.T, client, server *TLSStreamLayer) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		conn, err := server.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		tc := conn.(*tls.Conn)
		if err := tc.Handshake(); err != nil {
			errCh <- err
			return
		}
		buf := make([]byte, 4)
		if _, err := conn.Read(buf); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	conn, err := client.Dial(raftAddr(server), 2*time.Second)
	if err != nil {
		<-errCh
		return err
	}
	defer conn.Close()
	if err := conn.(*tls.Conn).Handshake(); err != nil {
		<-errCh
		return err
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		<-errCh
		return err
	}
	return <-errCh
}

func raftAddr(l *TLSStreamLayer) raft.ServerAddress {
	return raft.ServerAddress(l.listener.Addr().String())
}

func TestTLSStreamLayerRejectsForeignCA(t *testing.T) {
	caA := newTestCA(t, "CA-A")
	caB := newTestCA(t, "CA-B")

	newLayer := func(t *testing.T, cfg *tls.Config) *TLSStreamLayer {
		t.Helper()
		l, err := NewTLSStreamLayer("127.0.0.1:0", nil, cfg)
		if err != nil {
			t.Fatalf("NewTLSStreamLayer: %v", err)
		}
		t.Cleanup(func() { l.Close() })
		return l
	}

	t.Run("same CA succeeds", func(t *testing.T) {
		srv := newLayer(t, caA.mtlsConfig(t, "127.0.0.1", caA))
		cli := newLayer(t, caA.mtlsConfig(t, "127.0.0.1", caA))
		if err := exchange(t, cli, srv); err != nil {
			t.Fatalf("same-CA exchange failed: %v", err)
		}
	})

	t.Run("client from CA-B rejected by CA-A server", func(t *testing.T) {
		srv := newLayer(t, caA.mtlsConfig(t, "127.0.0.1", caA))
		// Valid, well-formed client that trusts CA-A's server cert but whose own
		// cert is from CA-B: the only defect is the issuer.
		cfg := caB.mtlsConfig(t, "127.0.0.1", caA)
		cli := newLayer(t, cfg)
		if err := exchange(t, cli, srv); err == nil {
			t.Fatal("handshake with a certificate from another CA succeeded")
		}
	})

	t.Run("client rejects server from CA-B", func(t *testing.T) {
		srv := newLayer(t, caB.mtlsConfig(t, "127.0.0.1", caA)) // server cert from CA-B
		cli := newLayer(t, caA.mtlsConfig(t, "127.0.0.1", caA))
		if err := exchange(t, cli, srv); err == nil {
			t.Fatal("client accepted a server certificate from another CA")
		}
	})

	t.Run("client without certificate rejected", func(t *testing.T) {
		srv := newLayer(t, caA.mtlsConfig(t, "127.0.0.1", caA))
		pool := x509.NewCertPool()
		pool.AddCert(caA.cert)
		errCh := make(chan error, 1)
		go func() {
			conn, err := srv.Accept()
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Close()
			errCh <- conn.(*tls.Conn).Handshake()
		}()
		// Otherwise valid client: trusts the server and speaks TLS 1.2+, but
		// presents no certificate.
		conn, err := tls.Dial("tcp", srv.listener.Addr().String(), &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
		if err == nil {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, err = conn.Read(make([]byte, 1))
			conn.Close()
		}
		if serr := <-errCh; serr == nil {
			t.Fatal("server completed a handshake without a client certificate")
		}
		_ = err
	})

	t.Run("dial config never skips verification", func(t *testing.T) {
		cli := newLayer(t, caA.mtlsConfig(t, "127.0.0.1", caA))
		if cli.dialConfig().InsecureSkipVerify {
			t.Fatal("InsecureSkipVerify set on raft dialer")
		}
	})
}

func TestNewTLSStreamLayerRequiresCAPool(t *testing.T) {
	ca := newTestCA(t, "CA")
	full := ca.mtlsConfig(t, "127.0.0.1", ca)

	noRoots := full.Clone()
	noRoots.RootCAs = nil
	noClientCAs := full.Clone()
	noClientCAs.ClientCAs = nil
	noClientAuth := full.Clone()
	noClientAuth.ClientAuth = tls.NoClientCert

	anyCert := full.Clone()
	anyCert.ClientAuth = tls.RequireAnyClientCert
	ifGiven := full.Clone()
	ifGiven.ClientAuth = tls.VerifyClientCertIfGiven
	requestOnly := full.Clone()
	requestOnly.ClientAuth = tls.RequestClientCert

	for name, cfg := range map[string]*tls.Config{
		"no RootCAs": noRoots, "no ClientCAs": noClientCAs, "no client auth": noClientAuth,
		"require any client cert": anyCert, "verify if given": ifGiven, "request only": requestOnly,
	} {
		t.Run(name, func(t *testing.T) {
			l, err := NewTLSStreamLayer("127.0.0.1:0", nil, cfg)
			if err == nil {
				l.Close()
				t.Fatal("NewTLSStreamLayer accepted an unauthenticated TLS config")
			}
		})
	}
}

func TestRaftSourceNeverSkipsVerify(t *testing.T) {
	src, err := os.ReadFile("raft.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "InsecureSkipVerify") {
		t.Fatal("raft.go must not reference InsecureSkipVerify")
	}
}

// peeredConfig returns a server config with peers: a routable advertise
// address and no cluster.tls.
func peeredConfig(t *testing.T, advertise string, port int) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Server.Name = "tls-test"
	cfg.Storage.DataDir = t.TempDir()
	cfg.Server.BindAddr = "0.0.0.0"
	cfg.Server.AdvertiseAddr = advertise
	cfg.Cluster.Raft.Port = port
	return cfg
}

func TestNewRaftNodePeeredRequiresTLSFiles(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	ca := newTestCA(t, "CA")
	goodCert, goodKey, goodCA := ca.writeNodeFiles(t, ext)
	missing := filepath.Join(t.TempDir(), "absent.pem")
	garbage := filepath.Join(t.TempDir(), "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		cert, key, ca  string
		wantInErr      string
		skipUnlessRoot bool
	}{
		{"no tls at all", "", "", "", "cluster.tls", false},
		{"cert missing", missing, goodKey, goodCA, missing, false},
		{"key missing", goodCert, missing, goodCA, missing, false},
		{"ca missing", goodCert, goodKey, missing, missing, false},
		{"cert garbage", garbage, goodKey, goodCA, garbage, false},
		{"ca empty", goodCert, goodKey, "", "cluster.tls.ca_file", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := freePort(t)
			cfg := peeredConfig(t, ext, port)
			cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, cfg.Cluster.TLS.CAFile = tt.cert, tt.key, tt.ca

			_, err := startRaftWith(t, cfg)
			if err == nil {
				t.Fatal("NewRaftNode succeeded with unusable cluster.tls on a peered node")
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantInErr)
			}
			addr := net.JoinHostPort(ext, strconv.Itoa(port))
			if dialable(addr) || dialable(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
				t.Error("a raft listener is open after the start failure")
			}
		})
	}

	t.Run("unreadable cert", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores file modes")
		}
		unreadable := filepath.Join(t.TempDir(), "locked.pem")
		if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
			t.Fatal(err)
		}
		port := freePort(t)
		cfg := peeredConfig(t, ext, port)
		cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, cfg.Cluster.TLS.CAFile = unreadable, goodKey, goodCA
		_, err := startRaftWith(t, cfg)
		if err == nil || !strings.Contains(err.Error(), unreadable) {
			t.Fatalf("want error naming %s, got %v", unreadable, err)
		}
		if dialable(net.JoinHostPort(ext, strconv.Itoa(port))) {
			t.Error("a raft listener is open after the start failure")
		}
	})

	t.Run("join with loopback advertise is peered", func(t *testing.T) {
		port := freePort(t)
		cfg := peeredConfig(t, "127.0.0.1", port)
		cfg.Cluster.Join = []string{"10.0.0.2:8301"}
		_, err := startRaftWith(t, cfg)
		if err == nil || !strings.Contains(err.Error(), "cluster.tls") {
			t.Fatalf("want cluster.tls error, got %v", err)
		}
		if dialable(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
			t.Error("a raft listener is open after the start failure")
		}
	})
}

func TestNewRaftNodePeeredBindsAdvertiseAndRequiresMTLS(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	ca := newTestCA(t, "CA")
	port := freePort(t)
	cfg := peeredConfig(t, ext, port)
	cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, cfg.Cluster.TLS.CAFile = ca.writeNodeFiles(t, ext)
	node, err := startRaftWith(t, cfg)
	if err != nil {
		t.Fatalf("NewRaftNode: %v", err)
	}
	defer node.Shutdown()

	addr := net.JoinHostPort(ext, strconv.Itoa(port))
	if !dialable(addr) {
		t.Fatalf("raft not reachable on %s", addr)
	}

	// A client with no certificate must not get a session.
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	conn, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
	if err == nil {
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, rerr := conn.Read(make([]byte, 1)); rerr == nil {
			t.Fatal("server completed a session without requiring a client certificate")
		}
	}
}

// A peered raft listener must reject a client whose certificate is valid but
// issued by another CA.
func TestNewRaftNodePeeredRejectsForeignClientCert(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	caA := newTestCA(t, "CA-A")
	caB := newTestCA(t, "CA-B")
	port := freePort(t)
	cfg := peeredConfig(t, ext, port)
	cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, cfg.Cluster.TLS.CAFile = caA.writeNodeFiles(t, ext)
	node, err := startRaftWith(t, cfg)
	if err != nil {
		t.Fatalf("NewRaftNode: %v", err)
	}
	defer node.Shutdown()
	addr := net.JoinHostPort(ext, strconv.Itoa(port))

	try := func(clientCfg *tls.Config) error {
		conn, err := tls.Dial("tcp", addr, clientCfg)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		return err
	}
	// Positive control: same-CA client gets past the handshake (read then
	// blocks until the deadline, which is not a TLS alert).
	good := caA.mtlsConfig(t, ext, caA)
	if err := try(good); err != nil {
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("same-CA client rejected: %v", err)
		}
	}
	// Valid client certificate, wrong CA: the client trusts the server, so the
	// only reason to fail is the server rejecting the client certificate.
	bad := caB.mtlsConfig(t, ext, caA)
	// Go only offers a certificate the server's CA list accepts; force it to
	// send the CB-issued one so the server must do the rejecting.
	badCert := bad.Certificates[0]
	bad.Certificates = nil
	bad.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return &badCert, nil
	}
	err = try(bad)
	var ne net.Error
	if err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		t.Fatalf("foreign-CA client certificate accepted (err=%v)", err)
	}
}

// cluster.bind_addr on a routable interface with a loopback advertise address
// must not open a plaintext raft listener.
func TestNewRaftNodeRoutableClusterBindNeverPlaintext(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	port := freePort(t)
	cfg := peeredConfig(t, "127.0.0.1", port)
	cfg.Cluster.BindAddr = ext
	node, err := startRaftWith(t, cfg)
	if err == nil {
		node.Shutdown()
	}
	if dialable(net.JoinHostPort(ext, strconv.Itoa(port))) {
		t.Fatal("plaintext raft reachable on a routable cluster.bind_addr")
	}
	if err == nil {
		t.Fatal("expected a start failure without cluster.tls")
	}
}

// LoadPeerTLS must stay strict on its own, not rely on raft having
// strict-loaded first: a certificate without ca_file is refused, and with
// ca_file the CA is both trust anchors and client certificates are required.
func TestLoadPeerTLSIsStrict(t *testing.T) {
	ca := newTestCA(t, "CA")
	cfg := peeredConfig(t, "127.0.0.1", 0)
	cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, _ = ca.writeNodeFiles(t, "127.0.0.1")
	if _, err := LoadPeerTLS(cfg); err == nil {
		t.Fatal("LoadPeerTLS accepted a certificate without ca_file")
	}

	_, _, cfg.Cluster.TLS.CAFile = ca.writeNodeFiles(t, "127.0.0.1")
	tc, err := LoadPeerTLS(cfg)
	if err != nil {
		t.Fatalf("LoadPeerTLS with ca_file: %v", err)
	}
	if tc.RootCAs == nil || tc.ClientCAs == nil || tc.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("LoadPeerTLS: RootCAs set %v, ClientCAs set %v, ClientAuth %v", tc.RootCAs != nil, tc.ClientCAs != nil, tc.ClientAuth)
	}
}
