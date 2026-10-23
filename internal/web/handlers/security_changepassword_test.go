package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ChangePassword bumps SessionGeneration, keeps the calling device signed in
// with a re-issued cookie of unchanged expiry, and ends every other session.
func TestChangePasswordSessions(t *testing.T) {
	for _, twoFA := range []bool{false, true} {
		name := "no 2fa"
		if twoFA {
			name = "2fa"
		}
		t.Run(name, func(t *testing.T) {
			env := newReauthEnv(t, twoFA, 50)
			p := env.store.profile(t)
			p.SessionGeneration = 4
			env.store.put(t, p)
			epochOf := func(p *bongov1.UserProfile) string {
				return security.SessionEpoch(p.PasswordHash, p.TotpSecret, p.TotpEnabled, p.SessionGeneration)
			}
			oldEpoch := epochOf(p)

			this := middleware.CreateSessionCookie(throttleSessionSecret, reauthUserID, reauthEmail, false, false, false, oldEpoch)
			other := middleware.CreateSessionCookie(throttleSessionSecret, reauthUserID, reauthEmail, false, false, false, oldEpoch)
			oldData := parseSession(t, this)

			ep := reauthEndpoints[0]
			code := ""
			if twoFA {
				code = totpNow(t)
			}
			req := httptest.NewRequest(http.MethodPost, "/settings/security", nil)
			req.PostForm = ep.form("right", code)
			req.Form = req.PostForm
			req.RemoteAddr = throttleTestIP
			req.AddCookie(this)
			req = req.WithContext(middleware.WithUserContext(req.Context(), reauthUserID, reauthEmail))
			rr := httptest.NewRecorder()
			env.security.ChangePassword(rr, req)

			after := env.store.profile(t)
			if after.SessionGeneration != 5 {
				t.Fatalf("SessionGeneration = %d, want 5", after.SessionGeneration)
			}
			if twoFA && after.TotpLastStep == p.TotpLastStep {
				t.Error("consumed TOTP step not persisted with the change")
			}
			liveEpoch := epochOf(after)

			var reissued *http.Cookie
			for _, c := range rr.Result().Cookies() {
				if c.Name == "bongo_session" && c.Value != "" {
					reissued = c
				}
			}
			if reissued == nil {
				t.Fatal("this device's cookie was not re-issued")
			}
			nd := parseSession(t, reissued)
			if nd.Epoch != liveEpoch {
				t.Errorf("re-issued epoch %q, want live %q", nd.Epoch, liveEpoch)
			}
			if nd.Exp != oldData.Exp {
				t.Errorf("Exp changed %d -> %d; the lifetime must not be extended", oldData.Exp, nd.Exp)
			}
			if reissued.MaxAge <= 0 || int64(reissued.MaxAge) > nd.Exp {
				t.Errorf("MaxAge = %d", reissued.MaxAge)
			}

			stateFn := func(string) middleware.LiveSessionState {
				raw, _ := env.store.GetKey([]byte("/user/" + reauthUserID + "/profile"))
				var lp bongov1.UserProfile
				_ = proto.Unmarshal(raw, &lp)
				return middleware.LiveSessionState{Found: true, Epoch: epochOf(&lp)}
			}
			protected := middleware.RequireAuth(throttleSessionSecret, stateFn)(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
			get := func(c *http.Cookie) int {
				r := httptest.NewRequest(http.MethodGet, "/mail/INBOX", nil)
				r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
				w := httptest.NewRecorder()
				protected.ServeHTTP(w, r)
				return w.Code
			}
			if got := get(reissued); got != http.StatusOK {
				t.Errorf("re-issued cookie = %d, want 200", got)
			}
			if got := get(other); got != http.StatusSeeOther {
				t.Errorf("other device cookie = %d, want redirect", got)
			}
		})
	}
}

func parseSession(t *testing.T, c *http.Cookie) *middleware.SessionData {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	d, err := middleware.ParseSessionCookie(throttleSessionSecret, req)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// A throttled client must be refused before the expensive new-password hash.
// Not t.Parallel: it swaps the package-level hashPassword seam.
func TestChangePasswordThrottledSkipsHash(t *testing.T) {
	const max = 3
	var calls int
	orig := hashPassword
	hashPassword = func(p []byte, cost int) ([]byte, error) {
		calls++
		return orig(p, cost)
	}
	t.Cleanup(func() { hashPassword = orig })

	env := newReauthEnv(t, false, max)
	ep := reauthEndpoints[0]
	for range max {
		env.call(ep, "wrong", "")
	}
	if calls != max {
		t.Fatalf("hash calls before throttling = %d, want %d", calls, max)
	}
	rr := env.call(ep, "right", "")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", rr.Code)
	}
	if calls != max {
		t.Errorf("throttled request reached the hash (%d calls)", calls)
	}
}
