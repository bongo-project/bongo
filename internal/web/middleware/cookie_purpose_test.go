package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// serveWithCookieValue sends value as the session cookie through RequireAuth
// and reports whether the downstream handler ran plus the recorded response.
func serveWithCookieValue(value string, live LiveSessionState) (called bool, rr *httptest.ResponseRecorder) {
	handler := RequireAuth(testSecret, func(string) LiveSessionState { return live })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))
	req := httptest.NewRequest("GET", "/mail/INBOX", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: value})
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return called, rr
}

// A 2FA-pending cookie is issued before the TOTP step. It must never be
// accepted as a session, whether or not the account has a live epoch.
func TestRequireAuth_RejectsPendingCookieAsSession(t *testing.T) {
	pending := CreatePendingCookie(testSecret, "u1", "a@x", true, true, false).Value
	session := CreateSessionCookie(testSecret, "u1", "a@x", false, false, false, "e1").Value

	tests := []struct {
		name       string
		value      string
		live       LiveSessionState
		wantCalled bool
	}{
		{"positive control: genuine session", session, LiveSessionState{Found: true, Epoch: "e1"}, true},
		{"pending cookie, live epoch set", pending, LiveSessionState{Found: true, Epoch: "e1"}, false},
		{"pending cookie, live epoch empty", pending, LiveSessionState{Found: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called, rr := serveWithCookieValue(tt.value, tt.live)
			if called != tt.wantCalled {
				t.Fatalf("handler called = %v, want %v", called, tt.wantCalled)
			}
			if !tt.wantCalled && (rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login") {
				t.Errorf("got %d Location %q, want 303 /login", rr.Code, rr.Header().Get("Location"))
			}
		})
	}
}

// Each cookie type must be accepted only by its own verifier.
func TestCookieVerifiers_RejectOtherTypes(t *testing.T) {
	session := CreateSessionCookie(testSecret, "u1", "a@x", false, false, false, "e1").Value
	pending := CreatePendingCookie(testSecret, "u1", "a@x", false, false, false).Value
	setupPayload, _ := json.Marshal(map[string]any{"secret": "S", "exp": time.Now().Add(time.Minute).Unix()})
	setup := SignValue(testSecret, TOTPSetupCookiePurpose, setupPayload)

	verifiers := []struct {
		name string
		fn   func(string) bool
	}{
		{"session", func(v string) bool {
			called, _ := serveWithCookieValue(v, LiveSessionState{Found: true, Epoch: "e1"})
			return called
		}},
		{"pending", func(v string) bool {
			req := httptest.NewRequest("GET", "/", nil)
			req.AddCookie(&http.Cookie{Name: pendingCookieName, Value: v})
			_, err := ValidatePendingCookie(testSecret, req)
			return err == nil
		}},
		{"totp-setup", func(v string) bool {
			_, err := VerifyValue(testSecret, TOTPSetupCookiePurpose, v)
			return err == nil
		}},
	}
	values := map[string]string{"session": session, "pending": pending, "totp-setup": setup}
	for _, v := range verifiers {
		for kind, val := range values {
			t.Run(v.name+" verifier given "+kind, func(t *testing.T) {
				if got, want := v.fn(val), kind == v.name; got != want {
					t.Errorf("accepted = %v, want %v", got, want)
				}
			})
		}
	}
}
