package icalevent

import (
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func dtstartProp(value string, params ...string) *ical.Prop {
	p := ical.NewProp(ical.PropDateTimeStart)
	p.Value = value
	for i := 0; i+1 < len(params); i += 2 {
		p.Params.Set(params[i], params[i+1])
	}
	return p
}

func TestSetUntilAndCount(t *testing.T) {
	for _, tc := range []struct {
		name, rule, until string
		count             int
		want              string
	}{
		{"until replaces count", "FREQ=WEEKLY;COUNT=6;BYDAY=MO", "20301103T225959Z", 0, "FREQ=WEEKLY;BYDAY=MO;UNTIL=20301103T225959Z"},
		{"until replaces until", "FREQ=DAILY;UNTIL=20310101", "20300512", 0, "FREQ=DAILY;UNTIL=20300512"},
		{"until is appended", "FREQ=MONTHLY;BYMONTHDAY=13", "20300512", 0, "FREQ=MONTHLY;BYMONTHDAY=13;UNTIL=20300512"},
		{"lower case parts", "FREQ=WEEKLY;count=3", "20300512", 0, "FREQ=WEEKLY;UNTIL=20300512"},
		{"stray separator", "FREQ=DAILY;;", "20300512", 0, "FREQ=DAILY;UNTIL=20300512"},
		{"count replaces until", "FREQ=WEEKLY;UNTIL=20310101T000000Z", "", 3, "FREQ=WEEKLY;COUNT=3"},
		{"count replaces count", "FREQ=WEEKLY;COUNT=9;BYDAY=MO", "", 4, "FREQ=WEEKLY;BYDAY=MO;COUNT=4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			if tc.count > 0 {
				got = SetCount(tc.rule, tc.count)
			} else {
				got = SetUntil(tc.rule, tc.until)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if _, err := rrule.StrToROption(got); err != nil {
				t.Errorf("result does not parse: %v", err)
			}
			if strings.Contains(got, "COUNT=") && strings.Contains(got, "UNTIL=") {
				t.Errorf("both COUNT and UNTIL in %q", got)
			}
		})
	}
	// A DATE UNTIL must survive as a DATE: rrule-go's own rendering would
	// write a DATE-TIME.
	if got := SetUntil("FREQ=DAILY", "20300512"); !strings.HasSuffix(got, "UNTIL=20300512") {
		t.Errorf("DATE UNTIL rewritten: %q", got)
	}
}

func TestRuleCount(t *testing.T) {
	for rule, want := range map[string]int{
		"FREQ=WEEKLY;COUNT=6": 6, "FREQ=WEEKLY": 0, "count=3;FREQ=DAILY": 3, "FREQ=DAILY;COUNT=x": 0, "FREQ=DAILY;COUNT=-2": 0,
	} {
		if got := RuleCount(rule); got != want {
			t.Errorf("RuleCount(%q) = %d, want %d", rule, got, want)
		}
	}
}

func TestInSeriesForm(t *testing.T) {
	slot := time.Date(2030, 11, 4, 8, 0, 0, 0, time.UTC) // 09:00 in Amsterdam
	for _, tc := range []struct {
		name      string
		dtstart   *ical.Prop
		wantValue string
		wantTZID  string
		wantType  string
	}{
		{"all-day", dtstartProp("20301014", "VALUE", "DATE"), "20301104", "", "DATE"},
		{"date without VALUE", dtstartProp("20301014"), "20301104", "", "DATE"},
		{"zoned", dtstartProp("20301014T090000", "TZID", "Europe/Amsterdam"), "20301104T090000", "Europe/Amsterdam", ""},
		{"utc", dtstartProp("20301014T080000Z"), "20301104T080000Z", "", ""},
		{"floating", dtstartProp("20301014T090000"), "20301104T080000", "", ""},
		{"non-IANA zone reads as UTC", dtstartProp("20301014T090000", "TZID", "W. Europe Standard Time"), "20301104T080000Z", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := InSeriesForm(ical.PropRecurrenceID, tc.dtstart, slot)
			if p.Name != ical.PropRecurrenceID || p.Value != tc.wantValue {
				t.Errorf("got %s %q, want %q", p.Name, p.Value, tc.wantValue)
			}
			if got := p.Params.Get("TZID"); got != tc.wantTZID {
				t.Errorf("TZID = %q, want %q", got, tc.wantTZID)
			}
			if got := p.Params.Get("VALUE"); got != tc.wantType {
				t.Errorf("VALUE = %q, want %q", got, tc.wantType)
			}
			// The expansion must read it back as the same instant (a date as
			// the same day).
			loc := ZoneOrUTC(tc.dtstart.Params.Get("TZID"))
			got, dateOnly, ok := PropInstant(p, loc)
			if !ok {
				t.Fatalf("PropInstant cannot read %q", p.Value)
			}
			if dateOnly {
				if got.Format("20060102") != "20301104" {
					t.Errorf("date read as %s", got)
				}
			} else if !got.Equal(slot) {
				t.Errorf("read back %s, want %s", got, slot)
			}
		})
	}
}

func TestUntilBefore(t *testing.T) {
	slot := time.Date(2030, 11, 4, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		dtstart *ical.Prop
		want    string
	}{
		{"all-day is the previous date", dtstartProp("20301014", "VALUE", "DATE"), "20301103"},
		{"zoned is UTC", dtstartProp("20301014T090000", "TZID", "Europe/Amsterdam"), "20301104T075959Z"},
		{"utc", dtstartProp("20301014T080000Z"), "20301104T075959Z"},
		{"floating stays floating", dtstartProp("20301014T080000"), "20301104T075959"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := UntilBefore(tc.dtstart, slot)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			rule := SetUntil("FREQ=WEEKLY", got)
			if _, err := rrule.StrToROption(rule); err != nil {
				t.Errorf("rule %q does not parse: %v", rule, err)
			}
		})
	}
	// Month and year boundaries.
	if got := UntilBefore(dtstartProp("20300101", "VALUE", "DATE"), time.Date(2030, 3, 1, 0, 0, 0, 0, time.UTC)); got != "20300228" {
		t.Errorf("1 March: %q", got)
	}
}

func TestPropInstant(t *testing.T) {
	ams, _ := time.LoadLocation("Europe/Amsterdam")
	for _, tc := range []struct {
		name     string
		p        *ical.Prop
		loc      *time.Location
		want     time.Time
		dateOnly bool
		ok       bool
	}{
		{"utc", dtstartProp("20301104T080000Z"), time.UTC, time.Date(2030, 11, 4, 8, 0, 0, 0, time.UTC), false, true},
		{"zoned", dtstartProp("20301104T090000", "TZID", "Europe/Amsterdam"), time.UTC, time.Date(2030, 11, 4, 8, 0, 0, 0, time.UTC), false, true},
		{"floating in series zone", dtstartProp("20301104T090000"), ams, time.Date(2030, 11, 4, 8, 0, 0, 0, time.UTC), false, true},
		{"date in series zone", dtstartProp("20301104", "VALUE", "DATE"), ams, time.Date(2030, 11, 3, 23, 0, 0, 0, time.UTC), true, true},
		{"garbage", dtstartProp("not-a-date"), time.UTC, time.Time{}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, dateOnly, ok := PropInstant(tc.p, tc.loc)
			if ok != tc.ok || dateOnly != tc.dateOnly || (ok && !got.Equal(tc.want)) {
				t.Errorf("got %v %v %v, want %v %v %v", got, dateOnly, ok, tc.want, tc.dateOnly, tc.ok)
			}
		})
	}
}

func TestSynthesise(t *testing.T) {
	start := time.Date(2030, 10, 14, 7, 0, 0, 0, time.UTC).Unix() // 09:00 Amsterdam (CEST)
	e := &bongov1.Event{
		Guid: "g1", Title: "Weekly", Description: "d", Location: "l", StartTime: start, EndTime: start + 3600,
		Rrule: "FREQ=WEEKLY", Timezone: "Europe/Amsterdam", Exdates: []string{"20301028T090000"},
	}
	cal := Synthesise(e)
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
	for name, want := range map[string]string{
		ical.PropUID: "g1", ical.PropSummary: "Weekly", ical.PropRecurrenceRule: "FREQ=WEEKLY",
		ical.PropDateTimeStart: "20301014T090000", ical.PropExceptionDates: "20301028T090000",
	} {
		if got := vevent.Props.Get(name); got == nil || got.Value != want {
			t.Errorf("%s = %+v, want %q", name, got, want)
		}
	}
	if got := vevent.Props.Get(ical.PropDateTimeStart).Params.Get("TZID"); got != "Europe/Amsterdam" {
		t.Errorf("DTSTART TZID = %q", got)
	}

	allDay := Synthesise(&bongov1.Event{
		Guid: "g2", Title: "Day", AllDay: true, StartTime: time.Date(2030, 10, 14, 0, 0, 0, 0, time.UTC).Unix(),
		EndTime: time.Date(2030, 10, 15, 0, 0, 0, 0, time.UTC).Unix(),
	})
	m, _ := Master(allDay)
	if p := m.Props.Get(ical.PropDateTimeStart); p == nil || p.Value != "20301014" || p.Params.Get("VALUE") != "DATE" {
		t.Errorf("all-day DTSTART = %+v", p)
	}
}
