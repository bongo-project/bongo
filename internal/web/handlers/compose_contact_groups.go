package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A contact group is expanded to ordinary addresses when it is picked in the
// compose form (and for ?contact_group=): the server never sees the group at
// send time, so the recipient cap, the budgets, the distribution-group posting
// policy and the fan-out cap all apply to the addresses as they would if the user
// had typed them. A message and a draft only ever hold real addresses.

// contactGroupMemberTotal is the number of member references a group lists.
func contactGroupMemberTotal(g *bongov1.Contact) int {
	_, members := store.ContactGroupInfo(g)
	return len(members)
}

// expandOwnContactGroup loads the caller's contact group and expands it. ok is
// false for an unknown guid, another user's guid and a person's guid alike.
func (h *ComposeHandler) expandOwnContactGroup(userID, guid string) (exp *store.ExpandedContactGroup, ok bool, err error) {
	if h.contacts == nil || userID == "" {
		return nil, false, nil
	}
	g, err := h.contacts.Get(userID, guid)
	if err != nil || !store.IsContactGroup(g) {
		return nil, false, nil
	}
	exp, err = h.contacts.ExpandContactGroup(userID, g)
	return exp, err == nil, err
}

// contactGroupNotices is what the user is told about an expansion: members
// skipped for having no address, and, when the addresses exceed the recipient
// limit, that the message cannot be sent as it stands. skipped and overLimit
// are "" when there is nothing to say.
func contactGroupNotices(locale string, exp *store.ExpandedContactGroup, limit int) (skipped, overLimit string) {
	if exp.Skipped > 0 {
		skipped = fmt.Sprintf(i18n.T(locale, "compose.contactGroupSkipped"), exp.Skipped)
	}
	if len(exp.Addresses) > limit {
		overLimit = fmt.Sprintf(i18n.T(locale, "compose.contactGroupOverLimit"), limit)
	}
	return skipped, overLimit
}

// contactGroupPrefill is the To value for ?contact_group={guid}: the group's
// addresses, or "" when the group is not the caller's or cannot be read. notice
// carries the same skipped-members and over-limit warnings the autocomplete
// shows, so the user sees them before sending.
func (h *ComposeHandler) contactGroupPrefill(userID, locale, guid string) (to, notice string) {
	exp, ok, err := h.expandOwnContactGroup(userID, guid)
	if err != nil {
		slog.Warn("compose: contact group prefill failed", "user", userID, "group", guid, "error", err)
		return "", ""
	}
	if !ok {
		return "", ""
	}
	skipped, over := contactGroupNotices(locale, exp, h.recipientLimitFor(userID))
	return strings.Join(exp.Addresses, ", "), strings.TrimSpace(skipped + " " + over)
}

// ContactGroupAddresses answers GET /api/contact-groups/{guid}/addresses with
// the group's member addresses for the compose form:
//
//	{"addresses": [...], "skipped": N, "limit": L,
//	 "skipped_notice": "...", "over_limit_notice": "..."}
//
// skipped counts members that give no address (no email, or a nested group);
// limit is the per-message recipient cap. The notices are translated. A guid
// that is not one of the caller's contact groups is a 404.
func (h *ComposeHandler) ContactGroupAddresses(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	exp, ok, err := h.expandOwnContactGroup(userID, guid)
	if err != nil {
		slog.Warn("compose: contact group expansion failed", "user", userID, "group", guid, "error", err)
		http.Error(w, i18n.T(locale, "compose.contactGroupFailed"), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, i18n.T(locale, "contacts.errGroupNotFound"), http.StatusNotFound)
		return
	}

	limit := h.recipientLimitFor(userID)
	resp := struct {
		Addresses       []string `json:"addresses"`
		Skipped         int      `json:"skipped"`
		Limit           int      `json:"limit"`
		SkippedNotice   string   `json:"skipped_notice,omitempty"`
		OverLimitNotice string   `json:"over_limit_notice,omitempty"`
	}{Addresses: exp.Addresses, Skipped: exp.Skipped, Limit: limit}
	if resp.Addresses == nil {
		resp.Addresses = []string{}
	}
	resp.SkippedNotice, _ = contactGroupNotices(locale, exp, limit)
	// The browser decides when to show this one: it counts To, Cc and Bcc
	// together, which the server cannot see.
	resp.OverLimitNotice = fmt.Sprintf(i18n.T(locale, "compose.contactGroupOverLimit"), limit)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		return
	}
}
