package handlers

import (
	"bytes"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	reauthUserID = "u-alice"
	reauthEmail  = "alice@example.com"
)

var reauthTOTPSecret = []byte("alice-totp-secret-20")

func reauthSecretB32() string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(reauthTOTPSecret)
}

// reauthStore is a profile store that also applies Raft PUTs. failWrites makes
// every write fail so fail-closed behaviour can be asserted.
type reauthStore struct {
	mu         sync.Mutex
	data       map[string][]byte
	writes     int
	failWrites bool
}

func (s *reauthStore) GetKey(key []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[string(key)]
	if !ok {
		return nil, http.ErrNoCookie
	}
	return v, nil
}

func (s *reauthStore) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWrites {
		return errors.New("raft unavailable")
	}
	s.writes++
	s.data[string(cmd.Key)] = cmd.Value
	return nil
}

func (s *reauthStore) profile(t *testing.T) *bongov1.UserProfile {
	t.Helper()
	raw, err := s.GetKey([]byte("/user/" + reauthUserID + "/profile"))
	if err != nil {
		t.Fatal(err)
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return &p
}

func (s *reauthStore) put(t *testing.T, p *bongov1.UserProfile) {
	t.Helper()
	raw, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.data["/user/"+reauthUserID+"/profile"] = raw
	s.mu.Unlock()
}

type reauthEnv struct {
	store    *reauthStore
	security *SecurityHandler
	authH    *AuthHandler
	limiter  *middleware.LoginRateLimiter
	recovery []string
}

func newReauthEnv(t *testing.T, twoFA bool, max int) *reauthEnv {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("right"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	p := &bongov1.UserProfile{Email: reauthEmail, PasswordHash: hash}
	var plain []string
	if twoFA {
		var hashed []string
		plain, hashed, err = auth.GenerateRecoveryCodes(2)
		if err != nil {
			t.Fatal(err)
		}
		p.TotpEnabled, p.TotpSecret, p.RecoveryCodes = true, reauthTOTPSecret, hashed
	}
	st := &reauthStore{data: map[string][]byte{}}
	st.put(t, p)

	authn := &throttleAuth{users: map[string]string{reauthEmail: reauthUserID}}
	rl := middleware.NewLoginRateLimiter(max, time.Minute)
	t.Cleanup(rl.Stop)

	sh := NewSecurityHandler(st, st, authn, throttleSessionSecret)
	sh.SetRateLimiter(rl)
	ah := NewAuthHandler(authn, throttleSessionSecret)
	ah.SetProfileLoader(st)
	ah.SetRaftApplier(st)
	ah.SetRateLimiter(rl)
	return &reauthEnv{store: st, security: sh, authH: ah, limiter: rl, recovery: plain}
}

// reauthEndpoint drives one re-auth endpoint with a password and optional code.
type reauthEndpoint struct {
	name    string
	handler func(*SecurityHandler) http.HandlerFunc
	form    func(password, code string) url.Values
	// changed reports whether the profile was altered by a successful call.
	changed func(before, after *bongov1.UserProfile) bool
}

var reauthEndpoints = []reauthEndpoint{
	{
		name:    "change-password",
		handler: func(h *SecurityHandler) http.HandlerFunc { return h.ChangePassword },
		form: func(pw, code string) url.Values {
			return url.Values{"current_password": {pw}, "new_password": {"brand-new-pass"}, "confirm_password": {"brand-new-pass"}, "totp_code": {code}}
		},
		changed: func(b, a *bongov1.UserProfile) bool { return !bytes.Equal(b.PasswordHash, a.PasswordHash) },
	},
	{
		name:    "disable-2fa",
		handler: func(h *SecurityHandler) http.HandlerFunc { return h.DisableTOTP },
		form: func(pw, code string) url.Values {
			return url.Values{"password": {pw}, "totp_code": {code}}
		},
		changed: func(b, a *bongov1.UserProfile) bool { return b.TotpEnabled && !a.TotpEnabled },
	},
}

func (e *reauthEnv) call(ep reauthEndpoint, pw, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/settings/security", strings.NewReader(ep.form(pw, code).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = throttleTestIP
	req = req.WithContext(middleware.WithUserContext(req.Context(), reauthUserID, reauthEmail))
	rr := httptest.NewRecorder()
	ep.handler(e.security)(rr, req)
	return rr
}

func totpNow(t *testing.T) string {
	t.Helper()
	c, err := totp.GenerateCode(reauthSecretB32(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// (a) Repeated wrong current passwords are throttled; once limited, even the
// right password is refused and nothing is written.
func TestReauthThrottled(t *testing.T) {
	for _, ep := range reauthEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			const max = 3
			env := newReauthEnv(t, false, max)
			for range max {
				env.call(ep, "wrong", "")
			}
			rr := env.call(ep, "right", "")
			if rr.Code != http.StatusTooManyRequests {
				t.Fatalf("after %d failures got %d, want 429", max, rr.Code)
			}
			if rr.Header().Get("Retry-After") == "" {
				t.Error("429 without Retry-After")
			}
			if env.store.writes != 0 {
				t.Errorf("profile written while throttled (%d writes)", env.store.writes)
			}
		})
	}
}

// Wrong second factors count against the same budget: with the right password
// a session holder could otherwise brute-force the 6-digit code unthrottled.
func TestReauthWrongSecondFactorThrottled(t *testing.T) {
	for _, ep := range reauthEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			const max = 3
			env := newReauthEnv(t, true, max)
			for range max {
				env.call(ep, "right", "000000")
			}
			rr := env.call(ep, "right", totpNow(t))
			if rr.Code != http.StatusTooManyRequests {
				t.Fatalf("after %d wrong codes got %d, want 429", max, rr.Code)
			}
			if env.store.writes != 0 {
				t.Errorf("profile written while throttled (%d writes)", env.store.writes)
			}
		})
	}
}

// Positive control: below the limit the right password goes through.
func TestReauthBelowLimitSucceeds(t *testing.T) {
	for _, ep := range reauthEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			env := newReauthEnv(t, true, 5)
			before := env.store.profile(t)
			env.call(ep, "wrong", "")
			rr := env.call(ep, "right", totpNow(t))
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("got %d", rr.Code)
			}
			if !ep.changed(before, env.store.profile(t)) {
				t.Error("change not applied")
			}
		})
	}
}

// (b) A 2FA user needs a valid second factor as well as the password.
func TestReauthRequiresSecondFactor(t *testing.T) {
	for _, ep := range reauthEndpoints {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			env := newReauthEnv(t, true, 50)
			for _, code := range []string{"", "000000", "AAAA-BBBB"} {
				env.call(ep, "right", code)
			}
			if env.store.writes != 0 {
				t.Fatalf("profile written without a valid second factor (%d writes)", env.store.writes)
			}
			if rr := env.call(ep, "right", totpNow(t)); rr.Code != http.StatusSeeOther {
				t.Fatalf("valid TOTP: got %d", rr.Code)
			}
			if env.store.writes != 1 {
				t.Errorf("valid TOTP: %d writes, want 1", env.store.writes)
			}
		})
	}
}

// Control: a user without 2FA needs only the password.
func TestReauthNo2FAUserNeedsNoCode(t *testing.T) {
	for _, ep := range reauthEndpoints[:1] {
		env := newReauthEnv(t, false, 50)
		before := env.store.profile(t)
		env.call(ep, "right", "")
		if !ep.changed(before, env.store.profile(t)) {
			t.Error("non-2FA change refused")
		}
	}
}

func TestReauthRecoveryCodeConsumed(t *testing.T) {
	env := newReauthEnv(t, true, 50)
	ep := reauthEndpoints[0]
	before := env.store.profile(t)
	if rr := env.call(ep, "right", env.recovery[0]); rr.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rr.Code)
	}
	after := env.store.profile(t)
	if !ep.changed(before, after) {
		t.Fatal("password not changed with a recovery code")
	}
	if len(after.RecoveryCodes) != 1 {
		t.Fatalf("recovery codes left = %d, want 1", len(after.RecoveryCodes))
	}
	// Reused recovery code is refused (disable endpoint, password still right).
	env.store.writes = 0
	env.call(reauthEndpoints[1], "right", env.recovery[0])
	if env.store.writes != 0 {
		t.Error("consumed recovery code accepted again")
	}
}

// (c) A code consumed by one path is refused by every other path.
func TestTOTPReplay(t *testing.T) {
	login := func(env *reauthEnv, code string) *httptest.ResponseRecorder {
		pending := middleware.CreatePendingCookie(throttleSessionSecret, reauthUserID, reauthEmail, false, false, false, "")
		mux := http.NewServeMux()
		mux.HandleFunc("POST /login/2fa", env.authH.TwoFactorVerify)
		return postForm(mux, "/login/2fa", url.Values{"code": {code}}, pending)
	}
	hasSession := func(rr *httptest.ResponseRecorder) bool {
		for _, c := range rr.Result().Cookies() {
			if c.Name == "bongo_session" && c.Value != "" && c.MaxAge >= 0 {
				return true
			}
		}
		return false
	}

	t.Run("login then login", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		code := totpNow(t)
		if rr := login(env, code); !hasSession(rr) {
			t.Fatalf("first login failed: %d %q", rr.Code, rr.Header().Get("Location"))
		}
		rr := login(env, code)
		if hasSession(rr) || rr.Header().Get("Location") == "/mail/INBOX" {
			t.Fatal("replayed TOTP code logged in a second time")
		}
	})

	t.Run("login then change-password", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		code := totpNow(t)
		if rr := login(env, code); !hasSession(rr) {
			t.Fatal("login failed")
		}
		env.store.writes = 0
		env.call(reauthEndpoints[0], "right", code)
		if env.store.writes != 0 {
			t.Fatal("code used at login was accepted for change-password")
		}
	})

	t.Run("change-password then login", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		code := totpNow(t)
		if rr := env.call(reauthEndpoints[0], "right", code); rr.Code != http.StatusSeeOther || env.store.writes != 1 {
			t.Fatalf("change-password failed: %d", rr.Code)
		}
		if hasSession(login(env, code)) {
			t.Fatal("code used for change-password logged in")
		}
	})

	t.Run("write failure refuses login", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		env.store.failWrites = true
		rr := login(env, totpNow(t))
		if hasSession(rr) {
			t.Fatal("session issued although the replay record could not be written")
		}
	})

	t.Run("no applier refuses login", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		env.authH.raft = nil
		if hasSession(login(env, totpNow(t))) {
			t.Fatal("session issued without a way to record the code")
		}
	})

	t.Run("write failure refuses re-auth", func(t *testing.T) {
		env := newReauthEnv(t, true, 50)
		env.store.failWrites = true
		before := env.store.profile(t)
		rr := env.call(reauthEndpoints[1], "right", totpNow(t))
		if rr.Code == http.StatusSeeOther && !env.store.profile(t).TotpEnabled {
			t.Fatal("2FA disabled although write failed")
		}
		if !proto.Equal(before, env.store.profile(t)) {
			t.Fatal("profile changed")
		}
	})
}

// (d) Logout revokes the session on the server; the cookie copy on another
// device dies with it (documented: logout signs out every web session).
func TestLogoutRevokesSession(t *testing.T) {
	env := newReauthEnv(t, false, 50)

	stateFn := func(userID string) middleware.LiveSessionState {
		raw, err := env.store.GetKey([]byte("/user/" + userID + "/profile"))
		if err != nil {
			return middleware.LiveSessionState{}
		}
		var p bongov1.UserProfile
		if proto.Unmarshal(raw, &p) != nil {
			return middleware.LiveSessionState{}
		}
		return middleware.LiveSessionState{Found: true, Epoch: security.SessionEpoch(p.PasswordHash, p.TotpSecret, p.TotpEnabled, p.SessionGeneration)}
	}
	protected := middleware.RequireAuth(throttleSessionSecret, stateFn)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", env.authH.Login)
	mux.HandleFunc("POST /logout", env.authH.Logout)
	mux.Handle("GET /mail/INBOX", protected)

	sessionOf := func(rr *httptest.ResponseRecorder) *http.Cookie {
		for _, c := range rr.Result().Cookies() {
			if c.Name == "bongo_session" && c.Value != "" {
				return c
			}
		}
		t.Fatalf("no session cookie in login response (%d %q)", rr.Code, rr.Header().Get("Location"))
		return nil
	}
	get := func(c *http.Cookie) int {
		req := httptest.NewRequest(http.MethodGet, "/mail/INBOX", nil)
		req.AddCookie(c)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code
	}

	a := sessionOf(postForm(mux, "/login", loginForm(reauthEmail, "right")))
	b := &http.Cookie{Name: a.Name, Value: a.Value}
	if get(a) != http.StatusOK || get(b) != http.StatusOK {
		t.Fatal("fresh sessions should be live")
	}

	// A stale, forged or absent cookie must not be able to force a global logout.
	postForm(mux, "/logout", url.Values{}, &http.Cookie{Name: "bongo_session", Value: "garbage.value"})
	postForm(mux, "/logout", url.Values{})
	if env.store.profile(t).SessionGeneration != 0 {
		t.Fatal("logout without a valid session bumped the generation")
	}
	if get(b) != http.StatusOK {
		t.Fatal("session died without a valid logout")
	}

	if rr := postForm(mux, "/logout", url.Values{}, a); rr.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rr.Code)
	}
	if got := get(a); got != http.StatusSeeOther {
		t.Errorf("logged-out cookie still live: %d", got)
	}
	if got := get(b); got != http.StatusSeeOther {
		t.Errorf("copied cookie still live after logout: %d", got)
	}

	// A stale cookie replayed against /logout must not bump the generation again.
	gen := env.store.profile(t).SessionGeneration
	postForm(mux, "/logout", url.Values{}, b)
	if env.store.profile(t).SessionGeneration != gen {
		t.Error("stale cookie bumped the generation")
	}

	c := sessionOf(postForm(mux, "/login", loginForm(reauthEmail, "right")))
	if get(c) != http.StatusOK {
		t.Error("fresh login after logout should work")
	}
}

// Logout still clears the cookie when the revocation write fails.
func TestLogoutWriteFailureStillClearsCookie(t *testing.T) {
	env := newReauthEnv(t, false, 50)
	env.store.failWrites = true
	sess := middleware.CreateSessionCookie(throttleSessionSecret, reauthUserID, reauthEmail, false, false, false,
		security.SessionEpoch(env.store.profile(t).PasswordHash, nil, false, 0))
	rr := postForm(http.HandlerFunc(env.authH.Logout), "/logout", url.Values{}, sess)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("got %d %q", rr.Code, rr.Header().Get("Location"))
	}
	cleared := false
	for _, c := range rr.Result().Cookies() {
		if c.Name == "bongo_session" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("session cookie not cleared")
	}
}

// pendingSetupCookie runs SetupTOTP's cookie writer for a chosen secret and
// returns the cookie ConfirmTOTP reads back.
func pendingSetupCookie(t *testing.T, env *reauthEnv, secret string) *http.Cookie {
	t.Helper()
	rr := httptest.NewRecorder()
	env.security.setTOTPPendingCookie(rr, secret)
	for _, c := range rr.Result().Cookies() {
		if c.Name == totpPendingCookieName {
			return c
		}
	}
	t.Fatal("no pending setup cookie")
	return nil
}

const attackerSecretB32 = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"

func confirmTOTP(env *reauthEnv, code string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/settings/security/totp/confirm", strings.NewReader(url.Values{"code": {code}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = throttleTestIP
	req.AddCookie(cookie)
	req = req.WithContext(middleware.WithUserContext(req.Context(), reauthUserID, reauthEmail))
	rr := httptest.NewRecorder()
	env.security.ConfirmTOTP(rr, req)
	return rr
}

// A session alone must not replace an enrolled second factor: no password and
// no current code, so the secret and recovery codes must stay as they were.
func TestConfirmTOTPRefusedWhenEnrolled(t *testing.T) {
	env := newReauthEnv(t, true, 50)
	before := env.store.profile(t)
	code, err := totp.GenerateCode(attackerSecretB32, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rr := confirmTOTP(env, code, pendingSetupCookie(t, env, attackerSecretB32))
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("got %d, want redirect", rr.Code)
	}
	if env.store.writes != 0 || !proto.Equal(before, env.store.profile(t)) {
		t.Fatal("enrolled second factor replaced without re-authentication")
	}

	// SetupTOTP refuses to hand out a fresh secret either.
	req := httptest.NewRequest(http.MethodPost, "/settings/security/totp/setup", nil)
	req = req.WithContext(middleware.WithUserContext(req.Context(), reauthUserID, reauthEmail))
	srr := httptest.NewRecorder()
	env.security.SetupTOTP(srr, req)
	if srr.Code != http.StatusSeeOther {
		t.Errorf("setup for enrolled user: got %d, want redirect", srr.Code)
	}
	for _, c := range srr.Result().Cookies() {
		if c.Name == totpPendingCookieName {
			t.Error("pending secret issued to an enrolled user")
		}
	}
}

// Positive control for the guard above, and the setup-step replay check: the
// code that confirms setup is spent, so it cannot then log in or re-authenticate.
func TestConfirmTOTPRecordsSetupStep(t *testing.T) {
	env := newReauthEnv(t, false, 50)
	code, err := totp.GenerateCode(attackerSecretB32, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rr := confirmTOTP(env, code, pendingSetupCookie(t, env, attackerSecretB32)); rr.Code != http.StatusOK {
		t.Fatalf("setup confirm: got %d", rr.Code)
	}
	p := env.store.profile(t)
	if !p.TotpEnabled || p.TotpLastStep == 0 {
		t.Fatalf("setup not recorded: enabled=%v step=%d", p.TotpEnabled, p.TotpLastStep)
	}

	pending := middleware.CreatePendingCookie(throttleSessionSecret, reauthUserID, reauthEmail, false, false, false, "")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/2fa", env.authH.TwoFactorVerify)
	rr := postForm(mux, "/login/2fa", url.Values{"code": {code}}, pending)
	for _, c := range rr.Result().Cookies() {
		if c.Name == "bongo_session" && c.Value != "" && c.MaxAge >= 0 {
			t.Fatal("setup code logged in")
		}
	}

	env.store.writes = 0
	env.call(reauthEndpoints[0], "right", code)
	if env.store.writes != 0 {
		t.Fatal("setup code accepted for change-password")
	}
}

// Two simultaneous submissions of one code: exactly one may win.
func TestTOTPDoubleSubmitOneWins(t *testing.T) {
	for range 20 {
		env := newReauthEnv(t, true, 1000)
		code := totpNow(t)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rr := env.call(reauthEndpoints[0], "right", code)
				if rr.Code == http.StatusSeeOther && strings.Contains(rr.Header().Get("Location"), "changed") {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("%d submissions of one code succeeded, want 1", wins)
		}
	}
}

// Concurrent /login/2fa submissions of one code, each with its own pending
// cookie: exactly one may be issued a session.
func TestTOTPLoginDoubleSubmitOneWins(t *testing.T) {
	for range 20 {
		env := newReauthEnv(t, true, 1000)
		code := totpNow(t)
		mux := http.NewServeMux()
		mux.HandleFunc("POST /login/2fa", env.authH.TwoFactorVerify)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for range 8 {
			pending := middleware.CreatePendingCookie(throttleSessionSecret, reauthUserID, reauthEmail, false, false, false, "")
			wg.Add(1)
			go func() {
				defer wg.Done()
				rr := postForm(mux, "/login/2fa", url.Values{"code": {code}}, pending)
				for _, c := range rr.Result().Cookies() {
					if c.Name == "bongo_session" && c.Value != "" && c.MaxAge >= 0 {
						mu.Lock()
						wins++
						mu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("%d logins with one code succeeded, want 1", wins)
		}
	}
}

// An expired but validly signed, current-epoch cookie must not force a logout;
// the same cookie unexpired is the positive control.
func TestLogoutIgnoresExpiredCookie(t *testing.T) {
	env := newReauthEnv(t, false, 50)
	epoch := security.SessionEpoch(env.store.profile(t).PasswordHash, nil, false, 0)
	cookieExpiring := func(d time.Duration) *http.Cookie {
		payload, _ := json.Marshal(middleware.SessionData{UserID: reauthUserID, Email: reauthEmail, Exp: time.Now().Add(d).Unix(), Epoch: epoch})
		return &http.Cookie{Name: "bongo_session", Value: middleware.SignValue(throttleSessionSecret, "bongo/session-cookie/v1", payload)}
	}
	logout := http.HandlerFunc(env.authH.Logout)

	postForm(logout, "/logout", url.Values{}, cookieExpiring(-time.Hour))
	if env.store.profile(t).SessionGeneration != 0 {
		t.Fatal("expired cookie bumped the session generation")
	}
	postForm(logout, "/logout", url.Values{}, cookieExpiring(time.Hour))
	if env.store.profile(t).SessionGeneration != 1 {
		t.Fatal("control: a live cookie should have bumped the generation")
	}
}
