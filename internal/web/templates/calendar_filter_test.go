package templates

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func calendarFilterFixture() ([]*bongov1.Calendar, *bongov1.Event, *bongov1.Event) {
	cals := []*bongov1.Calendar{{Guid: "default", Name: "Personal"}, {Guid: "work", Name: "Work"}}
	start := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC).Unix()
	work := &bongov1.Event{Guid: "e1", CalendarId: "work", Title: "Standup", StartTime: start, EndTime: start + 1800}
	// No calendar ID: shown under the default calendar.
	personal := &bongov1.Event{Guid: "e2", Title: "Dentist", StartTime: start + 7200, EndTime: start + 9000, AllDay: false}
	return cals, work, personal
}

// TestCalendarEventsCarryCalendarID: the sidebar's show/hide filter finds
// events by data-cal-id. No event element carried it, so unticking a calendar
// hid nothing even before the CSP error was fixed.
func TestCalendarEventsCarryCalendarID(t *testing.T) {
	cals, work, personal := calendarFilterFixture()
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	allDay := &bongov1.Event{Guid: "e3", CalendarId: "work", Title: "Offsite", StartTime: day.Unix(), EndTime: day.Unix() + 86400, AllDay: true}
	col := DayColumn{
		Date: day, DateStr: "2026-10-14",
		AllDay: []*bongov1.Event{allDay},
		Blocks: []EventBlock{{Event: work, TopMinutes: 540, HeightMinutes: 30}, {Event: personal, TopMinutes: 660, HeightMinutes: 30}},
	}
	views := map[string]string{
		"month": renderToString(t, CalendarMonthGrid(2026, 10, []DayCell{{Date: day, Events: []*bongov1.Event{work, personal}, IsCurrentMonth: true}}, cals, nil)),
		"week":  renderToString(t, WeekViewGrid(2026, 42, []DayColumn{col}, cals, nil)),
		"day":   renderToString(t, DayViewGrid(day, col, cals, nil)),
		"agenda": renderToString(t, AgendaContent(AgendaData{From: day, To: day, Days: []AgendaDay{{Date: day, Items: []AgendaItem{
			{Event: work, TimeLabel: "09:00"}, {Event: personal, TimeLabel: "11:00"},
		}}}})),
	}
	want := map[string]string{"e1": "work", "e2": "default", "e3": "work"}
	for name, out := range views {
		doc, err := html.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		found := 0
		walk(doc, func(n *html.Node) {
			if n.Type != html.ElementNode {
				return
			}
			if !hasClass(n, "event-chip") && !hasClass(n, "event-block") && !hasClass(n, "agenda-item") {
				return
			}
			found++
			m := attrMap(n)
			id, ok := m["data-cal-id"]
			if !ok {
				t.Errorf("%s: <%s class=%q> has no data-cal-id", name, n.Data, m["class"])
				return
			}
			href := m["href"]
			if hasClass(n, "agenda-item") {
				walk(n, func(c *html.Node) {
					if c.Type == html.ElementNode && c.Data == "a" {
						href = attrMap(c)["href"]
					}
				})
			}
			guid := href[strings.LastIndex(href, "/")+1:]
			if want[guid] != id {
				t.Errorf("%s: event %s has data-cal-id=%q, want %q", name, guid, id, want[guid])
			}
		})
		if found == 0 {
			t.Errorf("%s: no event elements rendered; the check is not looking at anything", name)
		}
	}
}

// TestCalendarListPanelIsCSPSafe: the show/hide checkbox used an arrow
// function, document. and a statement list, which the CSP build cannot parse.
func TestCalendarListPanelIsCSPSafe(t *testing.T) {
	cals, _, _ := calendarFilterFixture()
	out := renderToString(t, CalendarListPanel(cals))
	assertCSPSafe(t, "CalendarListPanel", out)
	for _, c := range cals {
		if !strings.Contains(out, `data-calendar="`+c.Guid+`"`) {
			t.Errorf("checkbox for %s has no data-calendar", c.Guid)
		}
	}
	if strings.Contains(out, "data-cal-id") {
		t.Error("a sidebar checkbox carries data-cal-id, so the filter would hide the checkbox itself")
	}
}
