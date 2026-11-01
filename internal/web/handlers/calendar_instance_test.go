package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	davcal "github.com/emersion/go-webdav/caldav"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/caldav"
	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// instApplier counts the Raft commands the handler issues and can fail one of
// them, to prove how many writes a scope makes and what is rolled back.
type instApplier struct {
	inner  store.RaftApplier
	calls  int
	failAt int         // 1-based number of the call that fails; 0 = none
	onCall func(n int) // runs before the n-th call
}

func (a *instApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	a.calls++
	if a.onCall != nil {
		a.onCall(a.calls)
	}
	if a.failAt == a.calls {
		return errors.New("injected apply failure")
	}
	return a.inner.ApplyCommand(cmd, d)
}

type instEnv struct {
	*agendaEnv
	ap     *instApplier
	events *store.EventStore
	h      *CalendarHandler
	ams    *time.Location
}

func newInstEnv(t *testing.T) *instEnv {
	t.Helper()
	base := newAgendaEnv(t, "Europe/Amsterdam")
	ap := &instApplier{inner: &directApplierTags{ps: base.ps}}
	ev := store.NewEventStore(base.ps, ap)
	h := NewCalendarHandler(ev, base.calendars, nil)
	ams := mustZone(t, "Europe/Amsterdam")
	h.SetZoneSource(fixedZone{ams})
	return &instEnv{agendaEnv: base, ap: ap, events: ev, h: h, ams: ams}
}

// fixture kinds: a CalDAV client's series (ical_raw) or a web-created one,
// timed in Europe/Amsterdam or all-day.
type kind struct {
	name   string
	allDay bool
	web    bool
}

var kinds = []kind{
	{"caldav timed", false, false},
	{"web timed", false, true},
	{"caldav all-day", true, false},
	{"web all-day", true, true},
}

const (
	itemWeekly = "Weekly"
	// Occurrence RECURRENCE-IDs: 21 October is CEST, 4 November is CET.
	ridOct21Timed = "20301021T070000Z"
	ridOct14Timed = "20301014T070000Z"
	ridNov04Timed = "20301104T080000Z"
)

func ridFor(k kind, day string) string {
	if k.allDay {
		return day
	}
	switch day {
	case "20301021":
		return ridOct21Timed
	case "20301104":
		return ridNov04Timed
	case "20301111":
		return "20301111T080000Z"
	case "20301118":
		return "20301118T080000Z"
	case "20301014":
		return "20301014T070000Z"
	}
	panic("no RID for " + day)
}

func icsFor(k kind, uid, rule string, masterExtra, components []string) []byte {
	start, end := "DTSTART;TZID=Europe/Amsterdam:20301014T090000", "DTEND;TZID=Europe/Amsterdam:20301014T100000"
	if k.allDay {
		start, end = "DTSTART;VALUE=DATE:20301014", "DTEND;VALUE=DATE:20301015"
	}
	lines := []string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Client//Cal//EN",
		"BEGIN:VEVENT", "UID:" + uid, "DTSTAMP:20300101T000000Z", start, end,
		"SUMMARY:" + itemWeekly, "RRULE:" + rule, "SEQUENCE:2", "X-FOO:bar",
	}
	lines = append(lines, masterExtra...)
	lines = append(lines, "END:VEVENT")
	lines = append(lines, components...)
	lines = append(lines, "END:VCALENDAR", "")
	return []byte(strings.Join(lines, "\r\n"))
}

// seed stores a weekly series starting Monday 14 October 2030 at 09:00 (or the
// date) and returns its GUID. extra lines go into the master VEVENT of a
// CalDAV series; components are further VEVENTs (overrides).
func (e *instEnv) seed(t *testing.T, k kind, rule string, masterExtra, components []string) string {
	t.Helper()
	defer func() { e.ap.calls = 0 }()
	if k.web {
		form := url.Values{
			"title": {itemWeekly}, "calendar_id": {"default"}, "reminder_minutes": {"0"}, "rrule": {rule},
			"start_date": {"2030-10-14"}, "start_time": {"09:00"}, "end_date": {"2030-10-14"}, "end_time": {"10:00"},
		}
		if k.allDay {
			form.Set("all_day", "on")
		}
		w := httptest.NewRecorder()
		e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", form))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		all, _, err := e.events.ListAll(quotaUser, 0, 0)
		if err != nil || len(all) != 1 {
			t.Fatalf("events = %d, %v", len(all), err)
		}
		return all[0].Guid
	}
	ev := &bongov1.Event{
		Title: itemWeekly, CalendarId: "default", Rrule: rule,
		IcalRaw: icsFor(k, "client-uid-"+k.name, rule, masterExtra, components),
	}
	if k.allDay {
		ev.AllDay = true
		ev.StartTime = utcUnix(2030, 10, 14, 0, 0)
		ev.EndTime = utcUnix(2030, 10, 15, 0, 0)
	} else {
		ev.Timezone = "Europe/Amsterdam"
		ev.StartTime = time.Date(2030, 10, 14, 9, 0, 0, 0, e.ams).Unix()
		ev.EndTime = time.Date(2030, 10, 14, 10, 0, 0, 0, e.ams).Unix()
	}
	guid, err := e.events.Create(quotaUser, ev)
	if err != nil {
		t.Fatal(err)
	}
	return guid
}

func (e *instEnv) get(t *testing.T, guid string) *bongov1.Event {
	t.Helper()
	ev, err := e.events.Get(quotaUser, guid)
	if err != nil {
		t.Fatalf("get %s: %v", guid, err)
	}
	return ev
}

func (e *instEnv) count(t *testing.T) int {
	t.Helper()
	return e.countEvents(t)
}

// otherGuid is the GUID of the one event that is not guid.
func (e *instEnv) otherGuid(t *testing.T, guid string) string {
	t.Helper()
	all, _, err := e.events.ListAll(quotaUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, ev := range all {
		if ev.Guid != guid {
			found = append(found, ev.Guid)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one other event, got %v", found)
	}
	return found[0]
}

// occurrence form: what the page submits for the occurrence on day.
func occurrenceForm(k kind, rule, title, day string, over map[string]string) url.Values {
	f := url.Values{
		"title": {title}, "calendar_id": {"default"}, "reminder_minutes": {"0"}, "rrule": {rule},
		"start_date": {day}, "end_date": {day},
	}
	if k.allDay {
		f.Set("all_day", "on")
	} else {
		f.Set("start_time", "09:00")
		f.Set("end_time", "10:00")
	}
	for key, v := range over {
		f.Set(key, v)
	}
	return f
}

func (e *instEnv) update(guid string, form url.Values) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.UpdateEvent(w, withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, form), guid))
	return w
}

func (e *instEnv) remove(guid string, q url.Values, htmx bool) *httptest.ResponseRecorder {
	req := withGUID(formReq(http.MethodDelete, "/calendar/event/"+guid+"?"+q.Encode(), nil), guid)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	e.h.DeleteEvent(w, req)
	return w
}

var (
	windowFrom = time.Date(2030, 10, 10, 0, 0, 0, 0, time.UTC)
	windowTo   = time.Date(2030, 11, 22, 0, 0, 0, 0, time.UTC)
)

func formatDays(occs []*bongov1.Event, ams *time.Location) []string {
	var out []string
	for _, o := range occs {
		if o.AllDay {
			out = append(out, time.Unix(o.StartTime, 0).UTC().Format("01-02")+" day "+o.Title)
		} else {
			out = append(out, time.Unix(o.StartTime, 0).In(ams).Format("01-02 15:04")+" "+o.Title)
		}
	}
	return out
}

// days lists the occurrences the views, free/busy and reminders would see.
func (e *instEnv) days(t *testing.T, guid string, to time.Time) []string {
	t.Helper()
	occs, err := store.ExpandRecurrence(e.get(t, guid), windowFrom.Unix(), to.Unix())
	if err != nil {
		t.Fatal(err)
	}
	return formatDays(occs, e.ams)
}

func weeklyDays(k kind, title string, dates ...string) []string {
	var out []string
	for _, d := range dates {
		if k.allDay {
			out = append(out, d+" day "+title)
		} else {
			out = append(out, d+" 09:00 "+title)
		}
	}
	return out
}

var allDates = []string{"10-14", "10-21", "10-28", "11-04", "11-11", "11-18"}

func assertDays(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("occurrences:\n got %v\nwant %v", got, want)
	}
}

func decodeICS(t *testing.T, raw []byte) *ical.Calendar {
	t.Helper()
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		t.Fatalf("ical_raw does not decode: %v\n%s", err, raw)
	}
	return cal
}

// rawParts returns the master VEVENT and the override VEVENTs of raw.
func rawParts(t *testing.T, raw []byte) (master *ical.Component, overrides []*ical.Component) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("event has no ical_raw")
	}
	for _, c := range decodeICS(t, raw).Children {
		if c.Name != ical.CompEvent {
			continue
		}
		if c.Props.Get(ical.PropRecurrenceID) != nil {
			overrides = append(overrides, c)
		} else {
			master = c
		}
	}
	if master == nil {
		t.Fatal("no master VEVENT")
	}
	return master, overrides
}

func (e *instEnv) caldavObject(t *testing.T, guid string) *davcal.CalendarObject {
	t.Helper()
	obj, err := e.backend.GetCalendarObject(caldav.WithUserID(context.Background(), quotaUser),
		"/caldav/"+quotaUser+"/calendars/default/"+guid+".ics", nil)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

// caldavDays expands what a CalDAV client receives for guid.
func (e *instEnv) caldavDays(t *testing.T, obj *davcal.CalendarObject, to time.Time) []string {
	t.Helper()
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(obj.Data); err != nil {
		t.Fatal(err)
	}
	ev, _ := icalevent.ToEvent(obj.Data)
	ev.Guid, ev.IcalRaw = "rt", buf.Bytes()
	occs, err := store.ExpandRecurrence(ev, windowFrom.Unix(), to.Unix())
	if err != nil {
		t.Fatal(err)
	}
	return formatDays(occs, e.ams)
}

func eventsEqualIgnoringStamps(a, b *bongov1.Event) bool {
	a, b = proto.Clone(a).(*bongov1.Event), proto.Clone(b).(*bongov1.Event)
	a.UpdatedAt, b.UpdatedAt = 0, 0
	return proto.Equal(a, b)
}

// --- this event: edit ----------------------------------------------------

func TestInstanceEditThis(t *testing.T) {
	for _, k := range kinds {
		for _, day := range []string{"20301021", "20301104"} { // either side of the DST change
			t.Run(k.name+" "+day, func(t *testing.T) {
				e := newInstEnv(t)
				guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
				before := e.get(t, guid)
				rid := ridFor(k, day)
				d := day[:4] + "-" + day[4:6] + "-" + day[6:]
				moved := time.Date(2030, 10, 22, 0, 0, 0, 0, time.UTC) // the day after 21 October
				if day == "20301104" {
					moved = time.Date(2030, 11, 5, 0, 0, 0, 0, time.UTC)
				}
				over := map[string]string{
					"occurrence": rid, "scope": "this", "start_date": moved.Format("2006-01-02"),
					"end_date": moved.Format("2006-01-02"),
				}
				if !k.allDay {
					over["start_time"], over["end_time"] = "14:30", "15:30"
				}
				w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Moved", d, over))
				if w.Code != http.StatusSeeOther {
					t.Fatalf("status = %d: %s", w.Code, w.Body.String())
				}
				if e.ap.calls != 1 {
					t.Errorf("Raft commands = %d, want one PUT", e.ap.calls)
				}
				if e.count(t) != 1 {
					t.Errorf("events = %d, want the series only", e.count(t))
				}

				// The structured master is untouched.
				after := e.get(t, guid)
				if after.Title != itemWeekly || after.Rrule != "FREQ=WEEKLY" || after.StartTime != before.StartTime || after.EndTime != before.EndTime {
					t.Errorf("master changed: %+v", after)
				}

				// One override in ical_raw, shaped as RFC 5545 asks.
				master, ovs := rawParts(t, after.IcalRaw)
				if len(ovs) != 1 {
					t.Fatalf("overrides = %d, want 1", len(ovs))
				}
				ov := ovs[0]
				rp := ov.Props.Get(ical.PropRecurrenceID)
				if k.allDay {
					if rp.Value != day || rp.Params.Get("VALUE") != "DATE" {
						t.Errorf("RECURRENCE-ID = %q %v, want a DATE %s", rp.Value, rp.Params, day)
					}
				} else {
					want := time.Date(2030, 10, 21, 9, 0, 0, 0, e.ams).Format("20060102T150405")
					if day == "20301104" {
						want = "20301104T090000"
					}
					if rp.Value != want || rp.Params.Get("TZID") != "Europe/Amsterdam" {
						t.Errorf("RECURRENCE-ID = %q %v, want %s local in Europe/Amsterdam", rp.Value, rp.Params, want)
					}
				}
				for _, name := range []string{ical.PropRecurrenceRule, ical.PropExceptionDates, ical.PropRecurrenceDates} {
					if ov.Props.Get(name) != nil {
						t.Errorf("override carries %s", name)
					}
				}
				if ov.Props.Get(ical.PropUID).Value != master.Props.Get(ical.PropUID).Value {
					t.Error("override UID differs from the master's")
				}
				if got := master.Props.Get(ical.PropRecurrenceRule).Value; got != "FREQ=WEEKLY" {
					t.Errorf("master RRULE = %q", got)
				}
				if got, want := sequenceValue(ov), sequenceValue(master)+1; got != want {
					t.Errorf("override SEQUENCE = %d, want %d", got, want)
				}
				if got := ov.Props.Get(ical.PropSummary).Value; got != "Moved" {
					t.Errorf("override SUMMARY = %q", got)
				}

				// Expansion: the instance is at its new time and its old slot is empty.
				want := weeklyDays(k, itemWeekly, allDates...)
				i := slices.IndexFunc(want, func(s string) bool { return strings.HasPrefix(s, d[5:]) })
				want = slices.Delete(want, i, i+1)
				if k.allDay {
					want = append(want, moved.Format("01-02")+" day Moved")
				} else {
					want = append(want, moved.Format("01-02")+" 14:30 Moved")
				}
				slices.Sort(want)
				assertDays(t, e.days(t, guid, windowTo), want)

				// CalDAV GET: one object holding the master and the override.
				obj := e.caldavObject(t, guid)
				var vevents int
				for _, c := range obj.Data.Children {
					if c.Name == ical.CompEvent {
						vevents++
					}
				}
				if vevents != 2 {
					t.Errorf("CalDAV object holds %d VEVENTs, want master and override", vevents)
				}
				assertDays(t, e.caldavDays(t, obj, windowTo), want)
			})
		}
	}
}

func sequenceValue(c *ical.Component) int {
	return sequenceOf(c)
}

func TestInstanceEditThisReplacesItsOverride(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			rid := ridFor(k, "20301104")
			var seqs []int
			for _, title := range []string{"First", "Second"} {
				w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", title, "2030-11-04", map[string]string{"occurrence": rid, "scope": "this"}))
				if w.Code != http.StatusSeeOther {
					t.Fatalf("%s: %d %s", title, w.Code, w.Body.String())
				}
				_, ovs := rawParts(t, e.get(t, guid).IcalRaw)
				if len(ovs) != 1 {
					t.Fatalf("%s: overrides = %d, want the one replaced", title, len(ovs))
				}
				seqs = append(seqs, sequenceValue(ovs[0]))
			}
			if seqs[1] <= seqs[0] {
				t.Errorf("SEQUENCE did not increase: %v", seqs)
			}
			got := e.days(t, guid, windowTo)
			if !slices.Contains(got, weeklyDays(k, "Second", "11-04")[0]) {
				t.Errorf("second title missing from %v", got)
			}
			if slices.ContainsFunc(got, func(s string) bool { return strings.HasSuffix(s, "First") }) {
				t.Errorf("first override still listed: %v", got)
			}
		})
	}
}

// --- this event: delete --------------------------------------------------

func TestInstanceDeleteThis(t *testing.T) {
	for _, k := range kinds {
		for _, day := range []string{"20301021", "20301104"} {
			t.Run(k.name+" "+day, func(t *testing.T) {
				e := newInstEnv(t)
				guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
				rid := ridFor(k, day)
				w := e.remove(guid, url.Values{"occurrence": {rid}, "scope": {"this"}}, false)
				if w.Code != http.StatusSeeOther {
					t.Fatalf("status = %d: %s", w.Code, w.Body.String())
				}
				if e.ap.calls != 1 || e.count(t) != 1 {
					t.Errorf("commands = %d, events = %d; want one PUT and the series kept", e.ap.calls, e.count(t))
				}
				ev := e.get(t, guid)
				dayShort := day[4:6] + "-" + day[6:]
				var want []string
				for _, d := range allDates {
					if d != dayShort {
						want = append(want, d)
					}
				}
				assertDays(t, e.days(t, guid, windowTo), weeklyDays(k, itemWeekly, want...))

				if k.web {
					// No ical_raw: the exclusion is in the structured Exdates, which
					// CalDAV GET turns into an EXDATE.
					if len(ev.IcalRaw) != 0 || !slices.Equal(ev.Exdates, []string{rid}) {
						t.Errorf("raw = %d bytes, Exdates = %v; want none and [%s]", len(ev.IcalRaw), ev.Exdates, rid)
					}
				} else {
					master, ovs := rawParts(t, ev.IcalRaw)
					ex := master.Props.Get(ical.PropExceptionDates)
					if ex == nil || len(ovs) != 0 {
						t.Fatalf("EXDATE = %v, overrides = %d", ex, len(ovs))
					}
					if k.allDay {
						if ex.Value != day || ex.Params.Get("VALUE") != "DATE" {
							t.Errorf("EXDATE = %q %v", ex.Value, ex.Params)
						}
					} else if ex.Params.Get("TZID") != "Europe/Amsterdam" || !strings.HasPrefix(ex.Value, day+"T090000") {
						t.Errorf("EXDATE = %q %v, want 09:00 local", ex.Value, ex.Params)
					}
				}
				assertDays(t, e.caldavDays(t, e.caldavObject(t, guid), windowTo), weeklyDays(k, itemWeekly, want...))
			})
		}
	}
}

func TestInstanceDeleteThisRemovesItsOverride(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			rid := ridFor(k, "20301104")
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Moved", "2030-11-04", map[string]string{"occurrence": rid, "scope": "this"})); w.Code != http.StatusSeeOther {
				t.Fatalf("edit: %d %s", w.Code, w.Body.String())
			}
			e.ap.calls = 0
			if w := e.remove(guid, url.Values{"occurrence": {rid}, "scope": {"this"}}, true); w.Code != http.StatusOK || w.Header().Get("HX-Redirect") != "/calendar" {
				t.Fatalf("delete: %d %v", w.Code, w.Header())
			}
			if e.ap.calls != 1 {
				t.Errorf("commands = %d, want one PUT", e.ap.calls)
			}
			master, ovs := rawParts(t, e.get(t, guid).IcalRaw)
			if len(ovs) != 0 || master.Props.Get(ical.PropExceptionDates) == nil {
				t.Errorf("overrides = %d, EXDATE = %v; want the override gone and an EXDATE", len(ovs), master.Props.Get(ical.PropExceptionDates))
			}
			assertDays(t, e.days(t, guid, windowTo), weeklyDays(k, itemWeekly, "10-14", "10-21", "10-28", "11-11", "11-18"))
		})
	}
}

// --- this and following --------------------------------------------------

func untilWant(k kind) string {
	if k.allDay {
		return "UNTIL=20301103"
	}
	return "UNTIL=20301104T075959Z"
}

func TestInstanceEditFollowing(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			before := e.get(t, guid)
			over := map[string]string{"occurrence": ridFor(k, "20301104"), "scope": "following"}
			if !k.allDay {
				over["start_time"], over["end_time"] = "10:00", "11:00"
			}
			w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Following", "2030-11-04", over))
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if e.ap.calls != 2 {
				t.Errorf("Raft commands = %d, want two PUTs", e.ap.calls)
			}
			if e.count(t) != 2 {
				t.Fatalf("events = %d, want the original and the new series", e.count(t))
			}

			// The original ends before the instance, in the structured rule the
			// expansion reads as well as in ical_raw.
			orig := e.get(t, guid)
			if !strings.Contains(orig.Rrule, untilWant(k)) || strings.Contains(orig.Rrule, "COUNT=") {
				t.Errorf("structured Rrule = %q, want %s", orig.Rrule, untilWant(k))
			}
			if orig.StartTime != before.StartTime || orig.Title != itemWeekly {
				t.Errorf("original changed beyond its end: %+v", orig)
			}
			if !k.web {
				master, _ := rawParts(t, orig.IcalRaw)
				if got := master.Props.Get(ical.PropRecurrenceRule).Value; !strings.Contains(got, untilWant(k)) {
					t.Errorf("raw RRULE = %q, want %s", got, untilWant(k))
				}
				if got, want := sequenceValue(master), 3; got != want {
					t.Errorf("original SEQUENCE = %d, want %d", got, want)
				}
			}
			assertDays(t, e.days(t, guid, windowTo), weeklyDays(k, itemWeekly, "10-14", "10-21", "10-28"))

			// The new series: a new GUID (and UID), the form's fields, from the instance.
			ng := e.otherGuid(t, guid)
			ns := e.get(t, ng)
			if ns.Title != "Following" || ns.Rrule != "FREQ=WEEKLY" {
				t.Errorf("new series = %q %q", ns.Title, ns.Rrule)
			}
			var want []string
			for _, d := range []string{"11-04", "11-11", "11-18"} {
				if k.allDay {
					want = append(want, d+" day Following")
				} else {
					want = append(want, d+" 10:00 Following")
				}
			}
			assertDays(t, e.days(t, ng, windowTo), want)
			if !k.web {
				master, ovs := rawParts(t, ns.IcalRaw)
				if got := master.Props.Get(ical.PropUID).Value; got != ng {
					t.Errorf("new UID = %q, want the new GUID %q", got, ng)
				}
				if len(ovs) != 0 || sequenceValue(master) != 0 {
					t.Errorf("new series: overrides = %d, SEQUENCE = %d; want none and 0", len(ovs), sequenceValue(master))
				}
				if master.Props.Get("X-FOO") == nil {
					t.Error("the new series lost the original's properties")
				}
				if got := master.Props.Get(ical.PropSummary).Value; got != "Following" {
					t.Errorf("new SUMMARY = %q", got)
				}
			}
		})
	}
}

func TestInstanceFollowingWithCountExdateAndOverride(t *testing.T) {
	override := []string{
		"BEGIN:VEVENT", "UID:client-uid-caldav timed", "DTSTAMP:20300101T000000Z",
		"RECURRENCE-ID;TZID=Europe/Amsterdam:20301118T090000",
		"DTSTART;TZID=Europe/Amsterdam:20301118T120000", "DTEND;TZID=Europe/Amsterdam:20301118T130000",
		"SUMMARY:Special", "END:VEVENT",
	}
	oldOverride := []string{
		"BEGIN:VEVENT", "UID:client-uid-caldav timed", "DTSTAMP:20300101T000000Z",
		"RECURRENCE-ID;TZID=Europe/Amsterdam:20301014T090000",
		"DTSTART;TZID=Europe/Amsterdam:20301014T080000", "DTEND;TZID=Europe/Amsterdam:20301014T090000",
		"SUMMARY:Early", "END:VEVENT",
	}
	exdates := []string{"EXDATE;TZID=Europe/Amsterdam:20301021T090000", "EXDATE;TZID=Europe/Amsterdam:20301111T090000"}
	k := kinds[0]
	farTo := time.Date(2031, 1, 31, 0, 0, 0, 0, time.UTC)

	t.Run("same time of day keeps what is still on the rule", func(t *testing.T) {
		e := newInstEnv(t)
		guid := e.seed(t, k, "FREQ=WEEKLY;COUNT=6", exdates, append(slices.Clone(override), oldOverride...))
		over := map[string]string{"occurrence": ridNov04Timed, "scope": "following"}
		if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY;COUNT=6", "Following", "2030-11-04", over)); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		// Original: instances before the split only; its EXDATE of 21 Oct and
		// override of 14 Oct stay, the later ones left it.
		orig := e.get(t, guid)
		if strings.Contains(orig.Rrule, "COUNT") || !strings.Contains(orig.Rrule, "UNTIL=20301104T075959Z") {
			t.Errorf("original Rrule = %q", orig.Rrule)
		}
		master, ovs := rawParts(t, orig.IcalRaw)
		if len(ovs) != 1 || ovs[0].Props.Get(ical.PropSummary).Value != "Early" {
			t.Errorf("original overrides = %d, want only the earlier one", len(ovs))
		}
		if exd := master.Props.Values(ical.PropExceptionDates); len(exd) != 1 || exd[0].Value != "20301021T090000" {
			t.Errorf("original EXDATEs = %v, want only 21 Oct", exd)
		}
		assertDays(t, e.days(t, guid, farTo), []string{"10-14 08:00 Early", "10-28 09:00 Weekly"})

		// New series: COUNT 6 - 3 before = 3 (4, 11, 18 Nov); 11 Nov is
		// excluded and 18 Nov overridden, both still on the rule.
		ng := e.otherGuid(t, guid)
		ns := e.get(t, ng)
		if ns.Rrule != "FREQ=WEEKLY;COUNT=3" {
			t.Errorf("new Rrule = %q, want COUNT reduced by the instances before the split", ns.Rrule)
		}
		nm, nov := rawParts(t, ns.IcalRaw)
		if exd := nm.Props.Values(ical.PropExceptionDates); len(exd) != 1 || exd[0].Value != "20301111T090000" {
			t.Errorf("new EXDATEs = %v, want 11 Nov", exd)
		}
		if len(nov) != 1 || nov[0].Props.Get(ical.PropUID).Value != ng {
			t.Errorf("new overrides = %d, want the 18 Nov one with the new UID", len(nov))
		}
		assertDays(t, e.days(t, ng, farTo), []string{"11-04 09:00 Following", "11-18 12:00 Special"})
	})

	t.Run("changed time of day drops what no longer matches", func(t *testing.T) {
		e := newInstEnv(t)
		guid := e.seed(t, k, "FREQ=WEEKLY;COUNT=6", exdates, override)
		over := map[string]string{"occurrence": ridNov04Timed, "scope": "following", "start_time": "10:00", "end_time": "11:00"}
		if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY;COUNT=6", "Following", "2030-11-04", over)); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		ng := e.otherGuid(t, guid)
		nm, nov := rawParts(t, e.get(t, ng).IcalRaw)
		if len(nov) != 0 || nm.Props.Get(ical.PropExceptionDates) != nil {
			t.Errorf("new series carries %d overrides and EXDATE %v for instances it does not have", len(nov), nm.Props.Get(ical.PropExceptionDates))
		}
		assertDays(t, e.days(t, ng, farTo), []string{"11-04 10:00 Following", "11-11 10:00 Following", "11-18 10:00 Following"})
	})

	t.Run("web series with COUNT and structured exclusions", func(t *testing.T) {
		e := newInstEnv(t)
		guid := e.seed(t, kinds[1], "FREQ=WEEKLY;COUNT=6", nil, nil)
		ev := e.get(t, guid)
		ev.Exdates = []string{"20301021T090000", "20301111T090000"}
		if err := e.events.Update(quotaUser, guid, ev); err != nil {
			t.Fatal(err)
		}
		e.ap.calls = 0
		over := map[string]string{"occurrence": ridNov04Timed, "scope": "following"}
		if w := e.update(guid, occurrenceForm(kinds[1], "FREQ=WEEKLY;COUNT=6", "Following", "2030-11-04", over)); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		orig := e.get(t, guid)
		if !slices.Equal(orig.Exdates, []string{"20301021T090000"}) {
			t.Errorf("original Exdates = %v, want the earlier one only", orig.Exdates)
		}
		assertDays(t, e.days(t, guid, farTo), []string{"10-14 09:00 Weekly", "10-28 09:00 Weekly"})
		ng := e.otherGuid(t, guid)
		ns := e.get(t, ng)
		if ns.Rrule != "FREQ=WEEKLY;COUNT=3" || !slices.Equal(ns.Exdates, []string{"20301111T090000"}) || len(ns.IcalRaw) != 0 {
			t.Errorf("new series: %q %v raw=%d", ns.Rrule, ns.Exdates, len(ns.IcalRaw))
		}
		assertDays(t, e.days(t, ng, farTo), []string{"11-04 09:00 Following", "11-18 09:00 Following"})
	})
}

func TestInstanceFollowingFromTheFirstInstanceIsTheWholeSeries(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name+" edit", func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			over := map[string]string{"occurrence": ridFor(k, "20301014"), "scope": "following"}
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Whole", "2030-10-14", over)); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if e.count(t) != 1 || e.ap.calls != 1 {
				t.Errorf("events = %d, commands = %d; want the one series, one PUT", e.count(t), e.ap.calls)
			}
			ev := e.get(t, guid)
			if strings.Contains(ev.Rrule, "UNTIL") {
				t.Errorf("Rrule = %q, an empty series would be left", ev.Rrule)
			}
			assertDays(t, e.days(t, guid, windowTo), weeklyDays(k, "Whole", allDates...))
		})
		t.Run(k.name+" delete", func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			if w := e.remove(guid, url.Values{"occurrence": {ridFor(k, "20301014")}, "scope": {"following"}}, false); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if e.count(t) != 0 {
				t.Errorf("events = %d, want the series deleted", e.count(t))
			}
		})
	}
}

func TestInstanceDeleteFollowing(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			w := e.remove(guid, url.Values{"occurrence": {ridFor(k, "20301104")}, "scope": {"following"}}, false)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if e.ap.calls != 1 || e.count(t) != 1 {
				t.Errorf("commands = %d, events = %d; want one PUT and the series kept", e.ap.calls, e.count(t))
			}
			ev := e.get(t, guid)
			if !strings.Contains(ev.Rrule, untilWant(k)) {
				t.Errorf("structured Rrule = %q, want %s", ev.Rrule, untilWant(k))
			}
			if !k.web {
				master, _ := rawParts(t, ev.IcalRaw)
				if got := master.Props.Get(ical.PropRecurrenceRule).Value; !strings.Contains(got, untilWant(k)) {
					t.Errorf("raw RRULE = %q", got)
				}
			}
			assertDays(t, e.days(t, guid, windowTo), weeklyDays(k, itemWeekly, "10-14", "10-21", "10-28"))
		})
	}
}

// --- compensation ---------------------------------------------------------

func TestInstanceEditFollowingRollsBack(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY;COUNT=6", nil, nil)
			before := e.get(t, guid)
			e.ap.failAt = 2 // the Create of the new series
			over := map[string]string{"occurrence": ridFor(k, "20301104"), "scope": "following"}
			w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY;COUNT=6", "Following", "2030-11-04", over))
			if w.Code < 500 {
				t.Fatalf("status = %d, want a failure", w.Code)
			}
			if e.ap.calls != 3 {
				t.Errorf("commands = %d, want the UNTIL write, the failed create and the restore", e.ap.calls)
			}
			if e.count(t) != 1 {
				t.Errorf("events = %d, want the original only", e.count(t))
			}
			after := e.get(t, guid)
			if !eventsEqualIgnoringStamps(before, after) {
				t.Errorf("original not restored:\n before %v\n after  %v", before, after)
			}
			if strings.Contains(after.Rrule, "UNTIL") {
				t.Errorf("structured Rrule = %q still has the UNTIL", after.Rrule)
			}
		})
	}
}

func TestInstanceRollbackSparesAConcurrentWrite(t *testing.T) {
	e := newInstEnv(t)
	k := kinds[0]
	guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
	other := store.NewEventStore(e.ps, &directApplierTags{ps: e.ps})
	e.ap.failAt = 2
	e.ap.onCall = func(n int) {
		if n != 2 {
			return
		}
		ev, err := other.Get(quotaUser, guid)
		if err != nil {
			t.Error(err)
			return
		}
		ev.Title = "Edited by a CalDAV client"
		if err := other.Update(quotaUser, guid, ev); err != nil {
			t.Error(err)
		}
	}
	over := map[string]string{"occurrence": ridNov04Timed, "scope": "following"}
	w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Following", "2030-11-04", over))
	if w.Code < 500 {
		t.Fatalf("status = %d, want a failure", w.Code)
	}
	if e.ap.calls != 2 {
		t.Errorf("commands = %d: the restore must not run over a record that changed", e.ap.calls)
	}
	if got := e.get(t, guid).Title; got != "Edited by a CalDAV client" {
		t.Errorf("title = %q, the concurrent write was overwritten", got)
	}
}

func TestInstanceRollbackLeavesADeletedSeriesDeleted(t *testing.T) {
	e := newInstEnv(t)
	k := kinds[0]
	guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
	e.ap.failAt = 2
	e.ap.onCall = func(n int) {
		if n == 2 {
			_ = store.NewEventStore(e.ps, &directApplierTags{ps: e.ps}).Delete(quotaUser, guid)
		}
	}
	over := map[string]string{"occurrence": ridNov04Timed, "scope": "following"}
	if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Following", "2030-11-04", over)); w.Code < 500 {
		t.Fatalf("status = %d", w.Code)
	}
	if e.count(t) != 0 {
		t.Errorf("events = %d, the restore resurrected a deleted series", e.count(t))
	}
}

// --- refusals --------------------------------------------------------------

func TestInstanceRefusesWhatIsNotAnOccurrence(t *testing.T) {
	for _, k := range kinds[:1] {
		rids := map[string]string{
			"off the rule":      "20301105T080000Z",
			"off by a second":   "20301104T080001Z",
			"a date for timed":  "20301104",
			"garbage":           "yesterday",
			"header injection":  "20301104T080000Z\r\nX-EVIL:1",
			"property injected": "20301104T080000Z;X-EVIL=1",
			"wrong zone":        "20301104T090000Z",
		}
		for name, rid := range rids {
			for _, scope := range []string{"this", "following"} {
				t.Run(name+" "+scope, func(t *testing.T) {
					e := newInstEnv(t)
					guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
					before := e.get(t, guid)
					over := map[string]string{"occurrence": rid, "scope": scope}
					if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "X", "2030-11-04", over)); w.Code != http.StatusNotFound {
						t.Errorf("edit status = %d, want 404", w.Code)
					}
					if w := e.remove(guid, url.Values{"occurrence": {rid}, "scope": {scope}}, false); w.Code != http.StatusNotFound {
						t.Errorf("delete status = %d, want 404", w.Code)
					}
					if e.ap.calls != 0 || !proto.Equal(before, e.get(t, guid)) {
						t.Errorf("commands = %d, record changed: the refusal must not write", e.ap.calls)
					}
				})
			}
		}
	}
}

func TestInstanceRefusesAnExcludedOrCancelledOccurrence(t *testing.T) {
	cancelled := []string{
		"BEGIN:VEVENT", "UID:client-uid-caldav timed", "DTSTAMP:20300101T000000Z",
		"RECURRENCE-ID;TZID=Europe/Amsterdam:20301111T090000", "DTSTART;TZID=Europe/Amsterdam:20301111T090000",
		"STATUS:CANCELLED", "SUMMARY:Gone", "END:VEVENT",
	}
	e := newInstEnv(t)
	k := kinds[0]
	guid := e.seed(t, k, "FREQ=WEEKLY", []string{"EXDATE;TZID=Europe/Amsterdam:20301021T090000"}, cancelled)
	for name, rid := range map[string]string{"excluded": ridOct21Timed, "cancelled": "20301111T080000Z"} {
		for _, scope := range []string{"this", "following"} {
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "X", "2030-11-04", map[string]string{"occurrence": rid, "scope": scope})); w.Code != http.StatusNotFound {
				t.Errorf("%s %s: edit status = %d, want 404", name, scope, w.Code)
			}
			if w := e.remove(guid, url.Values{"occurrence": {rid}, "scope": {scope}}, false); w.Code != http.StatusNotFound {
				t.Errorf("%s %s: delete status = %d, want 404", name, scope, w.Code)
			}
		}
	}
	if e.ap.calls != 0 {
		t.Errorf("commands = %d, want none", e.ap.calls)
	}
}

func TestInstanceUnknownScopeIsRefusedAndNoOccurrenceIgnoresScope(t *testing.T) {
	k := kinds[0]
	t.Run("unknown scope", func(t *testing.T) {
		e := newInstEnv(t)
		guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
		for _, scope := range []string{"everything", "THIS", "this,all", "following\r\nX:1"} {
			over := map[string]string{"occurrence": ridNov04Timed, "scope": scope}
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "X", "2030-11-04", over)); w.Code != http.StatusBadRequest {
				t.Errorf("edit scope %q: status = %d, want 400", scope, w.Code)
			}
			if w := e.remove(guid, url.Values{"occurrence": {ridNov04Timed}, "scope": {scope}}, false); w.Code != http.StatusBadRequest {
				t.Errorf("delete scope %q: status = %d, want 400", scope, w.Code)
			}
		}
		if e.ap.calls != 0 || e.count(t) != 1 {
			t.Errorf("commands = %d, events = %d; a refused scope must not write", e.ap.calls, e.count(t))
		}
	})
	t.Run("no occurrence", func(t *testing.T) {
		e := newInstEnv(t)
		guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
		form := occurrenceForm(k, "FREQ=WEEKLY", "Series", "2030-10-14", map[string]string{"scope": "this"})
		if w := e.update(guid, form); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		ev := e.get(t, guid)
		if ev.Title != "Series" {
			t.Errorf("title = %q, want the series edited", ev.Title)
		}
		if _, ovs := rawParts(t, ev.IcalRaw); len(ovs) != 0 {
			t.Errorf("overrides = %d, scope must be ignored without an occurrence", len(ovs))
		}
		if w := e.remove(guid, url.Values{"scope": {"this"}}, false); w.Code != http.StatusSeeOther || e.count(t) != 0 {
			t.Errorf("delete without occurrence: %d, events = %d; want the series deleted", w.Code, e.count(t))
		}
	})
	t.Run("delete of a missing event", func(t *testing.T) {
		e := newInstEnv(t)
		if w := e.remove("nope", url.Values{"occurrence": {ridNov04Timed}, "scope": {"this"}}, false); w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

// --- all events from an occurrence --------------------------------------

func TestInstanceEditAllFromAnOccurrenceKeepsTheSeriesDate(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name+" untouched times", func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			before := e.get(t, guid)
			over := map[string]string{"occurrence": ridFor(k, "20301104"), "scope": "all"}
			if w := e.update(guid, occurrenceForm(k, "FREQ=WEEKLY", "Renamed", "2030-11-04", over)); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			after := e.get(t, guid)
			if after.StartTime != before.StartTime || after.EndTime != before.EndTime {
				t.Errorf("series moved to %d-%d, want %d-%d", after.StartTime, after.EndTime, before.StartTime, before.EndTime)
			}
			assertDays(t, e.days(t, guid, windowTo), weeklyDays(k, "Renamed", allDates...))
		})
	}
	t.Run("timed, new time of day", func(t *testing.T) {
		e := newInstEnv(t)
		guid := e.seed(t, kinds[0], "FREQ=WEEKLY", nil, nil)
		over := map[string]string{"occurrence": ridNov04Timed, "scope": "all", "start_time": "11:00", "end_time": "12:30"}
		if w := e.update(guid, occurrenceForm(kinds[0], "FREQ=WEEKLY", "Weekly", "2030-11-04", over)); w.Code != http.StatusSeeOther {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		after := e.get(t, guid)
		if want := time.Date(2030, 10, 14, 11, 0, 0, 0, e.ams).Unix(); after.StartTime != want {
			t.Errorf("start = %v, want 14 October 11:00", time.Unix(after.StartTime, 0).In(e.ams))
		}
		if after.EndTime-after.StartTime != 90*60 {
			t.Errorf("duration = %ds, want 90 minutes", after.EndTime-after.StartTime)
		}
	})
	t.Run("a stale occurrence is refused", func(t *testing.T) {
		e := newInstEnv(t)
		guid := e.seed(t, kinds[0], "FREQ=WEEKLY", nil, nil)
		over := map[string]string{"occurrence": "20301105T080000Z", "scope": "all"}
		if w := e.update(guid, occurrenceForm(kinds[0], "FREQ=WEEKLY", "X", "2030-11-05", over)); w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

func TestRebindToSeries(t *testing.T) {
	ams := mustZone(t, "Europe/Amsterdam")
	series := &bongov1.Event{StartTime: time.Date(2030, 10, 14, 9, 0, 0, 0, ams).Unix(), EndTime: time.Date(2030, 10, 14, 10, 0, 0, 0, ams).Unix()}
	occ := &bongov1.Event{StartTime: time.Date(2030, 11, 4, 9, 0, 0, 0, ams).Unix(), EndTime: time.Date(2030, 11, 4, 10, 0, 0, 0, ams).Unix()}
	at := func(m time.Month, d, h, min int) int64 { return time.Date(2030, m, d, h, min, 0, 0, ams).Unix() }
	for _, tc := range []struct {
		name         string
		start, end   int64
		wantS, wantE int64
	}{
		{"untouched keeps the series exactly", occ.StartTime, occ.EndTime, series.StartTime, series.EndTime},
		{"later start keeps the series date", at(11, 4, 11, 15), at(11, 4, 12, 45), at(10, 14, 11, 15), at(10, 14, 12, 45)},
		{"end on the next day", at(11, 4, 22, 0), at(11, 5, 1, 30), at(10, 14, 22, 0), at(10, 15, 1, 30)},
		{"a date moved in the form does not move the series", at(11, 6, 9, 30), at(11, 6, 10, 30), at(10, 14, 9, 30), at(10, 14, 10, 30)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upd := &bongov1.Event{StartTime: tc.start, EndTime: tc.end}
			rebindToSeries(upd, series, occ, ams)
			if upd.StartTime != tc.wantS || upd.EndTime != tc.wantE {
				t.Errorf("got %v - %v, want %v - %v", time.Unix(upd.StartTime, 0).In(ams), time.Unix(upd.EndTime, 0).In(ams),
					time.Unix(tc.wantS, 0).In(ams), time.Unix(tc.wantE, 0).In(ams))
			}
		})
	}
	day := func(d int) int64 { return time.Date(2030, 10, d, 0, 0, 0, 0, time.UTC).Unix() }
	allDaySeries := &bongov1.Event{AllDay: true, StartTime: day(14), EndTime: day(15)}
	allDayOcc := &bongov1.Event{AllDay: true, StartTime: time.Date(2030, 11, 4, 0, 0, 0, 0, time.UTC).Unix(), EndTime: time.Date(2030, 11, 5, 0, 0, 0, 0, time.UTC).Unix()}
	upd := &bongov1.Event{AllDay: true, StartTime: allDayOcc.StartTime, EndTime: allDayOcc.StartTime + 3*86400}
	rebindToSeries(upd, allDaySeries, allDayOcc, ams)
	if upd.StartTime != day(14) || upd.EndTime != day(14)+3*86400 {
		t.Errorf("all-day: got %d-%d, want a three-day run from 14 October", upd.StartTime, upd.EndTime)
	}
}

// --- interop ----------------------------------------------------------------

// A TZID that is not an IANA name reads as UTC in the store. The override is
// written in UTC then, and must key to the same slot.
func TestInstanceEditThisOnANonIANAZone(t *testing.T) {
	e := newInstEnv(t)
	ics := strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Outlook//EN",
		"BEGIN:VEVENT", "UID:outlook-1", "DTSTAMP:20300101T000000Z",
		"DTSTART;TZID=W. Europe Standard Time:20301014T090000", "DTEND;TZID=W. Europe Standard Time:20301014T100000",
		"SUMMARY:Weekly", "RRULE:FREQ=WEEKLY", "END:VEVENT", "END:VCALENDAR", "",
	}, "\r\n")
	guid, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: itemWeekly, CalendarId: "default", Rrule: "FREQ=WEEKLY", Timezone: "W. Europe Standard Time",
		StartTime: utcUnix(2030, 10, 14, 9, 0), EndTime: utcUnix(2030, 10, 14, 10, 0), IcalRaw: []byte(ics),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.ap.calls = 0
	rid := "20301104T090000Z"
	form := occurrenceForm(kinds[0], "FREQ=WEEKLY", "Moved", "2030-11-04", map[string]string{"occurrence": rid, "scope": "this"})
	// The page shows UTC for such a series; Amsterdam is the viewer's zone, so
	// 09:00 UTC is 10:00 here.
	form.Set("start_time", "15:00")
	form.Set("end_time", "16:00")
	if w := e.update(guid, form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	_, ovs := rawParts(t, e.get(t, guid).IcalRaw)
	if len(ovs) != 1 {
		t.Fatalf("overrides = %d", len(ovs))
	}
	if rp := ovs[0].Props.Get(ical.PropRecurrenceID); rp.Value != rid {
		t.Errorf("RECURRENCE-ID = %q (%v), want the UTC form %s", rp.Value, rp.Params, rid)
	}
	occs, err := store.ExpandRecurrence(e.get(t, guid), windowFrom.Unix(), windowTo.Unix())
	if err != nil {
		t.Fatal(err)
	}
	var starts []string
	for _, o := range occs {
		starts = append(starts, time.Unix(o.StartTime, 0).UTC().Format("01-02 15:04 ")+o.Title)
	}
	want := []string{"10-14 09:00 Weekly", "10-21 09:00 Weekly", "10-28 09:00 Weekly", "11-04 14:00 Moved", "11-11 09:00 Weekly", "11-18 09:00 Weekly"}
	if !slices.Equal(starts, want) {
		t.Errorf("occurrences = %v, want %v (the 09:00 UTC slot of 4 November empty)", starts, want)
	}
}

// --- quota ----------------------------------------------------------------

func TestInstanceQuota(t *testing.T) {
	k := kinds[0]
	full := &fakeQuota{headroom: map[string]int64{quotaUser: 0}}

	t.Run("an edit that grows the record is refused", func(t *testing.T) {
		for _, scope := range []string{"this", "following"} {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			e.h.SetQuotaChecker(full)
			req := withGUID(formReq(http.MethodPut, "/calendar/event/"+guid, occurrenceForm(k, "FREQ=WEEKLY", "X", "2030-11-04", map[string]string{"occurrence": ridNov04Timed, "scope": scope})), guid)
			req.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			e.h.UpdateEvent(w, req)
			if w.Code != http.StatusInsufficientStorage {
				t.Errorf("%s: status = %d, want 507", scope, w.Code)
			}
			// For "following" both records are checked before the first write.
			if e.ap.calls != 0 {
				t.Errorf("%s: commands = %d, want none written", scope, e.ap.calls)
			}
		}
	})
	t.Run("a delete is allowed at quota", func(t *testing.T) {
		for _, scope := range []string{"this", "following"} {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			e.h.SetQuotaChecker(full)
			if w := e.remove(guid, url.Values{"occurrence": {ridNov04Timed}, "scope": {scope}}, false); w.Code != http.StatusSeeOther {
				t.Errorf("%s: status = %d, want the delete to go through", scope, w.Code)
			}
			if e.ap.calls != 1 {
				t.Errorf("%s: commands = %d, want 1", scope, e.ap.calls)
			}
		}
	})
}

// --- the form ---------------------------------------------------------------

func TestOccurrenceFormOffersScopeAndShowsTheOccurrence(t *testing.T) {
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			e := newInstEnv(t)
			guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
			rid := ridFor(k, "20301104")
			w := e.show(t, guid, "?occurrence="+rid)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			body := w.Body.String()
			for _, want := range []string{
				`name="occurrence" value="` + rid + `"`,
				`name="scope" value="this"`, `name="scope" value="following"`, `name="scope" value="all"`,
				`name="start_date" value="2030-11-04"`, `hx-include="#occurrence-scope"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("page lacks %s", want)
				}
			}
			if !k.allDay && !strings.Contains(body, `name="start_time" value="09:00"`) {
				t.Error("start time is not the occurrence's")
			}
			if strings.Contains(body, "apply to the whole series") {
				t.Error("the notice still says changes apply to the whole series")
			}
			// A plain event, and the series page without an occurrence, offer none.
			plain := e.show(t, guid, "").Body.String()
			if strings.Contains(plain, `name="scope"`) || strings.Contains(plain, `name="occurrence"`) {
				t.Error("the series page offers a scope")
			}
			if !strings.Contains(plain, `name="start_date" value="2030-10-14"`) {
				t.Error("the series page does not show the series start")
			}
		})
	}
}

func TestInstanceChangeNamesTheInstance(t *testing.T) {
	e := newInstEnv(t)
	k := kinds[1] // web-created: no ical_raw before
	guid := e.seed(t, k, "FREQ=WEEKLY", nil, nil)
	existing := e.get(t, guid)
	form, err := parseEventForm(formReq(http.MethodPut, "/x", occurrenceForm(k, "FREQ=WEEKLY", "Moved", "2030-11-04", map[string]string{"start_time": "14:00", "end_time": "15:00"})), e.ams)
	if err != nil {
		t.Fatal(err)
	}
	change, err := e.h.editOccurrence(instanceEdit{userID: quotaUser, existing: existing, form: form, rid: ridNov04Timed, slot: time.Date(2030, 11, 4, 8, 0, 0, 0, time.UTC), now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if change.Scope != "this" || change.Guid != guid || change.RecurrenceID != ridNov04Timed || change.Instance == nil {
		t.Fatalf("change = %+v", change)
	}
	if got := time.Unix(change.Instance.StartTime, 0).In(e.ams).Format("15:04"); got != "14:00" || change.Instance.Title != "Moved" || change.Instance.RecurrenceId != ridNov04Timed {
		t.Errorf("instance = %v %q %q", got, change.Instance.Title, change.Instance.RecurrenceId)
	}
	if len(change.Before.IcalRaw) != 0 || len(change.After.IcalRaw) == 0 {
		t.Error("Before and After do not describe the stored series before and after")
	}
}

func TestParseScope(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
		ok    bool
	}{
		{"", "all", true}, {"scope=", "all", true}, {"scope=all", "all", true}, {"scope=this", "this", true},
		{"scope=following", "following", true}, {"scope=ALL", "", false}, {"scope=future", "", false}, {"scope=this%0d%0a", "", false},
	} {
		got, ok := parseScope(httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil))
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q: got %q %v, want %q %v", tc.query, got, ok, tc.want, tc.ok)
		}
	}
}
