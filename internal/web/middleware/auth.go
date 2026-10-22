package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bongo-project/bongo/internal/security"
)

type contextKey string

const (
	userIDKey contextKey = "userID"
	emailKey  contextKey = "email"
	adminKey  contextKey = "isAdmin"

	cookieName        = "bongo_session"
	pendingCookieName = "bongo_2fa_pending"
	pendingCookieTTL  = 5 * time.Minute

	// Each signed cookie type derives its own subkey from the raw session
	// secret. Their JSON payloads overlap (uid, email, exp...), so sharing a
	// key would make one type decode as another. The session purpose must not
	// change: doing so would log every user out.
	sessionCookiePurpose = "bongo/session-cookie/v1"
	pendingCookiePurpose = "bongo/2fa-pending-cookie/v1"

	// TOTPSetupCookiePurpose is the subkey purpose for the TOTP-setup cookie.
	TOTPSetupCookiePurpose = "bongo/totp-setup-cookie/v1"
)

// PendingData represents the signed 2FA-pending cookie payload.
type PendingData struct {
	UserID   string `json:"uid"`
	Email    string `json:"email"`
	Remember bool   `json:"rem,omitempty"`
	IsAdmin  bool   `json:"adm,omitempty"`
	Exp      int64  `json:"exp"`
}

// SessionData represents the signed session cookie payload.
type SessionData struct {
	UserID  string `json:"uid"`
	Email   string `json:"email"`
	Exp     int64  `json:"exp"`
	IsAdmin bool   `json:"adm,omitempty"`
	// Epoch is a fingerprint of the user's credential state at login (see
	// security.SessionEpoch). RequireAuth revokes the session if it no longer
	// matches the account's current state, so a password change or 2FA toggle
	// invalidates outstanding sessions. Empty for cookies issued before this
	// field existed (those are simply not epoch-checked).
	Epoch string `json:"ep,omitempty"`
}

// LiveSessionState is the account state re-checked on every request. Because the
// session cookie is self-contained, this is where disable/admin/credential
// changes are re-evaluated so they take effect immediately rather than at the
// next login or expiry.
type LiveSessionState struct {
	// Found is false when the profile could not be read; with Missing also
	// false that means a store error and callers fail open, leaving the
	// cookie's snapshot in force.
	Found bool
	// Missing means the account no longer exists (profile not found): revoke.
	// A separate field so the zero value still means fail-open.
	Missing  bool
	Disabled bool
	IsAdmin  bool
	Epoch    string
}

// CreateSessionCookie creates a signed session cookie.
// Set secure to true when the server is running over HTTPS to enable the Secure flag.
// epoch is the credential fingerprint (security.SessionEpoch) used for live
// revocation; pass "" to opt out.
func CreateSessionCookie(secret []byte, userID, email string, remember, isAdmin, secure bool, epoch string) *http.Cookie {
	expiry := 24 * time.Hour
	if remember {
		expiry = 30 * 24 * time.Hour
	}

	data := SessionData{
		UserID:  userID,
		Email:   email,
		Exp:     time.Now().Add(expiry).Unix(),
		IsAdmin: isAdmin,
		Epoch:   epoch,
	}

	payload, _ := json.Marshal(data)
	value := SignValue(secret, sessionCookiePurpose, payload)

	return &http.Cookie{ // #nosec G124 -- Secure is set from runtime TLS/proxy config so dev HTTP can still log in
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(expiry.Seconds()),
	}
}

// ClearSessionCookie returns a cookie that clears the session.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{ // #nosec G124 -- clearing must also work for non-Secure dev cookies
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

// RequireAuth is middleware that validates the session cookie and injects
// userID and email into the request context. Redirects to /login on failure.
// For HTMX requests it uses HX-Redirect so the browser does a full-page
// navigation instead of swapping the login page into a partial target.
// stateFn, when non-nil, is consulted on every request so that disabling a
// user, changing their password, toggling 2FA, or changing their admin status
// takes effect immediately despite the self-contained cookie. It is the only
// place live account state is re-checked. Missing=true (account deleted)
// revokes the session; Found=false && !Missing (store error) fails open,
// leaving the cookie's snapshot in force.
func RequireAuth(secret []byte, stateFn func(userID string) LiveSessionState) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(cookieName)
			if err != nil {
				redirectToLogin(w, r)
				return
			}

			data, err := validateCookie(secret, cookie.Value)
			if err != nil {
				redirectToLogin(w, r)
				return
			}

			if data.UserID == "" || time.Now().Unix() > data.Exp {
				redirectToLogin(w, r)
				return
			}

			// Re-evaluate live account state. isAdmin defaults to the cookie
			// snapshot but is overridden by the live value so a demotion (or
			// promotion) takes effect at once.
			isAdmin := data.IsAdmin
			if stateFn != nil {
				st := stateFn(data.UserID)
				if st.Missing {
					http.SetCookie(w, ClearSessionCookie())
					redirectToLogin(w, r)
					return
				}
				if st.Found {
					// Disabled account, or credentials changed since this
					// session was issued -> revoke immediately.
					if st.Disabled || (data.Epoch != "" && st.Epoch != "" && data.Epoch != st.Epoch) {
						http.SetCookie(w, ClearSessionCookie())
						redirectToLogin(w, r)
						return
					}
					isAdmin = st.IsAdmin
				}
			}

			ctx := context.WithValue(r.Context(), userIDKey, data.UserID)
			ctx = context.WithValue(ctx, emailKey, data.Email)
			ctx = context.WithValue(ctx, adminKey, isAdmin)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// redirectToLogin sends the client to /login. For HTMX requests it uses the
// HX-Redirect header so the browser performs a full-page navigation rather
// than swapping the login form into a partial target element.
func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// RequireAdmin is middleware that checks the admin flag in the session.
// Must be used after RequireAuth. Returns 403 for non-admin users.
func RequireAdmin(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !AdminFromContext(r.Context()) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AdminFromContext returns whether the current user is an admin.
func AdminFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(adminKey).(bool)
	return v
}

// UserFromContext returns the userID and email from the request context.
func UserFromContext(ctx context.Context) (userID, email string) {
	if v, ok := ctx.Value(userIDKey).(string); ok {
		userID = v
	}
	if v, ok := ctx.Value(emailKey).(string); ok {
		email = v
	}
	return
}

// WithUserContext injects userID and email into a context for use in tests.
// Production code should use the RequireAuth middleware instead.
func WithUserContext(ctx context.Context, userID, email string) context.Context {
	ctx = context.WithValue(ctx, userIDKey, userID)
	ctx = context.WithValue(ctx, emailKey, email)
	return ctx
}

// WithAdminContext sets the global-admin flag in a context for use in tests.
// Production code should use the RequireAuth middleware instead.
func WithAdminContext(ctx context.Context, isAdmin bool) context.Context {
	return context.WithValue(ctx, adminKey, isAdmin)
}

func validateCookie(secret []byte, value string) (*SessionData, error) {
	payload, err := VerifyValue(secret, sessionCookiePurpose, value)
	if err != nil {
		return nil, err
	}
	var data SessionData
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// SignValue returns "payload.signature" for payload, signed with the subkey
// derived for purpose.
func SignValue(secret []byte, purpose string, payload []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sign(secret, purpose, payload))
}

// VerifyValue checks a SignValue string against the subkey for purpose and
// returns the payload. It is the single place cookie parsing and the purpose
// check happen.
func VerifyValue(secret []byte, purpose, value string) ([]byte, error) {
	var payloadB64, sigB64 string
	for i := len(value) - 1; i >= 0; i-- {
		if value[i] == '.' {
			payloadB64 = value[:i]
			sigB64 = value[i+1:]
			break
		}
	}
	if payloadB64 == "" || sigB64 == "" {
		return nil, http.ErrNoCookie
	}

	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return nil, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, err
	}

	if !hmac.Equal(sig, sign(secret, purpose, payload)) {
		return nil, http.ErrNoCookie
	}
	return payload, nil
}

// CreatePendingCookie creates a short-lived signed cookie indicating that 2FA is required.
func CreatePendingCookie(secret []byte, userID, email string, remember, isAdmin, secure bool) *http.Cookie {
	data := PendingData{
		UserID:   userID,
		Email:    email,
		Remember: remember,
		IsAdmin:  isAdmin,
		Exp:      time.Now().Add(pendingCookieTTL).Unix(),
	}

	payload, _ := json.Marshal(data)
	value := SignValue(secret, pendingCookiePurpose, payload)

	return &http.Cookie{ // #nosec G124 -- Secure is set from runtime TLS/proxy config so dev HTTP can still complete 2FA
		Name:     pendingCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(pendingCookieTTL.Seconds()),
	}
}

// ValidatePendingCookie validates the 2FA-pending cookie and returns its payload.
// Returns an error if the cookie is missing, tampered with, or expired.
func ValidatePendingCookie(secret []byte, r *http.Request) (*PendingData, error) {
	cookie, err := r.Cookie(pendingCookieName)
	if err != nil {
		return nil, http.ErrNoCookie
	}

	payload, err := VerifyValue(secret, pendingCookiePurpose, cookie.Value)
	if err != nil {
		return nil, err
	}

	var data PendingData
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}

	if time.Now().Unix() > data.Exp {
		return nil, http.ErrNoCookie
	}

	return &data, nil
}

// ClearPendingCookie returns a cookie that clears the 2FA-pending cookie.
func ClearPendingCookie() *http.Cookie {
	return &http.Cookie{ // #nosec G124 -- clearing must also work for non-Secure dev 2FA cookies
		Name:     pendingCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}

func sign(secret []byte, purpose string, payload []byte) []byte {
	mac := hmac.New(sha256.New, security.DeriveKey(secret, purpose))
	mac.Write(payload)
	return mac.Sum(nil)
}
