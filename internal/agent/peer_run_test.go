package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

// peerRunConfig is a server-role config that starts quickly in a test: every
// fixed port is replaced by a free one and the optional protocol listeners are
// off. bind is the web and cluster bind address.
func peerRunConfig(t *testing.T, bind, advertise string) *config.Config {
	t.Helper()
	cfg := config.Defaults()
	// Peers join each other's gossip, so gossip needs a real port; it and the
	// redirect port come from the same batch as every other port.
	extra := isolatePortsExtra(t, cfg, 2)
	cfg.Server.Role = "server"
	cfg.Server.Name = "node-" + strconv.Itoa(cfg.Web.Port)
	cfg.Server.BindAddr = bind
	cfg.Server.AdvertiseAddr = advertise
	cfg.Web.PlainHTTP = true
	cfg.Web.SessionSecret = strings.Repeat("s", 40)
	cfg.SMTP.Enabled = false
	cfg.IMAP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.Storage.DataDir = t.TempDir()
	cfg.Cluster.Raft.DataDir = t.TempDir()
	cfg.Cluster.Gossip.Port = extra[0]
	cfg.Web.HTTPRedirectPort = extra[1]
	cfg.Cluster.Gossip.EncryptionKey = "AAAAAAAAAAAAAAAAAAAAAA=="
	return cfg
}

// runAgentUntilReady starts Run and waits for readiness. The returned stop
// cancels and waits for Run to return.
func runAgentUntilReady(t *testing.T, cfg *config.Config) (*Agent, func()) {
	t.Helper()
	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("Run did not return after cancel")
		}
	}
	t.Cleanup(func() { _ = a.Shutdown() })
	deadline := time.Now().Add(20 * time.Second)
	for !a.Ready() {
		select {
		case err := <-done:
			t.Fatalf("Run exited before ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("agent not ready in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return a, stop
}

func dialOpen(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func webStatus(t *testing.T, addr, path string) int {
	t.Helper()
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// A node with peers, started through Run, serves blob sync and join only on
// the mTLS peer listener, advertises it, and syncs blobs over https.
func TestRunWithPeersOpensPeerListener(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	ca := newPeerTestCA(t, "CA")
	cfg := peerRunConfig(t, ext, ext)
	cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, cfg.Cluster.TLS.CAFile = ca.writeFiles(t, ext, true)

	a, stop := runAgentUntilReady(t, cfg)
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()

	peerAddr := net.JoinHostPort(ext, strconv.Itoa(cfg.Cluster.PeerPort))
	webAddr := net.JoinHostPort(ext, strconv.Itoa(cfg.Web.Port))
	hash := strings.Repeat("a", 64)

	// The peer port answers TLS and demands a client certificate.
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	noCert := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	if resp, err := noCert.Get("https://" + peerAddr + "/internal/blob/" + hash); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("peer listener served a client with no certificate (status %d)", resp.StatusCode)
	}
	resp, err := peerClientFor2(t, ca, ext).Get("https://" + peerAddr + "/internal/blob/" + hash)
	if err != nil {
		t.Fatalf("same-CA client refused by the peer listener: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("peer listener status %d for an unsigned blob request, want 401", resp.StatusCode)
	}

	// The public web port does not carry peer traffic.
	if got := webStatus(t, webAddr, "/internal/blob/"+hash); got != http.StatusNotFound {
		t.Errorf("web port /internal/blob = %d, want 404", got)
	}
	if got := webStatus(t, webAddr, "/internal/cluster/join"); got != http.StatusNotFound {
		t.Errorf("web port /internal/cluster/join = %d, want 404", got)
	}
	// Nor does the control listener: with peers they exist only on the peer
	// listener.
	if got := webStatus(t, cfg.Server.ControlAddr, "/internal/blob/"+hash); got != http.StatusNotFound {
		t.Errorf("control port /internal/blob = %d, want 404", got)
	}
	if got := webStatus(t, cfg.Server.ControlAddr, "/internal/cluster/join"); got != http.StatusNotFound {
		t.Errorf("control port /internal/cluster/join = %d, want 404", got)
	}
	// The peer listener binds the cluster address only, not every interface.
	if dialOpen(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Cluster.PeerPort))) {
		t.Error("peer listener answers on loopback; it must bind the cluster address only")
	}

	if a.BlobSync == nil || !a.BlobSync.PeerTLSEnabled() {
		t.Error("blob sync is not using the peer TLS listener")
	}
	if a.peerClient == nil || a.peerTLS == nil {
		t.Error("join client not configured for peer TLS")
	}
	var self string
	for _, m := range a.Gossip.Members() {
		if m.Name == cfg.Server.Name {
			self = m.PeerAddr
		}
	}
	if self != peerAddr {
		t.Errorf("gossip advertises peer_addr %q, want %q", self, peerAddr)
	}

	// Shutdown closes the peer listener.
	stopped = true
	stop()
	if dialOpen(peerAddr) {
		t.Error("peer listener still answers after shutdown")
	}
}

func peerClientFor2(t *testing.T, ca *peerTestCA, ip string) *http.Client {
	t.Helper()
	cfg := ca.mtls(t, ip, ca)
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
}

// Without peers: no listener, no clients, plain http, and the routes stay on
// the public router exactly as before. The production shape (loopback
// advertise, a mail certificate in cluster.tls with no ca_file) is included.
func TestRunWithoutPeersOpensNoPeerListener(t *testing.T) {
	mailCA := newPeerTestCA(t, "mail")
	hash := strings.Repeat("a", 64)

	tests := []struct {
		name string
		cfg  func(t *testing.T) *config.Config
	}{
		{"dev", func(t *testing.T) *config.Config {
			cfg := peerRunConfig(t, "127.0.0.1", "")
			cfg.Server.Role = "dev"
			cfg.Web.SessionSecret = ""
			return cfg
		}},
		{"server loopback advertise", func(t *testing.T) *config.Config {
			return peerRunConfig(t, "127.0.0.1", "127.0.0.1")
		}},
		{"server loopback advertise with mail cert and no ca_file", func(t *testing.T) *config.Config {
			cfg := peerRunConfig(t, "127.0.0.1", "127.0.0.1")
			cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, _ = mailCA.writeFiles(t, "127.0.0.1", false)
			return cfg
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg(t)
			if peerListenerEnabled(cfg) {
				t.Fatal("peerListenerEnabled is true for a node without peers")
			}
			a, stop := runAgentUntilReady(t, cfg)
			defer stop()

			if a.peerServer != nil || a.peerTLS != nil || a.peerClient != nil {
				t.Error("peer listener or clients configured on a node without peers")
			}
			if dialOpen(net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Cluster.PeerPort))) {
				t.Error("something listens on the peer port")
			}
			if a.BlobSync == nil || a.BlobSync.PeerTLSEnabled() {
				t.Error("blob sync should exist and stay on plain http")
			}
			req, _ := a.newJoinRequest(clusterMember("p", "10.0.0.2:443", "10.0.0.2:8302"), "n", "r")
			if req == nil || req.URL.Scheme != "http" {
				t.Errorf("join request scheme = %v, want http", req)
			}
			webAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Web.Port))
			// With nobody to sync with, the blob route is off the public
			// port and answers on the control listener only.
			if got := webStatus(t, webAddr, "/internal/blob/"+hash); got != http.StatusNotFound {
				t.Errorf("public /internal/blob = %d, want 404", got)
			}
			if got := webStatus(t, cfg.Server.ControlAddr, "/internal/blob/"+hash); got != http.StatusUnauthorized {
				t.Errorf("control /internal/blob = %d, want 401 (route on the control listener)", got)
			}
		})
	}
}
