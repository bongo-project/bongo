package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/caldav"
	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const seriesICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//C//EN\r\n" +
	"BEGIN:VEVENT\r\nUID:s1\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\n" +
	"SUMMARY:Weekly sync\r\nRRULE:FREQ=WEEKLY;BYDAY=MO\r\nEXDATE:20300520T090000Z\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:s1\r\nDTSTAMP:20300101T000000Z\r\nRECURRENCE-ID:20300513T090000Z\r\n" +
	"DTSTART:20300513T150000Z\r\nDTEND:20300513T160000Z\r\nSUMMARY:Moved sync\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func seedSeries(t *testing.T, e *agendaEnv) string {
	t.Helper()
	guid, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Weekly sync", CalendarId: "default", StartTime: utcUnix(2030, 5, 6, 9, 0), EndTime: utcUnix(2030, 5, 6, 10, 0),
		Rrule: "FREQ=WEEKLY;BYDAY=MO", IcalRaw: []byte(seriesICS),
	})
	if err != nil {
		t.Fatal(err)
	}
	return guid
}

func (e *agendaEnv) show(t *testing.T, guid, query string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/calendar/event/" + url.PathEscape(guid) + query
	w := httptest.NewRecorder()
	e.h.ShowEvent(w, withGUID(withUserContext(httptest.NewRequest(http.MethodGet, target, nil), quotaUser, quotaEmail), guid))
	return w
}

// The PRD suspected that the instance link the views rendered
// ({guid}_{date}) returned 404 because ShowEvent looks the key up exactly.
// This test recorded that on main (404, "want 302") before the fix.
func TestShowEventOldInstanceLink(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	guid := seedSeries(t, e)
	single, err := e.events.Create(quotaUser, &bongov1.Event{Title: "One", CalendarId: "default", StartTime: utcUnix(2030, 5, 6, 9, 0), EndTime: utcUnix(2030, 5, 6, 10, 0)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key string
		want      int
		location  string
	}{
		{"instance date", guid + "_2030-05-27", http.StatusFound, "/calendar/event/" + guid + "?occurrence=20300527T090000Z"},
		{"the first instance", guid + "_2030-05-06", http.StatusFound, "/calendar/event/" + guid + "?occurrence=20300506T090000Z"},
		{"an override is found by the day it is on", guid + "_2030-05-13", http.StatusFound, "/calendar/event/" + guid + "?occurrence=20300513T090000Z"},
		{"excluded day", guid + "_2030-05-20", http.StatusNotFound, ""},
		{"day with no instance", guid + "_2030-05-14", http.StatusNotFound, ""},
		{"not a series", single + "_2030-05-06", http.StatusNotFound, ""},
		{"unknown series", "nope_2030-05-06", http.StatusNotFound, ""},
		{"not a date", guid + "_2030-13-45", http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := e.show(t, tc.key, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if got := w.Header().Get("Location"); got != tc.location {
				t.Errorf("Location = %q, want %q", got, tc.location)
			}
		})
	}
}

// A user cannot reach another user's series through the old link.
func TestShowEventOldInstanceLinkIsScopedToCaller(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	guid, err := e.events.Create("someone-else", &bongov1.Event{
		Title: "Theirs", CalendarId: "default", StartTime: utcUnix(2030, 5, 6, 9, 0), EndTime: utcUnix(2030, 5, 6, 10, 0), Rrule: "FREQ=WEEKLY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := e.show(t, guid+"_2030-05-13", ""); w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestShowEventOccurrence(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	guid := seedSeries(t, e)
	for _, tc := range []struct {
		name, rid string
		want      int
		contains  string
	}{
		{"plain instance", "20300527T090000Z", http.StatusOK, "May 27, 2030 09:00"},
		{"override shows its own time", "20300513T090000Z", http.StatusOK, "May 13, 2030 15:00"},
		{"override shows its own title", "20300513T090000Z", http.StatusOK, "Moved sync"},
		{"excluded instance", "20300520T090000Z", http.StatusNotFound, ""},
		{"not on the rule", "20300514T090000Z", http.StatusNotFound, ""},
		{"garbage", "x%22%3E%3Cscript%3E", http.StatusNotFound, ""},
		{"a date for a timed series", "20300527", http.StatusNotFound, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := e.show(t, guid, "?occurrence="+tc.rid)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if !strings.Contains(w.Body.String(), tc.contains) {
				t.Errorf("page lacks %q", tc.contains)
			}
		})
	}

	// The form is bound to the series: it carries the master's start, not the
	// occurrence's, so saving it cannot move the series to the occurrence date.
	body := e.show(t, guid, "?occurrence=20300527T090000Z").Body.String()
	if got := inputValue(body, "start_date"); got != "2030-05-06" {
		t.Errorf("start_date = %q, want the series start 2030-05-06", got)
	}
	if !strings.Contains(body, `action="/calendar/event/`+guid+`"`) {
		t.Error("the form does not post to the series")
	}
}

func TestShowEventOccurrenceAllDay(t *testing.T) {
	e := newAgendaEnv(t, "Pacific/Auckland")
	guid, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Holiday", CalendarId: "default", AllDay: true, StartTime: utcUnix(2030, 5, 6, 0, 0), EndTime: utcUnix(2030, 5, 7, 0, 0), Rrule: "FREQ=DAILY;COUNT=3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := e.show(t, guid, "?occurrence=20300507"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "May 7, 2030") {
		t.Errorf("all-day occurrence: status %d", w.Code)
	}
	if w := e.show(t, guid, "?occurrence=20300509"); w.Code != http.StatusNotFound {
		t.Errorf("past COUNT: status %d, want 404", w.Code)
	}
	if w := e.show(t, guid, "?occurrence=20300507T000000Z"); w.Code != http.StatusNotFound {
		t.Errorf("a date-time for an all-day series: status %d, want 404", w.Code)
	}
}

var (
	occurrenceLinkRe = regexp.MustCompile(`href="(/calendar/event/[^"]*)"`)
	dataGuidRe       = regexp.MustCompile(`data-guid="`)
)

// Views link each instance with ?occurrence=, distinct per instance, and
// offer no drag handle for a repeating event.
func TestViewsLinkOccurrencesAndDoNotDragSeries(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	guid := seedSeries(t, e)
	single, err := e.events.Create(quotaUser, &bongov1.Event{Title: "One", CalendarId: "default", StartTime: utcUnix(2030, 5, 7, 9, 0), EndTime: utcUnix(2030, 5, 7, 10, 0)})
	if err != nil {
		t.Fatal(err)
	}

	month := e.view(t, e.h.MonthView, "/calendar/2030/5", map[string]string{"year": "2030", "month": "5"})
	got := map[string]bool{}
	for _, m := range occurrenceLinkRe.FindAllStringSubmatch(month, -1) {
		got[m[1]] = true
	}
	for _, want := range []string{
		"/calendar/event/" + guid + "?occurrence=20300506T090000Z",
		"/calendar/event/" + guid + "?occurrence=20300513T090000Z",
		"/calendar/event/" + guid + "?occurrence=20300527T090000Z",
		"/calendar/event/" + single,
	} {
		if !got[strings.ReplaceAll(want, "&", "&amp;")] && !got[want] {
			t.Errorf("month view lacks link %s (has %v)", want, got)
		}
	}
	if got["/calendar/event/"+guid+"?occurrence=20300520T090000Z"] {
		t.Error("the excluded instance is linked")
	}

	week := e.view(t, e.h.WeekView, "/calendar/week/2030/19", map[string]string{"year": "2030", "week": "19"})
	if n := len(dataGuidRe.FindAllString(week, -1)); n != 1 {
		t.Errorf("week view has %d draggable blocks, want only the single event", n)
	}
	if !strings.Contains(week, `data-guid="`+single+`"`) {
		t.Error("the single event lost its drag handle")
	}
	if strings.Contains(week, `data-guid="`+guid+`"`) {
		t.Error("a repeating event is draggable")
	}
}

func TestRescheduleRefusesSeries(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	guid := seedSeries(t, e)
	before, _ := e.events.Get(quotaUser, guid)
	form := url.Values{"start_time": {"1904000000"}, "end_time": {"1904003777"}}
	w := httptest.NewRecorder()
	e.h.RescheduleEvent(w, withGUID(formReq(http.MethodPost, "/calendar/event/"+guid+"/reschedule", form), guid))
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	after, _ := e.events.Get(quotaUser, guid)
	if after.StartTime != before.StartTime {
		t.Error("the series start moved")
	}
}

// The form no longer trusts a posted timezone: it reaches TZID output.
func TestCreateEventStampsUserZoneAndIgnoresPostedZone(t *testing.T) {
	e := newAgendaEnv(t, "Europe/Amsterdam")
	form := url.Values{
		"title": {"Weekly"}, "calendar_id": {"default"}, "reminder_minutes": {"0"}, "rrule": {"FREQ=WEEKLY"},
		"start_date": {"2030-05-06"}, "start_time": {"09:00"}, "end_date": {"2030-05-06"}, "end_time": {"10:00"},
		"timezone": {"Europe/Amsterdam\r\nX-EVIL:1"},
	}
	w := httptest.NewRecorder()
	e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	all, _, _ := e.events.ListAll(quotaUser, 0, 0)
	if len(all) != 1 || all[0].Timezone != "Europe/Amsterdam" {
		t.Fatalf("timezone = %+v", all)
	}

	form.Set("all_day", "on")
	form.Set("title", "Day")
	w = httptest.NewRecorder()
	e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	all, _, _ = e.events.ListAll(quotaUser, 0, 0)
	for _, ev := range all {
		if ev.Title == "Day" && ev.Timezone != "" {
			t.Errorf("an all-day event got zone %q", ev.Timezone)
		}
	}

	// No preference zone worth recording: UTC and the process zone store none.
	u := newAgendaEnv(t, "UTC")
	w = httptest.NewRecorder()
	form.Del("all_day")
	u.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	all, _, _ = u.events.ListAll(quotaUser, 0, 0)
	if len(all) != 1 || all[0].Timezone != "" {
		t.Errorf("UTC user stored zone %+v", all)
	}
}

// A web-created weekly series round-trips through export and CalDAV with its
// RRULE, EXDATE and VTIMEZONE, and re-expands at 09:00 local on both sides of
// the DST change.
func TestWebSeriesExportAndCalDAV(t *testing.T) {
	e := newAgendaEnv(t, "Europe/Amsterdam")
	form := url.Values{
		"title": {"Weekly"}, "calendar_id": {"default"}, "reminder_minutes": {"0"}, "rrule": {"FREQ=WEEKLY"},
		"start_date": {"2030-10-14"}, "start_time": {"09:00"}, "end_date": {"2030-10-14"}, "end_time": {"10:00"},
	}
	w := httptest.NewRecorder()
	e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", w.Code)
	}
	all, _, _ := e.events.ListAll(quotaUser, 0, 0)
	ev := all[0]
	ev.Exdates = []string{"20301028T090000"}
	if err := e.events.Update(quotaUser, ev.Guid, ev); err != nil {
		t.Fatal(err)
	}

	exp := httptest.NewRecorder()
	e.h.ExportCalendar(exp, withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/export", nil), quotaUser, quotaEmail))
	obj, err := e.backend.GetCalendarObject(caldav.WithUserID(context.Background(), quotaUser), "/caldav/"+quotaUser+"/calendars/default/"+ev.Guid+".ics", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, cal := range map[string]*ical.Calendar{
		"export": mustDecode(t, exp.Body.String()),
		"caldav": obj.Data,
	} {
		t.Run(name, func(t *testing.T) {
			assertSeriesCalendar(t, cal)
		})
	}
}

func mustDecode(t *testing.T, s string) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(strings.NewReader(s)).Decode()
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, s)
	}
	return cal
}

func assertSeriesCalendar(t *testing.T, cal *ical.Calendar) {
	t.Helper()
	var vevent, vtz *ical.Component
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompEvent:
			vevent = c
		case ical.CompTimezone:
			vtz = c
		}
	}
	if vevent == nil || vtz == nil {
		t.Fatalf("want a VEVENT and a VTIMEZONE, got %d children", len(cal.Children))
	}
	if got := vevent.Props.Get(ical.PropRecurrenceRule); got == nil || got.Value != "FREQ=WEEKLY" {
		t.Errorf("RRULE = %+v", got)
	}
	ex := vevent.Props.Get(ical.PropExceptionDates)
	if ex == nil || ex.Value != "20301028T090000" || ex.Params.Get("TZID") != "Europe/Amsterdam" {
		t.Errorf("EXDATE = %+v", ex)
	}
	dt := vevent.Props.Get(ical.PropDateTimeStart)
	if dt.Params.Get("TZID") != "Europe/Amsterdam" || dt.Value != "20301014T090000" {
		t.Errorf("DTSTART = %q TZID=%q", dt.Value, dt.Params.Get("TZID"))
	}
	if got := vtz.Props.Get(ical.PropTimezoneID).Value; got != dt.Params.Get("TZID") {
		t.Errorf("VTIMEZONE TZID %q does not match DTSTART's %q", got, dt.Params.Get("TZID"))
	}
	if len(vtz.Children) == 0 {
		t.Error("VTIMEZONE has no observances")
	}

	// A client (or a restore) that takes this object as ical_raw expands it at
	// 09:00 local on both sides of the 2030-10-27 change, minus the EXDATE.
	var buf strings.Builder
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatal(err)
	}
	ev, _ := icalevent.ToEvent(cal)
	ev.Guid, ev.IcalRaw = "rt", []byte(buf.String())
	ams := mustZone(t, "Europe/Amsterdam")
	from := time.Date(2030, 10, 10, 0, 0, 0, 0, ams).Unix()
	got, err := store.ExpandRecurrence(ev, from, time.Date(2030, 11, 20, 0, 0, 0, 0, ams).Unix())
	if err != nil {
		t.Fatal(err)
	}
	var days []string
	for _, o := range got {
		l := time.Unix(o.StartTime, 0).In(ams)
		if l.Hour() != 9 || l.Minute() != 0 {
			t.Errorf("%s is %s local, want 09:00", o.RecurrenceId, l.Format("15:04"))
		}
		days = append(days, l.Format("01-02"))
	}
	if want := "10-14 10-21 11-04 11-11 11-18"; strings.Join(days, " ") != want {
		t.Errorf("days = %v, want %s (28 Oct excluded)", days, want)
	}
}

// Free/busy reads the same expansion: an EXDATE leaves no busy block that
// day and an override moves the block to its new time.
func TestFreeBusyHonoursExdateAndOverride(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	seedSeries(t, e)
	busy := func(from, to int64) string {
		target := "/calendar/freebusy?user=" + quotaUser + "&start=" + strconv.FormatInt(from, 10) + "&end=" + strconv.FormatInt(to, 10)
		w := httptest.NewRecorder()
		e.h.FreeBusy(w, withUserContext(httptest.NewRequest(http.MethodGet, target, nil), quotaUser, quotaEmail))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}
	if body := busy(utcUnix(2030, 5, 20, 0, 0), utcUnix(2030, 5, 21, 0, 0)); strings.Contains(body, `"start"`) {
		t.Errorf("the excluded day is busy: %s", body)
	}
	if body := busy(utcUnix(2030, 5, 13, 8, 0), utcUnix(2030, 5, 13, 10, 0)); strings.Contains(body, `"start"`) {
		t.Errorf("the original slot of the moved instance is busy: %s", body)
	}
	want := `"start":` + strconv.FormatInt(utcUnix(2030, 5, 13, 15, 0), 10)
	if body := busy(utcUnix(2030, 5, 13, 0, 0), utcUnix(2030, 5, 14, 0, 0)); !strings.Contains(body, want) {
		t.Errorf("the moved block is missing (%s): %s", want, body)
	}
}

// One VTIMEZONE per zone in an export, however many series use it.
func TestExportWritesEachSeriesZoneOnce(t *testing.T) {
	e := newAgendaEnv(t, "Europe/Amsterdam")
	for _, title := range []string{"A", "B"} {
		if _, err := e.events.Create(quotaUser, &bongov1.Event{
			Title: title, CalendarId: "default", StartTime: utcUnix(2030, 5, 6, 7, 0), EndTime: utcUnix(2030, 5, 6, 8, 0),
			Rrule: "FREQ=DAILY", Timezone: "Europe/Amsterdam",
		}); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	e.h.ExportCalendar(w, withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/export", nil), quotaUser, quotaEmail))
	if n := strings.Count(w.Body.String(), "BEGIN:VTIMEZONE"); n != 1 {
		t.Errorf("%d VTIMEZONE definitions, want 1", n)
	}
	if n := strings.Count(w.Body.String(), "RRULE:FREQ=DAILY"); n != 2 {
		t.Errorf("%d RRULE lines, want 2", n)
	}
}

// An old link names the date of the occurrence's RECURRENCE-ID: an overnight
// instance of the day before, listed first, is not the one meant.
func TestShowEventOldInstanceLinkSkipsPreviousDayOvernight(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	guid, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Night job", CalendarId: "default", StartTime: utcUnix(2030, 5, 6, 23, 0), EndTime: utcUnix(2030, 5, 7, 2, 0),
		Rrule: "FREQ=DAILY",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := e.show(t, guid+"_2030-05-14", "")
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got, want := w.Header().Get("Location"), "/calendar/event/"+guid+"?occurrence=20300514T230000Z"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}

// An override that moved its instance to another day is not listed on the
// original date, but a link for that date still reaches it.
func TestShowEventOldInstanceLinkFindsMovedOverride(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//C//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:s2\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z\r\n" +
		"SUMMARY:Weekly\r\nRRULE:FREQ=WEEKLY;BYDAY=MO\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:s2\r\nDTSTAMP:20300101T000000Z\r\nRECURRENCE-ID:20300513T090000Z\r\n" +
		"DTSTART:20300514T150000Z\r\nDTEND:20300514T160000Z\r\nSUMMARY:Next day\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:s2\r\nDTSTAMP:20300101T000000Z\r\nRECURRENCE-ID:20300520T090000Z\r\n" +
		"STATUS:CANCELLED\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	guid, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Weekly", CalendarId: "default", StartTime: utcUnix(2030, 5, 6, 9, 0), EndTime: utcUnix(2030, 5, 6, 10, 0),
		Rrule: "FREQ=WEEKLY;BYDAY=MO", IcalRaw: []byte(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := e.show(t, guid+"_2030-05-13", "")
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got, want := w.Header().Get("Location"), "/calendar/event/"+guid+"?occurrence=20300513T090000Z"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	// A cancelled instance stays a 404 on the fallback path.
	if w := e.show(t, guid+"_2030-05-20", ""); w.Code != http.StatusNotFound {
		t.Errorf("cancelled instance: status = %d, want 404", w.Code)
	}
}
