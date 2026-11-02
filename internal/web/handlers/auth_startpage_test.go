package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type fakeStartPrefs map[string]*bongov1.UserPreferences

func (f fakeStartPrefs) Get(userID string) *bongov1.UserPreferences { return f[userID] }

// startPageHandler is the canonical test handler with login preferences wired
// (nil leaves them unwired).
func startPageHandler(prefs StartPagePreferences, profile *bongov1.UserProfile) http.Handler {
	h := NewAuthHandler(&canonicalAuth{users: map[string]string{"bob@example.com": "u-bob"}}, throttleSessionSecret)
	h.SetProfileLoader(&canonicalProfiles{profile: profile})
	h.SetRaftApplier(discardApplier{})
	if prefs != nil {
		h.SetPreferences(prefs)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", h.LoginPage)
	mux.HandleFunc("POST /login", h.Login)
	mux.HandleFunc("POST /login/2fa", h.TwoFactorVerify)
	return mux
}

func TestLoginRedirectsToTheStartPage(t *testing.T) {
	tests := []struct {
		name  string
		prefs StartPagePreferences
		want  string
	}{
		{"preferences not wired", nil, "/summary"},
		{"no stored preference", fakeStartPrefs{}, "/summary"},
		{"summary", fakeStartPrefs{"u-bob": {StartPage: "summary"}}, "/summary"},
		{"inbox", fakeStartPrefs{"u-bob": {StartPage: "inbox"}}, "/mail/INBOX"},
		{"unknown value", fakeStartPrefs{"u-bob": {StartPage: "//evil.example/"}}, "/summary"},
		{"another user's preference", fakeStartPrefs{"u-eve": {StartPage: "inbox"}}, "/summary"},
	}
	for _, tt := range tests {
		t.Run("login/"+tt.name, func(t *testing.T) {
			h := startPageHandler(tt.prefs, &bongov1.UserProfile{Email: "bob@example.com"})
			rr := postForm(h, "/login", loginForm("bob@example.com", "right"))
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.want {
				t.Fatalf("login = %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), tt.want)
			}
		})
		t.Run("2fa/"+tt.name, func(t *testing.T) {
			h := startPageHandler(tt.prefs, totpProfile())
			pc := middleware.CreatePendingCookie(throttleSessionSecret, "u-bob", "bob@example.com", false, false, false, "")
			rr := postForm(h, "/login/2fa", url.Values{"code": {totpCode(t)}}, pc)
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.want {
				t.Fatalf("2FA completion = %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), tt.want)
			}
		})
		t.Run("login page/"+tt.name, func(t *testing.T) {
			h := startPageHandler(tt.prefs, &bongov1.UserProfile{Email: "bob@example.com"})
			req := withUserContext(httptest.NewRequest(http.MethodGet, "/login", nil), "u-bob", "bob@example.com")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != tt.want {
				t.Fatalf("login page for a signed-in user = %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), tt.want)
			}
		})
	}
}

func TestRecoveryLoginKeepsTheStartPageAndWarning(t *testing.T) {
	tests := []struct {
		name  string
		prefs StartPagePreferences
		path  string
	}{
		{"summary", fakeStartPrefs{"u-bob": {StartPage: "summary"}}, "/summary"},
		{"inbox", fakeStartPrefs{"u-bob": {StartPage: "inbox"}}, "/mail/INBOX"},
		{"no stored preference", fakeStartPrefs{}, "/summary"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain, hashed, err := auth.GenerateRecoveryCodes(3)
			if err != nil {
				t.Fatal(err)
			}
			p := totpProfile()
			p.RecoveryCodes = hashed
			h := startPageHandler(tt.prefs, p)
			pc := middleware.CreatePendingCookie(throttleSessionSecret, "u-bob", "bob@example.com", false, false, false, "")
			rr := postForm(h, "/login/2fa", url.Values{"code": {plain[0]}}, pc)
			want := tt.path + "?warning=" + urlEncode("Recovery code used. You have 2 code(s) remaining.")
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != want {
				t.Fatalf("recovery login = %d %q, want 303 %q", rr.Code, rr.Header().Get("Location"), want)
			}
		})
	}
}
