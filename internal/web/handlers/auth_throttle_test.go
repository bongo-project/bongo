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

const throttleTestIP = "203.0.113.50:4321"

var throttleSessionSecret = []byte("0123456789abcdef0123456789abcdef")

// throttleAuth accepts a fixed password for the listed accounts only.
type throttleAuth struct{ users map[string]string }

func (a *throttleAuth) Authenticate(username, password string) (string, error) {
	if id, ok := a.users[username]; ok && password == "right" {
		return id, nil
	}
	return "", auth.ErrBadCredentials
}

// throttleProfiles serves a TOTP-enabled profile per user ID.
type throttleProfiles struct{ secrets map[string][]byte }

func (p *throttleProfiles) GetKey(key []byte) ([]byte, error) {
	parts := strings.Split(string(key), "/")
	if len(parts) < 3 {
		return nil, http.ErrNoCookie
	}
	secret, ok := p.secrets[parts[2]]
	if !ok {
		return nil, http.ErrNoCookie
	}
	return proto.Marshal(&bongov1.UserProfile{TotpEnabled: true, TotpSecret: secret})
}

func newThrottleHandler(t *testing.T, max int) (http.Handler, *middleware.LoginRateLimiter, *AuthHandler) {
	t.Helper()
	h := NewAuthHandler(&throttleAuth{users: map[string]string{
		"attacker@example.com": "u-attacker",
		"victim@example.com":   "u-victim",
	}}, throttleSessionSecret)
	rl := middleware.NewLoginRateLimiter(max, time.Minute)
	t.Cleanup(rl.Stop)
	h.SetRateLimiter(rl)

	mux := http.NewServeMux()
	mux.Handle("POST /login", rl.LoginRateLimit()(http.HandlerFunc(h.Login)))
	mux.Handle("POST /login/2fa", rl.LoginRateLimit()(http.HandlerFunc(h.TwoFactorVerify)))
	return mux, rl, h
}

func postForm(h http.Handler, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = throttleTestIP
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func loginForm(email, password string) url.Values {
	return url.Values{"email": {email}, "password": {password}}
}

// An attacker logging in to their own account between guesses at a victim must
// not reset the per-IP throttle on /login.
func TestLoginThrottleOtherAccountSuccessDoesNotClear(t *testing.T) {
	const max = 3
	h, _, _ := newThrottleHandler(t, max)

	for range max - 1 {
		postForm(h, "/login", loginForm("victim@example.com", "guess"))
	}
	rr := postForm(h, "/login", loginForm("attacker@example.com", "right"))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/mail/INBOX" {
		t.Fatalf("attacker login = %d %q, want redirect to inbox", rr.Code, rr.Header().Get("Location"))
	}
	postForm(h, "/login", loginForm("victim@example.com", "guess"))
	if rr := postForm(h, "/login", loginForm("victim@example.com", "right")); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("post after attacker success = %d, want 429", rr.Code)
	}
}

// Positive control: a user's own successful login forgives their own typos.
func TestLoginThrottleOwnSuccessForgivesOwnFailures(t *testing.T) {
	const max = 3
	h, rl, _ := newThrottleHandler(t, max)

	for range max - 1 {
		postForm(h, "/login", loginForm("Victim@example.com", "typo"))
	}
	if rr := postForm(h, "/login", loginForm("victim@example.com", "right")); rr.Code != http.StatusSeeOther {
		t.Fatalf("own login = %d, want 303", rr.Code)
	}
	if rl.IsLimited("203.0.113.50") {
		t.Fatal("own failures should be forgiven")
	}
	for range max - 1 {
		postForm(h, "/login", loginForm("victim@example.com", "typo"))
	}
	if rl.IsLimited("203.0.113.50") {
		t.Fatal("budget should be fresh after own success")
	}
}

// The same bypass on /login/2fa: finishing one's own TOTP login must not clear
// failures against someone else's code.
func TestTwoFactorThrottleOtherAccountSuccessDoesNotClear(t *testing.T) {
	const max = 3
	h, _, ah := newThrottleHandler(t, max)

	secrets := map[string][]byte{
		"u-attacker": []byte("attacker-secret-1234"),
		"u-victim":   []byte("victim-secret-123456"),
	}
	ah.SetProfileLoader(&throttleProfiles{secrets: secrets})

	attacker := middleware.CreatePendingCookie(throttleSessionSecret, "u-attacker", "attacker@example.com", false, false, false)
	victim := middleware.CreatePendingCookie(throttleSessionSecret, "u-victim", "victim@example.com", false, false, false)
	code, err := totp.GenerateCode(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secrets["u-attacker"]), time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	for range max - 1 {
		postForm(h, "/login/2fa", url.Values{"code": {"000000"}}, victim)
	}
	rr := postForm(h, "/login/2fa", url.Values{"code": {code}}, attacker)
	if rr.Header().Get("Location") != "/mail/INBOX" {
		t.Fatalf("attacker 2FA = %d %q, want redirect to inbox", rr.Code, rr.Header().Get("Location"))
	}
	postForm(h, "/login/2fa", url.Values{"code": {"000000"}}, victim)
	if rr := postForm(h, "/login/2fa", url.Values{"code": {"000000"}}, victim); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("2FA after attacker success = %d, want 429", rr.Code)
	}
}
