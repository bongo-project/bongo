package cluster

import (
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
)

// nonLoopbackIPv4 returns a local non-loopback IPv4 address, or "" if none.
func nonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() {
			return ipn.IP.String()
		}
	}
	return ""
}

func dialable(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func startServerRaft(t *testing.T, advertise string, port int) {
	t.Helper()
	cfg := peeredConfig(t, advertise, port)
	cfg.Server.Name = "bind-test"
	if advertise != "127.0.0.1" {
		ca := newTestCA(t, "CA")
		cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, cfg.Cluster.TLS.CAFile = ca.writeNodeFiles(t, advertise)
	}
	node, err := startRaftWith(t, cfg)
	if err != nil {
		t.Fatalf("NewRaftNode: %v", err)
	}
	t.Cleanup(func() { node.Shutdown() })
}

// startRaftWith builds the stores for cfg and calls NewRaftNode.
func startRaftWith(t *testing.T, cfg *config.Config) (*RaftNode, error) {
	t.Helper()
	dir := cfg.Storage.DataDir
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { mm.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRaftNode(cfg, ps, bs, mm, logger)
}

func TestNewRaftNodeServerLoopbackAdvertiseBindsLoopback(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	port := freePort(t)
	startServerRaft(t, "127.0.0.1", port)

	if !dialable(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
		t.Fatal("raft not reachable on loopback")
	}
	if dialable(net.JoinHostPort(ext, strconv.Itoa(port))) {
		t.Errorf("raft transport reachable on %s although advertise address is loopback", ext)
	}
}

// server.bind_addr is 0.0.0.0 here; raft must still listen on the advertise
// address only, so loopback must not answer.
func TestNewRaftNodeServerRoutableAdvertiseIgnoresServerBind(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	port := freePort(t)
	startServerRaft(t, ext, port)

	if !dialable(net.JoinHostPort(ext, strconv.Itoa(port))) {
		t.Errorf("raft not reachable on %s with routable advertise address", ext)
	}
	if dialable(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
		t.Errorf("raft reachable on loopback: bound server.bind_addr instead of advertise %s", ext)
	}
}

func TestNewRaftNodeClusterBindAddrOverridesAdvertise(t *testing.T) {
	ext := nonLoopbackIPv4()
	if ext == "" {
		t.Skip("no non-loopback IPv4 interface")
	}
	ca := newTestCA(t, "CA")
	port := freePort(t)
	cfg := peeredConfig(t, ext, port)
	cfg.Cluster.BindAddr = "127.0.0.1"
	cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, cfg.Cluster.TLS.CAFile = ca.writeNodeFiles(t, ext)
	node, err := startRaftWith(t, cfg)
	if err != nil {
		t.Fatalf("NewRaftNode: %v", err)
	}
	defer node.Shutdown()

	if !dialable(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
		t.Error("raft not reachable on cluster.bind_addr")
	}
	if dialable(net.JoinHostPort(ext, strconv.Itoa(port))) {
		t.Errorf("raft reachable on advertise address %s despite cluster.bind_addr", ext)
	}
}

// Production shape: one node, loopback advertise, a mail certificate in
// cluster.tls and no CA file. It must keep starting, over TLS.
func TestNewRaftNodeSingleNodeCertWithoutCAStarts(t *testing.T) {
	ca := newTestCA(t, "Mail CA")
	port := freePort(t)
	cfg := peeredConfig(t, "127.0.0.1", port)
	cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile, _ = ca.writeNodeFiles(t, "127.0.0.1")
	node, err := startRaftWith(t, cfg)
	if err != nil {
		t.Fatalf("single node with cert and no ca_file must start: %v", err)
	}
	defer node.Shutdown()
	if !dialable(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
		t.Fatal("raft not reachable on loopback")
	}
}

// A single node with a not-yet-provisioned cert keeps today's loopback-only
// plaintext fallback.
func TestNewRaftNodeSingleNodeMissingCertFallsBack(t *testing.T) {
	port := freePort(t)
	cfg := peeredConfig(t, "127.0.0.1", port)
	cfg.Cluster.TLS.CertFile = filepath.Join(t.TempDir(), "absent.pem")
	cfg.Cluster.TLS.KeyFile = filepath.Join(t.TempDir(), "absent.key")
	node, err := startRaftWith(t, cfg)
	if err != nil {
		t.Fatalf("single node with missing cert must still start: %v", err)
	}
	defer node.Shutdown()
}
