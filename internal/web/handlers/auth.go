package handlers

import (
	"encoding/base32"
	"fmt"
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

// LoginLimiter is the interface the auth handler uses to record and reset
// per-IP failure counts.
type LoginLimiter interface {
	RecordFailure(ip string)
	Reset(ip string)
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
			h.limiter.RecordFailure(ip)
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
					h.limiter.RecordFailure(ip)
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
		h.limiter.Reset(ip)
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
	return security.SessionEpoch(profile.PasswordHash, profile.TotpSecret, profile.TotpEnabled)
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

	// Load profile to get TOTP secret and recovery codes.
	profile, err := h.loadProfile(pending.UserID)
	if err != nil {
		if h.limiter != nil {
			h.limiter.RecordFailure(ip)
		}
		http.Redirect(w, r, "/login/2fa?error="+urlEncode("Verification failed. Please try again."), http.StatusSeeOther)
		return
	}

	// Convert TOTP secret bytes to base32 string for validation.
	totpSecret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(profile.TotpSecret)

	var recoveryWarning string

	if auth.ValidateTOTP(totpSecret, code) {
		// TOTP code valid — complete login.
		h.completeLogin(w, r, pending)
		return
	}

	// Try recovery code.
	matched, idx := auth.ValidateRecoveryCode(profile.RecoveryCodes, code)
	if matched {
		// Remove the used recovery code from stored codes via Raft.
		newCodes := make([]string, 0, len(profile.RecoveryCodes)-1)
		for i, c := range profile.RecoveryCodes {
			if i != idx {
				newCodes = append(newCodes, c)
			}
		}
		profile.RecoveryCodes = newCodes

		if h.raft == nil {
			if h.limiter != nil {
				h.limiter.RecordFailure(ip)
			}
			http.Redirect(w, r, "/login/2fa?error="+urlEncode("Verification failed. Please try again."), http.StatusSeeOther)
			return
		}
		key := []byte(fmt.Sprintf("/user/%s/profile", pending.UserID))
		profileBytes, marshalErr := proto.Marshal(profile)
		if marshalErr != nil {
			if h.limiter != nil {
				h.limiter.RecordFailure(ip)
			}
			http.Redirect(w, r, "/login/2fa?error="+urlEncode("Verification failed. Please try again."), http.StatusSeeOther)
			return
		}
		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   key,
			Value: profileBytes,
		}
		if err := h.raft.ApplyCommand(cmd, 5*time.Second); err != nil {
			if h.limiter != nil {
				h.limiter.RecordFailure(ip)
			}
			http.Redirect(w, r, "/login/2fa?error="+urlEncode("Verification failed. Please try again."), http.StatusSeeOther)
			return
		}

		remaining := len(newCodes)
		recoveryWarning = "Recovery code used. You have " + strconv.Itoa(remaining) + " code(s) remaining."

		// Complete login with recovery warning passed as query param.
		http.SetCookie(w, middleware.ClearPendingCookie())
		if h.limiter != nil {
			h.limiter.Reset(ip)
		}
		cookie := middleware.CreateSessionCookie(h.sessionSecret, pending.UserID, pending.Email, pending.Remember, pending.IsAdmin, h.tlsEnabled, h.sessionEpoch(pending.UserID))
		http.SetCookie(w, cookie)
		http.Redirect(w, r, "/mail/INBOX?warning="+urlEncode(recoveryWarning), http.StatusSeeOther)
		return
	}

	// Both TOTP and recovery code failed.
	if h.limiter != nil {
		h.limiter.RecordFailure(ip)
	}
	http.Redirect(w, r, "/login/2fa?error="+urlEncode("Invalid code. Please try again."), http.StatusSeeOther)
}

// completeLogin clears the pending cookie, sets the session cookie, and redirects to inbox.
func (h *AuthHandler) completeLogin(w http.ResponseWriter, r *http.Request, pending *middleware.PendingData) {
	ip := middleware.ClientIP(r)
	http.SetCookie(w, middleware.ClearPendingCookie())
	if h.limiter != nil {
		h.limiter.Reset(ip)
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

// Logout clears the session and redirects to login.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, middleware.ClearSessionCookie())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
