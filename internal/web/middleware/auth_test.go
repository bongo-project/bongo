package middleware

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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
	sig := sign(testSecret, sessionCookiePurpose, payload)
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

func TestRequireAuth_MissingUserRevoked(t *testing.T) {
	ran := false
	h := RequireAuth(testSecret, func(string) LiveSessionState { return LiveSessionState{Missing: true} })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true }))
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(CreateSessionCookie(testSecret, "u", "e@x.com", false, false, false, ""))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if ran || rr.Code != http.StatusSeeOther {
		t.Fatalf("ran=%v code=%d, want revoked 303", ran, rr.Code)
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == cookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("cookie not cleared")
	}
}

func TestRequireAuth_MissingAdminCannotReachAdmin(t *testing.T) {
	ran := false
	h := RequireAuth(testSecret, func(string) LiveSessionState { return LiveSessionState{Missing: true} })(
		RequireAdmin(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true })))
	req := httptest.NewRequest("GET", "/admin", nil)
	req.AddCookie(CreateSessionCookie(testSecret, "u", "e@x.com", false, true, false, ""))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if ran || rr.Code != http.StatusSeeOther {
		t.Fatalf("ran=%v code=%d, want revoked 303", ran, rr.Code)
	}
}

// A store error keeps the session readable but drops admin rights, whatever
// the cookie snapshot says.
func TestRequireAuth_StoreErrorDropsAdmin(t *testing.T) {
	var sawAdmin, sawUnavailable bool
	h := RequireAuth(testSecret, func(string) LiveSessionState { return LiveSessionState{} })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sawAdmin = AdminFromContext(r.Context())
			sawUnavailable = SessionStateUnavailable(r.Context())
		}))
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(CreateSessionCookie(testSecret, "u", "e@x.com", false, true, false, ""))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || sawAdmin || !sawUnavailable {
		t.Fatalf("code=%d admin=%v unavailable=%v, want 200 admin=false unavailable=true", rr.Code, sawAdmin, sawUnavailable)
	}
}

func TestSessionStateUnavailable_AdminGates(t *testing.T) {
	tests := []struct {
		name      string
		state     LiveSessionState
		path      string
		wantCode  int
		wantBody  string
		wantRan   bool
		wantAuthz bool
	}{
		{"store error, RequireAdmin", LiveSessionState{}, "/quarantine", 503, "account state unavailable", false, false},
		{"store error, domain gate", LiveSessionState{}, "/admin", 503, "account state unavailable", false, false},
		{"store error, plain route", LiveSessionState{}, "/mail", 200, "", true, false},
		{"found admin, RequireAdmin", LiveSessionState{Found: true, IsAdmin: true}, "/quarantine", 200, "", true, false},
		{"found admin, domain gate", LiveSessionState{Found: true, IsAdmin: true}, "/admin", 200, "", true, false},
		{"found non-admin, RequireAdmin", LiveSessionState{Found: true}, "/quarantine", 403, "", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran, authzCalled bool
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true })
			authz := func(string, bool) ([]string, error) {
				authzCalled = true
				return []string{"example.com"}, nil
			}
			r := chi.NewRouter()
			r.Use(RequireAuth(testSecret, func(string) LiveSessionState { return tt.state }))
			r.Get("/mail", handler)
			r.Group(func(r chi.Router) {
				r.Use(RequireAdmin(testSecret))
				r.Get("/quarantine", handler)
			})
			r.Group(func(r chi.Router) {
				r.Use(RequireAdminOrDomainAdmin(authz))
				r.Get("/admin", handler)
			})
			req := httptest.NewRequest("GET", tt.path, nil)
			req.AddCookie(CreateSessionCookie(testSecret, "u", "e@x.com", false, true, false, ""))
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != tt.wantCode {
				t.Fatalf("code=%d, want %d", rr.Code, tt.wantCode)
			}
			if tt.wantBody != "" && !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Fatalf("body=%q, want it to contain %q", rr.Body.String(), tt.wantBody)
			}
			if ran != tt.wantRan {
				t.Fatalf("handler ran=%v, want %v", ran, tt.wantRan)
			}
			if tt.wantCode == 503 && authzCalled {
				t.Fatal("authorizedFn called in unavailable state")
			}
		})
	}
}

// reissuedCookie builds a session issued under epochA and reissues it under
// epochB with a non-round remaining lifetime, as ChangePassword does.
func reissuedCookie(t *testing.T, secure bool) (*http.Cookie, SessionData) {
	t.Helper()
	base := CreateSessionCookie(testSecret, "user123", "e@x.com", false, false, false, "epochA")
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(base)
	data, err := ParseSessionCookie(testSecret, req)
	if err != nil || data == nil {
		t.Fatalf("ParseSessionCookie: %v", err)
	}
	if data.Epoch != "epochA" {
		t.Fatalf("base epoch = %q, want epochA", data.Epoch)
	}
	data.Exp = time.Now().Add(7*time.Hour + 13*time.Minute + 17*time.Second).Unix()
	return ReissueSessionCookie(testSecret, *data, secure, "epochB"), *data
}

func serveWithCookie(cookie *http.Cookie, epoch string) (called bool, rr *httptest.ResponseRecorder) {
	stateFn := func(string) LiveSessionState { return LiveSessionState{Found: true, Epoch: epoch} }
	handler := RequireAuth(testSecret, stateFn)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/mail/INBOX", nil)
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return called, rr
}

func TestReissueSessionCookie_RequireAuth(t *testing.T) {
	cookie, _ := reissuedCookie(t, false)

	t.Run("matching epoch passes", func(t *testing.T) {
		called, rr := serveWithCookie(cookie, "epochB")
		if !called || rr.Code != http.StatusOK {
			t.Fatalf("called=%v status=%d, want handler reached with 200", called, rr.Code)
		}
	})

	t.Run("other epoch refused and cookie cleared", func(t *testing.T) {
		called, rr := serveWithCookie(cookie, "epochC")
		if called {
			t.Fatal("handler ran despite epoch mismatch")
		}
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
			t.Fatalf("status=%d location=%q, want 303 /login", rr.Code, rr.Header().Get("Location"))
		}
		cleared := false
		for _, c := range rr.Result().Cookies() {
			if c.Name == cookieName && c.MaxAge < 0 {
				cleared = true
			}
		}
		if !cleared {
			t.Error("session cookie not cleared")
		}
	})

	t.Run("one signature byte changed is refused", func(t *testing.T) {
		i := strings.LastIndexByte(cookie.Value, '.')
		sig, err := base64.RawURLEncoding.DecodeString(cookie.Value[i+1:])
		if err != nil {
			t.Fatal(err)
		}
		sig[0] ^= 0x01
		tampered := *cookie
		tampered.Value = cookie.Value[:i+1] + base64.RawURLEncoding.EncodeToString(sig)
		called, rr := serveWithCookie(&tampered, "epochB")
		if called || rr.Code != http.StatusSeeOther {
			t.Fatalf("called=%v status=%d, want refused with 303", called, rr.Code)
		}
	})
}

func TestReissueSessionCookie_Attributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		t.Run(map[bool]string{true: "secure", false: "insecure"}[secure], func(t *testing.T) {
			cookie, data := reissuedCookie(t, secure)
			want := int(data.Exp - time.Now().Unix())
			if d := cookie.MaxAge - want; d < -1 || d > 1 {
				t.Errorf("MaxAge = %d, want within 1s of %d", cookie.MaxAge, want)
			}
			if !cookie.HttpOnly {
				t.Error("HttpOnly not set")
			}
			if cookie.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
			}
			if cookie.Secure != secure {
				t.Errorf("Secure = %v, want %v", cookie.Secure, secure)
			}
		})
	}
}
