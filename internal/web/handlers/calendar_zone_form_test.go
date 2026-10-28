package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The event form must read and write times in the same zone the views use
// (the user's preference zone), not the process zone. Each test sets
// time.Local to a third zone, so none may call t.Parallel.

func (e *agendaEnv) formEnv(t *testing.T) *agendaEnv {
	t.Helper()
	setLocalZone(t, "America/Los_Angeles")
	return newAgendaEnv(t, "Pacific/Auckland")
}

func TestEventFormCreateReadsUserZone(t *testing.T) {
	e := (&agendaEnv{}).formEnv(t)
	form := url.Values{
		"title": {"Nine"}, "calendar_id": {"default"}, "reminder_minutes": {"0"},
		"start_date": {"2026-11-12"}, "start_time": {"09:00"},
		"end_date": {"2026-11-12"}, "end_time": {"10:00"},
	}
	w := httptest.NewRecorder()
	e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/calendar/2026/11") {
		t.Errorf("redirect = %q", loc)
	}
	all, _, err := e.events.ListAll(quotaUser, 0, 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("events = %d, err %v", len(all), err)
	}
	// 09:00 NZDT (+13) on 12 November is 20:00 UTC on 11 November.
	if want := utc(2026, 11, 11, 20, 0); all[0].StartTime != want {
		t.Errorf("start = %d, want %d (09:00 Auckland)", all[0].StartTime, want)
	}
	if want := utc(2026, 11, 11, 21, 0); all[0].EndTime != want {
		t.Errorf("end = %d, want %d (10:00 Auckland)", all[0].EndTime, want)
	}

	month := e.view(t, e.h.MonthView, "/calendar/2026/11", map[string]string{"year": "2026", "month": "11"})
	cell := dayCell(t, month, "Thursday, November 12")
	if m := chipTimeRe.FindStringSubmatch(cell); m == nil || m[1] != "09:00" {
		t.Errorf("month chip on 12 November = %v, want 09:00", m)
	}
	week := e.view(t, e.h.WeekView, "/calendar/week/2026/46", map[string]string{"year": "2026", "week": "46"})
	if col := weekColumn(t, week, "2026-11-12"); !strings.Contains(col, ">09:00<") {
		t.Error("week column 2026-11-12 does not show 09:00")
	}
}

func seedEdge(t *testing.T, e *agendaEnv) *bongov1.Event {
	t.Helper()
	guid, err := e.events.Create(quotaUser, timedEvent("Edge", utc(2026, 11, 10, 23, 30)))
	if err != nil {
		t.Fatal(err)
	}
	ev, err := e.events.Get(quotaUser, guid)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestEventFormShowsUserZone(t *testing.T) {
	e := (&agendaEnv{}).formEnv(t)
	ev := seedEdge(t, e)
	html := e.editForm(t, ev.Guid)
	for name, want := range map[string]string{
		"start_date": "2026-11-11", "start_time": "12:30",
		"end_date": "2026-11-11", "end_time": "13:30",
	} {
		if got := inputValue(html, name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestEventFormSaveUnchangedKeepsInstant(t *testing.T) {
	e := (&agendaEnv{}).formEnv(t)
	ev := seedEdge(t, e)
	html := e.editForm(t, ev.Guid)
	form := url.Values{"title": {"Edge"}, "calendar_id": {"default"}, "reminder_minutes": {"0"}}
	for _, n := range []string{"start_date", "start_time", "end_date", "end_time"} {
		form.Set(n, inputValue(html, n))
	}
	w := httptest.NewRecorder()
	e.h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+ev.Guid, form), ev.Guid))
	if w.Code >= 400 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	got, err := e.events.Get(quotaUser, ev.Guid)
	if err != nil {
		t.Fatal(err)
	}
	if got.StartTime != ev.StartTime || got.EndTime != ev.EndTime {
		t.Errorf("instant moved: start %d -> %d, end %d -> %d", ev.StartTime, got.StartTime, ev.EndTime, got.EndTime)
	}
}
