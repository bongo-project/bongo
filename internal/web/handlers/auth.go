package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ProfileLoader loads user profiles from the store.
type ProfileLoader interface {
	GetKey(key []byte) ([]byte, error)
}

// LoginLimiter is the interface the auth handler uses to record failures and
// successes. A success forgives only the failures typed under the same account
// from the same IP; it never clears the IP's counter outright.
type LoginLimiter interface {
	RecordFailure(ip, account string)
	Succeed(ip, account string)
}

// TOTPRaftApplier applies Raft commands to update stored profiles.
type TOTPRaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// AuthHandler handles login and logout.
type AuthHandler struct {
	auth          auth.Authenticator
	sessionSecret []byte
	profiles      ProfileLoader
	limiter       LoginLimiter
	raft          TOTPRaftApplier
	mailboxes     DefaultMailboxEnsurer
	tlsEnabled    bool
}

// NewAuthHandler creates an AuthHandler.
func NewAuthHandler(authenticator auth.Authenticator, sessionSecret []byte) *AuthHandler {
	return &AuthHandler{
		auth:          authenticator,
		sessionSecret: sessionSecret,
	}
}

// SetProfileLoader sets the profile loader for checking admin status at login.
func (h *AuthHandler) SetProfileLoader(p ProfileLoader) {
	h.profiles = p
}

// SetRateLimiter wires the login rate limiter so the handler can record
// failures and resets directly (complementing the middleware check).
func (h *AuthHandler) SetRateLimiter(l LoginLimiter) {
	h.limiter = l
}

// SetTLSEnabled configures whether the session cookie should have the Secure flag set.
func (h *AuthHandler) SetTLSEnabled(enabled bool) {
	h.tlsEnabled = enabled
}

// SetRaftApplier wires a Raft applier for updating profiles (e.g., consuming recovery codes).
func (h *AuthHandler) SetRaftApplier(r TOTPRaftApplier) {
	h.raft = r
}

// SetMailboxEnsurer wires the store used to self-heal a user's default
// mailboxes on login, so accounts created before default-mailbox provisioning
// (or via paths that skipped it) gain their standard folders (notably Trash).
func (h *AuthHandler) SetMailboxEnsurer(m DefaultMailboxEnsurer) {
	h.mailboxes = m
}

// LoginPage renders the login form.
func (h *AuthHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	// Redirect to inbox if already logged in.
	userID, _ := middleware.UserFromContext(r.Context())
	if userID != "" {
		http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
		return
	}

	templ.Handler(templates.LoginPage("")).ServeHTTP(w, r)
}

// Login processes the login form.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")
	remember := r.FormValue("remember") == "1"

	ip := middleware.ClientIP(r)

	userID, err := h.auth.Authenticate(email, password)
	if err != nil {
		if h.limiter != nil {
			h.limiter.RecordFailure(ip, email)
		}
		templ.Handler(templates.LoginPage("Invalid email or password.")).ServeHTTP(w, r)
		return
	}

	// Load profile to check admin and disabled status.
	var isAdmin bool
	var totpEnabled bool
	if h.profiles != nil {
		profile, err := h.loadProfile(userID)
		if err == nil {
			if profile.Disabled {
				if h.limiter != nil {
					h.limiter.RecordFailure(ip, email)
				}
				// Use the same generic message as a bad password so a valid
				// (but disabled) account is not confirmed to an attacker who
				// already has the password.
				templ.Handler(templates.LoginPage("Invalid email or password.")).ServeHTTP(w, r)
				return
			}
			isAdmin = profile.IsAdmin
			totpEnabled = profile.TotpEnabled
		}
	}

	// If TOTP is enabled, issue a short-lived pending cookie and redirect to 2FA page.
	if totpEnabled {
		cookie := middleware.CreatePendingCookie(h.sessionSecret, userID, email, remember, isAdmin, h.tlsEnabled)
		http.SetCookie(w, cookie)
		http.Redirect(w, r, "/login/2fa", http.StatusSeeOther)
		return
	}

	if h.limiter != nil {
		h.limiter.Succeed(ip, email)
	}

	cookie := middleware.CreateSessionCookie(h.sessionSecret, userID, email, remember, isAdmin, h.tlsEnabled, h.sessionEpoch(userID))
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
}

// sessionEpoch computes the credential fingerprint embedded in a new session
// cookie so RequireAuth can revoke the session after a password or 2FA change.
// Returns "" (no epoch check) if the profile cannot be read.
func (h *AuthHandler) sessionEpoch(userID string) string {
	if h.profiles == nil {
		return ""
	}
	profile, err := h.loadProfile(userID)
	if err != nil {
		return ""
	}
	return security.SessionEpoch(profile.PasswordHash, profile.TotpSecret, profile.TotpEnabled, profile.SessionGeneration)
}

// TwoFactorPage renders the 2FA verification form.
func (h *AuthHandler) TwoFactorPage(w http.ResponseWriter, r *http.Request) {
	_, err := middleware.ValidatePendingCookie(h.sessionSecret, r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	errMsg := r.URL.Query().Get("error")
	recoveryWarning := r.URL.Query().Get("warning")
	templ.Handler(templates.TwoFactorPage(errMsg, recoveryWarning)).ServeHTTP(w, r)
}

// TwoFactorVerify processes the 2FA code submission.
func (h *AuthHandler) TwoFactorVerify(w http.ResponseWriter, r *http.Request) {
	pending, err := middleware.ValidatePendingCookie(h.sessionSecret, r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ip := middleware.ClientIP(r)
	code := r.FormValue("code")

	// Serialise load-check-write per user so two concurrent submissions of the
	// same code cannot both pass before either records it.
	defer lockUser(pending.UserID)()
	profile, err := h.loadProfile(pending.UserID)
	if err != nil {
		h.recordFailure(ip, pending.Email)
		http.Redirect(w, r, "/login/2fa?error="+urlEncode("Verification failed. Please try again."), http.StatusSeeOther)
		return
	}

	ok, usedRecovery := consumeSecondFactor(profile, code, time.Now())
	if !ok {
		h.recordFailure(ip, pending.Email)
		http.Redirect(w, r, "/login/2fa?error="+urlEncode("Invalid code. Please try again."), http.StatusSeeOther)
		return
	}

	// The consumed factor (TOTP step or recovery code) must be recorded before
	// the session is issued. Fail closed: if it cannot be written the code
	// would stay replayable, so the login is refused.
	if err := h.saveProfile(pending.UserID, profile); err != nil {
		h.recordFailure(ip, pending.Email)
		http.Redirect(w, r, "/login/2fa?error="+urlEncode("Verification failed. Please try again."), http.StatusSeeOther)
		return
	}

	if !usedRecovery {
		h.completeLogin(w, r, pending)
		return
	}

	recoveryWarning := "Recovery code used. You have " + strconv.Itoa(len(profile.RecoveryCodes)) + " code(s) remaining."
	http.SetCookie(w, middleware.ClearPendingCookie())
	if h.limiter != nil {
		h.limiter.Succeed(ip, pending.Email)
	}
	cookie := middleware.CreateSessionCookie(h.sessionSecret, pending.UserID, pending.Email, pending.Remember, pending.IsAdmin, h.tlsEnabled, h.sessionEpoch(pending.UserID))
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/mail/INBOX?warning="+urlEncode(recoveryWarning), http.StatusSeeOther)
}

func (h *AuthHandler) recordFailure(ip, email string) {
	if h.limiter != nil {
		h.limiter.RecordFailure(ip, email)
	}
}

// saveProfile writes the profile through Raft. It errors when no applier is
// wired (edge nodes), which callers treat as a refusal.
func (h *AuthHandler) saveProfile(userID string, profile *bongov1.UserProfile) error {
	if h.raft == nil {
		return errors.New("no raft applier")
	}
	data, err := proto.Marshal(profile)
	if err != nil {
		return fmt.Errorf("marshaling profile: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/profile", userID)),
		Value: data,
	}
	if err := h.raft.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying profile: %w", err)
	}
	return nil
}

// completeLogin clears the pending cookie, sets the session cookie, and redirects to inbox.
func (h *AuthHandler) completeLogin(w http.ResponseWriter, r *http.Request, pending *middleware.PendingData) {
	ip := middleware.ClientIP(r)
	http.SetCookie(w, middleware.ClearPendingCookie())
	if h.limiter != nil {
		h.limiter.Succeed(ip, pending.Email)
	}
	cookie := middleware.CreateSessionCookie(h.sessionSecret, pending.UserID, pending.Email, pending.Remember, pending.IsAdmin, h.tlsEnabled, h.sessionEpoch(pending.UserID))
	http.SetCookie(w, cookie)

	// Self-heal default mailboxes (idempotent, cheap) so older accounts missing
	// Trash/Drafts gain them. Best-effort: never block login on this.
	if h.mailboxes != nil {
		_ = h.mailboxes.EnsureDefaultMailboxes(pending.UserID)
	}

	http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
}

// urlEncode encodes a string for use in a URL query parameter.
func urlEncode(s string) string {
	encoded := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			encoded = append(encoded, c)
		default:
			encoded = append(encoded, '%')
			encoded = append(encoded, "0123456789ABCDEF"[c>>4])
			encoded = append(encoded, "0123456789ABCDEF"[c&0xF])
		}
	}
	return string(encoded)
}

func (h *AuthHandler) loadProfile(userID string) (*bongov1.UserProfile, error) {
	key := []byte(fmt.Sprintf("/user/%s/profile", userID))
	data, err := h.profiles.GetKey(key)
	if err != nil {
		return nil, err
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

// Logout clears the session cookie and revokes the session on the server by
// bumping the profile's session generation, which is part of the epoch every
// RequireAuth re-checks. A copied or stolen cookie therefore stops working too.
//
// The generation is per user, not per session: logging out signs the user out
// of every web session on every device. A per-session marker would need a
// session ID in the cookie plus a revocation set with garbage collection.
// Protocol sessions (IMAP, POP3, SMTP) use an epoch that ignores the
// generation and are unaffected.
//
// Only a validly signed, unexpired cookie whose epoch is still current may bump
// the generation, so a stale cookie cannot force a global logout. The route is
// public, so the cookie is verified here. Revocation is best-effort: the
// cookie is cleared and the user redirected whatever happens, and a failed
// write is logged. Cookies issued without an epoch are never epoch-checked and
// cannot be revoked; they expire within the cookie lifetime.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	h.revokeSession(r)
	http.SetCookie(w, middleware.ClearSessionCookie())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *AuthHandler) revokeSession(r *http.Request) {
	if h.profiles == nil || h.raft == nil {
		slog.Warn("logout: session not revoked server-side, no profile store or applier wired")
		return
	}
	data, err := middleware.ParseSessionCookie(h.sessionSecret, r)
	if err != nil || data.Epoch == "" {
		return
	}
	defer lockUser(data.UserID)()
	profile, err := h.loadProfile(data.UserID)
	if err != nil {
		return
	}
	if data.Epoch != security.SessionEpoch(profile.PasswordHash, profile.TotpSecret, profile.TotpEnabled, profile.SessionGeneration) {
		return
	}
	profile.SessionGeneration++
	if err := h.saveProfile(data.UserID, profile); err != nil {
		slog.Warn("logout: session revocation failed", "user_id", data.UserID, "error", err)
	}
}
