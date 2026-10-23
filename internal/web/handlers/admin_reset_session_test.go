package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func resetRequest(userID string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/admin/users/"+userID+"/reset-password", nil)
	ctx := middleware.WithUserContext(r.Context(), "admin-id", "admin@example.com")
	ctx = middleware.WithAdminScopeContext(ctx, globalAdminScope())
	return withChiParam(r.WithContext(ctx), "userID", userID)
}

func readStoredProfile(t *testing.T, h *AdminHandler, userID string) *bongov1.UserProfile {
	t.Helper()
	p, err := h.loadProfile(userID)
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	return p
}

func putStoredProfile(t *testing.T, applier *directApplierTags, userID string, p *bongov1.UserProfile) {
	t.Helper()
	b, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/user/" + userID + "/profile"), Value: b,
	}, 0); err != nil {
		t.Fatal(err)
	}
}

// The reset must bump SessionGeneration explicitly, so sessions end even if the
// epoch derivation stops hashing the password, and the stored hash must change.
func TestResetPasswordBumpsSessionGeneration(t *testing.T) {
	const userID = "u-gen"
	h, _, _, applier := newQuotaTestEnv(t)
	old, _ := bcrypt.GenerateFromPassword([]byte("old"), bcrypt.MinCost)
	putStoredProfile(t, applier, userID, &bongov1.UserProfile{Email: "g@example.com", PasswordHash: old, SessionGeneration: 4})

	w := httptest.NewRecorder()
	h.ResetPassword(w, resetRequest(userID))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	after := readStoredProfile(t, h, userID)
	if after.SessionGeneration != 5 {
		t.Errorf("SessionGeneration = %d, want 5", after.SessionGeneration)
	}
	if string(after.PasswordHash) == string(old) {
		t.Error("password hash unchanged")
	}
}

// A write that lands between the handler's first read and its save (while it
// waits for the user lock) must survive: the profile is re-read under the lock.
func TestResetPasswordKeepsConcurrentTotpLastStep(t *testing.T) {
	const userID = "u-race"
	h, _, _, applier := newQuotaTestEnv(t)
	old, _ := bcrypt.GenerateFromPassword([]byte("old"), bcrypt.MinCost)
	base := &bongov1.UserProfile{Email: "r@example.com", PasswordHash: old}
	putStoredProfile(t, applier, userID, base)

	unlock := lockUser(userID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ResetPassword(httptest.NewRecorder(), resetRequest(userID))
	}()
	// Let the handler read the profile and block on the lock.
	time.Sleep(300 * time.Millisecond)
	updated := proto.Clone(base).(*bongov1.UserProfile)
	updated.TotpLastStep = 987654
	putStoredProfile(t, applier, userID, updated)
	unlock()
	<-done

	after := readStoredProfile(t, h, userID)
	if after.TotpLastStep != 987654 {
		t.Errorf("TotpLastStep = %d, want 987654 (lost update)", after.TotpLastStep)
	}
	if string(after.PasswordHash) == string(old) {
		t.Error("password hash unchanged")
	}
}

// A 2FA-pending cookie issued before an admin reset must not complete login
// afterwards, even with a valid code, and must not burn the TOTP step.
func TestTwoFactorVerifyRefusesPendingCookieAcrossReset(t *testing.T) {
	hasSession := func(rr *httptest.ResponseRecorder) bool {
		for _, c := range rr.Result().Cookies() {
			if c.Name == "bongo_session" && c.Value != "" && c.MaxAge >= 0 {
				return true
			}
		}
		return false
	}
	verify := func(env *reauthEnv, pending *http.Cookie) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /login/2fa", env.authH.TwoFactorVerify)
		return postForm(mux, "/login/2fa", url.Values{"code": {totpNow(t)}}, pending)
	}
	// issue gets the pending cookie from Login itself, so the epoch Login
	// stamps into it is what the test exercises.
	issue := func(t *testing.T, env *reauthEnv) *http.Cookie {
		t.Helper()
		mux := http.NewServeMux()
		mux.HandleFunc("POST /login", env.authH.Login)
		rr := postForm(mux, "/login", loginForm(reauthEmail, "right"))
		for _, c := range rr.Result().Cookies() {
			if c.Name == "bongo_2fa_pending" && c.Value != "" {
				return c
			}
		}
		t.Fatalf("no pending cookie from Login (%d %q)", rr.Code, rr.Header().Get("Location"))
		return nil
	}

	t.Run("login stamps profile epoch into pending cookie", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		pc := issue(t, env)
		req := httptest.NewRequest(http.MethodPost, "/login/2fa", nil)
		req.AddCookie(pc)
		pd, err := middleware.ValidatePendingCookie(throttleSessionSecret, req)
		if err != nil {
			t.Fatal(err)
		}
		p := env.store.profile(t)
		want := security.SessionEpoch(p.PasswordHash, p.TotpSecret, p.TotpEnabled, p.SessionGeneration)
		if pd.Epoch == "" || pd.Epoch != want {
			t.Errorf("pending epoch %q, want %q", pd.Epoch, want)
		}
	})

	t.Run("no reset logs in", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		if rr := verify(env, issue(t, env)); !hasSession(rr) {
			t.Fatalf("control failed: %d %q", rr.Code, rr.Header().Get("Location"))
		}
	})

	t.Run("reset between password and code step", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		pending := issue(t, env)
		p := env.store.profile(t)
		newHash, _ := bcrypt.GenerateFromPassword([]byte("reset"), bcrypt.MinCost)
		p.PasswordHash = newHash
		p.SessionGeneration++
		env.store.put(t, p)
		stepBefore := p.TotpLastStep

		rr := verify(env, pending)
		if hasSession(rr) {
			t.Fatal("pre-reset pending cookie minted a session")
		}
		if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/login") || strings.HasPrefix(loc, "/login/2fa") {
			t.Errorf("redirect to %q, want /login", loc)
		}
		if got := env.store.profile(t).TotpLastStep; got != stepBefore {
			t.Errorf("TOTP step consumed: %d -> %d", stepBefore, got)
		}
	})
}

// A SessionGeneration-only change (hash and TOTP secret untouched) must also
// refuse the pending cookie, from a zero and from a non-zero starting
// generation. Each generation has a no-change control so a verifier that
// compares against a fixed generation fails one of them.
func TestTwoFactorVerifyRefusesPendingCookieOnGenerationOnlyChange(t *testing.T) {
	for _, startGen := range []uint64{0, 3} {
		for _, bump := range []bool{false, true} {
			name := "unchanged"
			if bump {
				name = "generation bumped"
			}
			t.Run(name+"/gen"+string(rune('0'+startGen)), func(t *testing.T) {
				env := newReauthEnv(t, true, 50)
				p := env.store.profile(t)
				p.SessionGeneration = startGen
				env.store.put(t, p)

				mux := http.NewServeMux()
				mux.HandleFunc("POST /login", env.authH.Login)
				mux.HandleFunc("POST /login/2fa", env.authH.TwoFactorVerify)
				var pending *http.Cookie
				for _, c := range postForm(mux, "/login", loginForm(reauthEmail, "right")).Result().Cookies() {
					if c.Name == "bongo_2fa_pending" && c.Value != "" {
						pending = c
					}
				}
				if pending == nil {
					t.Fatal("no pending cookie")
				}

				p = env.store.profile(t)
				if bump {
					p.SessionGeneration++
					env.store.put(t, p)
				}
				stepBefore := p.TotpLastStep

				rr := postForm(mux, "/login/2fa", url.Values{"code": {totpNow(t)}}, pending)
				minted := false
				for _, c := range rr.Result().Cookies() {
					if c.Name == "bongo_session" && c.Value != "" && c.MaxAge >= 0 {
						minted = true
					}
				}
				if bump {
					if minted {
						t.Fatal("pending cookie survived a generation-only change")
					}
					if loc := rr.Header().Get("Location"); loc != "/login" {
						t.Errorf("redirect %q, want /login", loc)
					}
					if got := env.store.profile(t).TotpLastStep; got != stepBefore {
						t.Errorf("TOTP step consumed: %d -> %d", stepBefore, got)
					}
				} else if !minted {
					t.Fatalf("control failed: %d %q", rr.Code, rr.Header().Get("Location"))
				}
			})
		}
	}
}
