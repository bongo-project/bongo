package handlers

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const winZone = "W. Europe Standard Time"

// winSeriesICS is an Outlook-style weekly series: 14 October 2030 09:00 is
// CEST (07:00Z), the 4 November instance is CET (08:00Z).
func winSeriesICS() []byte {
	return []byte(strings.Join([]string{
		"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Outlook//EN",
		"BEGIN:VEVENT", "UID:outlook-win", "DTSTAMP:20300101T000000Z",
		"DTSTART;TZID=" + winZone + ":20301014T090000", "DTEND;TZID=" + winZone + ":20301014T100000",
		"SUMMARY:Weekly", "RRULE:FREQ=WEEKLY", "END:VEVENT", "END:VCALENDAR", "",
	}, "\r\n"))
}

// A whole-series edit of a Windows-zone series keeps the series' own TZID text
// and writes the new Berlin wall clock. setEventTimes resolves the name with
// icalevent.Zone; time.LoadLocation fails on it and the times would be written
// in UTC (Z, no TZID).
func TestInstanceEditAllOnAWindowsZone(t *testing.T) {
	e := newInstEnv(t)
	guid, err := e.events.Create(quotaUser, &bongov1.Event{
		Title: itemWeekly, CalendarId: "default", Rrule: "FREQ=WEEKLY", Timezone: winZone,
		StartTime: utcUnix(2030, 10, 14, 7, 0), EndTime: utcUnix(2030, 10, 14, 8, 0), IcalRaw: winSeriesICS(),
	})
	if err != nil {
		t.Fatal(err)
	}
	over := map[string]string{"occurrence": ridNov04Timed, "scope": "all", "start_time": "11:00", "end_time": "12:30"}
	if w := e.update(guid, occurrenceForm(kinds[0], "FREQ=WEEKLY", "Weekly", "2030-11-04", over)); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	after := e.get(t, guid)
	if want := utcUnix(2030, 10, 14, 9, 0); after.StartTime != want {
		t.Fatalf("start = %v, want 14 October 11:00 CEST (09:00Z): the edit did not move the series", time.Unix(after.StartTime, 0).UTC())
	}
	master, ovs := rawParts(t, after.IcalRaw)
	if len(ovs) != 0 {
		t.Errorf("overrides = %d, want none", len(ovs))
	}
	for name, want := range map[string]string{ical.PropDateTimeStart: "20301014T110000", ical.PropDateTimeEnd: "20301014T123000"} {
		p := master.Props.Get(name)
		if p == nil {
			t.Fatalf("%s missing", name)
		}
		if p.Params.Get("TZID") != winZone || p.Value != want {
			t.Errorf("%s = %q %v, want %q with TZID=%s", name, p.Value, p.Params, want, winZone)
		}
	}
	occs, err := store.ExpandRecurrence(after, utcUnix(2030, 11, 1, 0, 0), utcUnix(2030, 11, 8, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 1 || occs[0].StartTime != utcUnix(2030, 11, 4, 10, 0) {
		t.Errorf("occurrences = %v, want one at 2030-11-04T10:00Z (11:00 CET)", occs)
	}
}

// eventTimesChanged compares an all-day edit with the stored DATE values. The
// zoned DATE-TIME rows below are the defensive path (an all-day proto whose raw
// DTSTART is a zoned DATE-TIME), not one live data reaches: AllDay is set from
// VALUE=DATE only. 02:00 CEST is exactly UTC midnight, so with the zone
// resolved the dates match and nothing changed; time.LoadLocation fails on the
// Windows name and the function answers "changed".
func TestEventTimesChangedWindowsZoneAllDay(t *testing.T) {
	for _, tzid := range []string{winZone, "Europe/Berlin"} {
		t.Run(tzid, func(t *testing.T) {
			props := ical.Props{}
			s := ical.NewProp(ical.PropDateTimeStart)
			s.Params.Set(ical.PropTimezoneID, tzid)
			s.Value = "20301014T020000"
			props.Set(s)
			en := ical.NewProp(ical.PropDateTimeEnd)
			en.Params.Set(ical.PropTimezoneID, tzid)
			en.Value = "20301015T020000"
			props.Set(en)
			existing := &bongov1.Event{AllDay: true, StartTime: utcUnix(2030, 10, 14, 0, 0), EndTime: utcUnix(2030, 10, 15, 0, 0)}
			updated := &bongov1.Event{AllDay: true, StartTime: utcUnix(2030, 10, 14, 0, 0), EndTime: utcUnix(2030, 10, 15, 0, 0)}
			if eventTimesChanged(props, existing, updated) {
				t.Error("eventTimesChanged = true for the same dates, want false")
			}
			// Negative control: a different date is still a change.
			updated.EndTime = utcUnix(2030, 10, 16, 0, 0)
			if !eventTimesChanged(props, existing, updated) {
				t.Error("eventTimesChanged = false for a moved end date, want true")
			}
		})
	}
}

// exdateFor rewrites a UTC RECURRENCE-ID in the master's zone. The assertions
// are on the text: an EXDATE left in UTC removes the same instant, so an
// occurrence check stays green when the zone is lost.
func TestExdateForWindowsZoneMaster(t *testing.T) {
	for _, tc := range []struct {
		name, tzid, rid, wantValue string
	}{
		{"windows, after the DST change", winZone, "20301104T080000Z", "20301104T090000"},
		{"windows, before the DST change", winZone, "20301021T070000Z", "20301021T090000"},
		{"iana control", "Europe/Berlin", "20301104T080000Z", "20301104T090000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master := ical.NewComponent(ical.CompEvent)
			p := ical.NewProp(ical.PropDateTimeStart)
			p.Params.Set(ical.PropTimezoneID, tc.tzid)
			p.Value = "20301014T090000"
			master.Props.Set(p)
			rid := ical.NewProp(ical.PropRecurrenceID)
			rid.Value = tc.rid
			ex := exdateFor(master, rid)
			if ex == nil {
				t.Fatal("no EXDATE")
			}
			if ex.Value != tc.wantValue || ex.Params.Get("TZID") != tc.tzid {
				t.Errorf("EXDATE = %q %v, want %q with TZID=%s", ex.Value, ex.Params, tc.wantValue, tc.tzid)
			}
		})
	}
}

// The same through the published feed: the hidden overrides of a Windows-zone
// series (UTC RECURRENCE-IDs) become EXDATEs in the series' own zone text.
func TestFeedExdateOnWindowsZoneMaster(t *testing.T) {
	e := newPubEnv(t)
	dtstart := "DTSTART;TZID=" + winZone + ":20300506T090000\r\nDTEND;TZID=" + winZone + ":20300506T100000"
	raw := ovrObject("w@x", "", dtstart, "RECURRENCE-ID:%sT070000Z")
	if _, err := e.events.Create(pubOwner, &bongov1.Event{Guid: "w", CalendarId: "default", StartTime: 1, EndTime: 2, IcalRaw: []byte(raw)}); err != nil {
		t.Fatal(err)
	}
	tok := e.publish(t, "default", "busy")
	body := e.get(pubOwner, "default", tok, "", nil).Body.String()
	for _, want := range []string{
		"EXDATE;TZID=" + winZone + ":20300513T090000",
		"EXDATE;TZID=" + winZone + ":20300520T090000",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("feed lacks %q:\n%s", want, body)
		}
	}
}

// An Outlook series names its zone with a Windows name. Import reads the
// times through icalevent.EventStart/EventEnd (via ToEvent), which resolve the
// name with icalevent.Zone; go-ical's DateTime would fail time.LoadLocation
// and leave StartTime at 0.
func TestImportWindowsZoneSeries(t *testing.T) {
	evs, _ := importICS(t, importHead+
		"BEGIN:VTIMEZONE\r\nTZID:"+winZone+"\r\nBEGIN:STANDARD\r\nDTSTART:16011028T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n"+
		"BEGIN:VEVENT\r\nUID:win@x\r\nDTSTAMP:20301001T000000Z\r\nDTSTART;TZID="+winZone+":20301014T090000\r\nDTEND;TZID="+winZone+":20301014T100000\r\nSUMMARY:Weekly\r\nRRULE:FREQ=WEEKLY\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	ev := evs[0]
	if ev.StartTime != utcUnix(2030, 10, 14, 7, 0) {
		t.Errorf("StartTime = %v, want 2030-10-14T07:00Z", time.Unix(ev.StartTime, 0).UTC())
	}
	if ev.EndTime != utcUnix(2030, 10, 14, 8, 0) {
		t.Errorf("EndTime = %v, want 2030-10-14T08:00Z", time.Unix(ev.EndTime, 0).UTC())
	}
	if ev.Timezone != winZone {
		t.Errorf("Timezone = %q, want the Windows name kept", ev.Timezone)
	}
	if !strings.Contains(string(ev.IcalRaw), "TZID:"+winZone) {
		t.Errorf("referenced VTIMEZONE missing:\n%s", ev.IcalRaw)
	}
	occs, err := store.ExpandRecurrence(ev, utcUnix(2030, 11, 1, 0, 0), utcUnix(2030, 11, 8, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(occs) != 1 || occs[0].StartTime != utcUnix(2030, 11, 4, 8, 0) {
		t.Errorf("occurrences = %v, want one at 2030-11-04T08:00Z (CET)", occs)
	}
}
