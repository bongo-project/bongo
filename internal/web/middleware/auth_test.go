package middleware

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var testSecret = []byte("test-secret-key-32-bytes-long!!")

func TestCreateSessionCookie(t *testing.T) {
	cookie := CreateSessionCookie(testSecret, "user123", "test@example.com", false, false, false, "")

	if cookie.Name != "bongo_session" {
		t.Errorf("cookie name = %q, want bongo_session", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Value == "" {
		t.Error("cookie value should not be empty")
	}

	// Should be valid.
	data, err := validateCookie(testSecret, cookie.Value)
	if err != nil {
		t.Fatalf("validateCookie failed: %v", err)
	}
	if data.UserID != "user123" {
		t.Errorf("userID = %q, want user123", data.UserID)
	}
	if data.Email != "test@example.com" {
		t.Errorf("email = %q, want test@example.com", data.Email)
	}
	if data.Exp <= time.Now().Unix() {
		t.Error("expiry should be in the future")
	}
}

func TestCreateSessionCookie_Remember(t *testing.T) {
	normal := CreateSessionCookie(testSecret, "u", "e", false, false, false, "")
	remember := CreateSessionCookie(testSecret, "u", "e", true, false, false, "")

	if remember.MaxAge <= normal.MaxAge {
		t.Error("remember cookie should have longer MaxAge")
	}
}

func TestClearSessionCookie(t *testing.T) {
	cookie := ClearSessionCookie()
	if cookie.MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", cookie.MaxAge)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", cookie.SameSite)
	}
}

func TestValidateCookie_InvalidSignature(t *testing.T) {
	cookie := CreateSessionCookie(testSecret, "user123", "test@example.com", false, false, false, "")

	// Tamper with cookie.
	_, err := validateCookie([]byte("wrong-secret"), cookie.Value)
	if err == nil {
		t.Error("expected error with wrong secret")
	}
}

func TestValidateCookie_MalformedValue(t *testing.T) {
	_, err := validateCookie(testSecret, "not-a-valid-cookie")
	if err == nil {
		t.Error("expected error with malformed value")
	}

	_, err = validateCookie(testSecret, ".")
	if err == nil {
		t.Error("expected error with empty parts")
	}
}

func TestRequireAuth_NoCookie(t *testing.T) {
	handler := RequireAuth(testSecret, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without auth")
	}))

	req := httptest.NewRequest("GET", "/mail/INBOX", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("redirect to %q, want /login", loc)
	}
}

func TestRequireAuth_ValidCookie(t *testing.T) {
	var gotUserID, gotEmail string
	handler := RequireAuth(testSecret, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID, gotEmail = UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	cookie := CreateSessionCookie(testSecret, "user123", "test@example.com", false, false, false, "")
	req := httptest.NewRequest("GET", "/mail/INBOX", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if gotUserID != "user123" {
		t.Errorf("userID = %q, want user123", gotUserID)
	}
	if gotEmail != "test@example.com" {
		t.Errorf("email = %q, want test@example.com", gotEmail)
	}
}

func TestRequireAuth_DisabledUserRevoked(t *testing.T) {
	called := false
	stateFn := func(userID string) LiveSessionState {
		return LiveSessionState{Found: true, Disabled: userID == "user123"}
	}
	handler := RequireAuth(testSecret, stateFn)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	cookie := CreateSessionCookie(testSecret, "user123", "test@example.com", false, false, false, "")
	req := httptest.NewRequest("GET", "/mail/INBOX", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("handler was called for a disabled user; session should be revoked")
	}
	if rr.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 (redirect to login)", rr.Code)
	}
	// The stale session cookie should be cleared.
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == cookie.Name && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("session cookie was not cleared for the disabled user")
	}
}

// signedSessionCookie mints a session cookie with an arbitrary payload, signed
// correctly with secret. CreateSessionCookie always sets a future Exp, so this
// is the only way to build a genuinely expired cookie without sleeping.
//
// The valid signature is the point: an expired-but-unsigned cookie would be
// rejected by the HMAC check at validateCookie and never reach the expiry
// check, so a test built on one would pass even with expiry enforcement
// deleted. It has to be a cookie the server would otherwise trust.
func signedSessionCookie(data SessionData) *http.Cookie {
	payload, _ := json.Marshal(data)
	sig := sign(testSecret, payload)
	return &http.Cookie{
		Name: cookieName,
		Value: base64.RawURLEncoding.EncodeToString(payload) + "." +
			base64.RawURLEncoding.EncodeToString(sig),
		Path: "/",
	}
}

func TestRequireAuth_ExpiredCookie(t *testing.T) {
	called := false
	handler := RequireAuth(testSecret, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	cookie := signedSessionCookie(SessionData{
		UserID: "user123",
		Email:  "test@example.com",
		Exp:    time.Now().Add(-1 * time.Hour).Unix(),
	})

	req := httptest.NewRequest("GET", "/mail/INBOX", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("handler was called with an expired session cookie")
	}
	if rr.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 (redirect to login)", rr.Code)
	}
}

// TestRequireAuth_ExpiryBoundary pins the comparison itself. The guard is
// `time.Now().Unix() > data.Exp`, so a cookie expiring one second from now is
// still good and one that expired a second ago is not. Without this, changing
// the operator to >= or flipping the operands stays green.
func TestRequireAuth_ExpiryBoundary(t *testing.T) {
	tests := []struct {
		name   string
		exp    int64
		wantOK bool
	}{
		{"one second in the future", time.Now().Unix() + 1, true},
		{"one second in the past", time.Now().Unix() - 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := RequireAuth(testSecret, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/mail/INBOX", nil)
			req.AddCookie(signedSessionCookie(SessionData{
				UserID: "user123",
				Email:  "test@example.com",
				Exp:    tt.exp,
			}))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if called != tt.wantOK {
				t.Errorf("handler called = %v, want %v (Exp=%d, now=%d)",
					called, tt.wantOK, tt.exp, time.Now().Unix())
			}
		})
	}
}

func TestRequireAuth_HTMXRedirect(t *testing.T) {
	handler := RequireAuth(testSecret, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without auth")
	}))

	req := httptest.NewRequest("GET", "/mail/INBOX", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// HTMX requests get HX-Redirect header instead of a 303.
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for HTMX redirect", rr.Code)
	}
	if loc := rr.Header().Get("HX-Redirect"); loc != "/login" {
		t.Errorf("HX-Redirect = %q, want /login", loc)
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Errorf("Location header should be empty for HTMX, got %q", loc)
	}
}

func TestUserFromContext_Empty(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	userID, email := UserFromContext(req.Context())
	if userID != "" || email != "" {
		t.Errorf("expected empty values, got userID=%q email=%q", userID, email)
	}
}

func TestRequireAuth_EpochMismatchRevoked(t *testing.T) {
	called := false
	// Session was issued with epoch "old"; the live state reports "new" (e.g.
	// the password was changed since login) -> revoke.
	stateFn := func(string) LiveSessionState {
		return LiveSessionState{Found: true, Epoch: "new"}
	}
	handler := RequireAuth(testSecret, stateFn)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	cookie := CreateSessionCookie(testSecret, "user123", "e@x.com", false, false, false, "old")
	req := httptest.NewRequest("GET", "/mail/INBOX", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if called {
		t.Error("handler ran despite epoch mismatch; stale session should be revoked")
	}
	if rr.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", rr.Code)
	}
}

func TestRequireAuth_LiveAdminOverride(t *testing.T) {
	var sawAdmin bool
	// Cookie snapshot says non-admin, but the live state reports admin -> the
	// request context must reflect the live value (and vice versa for demotion).
	stateFn := func(string) LiveSessionState {
		return LiveSessionState{Found: true, IsAdmin: true, Epoch: "e"}
	}
	handler := RequireAuth(testSecret, stateFn)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAdmin = AdminFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	cookie := CreateSessionCookie(testSecret, "user123", "e@x.com", false, false /*isAdmin*/, false, "e")
	req := httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(cookie)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !sawAdmin {
		t.Error("live admin promotion not reflected in context (M6)")
	}
}
