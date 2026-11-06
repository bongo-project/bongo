package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/a-h/templ"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

const (
	securityPath = "/settings/security"
	devicesPath  = "/settings/security/devices"
	// devicesSetupPath is the Add a device page as the last step of 2FA
	// enrolment: it offers Finish and Skip.
	devicesSetupPath = devicesPath + "?setup=1"
)

// SetAppPasswords wires the app password service. It is the concrete type, not
// an interface, so a nil pointer is what the nil checks below see.
func (h *SecurityHandler) SetAppPasswords(p *auth.AppPasswords) { h.appPasswords = p }

// SetClientSettings wires the source of the server settings shown beside a new
// app password. It is the autoconfig handler's method, so the page and the
// autoconfig documents render from the same values. With none set the page
// shows the password and the user name only.
func (h *SecurityHandler) SetClientSettings(f func(*http.Request) ClientSettings) {
	h.clientSettings = f
}

// appPasswordView builds the App passwords card. plaintext is non-empty only
// for the response to a create; errText is a translated banner.
func (h *SecurityHandler) appPasswordView(r *http.Request, userID, plaintext, errText string) templates.AppPasswordView {
	view := templates.AppPasswordView{Available: h.appPasswords != nil, Plaintext: plaintext, Error: errText}
	if h.profiles != nil {
		if profile, err := h.loadProfile(userID); err == nil {
			view.TotpEnabled = profile.TotpEnabled
			// A flag without 2FA is not enforced, so it is not shown as on.
			view.RequireAppPasswords = profile.TotpEnabled && profile.RequireAppPasswords
		}
	}
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
	view.Count = len(infos)
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
	// The response carries a secret when plaintext is set. It must not be
	// cached or replayed from history.
	if plaintext != "" {
		noStore(w)
	}
	view := h.appPasswordView(r, userID, plaintext, errText)
	templ.Handler(templates.SecuritySettings(view.TotpEnabled, email, "", view), templ.WithStatus(status)).ServeHTTP(w, r)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

// credentialResult is the outcome of createCredential. Done means the response
// is already written (a refusal or a redirect) and the caller must not write.
type credentialResult struct {
	Plaintext string
	// LookupID is the public lookup group of the new credential. It is set
	// with Plaintext and is not secret.
	LookupID string
	ErrText  string
	Status   int
	Done     bool
}

// createCredential is the one create path: the plain form on the App passwords
// card and Add a device both end here, so the re-authentication, the limit and
// the second-factor bookkeeping cannot differ between them. back is the page a
// failed re-authentication returns to; it must be a fixed same-origin path.
//
// Order matters. The factor is spent only when the profile is written, and
// reauthenticate applies the spend to the loaded profile, so Create runs first:
// a bad label or the limit leaves the code unspent. If the profile write then
// fails the new credential is revoked, because a code that was not recorded
// stays replayable and the user was not told the create worked.
func (h *SecurityHandler) createCredential(w http.ResponseWriter, r *http.Request, back string) credentialResult {
	userID, email := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())

	// Require2FA exempts everything under /settings/security so a user can
	// enrol. That exemption must not let a session held only by the password
	// mint a credential that outlives the enrolment. The hold check comes
	// before the re-authentication, so a held session spends no factor and
	// counts no failure.
	if middleware.Require2FAFromContext(r.Context()) {
		http.Redirect(w, r, securityPath, http.StatusSeeOther)
		return credentialResult{Done: true}
	}

	defer lockUser(userID)()
	profile, ok := h.reauthenticate(w, r, userID, email, r.FormValue("password"), back)
	if !ok {
		return credentialResult{Done: true}
	}

	plaintext, info, err := h.appPasswords.Create(userID, r.FormValue("label"))
	switch {
	case errors.Is(err, auth.ErrInvalidAppPasswordLabel):
		return credentialResult{Status: http.StatusBadRequest, ErrText: i18n.T(locale, "security.appPasswordLabelInvalid")}
	case errors.Is(err, auth.ErrAppPasswordLimit):
		return credentialResult{Status: http.StatusConflict, ErrText: i18n.T(locale, "security.appPasswordLimit")}
	case err != nil:
		slog.Error("app passwords: create failed", "user_id", userID, "error", err)
		return credentialResult{Status: http.StatusInternalServerError, ErrText: i18n.T(locale, "security.appPasswordCreateFailed")}
	}

	// Only a 2FA account has anything to record: reauthenticate changed the
	// profile only by consuming a code.
	if profile.TotpEnabled {
		if err := h.saveProfile(userID, profile); err != nil {
			slog.Error("app passwords: recording the second factor failed, revoking the new credential",
				"user_id", userID, "lookup", info.ID, "error", err)
			if rerr := h.appPasswords.Revoke(userID, info.ID); rerr != nil {
				slog.Error("app passwords: revoking the unrecorded credential failed",
					"user_id", userID, "lookup", info.ID, "error", rerr)
			}
			return credentialResult{Status: http.StatusInternalServerError, ErrText: i18n.T(locale, "security.appPasswordCreateFailed")}
		}
	}
	h.succeed(r, email)
	return credentialResult{Plaintext: plaintext, LookupID: info.ID, Status: http.StatusOK}
}

// CreateAppPassword makes a credential and renders its plaintext in this one
// response. There is no redirect: a URL or flash would put the secret in logs,
// history or a banner. A browser refresh of this POST creates another
// credential, which is accepted because post-redirect-get cannot carry it. The
// account password (and a code, with 2FA) is required: see createCredential.
func (h *SecurityHandler) CreateAppPassword(w http.ResponseWriter, r *http.Request) {
	if h.appPasswords == nil {
		http.NotFound(w, r)
		return
	}
	res := h.createCredential(w, r, securityPath)
	if res.Done {
		return
	}
	// A List failure after a successful Create still shows the plaintext: the
	// credential exists and cannot be shown again.
	h.renderSecurity(w, r, res.Status, res.Plaintext, res.ErrText)
}

// deviceChoices lists what Add a device can set up. Apple needs the server
// settings to build its profile, so it is offered only when they are wired.
func (h *SecurityHandler) deviceChoices(locale string) []templates.DeviceChoice {
	var choices []templates.DeviceChoice
	if h.clientSettings != nil {
		choices = append(choices, templates.DeviceChoice{
			Key: deviceKindApple, Title: i18n.T(locale, "security.deviceApple"), Help: i18n.T(locale, "security.deviceAppleHelp"),
		})
	}
	return append(choices, templates.DeviceChoice{
		Key: deviceKindOther, Title: i18n.T(locale, "security.deviceOther"), Help: i18n.T(locale, "security.deviceOtherHelp"),
	})
}

const (
	deviceKindOther = "other"
	deviceKindApple = "apple"
)

// deviceView builds the Add a device page. A held session never gets here: the
// handlers send it back to the Security page first.
func (h *SecurityHandler) deviceView(r *http.Request, userID string, setup bool) templates.DeviceView {
	locale := middleware.LocaleFromContext(r.Context())
	view := templates.DeviceView{
		Setup:   setup,
		Choices: h.deviceChoices(locale),
	}
	apps := h.appPasswordView(r, userID, "", "")
	view.TotpEnabled = apps.TotpEnabled
	view.Count = apps.Count
	return view
}

// AddDevice renders the Add a device page.
func (h *SecurityHandler) AddDevice(w http.ResponseWriter, r *http.Request) {
	if h.appPasswords == nil {
		http.NotFound(w, r)
		return
	}
	if middleware.Require2FAFromContext(r.Context()) {
		http.Redirect(w, r, securityPath, http.StatusSeeOther)
		return
	}
	userID, email := middleware.UserFromContext(r.Context())
	view := h.deviceView(r, userID, r.URL.Query().Get("setup") == "1")
	view.Flash, view.FlashLevel = flashText(r), flashLevel(r)
	templ.Handler(templates.AddDevice(email, view)).ServeHTTP(w, r)
}

// CreateDevice creates the credential for Add a device. Other shows it once
// with the server settings to enter beside it; Apple returns a profile that
// carries it. Both are the same create path as the plain form
// (createCredential), so they need the account password and obey the limit.
//
// The kind and the settings it needs are checked before the credential
// exists: a refusal after Create would leave a working credential nobody was
// given. A kind this build does not know is refused, not treated as Other, so
// a link to a later choice fails visibly.
//
// The profile is a POST response, so the page does not change: the device
// list and the setup step catch up on the next load of the page, which the
// Apple card's panel links to.
func (h *SecurityHandler) CreateDevice(w http.ResponseWriter, r *http.Request) {
	if h.appPasswords == nil {
		http.NotFound(w, r)
		return
	}
	userID, email := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())
	kind := r.FormValue("kind")
	var settings ClientSettings
	switch kind {
	case "", deviceKindOther:
	case deviceKindApple:
		if h.clientSettings == nil {
			http.Error(w, "unknown device kind", http.StatusBadRequest)
			return
		}
		settings = h.clientSettings(r)
		if len(settings.SMTP) == 0 || domainOf(email) == "" {
			http.Error(w, "device kind not available", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "unknown device kind", http.StatusBadRequest)
		return
	}
	setup := r.FormValue("setup") == "1"
	back := devicesPath
	if setup {
		back = devicesSetupPath
	}

	// reauthenticate answers a throttled client with plain text. For a form
	// that would leave the user on a bare error, so say it on the page. A held
	// session is sent to the Security page first, as createCredential does.
	if middleware.Require2FAFromContext(r.Context()) {
		http.Redirect(w, r, securityPath, http.StatusSeeOther)
		return
	}
	if h.limiter != nil && h.limiter.IsLimited(middleware.ClientIP(r)) {
		w.Header().Set("Retry-After", strconv.Itoa(int(h.limiter.Window().Seconds())))
		view := h.deviceView(r, userID, setup)
		view.Error = i18n.T(locale, "security.deviceThrottled")
		templ.Handler(templates.AddDevice(email, view), templ.WithStatus(http.StatusTooManyRequests)).ServeHTTP(w, r)
		return
	}

	res := h.createCredential(w, r, back)
	if res.Done {
		return
	}
	if kind == deviceKindApple && res.Plaintext != "" {
		// Built in the response and never stored or logged. The label stays
		// out of the file name.
		body := buildAppleProfile(settings, email, &appleCredential{Password: res.Plaintext, LookupID: res.LookupID})
		noStore(w)
		w.Header().Set("Content-Type", "application/x-apple-aspen-config; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", appleProfileFilename(email)))
		_, _ = w.Write(body)
		return
	}
	view := h.deviceView(r, userID, setup)
	view.Error = res.ErrText
	if res.Plaintext != "" {
		noStore(w)
		view.Plaintext = res.Plaintext
		// The credential exists even when listing it back failed, so the
		// setup step must not claim there is none.
		view.Count = max(view.Count, 1)
		view.Groups = h.settingGroups(r, locale, email)
	}
	templ.Handler(templates.AddDevice(email, view), templ.WithStatus(res.Status)).ServeHTTP(w, r)
}

// settingGroups renders the server settings for the page. Every value comes
// from ClientSettings, which the autoconfig documents also use.
func (h *SecurityHandler) settingGroups(r *http.Request, locale, email string) []templates.SettingGroup {
	groups := []templates.SettingGroup{{
		Rows: []templates.SettingRow{{Label: i18n.T(locale, "security.settingUsername"), Value: email}},
	}}
	if h.clientSettings == nil {
		return groups
	}
	cs := h.clientSettings(r)
	mail := func(title string, ep MailEndpoint) templates.SettingGroup {
		return templates.SettingGroup{Title: title, Rows: []templates.SettingRow{
			{Label: i18n.T(locale, "security.settingServer"), Value: cs.Host},
			{Label: i18n.T(locale, "security.settingPort"), Value: strconv.Itoa(ep.Port)},
			{Label: i18n.T(locale, "security.settingEncryption"), Value: ep.Security},
		}}
	}
	dav := func(title string, ep DAVEndpoint) templates.SettingGroup {
		return templates.SettingGroup{Title: title, Rows: []templates.SettingRow{
			{Label: i18n.T(locale, "security.settingServer"), Value: cs.Host},
			{Label: i18n.T(locale, "security.settingPort"), Value: strconv.Itoa(ep.Port)},
			{Label: i18n.T(locale, "security.settingPath"), Value: ep.Path},
		}}
	}
	groups = append(groups, mail(i18n.T(locale, "security.settingsIncoming"), cs.IMAP))
	for _, ep := range cs.SMTP {
		// The port tells the endpoints apart when there are two.
		groups = append(groups, mail(i18n.T(locale, "security.settingsOutgoing")+" - "+strconv.Itoa(ep.Port), ep))
	}
	groups = append(groups,
		dav(i18n.T(locale, "security.settingsCalendar"), cs.CalDAV),
		dav(i18n.T(locale, "security.settingsContacts"), cs.CardDAV))
	return groups
}

// RequireAppPasswordsOn turns the per-account switch on. It is offered only
// with 2FA and at least one app password, so the account is not left with no
// way into its mail clients. It needs the session and CSRF but no second
// factor: it only tightens the account. It cannot be reached with an app
// password, which creates no web session.
func (h *SecurityHandler) RequireAppPasswordsOn(w http.ResponseWriter, r *http.Request) {
	if h.appPasswords == nil {
		http.NotFound(w, r)
		return
	}
	if middleware.Require2FAFromContext(r.Context()) {
		http.Redirect(w, r, securityPath, http.StatusSeeOther)
		return
	}
	userID, _ := middleware.UserFromContext(r.Context())
	back := securityPath
	if r.FormValue("setup") == "1" {
		back = devicesSetupPath
	}

	defer lockUser(userID)()
	profile, err := h.loadProfile(userID)
	if err != nil {
		slog.Error("require app passwords: loading profile failed", "user_id", userID, "error", err)
		http.Redirect(w, r, withFlash(back, flashRequireAppPasswordsFailed), http.StatusSeeOther)
		return
	}
	if !profile.TotpEnabled {
		http.Redirect(w, r, withFlash(securityPath, flashRequireAppPasswordsNo2FA), http.StatusSeeOther)
		return
	}
	infos, err := h.appPasswords.List(userID)
	if err != nil {
		slog.Error("require app passwords: listing failed", "user_id", userID, "error", err)
		http.Redirect(w, r, withFlash(back, flashRequireAppPasswordsFailed), http.StatusSeeOther)
		return
	}
	if len(infos) == 0 {
		http.Redirect(w, r, withFlash(back, flashRequireAppPasswordsNone), http.StatusSeeOther)
		return
	}
	if !profile.RequireAppPasswords {
		profile.RequireAppPasswords = true
		if err := h.saveProfile(userID, profile); err != nil {
			slog.Error("require app passwords: saving profile failed", "user_id", userID, "error", err)
			http.Redirect(w, r, withFlash(back, flashRequireAppPasswordsFailed), http.StatusSeeOther)
			return
		}
		slog.Info("app passwords required for account", "user_id", userID)
	}
	http.Redirect(w, r, withFlash(securityPath, flashRequireAppPasswordsOn), http.StatusSeeOther)
}

// RequireAppPasswordsOff turns the switch off after the same re-authentication
// as a 2FA change: password and code. The code is spent in the one write that
// clears the switch, so the factor and the change land together.
func (h *SecurityHandler) RequireAppPasswordsOff(w http.ResponseWriter, r *http.Request) {
	if h.appPasswords == nil {
		http.NotFound(w, r)
		return
	}
	userID, email := middleware.UserFromContext(r.Context())

	defer lockUser(userID)()
	profile, ok := h.reauthenticate(w, r, userID, email, r.FormValue("password"), securityPath)
	if !ok {
		return
	}
	profile.RequireAppPasswords = false
	if err := h.saveProfile(userID, profile); err != nil {
		slog.Error("require app passwords: saving profile failed", "user_id", userID, "error", err)
		http.Redirect(w, r, withFlash(securityPath, flashRequireAppPasswordsFailed), http.StatusSeeOther)
		return
	}
	h.succeed(r, email)
	slog.Info("app passwords no longer required for account", "user_id", userID)
	http.Redirect(w, r, withFlash(securityPath, flashRequireAppPasswordsOff), http.StatusSeeOther)
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
	redirectAfterAction(w, r, withFlash(securityPath, flashAppPasswordRevoked))
}

// redirectAfterAction sends a browser on to target. htmx needs a 200 with
// HX-Redirect: on a 303 the XHR follows the redirect and the header is never
// seen. target must be a fixed same-origin path.
func redirectAfterAction(w http.ResponseWriter, r *http.Request, target string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther) // #nosec G710 -- callers pass fixed same-origin paths
}
