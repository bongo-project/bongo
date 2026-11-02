package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func showAsRequest(extra map[string]string, allDay bool) *http.Request {
	f := url.Values{"title": {"T"}, "start_date": {"2030-05-06"}, "end_date": {"2030-05-06"}}
	if allDay {
		f.Set("all_day", "on")
	} else {
		f.Set("start_time", "09:00")
		f.Set("end_time", "10:00")
	}
	for k, v := range extra {
		f.Set(k, v)
	}
	return formReq(http.MethodPost, "/calendar/event", f)
}

func TestParseEventFormShowAs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		allDay bool
		extra  map[string]string
		want   bool // Transparent
		bad    bool
	}{
		{"free timed", false, map[string]string{"show_as": "free"}, true, false},
		{"busy timed", false, map[string]string{"show_as": "busy"}, false, false},
		{"free all-day", true, map[string]string{"show_as": "free"}, true, false},
		{"busy all-day overrides the default", true, map[string]string{"show_as": "busy"}, false, false},
		{"absent all-day defaults to free", true, nil, true, false},
		{"absent timed defaults to busy", false, nil, false, false},
		{"empty counts as absent, all-day", true, map[string]string{"show_as": ""}, true, false},
		{"empty counts as absent, timed", false, map[string]string{"show_as": ""}, false, false},
		{"invalid value", false, map[string]string{"show_as": "tentative"}, false, true},
		{"invalid value, all-day", true, map[string]string{"show_as": "FREE"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := parseEventForm(showAsRequest(tc.extra, tc.allDay), time.UTC)
			if tc.bad {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if ev.Transparent != tc.want {
				t.Errorf("Transparent = %v, want %v", ev.Transparent, tc.want)
			}
		})
	}
}

func TestCreateEventRejectsInvalidShowAs(t *testing.T) {
	e := newInstEnv(t)
	w := httptest.NewRecorder()
	e.h.CreateEvent(w, showAsRequest(map[string]string{"show_as": "tentative"}, false))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if n := e.count(t); n != 0 {
		t.Errorf("%d events stored, want none", n)
	}
}

func (e *instEnv) createShowAs(t *testing.T, title, date string, allDay bool, showAs string) string {
	t.Helper()
	f := url.Values{
		"title": {title}, "calendar_id": {"default"}, "reminder_minutes": {"0"},
		"start_date": {date}, "end_date": {date}, "start_time": {"09:00"}, "end_time": {"10:00"},
	}
	if allDay {
		f.Set("all_day", "on")
	}
	if showAs != "" {
		f.Set("show_as", showAs)
	}
	w := httptest.NewRecorder()
	e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", f))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	all, _, err := e.events.ListAll(quotaUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range all {
		if ev.Title == title {
			return ev.Guid
		}
	}
	t.Fatalf("event %q not stored", title)
	return ""
}

func (e *instEnv) caldavICS(t *testing.T, guid string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(e.caldavObject(t, guid).Data); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestShowAsRoundTripWebEvent(t *testing.T) {
	e := newInstEnv(t)
	guid := e.createShowAs(t, "Free one", "2030-05-06", false, "free")
	if !e.get(t, guid).Transparent {
		t.Fatal("stored event is not transparent")
	}
	if ics := e.caldavICS(t, guid); !strings.Contains(ics, "TRANSP:TRANSPARENT") {
		t.Errorf("CalDAV object lacks TRANSP:TRANSPARENT:\n%s", ics)
	}
	w := httptest.NewRecorder()
	e.h.ExportCalendar(w, withUserContext(httptest.NewRequest(http.MethodGet, "/calendar/export", nil), quotaUser, quotaEmail))
	if !strings.Contains(w.Body.String(), "TRANSP:TRANSPARENT") {
		t.Errorf("export lacks TRANSP:TRANSPARENT:\n%s", w.Body.String())
	}

	// Edit to Busy: the update takes the value from the form, not the stored event.
	f := url.Values{
		"title": {"Free one"}, "calendar_id": {"default"}, "reminder_minutes": {"0"},
		"start_date": {"2030-05-06"}, "end_date": {"2030-05-06"}, "start_time": {"09:00"}, "end_time": {"10:00"},
		"show_as": {"busy"},
	}
	if w := e.update(guid, f); w.Code != http.StatusSeeOther {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if e.get(t, guid).Transparent {
		t.Error("still transparent after editing to Busy")
	}
	if ics := e.caldavICS(t, guid); strings.Contains(ics, "TRANSP") {
		t.Errorf("CalDAV object still has TRANSP:\n%s", ics)
	}
	// And back to Free.
	f.Set("show_as", "free")
	if w := e.update(guid, f); w.Code != http.StatusSeeOther {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if !e.get(t, guid).Transparent {
		t.Error("not transparent after editing to Free")
	}
}

func masterTransp(t *testing.T, raw []byte) string {
	t.Helper()
	m, _ := rawParts(t, raw)
	if p := m.Props.Get(ical.PropTransparency); p != nil {
		return p.Value
	}
	return ""
}

func TestShowAsEditOfRawBackedEvent(t *testing.T) {
	e := newInstEnv(t)
	start := time.Date(2030, 10, 14, 9, 0, 0, 0, e.ams)
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Client//Cal//EN", "BEGIN:VEVENT", "UID:raw-1",
		"DTSTAMP:20300101T000000Z", "DTSTART;TZID=Europe/Amsterdam:20301014T090000",
		"DTEND;TZID=Europe/Amsterdam:20301014T100000", "SUMMARY:Raw", "TRANSP:TRANSPARENT", "X-FOO:bar",
		"END:VEVENT", "END:VCALENDAR", "",
	}, "\r\n")
	guid, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: "Raw", CalendarId: "default", Timezone: "Europe/Amsterdam", Transparent: true,
		StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(), IcalRaw: []byte(ics),
	})
	if err != nil {
		t.Fatal(err)
	}
	form := func(showAs string) url.Values {
		f := url.Values{
			"title": {"Raw"}, "calendar_id": {"default"}, "reminder_minutes": {"0"},
			"start_date": {"2030-10-14"}, "end_date": {"2030-10-14"}, "start_time": {"09:00"}, "end_time": {"10:00"},
		}
		if showAs != "" {
			f.Set("show_as", showAs)
		}
		return f
	}

	// Unchanged choice: TRANSP stays.
	if w := e.update(guid, form("free")); w.Code != http.StatusSeeOther {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if got := masterTransp(t, e.get(t, guid).IcalRaw); got != "TRANSPARENT" {
		t.Errorf("TRANSP = %q after a Free save, want TRANSPARENT", got)
	}

	// Busy removes it and leaves the rest alone.
	if w := e.update(guid, form("busy")); w.Code != http.StatusSeeOther {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	got := e.get(t, guid)
	if got.Transparent {
		t.Error("structured Transparent kept after Busy")
	}
	if v := masterTransp(t, got.IcalRaw); v != "" {
		t.Errorf("TRANSP = %q after Busy, want none", v)
	}
	if m, _ := rawParts(t, got.IcalRaw); m.Props.Get("X-FOO") == nil || m.Props.Get(ical.PropUID).Value != "raw-1" {
		t.Error("an unrelated property was lost")
	}

	// Free sets it again.
	if w := e.update(guid, form("free")); w.Code != http.StatusSeeOther {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if v := masterTransp(t, e.get(t, guid).IcalRaw); v != "TRANSPARENT" {
		t.Errorf("TRANSP = %q after Free, want TRANSPARENT", v)
	}
}

func TestShowAsOccurrenceOverride(t *testing.T) {
	k := kinds[0] // caldav timed
	for _, tc := range []struct {
		name        string
		masterExtra []string
		showAs      string
		wantMaster  string
		wantOver    string
	}{
		{"free override of a busy series", nil, "free", "", "TRANSPARENT"},
		{"busy override of a free series", []string{"TRANSP:TRANSPARENT"}, "busy", "TRANSPARENT", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", tc.masterExtra, nil)
			over := map[string]string{
				"occurrence": ridFor(k, "20301021"), "scope": "this", "show_as": tc.showAs,
			}
			w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Moved", "2030-10-21", over))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			master, overrides := rawParts(t, e.get(t, guid).IcalRaw)
			if len(overrides) != 1 {
				t.Fatalf("%d overrides, want 1", len(overrides))
			}
			val := func(c *ical.Component) string {
				if p := c.Props.Get(ical.PropTransparency); p != nil {
					return p.Value
				}
				return ""
			}
			if got := val(overrides[0]); got != tc.wantOver {
				t.Errorf("override TRANSP = %q, want %q", got, tc.wantOver)
			}
			if got := val(master); got != tc.wantMaster {
				t.Errorf("master TRANSP = %q, want %q (an instance edit must not touch the series)", got, tc.wantMaster)
			}
		})
	}
}

func TestShowAsFollowingStartsANewSeriesWithTheChoice(t *testing.T) {
	k := kinds[0]
	e := newInstEnv(t)
	guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
	over := map[string]string{"occurrence": ridFor(k, "20301021"), "scope": "following", "show_as": "free"}
	if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Later", "2030-10-21", over)); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	ns := e.get(t, e.otherGuid(t, guid))
	if !ns.Transparent {
		t.Error("the new series is not transparent")
	}
	if got := masterTransp(t, ns.IcalRaw); got != "TRANSPARENT" {
		t.Errorf("new series TRANSP = %q, want TRANSPARENT", got)
	}
	if got := masterTransp(t, e.get(t, guid).IcalRaw); got != "" {
		t.Errorf("the earlier series got TRANSP = %q", got)
	}
}

func TestFreeAllDayEventIsNotInFreeBusy(t *testing.T) {
	e := newInstEnv(t)
	e.createShowAs(t, "Holiday", "2030-05-06", true, "free")
	e.createShowAs(t, "Conference", "2030-05-08", true, "busy")
	e.createShowAs(t, "Defaulted", "2030-05-10", true, "") // no show_as: all-day means free

	w := httptest.NewRecorder()
	from, to := time.Date(2030, 5, 1, 0, 0, 0, 0, time.UTC), time.Date(2030, 5, 31, 0, 0, 0, 0, time.UTC)
	target := "/calendar/freebusy?user=" + quotaUser + "&start=" + strconv.FormatInt(from.Unix(), 10) + "&end=" + strconv.FormatInt(to.Unix(), 10)
	e.h.FreeBusy(w, withUserContext(httptest.NewRequest(http.MethodGet, target, nil), quotaUser, quotaEmail))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if n := strings.Count(body, `"start"`); n != 1 {
		t.Fatalf("%d busy blocks, want 1 (the Busy all-day event): %s", n, body)
	}
	day := func(d int) string {
		return `"start":` + strconv.FormatInt(time.Date(2030, 5, d, 0, 0, 0, 0, e.ams).Unix(), 10)
	}
	if !strings.Contains(body, day(8)) {
		t.Errorf("the Busy all-day event is missing (%s): %s", day(8), body)
	}
	for _, d := range []int{6, 10} {
		if strings.Contains(body, day(d)) {
			t.Errorf("a Free all-day event on 5/%d is busy: %s", d, body)
		}
	}
}
