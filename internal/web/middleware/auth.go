package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
)

type contextKey string

const (
	userIDKey contextKey = "userID"
	emailKey  contextKey = "email"
	adminKey  contextKey = "isAdmin"

	cookieName = "bongo_session"
)

// SessionData represents the signed session cookie payload.
type SessionData struct {
	UserID  string `json:"uid"`
	Email   string `json:"email"`
	Exp     int64  `json:"exp"`
	IsAdmin bool   `json:"adm,omitempty"`
}

// CreateSessionCookie creates a signed session cookie.
func CreateSessionCookie(secret []byte, userID, email string, remember, isAdmin bool) *http.Cookie {
	expiry := 24 * time.Hour
	if remember {
		expiry = 30 * 24 * time.Hour
	}

	data := SessionData{
		UserID:  userID,
		Email:   email,
		Exp:     time.Now().Add(expiry).Unix(),
		IsAdmin: isAdmin,
	}

	payload, _ := json.Marshal(data)
	sig := sign(secret, payload)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig)

	return &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(expiry.Seconds()),
	}
}

// ClearSessionCookie returns a cookie that clears the session.
func ClearSessionCookie() *http.Cookie {
	return &http.Cookie{
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
func RequireAuth(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(cookieName)
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			data, err := validateCookie(secret, cookie.Value)
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			if time.Now().Unix() > data.Exp {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			ctx := context.WithValue(r.Context(), userIDKey, data.UserID)
			ctx = context.WithValue(ctx, emailKey, data.Email)
			ctx = context.WithValue(ctx, adminKey, data.IsAdmin)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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

func sign(secret, payload []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return mac.Sum(nil)
}
