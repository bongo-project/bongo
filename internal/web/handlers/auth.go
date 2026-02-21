package handlers

import (
	"fmt"
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

// AuthHandler handles login and logout.
type AuthHandler struct {
	auth          auth.Authenticator
	sessionSecret []byte
	profiles      ProfileLoader
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

	userID, err := h.auth.Authenticate(email, password)
	if err != nil {
		templ.Handler(templates.LoginPage("Invalid email or password.")).ServeHTTP(w, r)
		return
	}

	// Load profile to check admin and disabled status.
	var isAdmin bool
	if h.profiles != nil {
		profile, err := h.loadProfile(userID)
		if err == nil {
			if profile.Disabled {
				templ.Handler(templates.LoginPage("Account is disabled.")).ServeHTTP(w, r)
				return
			}
			isAdmin = profile.IsAdmin
		}
	}

	cookie := middleware.CreateSessionCookie(h.sessionSecret, userID, email, remember, isAdmin)
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
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
