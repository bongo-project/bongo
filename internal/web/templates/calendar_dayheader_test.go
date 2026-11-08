package templates

import (
	"bytes"
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/web/middleware"
)

// weekColumns returns Monday 12 to Sunday 18 October 2026; todayIdx marks
// one column as today (-1 for none).
func weekColumns(todayIdx int) []DayColumn {
	var cols []DayColumn
	for i := range 7 {
		d := time.Date(2026, 10, 12+i, 0, 0, 0, 0, time.UTC)
		cols = append(cols, DayColumn{Date: d, DateStr: d.Format("2006-01-02"), IsToday: i == todayIdx})
	}
	return cols
}

func renderWeekHeader(t *testing.T, locale string, days []DayColumn) string {
	t.Helper()
	initI18nForTest()
	var buf bytes.Buffer
	ctx := middleware.WithLocale(context.Background(), locale)
	if err := WeekViewGrid(2026, 42, days, nil, nil).Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func renderDayHeader(t *testing.T, locale string, col DayColumn) string {
	t.Helper()
	initI18nForTest()
	var buf bytes.Buffer
	ctx := middleware.WithLocale(context.Background(), locale)
	if err := DayViewGrid(col.Date, col, nil, nil).Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestWeekViewDayHeader(t *testing.T) {
	out := renderWeekHeader(t, "en", weekColumns(2))
	for d := 12; d <= 18; d++ {
		link := `href="/calendar/day/2026/10/` + strconv.Itoa(d) + `"`
		if !strings.Contains(out, link) {
			t.Errorf("missing day link %s", link)
		}
	}
	if n := strings.Count(out, `day-header-cell today`); n != 1 {
		t.Errorf("today cells = %d, want 1", n)
	}
	if n := strings.Count(out, `aria-current="date"`); n != 1 {
		t.Errorf("aria-current count = %d, want 1", n)
	}
	hi, ai := strings.Index(out, `class="day-header-row"`), strings.Index(out, `class="all-day-header"`)
	if hi < 0 || ai < 0 || hi > ai {
		t.Errorf("header row index %d must precede all-day strip %d", hi, ai)
	}
	// Each weekday must sit beside its own date, in the column's order.
	pairs := regexp.MustCompile(`day-header-weekday">([^<]+)</span>\s*<span class="day-header-num">(\d+)<`).FindAllStringSubmatch(out, -1)
	want := [][2]string{{"Mon", "12"}, {"Tue", "13"}, {"Wed", "14"}, {"Thu", "15"}, {"Fri", "16"}, {"Sat", "17"}, {"Sun", "18"}}
	if len(pairs) != len(want) {
		t.Fatalf("header cells = %d, want %d", len(pairs), len(want))
	}
	for i, w := range want {
		if pairs[i][1] != w[0] || pairs[i][2] != w[1] {
			t.Errorf("cell %d = %s %s, want %s %s", i, pairs[i][1], pairs[i][2], w[0], w[1])
		}
	}
}

// Regression: the day view with no all-day events used to show no date.
func TestDayViewDayHeaderWithoutAllDay(t *testing.T) {
	d := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	out := renderDayHeader(t, "en", DayColumn{Date: d, DateStr: "2026-10-14", IsToday: true})
	if !strings.Contains(out, `class="day-header-row"`) {
		t.Fatal("day view without all-day events must still render the header row")
	}
	for _, w := range []string{">Wed<", ">14<", `day-header-cell today`} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func TestEmptyHourLinksNameTheCreateAction(t *testing.T) {
	out := renderWeekHeader(t, "en", weekColumns(-1))
	if got := strings.Count(out, `class="hour-row-link" aria-label="Create Event:`); got != 7*24 {
		t.Errorf("named hour links = %d, want %d", got, 7*24)
	}
}

func TestDayHeaderLocalised(t *testing.T) {
	cases := []struct{ locale, want, notWant string }{
		{"de", ">Mo<", ">Mon<"},
		{"fr", ">Lun<", ">Mon<"},
		{"es", ">Lun<", ">Mon<"},
	}
	for _, tc := range cases {
		t.Run(tc.locale, func(t *testing.T) {
			out := renderWeekHeader(t, tc.locale, weekColumns(-1))
			if !strings.Contains(out, tc.want) {
				t.Errorf("missing %q", tc.want)
			}
			if strings.Contains(out, tc.notWant) {
				t.Errorf("found English %q", tc.notWant)
			}
		})
	}
}
