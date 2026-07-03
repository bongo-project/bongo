package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bongo-go/bongo-go/internal/security"
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
