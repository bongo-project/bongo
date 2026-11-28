package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// featureLabelKeys maps a switch to the locale key of its label.
var featureLabelKeys = map[string]string{
	store.FeatureIMAP:       "admin.feature.imap",
	store.FeaturePOP3:       "admin.feature.pop3",
	store.FeatureSubmission: "admin.feature.submission",
	store.FeatureDAV:        "admin.feature.dav",
	store.FeatureWeb:        "admin.feature.web",
	store.FeatureForwarding: "admin.feature.forwarding",
	store.FeatureRules:      "admin.feature.rules",
	store.FeatureVacation:   "admin.feature.vacation",
}

// featureOptions builds the form rows for a profile: every known switch with
// its current state. Unknown stored names have no row; the save keeps them.
func featureOptions(profile *bongov1.UserProfile) []templates.AdminFeatureOption {
	out := make([]templates.AdminFeatureOption, 0, len(featureLabelKeys))
	for _, f := range store.KnownFeatures() {
		out = append(out, templates.AdminFeatureOption{
			Name:     f,
			LabelKey: featureLabelKeys[f],
			Enabled:  !store.FeatureDisabled(profile, f),
		})
	}
	return out
}

// parseSendLimitField parses one limit input. Empty means 0 (the global value).
func parseSendLimitField(v string) (uint32, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("not a whole number: %w", err)
	}
	return uint32(n), nil
}

// countsAsLoginAdmin reports whether a profile is an administrator who can
// still sign in to the web interface.
func countsAsLoginAdmin(p *bongov1.UserProfile) bool {
	return p.IsAdmin && !p.Disabled && p.Kind == "" && !store.FeatureDisabled(p, store.FeatureWeb)
}

// otherLoginAdmins counts the administrators other than exclude who can sign
// in to the web interface. A read error is returned so the caller can refuse:
// a count that cannot be made must not let the last admin go.
func (h *AdminHandler) otherLoginAdmins(exclude string) (int, error) {
	if h.metadata == nil || h.metadata.System == nil {
		return 0, errors.New("user index unavailable")
	}
	rows, err := h.metadata.System.DB().Query("SELECT id FROM users")
	if err != nil {
		return 0, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scanning user id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("listing users: %w", err)
	}
	n := 0
	for _, id := range ids {
		if id == exclude {
			continue
		}
		p, err := h.loadProfile(id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue // an index row with no profile cannot sign in
			}
			return 0, fmt.Errorf("reading profile %s: %w", id, err)
		}
		if countsAsLoginAdmin(p) {
			n++
		}
	}
	return n, nil
}

// lastAdminMu serialises every decision that could leave the server with no
// administrator who can sign in. It is always taken after lockUser, never
// before, so the two cannot deadlock. It covers one process only.
var lastAdminMu sync.Mutex

// lastAdminCountedHook runs after wouldLeaveNoLoginAdmin has counted and before
// it returns. Tests set it to hold two requests between the read and the
// write; it is nil in production.
var lastAdminCountedHook func()

// wouldLeaveNoLoginAdmin reports whether no administrator other than userID
// can sign in to the web interface. The caller holds lastAdminMu.
func (h *AdminHandler) wouldLeaveNoLoginAdmin(userID string) (bool, error) {
	others, err := h.otherLoginAdmins(userID)
	if err != nil {
		return false, err
	}
	if lastAdminCountedHook != nil {
		lastAdminCountedHook()
	}
	return others == 0, nil
}

// SetGlobalSendLimits records the configured global send limits (messages per
// minute, recipients per hour, recipients per message) a domain admin may not
// exceed for a user.
func (h *AdminHandler) SetGlobalSendLimits(perMinute, perHour, maxRecipients int) {
	h.globalLimits = &globalSendLimits{perMinute: perMinute, perHour: perHour, maxRecipients: maxRecipients}
}

type globalSendLimits struct{ perMinute, perHour, maxRecipients int }

// limitsWithinGlobal reports whether every set limit is at or below the global
// value. 0 (use the global value) always passes. Without wired globals it
// refuses any set value (fail closed); a global of 0 means unlimited, so any
// per-user value is stricter and passes.
func (h *AdminHandler) limitsWithinGlobal(l *bongov1.SendLimits) bool {
	g := h.globalLimits
	for _, c := range []struct {
		v      uint32
		global func() int
	}{
		{l.MessagesPerMinute, func() int { return g.perMinute }},
		{l.RecipientsPerHour, func() int { return g.perHour }},
		{l.MaxRecipients, func() int { return g.maxRecipients }},
	} {
		if c.v == 0 {
			continue
		}
		if g == nil {
			return false
		}
		if gv := c.global(); gv > 0 && int64(c.v) > int64(gv) {
			return false
		}
	}
	return true
}

// UpdateUserFeatures saves the per-user feature switches and send limits of a
// person account (POST /admin/users/{userID}/features). It is a separate route
// from UpdateUser so that form can never clear these fields.
//
// Checked boxes are enabled features; the stored list holds the unchecked known
// ones plus any name this binary does not know (a newer node may have written
// it, so an edit keeps it). Limits of 0 mean the global value.
//
// The last-administrator decision is serialised on one process-wide mutex
// (lastAdminMu) shared with DeleteUser, ToggleDisabled and UpdateUser, so
// concurrent requests on one node cannot each see "one other admin" and both
// proceed. Across nodes it is still check-then-act: it reads every profile and
// then writes one, so two admins on different nodes can both pass it.
// Domain admins cannot edit their own record (403, like an out-of-scope
// target) and may set a limit only at or below the global value; a global
// admin may set any value within the ceilings. Disabling web ends the user's web sessions on their
// next request; disabling a protocol ends its sessions on their next command.
func (h *AdminHandler) UpdateUserFeatures(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	scope := middleware.AdminScopeFromContext(r.Context())
	adminID, _ := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())

	// Same per-user lock as the other profile writers, so this read-modify-write
	// cannot overwrite TotpLastStep or SessionGeneration with an older value.
	defer lockUser(userID)()
	lastAdminMu.Lock()
	defer lastAdminMu.Unlock()

	profile, ok := h.requireUserAccess(w, scope, userID)
	if !ok {
		return
	}
	// A domain admin must not lift switches or limits a global admin set on
	// their own account: the same 403 as an out-of-scope target.
	if !scope.Global && userID == adminID {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// Resources and shared inboxes never sign in: nothing to switch.
	if profile.Kind != "" {
		http.Error(w, i18n.T(locale, "admin.errFeaturesNotPerson"), http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	enabled := make(map[string]bool, len(featureLabelKeys))
	for _, f := range r.Form["feature"] {
		if !store.KnownFeature(f) {
			http.Error(w, i18n.T(locale, "admin.errFeatureUnknown"), http.StatusBadRequest)
			return
		}
		enabled[f] = true
	}
	var disabled []string
	for _, f := range store.KnownFeatures() {
		if !enabled[f] {
			disabled = append(disabled, f)
		}
	}
	for _, f := range profile.DisabledFeatures {
		if !store.KnownFeature(f) && !slices.Contains(disabled, f) {
			disabled = append(disabled, f)
		}
	}

	var limits bongov1.SendLimits
	for _, field := range []struct {
		name string
		dst  *uint32
	}{
		{"messages_per_minute", &limits.MessagesPerMinute},
		{"recipients_per_hour", &limits.RecipientsPerHour},
		{"max_recipients", &limits.MaxRecipients},
	} {
		v, err := parseSendLimitField(r.FormValue(field.name))
		if err != nil {
			http.Error(w, i18n.T(locale, "admin.errLimitsInvalid"), http.StatusBadRequest)
			return
		}
		*field.dst = v
	}
	if err := store.ValidateSendLimits(&limits); err != nil {
		http.Error(w, i18n.T(locale, "admin.errLimitsInvalid"), http.StatusBadRequest)
		return
	}

	if !scope.Global && !h.limitsWithinGlobal(&limits) {
		http.Error(w, i18n.T(locale, "admin.errLimitsAboveGlobal"), http.StatusBadRequest)
		return
	}

	next := profileWith(profile, disabled, &limits)
	if countsAsLoginAdmin(profile) && !countsAsLoginAdmin(next) {
		refused, err := h.wouldLeaveNoLoginAdmin(userID)
		if err != nil {
			h.logger.Error("admin: counting administrators failed", "admin_id", adminID, "user_id", userID, "error", err)
			http.Error(w, "failed to update user", http.StatusInternalServerError)
			return
		}
		if refused {
			h.logger.Warn("admin: refused to switch off web for the last administrator", "admin_id", adminID, "user_id", userID)
			redirectAfterAction(w, r, withFlash("/admin/users/"+url.PathEscape(userID), flashCannotDisableLastAdminWeb))
			return
		}
	}

	if err := h.saveProfile(userID, next); err != nil {
		h.logger.Error("admin: saving features failed", "admin_id", adminID, "user_id", userID, "error", err)
		http.Error(w, "failed to update user", http.StatusInternalServerError)
		return
	}

	h.logger.Info("admin: user features updated",
		"admin_id", adminID, "user_id", userID,
		"disabled_features", strings.Join(disabled, ","),
		"messages_per_minute", limits.MessagesPerMinute,
		"recipients_per_hour", limits.RecipientsPerHour,
		"max_recipients", limits.MaxRecipients)

	redirectAfterAction(w, r, withFlash("/admin/users/"+url.PathEscape(userID), flashUserFeaturesSaved))
}

// profileWith returns a copy of p with the given switches and limits. All-zero
// limits are stored as absent so an untouched account stays byte-identical.
func profileWith(p *bongov1.UserProfile, disabled []string, limits *bongov1.SendLimits) *bongov1.UserProfile {
	next, _ := proto.Clone(p).(*bongov1.UserProfile)
	next.DisabledFeatures = disabled
	if limits.MessagesPerMinute == 0 && limits.RecipientsPerHour == 0 && limits.MaxRecipients == 0 {
		next.SendLimits = nil
	} else {
		next.SendLimits = limits
	}
	return next
}
