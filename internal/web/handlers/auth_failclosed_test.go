package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type okAuth struct{}

func (okAuth) Authenticate(email, password string) (string, error) { return "u1", nil }

// errProfiles returns err for every read, or serves profile when err is nil.
type errProfiles struct {
	err     error
	profile *bongov1.UserProfile
}

func (p errProfiles) GetKey(key []byte) ([]byte, error) {
	if p.err != nil {
		return nil, p.err
	}
	return proto.Marshal(p.profile)
}

func TestLoginFailsClosedOnProfileError(t *testing.T) {
	tests := []struct {
		name         string
		profiles     errProfiles
		wantCookie   string // "" means no cookie at all
		wantLocation string
		wantBody     string
	}{
		{"io error", errProfiles{err: errors.New("io")}, "", "", "temporarily unavailable"},
		{"profile missing", errProfiles{err: store.ErrNotFound}, "", "", "temporarily unavailable"},
		// Positive controls.
		{"totp profile still gets pending cookie", errProfiles{profile: &bongov1.UserProfile{Email: "a@example.com", TotpEnabled: true, TotpSecret: []byte("s")}}, "bongo_2fa_pending", "/login/2fa", ""},
		{"plain profile still gets session cookie", errProfiles{profile: &bongov1.UserProfile{Email: "a@example.com"}}, "bongo_session", "/mail/INBOX", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAuthHandler(okAuth{}, []byte("0123456789abcdef0123456789abcdef"))
			h.SetProfileLoader(tt.profiles)
			form := url.Values{"email": {"a@example.com"}, "password": {"pw"}}
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rr := httptest.NewRecorder()
			h.Login(rr, req)

			cookies := rr.Result().Cookies()
			if tt.wantCookie == "" {
				if len(cookies) != 0 {
					t.Errorf("cookies issued on profile error: %v", cookies)
				}
			} else if len(cookies) != 1 || cookies[0].Name != tt.wantCookie {
				t.Errorf("cookies = %v, want one %q", cookies, tt.wantCookie)
			}
			if loc := rr.Header().Get("Location"); loc != tt.wantLocation {
				t.Errorf("Location = %q, want %q", loc, tt.wantLocation)
			}
			if tt.wantBody != "" && !strings.Contains(rr.Body.String(), tt.wantBody) {
				t.Errorf("body missing %q", tt.wantBody)
			}
		})
	}
}

// failAfterFirst serves the first read and fails every later one.
type failAfterFirst struct {
	inner interface{ GetKey([]byte) ([]byte, error) }
	reads int
}

func (f *failAfterFirst) GetKey(key []byte) ([]byte, error) {
	f.reads++
	if f.reads > 1 {
		return nil, errors.New("io")
	}
	return f.inner.GetKey(key)
}

// A read failure after the 2FA check must not yield a session without an
// epoch, which RequireAuth would never revoke.
func TestTwoFactorVerifySessionHasEpochDespiteLaterReadFailure(t *testing.T) {
	tests := []struct {
		name string
		code func(env *reauthEnv, t *testing.T) string
	}{
		{"totp code", func(_ *reauthEnv, t *testing.T) string { return totpNow(t) }},
		{"recovery code", func(env *reauthEnv, _ *testing.T) string { return env.recovery[0] }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newReauthEnv(t, true, 50)
			env.authH.SetProfileLoader(&failAfterFirst{inner: env.store})
			pending := middleware.CreatePendingCookie(throttleSessionSecret, reauthUserID, reauthEmail, false, false, false)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /login/2fa", env.authH.TwoFactorVerify)
			rr := postForm(mux, "/login/2fa", url.Values{"code": {tt.code(env, t)}}, pending)

			var found bool
			for _, c := range rr.Result().Cookies() {
				if c.Name != "bongo_session" || c.MaxAge < 0 {
					continue
				}
				found = true
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.AddCookie(c)
				data, err := middleware.ParseSessionCookie(throttleSessionSecret, req)
				if err != nil {
					t.Fatal(err)
				}
				if data.Epoch == "" {
					t.Error("session issued with an empty epoch")
				}
			}
			if !found {
				t.Fatalf("no session cookie: %d %q", rr.Code, rr.Header().Get("Location"))
			}
		})
	}
}
