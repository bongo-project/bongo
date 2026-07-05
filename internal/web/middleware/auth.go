package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bongo-go/bongo-go/internal/security"
)

type contextKey string

const (
	userIDKey contextKey = "userID"
	emailKey  contextKey = "email"
	adminKey  contextKey = "isAdmin"

	cookieName        = "bongo_session"
	pendingCookieName = "bongo_2fa_pending"
	pendingCookieTTL  = 5 * time.Minute

	// sessionCookiePurpose labels the subkey derived from the raw session secret
	// for cookie HMAC. The session and 2FA-pending cookies share the sign/verify
	// helper below, so both use this purpose.
	sessionCookiePurpose = "bongo/session-cookie/v1"
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
}

// CreateSessionCookie creates a signed session cookie.
// Set secure to true when the server is running over HTTPS to enable the Secure flag.
func CreateSessionCookie(secret []byte, userID, email string, remember, isAdmin, secure bool) *http.Cookie {
	expiry := 24 * time.Hour
	if remember {
		expiry = 30 * 24 * time.Hour
	}

	// IsAdmin (and the account's disabled state) are snapshotted into the cookie
	// at login and are only re-evaluated when the session expires. Privilege or
	// disable changes therefore do not take effect until the user's next login or
	// session expiry.
	data := SessionData{
		UserID:  userID,
		Email:   email,
		Exp:     time.Now().Add(expiry).Unix(),
		IsAdmin: isAdmin,
	}

	payload, _ := json.Marshal(data)
	sig := sign(secret, payload)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)

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
// isDisabled, when non-nil, is consulted on every request so that disabling a
// user immediately revokes their existing sessions — the cookie is
// self-contained, so this is the only place live account state is re-checked.
func RequireAuth(secret []byte, isDisabled func(userID string) bool) func(http.Handler) http.Handler {
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

			if time.Now().Unix() > data.Exp {
				redirectToLogin(w, r)
				return
			}

			// Revoke the session immediately if the account has been disabled.
			if isDisabled != nil && isDisabled(data.UserID) {
				http.SetCookie(w, ClearSessionCookie())
				redirectToLogin(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), userIDKey, data.UserID)
			ctx = context.WithValue(ctx, emailKey, data.Email)
			ctx = context.WithValue(ctx, adminKey, data.IsAdmin)
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

func validateCookie(secret []byte, value string) (*SessionData, error) {
	// Split "payload.signature"
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

	if !hmac.Equal(sig, sign(secret, payload)) {
		return nil, http.ErrNoCookie
	}

	var data SessionData
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, err
	}

	return &data, nil
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
	sig := sign(secret, payload)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)

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

	value := cookie.Value
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

	if !hmac.Equal(sig, sign(secret, payload)) {
		return nil, http.ErrNoCookie
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

func sign(secret, payload []byte) []byte {
	mac := hmac.New(sha256.New, security.DeriveKey(secret, sessionCookiePurpose))
	mac.Write(payload)
	return mac.Sum(nil)
}
