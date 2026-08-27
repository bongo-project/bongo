package handlers

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// RaftApplier is the interface needed by KVHandler to apply commands and read state.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
	GetKey(key []byte) ([]byte, error)
}

// KVHandler provides internal key-value endpoints for testing Raft consensus.
type KVHandler struct {
	applier RaftApplier
}

// NewKVHandler creates a new KVHandler.
func NewKVHandler(applier RaftApplier) *KVHandler {
	return &KVHandler{applier: applier}
}

// Put handles PUT /internal/kv/{key} — applies a put command through Raft.
func (h *KVHandler) Put(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "*")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
		return
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/" + key),
		Value: body,
	}

	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		var nle *cluster.NotLeaderError
		if errors.As(err, &nle) {
			w.Header().Set("X-Raft-Leader", string(nle.LeaderAddr))
			http.Error(w, err.Error(), http.StatusTemporaryRedirect)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Get handles GET /internal/kv/{key} — reads directly from local Pebble.
func (h *KVHandler) Get(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "*")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	val, err := h.applier.GetKey([]byte("/" + key))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := w.Write(val); err != nil { // #nosec G705 -- internal KV endpoint returns binary values by design
		return
	}
}

// Delete handles DELETE /internal/kv/{key} — applies a delete command through Raft.
func (h *KVHandler) Delete(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "*")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}

	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: []byte("/" + key),
	}

	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		var nle *cluster.NotLeaderError
		if errors.As(err, &nle) {
			w.Header().Set("X-Raft-Leader", string(nle.LeaderAddr))
			http.Error(w, err.Error(), http.StatusTemporaryRedirect)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
