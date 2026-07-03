package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSameOrigin_AllowsSameOrigin(t *testing.T) {
	handler := RequireSameOrigin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodPost, "https://mail.example.com/logout", nil)
	req.Host = "mail.example.com"
	req.Header.Set("Origin", "https://mail.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
}

func TestRequireSameOrigin_RejectsCrossSite(t *testing.T) {
	handler := RequireSameOrigin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "https://mail.example.com/logout", nil)
	req.Host = "mail.example.com"
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}
