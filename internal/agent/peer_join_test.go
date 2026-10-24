package agent

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// logCapture is a slog.Handler that records every record.
type logCapture struct {
	mu    sync.Mutex
	lines []string
	peers []string
}

func newCaptureLogger() (*slog.Logger, *logCapture) {
	c := &logCapture{}
	return slog.New(c), c
}

func (c *logCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *logCapture) WithAttrs([]slog.Attr) slog.Handler       { return c }
func (c *logCapture) WithGroup(string) slog.Handler            { return c }
func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	b.WriteString(r.Level.String() + " " + r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.Key + "=" + a.Value.String())
		if a.Key == "peer" && r.Level >= slog.LevelWarn {
			c.peers = append(c.peers, a.Value.String())
		}
		return true
	})
	c.lines = append(c.lines, b.String())
	return nil
}

func (c *logCapture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// warnedAbout reports whether a warn-or-above record carries peer=name.
func (c *logCapture) warnedAbout(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.peers {
		if p == name {
			return true
		}
	}
	return false
}

func TestNewJoinRequestTarget(t *testing.T) {
	m := cluster.MemberInfo{Name: "peer", HTTPAddr: "0.0.0.0:443", PeerAddr: "10.0.0.2:8302"}
	ca := newPeerTestCA(t, "CA")

	t.Run("with peer TLS the request goes to https PeerAddr", func(t *testing.T) {
		a := &Agent{Config: config.Defaults(), logger: testLogger(), internalSecret: []byte("s"), peerTLS: ca.mtls(t, "127.0.0.1", ca)}
		req, err := a.newJoinRequest(m, "n", "10.0.0.1:8300")
		if err != nil {
			t.Fatal(err)
		}
		if got, want := req.URL.String(), "https://10.0.0.2:8302/internal/cluster/join"; got != want {
			t.Errorf("url = %s, want %s", got, want)
		}
	})
	t.Run("without peer TLS the request goes to http HTTPAddr", func(t *testing.T) {
		a := &Agent{Config: config.Defaults(), logger: testLogger(), internalSecret: []byte("s")}
		req, err := a.newJoinRequest(m, "n", "10.0.0.1:8300")
		if err != nil {
			t.Fatal(err)
		}
		if got, want := req.URL.String(), "http://0.0.0.0:443/internal/cluster/join"; got != want {
			t.Errorf("url = %s, want %s", got, want)
		}
	})
	t.Run("with peer TLS a member without PeerAddr has no target", func(t *testing.T) {
		a := &Agent{Config: config.Defaults(), logger: testLogger(), internalSecret: []byte("s"), peerTLS: ca.mtls(t, "127.0.0.1", ca)}
		if _, err := a.newJoinRequest(cluster.MemberInfo{Name: "old", HTTPAddr: "10.0.0.9:443"}, "n", "r"); err == nil {
			t.Fatal("built a join request for a member with no peer address")
		}
	})
}

// startJoinStub serves /internal/cluster/join behind the real internal-auth
// middleware over mTLS, counting handler runs.
func startJoinStub(t *testing.T, serverCfg *tls.Config, secret []byte, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	h := middleware.RequireInternalAuth(secret)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = serverCfg.Clone()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestSendJoinOverPeerTLS(t *testing.T) {
	caA := newPeerTestCA(t, "CA-A")
	caB := newPeerTestCA(t, "CA-B")
	secret := []byte("join-secret-join-secret-join-secret")

	newAgent := func(t *testing.T, log *slog.Logger) *Agent {
		cfg := caA.mtls(t, "127.0.0.1", caA)
		return &Agent{Config: config.Defaults(), logger: log, internalSecret: secret, peerTLS: cfg, peerClient: newPeerHTTPClient(cfg)}
	}

	t.Run("same CA peer accepts the signed join", func(t *testing.T) {
		var hits atomic.Int32
		srv := startJoinStub(t, caA.mtls(t, "127.0.0.1", caA), secret, &hits)
		a := newAgent(t, testLogger())
		status, err := a.sendJoin(cluster.MemberInfo{Name: "peer-1", PeerAddr: srv.Listener.Addr().String()}, "me", "10.0.0.1:8300")
		if err != nil || status != http.StatusOK {
			t.Fatalf("sendJoin = %d, %v; want 200", status, err)
		}
		if hits.Load() != 1 {
			t.Errorf("handler ran %d times, want 1", hits.Load())
		}
	})

	t.Run("peer with a certificate from another CA fails and is logged by name", func(t *testing.T) {
		var hits atomic.Int32
		// Otherwise valid peer: right IP SAN, trusts our client certificate.
		srv := startJoinStub(t, caB.mtls(t, "127.0.0.1", caA), secret, &hits)
		log, rec := newCaptureLogger()
		a := newAgent(t, log)
		status, err := a.sendJoin(cluster.MemberInfo{Name: "peer-evil", PeerAddr: srv.Listener.Addr().String()}, "me", "10.0.0.1:8300")
		if err == nil {
			t.Fatalf("join to a foreign-CA peer succeeded with status %d", status)
		}
		if hits.Load() != 0 {
			t.Errorf("handler ran %d times for an untrusted peer", hits.Load())
		}
		if !rec.warnedAbout("peer-evil") {
			t.Errorf("no warn record names the peer; log:\n%s", rec.text())
		}
		if strings.Contains(rec.text(), string(secret)) {
			t.Errorf("log leaks the secret:\n%s", rec.text())
		}
	})
}

func clusterMember(name, httpAddr, peerAddr string) cluster.MemberInfo {
	return cluster.MemberInfo{Name: name, HTTPAddr: httpAddr, PeerAddr: peerAddr}
}
