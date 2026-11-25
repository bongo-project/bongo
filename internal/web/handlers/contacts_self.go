package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// MarkSelf handles POST /contacts/{guid}/self: the contact becomes the user's
// own card ("This is me"), replacing any earlier one. The card is looked up in
// the session user's keyspace only, so another user's GUID, or a contact in a
// book shared with the user, answers 404. A contact group is refused (400).
func (h *ContactsHandler) MarkSelf(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	switch err := h.contacts.SetSelfContact(userID, guid); {
	case err == nil:
	case errors.Is(err, store.ErrContactNotFound):
		bookFail(w, r, http.StatusNotFound, "contacts.errContactNotFound")
		return
	case errors.Is(err, store.ErrSelfContactGroup):
		bookFail(w, r, http.StatusBadRequest, "contacts.errSelfGroup")
		return
	default:
		slog.Error("marking own contact failed", "user_id", userID, "contact_id", guid, "error", err)
		http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), "contacts.errSave"), http.StatusInternalServerError)
		return
	}
	bookRedirect(w, r, withFlash("/contacts/"+url.PathEscape(guid), flashContactSelfSet))
}

// ClearSelf handles POST /contacts/{guid}/self/clear. It unmarks the card only
// while that card is the marked one, so a stale tab cannot unmark another.
func (h *ContactsHandler) ClearSelf(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	guid := chi.URLParam(r, "guid")

	if err := h.contacts.ClearSelfContact(userID, guid); err != nil {
		slog.Error("clearing own contact failed", "user_id", userID, "contact_id", guid, "error", err)
		http.Error(w, i18n.T(middleware.LocaleFromContext(r.Context()), "contacts.errSave"), http.StatusInternalServerError)
		return
	}
	bookRedirect(w, r, withFlash("/contacts/"+url.PathEscape(guid), flashContactSelfCleared))
}
