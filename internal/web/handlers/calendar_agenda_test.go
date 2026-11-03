package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
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
	days := buildAgenda(from, agendaRangeDays, events, nil, nil, time.UTC, time.Time{})
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
			days := buildAgenda(f, agendaRangeDays, []*bongov1.Event{tc.ev}, nil, nil, loc, time.Time{})
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
	days := buildAgenda(f, agendaRangeDays, events, nil, nil, loc, time.Time{})
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
	days := buildAgenda(from, agendaRangeDays, []*bongov1.Event{ev, other}, nil, map[string]string{"work": "#ef4444"}, time.UTC, time.Time{})
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
	days := buildAgenda(f, agendaRangeDays, events, nil, nil, loc, time.Time{})
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

// agendaSourcesEnv is a shared-calendar environment with a task store, a
// subscribed calendar and a coloured own calendar for the actor rita (read
// grant on the owner's calA).
func agendaSourcesEnv(t *testing.T) *sharedEnv {
	t.Helper()
	e := newSharedEnv(t)
	e.h.SetTaskStore(store.NewTaskStore(e.ps, e.app))
	e.mustCal(actRead, &bongov1.Calendar{Guid: "default", Name: "Rita", Color: "#3B82F6"})
	e.mustCal(actRead, &bongov1.Calendar{Guid: "sub", Name: "Feed", Color: "#F59E0B",
		Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://feed.example/x.ics"}})
	e.mustCal(actRead, &bongov1.Calendar{Guid: "chores", Name: "Chores", Color: "#22C55E"})
	return e
}

func (e *sharedEnv) mustEvent(owner string, ev *bongov1.Event) {
	e.t.Helper()
	if _, err := e.events.Create(owner, ev); err != nil {
		e.t.Fatal(err)
	}
}

func (e *sharedEnv) mustTask(owner string, tk *bongov1.Task) {
	e.t.Helper()
	if _, err := store.NewTaskStore(e.ps, e.app).Create(owner, tk); err != nil {
		e.t.Fatal(err)
	}
}

// agendaItem returns the li.agenda-item whose content contains marker (a
// title or data-task-guid attribute), so assertions cannot be satisfied by
// the sidebar's identically classed colour dots.
func agendaItem(t *testing.T, body, marker string) string {
	t.Helper()
	for _, chunk := range strings.Split(body, `<li class="agenda-item"`)[1:] {
		if end := strings.Index(chunk, "</li>"); end >= 0 {
			chunk = chunk[:end]
		}
		if strings.Contains(chunk, marker) {
			return chunk
		}
	}
	t.Fatalf("no agenda item contains %q", marker)
	return ""
}

func TestAgendaSources(t *testing.T) {
	const when = sharedEvAWhn
	dueTask := func(guid, cal string) *bongov1.Task {
		return &bongov1.Task{Guid: guid, Summary: "Pay " + guid, CalendarId: cal, Due: when}
	}
	tests := []struct {
		name  string
		actor string
		setup func(e *sharedEnv)
		check func(t *testing.T, body string)
	}{
		{"subscribed event once with its colour", actRead,
			func(e *sharedEnv) {
				e.mustEvent(actRead, &bongov1.Event{Guid: "s1", Title: "Feed gig", CalendarId: "sub", StartTime: when, EndTime: when + 3600})
			},
			func(t *testing.T, body string) {
				if n := strings.Count(body, `data-cal-id="sub"`); n != 1 {
					t.Errorf("subscribed event rows = %d, want 1", n)
				}
				if !strings.Contains(agendaItem(t, body, "Feed gig"), "agenda-bar cal-colour-orange") {
					t.Error("subscribed event lacks its calendar colour")
				}
			}},
		{"shared read event and due task together", actRead,
			func(e *sharedEnv) { e.mustTask(actRead, dueTask("rent", "chores")) },
			func(t *testing.T, body string) {
				ev := agendaItem(t, body, "Team standup")
				if !strings.Contains(ev, "agenda-bar cal-colour-red") {
					t.Errorf("shared event lacks red bar: %s", ev)
				}
				task := agendaItem(t, body, `data-task-guid="rent"`)
				if !strings.Contains(task, "Pay rent") || !strings.Contains(task, "agenda-bar cal-colour-green") {
					t.Errorf("task item lacks title or green bar: %s", task)
				}
			}},
		{"task row carries its calendar id", actRead,
			func(e *sharedEnv) {
				e.mustTask(actRead, dueTask("a", "chores"))
				e.mustTask(actRead, dueTask("b", ""))
			},
			func(t *testing.T, body string) {
				if !strings.Contains(body, `data-task-guid="a" data-cal-id="chores"`) {
					t.Errorf("task a lacks data-cal-id: %s", body)
				}
				if !strings.Contains(body, `data-task-guid="b" data-cal-id="`+store.TasksCalendarGUID+`"`) {
					t.Error("task without a calendar lacks the Tasks calendar id")
				}
			}},
		{"freebusy grant shows no shared event", actFreeBusy, func(e *sharedEnv) {},
			func(t *testing.T, body string) {
				if strings.Contains(body, "Team standup") {
					t.Error("freebusy grantee sees the shared event")
				}
			}},
		{"declined own event hidden, cancelled kept struck", actRead,
			func(e *sharedEnv) {
				e.mustEvent(actRead, &bongov1.Event{Guid: "d1", Title: "Declined one", CalendarId: "default", StartTime: when, EndTime: when + 3600,
					Attendees: []*bongov1.Attendee{{Address: "rita@d.test", Partstat: "DECLINED"}}})
				e.mustEvent(actRead, &bongov1.Event{Guid: "c1", Title: "Cancelled one", CalendarId: "default", StartTime: when, EndTime: when + 3600, Status: "CANCELLED"})
			},
			func(t *testing.T, body string) {
				if strings.Contains(body, "Declined one") {
					t.Error("declined event shown")
				}
				if !strings.Contains(body, "Cancelled one") || !strings.Contains(body, "event-cancelled") {
					t.Error("cancelled event not rendered struck through")
				}
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := agendaSourcesEnv(t)
			tc.setup(e)
			tc.check(t, viewBody(t, e, tc.actor, "agenda"))
		})
	}
}

func TestAgendaDaysRangeAndClamp(t *testing.T) {
	e := agendaSourcesEnv(t)
	from := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	day := func(n int) int64 { return from.AddDate(0, 0, n).Add(10 * time.Hour).Unix() }
	for _, n := range []int{0, 1, 6, 7, 29, 30, 31} {
		e.mustEvent(actRead, &bongov1.Event{Guid: "d" + strconv.Itoa(n), Title: "E" + strconv.Itoa(n), CalendarId: "default", StartTime: day(n), EndTime: day(n) + 3600})
	}
	e.mustTask(actRead, &bongov1.Task{Guid: "t3", Summary: "T3", CalendarId: "chores", Due: day(3)})
	e.mustTask(actRead, &bongov1.Task{Guid: "t8", Summary: "T8", CalendarId: "chores", Due: day(8)})
	tests := []struct {
		name string
		days int
		want []string
		not  []string
	}{
		{"seven days", 7, []string{"E6", "T3"}, []string{"E7", "E29", "E30", "T8"}},
		{"thirty days", 30, []string{"E29", "T8"}, []string{"E30", "E31"}},
		{"over the cap is clamped", 365, []string{"E29"}, []string{"E30", "E31"}},
		{"zero is one day", 0, []string{"E0"}, []string{"E1", "E6", "T3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := withRoute(httptest.NewRequest("GET", "/x", nil), actRead, nil)
			_, ds, _, err := e.h.agendaDays(req, actRead, "rita@d.test", from, tc.days, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			for _, d := range ds {
				for _, it := range d.Items {
					if it.Event != nil {
						titles = append(titles, it.Event.Title)
					}
					if it.Task != nil {
						titles = append(titles, it.Task.Summary)
					}
				}
			}
			all := strings.Join(titles, ",") + ","
			for _, w := range tc.want {
				if !strings.Contains(all, w+",") {
					t.Errorf("missing %s in %v", w, titles)
				}
			}
			for _, w := range tc.not {
				if strings.Contains(all, w+",") {
					t.Errorf("unexpected %s in %v", w, titles)
				}
			}
		})
	}
}

func TestAgendaDaysFreeBusyGrantee(t *testing.T) {
	e := agendaSourcesEnv(t)
	req := withRoute(httptest.NewRequest("GET", "/x", nil), actFreeBusy, nil)
	from := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	_, ds, _, err := e.h.agendaDays(req, actFreeBusy, "fred@d.test", from, 7, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 0 {
		t.Errorf("freebusy grantee got %d agenda days, want 0", len(ds))
	}
}
