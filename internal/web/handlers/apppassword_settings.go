package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

const securityPath = "/settings/security"

// SetAppPasswords wires the app password service. It is the concrete type, not
// an interface, so a nil pointer is what the nil checks below see.
func (h *SecurityHandler) SetAppPasswords(p *auth.AppPasswords) { h.appPasswords = p }

// appPasswordView builds the App passwords card. plaintext is non-empty only
// for the response to a create; errText is a translated banner.
func (h *SecurityHandler) appPasswordView(r *http.Request, userID, plaintext, errText string) templates.AppPasswordView {
	view := templates.AppPasswordView{Available: h.appPasswords != nil, Plaintext: plaintext, Error: errText}
	if h.appPasswords == nil {
		return view
	}
	infos, err := h.appPasswords.List(userID)
	if err != nil {
		// Show an error state rather than an empty list that reads as "none".
		slog.Error("app passwords: list failed", "user_id", userID, "error", err)
		view.ListFailed = true
		return view
	}
	locale := middleware.LocaleFromContext(r.Context())
	for _, info := range infos {
		row := templates.AppPasswordRow{
			ID:     info.ID,
			Masked: auth.MaskAppPassword(info.ID),
			Label:  info.Label,
			// Dates stay in UTC: last_used_at is the start of a UTC day, so a
			// zone west of UTC would show the day before.
			Created: i18n.FormatDate(locale, info.CreatedAt, true),
		}
		if !info.LastUsedAt.IsZero() {
			row.LastUsed = i18n.FormatDate(locale, info.LastUsedAt, true)
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

func (h *SecurityHandler) renderSecurity(w http.ResponseWriter, r *http.Request, status int, plaintext, errText string) {
	userID, email := middleware.UserFromContext(r.Context())
	totpEnabled := false
	if h.profiles != nil {
		if profile, err := h.loadProfile(userID); err == nil {
			totpEnabled = profile.TotpEnabled
		}
	}
	// The response carries a secret when plaintext is set. It must not be
	// cached or replayed from history.
	if plaintext != "" {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
	}
	view := h.appPasswordView(r, userID, plaintext, errText)
	templ.Handler(templates.SecuritySettings(totpEnabled, email, "", view), templ.WithStatus(status)).ServeHTTP(w, r)
}

// CreateAppPassword makes a credential and renders its plaintext in this one
// response. There is no redirect: a URL or flash would put the secret in logs,
// history or a banner. A browser refresh of this POST creates another
// credential, which is accepted because post-redirect-get cannot carry it.
func (h *SecurityHandler) CreateAppPassword(w http.ResponseWriter, r *http.Request) {
	if h.appPasswords == nil {
		http.NotFound(w, r)
		return
	}
	// Require2FA exempts everything under /settings/security so a user can
	// enrol. That exemption must not let a session held only by the password
	// mint a credential that outlives the enrolment.
	if middleware.Require2FAFromContext(r.Context()) {
		http.Redirect(w, r, securityPath, http.StatusSeeOther)
		return
	}
	userID, _ := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())

	plaintext, _, err := h.appPasswords.Create(userID, r.FormValue("label"))
	switch {
	case errors.Is(err, auth.ErrInvalidAppPasswordLabel):
		h.renderSecurity(w, r, http.StatusBadRequest, "", i18n.T(locale, "security.appPasswordLabelInvalid"))
		return
	case err != nil:
		slog.Error("app passwords: create failed", "user_id", userID, "error", err)
		h.renderSecurity(w, r, http.StatusInternalServerError, "", i18n.T(locale, "security.appPasswordCreateFailed"))
		return
	}
	// A List failure after a successful Create still shows the plaintext: the
	// credential exists and cannot be shown again.
	h.renderSecurity(w, r, http.StatusOK, plaintext, "")
}

// RevokeAppPassword deletes one of the caller's own credentials. An unknown,
// already-revoked or malformed id gets the same response as a real revoke, so
// the endpoint is not an existence oracle. The id is a form field, not a path
// segment, so an empty or slash-containing id reaches this handler instead of
// a router 404 that would differ from the success response.
func (h *SecurityHandler) RevokeAppPassword(w http.ResponseWriter, r *http.Request) {
	if h.appPasswords == nil {
		http.NotFound(w, r)
		return
	}
	userID, _ := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())

	err := h.appPasswords.Revoke(userID, r.FormValue("id"))
	if err != nil && !errors.Is(err, store.ErrInvalidAppPasswordID) {
		slog.Error("app passwords: revoke failed", "user_id", userID, "error", err)
		http.Error(w, i18n.T(locale, "security.appPasswordRevokeFailed"), http.StatusInternalServerError)
		return
	}
	target := withFlash(securityPath, flashAppPasswordRevoked)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
