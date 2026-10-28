package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/caldav"
	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// allDayZones are the process zones every all-day case runs under. The tests
// that use them change time.Local, which is global, so they must not call
// t.Parallel. Top-level parallel tests only start after the sequential ones
// finish, so no other test observes the changed zone.
var allDayZones = []string{"America/New_York", "Asia/Tokyo", "UTC"}

func setLocalZone(t *testing.T, name string) {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zone %s unavailable: %v", name, err)
	}
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
}

const zoneTitle = "Zonetitle"

type allDayEnv struct {
	*quotaEnv
	h       *CalendarHandler
	backend *caldav.Backend
}

func newAllDayEnv(t *testing.T) *allDayEnv {
	t.Helper()
	env := newQuotaEnv(t)
	return &allDayEnv{
		quotaEnv: env,
		h:        NewCalendarHandler(env.events, env.calendars, nil),
		backend:  caldav.NewBackend(env.events, env.calendars, discardLogger()),
	}
}

// caldavPutAllDay stores an all-day event the way a CalDAV client does.
func (e *allDayEnv) caldavPutAllDay(t *testing.T, guid, dtstart, dtend string) {
	t.Helper()
	body := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//C//EN\r\nBEGIN:VEVENT\r\nUID:" + guid +
		"\r\nDTSTAMP:20261001T000000Z\r\n" + dtstart + "\r\n" + dtend + "\r\nSUMMARY:" + zoneTitle +
		"\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	cal, err := ical.NewDecoder(strings.NewReader(body)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	ctx := caldav.WithUserID(context.Background(), quotaUser)
	if _, err := e.backend.PutCalendarObject(ctx, "/caldav/"+quotaUser+"/calendars/default/"+guid+".ics", cal, nil); err != nil {
		t.Fatalf("PUT: %v", err)
	}
}

func (e *allDayEnv) body(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func (e *allDayEnv) editForm(t *testing.T, guid string) string {
	t.Helper()
	w := httptest.NewRecorder()
	e.h.ShowEvent(w, withGUID(withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/event/"+guid, nil), quotaUser, quotaEmail), guid))
	return e.body(t, w)
}

func (e *allDayEnv) view(t *testing.T, call func(http.ResponseWriter, *http.Request), target string, params map[string]string) string {
	t.Helper()
	req := withUserContext(httptest.NewRequest(http.MethodGet, target, nil), quotaUser, quotaEmail)
	req = withURLParams(req, params)
	w := httptest.NewRecorder()
	call(w, req)
	return e.body(t, w)
}

// dayCell returns the HTML of the month-grid cell labelled label.
func dayCell(t *testing.T, html, label string) string {
	t.Helper()
	marker := `aria-label="` + label + `"`
	i := strings.Index(html, marker)
	if i < 0 {
		t.Fatalf("no cell %q in month view", label)
	}
	rest := html[i+len(marker):]
	if j := strings.Index(rest, `aria-label="`); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func inputValue(html, name string) string {
	i := strings.Index(html, `name="`+name+`"`)
	if i < 0 {
		return ""
	}
	rest := html[i:]
	k := strings.Index(rest, `value="`)
	if k < 0 {
		return ""
	}
	rest = rest[k+len(`value="`):]
	return rest[:strings.Index(rest, `"`)]
}

func TestAllDayCalDAVEventKeepsDateInAnyZone(t *testing.T) {
	for _, zone := range allDayZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			e.caldavPutAllDay(t, "cd1", "DTSTART;VALUE=DATE:20261105", "DTEND;VALUE=DATE:20261106")

			if got := inputValue(e.editForm(t, "cd1"), "start_date"); got != "2026-11-05" {
				t.Errorf("start_date = %q, want 2026-11-05", got)
			}
			if got := inputValue(e.editForm(t, "cd1"), "end_date"); got != "2026-11-05" {
				t.Errorf("end_date = %q, want 2026-11-05", got)
			}

			// A title-only edit: the browser posts the dates the form showed.
			form := url.Values{
				"title": {"Renamed"}, "calendar_id": {"default"}, "all_day": {"on"},
				"start_date": {"2026-11-05"}, "end_date": {"2026-11-05"},
				"start_time": {"09:00"}, "end_time": {"10:00"}, "reminder_minutes": {"0"},
			}
			if w := putEdit(t, e.h, "cd1", form); w.Code >= 400 {
				t.Fatalf("edit status = %d: %s", w.Code, w.Body.String())
			}
			ev, cal := decodeStored(t, e.quotaEnv, "cd1")
			if ev.Title != "Renamed" {
				t.Fatalf("title = %q", ev.Title)
			}
			vev := cal.Events()[0]
			s, en := vev.Props.Get(ical.PropDateTimeStart), vev.Props.Get(ical.PropDateTimeEnd)
			if s.Value != "20261105" || s.Params.Get(ical.ParamValue) != "DATE" || en.Value != "20261106" {
				t.Errorf("DTSTART=%v DTEND=%v, want 20261105/20261106 untouched", s, en)
			}
			if ev.StartTime != utcUnix(2026, 11, 5, 0, 0) || ev.EndTime != utcUnix(2026, 11, 6, 0, 0) {
				t.Errorf("stored times = %d..%d, want UTC midnights of 5 and 6 November", ev.StartTime, ev.EndTime)
			}
		})
	}
}

// A title-only edit must not touch the time properties at all: an event that
// carries DURATION instead of DTEND keeps it, which a rewrite would replace.
func TestAllDayTitleOnlyEditLeavesTimePropertiesAlone(t *testing.T) {
	for _, zone := range allDayZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			e.caldavPutAllDay(t, "cd3", "DTSTART;VALUE=DATE:20261105", "DURATION:P1D")
			form := url.Values{
				"title": {"Renamed"}, "calendar_id": {"default"}, "all_day": {"on"},
				"start_date":       {inputValue(e.editForm(t, "cd3"), "start_date")},
				"end_date":         {inputValue(e.editForm(t, "cd3"), "end_date")},
				"reminder_minutes": {"0"},
			}
			if w := putEdit(t, e.h, "cd3", form); w.Code >= 400 {
				t.Fatalf("edit status = %d: %s", w.Code, w.Body.String())
			}
			_, cal := decodeStored(t, e.quotaEnv, "cd3")
			vev := cal.Events()[0]
			if vev.Props.Get(ical.PropDuration) == nil || vev.Props.Get(ical.PropDateTimeEnd) != nil {
				t.Errorf("DURATION/DTEND were rewritten:\n%s", func() string {
					var b bytes.Buffer
					_ = ical.NewEncoder(&b).Encode(cal)
					return b.String()
				}())
			}
		})
	}
}

func TestAllDayCalDAVDateEditMovesInUTC(t *testing.T) {
	for _, zone := range allDayZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			e.caldavPutAllDay(t, "cd2", "DTSTART;VALUE=DATE:20261105", "DTEND;VALUE=DATE:20261106")
			form := url.Values{
				"title": {zoneTitle}, "calendar_id": {"default"}, "all_day": {"on"},
				"start_date": {"2026-11-09"}, "end_date": {"2026-11-10"}, "reminder_minutes": {"0"},
			}
			if w := putEdit(t, e.h, "cd2", form); w.Code >= 400 {
				t.Fatalf("edit status = %d: %s", w.Code, w.Body.String())
			}
			_, cal := decodeStored(t, e.quotaEnv, "cd2")
			vev := cal.Events()[0]
			if got := vev.Props.Get(ical.PropDateTimeStart).Value; got != "20261109" {
				t.Errorf("DTSTART = %s, want 20261109", got)
			}
			if got := vev.Props.Get(ical.PropDateTimeEnd).Value; got != "20261111" {
				t.Errorf("DTEND = %s, want 20261111", got)
			}
		})
	}
}

func TestAllDayWebCreatedEventInAnyZone(t *testing.T) {
	for _, zone := range allDayZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			form := url.Values{
				"title": {zoneTitle}, "calendar_id": {"default"}, "all_day": {"on"},
				"start_date": {"2026-11-05"}, "end_date": {"2026-11-05"}, "reminder_minutes": {"0"},
			}
			w := httptest.NewRecorder()
			e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("create status = %d: %s", w.Code, w.Body.String())
			}
			if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/calendar/2026/11") {
				t.Errorf("redirect = %q, want the November page", loc)
			}
			all, _, err := e.events.ListAll(quotaUser, 0, 0)
			if err != nil || len(all) != 1 {
				t.Fatalf("events = %d, %v", len(all), err)
			}
			ev := all[0]
			if ev.StartTime != utcUnix(2026, 11, 5, 0, 0) || ev.EndTime != utcUnix(2026, 11, 6, 0, 0) {
				t.Errorf("stored %d..%d, want UTC midnights of 5 and 6 November", ev.StartTime, ev.EndTime)
			}

			// The edit form shows the date that was entered.
			if got := inputValue(e.editForm(t, ev.Guid), "start_date"); got != "2026-11-05" {
				t.Errorf("start_date = %q", got)
			}

			// CalDAV GET (no ical_raw, so serialised from the proto).
			ctx := caldav.WithUserID(context.Background(), quotaUser)
			obj, err := e.backend.GetCalendarObject(ctx, "/caldav/"+quotaUser+"/calendars/default/"+ev.Guid+".ics", nil)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := ical.NewEncoder(&buf).Encode(obj.Data); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"DTSTART;VALUE=DATE:20261105", "DTEND;VALUE=DATE:20261106"} {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("CalDAV object lacks %s:\n%s", want, buf.String())
				}
			}

			// The .ics export.
			ew := httptest.NewRecorder()
			e.h.ExportCalendar(ew, withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/export", nil), quotaUser, quotaEmail))
			for _, want := range []string{"DTSTART;VALUE=DATE:20261105", "DTEND;VALUE=DATE:20261106"} {
				if !strings.Contains(ew.Body.String(), want) {
					t.Errorf("export lacks %s:\n%s", want, ew.Body.String())
				}
			}

			assertAllDayViews(t, e)
		})
	}
}

// assertAllDayViews checks the all-day event appears on 5 November and not on
// the days either side, in the month and day views.
func assertAllDayViews(t *testing.T, e *allDayEnv) {
	t.Helper()
	month := e.view(t, e.h.MonthView, "/calendar/2026/11", map[string]string{"year": "2026", "month": "11"})
	for label, want := range map[string]bool{
		"Wednesday, November 4": false, "Thursday, November 5": true, "Friday, November 6": false,
	} {
		if got := strings.Contains(dayCell(t, month, label), zoneTitle); got != want {
			t.Errorf("month cell %q has event = %v, want %v", label, got, want)
		}
	}
	for day, want := range map[string]bool{"4": false, "5": true, "6": false} {
		html := e.view(t, e.h.DayView, "/calendar/2026/11/"+day, map[string]string{"year": "2026", "month": "11", "day": day})
		if got := strings.Contains(html, zoneTitle); got != want {
			t.Errorf("day view of 2026-11-%s has event = %v, want %v", day, got, want)
		}
	}
	all, _, err := e.events.ListAll(quotaUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range buildWeekColumns(isoWeekMonday(2026, 45, time.Local), all, nil, time.Now()) {
		if got, want := len(col.AllDay), map[bool]int{true: 1, false: 0}[col.DateStr == "2026-11-05"]; got != want {
			t.Errorf("week column %s holds %d all-day events, want %d", col.DateStr, got, want)
		}
	}
	week := e.view(t, e.h.WeekView, "/calendar/week/2026/45", map[string]string{"year": "2026", "week": "45"})
	if !strings.Contains(week, zoneTitle) {
		t.Error("week 45 (2-8 November) lacks the event")
	}
}

func TestAllDayCreateRedirectUsesEventDate(t *testing.T) {
	for _, zone := range allDayZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			form := url.Values{
				"title": {zoneTitle}, "calendar_id": {"default"}, "all_day": {"on"},
				"start_date": {"2026-12-01"}, "end_date": {"2026-12-01"}, "reminder_minutes": {"0"},
			}
			w := httptest.NewRecorder()
			e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
			if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/calendar/2026/12") {
				t.Errorf("redirect = %q, want the December page", loc)
			}
		})
	}
}

// Legacy records written by a non-UTC server hold local midnight and no
// ical_raw. Every zone between UTC-11 and UTC+11 maps back to its date.
func TestAllDayLegacyLocalMidnightKeepsDate(t *testing.T) {
	for _, zone := range allDayZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			start := time.Date(2026, 11, 5, 0, 0, 0, 0, time.Local).Unix()
			end := time.Date(2026, 11, 6, 0, 0, 0, 0, time.Local).Unix()
			guid, err := e.events.Create(quotaUser, &bongov1.Event{
				Title: zoneTitle, CalendarId: "default", AllDay: true, StartTime: start, EndTime: end,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := inputValue(e.editForm(t, guid), "start_date"); got != "2026-11-05" {
				t.Errorf("start_date = %q, want 2026-11-05", got)
			}
			assertAllDayViews(t, e)
		})
	}
}

func TestAllDayDate(t *testing.T) {
	for _, tt := range []struct {
		name string
		unix int64
		want string
	}{
		{"utc midnight", utcUnix(2026, 11, 5, 0, 0), "2026-11-05"},
		{"tokyo local midnight", time.Date(2026, 11, 5, 0, 0, 0, 0, time.FixedZone("JST", 9*3600)).Unix(), "2026-11-05"},
		{"new york local midnight", time.Date(2026, 11, 5, 0, 0, 0, 0, time.FixedZone("EST", -5*3600)).Unix(), "2026-11-05"},
	} {
		if got := icalevent.AllDayDate(tt.unix).Format("2006-01-02"); got != tt.want {
			t.Errorf("%s: %s, want %s", tt.name, got, tt.want)
		}
	}
}

func withURLParams(req *http.Request, params map[string]string) *http.Request {
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
}

// A deleted all-day event redirects to the month of its date, not the month
// its UTC midnight falls in locally.
func TestAllDayDeleteRedirectUsesEventDate(t *testing.T) {
	for _, zone := range allDayZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			guid, err := e.events.Create(quotaUser, &bongov1.Event{
				Title: zoneTitle, CalendarId: "default", AllDay: true,
				StartTime: utcUnix(2026, 12, 1, 0, 0), EndTime: utcUnix(2026, 12, 2, 0, 0),
			})
			if err != nil {
				t.Fatal(err)
			}
			req := withGUID(withUserContext(httptest.NewRequest(http.MethodPost, "/calendar/event/"+guid+"/delete", nil), quotaUser, quotaEmail), guid)
			w := httptest.NewRecorder()
			e.h.DeleteEvent(w, req)
			if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/calendar/2026/12") {
				t.Errorf("redirect = %q, want the December page", loc)
			}
		})
	}
}

// Search results list an all-day event on its own date in any server zone.
func TestAllDaySearchResultsShowEventDate(t *testing.T) {
	for _, zone := range allDayZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			ev := &bongov1.Event{
				Guid: "g1", Title: zoneTitle, AllDay: true,
				StartTime: utcUnix(2026, 11, 5, 0, 0), EndTime: utcUnix(2026, 11, 6, 0, 0),
			}
			var buf bytes.Buffer
			if err := templates.CalendarSearchResults([]*bongov1.Event{ev}, "q", quotaEmail).Render(context.Background(), &buf); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), "Nov 5, 2026") || strings.Contains(buf.String(), "Nov 4, 2026") {
				t.Errorf("search results do not show Nov 5, 2026 for the all-day event")
			}
		})
	}
}

// assertOnlyInCell checks title is rendered in the labelled cell and nowhere
// else in the grid. A cell renders an event's title more than once, so the
// count in the cell is the baseline for the whole page.
func assertOnlyInCell(t *testing.T, html, label, title string) {
	t.Helper()
	n := strings.Count(dayCell(t, html, label), title)
	if n == 0 {
		t.Errorf("%s missing from cell %q", title, label)
	} else if total := strings.Count(html, title); total != n {
		t.Errorf("%s appears %d times in the page, %d of them in cell %q", title, total, n, label)
	}
}

// monthEdgeZones are the process zones for the month-grid edge cases: the
// furthest east, UTC and the furthest west.
var monthEdgeZones = []string{"Pacific/Kiritimati", "UTC", "Etc/GMT+12"}

// All-day events on the first and last day of a month, and on the neighbouring
// months' edge days shown in the grid's leading and trailing cells, sit in
// their own cell and no other, whatever the server zone.
func TestMonthViewAllDayEdges(t *testing.T) {
	// November 2026: 1 November is a Sunday, so the grid starts Monday 26
	// October and ends Sunday 6 December (6 weeks). 30 October is a Friday.
	cases := []struct{ title, date, label string }{
		{"Prevmonth", "2026-10-31", "Saturday, October 31"},
		{"Firstday", "2026-11-01", "Sunday, November 1"},
		{"Lastday", "2026-11-30", "Monday, November 30"},
		{"Nextmonth", "2026-12-01", "Tuesday, December 1"},
	}
	for _, zone := range monthEdgeZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			for _, c := range cases {
				form := url.Values{
					"title": {c.title}, "calendar_id": {"default"}, "all_day": {"on"},
					"start_date": {c.date}, "end_date": {c.date}, "reminder_minutes": {"0"},
				}
				w := httptest.NewRecorder()
				e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
				if w.Code != http.StatusSeeOther {
					t.Fatalf("create %s: %d %s", c.title, w.Code, w.Body.String())
				}
			}
			html := e.view(t, e.h.MonthView, "/calendar/2026/11", map[string]string{"year": "2026", "month": "11"})
			for _, c := range cases {
				assertOnlyInCell(t, html, c.label, c.title)
			}
		})
	}
}

// A timed event near local midnight stays on the server-zone date.
func TestMonthViewTimedNearMidnightStaysInServerZone(t *testing.T) {
	for _, zone := range monthEdgeZones {
		t.Run(zone, func(t *testing.T) {
			setLocalZone(t, zone)
			e := newAllDayEnv(t)
			for title, hm := range map[string][2]int{"Latenight": {23, 37}, "Earlybird": {0, 23}} {
				start := time.Date(2026, 11, 10, hm[0], hm[1], 0, 0, time.Local)
				if _, err := e.events.Create(quotaUser, &bongov1.Event{
					Title: title, CalendarId: "default",
					StartTime: start.Unix(), EndTime: start.Add(20 * time.Minute).Unix(),
				}); err != nil {
					t.Fatal(err)
				}
			}
			html := e.view(t, e.h.MonthView, "/calendar/2026/11", map[string]string{"year": "2026", "month": "11"})
			for _, title := range []string{"Latenight", "Earlybird"} {
				assertOnlyInCell(t, html, "Tuesday, November 10", title)
			}
		})
	}
}

// A zero-length all-day event ends at its own UTC midnight. On the grid's first
// cell in a zone west of UTC that is before the cell's local midnight, so the
// month query must reach a day before the grid to find it.
func TestMonthViewZeroLengthAllDayAtGridEdge(t *testing.T) {
	setLocalZone(t, "Etc/GMT+12")
	e := newAllDayEnv(t)
	// The November 2026 grid starts Monday 26 October.
	d := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if _, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Zerolen", CalendarId: "default", AllDay: true,
		StartTime: d.Unix(), EndTime: d.Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	html := e.view(t, e.h.MonthView, "/calendar/2026/11", map[string]string{"year": "2026", "month": "11"})
	assertOnlyInCell(t, html, "Monday, October 26", "Zerolen")
}
