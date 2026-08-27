package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/queue"
)

// QueueHandler provides HTTP endpoints for queue management.
type QueueHandler struct {
	queueStore *queue.QueueStore
}

// NewQueueHandler creates a new QueueHandler.
func NewQueueHandler(qs *queue.QueueStore) *QueueHandler {
	return &QueueHandler{queueStore: qs}
}

// Stats returns queue statistics as JSON.
func (h *QueueHandler) Stats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.queueStore.Stats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		return
	}
}

// Retry resets a message for immediate retry.
func (h *QueueHandler) Retry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing message id", http.StatusBadRequest)
		return
	}

	if err := h.queueStore.RetryNow(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": id}); err != nil {
		return
	}
}

// Drop removes a message from the queue.
func (h *QueueHandler) Drop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "missing message id", http.StatusBadRequest)
		return
	}

	if err := h.queueStore.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": id}); err != nil {
		return
	}
}
