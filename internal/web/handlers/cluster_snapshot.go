package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hashicorp/raft"
)

// RaftSnapshotter is the subset of *raft.Raft the snapshot endpoint needs,
// defined here at the consumer so the handler can be tested without a live
// consensus group.
type RaftSnapshotter interface {
	State() raft.RaftState
	Snapshot() raft.SnapshotFuture
}

// ClusterSnapshotHandler forces a Raft snapshot on demand. Mounted on
// /internal/ behind RequireInternalAuth like the other control-plane
// handlers: snapshotting is cheap but it is a state-machine operation on the
// leader and must not be reachable publicly.
//
// Without this there is no way to cut a snapshot short of reaching
// snapshot_threshold, so a node whose threshold is set too high for its write
// volume keeps restoring from a snapshot that is weeks out of date.
type ClusterSnapshotHandler struct {
	raft   RaftSnapshotter
	logger *slog.Logger
}

// NewClusterSnapshotHandler creates a new ClusterSnapshotHandler.
func NewClusterSnapshotHandler(r RaftSnapshotter, logger *slog.Logger) *ClusterSnapshotHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClusterSnapshotHandler{raft: r, logger: logger}
}

// Snapshot handles POST /internal/cluster/snapshot.
//
// Leader-only: raft.Snapshot() on a follower fails inside the future with an
// opaque error, so the state is checked up front to give the operator an
// answer they can act on.
func (h *ClusterSnapshotHandler) Snapshot(w http.ResponseWriter, r *http.Request) {
	if h.raft == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "raft not available on this node",
		})
		return
	}

	if state := h.raft.State(); state != raft.Leader {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("node is %s, not the leader; run this against the leader node", state),
		})
		return
	}

	start := time.Now()
	err := h.raft.Snapshot().Error()
	elapsed := time.Since(start)

	// Nothing new since the last snapshot is a successful no-op, not a
	// failure: the operator asked for a current snapshot and there is one.
	if errors.Is(err, raft.ErrNothingNewToSnapshot) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "nothing new to snapshot",
			"duration_ms": elapsed.Milliseconds(),
		})
		return
	}

	if err != nil {
		h.logger.Error("cluster: forced raft snapshot failed", "error", err, "duration", elapsed)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("snapshot failed: %v", err),
		})
		return
	}

	h.logger.Info("cluster: forced raft snapshot completed", "duration", elapsed)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"duration_ms": elapsed.Milliseconds(),
	})
}
