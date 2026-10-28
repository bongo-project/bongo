package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fixedZone is the zoneSource the agenda tests use, so no test depends on
// the process zone.
type fixedZone struct{ loc *time.Location }

func (z fixedZone) Location(string) *time.Location { return z.loc }

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zone %s unavailable: %v", name, err)
	}
	return loc
}

func utc(y int, m time.Month, d, hh, mm int) int64 {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC).Unix()
}

func timedEvent(title string, start int64) *bongov1.Event {
	return &bongov1.Event{Guid: title, Title: title, CalendarId: "default", StartTime: start, EndTime: start + 3600}
}

func allDayEvent(title string, y int, m time.Month, d int) *bongov1.Event {
	s := utc(y, m, d, 0, 0)
	return &bongov1.Event{Guid: title, Title: title, CalendarId: "default", StartTime: s, EndTime: s + 86400, AllDay: true}
}

func TestBuildAgendaOrderingAndGrouping(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []*bongov1.Event{
		timedEvent("late", utc(2026, 1, 5, 15, 0)),
		timedEvent("next-day", utc(2026, 1, 6, 9, 0)),
		timedEvent("early", utc(2026, 1, 5, 8, 0)),
		allDayEvent("holiday", 2026, 1, 5),
	}
	days := buildAgenda(from, agendaRangeDays, events, nil, time.UTC)
	if len(days) != 2 {
		t.Fatalf("groups = %d, want 2", len(days))
	}
	if got := days[0].Date.Format("2006-01-02"); got != "2026-01-05" {
		t.Errorf("first group = %s", got)
	}
	var order []string
	for _, it := range days[0].Items {
		order = append(order, it.Event.Title)
	}
	if strings.Join(order, ",") != "holiday,early,late" {
		t.Errorf("order = %v, want holiday,early,late", order)
	}
	if len(days[1].Items) != 1 || days[1].Items[0].Event.Title != "next-day" {
		t.Errorf("second group = %+v", days[1].Items)
	}
}

func TestBuildAgendaMidnightEdge(t *testing.T) {
	cases := []struct {
		zone     string
		title    string
		ev       *bongov1.Event
		wantDate string
		wantTime string
	}{
		{"Europe/Berlin", "timed", timedEvent("timed", utc(2026, 1, 10, 23, 30)), "2026-01-11", "00:30"},
		{"UTC", "timed", timedEvent("timed", utc(2026, 1, 10, 23, 30)), "2026-01-10", "23:30"},
		{"Asia/Tokyo", "allday", allDayEvent("allday", 2026, 1, 10), "2026-01-10", ""},
		{"America/New_York", "allday", allDayEvent("allday", 2026, 1, 10), "2026-01-10", ""},
	}
	for _, tc := range cases {
		t.Run(tc.zone+"/"+tc.title, func(t *testing.T) {
			loc := mustZone(t, tc.zone)
			f := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
			days := buildAgenda(f, agendaRangeDays, []*bongov1.Event{tc.ev}, nil, loc)
			if len(days) != 1 {
				t.Fatalf("groups = %d, want 1", len(days))
			}
			if got := days[0].Date.Format("2006-01-02"); got != tc.wantDate {
				t.Errorf("group = %s, want %s", got, tc.wantDate)
			}
			if got := days[0].Items[0].TimeLabel; got != tc.wantTime {
				t.Errorf("time label = %q, want %q", got, tc.wantTime)
			}
		})
	}
}

func TestBuildAgendaDropsOutOfRange(t *testing.T) {
	loc := mustZone(t, "Asia/Tokyo")
	f := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	// Dec 31 all-day overlaps the Tokyo query window but belongs to Dec 31.
	events := []*bongov1.Event{
		allDayEvent("before", 2025, 12, 31),
		allDayEvent("inside", 2026, 1, 1),
		allDayEvent("after", 2026, 1, 31),
	}
	days := buildAgenda(f, agendaRangeDays, events, nil, loc)
	if len(days) != 1 || days[0].Items[0].Event.Title != "inside" {
		t.Fatalf("days = %+v, want only inside", days)
	}
}

// agendaEnv drives the real handler against the real store.
type agendaEnv struct{ *allDayEnv }

func newAgendaEnv(t *testing.T, zone string) *agendaEnv {
	t.Helper()
	e := &agendaEnv{newAllDayEnv(t)}
	e.h.SetZoneSource(fixedZone{mustZone(t, zone)})
	return e
}

func (e *agendaEnv) get(t *testing.T, target string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := withUserContext(httptest.NewRequest(http.MethodGet, target, nil), quotaUser, quotaEmail)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	e.h.AgendaView(w, req)
	return w
}

var (
	agendaSectionRe = regexp.MustCompile(`data-agenda-date="([0-9-]+)"`)
	agendaTitleRe   = regexp.MustCompile(`class="agenda-title"[^>]*>([^<]*)<`)
)

// agendaGroups maps each rendered day to the titles under it, in order.
func agendaGroups(html string) (dates []string, titles map[string][]string) {
	titles = map[string][]string{}
	idx := agendaSectionRe.FindAllStringSubmatchIndex(html, -1)
	for i, m := range idx {
		date := html[m[2]:m[3]]
		end := len(html)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		dates = append(dates, date)
		for _, tm := range agendaTitleRe.FindAllStringSubmatch(html[m[1]:end], -1) {
			titles[date] = append(titles[date], tm[1])
		}
	}
	return dates, titles
}

func TestAgendaViewRecurrenceExpandsWithinRange(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	// Weekly Monday event that began before the range.
	if _, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Standup", CalendarId: "default", Rrule: "FREQ=WEEKLY",
		StartTime: utc(2026, 2, 2, 10, 0), EndTime: utc(2026, 2, 2, 10, 30),
	}); err != nil {
		t.Fatal(err)
	}
	w := e.get(t, "/calendar/agenda?from=2026-03-02", false)
	dates, titles := agendaGroups(e.body(t, w))
	want := []string{"2026-03-02", "2026-03-09", "2026-03-16", "2026-03-23", "2026-03-30"}
	if strings.Join(dates, ",") != strings.Join(want, ",") {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
	for _, d := range dates {
		if len(titles[d]) != 1 || titles[d][0] != "Standup" {
			t.Errorf("%s titles = %v", d, titles[d])
		}
	}
}

func TestAgendaViewZoneEdgeThroughHandler(t *testing.T) {
	cases := []struct {
		zone                        string
		wantTimedDay, wantAllDayDay string
	}{
		{"Europe/Berlin", "2026-01-11", "2026-01-10"},
		{"Asia/Tokyo", "2026-01-11", "2026-01-10"},
		{"America/New_York", "2026-01-10", "2026-01-10"},
	}
	for _, tc := range cases {
		t.Run(tc.zone, func(t *testing.T) {
			e := newAgendaEnv(t, tc.zone)
			for _, ev := range []*bongov1.Event{
				timedEvent("Timed", utc(2026, 1, 10, 23, 30)),
				allDayEvent("AllDay", 2026, 1, 10),
				allDayEvent("PrevDay", 2025, 12, 31),
			} {
				if _, err := e.events.Create(quotaUser, ev); err != nil {
					t.Fatal(err)
				}
			}
			_, titles := agendaGroups(e.body(t, e.get(t, "/calendar/agenda?from=2026-01-01", false)))
			has := func(day, title string) bool {
				for _, x := range titles[day] {
					if x == title {
						return true
					}
				}
				return false
			}
			if !has(tc.wantTimedDay, "Timed") {
				t.Errorf("Timed not under %s: %v", tc.wantTimedDay, titles)
			}
			if !has(tc.wantAllDayDay, "AllDay") {
				t.Errorf("AllDay not under %s: %v", tc.wantAllDayDay, titles)
			}
			for d, ts := range titles {
				for _, x := range ts {
					if x == "PrevDay" {
						t.Errorf("PrevDay leaked into %s", d)
					}
				}
			}
		})
	}
}

func TestAgendaViewPrevNextToday(t *testing.T) {
	e := newAgendaEnv(t, "Europe/Berlin")
	html := e.body(t, e.get(t, "/calendar/agenda?from=2026-03-20", false))
	for _, want := range []string{
		`/calendar/agenda?from=2026-02-18`,
		`/calendar/agenda?from=2026-04-19`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s", want)
		}
	}
	if !strings.Contains(html, `href="/calendar/agenda"`) {
		t.Error("Today link should be /calendar/agenda with no from")
	}
}

func TestAgendaViewBadFrom(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	for _, from := range []string{"garbage", "2026-13-45", "2026-02-30", ""} {
		target := "/calendar/agenda?from=" + from
		w := e.get(t, target, false)
		if from == "" {
			if w.Code != http.StatusOK {
				t.Errorf("empty from: status %d, want 200 (defaults to today)", w.Code)
			}
			continue
		}
		if w.Code != http.StatusBadRequest {
			t.Errorf("from=%q: status %d, want 400", from, w.Code)
		}
	}
}

func TestAgendaViewFullPageAndHTMX(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	full := e.body(t, e.get(t, "/calendar/agenda?from=2026-03-01", false))
	if !strings.Contains(full, "<html") || !strings.Contains(full, `id="main-content"`) {
		t.Error("full page should include the layout and #main-content")
	}
	part := e.body(t, e.get(t, "/calendar/agenda?from=2026-03-01", true))
	if strings.Contains(part, "<html") || strings.Contains(part, `id="main-content"`) {
		t.Error("HTMX response should be the content fragment only")
	}
	if !strings.Contains(part, `id="agenda"`) {
		t.Error("fragment should contain the agenda container")
	}
	for _, h := range []string{`hx-target="#main-content"`, `hx-push-url="true"`} {
		if !strings.Contains(part, h) {
			t.Errorf("fragment missing %s", h)
		}
	}
}

func TestAgendaViewEmptyStateTranslated(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	html := e.body(t, e.get(t, "/calendar/agenda?from=2026-03-01", false))
	msg := i18n.T("en", "calendar.agendaEmpty")
	if msg == "calendar.agendaEmpty" || msg == "" {
		t.Fatalf("calendar.agendaEmpty untranslated: %q", msg)
	}
	if !strings.Contains(html, msg) {
		t.Errorf("empty state %q not rendered", msg)
	}
}

func TestCalendarUserLocationFallback(t *testing.T) {
	h := NewCalendarHandler(nil, nil, nil)
	if h.userLocation("u") != time.Local {
		t.Error("without a zone source the location should be time.Local")
	}
	berlin := mustZone(t, "Europe/Berlin")
	h.SetZoneSource(fixedZone{berlin})
	if h.userLocation("u") != berlin {
		t.Error("zone source not used")
	}
}

func TestMonthChipsCarryCalendarColour(t *testing.T) {
	e := newAllDayEnv(t)
	if _, err := e.calendars.EnsureDefault(quotaUser); err != nil {
		t.Fatal(err)
	}
	cals, err := e.calendars.List(quotaUser)
	if err != nil || len(cals) == 0 {
		t.Fatalf("List: %v", err)
	}
	cal := cals[0]
	cal.Color = "#ef4444"
	if err := e.calendars.Update(quotaUser, cal.Guid, cal); err != nil {
		t.Fatal(err)
	}
	if _, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Coloured", CalendarId: cal.Guid, StartTime: utc(2026, 5, 12, 12, 0), EndTime: utc(2026, 5, 12, 13, 0),
	}); err != nil {
		t.Fatal(err)
	}
	html := e.view(t, e.h.MonthView, "/calendar/2026/5", map[string]string{"year": "2026", "month": "5"})
	// #ef4444 maps to red, not the blue fallback an unmapped calendar gets.
	if !strings.Contains(html, `class="event-chip cal-colour-red"`) {
		t.Errorf("month chip lacks cal-colour-red; chips: %v", regexp.MustCompile(`class="event-chip[^"]*"`).FindAllString(html, -1))
	}
}

func TestBuildAgendaCarriesCalendarColour(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ev := timedEvent("a", utc(2026, 1, 5, 9, 0))
	ev.CalendarId = "work"
	other := timedEvent("b", utc(2026, 1, 5, 10, 0))
	other.CalendarId = "unmapped"
	days := buildAgenda(from, agendaRangeDays, []*bongov1.Event{ev, other}, map[string]string{"work": "#ef4444"}, time.UTC)
	if len(days) != 1 || len(days[0].Items) != 2 {
		t.Fatalf("days = %+v, want one group of two", days)
	}
	if got := days[0].Items[0].ColourClass; got != "cal-colour-red" {
		t.Errorf("mapped calendar ColourClass = %q, want cal-colour-red", got)
	}
	if got := days[0].Items[1].ColourClass; got != "cal-colour-blue" {
		t.Errorf("unmapped calendar ColourClass = %q, want the blue fallback", got)
	}
}

func TestAgendaBarCarriesCalendarColour(t *testing.T) {
	e := newAgendaEnv(t, "UTC")
	if _, err := e.calendars.EnsureDefault(quotaUser); err != nil {
		t.Fatal(err)
	}
	cals, err := e.calendars.List(quotaUser)
	if err != nil || len(cals) == 0 {
		t.Fatalf("List: %v", err)
	}
	cal := cals[0]
	cal.Color = "#ef4444"
	if err := e.calendars.Update(quotaUser, cal.Guid, cal); err != nil {
		t.Fatal(err)
	}
	if _, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Barred", CalendarId: cal.Guid, StartTime: utc(2026, 1, 5, 9, 0), EndTime: utc(2026, 1, 5, 10, 0),
	}); err != nil {
		t.Fatal(err)
	}
	html := e.body(t, e.get(t, "/calendar/agenda?from=2026-01-01", false))
	if !strings.Contains(html, `class="agenda-bar cal-colour-red"`) {
		t.Errorf("agenda bar lacks cal-colour-red; bars: %v", regexp.MustCompile(`class="agenda-bar[^"]*"`).FindAllString(html, -1))
	}
}

// In an east-of-UTC zone a timed event in the first local hour of a day has a
// UTC start on the previous day, so a naive StartTime sort puts it ahead of
// the day's all-day event.
func TestBuildAgendaAllDayFirstEastOfUTC(t *testing.T) {
	loc := mustZone(t, "Asia/Tokyo")
	f := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	events := []*bongov1.Event{
		timedEvent("early", utc(2026, 1, 9, 15, 30)), // 00:30 on 10 Jan in Tokyo
		allDayEvent("holiday", 2026, 1, 10),
	}
	days := buildAgenda(f, agendaRangeDays, events, nil, loc)
	if len(days) != 1 || len(days[0].Items) != 2 {
		t.Fatalf("days = %+v, want one group of two", days)
	}
	if got := days[0].Items[0].Event.Title; got != "holiday" {
		t.Errorf("first item = %s, want the all-day event", got)
	}
}

// The ?from date must be read in the user's zone: for a Berlin user an event
// at 00:10 on 1 Jan is 23:10 UTC on 31 Dec and falls outside a window that
// starts at 00:00 UTC on 1 Jan.
func TestAgendaViewFromParsedInUserZone(t *testing.T) {
	e := newAgendaEnv(t, "Europe/Berlin")
	start := utc(2025, 12, 31, 23, 10)
	if _, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "FirstHour", CalendarId: "default", StartTime: start, EndTime: start + 1800,
	}); err != nil {
		t.Fatal(err)
	}
	_, titles := agendaGroups(e.body(t, e.get(t, "/calendar/agenda?from=2026-01-01", false)))
	if len(titles["2026-01-01"]) != 1 || titles["2026-01-01"][0] != "FirstHour" {
		t.Errorf("first-hour event missing from 2026-01-01: %v", titles)
	}
}
