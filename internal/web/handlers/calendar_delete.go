package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// DeleteCalendar deletes one of the user's own calendars with its events,
// tasks, grants and public feed (store.PurgeCalendar documents the partial
// failure semantics). Tasks, default, the last event calendar and a
// subscription are refused and nothing changes. It acts on the authenticated
// user's key space only, and reads the calendar first, so a crafted ID reaches
// no other key. Refusals and failures answer with a translated plain-text
// body that the settings card shows beside the button. Attendees of the deleted events are not notified.
func (h *CalendarHandler) DeleteCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	calID := chi.URLParam(r, "calendarID")
	locale := middleware.LocaleFromContext(r.Context())

	if _, err := h.calendars.Get(userID, calID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, i18n.T(locale, "calendar.errDeleteNotFound"), http.StatusNotFound)
			return
		}
		slog.Error("calendar: delete could not read the calendar", "user", userID, "calendar", calID, "error", err)
		http.Error(w, i18n.T(locale, "calendar.errDeleteFailed"), http.StatusInternalServerError)
		return
	}
	cals, err := h.calendars.List(userID)
	if err != nil {
		slog.Error("calendar: delete could not list calendars", "user", userID, "calendar", calID, "error", err)
		http.Error(w, i18n.T(locale, "calendar.errDeleteFailed"), http.StatusInternalServerError)
		return
	}
	switch err := store.CheckCalendarDeletable(cals, calID); {
	case err == nil:
	case errors.Is(err, store.ErrCalendarProtected):
		http.Error(w, i18n.T(locale, "calendar.errDeleteProtected"), http.StatusForbidden)
		return
	case errors.Is(err, store.ErrCalendarSubscribed):
		http.Error(w, i18n.T(locale, "calendar.errDeleteSubscribed"), http.StatusConflict)
		return
	case errors.Is(err, store.ErrLastEventCalendar):
		http.Error(w, i18n.T(locale, "calendar.errDeleteLast"), http.StatusConflict)
		return
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, i18n.T(locale, "calendar.errDeleteNotFound"), http.StatusNotFound)
		return
	default:
		slog.Error("calendar: delete check failed", "user", userID, "calendar", calID, "error", err)
		http.Error(w, i18n.T(locale, "calendar.errDeleteFailed"), http.StatusInternalServerError)
		return
	}

	if err := store.PurgeCalendar(h.calendars, h.events, h.tasks, userID, calID); err != nil {
		slog.Error("calendar: delete failed", "user", userID, "calendar", calID, "error", err)
		http.Error(w, i18n.T(locale, "calendar.errDeleteFailed"), http.StatusInternalServerError)
		return
	}
	slog.Info("calendar: deleted", "user", userID, "calendar", calID)

	w.Header().Set("HX-Trigger", "calendarRefresh")
	if r.Header.Get("HX-Request") == "true" {
		refreshSettings(w, r)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/calendar/settings", http.StatusSeeOther)
}
