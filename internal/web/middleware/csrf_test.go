package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func csrfCtx(userID string) context.Context {
	return WithUserContext(context.Background(), userID, "e@x.com")
}

func TestRequireCSRF_RejectsMissingToken(t *testing.T) {
	called := false
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest("POST", "/compose/send", nil).WithContext(csrfCtx("u1"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if called {
		t.Error("handler ran without a CSRF token")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestRequireCSRF_AcceptsValidToken(t *testing.T) {
	called := false
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest("POST", "/compose/send", nil).WithContext(csrfCtx("u1"))
	req.Header.Set(csrfHeaderName, csrfToken(testSecret, "u1"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !called {
		t.Errorf("handler did not run with a valid token (status %d)", rr.Code)
	}
}

func TestRequireCSRF_AcceptsFormFieldToken(t *testing.T) {
	// Native <form method="post"> submissions carry the token in a hidden _csrf
	// field, not a header.
	called := false
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	body := url.Values{"_csrf": {csrfToken(testSecret, "u1")}, "subject": {"hi"}}.Encode()
	req := httptest.NewRequest("POST", "/compose/send", strings.NewReader(body)).WithContext(csrfCtx("u1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !called {
		t.Errorf("handler did not run with a valid _csrf form field (status %d)", rr.Code)
	}
}

func TestRequireCSRF_RejectsWrongFormFieldToken(t *testing.T) {
	called := false
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	body := url.Values{"_csrf": {csrfToken(testSecret, "attacker")}}.Encode()
	req := httptest.NewRequest("POST", "/compose/send", strings.NewReader(body)).WithContext(csrfCtx("victim"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if called {
		t.Error("handler ran with another user's token in _csrf field")
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestRequireCSRF_RejectsOtherUsersToken(t *testing.T) {
	h := RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler ran with another user's token")
	}))
	req := httptest.NewRequest("POST", "/compose/send", nil).WithContext(csrfCtx("victim"))
	req.Header.Set(csrfHeaderName, csrfToken(testSecret, "attacker"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestRequireCSRF_SafeMethodsAndDevBypass(t *testing.T) {
	// GET passes without a token.
	getOK := false
	RequireCSRF(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { getOK = true })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/mail/INBOX", nil).WithContext(csrfCtx("u1")))
	if !getOK {
		t.Error("GET should pass without a token")
	}
	// enforce=false (dev) passes POST without a token.
	devOK := false
	RequireCSRF(testSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { devOK = true })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/x", nil).WithContext(csrfCtx("u1")))
	if !devOK {
		t.Error("dev-mode (enforce=false) POST should pass without a token")
	}
}
