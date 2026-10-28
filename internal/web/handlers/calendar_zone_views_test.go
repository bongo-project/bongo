package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// These tests pin one rule: every calendar view places events, marks today
// and builds Today links in the user's preference zone, never the process
// zone. Each case sets time.Local to a third zone so a view that still reads
// the server zone cannot agree with the user zone by accident, so none may
// call t.Parallel.

var (
	chipTimeRe  = regexp.MustCompile(`class="event-time">([0-9:]+)<`)
	miniTodayRe = regexp.MustCompile(`href="/calendar/day/\d+/\d+/(\d+)"[^>]*class="mini-cal-day today[ "]`)
)

type zoneViewCase struct {
	name     string
	userZone string
	server   string
	now      time.Time // the injected clock
	event    int64     // a timed event at 23:30 UTC on 10 November 2026
	wantDay  string    // the event's day in the user zone
	wantTime string    // the event's start time in the user zone
	today    string    // today in the user zone
	week     string    // ISO week containing today
}

var zoneViewCases = []zoneViewCase{
	{
		name: "Auckland", userZone: "Pacific/Auckland", server: "America/Los_Angeles",
		now:   time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		event: utc(2026, 11, 10, 23, 30), wantDay: "2026-11-11", wantTime: "12:30",
		today: "2026-11-11", week: "46",
	},
	{
		// No preference: PreferenceStore.Location is UTC.
		name: "UTC", userZone: "UTC", server: "Asia/Tokyo",
		now:   time.Date(2026, 11, 10, 23, 45, 0, 0, time.UTC),
		event: utc(2026, 11, 10, 23, 30), wantDay: "2026-11-10", wantTime: "23:30",
		today: "2026-11-10", week: "46",
	},
}

func (tc zoneViewCase) env(t *testing.T) *agendaEnv {
	t.Helper()
	setLocalZone(t, tc.server)
	e := newAgendaEnv(t, tc.userZone)
	e.h.now = func() time.Time { return tc.now }
	if _, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Edge", CalendarId: "default", StartTime: tc.event, EndTime: tc.event + 3600,
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestCalendarViewsShareUserZone(t *testing.T) {
	for _, tc := range zoneViewCases {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.env(t)
			day := tc.wantDay
			dayLabel, err := time.Parse("2006-01-02", day)
			if err != nil {
				t.Fatal(err)
			}

			// Agenda is the reference.
			_, titles := agendaGroups(e.body(t, e.get(t, "/calendar/agenda?from=2026-11-01", false)))
			if len(titles[day]) != 1 {
				t.Fatalf("agenda: Edge not under %s: %v", day, titles)
			}

			// Month: the chip sits in the cell for the agenda's day, with the
			// agenda's time, and no other cell holds it.
			month := e.view(t, e.h.MonthView, "/calendar/2026/11", map[string]string{"year": "2026", "month": "11"})
			cell := dayCell(t, month, dayLabel.Format("Monday, January 2"))
			if !strings.Contains(cell, "Edge") {
				t.Errorf("month: Edge not in the %s cell", day)
			}
			if got := chipTimeRe.FindStringSubmatch(cell); got == nil || got[1] != tc.wantTime {
				t.Errorf("month: chip time = %v, want %s", got, tc.wantTime)
			}
			if n := strings.Count(month, ">Edge<") + strings.Count(month, "Edge</a>"); n != 1 {
				t.Errorf("month: Edge rendered %d times, want 1", n)
			}
			todayLabel, _ := time.Parse("2006-01-02", tc.today)
			if got := todayCells(month); len(got) != 1 || got[0] != todayLabel.Format("Monday, January 2") {
				t.Errorf("month: today cells = %v, want only %s", got, tc.today)
			}
			if m := miniTodayRe.FindStringSubmatch(month); m == nil || m[1] != strings.TrimPrefix(tc.today[8:], "0") {
				t.Errorf("mini calendar today = %v, want day %s", m, tc.today[8:])
			}

			// Week: the block is in the column for the agenda's day, shows
			// the same time, and the today column follows the user zone.
			week := e.view(t, e.h.WeekView, "/calendar/week/2026/46", map[string]string{"year": "2026", "week": "46"})
			col := weekColumn(t, week, day)
			if !strings.Contains(col, "Edge") {
				t.Errorf("week: Edge not in column %s", day)
			}
			if !strings.Contains(col, ">"+tc.wantTime+"<") {
				t.Errorf("week: block time not %s in column %s", tc.wantTime, day)
			}
			if got := todayColumns(week); len(got) != 1 || got[0] != tc.today {
				t.Errorf("week: today columns = %v, want only %s", got, tc.today)
			}
			if !strings.Contains(week, `href="/calendar/week/2026/`+tc.week+`"`) {
				t.Errorf("week: Today link not week %s", tc.week)
			}

			// Day view of the agenda's day shows the event; Today link and
			// highlight follow the user zone.
			y, mo, d := dayLabel.Date()
			dayHTML := e.view(t, e.h.DayView, "/calendar/day/"+dayLabel.Format("2006/1/2"), map[string]string{
				"year": strconv.Itoa(y), "month": strconv.Itoa(int(mo)), "day": strconv.Itoa(d),
			})
			if !strings.Contains(dayHTML, "Edge") || !strings.Contains(dayHTML, ">"+tc.wantTime+"<") {
				t.Errorf("day: Edge at %s missing on %s", tc.wantTime, day)
			}
			ty, tmo, td := todayLabel.Date()
			wantToday := `href="/calendar/day/` + strconv.Itoa(ty) + `/` + strconv.Itoa(int(tmo)) + `/` + strconv.Itoa(td) + `"`
			if !strings.Contains(dayHTML, wantToday) {
				t.Errorf("day: Today link not %s", wantToday)
			}
			if strings.Contains(dayHTML, `day-column today"`) != (day == tc.today) {
				t.Errorf("day: today highlight wrong for %s with today %s", day, tc.today)
			}

			// Mini calendar partial defaults to the user's today.
			mini := e.view(t, e.h.MiniCalendar, "/calendar/mini", nil)
			if m := miniTodayRe.FindStringSubmatch(mini); m == nil || m[1] != strings.TrimPrefix(tc.today[8:], "0") {
				t.Errorf("mini partial today = %v, want day %s", m, tc.today[8:])
			}
		})
	}
}

func TestCalendarIndexRedirectsToUserMonth(t *testing.T) {
	setLocalZone(t, "America/Los_Angeles")
	e := newAgendaEnv(t, "Pacific/Auckland")
	// 31 Oct 12:00 UTC is already 1 Nov in Auckland, still 31 Oct in Los Angeles.
	e.h.now = func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) }
	req := withUserContext(httptest.NewRequest(http.MethodGet, "/calendar", nil), quotaUser, quotaEmail)
	w := httptest.NewRecorder()
	e.h.CalendarIndex(w, req)
	if got := w.Header().Get("Location"); got != "/calendar/2026/11" {
		t.Errorf("redirect = %q, want /calendar/2026/11", got)
	}
}

func TestISOWeekMondayUsesGivenZone(t *testing.T) {
	loc := mustZone(t, "Pacific/Auckland")
	got := isoWeekMonday(2026, 46, loc)
	if got.Location() != loc || got.Format("2006-01-02 15:04") != "2026-11-09 00:00" {
		t.Errorf("isoWeekMonday = %v", got)
	}
}

var (
	colRe = regexp.MustCompile(`<div class="day-column( today)?" data-date="([0-9-]+)"`)
)

// todayCells lists the labels of month cells carrying the today class.
func todayCells(html string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<div class="day-cell[^"]*\btoday\b[^"]*" aria-label="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		out = append(out, m[1])
	}
	return out
}

func todayColumns(html string) []string {
	var out []string
	for _, m := range colRe.FindAllStringSubmatch(html, -1) {
		if m[1] != "" {
			out = append(out, m[2])
		}
	}
	return out
}

// weekColumn returns the HTML of the week column for date.
func weekColumn(t *testing.T, html, date string) string {
	t.Helper()
	idx := colRe.FindAllStringSubmatchIndex(html, -1)
	for i, m := range idx {
		if html[m[4]:m[5]] != date {
			continue
		}
		end := len(html)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		return html[m[1]:end]
	}
	t.Fatalf("no week column %s", date)
	return ""
}
