package handlers

import (
	"encoding/base32"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// canonicalAuth mirrors LocalAuth.lookupUserID: exact match first, then a
// lower-cased, trimmed match.
type canonicalAuth struct{ users map[string]string }

func (a *canonicalAuth) Authenticate(username, password string) (string, error) {
	if password != "right" {
		return "", auth.ErrBadCredentials
	}
	if id, ok := a.users[username]; ok {
		return id, nil
	}
	if id, ok := a.users[strings.ToLower(strings.TrimSpace(username))]; ok {
		return id, nil
	}
	return "", auth.ErrBadCredentials
}

type canonicalProfiles struct{ profile *bongov1.UserProfile }

func (p *canonicalProfiles) GetKey([]byte) ([]byte, error) { return proto.Marshal(p.profile) }

var canonicalTOTPSecret = []byte("canonical-secret-123")

func newCanonicalHandler(stored string, profile *bongov1.UserProfile) http.Handler {
	h := NewAuthHandler(&canonicalAuth{users: map[string]string{stored: "u-bob"}}, throttleSessionSecret)
	h.SetProfileLoader(&canonicalProfiles{profile: profile})
	h.SetRaftApplier(discardApplier{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", h.Login)
	mux.HandleFunc("POST /login/2fa", h.TwoFactorVerify)
	return mux
}

func cookieByName(rr *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rr.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

func sessionEmail(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var sess *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Value != "" && !strings.Contains(c.Name, "pending") {
			sess = c
		}
	}
	if sess == nil {
		t.Fatalf("no session cookie set (status %d)", rr.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(sess)
	data, err := middleware.ParseSessionCookie(throttleSessionSecret, req)
	if err != nil {
		t.Fatalf("ParseSessionCookie: %v", err)
	}
	return data.Email
}

func TestLoginCanonicalisesSessionEmail(t *testing.T) {
	tests := []struct {
		name    string
		stored  string
		profile string
		typed   string
		want    string
	}{
		{"different case", "bob@example.com", "bob@example.com", "BOB@Example.com", "bob@example.com"},
		{"whitespace and CRLF", "bob@example.com", "bob@example.com", " bob@example.com\r\n", "bob@example.com"},
		{"exact login", "bob@example.com", "bob@example.com", "bob@example.com", "bob@example.com"},
		{"legacy mixed-case account keeps stored case", "Bob@Example.com", "Bob@Example.com", "Bob@Example.com", "Bob@Example.com"},
		{"profile without email falls back to normalised", "bob@example.com", "", "BOB@example.com\r\n", "bob@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCanonicalHandler(tt.stored, &bongov1.UserProfile{Email: tt.profile})
			rr := postForm(h, "/login", loginForm(tt.typed, "right"))
			if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/mail/INBOX" {
				t.Fatalf("login = %d %q, want 303 /mail/INBOX", rr.Code, rr.Header().Get("Location"))
			}
			got := sessionEmail(t, rr)
			if got != tt.want {
				t.Fatalf("session email = %q, want %q", got, tt.want)
			}
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("session email contains CR/LF: %q", got)
			}
		})
	}
}

func TestLoginWrongPasswordSetsNoSession(t *testing.T) {
	h := newCanonicalHandler("bob@example.com", &bongov1.UserProfile{Email: "bob@example.com"})
	rr := postForm(h, "/login", loginForm("BOB@Example.com", "wrong"))
	if rr.Code == http.StatusSeeOther {
		t.Fatalf("wrong password redirected to %q", rr.Header().Get("Location"))
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatalf("wrong password set cookies: %v", rr.Result().Cookies())
	}
}

func totpProfile() *bongov1.UserProfile {
	return &bongov1.UserProfile{Email: "bob@example.com", TotpEnabled: true, TotpSecret: canonicalTOTPSecret}
}

func totpCode(t *testing.T) string {
	t.Helper()
	code, err := totp.GenerateCode(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(canonicalTOTPSecret), time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	return code
}

func TestTwoFactorCanonicalisesSessionEmail(t *testing.T) {
	t.Run("pending cookie from login", func(t *testing.T) {
		h := newCanonicalHandler("bob@example.com", totpProfile())
		rr := postForm(h, "/login", loginForm("BOB@Example.com\r\n", "right"))
		pc := cookieByName(rr, "bongo_2fa_pending")
		if pc == nil {
			t.Fatalf("no pending cookie; status %d", rr.Code)
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(pc)
		pd, err := middleware.ValidatePendingCookie(throttleSessionSecret, req)
		if err != nil {
			t.Fatalf("ValidatePendingCookie: %v", err)
		}
		if pd.Email != "bob@example.com" {
			t.Fatalf("pending email = %q, want canonical", pd.Email)
		}
		rr2 := postForm(h, "/login/2fa", url.Values{"code": {totpCode(t)}}, pc)
		if rr2.Header().Get("Location") != "/mail/INBOX" {
			t.Fatalf("2FA = %d %q", rr2.Code, rr2.Header().Get("Location"))
		}
		if got := sessionEmail(t, rr2); got != "bob@example.com" {
			t.Fatalf("session email = %q", got)
		}
	})

	t.Run("forged raw pending cookie", func(t *testing.T) {
		h := newCanonicalHandler("bob@example.com", totpProfile())
		pc := middleware.CreatePendingCookie(throttleSessionSecret, "u-bob", "BOB@Example.com", false, false, false)
		rr := postForm(h, "/login/2fa", url.Values{"code": {totpCode(t)}}, pc)
		if rr.Header().Get("Location") != "/mail/INBOX" {
			t.Fatalf("2FA = %d %q", rr.Code, rr.Header().Get("Location"))
		}
		if got := sessionEmail(t, rr); got != "bob@example.com" {
			t.Fatalf("session email = %q, want canonical", got)
		}
	})

	t.Run("recovery code with forged raw pending cookie", func(t *testing.T) {
		plain, hashed, err := auth.GenerateRecoveryCodes(2)
		if err != nil {
			t.Fatal(err)
		}
		p := totpProfile()
		p.RecoveryCodes = hashed
		h := newCanonicalHandler("bob@example.com", p)
		pc := middleware.CreatePendingCookie(throttleSessionSecret, "u-bob", "BOB@Example.com\r\n", false, false, false)
		rr := postForm(h, "/login/2fa", url.Values{"code": {plain[0]}}, pc)
		if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/mail/INBOX?warning=") {
			t.Fatalf("2FA recovery = %d %q, want /mail/INBOX?warning=...", rr.Code, loc)
		}
		if got := sessionEmail(t, rr); got != "bob@example.com" {
			t.Fatalf("session email = %q, want canonical", got)
		}
	})

	t.Run("profile without email keeps pending email", func(t *testing.T) {
		p := totpProfile()
		p.Email = ""
		h := newCanonicalHandler("bob@example.com", p)
		pc := middleware.CreatePendingCookie(throttleSessionSecret, "u-bob", "bob@example.com", false, false, false)
		rr := postForm(h, "/login/2fa", url.Values{"code": {totpCode(t)}}, pc)
		if rr.Header().Get("Location") != "/mail/INBOX" {
			t.Fatalf("2FA = %d %q", rr.Code, rr.Header().Get("Location"))
		}
		if got := sessionEmail(t, rr); got != "bob@example.com" {
			t.Fatalf("session email = %q, want pending email retained", got)
		}
	})
}
