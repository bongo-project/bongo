package handlers

import (
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/store"
)

// BlobGetter provides read access to the blob store.
type BlobGetter interface {
	Exists(hash string) bool
	Get(hash string) (io.ReadCloser, error)
}

// BlobHandler provides internal blob sync endpoints.
type BlobHandler struct {
	blobs BlobGetter
}

// NewBlobHandler creates a new BlobHandler.
func NewBlobHandler(blobs BlobGetter) *BlobHandler {
	return &BlobHandler{blobs: blobs}
}

// Get handles GET /internal/blob/{hash} — serves blob content.
func (h *BlobHandler) Get(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !store.IsValidHash(hash) {
		http.Error(w, "invalid blob hash", http.StatusBadRequest)
		return
	}

	rc, err := h.blobs.Get(hash)
	if err != nil {
		if isNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	io.Copy(w, rc)
}

// Head handles HEAD /internal/blob/{hash} — checks blob existence.
func (h *BlobHandler) Head(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !store.IsValidHash(hash) {
		http.Error(w, "invalid blob hash", http.StatusBadRequest)
		return
	}

	if !h.blobs.Exists(hash) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func isNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound)
}
