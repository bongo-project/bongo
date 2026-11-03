package handlers

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The Delete button asks for confirmation and shows a refused or failed
// delete in the status element that follows it.
func TestSettingsCardDeleteButtonConfirmsAndShowsErrors(t *testing.T) {
	e := newCalDelEnv(t)
	req := withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/settings", nil), pubOwner, pubOwner+"@example.com")
	w := httptest.NewRecorder()
	e.cal.CalendarSettings(w, req)
	body := w.Body.String()

	_, rest, ok := strings.Cut(body, `hx-delete="/calendar/work"`)
	if !ok {
		t.Fatal("no delete button for work")
	}
	btn, after, ok := strings.Cut(rest, "</button>")
	if !ok {
		t.Fatal("delete button not closed")
	}
	want := `hx-confirm="` + html.EscapeString(i18n.T("en", "calendar.deleteConfirm")) + `"`
	if !strings.Contains(btn, want) {
		t.Errorf("button lacks %s: %s", want, btn)
	}
	if !strings.Contains(btn, `hx-target="next .calendar-delete-status"`) {
		t.Errorf("button lacks its status target: %s", btn)
	}
	if !strings.HasPrefix(strings.TrimSpace(after), `<div class="calendar-delete-status" data-form-status`) {
		t.Errorf("status element does not follow the button: %.120s", after)
	}
}

func deleteWithLocale(h *CalendarHandler, id, locale string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/calendar/"+id, nil)
	req.Header.Set("HX-Request", "true")
	req = withChiParam(withUserContext(req, pubOwner, pubOwner+"@example.com"), "calendarID", id)
	req = req.WithContext(middleware.WithLocale(req.Context(), locale))
	w := httptest.NewRecorder()
	h.DeleteCalendar(w, req)
	return w
}

// Refusals answer in the user's language, never with the bare message key.
func TestDeleteCalendarRefusalsAreTranslated(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, e *calDelEnv)
		id    string
		want  int
		key   string
	}{
		{"tasks", nil, "tasks", http.StatusForbidden, "calendar.errDeleteProtected"},
		{"subscription", func(t *testing.T, e *calDelEnv) {
			if _, err := e.calendars.Create(pubOwner, &bongov1.Calendar{Guid: "feed", Name: "Feed", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"}}); err != nil {
				t.Fatal(err)
			}
		}, "feed", http.StatusConflict, "calendar.errDeleteSubscribed"},
		{"last event calendar", func(t *testing.T, e *calDelEnv) {
			if err := e.calendars.Delete(pubOwner, "default"); err != nil {
				t.Fatal(err)
			}
		}, "work", http.StatusConflict, "calendar.errDeleteLast"},
		{"missing", nil, "nope", http.StatusNotFound, "calendar.errDeleteNotFound"},
	}
	for _, tt := range cases {
		english := i18n.T("en", tt.key)
		for _, locale := range []string{"en", "de", "fr", "es"} {
			t.Run(tt.name+"/"+locale, func(t *testing.T) {
				e := newCalDelEnv(t)
				if tt.setup != nil {
					tt.setup(t, e)
				}
				w := deleteWithLocale(e.cal, tt.id, locale)
				if w.Code != tt.want {
					t.Fatalf("DELETE = %d %q, want %d", w.Code, w.Body.String(), tt.want)
				}
				got := strings.TrimSpace(w.Body.String())
				if got != i18n.T(locale, tt.key) {
					t.Errorf("body = %q, want %q", got, i18n.T(locale, tt.key))
				}
				if got == tt.key || got == "" {
					t.Errorf("body is the key or empty: %q", got)
				}
				if locale != "en" && got == english {
					t.Errorf("%s body is still English: %q", locale, got)
				}
				if tt.id != "nope" && !e.calendarExists(t, pubOwner, tt.id) {
					t.Errorf("%s was deleted", tt.id)
				}
				if !e.calendarExists(t, pubOwner, "work") {
					t.Error("work was deleted")
				}
			})
		}
	}
}

// A persistence failure answers 500 with the translated message and leaves the
// calendar and its events alone.
func TestDeleteCalendarWriteFailure(t *testing.T) {
	e := newCalDelEnv(t)
	h := NewCalendarHandler(store.NewEventStore(e.ps, failingApplier{}), store.NewCalendarStore(e.ps, failingApplier{}), nil)
	w := deleteWithLocale(h, "work", "de")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE = %d %q, want 500", w.Code, w.Body.String())
	}
	want := i18n.T("de", "calendar.errDeleteFailed")
	got := strings.TrimSpace(w.Body.String())
	if got != want || got == "calendar.errDeleteFailed" || got == i18n.T("en", "calendar.errDeleteFailed") {
		t.Errorf("body = %q, want %q", got, want)
	}
	if w.Header().Get("HX-Refresh") != "" {
		t.Error("a failed delete asked the page to refresh")
	}
	if !e.calendarExists(t, pubOwner, "work") {
		t.Error("calendar deleted despite the failure")
	}
	if _, err := e.events.Get(pubOwner, "ev-work"); err != nil {
		t.Errorf("event lost: %v", err)
	}
}
