package handlers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/store"
)

func setupBlobRouter(blobs BlobGetter) *chi.Mux {
	r := chi.NewRouter()
	h := NewBlobHandler(blobs)
	r.Get("/internal/blob/{hash}", h.Get)
	r.Head("/internal/blob/{hash}", h.Head)
	return r
}

func TestBlobGetAndHead(t *testing.T) {
	dir := t.TempDir()
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	// Store a blob.
	data := []byte("hello blob content")
	hash, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	router := setupBlobRouter(bs)

	// GET should return the blob content.
	req := httptest.NewRequest(http.MethodGet, "/internal/blob/"+hash, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", w.Code, http.StatusOK)
	}
	body, _ := io.ReadAll(w.Body)
	if string(body) != string(data) {
		t.Errorf("GET body = %q, want %q", body, data)
	}

	// HEAD should return 200.
	req = httptest.NewRequest(http.MethodHead, "/internal/blob/"+hash, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestBlobGetNotFound(t *testing.T) {
	dir := t.TempDir()
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	router := setupBlobRouter(bs)

	req := httptest.NewRequest(http.MethodGet, "/internal/blob/deadbeef0000000000000000000000000000000000000000000000000000dead", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestBlobHeadNotFound(t *testing.T) {
	dir := t.TempDir()
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	router := setupBlobRouter(bs)

	req := httptest.NewRequest(http.MethodHead, "/internal/blob/deadbeef0000000000000000000000000000000000000000000000000000dead", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("HEAD status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
