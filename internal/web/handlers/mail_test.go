package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/store"
)

func TestBlockRemoteImages(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantBlocked bool
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "no images",
			input:       `<p>Hello world</p>`,
			wantBlocked: false,
			wantContain: "Hello world",
		},
		{
			name:        "http remote image blocked",
			input:       `<img src="http://example.com/tracker.gif">`,
			wantBlocked: true,
			wantContain: `class="blocked-image"`,
			wantAbsent:  `<img`,
		},
		{
			name:        "https remote image blocked",
			input:       `<img src="https://example.com/image.png" alt="test">`,
			wantBlocked: true,
			wantContain: `data-src="https://example.com/image.png"`,
			wantAbsent:  `<img`,
		},
		{
			name:        "cid inline image preserved",
			input:       `<img src="cid:abc123@example.com">`,
			wantBlocked: false,
			wantContain: `<img`,
		},
		{
			name:        "data uri inline image preserved",
			input:       `<img src="data:image/png;base64,abc123">`,
			wantBlocked: false,
			wantContain: `<img`,
		},
		{
			name:        "mixed: remote blocked cid preserved",
			input:       `<img src="cid:part1"><img src="https://tracker.example.com/px.gif">`,
			wantBlocked: true,
			wantContain: `cid:part1`,
		},
		{
			name:        "single-quoted src",
			input:       `<img src='https://example.com/image.png'>`,
			wantBlocked: true,
			wantContain: `class="blocked-image"`,
		},
		{
			name:        "original url stored in data-src",
			input:       `<img src="https://example.com/track.gif">`,
			wantBlocked: true,
			wantContain: `[Remote image blocked]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, blocked := blockRemoteImages(tc.input)
			if blocked != tc.wantBlocked {
				t.Errorf("blocked = %v, want %v", blocked, tc.wantBlocked)
			}
			if tc.wantContain != "" && !strings.Contains(got, tc.wantContain) {
				t.Errorf("output does not contain %q\ngot: %s", tc.wantContain, got)
			}
			if tc.wantAbsent != "" && strings.Contains(got, tc.wantAbsent) {
				t.Errorf("output should not contain %q\ngot: %s", tc.wantAbsent, got)
			}
		})
	}
}

func setupServeBlobRouter(t *testing.T) (*chi.Mux, *store.BlobStore) {
	t.Helper()
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	// NewMailHandler with nil pebble/mailboxes — ServeBlob only uses blobs.
	h := NewMailHandler(nil, bs, nil, discardLogger())
	r := chi.NewRouter()
	r.Get("/mail/blob/{hash}", h.ServeBlob)
	return r, bs
}

func TestServeBlobInvalidHash(t *testing.T) {
	router, _ := setupServeBlobRouter(t)

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
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/mail/blob/"+tc.hash, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("ServeBlob(%q): status = %d, want %d", tc.hash, w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestServeBlobValidHashNotFound(t *testing.T) {
	router, _ := setupServeBlobRouter(t)

	hash := "0000000000000000000000000000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "/mail/blob/"+hash, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Valid hash but blob does not exist — 404, not 400.
	if w.Code != http.StatusNotFound {
		t.Errorf("ServeBlob valid-not-found: status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
