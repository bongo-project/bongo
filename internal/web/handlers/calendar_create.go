package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// maxNewCalendarName is the longest name the New calendar form accepts, in
// runes. A longer name is refused, not truncated.
const maxNewCalendarName = 255

// hexColourRE is what <input type=color> posts: # and six hex digits.
var hexColourRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// CreateCalendar handles POST /calendar: a new calendar of the user's own,
// optionally holding tasks. The GUID is generated here, never taken from the
// form. Every failure path writes nothing. store.CalendarStore.CreateNew
// enforces the per-user cap (409).
func (h *CalendarHandler) CreateCalendar(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	locale := middleware.LocaleFromContext(r.Context())

	name := strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, r.FormValue("name")))
	if name == "" || utf8.RuneCountInString(name) > maxNewCalendarName {
		http.Error(w, i18n.T(locale, "calendar.errNewName"), http.StatusBadRequest)
		return
	}
	colour := r.FormValue("color")
	if !hexColourRE.MatchString(colour) {
		http.Error(w, i18n.T(locale, "calendar.errNewColour"), http.StatusBadRequest)
		return
	}
	var components []string // nil is the stored VEVENT default
	if r.FormValue("holds_tasks") != "" {
		components = []string{store.ComponentEvent, store.ComponentTodo}
	}

	if _, err := h.calendars.EnsureDefault(userID); err != nil {
		http.Error(w, "failed to init calendar", http.StatusInternalServerError)
		return
	}
	err := h.calendars.CreateNew(userID, &bongov1.Calendar{
		Guid:       store.NewCalendarGUID(),
		Name:       name,
		Color:      colour,
		Components: components,
	})
	switch {
	case errors.Is(err, store.ErrCalendarLimit):
		http.Error(w, calendarLimitMessage(locale), http.StatusConflict)
		return
	case err != nil:
		slog.Error("calendar: create failed", "user", userID, "error", err)
		http.Error(w, "failed to create calendar", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "calendarRefresh")
	if r.Header.Get("HX-Request") == "true" {
		refreshSettings(w, r)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/calendar/settings", http.StatusSeeOther)
}

// calendarLimitMessage tells the user they own store.MaxCalendarsPerUser
// calendars already.
func calendarLimitMessage(locale string) string {
	return fmt.Sprintf(i18n.T(locale, "calendar.errCalendarLimit"), store.MaxCalendarsPerUser)
}
