package cluster

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bongo-go/bongo-go/internal/config"
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
	raftCfg.LogOutput = io.Discard // We use slog, not hclog.

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
		bindAddr := net.JoinHostPort(cfg.Server.BindAddr, "8300")
		advertise := bindAddr
		if cfg.Server.AdvertiseAddr != "" {
			advertise = net.JoinHostPort(cfg.Server.AdvertiseAddr, "8300")
		}

		addr, err := net.ResolveTCPAddr("tcp", advertise)
		if err != nil {
			return nil, fmt.Errorf("resolving raft advertise address: %w", err)
		}

		tcpTransport, err := raft.NewTCPTransport(bindAddr, addr, 3, 10*time.Second, io.Discard)
		if err != nil {
			return nil, fmt.Errorf("creating tcp transport: %w", err)
		}
		transport = tcpTransport
		raftAddr = raft.ServerAddress(advertise)

		// File-based snapshot store for server mode.
		snapDir := filepath.Join(cfg.Storage.DataDir, "raft-snapshots")
		if err := os.MkdirAll(snapDir, 0o750); err != nil {
			tcpTransport.Close()
			return nil, fmt.Errorf("creating snapshot dir: %w", err)
		}

		fileSnaps, err := raft.NewFileSnapshotStore(snapDir, 2, io.Discard)
		if err != nil {
			tcpTransport.Close()
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

	// Bootstrap for single-node dev mode.
	if cfg.Server.Role == "dev" {
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

// Shutdown shuts down the Raft node.
func (n *RaftNode) Shutdown() error {
	future := n.Raft.Shutdown()
	return future.Error()
}
