package handlers

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
)

// AuthHandler handles login and logout.
type AuthHandler struct {
	auth          auth.Authenticator
	sessionSecret []byte
}

// NewAuthHandler creates an AuthHandler.
func NewAuthHandler(authenticator auth.Authenticator, sessionSecret []byte) *AuthHandler {
	return &AuthHandler{
		auth:          authenticator,
		sessionSecret: sessionSecret,
	}
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

	cookie := middleware.CreateSessionCookie(h.sessionSecret, userID, email, remember)
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/mail/INBOX", http.StatusSeeOther)
}

// Logout clears the session and redirects to login.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, middleware.ClearSessionCookie())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
