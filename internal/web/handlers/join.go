package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/hashicorp/raft"
)

// RaftJoiner is the interface needed to add voters to the Raft cluster.
type RaftJoiner interface {
	State() raft.RaftState
	AddVoter(id raft.ServerID, address raft.ServerAddress, prevIndex uint64, timeout time.Duration) raft.IndexFuture
	LeaderWithID() (raft.ServerAddress, raft.ServerID)
}

// JoinHandler provides the cluster join endpoint.
type JoinHandler struct {
	raft RaftJoiner
}

// NewJoinHandler creates a new JoinHandler.
func NewJoinHandler(r RaftJoiner) *JoinHandler {
	return &JoinHandler{raft: r}
}

type joinRequest struct {
	ID   string `json:"id"`
	Addr string `json:"addr"`
}

// Join handles POST /internal/cluster/join.
// Only callable on the Raft leader. Returns 503 with leader hint otherwise.
func (h *JoinHandler) Join(w http.ResponseWriter, r *http.Request) {
	if h.raft.State() != raft.Leader {
		leaderAddr, leaderID := h.raft.LeaderWithID()
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":     "not the leader",
			"leader_id": string(leaderID),
			"leader":    string(leaderAddr),
		})
		return
	}

	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.ID == "" || req.Addr == "" {
		http.Error(w, "id and addr are required", http.StatusBadRequest)
		return
	}

	future := h.raft.AddVoter(raft.ServerID(req.ID), raft.ServerAddress(req.Addr), 0, 30*time.Second)
	if err := future.Error(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "joined",
		"id":     req.ID,
		"addr":   req.Addr,
	})
}
