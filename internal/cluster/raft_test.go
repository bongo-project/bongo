package cluster

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
)

func TestNewRaftNodeDev(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}

	bs, err := store.NewBlobStore(dir)
	if err != nil {
		ps.Close()
		t.Fatalf("NewBlobStore: %v", err)
	}

	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		ps.Close()
		t.Fatalf("NewMetadataManager: %v", err)
	}

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.Name = "test-node"

	node, err := NewRaftNode(cfg, ps, bs, mm, logger)
	if err != nil {
		mm.Close()
		ps.Close()
		t.Fatalf("NewRaftNode: %v", err)
	}

	// Wait for leader election.
	leaderAddr, err := node.WaitForLeader(5 * time.Second)
	if err != nil {
		t.Fatalf("WaitForLeader: %v", err)
	}
	if leaderAddr == "" {
		t.Error("expected non-empty leader address")
	}

	t.Logf("leader elected: %s", leaderAddr)

	// Verify this node is the leader.
	if node.Raft.State().String() != "Leader" {
		t.Errorf("node state = %s, want Leader", node.Raft.State())
	}

	// Shutdown.
	if err := node.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	mm.Close()
	ps.Close()
}

func TestNewRaftNodeEdgeReturnsError(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer ps.Close()

	cfg := config.Defaults()
	cfg.Server.Role = "edge"

	_, err = NewRaftNode(cfg, ps, nil, nil, logger)
	if err == nil {
		t.Error("expected error creating raft node for edge role")
	}
}
