package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

// The TOTP-setup cookie must not authenticate as a session, and a session
// cookie must not be accepted as TOTP-setup state.
func TestTOTPSetupCookie_NotASession(t *testing.T) {
	secret := []byte("test-secret-key-32-bytes-long!!")
	h := NewSecurityHandler(nil, nil, nil, secret)

	rec := httptest.NewRecorder()
	h.setTOTPPendingCookie(rec, "JBSWY3DPEHPK3PXP")
	setup := rec.Result().Cookies()[0]

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(setup)
	if got, err := h.getTOTPPendingCookie(req); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("round trip: got %q, %v", got, err)
	}

	for _, live := range []middleware.LiveSessionState{{Found: true, Epoch: "e1"}, {Found: true}, {}} {
		called := false
		handler := middleware.RequireAuth(secret, func(string) middleware.LiveSessionState { return live })(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
		r := httptest.NewRequest("GET", "/mail/INBOX", nil)
		r.AddCookie(&http.Cookie{Name: "bongo_session", Value: setup.Value})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if called {
			t.Errorf("live=%+v: TOTP-setup cookie authenticated as a session", live)
		}
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("live=%+v: got %d %q, want 303 /login", live, w.Code, w.Header().Get("Location"))
		}
	}

	sess := middleware.CreateSessionCookie(secret, "u1", "a@x", false, false, false, "e1")
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: totpPendingCookieName, Value: sess.Value})
	if _, err := h.getTOTPPendingCookie(r); err == nil {
		t.Error("session cookie accepted as TOTP-setup cookie")
	}
}
