package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The drag script binds to .event-block[data-guid]; a read-only event must not
// carry it, as the server answers 403 to the move.
func TestWeekAndDayBlocksSkipDragHandleWhenReadOnly(t *testing.T) {
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	start := day.Add(9 * time.Hour).Unix()
	mk := func(guid, cal, rrule string) EventBlock {
		return EventBlock{HeightMinutes: 30, Event: &bongov1.Event{Guid: guid, CalendarId: cal, Title: guid, StartTime: start, EndTime: start + 1800, Rrule: rrule}}
	}
	cals := []*bongov1.Calendar{
		{Guid: "own", Name: "Own"},
		{Guid: "feed", Name: "Feed", Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"}},
	}
	rw, ro := SharedKey("o1", "w"), SharedKey("o1", "r")
	ctx := WithSharedCalendars(context.Background(), []SharedCalendar{
		{Key: rw, Level: "write"}, {Key: ro, Level: "read"},
	})
	col := DayColumn{Date: day, DateStr: "2026-10-14", Blocks: []EventBlock{
		mk("own-ev", "own", ""), mk("feed-ev", "feed", ""), mk("series-ev", "own", "FREQ=DAILY"),
		mk("write-ev", rw, ""), mk("read-ev", ro, ""),
	}}
	want := map[string]bool{"own-ev": true, "write-ev": true, "feed-ev": false, "series-ev": false, "read-ev": false}

	render := func(c templ.Component) string {
		initI18nForTest()
		var buf bytes.Buffer
		if err := c.Render(ctx, &buf); err != nil {
			t.Fatalf("render: %v", err)
		}
		return buf.String()
	}
	views := map[string]string{}
	views["week"] = render(WeekViewGrid(2026, 42, []DayColumn{col}, cals, nil))
	views["day"] = render(DayViewGrid(day, col, cals, nil))
	for view, out := range views {
		for guid, movable := range want {
			if got := strings.Contains(out, `data-guid="`+guid+`"`); got != movable {
				t.Errorf("%s view: drag handle for %s = %v, want %v", view, guid, got, movable)
			}
		}
	}
}
