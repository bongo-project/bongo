package cluster

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
)

// recordingHandler captures slog records so a test can assert what was logged.
type recordingHandler struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs = append(h.recs, r.Clone())
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// flatten renders every record as "level msg k=v ..." lines.
func (h *recordingHandler) flatten() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var b strings.Builder
	for _, r := range h.recs {
		b.WriteString(r.Level.String() + " " + r.Message)
		r.Attrs(func(a slog.Attr) bool {
			b.WriteString(" " + a.Key + "=" + a.Value.String())
			return true
		})
		b.WriteString("\n")
	}
	return b.String()
}

// hasPeerAttr reports whether some record at or above Warn carries peer=name.
func (h *recordingHandler) hasPeerAttr(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.recs {
		if r.Level < slog.LevelWarn {
			continue
		}
		found := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "peer" && a.Value.String() == name {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// peerBlobHandler serves blobs the way the peer listener does, counting hits.
func peerBlobHandler(blobs *store.BlobStore, hits *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		hash := strings.TrimPrefix(r.URL.Path, "/internal/blob/")
		rc, err := blobs.Get(hash)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer rc.Close()
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, rc)
		}
	})
}

// startPeerTLSServer starts an mTLS server (client cert required, verified
// against serverCfg.ClientCAs) on 127.0.0.1.
func startPeerTLSServer(t *testing.T, serverCfg *tls.Config, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = serverCfg.Clone()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func newRemoteBlob(t *testing.T, data []byte) (*store.BlobStore, string) {
	t.Helper()
	remote, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := remote.Put(data)
	if err != nil {
		t.Fatal(err)
	}
	return remote, hash
}

func newLocalBlobs(t *testing.T) *store.BlobStore {
	t.Helper()
	b, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// clientTLS is the config a node hands to SetPeerTLS: own certificate, CA as
// the only trust root.
func clientTLS(t *testing.T, own *testCA, trust *testCA) *tls.Config {
	t.Helper()
	return own.mtlsConfig(t, "127.0.0.1", trust)
}

func TestBlobSyncPeerTLS(t *testing.T) {
	caA := newTestCA(t, "CA-A")
	caB := newTestCA(t, "CA-B")
	data := []byte("blob over the peer listener")
	const secret = "peer-tls-test-shared-secret-value"

	t.Run("same CA peer succeeds over https", func(t *testing.T) {
		remote, hash := newRemoteBlob(t, data)
		var hits atomic.Int32
		srv := startPeerTLSServer(t, caA.mtlsConfig(t, "127.0.0.1", caA), peerBlobHandler(remote, &hits))
		local := newLocalBlobs(t)
		members := &mockMemberLister{members: []MemberInfo{
			{Name: "peer-1", PeerAddr: srv.Listener.Addr().String()},
		}}
		bs := NewBlobSyncer(local, members, testLogger())
		bs.SetSharedSecret([]byte(secret))
		bs.SetPeerTLS(clientTLS(t, caA, caA))
		if err := bs.EnsureBlob(hash); err != nil {
			t.Fatalf("EnsureBlob: %v", err)
		}
		if !local.Exists(hash) {
			t.Error("blob not stored locally")
		}
		if hits.Load() == 0 {
			t.Error("the TLS peer was never contacted")
		}
	})

	t.Run("peer certificate from another CA fails and is logged by name", func(t *testing.T) {
		remote, hash := newRemoteBlob(t, data)
		var hits atomic.Int32
		// The peer is otherwise valid: well-formed certificate with the right
		// IP SAN, trusting our client certificate. Only its issuer differs.
		serverCfg := caB.mtlsConfig(t, "127.0.0.1", caA)
		srv := startPeerTLSServer(t, serverCfg, peerBlobHandler(remote, &hits))
		local := newLocalBlobs(t)
		rec := &recordingHandler{}
		members := &mockMemberLister{members: []MemberInfo{
			{Name: "peer-evil", PeerAddr: srv.Listener.Addr().String()},
		}}
		bs := NewBlobSyncer(local, members, slog.New(rec))
		bs.SetSharedSecret([]byte(secret))
		bs.SetPeerTLS(clientTLS(t, caA, caA))
		if err := bs.EnsureBlob(hash); err == nil {
			t.Fatal("pull from a peer with a foreign-CA certificate succeeded")
		}
		if local.Exists(hash) {
			t.Error("blob stored from an untrusted peer")
		}
		if hits.Load() != 0 {
			t.Errorf("handler ran %d times for an untrusted peer", hits.Load())
		}
		if !rec.hasPeerAttr("peer-evil") {
			t.Errorf("no warn record names the peer; log:\n%s", rec.flatten())
		}
		out := rec.flatten()
		if strings.Contains(out, secret) || strings.Contains(out, string(data)) {
			t.Errorf("log leaks the secret or blob content:\n%s", out)
		}
	})

	t.Run("client without our CA certificate is refused by the peer", func(t *testing.T) {
		remote, hash := newRemoteBlob(t, data)
		var hits atomic.Int32
		srv := startPeerTLSServer(t, caA.mtlsConfig(t, "127.0.0.1", caA), peerBlobHandler(remote, &hits))
		local := newLocalBlobs(t)
		members := &mockMemberLister{members: []MemberInfo{
			{Name: "peer-1", PeerAddr: srv.Listener.Addr().String()},
		}}
		bs := NewBlobSyncer(local, members, testLogger())
		// Trusts the server, but its own certificate is from CA-B.
		bs.SetPeerTLS(caB.mtlsConfig(t, "127.0.0.1", caA))
		if err := bs.EnsureBlob(hash); err == nil {
			t.Fatal("pull succeeded with a client certificate from another CA")
		}
		if hits.Load() != 0 {
			t.Errorf("handler ran %d times for a foreign client", hits.Load())
		}
	})

	t.Run("member without PeerAddr is never contacted over http", func(t *testing.T) {
		remote, hash := newRemoteBlob(t, data)
		var hits atomic.Int32
		plain := httptest.NewServer(peerBlobHandler(remote, &hits))
		t.Cleanup(plain.Close)
		local := newLocalBlobs(t)
		rec := &recordingHandler{}
		members := &mockMemberLister{members: []MemberInfo{
			{Name: "old-node", HTTPAddr: plain.Listener.Addr().String()},
		}}
		bs := NewBlobSyncer(local, members, slog.New(rec))
		bs.SetPeerTLS(clientTLS(t, caA, caA))
		if err := bs.EnsureBlob(hash); err == nil {
			t.Fatal("pull succeeded from a member that has no peer address")
		}
		if hits.Load() != 0 {
			t.Errorf("plaintext peer contacted %d times while TLS is required", hits.Load())
		}
		if local.Exists(hash) {
			t.Error("blob stored over plaintext")
		}
	})

	t.Run("a GET that fails after a good HEAD is logged by name", func(t *testing.T) {
		srv := startPeerTLSServer(t, caA.mtlsConfig(t, "127.0.0.1", caA), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}))
		local := newLocalBlobs(t)
		rec := &recordingHandler{}
		members := &mockMemberLister{members: []MemberInfo{
			{Name: "peer-flaky", PeerAddr: srv.Listener.Addr().String()},
		}}
		bs := NewBlobSyncer(local, members, slog.New(rec))
		bs.SetPeerTLS(clientTLS(t, caA, caA))
		if err := bs.EnsureBlob(sha256hex([]byte("anything"))); err == nil {
			t.Fatal("pull succeeded although the GET was dropped")
		}
		if !rec.hasPeerAttr("peer-flaky") {
			t.Errorf("GET failure not logged with the peer name; log:\n%s", rec.flatten())
		}
	})

	t.Run("hash mismatch over TLS is rejected and removed", func(t *testing.T) {
		srv := startPeerTLSServer(t, caA.mtlsConfig(t, "127.0.0.1", caA), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte("not the content you asked for"))
			}
		}))
		local := newLocalBlobs(t)
		members := &mockMemberLister{members: []MemberInfo{
			{Name: "peer-1", PeerAddr: srv.Listener.Addr().String()},
		}}
		bs := NewBlobSyncer(local, members, testLogger())
		bs.SetPeerTLS(clientTLS(t, caA, caA))
		want := sha256hex([]byte("expected content"))
		if err := bs.EnsureBlob(want); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
			t.Fatalf("want hash mismatch, got %v", err)
		}
		if local.Exists(want) || local.Exists(sha256hex([]byte("not the content you asked for"))) {
			t.Error("mismatched content left in the blob store")
		}
	})
}

// Without SetPeerTLS the syncer keeps http:// and HTTPAddr exactly as before.
func TestBlobSyncWithoutPeerTLSStaysHTTP(t *testing.T) {
	remote, hash := newRemoteBlob(t, []byte("plain path content"))
	var hits atomic.Int32
	var scheme atomic.Value
	h := peerBlobHandler(remote, &hits)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme.Store(map[bool]string{true: "https", false: "http"}[r.TLS != nil])
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	local := newLocalBlobs(t)
	members := &mockMemberLister{members: []MemberInfo{
		{Name: "peer-1", HTTPAddr: srv.Listener.Addr().String(), PeerAddr: "192.0.2.1:1"},
	}}
	bs := NewBlobSyncer(local, members, testLogger())
	if err := bs.EnsureBlob(hash); err != nil {
		t.Fatalf("EnsureBlob: %v", err)
	}
	if got, _ := scheme.Load().(string); got != "http" {
		t.Errorf("scheme = %q, want http", got)
	}
}
