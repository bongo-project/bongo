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

func TestBlobHandlerInvalidHash(t *testing.T) {
	dir := t.TempDir()
	bs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	router := setupBlobRouter(bs)

	// Note: empty hash and path traversal sequences are rejected by chi's router
	// before reaching the handler (chi normalises paths). The store-level
	// tests cover those inputs. Here we test hashes that do route to the
	// handler but fail IsValidHash.
	badHashes := []struct {
		name string
		hash string
	}{
		{"short", "abc"},
		{"63 chars", "deadbeef0000000000000000000000000000000000000000000000000000dea"},
		{"65 chars", "deadbeef0000000000000000000000000000000000000000000000000000deadf"},
		{"uppercase", "DEADBEEF0000000000000000000000000000000000000000000000000000DEAD"},
		{"percent encoded", "%2e%2e%2f%2e%2e%2fetc%2fpasswd000000000000000000000000000000000000"},
		{"non-hex chars", "zzzzzzzz0000000000000000000000000000000000000000000000000000zzzz"},
	}

	for _, tc := range badHashes {
		t.Run("GET/"+tc.name, func(t *testing.T) {
			path := "/internal/blob/" + tc.hash
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("GET %q: status = %d, want %d", tc.hash, w.Code, http.StatusBadRequest)
			}
		})

		t.Run("HEAD/"+tc.name, func(t *testing.T) {
			path := "/internal/blob/" + tc.hash
			req := httptest.NewRequest(http.MethodHead, path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("HEAD %q: status = %d, want %d", tc.hash, w.Code, http.StatusBadRequest)
			}
		})
	}
}
