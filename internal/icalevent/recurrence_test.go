package icalevent

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestZone(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"Europe/Amsterdam", true},
		{"UTC", true},
		{"America/Argentina/Buenos_Aires", true},
		{"", false},
		{"Local", false},
		{"W. Europe Standard Time", false},
		{"/mozilla.org/20050126_1/Europe/Amsterdam", false},
		{"../../etc/passwd", false},
		{"Europe/Amsterdam\r\nX-EVIL:1", false},
		{"Europe/Amsterdam;VALUE=DATE", false},
		{"Not/AZone", false},
		{strings.Repeat("A", 65), false},
	} {
		l, ok := Zone(tc.name)
		if ok != tc.ok {
			t.Errorf("Zone(%q) ok = %v, want %v", tc.name, ok, tc.ok)
		}
		if ok && l == nil {
			t.Errorf("Zone(%q) returned a nil zone", tc.name)
		}
	}
	// Failures are not cached, so client names cannot grow the cache.
	if _, hit := zoneCache.Load("Not/AZone"); hit {
		t.Error("an unloadable zone was cached")
	}
	if _, hit := zoneCache.Load("Europe/Amsterdam"); !hit {
		t.Error("a loaded zone was not cached")
	}
}

func TestVTimezoneAmsterdam(t *testing.T) {
	loc, ok := Zone("Europe/Amsterdam")
	if !ok {
		t.Skip("no zone database")
	}
	tz := VTimezone(loc, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC))
	if tz.Props.Get(ical.PropTimezoneID).Value != "Europe/Amsterdam" {
		t.Fatalf("TZID = %q", tz.Props.Get(ical.PropTimezoneID).Value)
	}
	// Two changes a year for two years.
	if len(tz.Children) != 4 {
		t.Fatalf("got %d observances, want 4", len(tz.Children))
	}
	want := []struct{ kind, start, from, to string }{
		{ical.CompTimezoneDaylight, "20260329T020000", "+0100", "+0200"},
		{ical.CompTimezoneStandard, "20261025T030000", "+0200", "+0100"},
		{ical.CompTimezoneDaylight, "20270328T020000", "+0100", "+0200"},
		{ical.CompTimezoneStandard, "20271031T030000", "+0200", "+0100"},
	}
	for i, w := range want {
		o := tz.Children[i]
		got := [4]string{o.Name, o.Props.Get(ical.PropDateTimeStart).Value, o.Props.Get(ical.PropTimezoneOffsetFrom).Value, o.Props.Get(ical.PropTimezoneOffsetTo).Value}
		if got != [4]string{w.kind, w.start, w.from, w.to} {
			t.Errorf("observance %d = %v, want %v", i, got, w)
		}
	}
}

func TestVTimezoneFixedOffset(t *testing.T) {
	loc, ok := Zone("Asia/Kolkata")
	if !ok {
		t.Skip("no zone database")
	}
	tz := VTimezone(loc, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if len(tz.Children) != 1 || tz.Children[0].Name != ical.CompTimezoneStandard {
		t.Fatalf("want a single STANDARD observance, got %d children", len(tz.Children))
	}
	if got := tz.Children[0].Props.Get(ical.PropTimezoneOffsetTo).Value; got != "+0530" {
		t.Errorf("offset = %q, want +0530", got)
	}
}

func encode(t *testing.T, comps ...*ical.Component) string {
	t.Helper()
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//T//T//EN")
	cal.Children = comps
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestAddRecurrence(t *testing.T) {
	ams, ok := Zone("Europe/Amsterdam")
	if !ok {
		t.Skip("no zone database")
	}
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, ams)
	mk := func(rrule string, exdates ...string) (*ical.Event, *bongov1.Event) {
		e := &bongov1.Event{Guid: "g", StartTime: start.Unix(), EndTime: start.Unix() + 3600, Rrule: rrule, Timezone: "Europe/Amsterdam", Exdates: exdates}
		v := ical.NewEvent()
		AddRecurrence(v, e, ams)
		return v, e
	}

	t.Run("rule and exdates in the DTSTART zone", func(t *testing.T) {
		v, _ := mk("FREQ=WEEKLY", "20300513T090000", "20300527")
		if got := v.Props.Get(ical.PropRecurrenceRule).Value; got != "FREQ=WEEKLY" {
			t.Errorf("RRULE = %q", got)
		}
		ex := v.Props[ical.PropExceptionDates]
		if len(ex) != 2 {
			t.Fatalf("got %d EXDATE, want 2", len(ex))
		}
		for i, want := range []string{"20300513T090000", "20300527T090000"} {
			if ex[i].Value != want || ex[i].Params.Get("TZID") != "Europe/Amsterdam" {
				t.Errorf("EXDATE %d = %q TZID=%q, want %q in Europe/Amsterdam", i, ex[i].Value, ex[i].Params.Get("TZID"), want)
			}
		}
	})
	t.Run("no rule, nothing written", func(t *testing.T) {
		v, _ := mk("", "20300513")
		if len(v.Props) != 0 {
			t.Errorf("wrote %v", v.Props)
		}
	})
	t.Run("hostile rule is refused", func(t *testing.T) {
		v, _ := mk("FREQ=WEEKLY\r\nX-EVIL:1")
		if len(v.Props) != 0 {
			t.Errorf("wrote %v", v.Props)
		}
	})
	t.Run("all-day exdates are dates", func(t *testing.T) {
		d := time.Date(2030, 5, 6, 0, 0, 0, 0, time.UTC)
		e := &bongov1.Event{Guid: "g", AllDay: true, StartTime: d.Unix(), EndTime: d.Unix() + 86400, Rrule: "FREQ=DAILY", Exdates: []string{"20300507"}}
		v := ical.NewEvent()
		AddRecurrence(v, e, ams)
		ex := v.Props[ical.PropExceptionDates]
		if len(ex) != 1 || ex[0].Value != "20300507" || ex[0].Params.Get("VALUE") != "DATE" || ex[0].Params.Get("TZID") != "" {
			t.Errorf("EXDATE = %+v", ex)
		}
	})
}

func TestVTimezoneForSkipsDatesAndUTC(t *testing.T) {
	ams, ok := Zone("Europe/Amsterdam")
	if !ok {
		t.Skip("no zone database")
	}
	e := &bongov1.Event{StartTime: 1900000000, EndTime: 1900003600}
	if VTimezoneFor(e, time.UTC) != nil {
		t.Error("UTC needs no VTIMEZONE")
	}
	e.AllDay = true
	if VTimezoneFor(e, ams) != nil {
		t.Error("an all-day event needs no VTIMEZONE")
	}
	e.AllDay = false
	if VTimezoneFor(e, ams) == nil {
		t.Error("a zoned event needs a VTIMEZONE")
	}
	if got := encode(t, VTimezoneFor(e, ams)); !strings.Contains(got, "BEGIN:VTIMEZONE") {
		t.Errorf("did not encode: %s", got)
	}
}

func TestSeriesSpanCapsFarFutureUntil(t *testing.T) {
	ams, ok := Zone("Europe/Amsterdam")
	if !ok {
		t.Skip("no zone database")
	}
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, time.UTC)
	e := &bongov1.Event{
		StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
		Rrule: "FREQ=WEEKLY;UNTIL=99991231T235959Z", Timezone: "Europe/Amsterdam",
	}
	from, to := seriesSpan(e)
	if limit := start.AddDate(50, 0, 0); to.After(limit) {
		t.Fatalf("span ends %s, want at most %s", to, limit)
	}
	if !to.After(start.AddDate(10, 0, 0)) {
		t.Errorf("a long UNTIL must still extend past the open-ended default, got %s", to)
	}
	if from.After(start) {
		t.Errorf("span starts %s, after DTSTART", from)
	}

	began := time.Now()
	tz := VTimezoneFor(e, ams)
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("VTimezoneFor took %s", took)
	}
	if tz == nil {
		t.Fatal("no VTIMEZONE")
	}
	// About 100 changes in 50 years, not thousands.
	if n := len(tz.Children); n == 0 || n > 200 {
		t.Errorf("%d observances, want a bounded few", n)
	}
}
