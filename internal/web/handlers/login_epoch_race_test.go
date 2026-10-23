package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// resetRacingAuth verifies the password against the stored hash, snapshots the
// profile it verified, then applies an admin-style reset (new hash, bumped
// generation) before returning: the window between bcrypt and the caller's
// next read.
type resetRacingAuth struct {
	t     *testing.T
	store *reauthStore
}

func (a *resetRacingAuth) verifyThenReset(username, password string) (string, *bongov1.UserProfile, error) {
	if username != reauthEmail {
		return "", nil, auth.ErrBadCredentials
	}
	snap := a.store.profile(a.t)
	if err := bcrypt.CompareHashAndPassword(snap.PasswordHash, []byte(password)); err != nil {
		return "", nil, auth.ErrBadCredentials
	}
	reset := proto.Clone(snap).(*bongov1.UserProfile)
	reset.PasswordHash, _ = bcrypt.GenerateFromPassword([]byte("reset"), bcrypt.MinCost)
	reset.SessionGeneration++
	a.store.put(a.t, reset)
	return reauthUserID, snap, nil
}

func (a *resetRacingAuth) Authenticate(username, password string) (string, error) {
	id, _, err := a.verifyThenReset(username, password)
	return id, err
}

func (a *resetRacingAuth) AuthenticateProfile(username, password string) (string, *bongov1.UserProfile, error) {
	return a.verifyThenReset(username, password)
}

// A login that verified the old password while a reset landed must not be
// issued a cookie carrying the post-reset epoch.
func TestLoginEpochBoundToVerifiedProfile(t *testing.T) {
	for _, twoFA := range []bool{false, true} {
		name := "no 2fa"
		if twoFA {
			name = "2fa"
		}
		t.Run(name, func(t *testing.T) {
			env := newReauthEnv(t, twoFA, 50)
			env.authH.auth = &resetRacingAuth{t: t, store: env.store}

			mux := http.NewServeMux()
			mux.HandleFunc("POST /login", env.authH.Login)
			mux.HandleFunc("POST /login/2fa", env.authH.TwoFactorVerify)
			rr := postForm(mux, "/login", loginForm(reauthEmail, "right"))

			live := env.store.profile(t)
			liveEpoch := security.SessionEpoch(live.PasswordHash, live.TotpSecret, live.TotpEnabled, live.SessionGeneration)

			var pending *http.Cookie
			for _, c := range rr.Result().Cookies() {
				if c.Value == "" {
					continue
				}
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.AddCookie(c)
				switch c.Name {
				case "bongo_session":
					d, err := middleware.ParseSessionCookie(throttleSessionSecret, req)
					if err == nil && d.Epoch == liveEpoch {
						t.Errorf("session cookie carries the post-reset epoch")
					}
				case "bongo_2fa_pending":
					pending = c
					d, err := middleware.ValidatePendingCookie(throttleSessionSecret, req)
					if err == nil && d.Epoch == liveEpoch {
						t.Errorf("pending cookie carries the post-reset epoch")
					}
				}
			}
			if twoFA {
				if pending == nil {
					t.Fatal("no pending cookie")
				}
				vr := postForm(mux, "/login/2fa", url.Values{"code": {totpNow(t)}}, pending)
				for _, c := range vr.Result().Cookies() {
					if c.Name == "bongo_session" && c.Value != "" && c.MaxAge >= 0 {
						t.Error("racing pending cookie minted a session")
					}
				}
			}
		})
	}
}
