package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bongo-project/bongo/internal/security"
)

func TestRequireInternalAuth(t *testing.T) {
	handler := RequireInternalAuth(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/blob/hash", nil)
	security.SignInternalRequest(req, testSecret)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
}

func TestRequireInternalAuth_MissingHeader(t *testing.T) {
	handler := RequireInternalAuth(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/internal/blob/hash", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestRequireBlobSyncAuth(t *testing.T) {
	tests := []struct {
		name string
		sign func(*http.Request, []byte)
		want int
	}{
		{"blob-sync signed", security.SignBlobSyncRequest, http.StatusNoContent},
		{"control-plane signed", security.SignInternalRequest, http.StatusUnauthorized},
		{"unsigned", func(*http.Request, []byte) {}, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := RequireBlobSyncAuth(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/internal/blob/hash", nil)
			tt.sign(req, testSecret)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tt.want || called != (tt.want == http.StatusNoContent) {
				t.Errorf("status = %d called = %v, want %d", rr.Code, called, tt.want)
			}
		})
	}
}

func TestRequireInternalAuth_RejectsBlobSyncSignature(t *testing.T) {
	handler := RequireInternalAuth(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	req := httptest.NewRequest(http.MethodGet, "/internal/kv/x", nil)
	security.SignBlobSyncRequest(req, testSecret)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}
