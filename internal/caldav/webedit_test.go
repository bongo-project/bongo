package caldav

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/emersion/go-ical"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/web/handlers"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// A web edit of a CalDAV-created event must be what CalDAV GET then serves:
// new summary and time, exceptions and overrides intact, and a new ETag.
func TestGetAfterWebEdit(t *testing.T) {
	b, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := "/caldav/" + userID + "/calendars/default/rt1.ics"

	src := strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Client//Cal//EN",
		"BEGIN:VEVENT", "UID:rt1", "DTSTAMP:20300101T000000Z",
		"DTSTART:20300506T090000Z", "DTEND:20300506T100000Z", "SUMMARY:Original",
		"RRULE:FREQ=WEEKLY;BYDAY=MO,WE", "EXDATE:20300513T090000Z", "EXDATE:20300520T090000Z",
		"END:VEVENT",
		"BEGIN:VEVENT", "UID:rt1", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300508T090000Z",
		"DTSTART:20300508T110000Z", "DTEND:20300508T120000Z", "SUMMARY:Override",
		"END:VEVENT", "END:VCALENDAR", "",
	}, "\r\n")
	cal, err := ical.NewDecoder(strings.NewReader(src)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.PutCalendarObject(ctx, path, cal, nil); err != nil {
		t.Fatalf("PUT: %v", err)
	}
	before, err := b.GetCalendarObject(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}

	h := handlers.NewCalendarHandler(b.events, b.calendars, nil)
	form := url.Values{
		"title": {"Edited on the web"}, "calendar_id": {"default"},
		"start_date": {"2030-05-06"}, "start_time": {"09:00"},
		"end_date": {"2030-05-06"}, "end_time": {"10:00"},
		"rrule": {"FREQ=WEEKLY;BYDAY=MO,WE"}, "reminder_minutes": {"0"},
	}
	req := httptest.NewRequest(http.MethodPut, "/calendar/event/rt1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rc := chi.NewRouteContext()
	rc.URLParams.Add("guid", "rt1")
	req = req.WithContext(context.WithValue(middleware.WithUserContext(req.Context(), userID, "u@example.com"), chi.RouteCtxKey, rc))
	w := httptest.NewRecorder()
	h.UpdateEvent(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("web edit status = %d: %s", w.Code, w.Body)
	}

	after, err := b.GetCalendarObject(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if after.ETag == before.ETag {
		t.Errorf("ETag unchanged after a web edit: %s", after.ETag)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(after.Data); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"SUMMARY:Edited on the web", "SUMMARY:Override", "UID:rt1",
		"EXDATE:20300513T090000Z", "EXDATE:20300520T090000Z",
		"RECURRENCE-ID:20300508T090000Z", "RRULE:FREQ=WEEKLY;BYDAY=MO,WE", "SEQUENCE:1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("served calendar lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SUMMARY:Original") {
		t.Error("served calendar still has the old summary")
	}
}
