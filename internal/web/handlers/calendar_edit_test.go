package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const clientUID = "client-uid-7f3a@example.org"

// clientICS is what a CalDAV client stores: a recurring master with two
// EXDATEs, an alarm, an attendee and an X- property, plus an override.
func clientICS(dtstart, dtend string) []byte {
	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//Client//Cal//EN",
		"BEGIN:VEVENT",
		"UID:" + clientUID,
		"DTSTAMP:20300101T000000Z",
		dtstart,
		dtend,
		"SUMMARY:Team sync",
		"RRULE:FREQ=WEEKLY;BYDAY=MO,WE",
		"EXDATE:20300513T090000Z",
		"EXDATE:20300520T090000Z",
		"SEQUENCE:2",
		"ATTENDEE;CN=Bob:mailto:bob@example.org",
		"X-FOO:bar",
		"BEGIN:VALARM",
		"ACTION:DISPLAY",
		"DESCRIPTION:Reminder",
		"TRIGGER:-PT15M",
		"END:VALARM",
		"END:VEVENT",
		"BEGIN:VEVENT",
		"UID:" + clientUID,
		"DTSTAMP:20300101T000000Z",
		"RECURRENCE-ID:20300508T090000Z",
		"DTSTART:20300508T110000Z",
		"DTEND:20300508T120000Z",
		"SUMMARY:Moved occurrence",
		"END:VEVENT",
		"END:VCALENDAR",
		"",
	}
	return []byte(strings.Join(lines, "\r\n"))
}

func utcUnix(y int, m time.Month, d, h, min int) int64 {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC).Unix()
}

func seedClientEvent(t *testing.T, env *quotaEnv, raw []byte, start, end int64, tz string) (string, *bongov1.Event) {
	t.Helper()
	ev := &bongov1.Event{
		Title: "Team sync", CalendarId: "default", StartTime: start, EndTime: end,
		Rrule: "FREQ=WEEKLY;BYDAY=MO,WE", Timezone: tz, IcalRaw: raw,
	}
	guid, err := env.events.Create(quotaUser, ev)
	if err != nil {
		t.Fatal(err)
	}
	got, err := env.events.Get(quotaUser, guid)
	if err != nil {
		t.Fatal(err)
	}
	return guid, got
}

// browserForm builds what the edit page submits for ev, with overrides. Times
// are in the server's local zone, which is how parseEventForm reads them.
func browserForm(ev *bongov1.Event, over map[string]string) url.Values {
	s, e := time.Unix(ev.StartTime, 0), time.Unix(ev.EndTime, 0)
	f := url.Values{
		"title":            {ev.Title},
		"description":      {ev.Description},
		"location":         {ev.Location},
		"calendar_id":      {ev.CalendarId},
		"start_date":       {s.Format("2006-01-02")},
		"start_time":       {s.Format("15:04")},
		"end_date":         {e.Format("2006-01-02")},
		"end_time":         {e.Format("15:04")},
		"rrule":            {ev.Rrule},
		"reminder_minutes": {"0"},
	}
	for k, v := range over {
		f.Set(k, v)
	}
	return f
}

func decodeStored(t *testing.T, env *quotaEnv, guid string) (*bongov1.Event, *ical.Calendar) {
	t.Helper()
	ev, err := env.events.Get(quotaUser, guid)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev.IcalRaw) == 0 {
		return ev, nil
	}
	cal, err := ical.NewDecoder(bytes.NewReader(ev.IcalRaw)).Decode()
	if err != nil {
		t.Fatalf("stored ical_raw does not decode: %v\n%s", err, ev.IcalRaw)
	}
	return ev, cal
}

// masterAndOverride splits the VEVENTs of cal.
func masterAndOverride(t *testing.T, cal *ical.Calendar) (master, override *ical.Component) {
	t.Helper()
	if cal == nil {
		t.Fatal("event has no ical_raw")
	}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if c.Props.Get(ical.PropRecurrenceID) != nil {
			override = c
		} else {
			master = c
		}
	}
	if master == nil || override == nil {
		t.Fatalf("want master and override VEVENT, got master=%v override=%v", master != nil, override != nil)
	}
	return master, override
}

func propValue(c *ical.Component, name string) string {
	if p := c.Props.Get(name); p != nil {
		return p.Value
	}
	return ""
}

func assertPreserved(t *testing.T, master, override *ical.Component) {
	t.Helper()
	if got := propValue(master, ical.PropRecurrenceRule); got != "FREQ=WEEKLY;BYDAY=MO,WE" {
		t.Errorf("RRULE = %q", got)
	}
	if n := len(master.Props.Values(ical.PropExceptionDates)); n != 2 {
		t.Errorf("EXDATE count = %d, want 2", n)
	}
	if got := propValue(master, ical.PropUID); got != clientUID {
		t.Errorf("UID = %q, want the client's", got)
	}
	if got := propValue(master, "X-FOO"); got != "bar" {
		t.Errorf("X-FOO = %q", got)
	}
	if got := propValue(master, ical.PropAttendee); got != "mailto:bob@example.org" {
		t.Errorf("ATTENDEE = %q", got)
	}
	var alarm bool
	for _, c := range master.Children {
		alarm = alarm || (c.Name == ical.CompAlarm && propValue(c, ical.PropTrigger) == "-PT15M")
	}
	if !alarm {
		t.Error("VALARM lost")
	}
	if got := propValue(override, ical.PropSummary); got != "Moved occurrence" {
		t.Errorf("override SUMMARY = %q", got)
	}
	if got := propValue(override, ical.PropRecurrenceID); got != "20300508T090000Z" {
		t.Errorf("override RECURRENCE-ID = %q", got)
	}
}

func TestUpdateEventPreservesIcalRaw(t *testing.T) {
	start, end := utcUnix(2030, 5, 6, 9, 0), utcUnix(2030, 5, 6, 10, 0)
	raw := clientICS("DTSTART:20300506T090000Z", "DTEND:20300506T100000Z")

	t.Run("summary and time edit keeps everything else", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		guid, existing := seedClientEvent(t, env, raw, start, end, "")

		newStart := time.Unix(utcUnix(2030, 5, 6, 14, 0), 0)
		newEnd := time.Unix(utcUnix(2030, 5, 6, 15, 30), 0)
		form := browserForm(existing, map[string]string{
			"title":      "Renamed sync",
			"start_date": newStart.Format("2006-01-02"), "start_time": newStart.Format("15:04"),
			"end_date": newEnd.Format("2006-01-02"), "end_time": newEnd.Format("15:04"),
		})
		w := httptest.NewRecorder()
		h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, form), guid))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}

		ev, cal := decodeStored(t, env, guid)
		master, override := masterAndOverride(t, cal)
		assertPreserved(t, master, override)
		if got := propValue(master, ical.PropSummary); got != "Renamed sync" {
			t.Errorf("SUMMARY = %q", got)
		}
		if got := propValue(master, ical.PropDateTimeStart); got != "20300506T140000Z" {
			t.Errorf("DTSTART = %q", got)
		}
		if got := propValue(master, ical.PropDateTimeEnd); got != "20300506T153000Z" {
			t.Errorf("DTEND = %q", got)
		}
		if got := propValue(master, ical.PropSequence); got != "3" {
			t.Errorf("SEQUENCE = %q, want 3", got)
		}
		if propValue(master, ical.PropDateTimeStamp) == "20300101T000000Z" || propValue(master, ical.PropLastModified) == "" {
			t.Error("DTSTAMP not refreshed or LAST-MODIFIED missing")
		}
		if ev.Title != "Renamed sync" || ev.StartTime != newStart.Unix() || ev.EndTime != newEnd.Unix() {
			t.Errorf("structured fields out of sync: %q %d %d", ev.Title, ev.StartTime, ev.EndTime)
		}
		if ev.Rrule != "FREQ=WEEKLY;BYDAY=MO,WE" {
			t.Errorf("structured Rrule = %q", ev.Rrule)
		}
	})

	t.Run("rrule outside the allowlist is refused", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		guid, existing := seedClientEvent(t, env, raw, start, end, "")
		for _, bad := range []string{"FREQ=HOURLY", "FREQ=DAILY\r\nATTENDEE:mailto:evil@example.org"} {
			form := browserForm(existing, map[string]string{"rrule": bad})
			w := httptest.NewRecorder()
			h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, form), guid))
			if w.Code != http.StatusBadRequest {
				t.Errorf("rrule %q: status = %d, want 400", bad, w.Code)
			}
		}
		got, _ := env.events.Get(quotaUser, guid)
		if !bytes.Equal(got.IcalRaw, raw) {
			t.Error("refused edit changed the stored record")
		}
	})

	t.Run("choosing None removes the RRULE and a preset replaces it", func(t *testing.T) {
		for _, tc := range []struct{ rrule, want string }{{"", ""}, {"FREQ=DAILY", "FREQ=DAILY"}} {
			env := newQuotaEnv(t)
			h := NewCalendarHandler(env.events, env.calendars, nil)
			guid, existing := seedClientEvent(t, env, raw, start, end, "")
			w := httptest.NewRecorder()
			h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, browserForm(existing, map[string]string{"rrule": tc.rrule})), guid))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d", w.Code)
			}
			ev, cal := decodeStored(t, env, guid)
			master, _ := masterAndOverride(t, cal)
			if got := propValue(master, ical.PropRecurrenceRule); got != tc.want || ev.Rrule != tc.want {
				t.Errorf("rrule %q: RRULE = %q, structured = %q", tc.rrule, got, ev.Rrule)
			}
		}
	})

	t.Run("summary-only edit leaves a TZID DTSTART byte-identical", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		london, err := time.LoadLocation("Europe/London")
		if err != nil {
			t.Skip("no tzdata")
		}
		s := time.Date(2030, 7, 8, 9, 0, 0, 0, london)
		tzRaw := clientICS("DTSTART;TZID=Europe/London:20300708T090000", "DTEND;TZID=Europe/London:20300708T100000")
		guid, existing := seedClientEvent(t, env, tzRaw, s.Unix(), s.Add(time.Hour).Unix(), "Europe/London")
		w := httptest.NewRecorder()
		h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, browserForm(existing, map[string]string{"title": "Only the name"})), guid))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", w.Code)
		}
		ev, cal := decodeStored(t, env, guid)
		master, override := masterAndOverride(t, cal)
		assertPreserved(t, master, override)
		p := master.Props.Get(ical.PropDateTimeStart)
		if p.Value != "20300708T090000" || p.Params.Get(ical.PropTimezoneID) != "Europe/London" {
			t.Errorf("DTSTART = %v %v", p.Value, p.Params)
		}
		if got := propValue(master, ical.PropDateTimeEnd); got != "20300708T100000" {
			t.Errorf("DTEND = %q", got)
		}
		if ev.Timezone != "Europe/London" {
			t.Errorf("Timezone = %q, want it kept", ev.Timezone)
		}
	})

	t.Run("summary-only edit keeps an unloadable TZID and DURATION as stored", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		odd := clientICS("DTSTART;TZID=Mars/Olympus:20300506T090000", "DURATION:PT1H")
		guid, existing := seedClientEvent(t, env, odd, start, end, "Mars/Olympus")
		w := httptest.NewRecorder()
		h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, browserForm(existing, map[string]string{"title": "Only the name"})), guid))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", w.Code)
		}
		_, cal := decodeStored(t, env, guid)
		master, _ := masterAndOverride(t, cal)
		p := master.Props.Get(ical.PropDateTimeStart)
		if p.Value != "20300506T090000" || p.Params.Get(ical.PropTimezoneID) != "Mars/Olympus" {
			t.Errorf("DTSTART = %v %v", p.Value, p.Params)
		}
		if got := propValue(master, ical.PropDuration); got != "PT1H" {
			t.Errorf("DURATION = %q, want it kept", got)
		}
		if master.Props.Get(ical.PropDateTimeEnd) != nil {
			t.Error("DTEND added to an untouched event")
		}
	})

	t.Run("web-created event updates as before", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		guid, err := env.events.Create(quotaUser, &bongov1.Event{
			Title: "Lunch", CalendarId: "default", StartTime: start, EndTime: end, Timezone: "Europe/Berlin", Rrule: "FREQ=DAILY",
			Exdates: []string{"20300513"},
		})
		if err != nil {
			t.Fatal(err)
		}
		existing, _ := env.events.Get(quotaUser, guid)
		w := httptest.NewRecorder()
		h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, browserForm(existing, map[string]string{"title": "Dinner"})), guid))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d", w.Code)
		}
		got, _ := env.events.Get(quotaUser, guid)
		if got.Title != "Dinner" || len(got.IcalRaw) != 0 || got.Timezone != "Europe/Berlin" || got.Rrule != "FREQ=DAILY" || len(got.Exdates) != 1 || got.Exdates[0] != "20300513" {
			t.Errorf("got title=%q raw=%d tz=%q rrule=%q exdates=%v", got.Title, len(got.IcalRaw), got.Timezone, got.Rrule, got.Exdates)
		}
	})
}

func TestRescheduleEventRewritesIcalRaw(t *testing.T) {
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	start, end := utcUnix(2030, 5, 6, 9, 0), utcUnix(2030, 5, 6, 10, 0)
	raw := clientICS("DTSTART:20300506T090000Z", "DTEND:20300506T100000Z")
	guid, before := seedClientEvent(t, env, raw, start, end, "")

	form := url.Values{
		"start_time": {"1904000000"}, // 2030-05-03T... fixed instants, not round times
		"end_time":   {"1904003777"},
	}
	w := httptest.NewRecorder()
	h.RescheduleEvent(w, withGUID(formReq(http.MethodPost, "/calendar/event/"+guid+"/reschedule", form), guid))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	ev, cal := decodeStored(t, env, guid)
	master, override := masterAndOverride(t, cal)
	assertPreserved(t, master, override)
	wantStart := time.Unix(1904000000, 0).UTC().Format("20060102T150405Z")
	wantEnd := time.Unix(1904003777, 0).UTC().Format("20060102T150405Z")
	if got := propValue(master, ical.PropDateTimeStart); got != wantStart {
		t.Errorf("DTSTART = %q, want %q", got, wantStart)
	}
	if got := propValue(master, ical.PropDateTimeEnd); got != wantEnd {
		t.Errorf("DTEND = %q, want %q", got, wantEnd)
	}
	if got := propValue(master, ical.PropSummary); got != "Team sync" {
		t.Errorf("SUMMARY = %q", got)
	}
	if got := propValue(master, ical.PropSequence); got != "3" {
		t.Errorf("SEQUENCE = %q, want 3", got)
	}
	if store.ContentETag(ev.IcalRaw, ev.UpdatedAt) == store.ContentETag(before.IcalRaw, before.UpdatedAt) {
		t.Error("ETag unchanged after reschedule")
	}
}

func TestApplyEventEditToICALAllDay(t *testing.T) {
	raw := []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:ad@x\r\nDTSTAMP:20300101T000000Z\r\nDTSTART;VALUE=DATE:20300506\r\nDTEND;VALUE=DATE:20300507\r\nSUMMARY:Holiday\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	// The form reads dates in the server's local zone.
	day := func(d int) int64 { return time.Date(2030, 5, d, 0, 0, 0, 0, time.Local).Unix() }
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	existing := &bongov1.Event{Title: "Holiday", AllDay: true, StartTime: utcUnix(2030, 5, 6, 0, 0), EndTime: utcUnix(2030, 5, 7, 0, 0)}

	for _, tc := range []struct {
		name       string
		start, end int64
		wantStart  string
		wantEnd    string
	}{
		{"same dates are left alone", day(6), day(7), "20300506", "20300507"},
		{"moved dates are written as DATE", day(9), day(11), "20300509", "20300511"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated := &bongov1.Event{Title: "Holiday", AllDay: true, StartTime: tc.start, EndTime: tc.end}
			out, err := applyEventEditToICal(raw, existing, updated, now)
			if err != nil {
				t.Fatal(err)
			}
			cal, err := ical.NewDecoder(bytes.NewReader(out)).Decode()
			if err != nil {
				t.Fatal(err)
			}
			ev := cal.Events()[0]
			s, e := ev.Props.Get(ical.PropDateTimeStart), ev.Props.Get(ical.PropDateTimeEnd)
			if s.Value != tc.wantStart || e.Value != tc.wantEnd || s.Params.Get(ical.ParamValue) != "DATE" {
				t.Errorf("DTSTART=%v DTEND=%v", s, e)
			}
		})
	}
}

// putEdit submits form through UpdateEvent and returns the status.
func putEdit(t *testing.T, h *CalendarHandler, guid string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, form), guid))
	return w
}

func TestUpdateEventIcalEdgeCases(t *testing.T) {
	start, end := utcUnix(2030, 5, 6, 9, 0), utcUnix(2030, 5, 6, 10, 0)
	utcRaw := clientICS("DTSTART:20300506T090000Z", "DTEND:20300506T100000Z")

	t.Run("CRLF description from a browser encodes and is stable across saves", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		guid, existing := seedClientEvent(t, env, utcRaw, start, end, "")

		w := putEdit(t, h, guid, browserForm(existing, map[string]string{"description": "line one\r\nline two"}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		ev, cal := decodeStored(t, env, guid)
		master, _ := masterAndOverride(t, cal)
		if got, _ := master.Props.Text(ical.PropDescription); got != "line one\nline two" {
			t.Errorf("DESCRIPTION = %q", got)
		}
		if ev.Description != "line one\nline two" {
			t.Errorf("structured Description = %q", ev.Description)
		}

		// The page now posts the stored LF description back as CRLF; a
		// summary-only edit must still succeed and leave it alone.
		w = putEdit(t, h, guid, browserForm(ev, map[string]string{"title": "Renamed", "description": "line one\r\nline two"}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("second save status = %d: %s", w.Code, w.Body)
		}
		_, cal = decodeStored(t, env, guid)
		master, _ = masterAndOverride(t, cal)
		if got, _ := master.Props.Text(ical.PropDescription); got != "line one\nline two" {
			t.Errorf("DESCRIPTION after second save = %q", got)
		}
	})

	t.Run("moving a TZID event keeps the TZID and local wall time", func(t *testing.T) {
		london, err := time.LoadLocation("Europe/London")
		if err != nil {
			t.Skip("no tzdata")
		}
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		s := time.Date(2030, 7, 8, 9, 0, 0, 0, london)
		tzRaw := clientICS("DTSTART;TZID=Europe/London:20300708T090000", "DTEND;TZID=Europe/London:20300708T100000")
		guid, existing := seedClientEvent(t, env, tzRaw, s.Unix(), s.Add(time.Hour).Unix(), "Europe/London")

		ns := time.Date(2030, 7, 9, 14, 30, 0, 0, london).In(time.Local)
		ne := ns.Add(time.Hour)
		w := putEdit(t, h, guid, browserForm(existing, map[string]string{
			"start_date": ns.Format("2006-01-02"), "start_time": ns.Format("15:04"),
			"end_date": ne.Format("2006-01-02"), "end_time": ne.Format("15:04"),
		}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		_, cal := decodeStored(t, env, guid)
		master, _ := masterAndOverride(t, cal)
		p := master.Props.Get(ical.PropDateTimeStart)
		if p.Value != "20300709T143000" || p.Params.Get(ical.PropTimezoneID) != "Europe/London" {
			t.Errorf("DTSTART = %q %v, want 20300709T143000 with TZID=Europe/London", p.Value, p.Params)
		}
		q := master.Props.Get(ical.PropDateTimeEnd)
		if q.Value != "20300709T153000" || q.Params.Get(ical.PropTimezoneID) != "Europe/London" {
			t.Errorf("DTEND = %q %v", q.Value, q.Params)
		}
	})

	t.Run("moving a DURATION event replaces DURATION with DTEND", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		raw := clientICS("DTSTART:20300506T090000Z", "DURATION:PT1H")
		guid, existing := seedClientEvent(t, env, raw, start, end, "")
		ns, ne := time.Unix(utcUnix(2030, 5, 6, 13, 0), 0), time.Unix(utcUnix(2030, 5, 6, 15, 0), 0)
		w := putEdit(t, h, guid, browserForm(existing, map[string]string{
			"start_date": ns.Format("2006-01-02"), "start_time": ns.Format("15:04"),
			"end_date": ne.Format("2006-01-02"), "end_time": ne.Format("15:04"),
		}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		_, cal := decodeStored(t, env, guid)
		master, _ := masterAndOverride(t, cal)
		if master.Props.Get(ical.PropDuration) != nil {
			t.Error("DURATION kept alongside DTEND")
		}
		if got := propValue(master, ical.PropDateTimeEnd); got != "20300506T150000Z" {
			t.Errorf("DTEND = %q", got)
		}
	})

	t.Run("an override listed first is not mistaken for the master", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		cal, err := ical.NewDecoder(bytes.NewReader(utcRaw)).Decode()
		if err != nil {
			t.Fatal(err)
		}
		cal.Children[0], cal.Children[1] = cal.Children[1], cal.Children[0]
		var buf bytes.Buffer
		if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
			t.Fatal(err)
		}
		guid, existing := seedClientEvent(t, env, buf.Bytes(), start, end, "")
		w := putEdit(t, h, guid, browserForm(existing, map[string]string{"title": "Renamed sync"}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		_, got := decodeStored(t, env, guid)
		master, override := masterAndOverride(t, got)
		if v := propValue(master, ical.PropSummary); v != "Renamed sync" {
			t.Errorf("master SUMMARY = %q", v)
		}
		if v := propValue(override, ical.PropSummary); v != "Moved occurrence" {
			t.Errorf("override SUMMARY = %q, want it untouched", v)
		}
	})

	t.Run("clearing description and location removes them from the raw", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		raw := []byte(strings.Replace(string(utcRaw), "SUMMARY:Team sync", "SUMMARY:Team sync\r\nDESCRIPTION:old notes\r\nLOCATION:Room 1", 1))
		guid, err := env.events.Create(quotaUser, &bongov1.Event{
			Title: "Team sync", Description: "old notes", Location: "Room 1", CalendarId: "default",
			StartTime: start, EndTime: end, Rrule: "FREQ=WEEKLY;BYDAY=MO,WE", IcalRaw: raw,
		})
		if err != nil {
			t.Fatal(err)
		}
		existing, _ := env.events.Get(quotaUser, guid)
		w := putEdit(t, h, guid, browserForm(existing, map[string]string{"description": "", "location": ""}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		ev, cal := decodeStored(t, env, guid)
		master, _ := masterAndOverride(t, cal)
		if master.Props.Get(ical.PropDescription) != nil || master.Props.Get(ical.PropLocation) != nil {
			t.Errorf("DESCRIPTION/LOCATION still in raw: %q %q", propValue(master, ical.PropDescription), propValue(master, ical.PropLocation))
		}
		if ev.Description != "" || ev.Location != "" {
			t.Errorf("structured fields = %q %q", ev.Description, ev.Location)
		}
	})
}

// TestEventEditRawFailurePaths pins what UpdateEvent and RescheduleEvent do
// when the stored raw cannot be rewritten. Raw that does not decode is dropped,
// so CalDAV GET serves the edit built from the structured fields rather than
// the stale raw with an unchanged ETag. Raw that decodes but cannot be
// re-encoded (here an override with both DTEND and DURATION, which the decoder
// accepts and the encoder refuses) fails the request and leaves the event as
// it was, rather than silently discarding the client's data.
func TestEventEditRawFailurePaths(t *testing.T) {
	start, end := utcUnix(2030, 5, 6, 9, 0), utcUnix(2030, 5, 6, 10, 0)
	garbage := []byte("not an icalendar object")
	unencodable := []byte(strings.Replace(string(clientICS("DTSTART:20300506T090000Z", "DTEND:20300506T100000Z")),
		"DTEND:20300508T120000Z", "DTEND:20300508T120000Z\r\nDURATION:PT1H", 1))

	t.Run("update with undecodable raw saves the edit and drops the raw", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		guid, existing := seedClientEvent(t, env, garbage, start, end, "")
		w := putEdit(t, h, guid, browserForm(existing, map[string]string{"title": "Renamed sync"}))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body)
		}
		ev, _ := decodeStored(t, env, guid)
		if len(ev.IcalRaw) != 0 {
			t.Errorf("stale ical_raw kept: %q", ev.IcalRaw)
		}
		if ev.Title != "Renamed sync" {
			t.Errorf("Title = %q", ev.Title)
		}
	})

	t.Run("reschedule with undecodable raw moves the event and drops the raw", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		guid, _ := seedClientEvent(t, env, garbage, start, end, "")
		form := url.Values{"start_time": {"1904000000"}, "end_time": {"1904003777"}}
		w := httptest.NewRecorder()
		h.RescheduleEvent(w, withGUID(formReq(http.MethodPost, "/calendar/event/"+guid+"/reschedule", form), guid))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		ev, _ := decodeStored(t, env, guid)
		if len(ev.IcalRaw) != 0 {
			t.Errorf("stale ical_raw kept: %q", ev.IcalRaw)
		}
		if ev.StartTime != 1904000000 || ev.EndTime != 1904003777 {
			t.Errorf("times = %d-%d", ev.StartTime, ev.EndTime)
		}
	})

	t.Run("update with raw that cannot be re-encoded fails and changes nothing", func(t *testing.T) {
		env := newQuotaEnv(t)
		h := NewCalendarHandler(env.events, env.calendars, nil)
		guid, existing := seedClientEvent(t, env, unencodable, start, end, "")
		w := putEdit(t, h, guid, browserForm(existing, map[string]string{"title": "Renamed sync"}))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", w.Code)
		}
		ev, err := env.events.Get(quotaUser, guid)
		if err != nil {
			t.Fatal(err)
		}
		if ev.Title != "Team sync" || !bytes.Equal(ev.IcalRaw, unencodable) {
			t.Errorf("event changed: title %q, raw kept %v", ev.Title, bytes.Equal(ev.IcalRaw, unencodable))
		}
	})
}
