package cluster

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/raft"

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

// TestApplyRaftTuningAppliesAllValues is the regression test for the original
// bug: cluster.raft.snapshot_interval was parsed from config, documented, and
// set in the shipped production config, but NewRaftNode never assigned it to
// raft.Config. The value was read and dropped, which no behavioural test could
// see -- so this asserts the destination struct field by field. Deleting any
// one of the three assignments in applyRaftTuning must fail this test.
func TestApplyRaftTuningAppliesAllValues(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	rc := &config.RaftConfig{
		SnapshotThreshold: 256,
		TrailingLogs:      128,
		SnapshotInterval:  "45s",
	}

	raftCfg := raft.DefaultConfig()
	// Prove each field is written rather than merely happening to match: seed
	// every one with a value the config does not ask for.
	raftCfg.SnapshotThreshold = 9999
	raftCfg.TrailingLogs = 9999
	raftCfg.SnapshotInterval = 999 * time.Second

	if err := applyRaftTuning(raftCfg, rc, logger); err != nil {
		t.Fatalf("applyRaftTuning: %v", err)
	}

	if raftCfg.SnapshotThreshold != 256 {
		t.Errorf("SnapshotThreshold = %d, want 256", raftCfg.SnapshotThreshold)
	}
	if raftCfg.TrailingLogs != 128 {
		t.Errorf("TrailingLogs = %d, want 128", raftCfg.TrailingLogs)
	}
	if raftCfg.SnapshotInterval != 45*time.Second {
		t.Errorf("SnapshotInterval = %v, want 45s", raftCfg.SnapshotInterval)
	}
}

func TestApplyRaftTuningDefaults(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name          string
		rc            *config.RaftConfig
		wantThreshold uint64
		wantTrailing  uint64
		wantInterval  time.Duration
	}{
		{
			// An absent snapshot_interval must still be applied as the
			// documented 120s default, not left at raft's own default.
			name:          "unset falls back to the config defaults",
			rc:            &config.RaftConfig{},
			wantThreshold: raft.DefaultConfig().SnapshotThreshold,
			wantTrailing:  raft.DefaultConfig().TrailingLogs,
			wantInterval:  120 * time.Second,
		},
		{
			name:          "nil block is treated as unset",
			rc:            nil,
			wantThreshold: raft.DefaultConfig().SnapshotThreshold,
			wantTrailing:  raft.DefaultConfig().TrailingLogs,
			wantInterval:  120 * time.Second,
		},
		{
			name:          "threshold only leaves trailing at raft's default",
			rc:            &config.RaftConfig{SnapshotThreshold: 512},
			wantThreshold: 512,
			wantTrailing:  raft.DefaultConfig().TrailingLogs,
			wantInterval:  120 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raftCfg := raft.DefaultConfig()
			if err := applyRaftTuning(raftCfg, tt.rc, logger); err != nil {
				t.Fatalf("applyRaftTuning: %v", err)
			}
			if raftCfg.SnapshotThreshold != tt.wantThreshold {
				t.Errorf("SnapshotThreshold = %d, want %d", raftCfg.SnapshotThreshold, tt.wantThreshold)
			}
			if raftCfg.TrailingLogs != tt.wantTrailing {
				t.Errorf("TrailingLogs = %d, want %d", raftCfg.TrailingLogs, tt.wantTrailing)
			}
			if raftCfg.SnapshotInterval != tt.wantInterval {
				t.Errorf("SnapshotInterval = %v, want %v", raftCfg.SnapshotInterval, tt.wantInterval)
			}
		})
	}
}

// TestApplyRaftTuningRejectsInvalidPairing pins the enforcement point. The
// check also lives in config.Validate(), which is not wired into the startup
// path (task B8), so this is the copy that actually runs in production.
func TestApplyRaftTuningRejectsInvalidPairing(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name    string
		rc      *config.RaftConfig
		wantErr bool
	}{
		{"trailing below threshold", &config.RaftConfig{SnapshotThreshold: 256, TrailingLogs: 128}, false},
		{"trailing equals threshold", &config.RaftConfig{SnapshotThreshold: 256, TrailingLogs: 256}, true},
		{"trailing above threshold", &config.RaftConfig{SnapshotThreshold: 256, TrailingLogs: 512}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raftCfg := raft.DefaultConfig()
			err := applyRaftTuning(raftCfg, tt.rc, logger)
			if (err != nil) != tt.wantErr {
				t.Fatalf("applyRaftTuning() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "trailing_logs") {
				t.Errorf("error = %q, want it to name trailing_logs", err.Error())
			}
		})
	}
}

// TestNewRaftNodeRejectsInvalidTuning proves the invalid pairing fails startup
// rather than being caught only by a helper nothing calls.
func TestNewRaftNodeRejectsInvalidTuning(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer ps.Close()

	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	mm, err := store.NewMetadataManager(dir)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	defer mm.Close()

	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Server.Name = "test-node"
	cfg.Cluster.Raft.SnapshotThreshold = 256
	cfg.Cluster.Raft.TrailingLogs = 256

	node, err := NewRaftNode(cfg, ps, bs, mm, logger)
	if err == nil {
		if shutdownErr := node.Shutdown(); shutdownErr != nil {
			t.Logf("Shutdown: %v", shutdownErr)
		}
		t.Fatal("NewRaftNode() = nil error, want a rejection of trailing_logs >= snapshot_threshold")
	}
	if !strings.Contains(err.Error(), "trailing_logs") {
		t.Errorf("NewRaftNode() error = %q, want it to name trailing_logs", err.Error())
	}
}
