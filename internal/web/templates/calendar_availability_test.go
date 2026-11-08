package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func availabilityView() *AvailabilityView {
	day := time.Date(2030, 5, 6, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) int64 { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).Unix() }
	return &AvailabilityView{
		DayStart: day.Unix(),
		DayEnd:   day.AddDate(0, 0, 1).Unix(),
		Proposed: [2]int64{at(10, 0), at(11, 0)},
		Zone:     time.UTC,
		Rows: []AvailabilityRow{
			{Label: "a@x.test", Shared: true, Blocks: [][2]int64{{at(9, 0), at(10, 30)}, {at(14, 0), at(15, 0)}}},
			{Label: "b@x.test", Shared: true},
			{Label: "c@x.test"},
		},
	}
}

func TestEventAvailabilityRenders(t *testing.T) {
	out := renderToString(t, EventAvailability(availabilityView()))
	for _, want := range []string{
		"Busy 09:00–10:30", "Busy 14:00–15:00",
		"No busy time this day", "Availability not shared", "Proposed time 10:00–11:00",
		`class="availability-busy conflict"`, `class="availability-busy"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	// Only the overlapping block is marked: one rect and one label.
	if got := strings.Count(out, "conflict"); got != 2 {
		t.Errorf("conflict marked %d times, want 2 (rect and label of the 09:00 block)", got)
	}
	if strings.Contains(out, "style=") {
		t.Error("inline style is blocked by the CSP")
	}
}

func TestEventAvailabilityEmpty(t *testing.T) {
	for name, v := range map[string]*AvailabilityView{"nil": nil, "no rows": {Zone: time.UTC}} {
		if out := strings.TrimSpace(renderToString(t, EventAvailability(v))); out != "" {
			t.Errorf("%s: %q, want nothing", name, out)
		}
	}
}

// The labels exist in every shipped locale, translated.
func TestEventAvailabilityLocales(t *testing.T) {
	initI18nForTest()
	en := renderToString(t, EventAvailability(availabilityView()))
	for _, loc := range []string{"de", "es", "fr"} {
		ctx := middleware.WithLocale(context.Background(), loc)
		var buf bytes.Buffer
		if err := EventAvailability(availabilityView()).Render(ctx, &buf); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		for _, english := range []string{"Availability not shared", "Proposed time", "No busy time this day", ">Availability<"} {
			if strings.Contains(out, english) {
				t.Errorf("%s: %q is not translated", loc, english)
			}
		}
		if out == en {
			t.Errorf("%s renders like en", loc)
		}
	}
}

// The event form carries the container that loads the view, includes named
// fields only (no CSRF token in a GET URL) and names the edited event.
func TestEventFormHasAvailabilityContainer(t *testing.T) {
	out := renderToString(t, EventFormPage(&bongov1.Event{Guid: "g1", Title: "T"}, nil, false, "2030-05-06", "me@example.com"))
	for _, want := range []string{
		`class="event-editor"`, `class="event-editor-head"`, `class="event-form-actions"`,
		`hx-get="/calendar/freebusy"`,
		`hx-include="[name=attendees],[name=start_date],[name=start_time],[name=end_time],[name=all_day]"`,
		`availability-refresh from:closest form`,
		`&#34;event&#34;:&#34;g1&#34;`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("event form missing %q", want)
		}
	}
	if strings.Contains(out, `hx-include="closest form"`) {
		t.Error("the whole form must not be sent in a GET")
	}
}
