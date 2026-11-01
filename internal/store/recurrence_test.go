package store

import (
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func ics(lines ...string) []byte {
	return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//T//EN\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VCALENDAR\r\n")
}

func utcAt(y int, m time.Month, d, h, min int) int64 {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC).Unix()
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("zone database lacks %s: %v", name, err)
	}
	return l
}

func days(evs []*bongov1.Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, time.Unix(e.StartTime, 0).UTC().Format("2006-01-02T15:04"))
	}
	return out
}

func TestExpandRecurrenceKeepsLocalTimeAcrossDST(t *testing.T) {
	ams := mustZone(t, "Europe/Amsterdam")
	// Monday 09:00 Amsterdam; the clocks go back on 2026-10-25 and forward on
	// 2027-03-28.
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, ams)
	ev := &bongov1.Event{
		Guid: "dst", Title: "Sync", StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
		Rrule: "FREQ=WEEKLY", Timezone: "Europe/Amsterdam",
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2027, 4, 15, 0, 0, 0, 0, time.UTC).Unix()
	got, err := ExpandRecurrence(ev, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 25 {
		t.Fatalf("got %d instances, want a half year of them", len(got))
	}
	var sawWinter, sawSummer bool
	for _, e := range got {
		l := time.Unix(e.StartTime, 0).In(ams)
		if l.Hour() != 9 || l.Minute() != 0 {
			t.Errorf("instance %s is %s local, want 09:00", e.RecurrenceId, l.Format("15:04"))
		}
		if end := time.Unix(e.EndTime, 0).In(ams); end.Hour() != 10 {
			t.Errorf("instance %s ends %s local, want 10:00", e.RecurrenceId, end.Format("15:04"))
		}
		_, off := l.Zone()
		sawWinter = sawWinter || off == 3600
		sawSummer = sawSummer || off == 7200
	}
	if !sawWinter || !sawSummer {
		t.Errorf("range did not cross both offsets (winter %v, summer %v)", sawWinter, sawSummer)
	}
}

func TestExpandRecurrenceZoneFallbacks(t *testing.T) {
	start := utcAt(2030, 1, 7, 9, 0)
	from, to := utcAt(2030, 1, 1, 0, 0), utcAt(2030, 1, 20, 0, 0)
	for _, tc := range []struct {
		name, tz string
		allDay   bool
	}{
		{"outlook name", "W. Europe Standard Time", false},
		{"mozilla prefix", "/mozilla.org/20050126_1/Europe/Amsterdam", false},
		{"injection", "Europe/Amsterdam\r\nX-EVIL:1", false},
		{"all-day ignores zone", "Pacific/Auckland", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := start
			if tc.allDay {
				st = utcAt(2030, 1, 7, 0, 0)
			}
			ev := &bongov1.Event{Guid: "z", StartTime: st, EndTime: st + 3600, Rrule: "FREQ=DAILY", Timezone: tc.tz, AllDay: tc.allDay}
			got, err := ExpandRecurrence(ev, from, to)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 13 {
				t.Fatalf("got %d instances, want 13", len(got))
			}
			for _, e := range got {
				if (e.StartTime-st)%86400 != 0 {
					t.Errorf("instance %d drifted from the UTC grid", e.StartTime)
				}
			}
		})
	}
}

func TestExpandRecurrenceIcalExdates(t *testing.T) {
	start := utcAt(2030, 5, 6, 9, 0) // Monday
	from, to := utcAt(2030, 5, 1, 0, 0), utcAt(2030, 6, 1, 0, 0)
	base := func(extra ...string) *bongov1.Event {
		lines := append([]string{
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z",
			"DTSTART:20300506T090000Z", "DTEND:20300506T100000Z", "RRULE:FREQ=WEEKLY;BYDAY=MO",
		}, extra...)
		lines = append(lines, "END:VEVENT")
		return &bongov1.Event{Guid: "x", StartTime: start, EndTime: start + 3600, Rrule: "FREQ=WEEKLY;BYDAY=MO", IcalRaw: ics(lines...)}
	}
	ams := mustZone(t, "Europe/Amsterdam")
	_ = ams
	for _, tc := range []struct {
		name  string
		extra []string
		want  []string
	}{
		{"none", nil, []string{"2030-05-06T09:00", "2030-05-13T09:00", "2030-05-20T09:00", "2030-05-27T09:00"}},
		{"one UTC", []string{"EXDATE:20300513T090000Z"}, []string{"2030-05-06T09:00", "2030-05-20T09:00", "2030-05-27T09:00"}},
		{"comma list", []string{"EXDATE:20300513T090000Z,20300527T090000Z"}, []string{"2030-05-06T09:00", "2030-05-20T09:00"}},
		{"two properties", []string{"EXDATE:20300513T090000Z", "EXDATE:20300520T090000Z"}, []string{"2030-05-06T09:00", "2030-05-27T09:00"}},
		{"TZID instant", []string{"EXDATE;TZID=Europe/Amsterdam:20300513T110000"}, []string{"2030-05-06T09:00", "2030-05-20T09:00", "2030-05-27T09:00"}},
		{"lower case name", []string{"exdate:20300513T090000Z"}, []string{"2030-05-06T09:00", "2030-05-20T09:00", "2030-05-27T09:00"}},
		{"date value", []string{"EXDATE;VALUE=DATE:20300513"}, []string{"2030-05-06T09:00", "2030-05-20T09:00", "2030-05-27T09:00"}},
		// An EXDATE at a different time of day is not this instance.
		{"other time same day", []string{"EXDATE:20300513T100000Z"}, []string{"2030-05-06T09:00", "2030-05-13T09:00", "2030-05-20T09:00", "2030-05-27T09:00"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandRecurrence(base(tc.extra...), from, to)
			if err != nil {
				t.Fatal(err)
			}
			if g := strings.Join(days(got), " "); g != strings.Join(tc.want, " ") {
				t.Errorf("got  %s\nwant %s", g, strings.Join(tc.want, " "))
			}
		})
	}
}

func TestExpandRecurrenceOverrides(t *testing.T) {
	start := utcAt(2030, 5, 6, 9, 0)
	ev := &bongov1.Event{
		Guid: "x", Title: "Team sync", Location: "Room 1", StartTime: start, EndTime: start + 3600,
		Rrule: "FREQ=WEEKLY;BYDAY=MO",
		IcalRaw: ics(
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "DTSTART:20300506T090000Z", "DTEND:20300506T100000Z",
			"SUMMARY:Team sync", "LOCATION:Room 1", "RRULE:FREQ=WEEKLY;BYDAY=MO", "END:VEVENT",
			// Moves the 13th to 15:00 with a new title.
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300513T090000Z",
			"DTSTART:20300513T150000Z", "DTEND:20300513T160000Z", "SUMMARY:Moved sync", "END:VEVENT",
			// The 20th is cancelled by the organiser.
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300520T090000Z",
			"DTSTART:20300520T090000Z", "DTEND:20300520T100000Z", "STATUS:CANCELLED", "SUMMARY:Gone", "END:VEVENT",
			// The 3rd of June moves forward into May, out of its own slot.
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300603T090000Z",
			"DTSTART:20300529T120000Z", "DTEND:20300529T130000Z", "SUMMARY:Early", "END:VEVENT",
		),
	}
	got, err := ExpandRecurrence(ev, utcAt(2030, 5, 1, 0, 0), utcAt(2030, 6, 1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if g, w := strings.Join(days(got), " "), "2030-05-06T09:00 2030-05-13T15:00 2030-05-27T09:00 2030-05-29T12:00"; g != w {
		t.Fatalf("got  %s\nwant %s", g, w)
	}
	moved := got[1]
	if moved.Title != "Moved sync" || moved.RecurrenceId != "20300513T090000Z" || moved.Guid != "x" {
		t.Errorf("moved instance: %+v", moved)
	}
	if moved.Location != "Room 1" {
		t.Errorf("an absent LOCATION must be inherited, got %q", moved.Location)
	}
	if early := got[3]; early.RecurrenceId != "20300603T090000Z" || early.Title != "Early" {
		t.Errorf("override moved in from outside the range: %+v", early)
	}

	// The week of the 13th shows the override only: the 09:00 slot is empty.
	wk, _ := ExpandRecurrence(ev, utcAt(2030, 5, 13, 8, 0), utcAt(2030, 5, 13, 10, 0))
	if len(wk) != 0 {
		t.Errorf("original slot should be empty, got %v", days(wk))
	}
	// And an override moved out of the range leaves its slot empty too.
	out, _ := ExpandRecurrence(ev, utcAt(2030, 6, 3, 8, 0), utcAt(2030, 6, 3, 10, 0))
	if len(out) != 0 {
		t.Errorf("slot of an override moved away should be empty, got %v", days(out))
	}
}

func TestExpandRecurrenceCountAppliesBeforeExclusions(t *testing.T) {
	start := utcAt(2030, 5, 6, 9, 0)
	ev := &bongov1.Event{
		Guid: "c", StartTime: start, EndTime: start + 3600, Rrule: "FREQ=WEEKLY;COUNT=3",
		IcalRaw: ics("BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "DTSTART:20300506T090000Z",
			"RRULE:FREQ=WEEKLY;COUNT=3", "EXDATE:20300513T090000Z", "END:VEVENT"),
	}
	got, _ := ExpandRecurrence(ev, utcAt(2030, 5, 1, 0, 0), utcAt(2030, 8, 1, 0, 0))
	if g := strings.Join(days(got), " "); g != "2030-05-06T09:00 2030-05-20T09:00" {
		t.Errorf("COUNT must not be refilled by an exclusion, got %s", g)
	}
}

func TestExpandRecurrenceStructuredExdates(t *testing.T) {
	ams := mustZone(t, "Europe/Amsterdam")
	start := time.Date(2030, 5, 6, 9, 0, 0, 0, ams)
	for _, tc := range []struct {
		name   string
		exdate string
		want   int
	}{
		{"local date-time", "20300513T090000", 3},
		{"UTC instant", "20300513T070000Z", 3},
		{"date", "20300513", 3},
		{"legacy date", "2030-05-13", 3},
		{"wrong time of day", "20300513T100000", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := &bongov1.Event{
				Guid: "s", StartTime: start.Unix(), EndTime: start.Unix() + 3600, Rrule: "FREQ=WEEKLY",
				Timezone: "Europe/Amsterdam", Exdates: []string{tc.exdate},
			}
			got, _ := ExpandRecurrence(ev, utcAt(2030, 5, 1, 0, 0), utcAt(2030, 6, 1, 0, 0))
			if len(got) != tc.want {
				t.Errorf("got %v, want %d instances", days(got), tc.want)
			}
		})
	}
}

func TestExpandRecurrenceAllDayExdate(t *testing.T) {
	start := utcAt(2030, 5, 6, 0, 0)
	ev := &bongov1.Event{
		Guid: "a", AllDay: true, StartTime: start, EndTime: start + 86400, Rrule: "FREQ=DAILY;COUNT=4", Timezone: "Pacific/Auckland",
		IcalRaw: ics("BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "DTSTART;VALUE=DATE:20300506",
			"RRULE:FREQ=DAILY;COUNT=4", "EXDATE;VALUE=DATE:20300507", "END:VEVENT"),
	}
	got, _ := ExpandRecurrence(ev, utcAt(2030, 5, 1, 0, 0), utcAt(2030, 6, 1, 0, 0))
	if g := strings.Join(days(got), " "); g != "2030-05-06T00:00 2030-05-08T00:00 2030-05-09T00:00" {
		t.Errorf("got %s", g)
	}
	if got[0].RecurrenceId != "20300506" {
		t.Errorf("all-day RecurrenceId = %q, want a date", got[0].RecurrenceId)
	}
}

func TestFindOccurrence(t *testing.T) {
	start := utcAt(2030, 5, 6, 9, 0)
	ev := &bongov1.Event{
		Guid: "x", Title: "Sync", StartTime: start, EndTime: start + 3600, Rrule: "FREQ=WEEKLY;BYDAY=MO",
		IcalRaw: ics(
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "DTSTART:20300506T090000Z", "RRULE:FREQ=WEEKLY;BYDAY=MO",
			"EXDATE:20300520T090000Z", "END:VEVENT",
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300513T090000Z",
			"DTSTART:20300513T150000Z", "DTEND:20300513T160000Z", "SUMMARY:Moved", "END:VEVENT",
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300603T090000Z",
			"STATUS:CANCELLED", "END:VEVENT"),
	}
	for _, tc := range []struct {
		name, rid string
		ok        bool
		title     string
		start     int64
	}{
		{"plain instance", "20300527T090000Z", true, "Sync", utcAt(2030, 5, 27, 9, 0)},
		{"the master slot", "20300506T090000Z", true, "Sync", start},
		{"override", "20300513T090000Z", true, "Moved", utcAt(2030, 5, 13, 15, 0)},
		{"excluded", "20300520T090000Z", false, "", 0},
		{"cancelled override", "20300603T090000Z", false, "", 0},
		{"not on the rule (a Tuesday)", "20300514T090000Z", false, "", 0},
		{"wrong time of day", "20300527T091500Z", false, "", 0},
		{"before the series", "20300429T090000Z", false, "", 0},
		{"a date for a timed series", "20300527", false, "", 0},
		{"garbage", "../../etc/passwd", false, "", 0},
		{"empty", "", false, "", 0},
		{"unterminated", "20300527T090000", false, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FindOccurrence(ev, tc.rid)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if got.Title != tc.title || got.StartTime != tc.start || got.Guid != "x" || got.RecurrenceId != tc.rid {
				t.Errorf("got %+v", got)
			}
		})
	}
	if _, ok := FindOccurrence(&bongov1.Event{Guid: "s", StartTime: start, EndTime: start + 1}, "20300506T090000Z"); ok {
		t.Error("a non-recurring event has no occurrences")
	}
}

func TestContainsFold(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{{"x\r\nEXDATE:1", true}, {"exdate:1", true}, {"ExDate", true}, {"EXDAT", false}, {"", false}} {
		if got := containsFold([]byte(tc.in), "EXDATE"); got != tc.want {
			t.Errorf("containsFold(%q) = %v", tc.in, got)
		}
	}
}

func TestListByDateRangeHonoursExdateFromIcal(t *testing.T) {
	es := newEventTestEnv(t)
	start := utcAt(2030, 5, 6, 9, 0)
	if _, err := es.Create("u1", &bongov1.Event{
		CalendarId: "default", Title: "Sync", StartTime: start, EndTime: start + 3600, Rrule: "FREQ=WEEKLY;BYDAY=MO",
		IcalRaw: ics("BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "DTSTART:20300506T090000Z",
			"RRULE:FREQ=WEEKLY;BYDAY=MO", "EXDATE:20300513T090000Z", "END:VEVENT"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := es.ListByDateRange("u1", utcAt(2030, 5, 13, 0, 0), utcAt(2030, 5, 14, 0, 0), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("the excluded day has %d events, want none", len(got))
	}
	got, _ = es.ListByDateRange("u1", utcAt(2030, 5, 20, 0, 0), utcAt(2030, 5, 21, 0, 0), "")
	if len(got) != 1 {
		t.Errorf("the next week has %d events, want 1", len(got))
	}
}

func TestExpandRecurrenceInstanceCarriesOrganiserAndAttendees(t *testing.T) {
	start := utcAt(2030, 5, 6, 9, 0)
	ev := &bongov1.Event{
		Guid: "x", Title: "Sync", StartTime: start, EndTime: start + 3600,
		Rrule:     "FREQ=WEEKLY;BYDAY=MO",
		Organizer: "boss@example.com",
		Sequence:  3,
		Status:    "CONFIRMED",
		Attendees: []*bongov1.Attendee{{Address: "a@example.com", Name: "A"}},
		IcalRaw:   ics("BEGIN:VEVENT", "UID:u", "END:VEVENT"),
		Exdates:   []string{"20300513T090000Z"},
	}
	got, err := ExpandRecurrence(ev, utcAt(2030, 5, 1, 0, 0), utcAt(2030, 5, 24, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d instances, want 2", len(got))
	}
	for _, inst := range got {
		if inst.Organizer != "boss@example.com" || inst.Sequence != 3 || inst.Status != "CONFIRMED" {
			t.Errorf("instance %s lost organiser/sequence/status: %+v", inst.RecurrenceId, inst)
		}
		if len(inst.Attendees) != 1 || inst.Attendees[0].Address != "a@example.com" {
			t.Errorf("instance %s attendees: %v", inst.RecurrenceId, inst.Attendees)
		}
		if inst.IcalRaw != nil || inst.Exdates != nil {
			t.Errorf("instance %s must not carry ical_raw or exdates", inst.RecurrenceId)
		}
	}
	// Instances must not alias the master's attendees.
	got[0].Attendees[0].Name = "changed"
	if ev.Attendees[0].Name != "A" {
		t.Error("instance shares attendee memory with the master")
	}
}

func TestExpandRecurrenceOverrideCarriesAttendeePartstat(t *testing.T) {
	start := utcAt(2030, 5, 6, 9, 0)
	ev := &bongov1.Event{
		Guid: "x", Title: "Sync", StartTime: start, EndTime: start + 3600,
		Rrule:     "FREQ=WEEKLY;BYDAY=MO",
		Attendees: []*bongov1.Attendee{{Address: "Me@Example.com", Partstat: "ACCEPTED"}, {Address: "b@example.com", Partstat: "ACCEPTED"}},
		IcalRaw: ics(
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "DTSTART:20300506T090000Z", "DTEND:20300506T100000Z",
			"RRULE:FREQ=WEEKLY;BYDAY=MO", "END:VEVENT",
			"BEGIN:VEVENT", "UID:u", "DTSTAMP:20300101T000000Z", "RECURRENCE-ID:20300513T090000Z",
			"DTSTART:20300513T090000Z", "DTEND:20300513T100000Z",
			"ATTENDEE;PARTSTAT=DECLINED:MAILTO:me@example.com", "END:VEVENT",
		),
	}
	got, err := ExpandRecurrence(ev, utcAt(2030, 5, 1, 0, 0), utcAt(2030, 5, 24, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d instances, want 3", len(got))
	}
	for _, inst := range got {
		want := "ACCEPTED"
		if inst.RecurrenceId == "20300513T090000Z" {
			want = "DECLINED"
		}
		if ps := inst.Attendees[0].Partstat; ps != want {
			t.Errorf("instance %s: my PARTSTAT %q, want %q", inst.RecurrenceId, ps, want)
		}
		if inst.Attendees[1].Partstat != "ACCEPTED" {
			t.Errorf("instance %s: another attendee changed to %q", inst.RecurrenceId, inst.Attendees[1].Partstat)
		}
	}
	if ev.Attendees[0].Partstat != "ACCEPTED" {
		t.Error("the master's attendee was changed")
	}
}
