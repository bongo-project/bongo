package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bongo-go/bongo-go/internal/config"
	"github.com/bongo-go/bongo-go/internal/security"
	"github.com/bongo-go/bongo-go/internal/store"
)

// RaftNode wraps the hashicorp/raft node and its resources.
type RaftNode struct {
	Raft      *raft.Raft
	Transport raft.Transport
	Store     *RaftStore
	FSM       *FSM
	logger    *slog.Logger
}

// NewRaftNode creates and configures a Raft node based on the server role.
// For dev mode, it uses in-memory transport and auto-bootstraps as a single node.
// For server mode, it uses TCP transport and bootstraps when bootstrap-expect is met.
func NewRaftNode(
	cfg *config.Config,
	pebbleStore *store.PebbleStore,
	blobs *store.BlobStore,
	metadata *store.MetadataManager,
	logger *slog.Logger,
) (*RaftNode, error) {
	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(cfg.Server.Name)

	// Forward raft's own log lines into slog rather than discarding them.
	//
	// These were previously sent to io.Discard, which made the consensus layer
	// completely silent: snapshotting, log truncation, election churn and slow
	// commits all happened with no trace. That cost real diagnostic time — an
	// 80s stall inside raft.Apply could be measured from the outside but not
	// explained, because the only component that knew what it was doing was
	// not allowed to say. Consensus is the one subsystem that should never be
	// mute.
	raftCfg.LogOutput = &slogWriter{logger: logger}
	raftCfg.LogLevel = "INFO"

	// Tune timeouts for dev mode (faster election).
	if cfg.Server.Role == "dev" {
		raftCfg.HeartbeatTimeout = 500 * time.Millisecond
		raftCfg.ElectionTimeout = 500 * time.Millisecond
		raftCfg.LeaderLeaseTimeout = 250 * time.Millisecond
		raftCfg.CommitTimeout = 200 * time.Millisecond
	}

	// Set snapshot threshold from config.
	if cfg.Cluster.Raft.SnapshotThreshold > 0 {
		raftCfg.SnapshotThreshold = uint64(cfg.Cluster.Raft.SnapshotThreshold)
	}

	fsm := NewFSM(pebbleStore, blobs, metadata, logger)
	raftStore := NewRaftStore(pebbleStore)

	var transport raft.Transport
	var snaps raft.SnapshotStore
	var raftAddr raft.ServerAddress

	switch cfg.Server.Role {
	case "dev":
		// In-memory transport for dev mode — fast, no network.
		_, inmem := raft.NewInmemTransport(raft.ServerAddress(""))
		transport = inmem
		raftAddr = inmem.LocalAddr()
		snaps = raft.NewInmemSnapshotStore()

	case "server":
		// TCP transport for production clusters.
		raftPort := strconv.Itoa(cfg.Cluster.Raft.Port)
		bindAddr := net.JoinHostPort(cfg.Server.BindAddr, raftPort)
		advertise := bindAddr
		if cfg.Server.AdvertiseAddr != "" {
			advertise = net.JoinHostPort(cfg.Server.AdvertiseAddr, raftPort)
		}

		addr, err := net.ResolveTCPAddr("tcp", advertise)
		if err != nil {
			return nil, fmt.Errorf("resolving raft advertise address: %w", err)
		}

		var netTransport *raft.NetworkTransport
		useTLS := cfg.Cluster.TLS.CertFile != "" && cfg.Cluster.TLS.KeyFile != ""
		// Tolerate a not-yet-provisioned cert on first boot (mirrors the mail
		// listeners): warn loudly and fall back to a plaintext transport rather
		// than crashing. A cert that exists but fails to load is a real
		// misconfiguration and still fails hard below.
		if useTLS {
			if _, statErr := os.Stat(cfg.Cluster.TLS.CertFile); os.IsNotExist(statErr) {
				logger.Warn("raft: TLS certificate not found, using an unencrypted transport; provide the cert and restart", "cert_file", cfg.Cluster.TLS.CertFile)
				useTLS = false
			}
		}
		if useTLS {
			tlsCfg, tlsErr := loadRaftTLS(cfg)
			if tlsErr != nil {
				return nil, fmt.Errorf("loading raft TLS: %w", tlsErr)
			}
			stream, streamErr := NewTLSStreamLayer(bindAddr, addr, tlsCfg)
			if streamErr != nil {
				return nil, fmt.Errorf("creating TLS stream layer: %w", streamErr)
			}
			netTransport = raft.NewNetworkTransportWithConfig(&raft.NetworkTransportConfig{
				Stream:  stream,
				MaxPool: 3,
				Timeout: 10 * time.Second,
			})
			logger.Info("raft: TLS transport enabled")
		} else {
			netTransport, err = raft.NewTCPTransport(bindAddr, addr, 3, 10*time.Second, io.Discard)
			if err != nil {
				return nil, fmt.Errorf("creating tcp transport: %w", err)
			}
		}
		transport = netTransport
		raftAddr = raft.ServerAddress(advertise)

		// File-based snapshot store for server mode.
		snapDir := filepath.Join(cfg.Storage.DataDir, "raft-snapshots")
		if err := os.MkdirAll(snapDir, 0o750); err != nil {
			if closeErr := netTransport.Close(); closeErr != nil {
				logger.Warn("raft: failed to close transport after snapshot dir failure", "error", closeErr)
			}
			return nil, fmt.Errorf("creating snapshot dir: %w", err)
		}

		fileSnaps, err := raft.NewFileSnapshotStore(snapDir, 2, io.Discard)
		if err != nil {
			if closeErr := netTransport.Close(); closeErr != nil {
				logger.Warn("raft: failed to close transport after snapshot store failure", "error", closeErr)
			}
			return nil, fmt.Errorf("creating file snapshot store: %w", err)
		}
		snaps = fileSnaps

	default:
		return nil, fmt.Errorf("raft not supported for role %q", cfg.Server.Role)
	}

	r, err := raft.NewRaft(raftCfg, fsm, raftStore, raftStore, snaps, transport)
	if err != nil {
		return nil, fmt.Errorf("creating raft node: %w", err)
	}

	node := &RaftNode{
		Raft:      r,
		Transport: transport,
		Store:     raftStore,
		FSM:       fsm,
		logger:    logger,
	}

	// Bootstrap a single-node cluster for dev, or for a server that has no peers
	// to join (the seed node). bootstrapSingle is a no-op when the node already
	// has persisted Raft state, so this is safe across restarts. Additional
	// nodes join later via gossip + the cluster join endpoint.
	if cfg.Server.Role == "dev" ||
		(cfg.Server.Role == "server" && len(cfg.Cluster.Join) == 0) {
		if err := node.bootstrapSingle(raftCfg.LocalID, raftAddr); err != nil {
			r.Shutdown()
			return nil, err
		}
	}

	logger.Info("raft node created", "role", cfg.Server.Role, "id", string(raftCfg.LocalID), "addr", string(raftAddr))

	return node, nil
}

// bootstrapSingle bootstraps this node as a single-node cluster.
func (n *RaftNode) bootstrapSingle(id raft.ServerID, addr raft.ServerAddress) error {
	cfg := raft.Configuration{
		Servers: []raft.Server{
			{
				ID:      id,
				Address: addr,
			},
		},
	}

	// If already bootstrapped, this is a no-op error we can ignore.
	future := n.Raft.BootstrapCluster(cfg)
	if err := future.Error(); err != nil {
		if err == raft.ErrCantBootstrap {
			n.logger.Debug("raft already bootstrapped")
			return nil
		}
		return fmt.Errorf("bootstrapping cluster: %w", err)
	}

	n.logger.Info("raft single-node cluster bootstrapped")
	return nil
}

// Bootstrap bootstraps a multi-node cluster with the given server configuration.
// This should only be called once when the expected number of servers have joined.
func (n *RaftNode) Bootstrap(servers []raft.Server) error {
	cfg := raft.Configuration{Servers: servers}
	future := n.Raft.BootstrapCluster(cfg)
	if err := future.Error(); err != nil {
		if err == raft.ErrCantBootstrap {
			return nil
		}
		return fmt.Errorf("bootstrapping cluster: %w", err)
	}
	return nil
}

// WaitForLeader blocks until a leader is elected or the timeout expires.
func (n *RaftNode) WaitForLeader(timeout time.Duration) (raft.ServerAddress, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return "", fmt.Errorf("timeout waiting for leader after %v", timeout)
		case <-ticker.C:
			addr, _ := n.Raft.LeaderWithID()
			if addr != "" {
				return addr, nil
			}
		}
	}
}

// Shutdown shuts down the Raft node, transferring leadership first if this node is leader.
func (n *RaftNode) Shutdown() error {
	if n.Raft.State() == raft.Leader {
		n.logger.Info("raft: transferring leadership before shutdown")
		transferFuture := n.Raft.LeadershipTransfer()
		errCh := make(chan error, 1)
		go func() {
			errCh <- transferFuture.Error()
		}()
		select {
		case err := <-errCh:
			if err != nil {
				n.logger.Warn("raft: leadership transfer failed", "error", err)
			} else {
				n.logger.Info("raft: leadership transferred")
			}
		case <-time.After(5 * time.Second):
			n.logger.Warn("raft: leadership transfer timed out")
		}
	}

	future := n.Raft.Shutdown()
	return future.Error()
}

// loadRaftTLS builds a tls.Config from the cluster TLS configuration.
func loadRaftTLS(cfg *config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.Cluster.TLS.CertFile, cfg.Cluster.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("loading TLS key pair: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if cfg.Cluster.TLS.CAFile != "" {
		caPool, err := security.LoadCAPool(cfg.Cluster.TLS.CAFile)
		if err != nil {
			return nil, fmt.Errorf("loading CA pool: %w", err)
		}
		tlsCfg.ClientCAs = caPool
		tlsCfg.RootCAs = caPool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsCfg, nil
}

// TLSStreamLayer implements raft.StreamLayer over TLS.
type TLSStreamLayer struct {
	listener  net.Listener
	advertise net.Addr
	tlsCfg    *tls.Config
}

// NewTLSStreamLayer creates a TLS-wrapped stream layer for Raft.
// If advertise is nil, the listener address is used.
func NewTLSStreamLayer(bindAddr string, advertise net.Addr, tlsCfg *tls.Config) (*TLSStreamLayer, error) {
	ln, err := tls.Listen("tcp", bindAddr, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("tls listen: %w", err)
	}
	if advertise == nil {
		advertise = ln.Addr()
	}
	return &TLSStreamLayer{
		listener:  ln,
		advertise: advertise,
		tlsCfg:    tlsCfg,
	}, nil
}

func (t *TLSStreamLayer) Accept() (net.Conn, error) { return t.listener.Accept() }
func (t *TLSStreamLayer) Close() error              { return t.listener.Close() }
func (t *TLSStreamLayer) Addr() net.Addr            { return t.advertise }

func (t *TLSStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", string(address), &tls.Config{
		Certificates:       t.tlsCfg.Certificates,
		RootCAs:            t.tlsCfg.RootCAs,
		InsecureSkipVerify: t.tlsCfg.RootCAs == nil, // #nosec G402 -- private raft peers may use pinned self-signed certs without a CA
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("tls dial %s: %w", address, err)
	}
	return conn, nil
}

// slogWriter adapts hashicorp/raft's io.Writer-based logging onto slog.
//
// raft formats its own lines (with a level prefix) and writes them here. The
// prefix is parsed back out so a raft WARN stays a WARN in our logs rather
// than being flattened, and the line is emitted under a "raft" source so it is
// filterable.
type slogWriter struct {
	logger *slog.Logger
}

func (w *slogWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line == "" {
		return len(p), nil
	}

	level := slog.LevelInfo
	switch {
	case strings.Contains(line, "[ERROR]"):
		level = slog.LevelError
	case strings.Contains(line, "[WARN]"):
		level = slog.LevelWarn
	case strings.Contains(line, "[DEBUG]"), strings.Contains(line, "[TRACE]"):
		level = slog.LevelDebug
	}

	w.logger.Log(context.Background(), level, "raft (internal)", "line", line)
	return len(p), nil
}
