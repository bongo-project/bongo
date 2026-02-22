package handlers

import (
	"fmt"
	"net"
	"net/http"

	"github.com/a-h/templ"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
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

// AuthHandler handles login and logout.
type AuthHandler struct {
	auth          auth.Authenticator
	sessionSecret []byte
	profiles      ProfileLoader
	limiter       LoginLimiter
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

	ip := clientIP(r)

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
	if h.profiles != nil {
		profile, err := h.loadProfile(userID)
		if err == nil {
			if profile.Disabled {
				if h.limiter != nil {
					h.limiter.RecordFailure(ip)
				}
				templ.Handler(templates.LoginPage("Account is disabled.")).ServeHTTP(w, r)
				return
			}
			isAdmin = profile.IsAdmin
		}
	}

	if h.limiter != nil {
		h.limiter.Reset(ip)
	}

	cookie := middleware.CreateSessionCookie(h.sessionSecret, userID, email, remember, isAdmin, h.tlsEnabled)
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
}

// clientIP extracts the client IP from the request, preferring X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				xff = xff[:i]
				break
			}
		}
		// Trim spaces.
		for len(xff) > 0 && xff[0] == ' ' {
			xff = xff[1:]
		}
		for len(xff) > 0 && xff[len(xff)-1] == ' ' {
			xff = xff[:len(xff)-1]
		}
		if xff != "" {
			return xff
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
