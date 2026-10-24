package agent

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
)

func peerTestSecret() []byte { return []byte("peer-listener-secret-0123456789abcdef") }

// peerClientFor builds an https client presenting cert (forced, so Go does not
// silently drop a certificate the server's CA list would not accept) and
// trusting trust.
func peerClientFor(t *testing.T, own *peerTestCA, trust *peerTestCA) *http.Client {
	t.Helper()
	cfg := own.mtls(t, "127.0.0.1", trust)
	pair := cfg.Certificates[0]
	cfg.Certificates = nil
	cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &pair, nil }
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
}

func newPeerTestAgent(t *testing.T, ca *peerTestCA) (*Agent, string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "server"
	cfg.Cluster.BindAddr = "127.0.0.1"
	cfg.Cluster.PeerPort = freePort(t)
	blobs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		Config:  cfg,
		logger:  testLogger(),
		Blobs:   blobs,
		Raft:    &cluster.RaftNode{},
		peerTLS: ca.mtls(t, "127.0.0.1", ca),
	}
	t.Cleanup(func() { _ = a.shutdownPeerServer() })
	return a, "https://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Cluster.PeerPort))
}

func TestPeerListenerRequiresClientCertificate(t *testing.T) {
	caA := newPeerTestCA(t, "CA-A")
	caB := newPeerTestCA(t, "CA-B")
	a, base := newPeerTestAgent(t, caA)

	var handled atomic.Int32
	if err := a.servePeer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handled.Add(1)
		w.WriteHeader(http.StatusOK)
	})); err != nil {
		t.Fatal(err)
	}

	// Positive control: a same-CA client reaches the handler.
	resp, err := peerClientFor(t, caA, caA).Get(base + "/x")
	if err != nil {
		t.Fatalf("same-CA client refused: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || handled.Load() != 1 {
		t.Fatalf("same-CA status %d, handler runs %d; want 200 and 1", resp.StatusCode, handled.Load())
	}
	handled.Store(0)

	t.Run("no client certificate", func(t *testing.T) {
		pool := caA.mtls(t, "127.0.0.1", caA).RootCAs
		c := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}}
		if resp, err := c.Get(base + "/x"); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("request without a client certificate got status %d", resp.StatusCode)
		}
		if handled.Load() != 0 {
			t.Errorf("handler ran %d times", handled.Load())
		}
	})
	t.Run("client certificate from another CA", func(t *testing.T) {
		// The client trusts the server; only its own issuer is wrong.
		if resp, err := peerClientFor(t, caB, caA).Get(base + "/x"); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("foreign-CA client got status %d", resp.StatusCode)
		}
		if handled.Load() != 0 {
			t.Errorf("handler ran %d times", handled.Load())
		}
	})
}

func TestPeerListenerServesBlobsAndJoinWithHMAC(t *testing.T) {
	ca := newPeerTestCA(t, "CA")
	a, base := newPeerTestAgent(t, ca)
	secret := peerTestSecret()
	hash, err := a.Blobs.Put([]byte("served over the peer listener"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startPeerListener(secret); err != nil {
		t.Fatal(err)
	}
	client := peerClientFor(t, ca, ca)

	t.Run("blob with a valid signature", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, base+"/internal/blob/"+hash, nil)
		security.SignBlobSyncRequest(req, secret)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || string(body) != "served over the peer listener" {
			t.Fatalf("status %d body %q", resp.StatusCode, body)
		}
	})
	t.Run("blob without a signature is refused even over mTLS", func(t *testing.T) {
		resp, err := client.Get(base + "/internal/blob/" + hash)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", resp.StatusCode)
		}
	})
	t.Run("join without a signature is refused even over mTLS", func(t *testing.T) {
		resp, err := client.Post(base+"/internal/cluster/join", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", resp.StatusCode)
		}
	})
	t.Run("only the peer routes are served", func(t *testing.T) {
		resp, err := client.Get(base + "/internal/cluster/members")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status %d, want 404", resp.StatusCode)
		}
	})
}

func TestPeerServerHasExplicitTimeouts(t *testing.T) {
	ca := newPeerTestCA(t, "CA")
	cfg := ca.mtls(t, "127.0.0.1", ca)
	srv := newPeerServer("127.0.0.1:0", http.NotFoundHandler(), cfg, testLogger())
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Errorf("missing timeout: %+v", srv)
	}
	if srv.TLSConfig == cfg {
		t.Error("server shares the caller's tls.Config instead of cloning it")
	}
	if srv.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert || srv.TLSConfig.MinVersion < tls.VersionTLS12 {
		t.Errorf("weak TLS config: ClientAuth=%v MinVersion=%x", srv.TLSConfig.ClientAuth, srv.TLSConfig.MinVersion)
	}
}

// With peers the peer routes leave the public router; without them they stay.
func TestPublicRouterPeerRoutes(t *testing.T) {
	hash := strings.Repeat("a", 64)
	tests := []struct {
		name      string
		advertise string
		want      int
	}{
		{"node with peers", "10.0.0.5", http.StatusNotFound},
		{"single node", "127.0.0.1", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Server.Role = "server"
			cfg.Server.AdvertiseAddr = tt.advertise
			blobs, err := store.NewBlobStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			a := &Agent{Config: cfg, logger: testLogger(), Blobs: blobs, Raft: &cluster.RaftNode{}}
			r := chi.NewRouter()
			a.registerControlPeerRoutes(r, peerTestSecret())
			srv := httptest.NewServer(r)
			defer srv.Close()

			for _, req := range []struct{ method, path string }{
				{http.MethodGet, "/internal/blob/" + hash},
				{http.MethodHead, "/internal/blob/" + hash},
				{http.MethodPost, "/internal/cluster/join"},
			} {
				r, _ := http.NewRequest(req.method, srv.URL+req.path, nil)
				resp, err := http.DefaultClient.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != tt.want {
					t.Errorf("%s %s = %d, want %d", req.method, req.path, resp.StatusCode, tt.want)
				}
			}
		})
	}
}
