package handlers

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ovrObject is a weekly series (Mondays from 6 May 2030) with two overrides:
// the 13th cancelled and the 20th transparent. dtstart is the DTSTART/DTEND
// lines of the master; ridFmt builds a RECURRENCE-ID line for a day.
func ovrObject(uid, class, dtstart, ridFmt string) string {
	rid := func(day string) string { return fmt.Sprintf(ridFmt, day) }
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261001T000000Z\r\n" + dtstart + "\r\nSUMMARY:MasterTitle\r\nRRULE:FREQ=WEEKLY\r\n" + class + "END:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261001T000000Z\r\n" + rid("20300513") + "\r\n" + dtstart + "\r\nSUMMARY:CancelledMarker\r\nSTATUS:CANCELLED\r\nX-LEAK:XMarker\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20261001T000000Z\r\n" + rid("20300520") + "\r\n" + dtstart + "\r\nSUMMARY:TransparentMarker\r\nTRANSP:TRANSPARENT\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
}

// expandedDays parses a published body with the repo's own code, expands the
// series over May 2030 and returns the start days.
func expandedDays(t *testing.T, body string) []string {
	t.Helper()
	feed, err := icalevent.ParseFeed([]byte(body), 100)
	if err != nil {
		t.Fatalf("parsing published feed: %v\n%s", err, body)
	}
	var days []string
	from := time.Date(2030, 5, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	for _, fe := range feed.Events {
		// A subscriber stores the group's raw iCalendar with the event.
		fe.Event.IcalRaw = fe.Raw
		insts, err := store.ExpandRecurrence(fe.Event, from, to)
		if err != nil {
			t.Fatal(err)
		}
		loc := time.UTC
		if fe.Event.Timezone != "" {
			if l, err := time.LoadLocation(fe.Event.Timezone); err == nil {
				loc = l
			}
		}
		for _, in := range insts {
			days = append(days, time.Unix(in.StartTime, 0).In(loc).Format("2006-01-02"))
		}
	}
	return days
}

func TestFeedHiddenOverrideLeavesOccurrenceFree(t *testing.T) {
	series := []struct {
		name, dtstart, ridFmt string
	}{
		{"utc", "DTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z", "RECURRENCE-ID:%sT090000Z"},
		{"tzid", "DTSTART;TZID=Europe/Amsterdam:20300506T090000\r\nDTEND;TZID=Europe/Amsterdam:20300506T100000", "RECURRENCE-ID;TZID=Europe/Amsterdam:%sT090000"},
		{"tzid-with-utc-rid", "DTSTART;TZID=Europe/Amsterdam:20300506T090000\r\nDTEND;TZID=Europe/Amsterdam:20300506T100000", "RECURRENCE-ID:%sT070000Z"},
		{"allday", "DTSTART;VALUE=DATE:20300506\r\nDTEND;VALUE=DATE:20300507", "RECURRENCE-ID;VALUE=DATE:%s"},
	}
	for _, s := range series {
		for _, tc := range []struct {
			name, detail, class string
		}{
			{"busy", "busy", ""},
			{"full-private-master", "full", "CLASS:PRIVATE\r\n"},
		} {
			t.Run(s.name+"/"+tc.name, func(t *testing.T) {
				e := newPubEnv(t)
				raw := ovrObject("s@x", tc.class, s.dtstart, s.ridFmt)
				if _, err := e.events.Create(pubOwner, &bongov1.Event{Guid: "r", CalendarId: "default", StartTime: 1, EndTime: 2, IcalRaw: []byte(raw)}); err != nil {
					t.Fatal(err)
				}
				tok := e.publish(t, "default", tc.detail)
				body := e.get(pubOwner, "default", tok, "", nil).Body.String()
				for _, leak := range []string{"CancelledMarker", "TransparentMarker", "MasterTitle", "XMarker", "X-LEAK", "STATUS", "TRANSP"} {
					if strings.Contains(body, leak) {
						t.Errorf("leaked %q:\n%s", leak, body)
					}
				}
				got := strings.Join(expandedDays(t, body), ",")
				if want := "2030-05-06,2030-05-27"; got != want {
					t.Errorf("occurrences = %s, want %s\n%s", got, want, body)
				}
			})
		}
	}
}

func TestFeedOverrideInheritsPrivateClass(t *testing.T) {
	const dt = "DTSTART:20300506T090000Z\r\nDTEND:20300506T100000Z"
	obj := func(masterClass, overrideClass string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\n" +
			"BEGIN:VEVENT\r\nUID:o@x\r\nDTSTAMP:20261001T000000Z\r\n" + dt + "\r\nSUMMARY:MasterTitle\r\nRRULE:FREQ=WEEKLY\r\n" + masterClass + "END:VEVENT\r\n" +
			"BEGIN:VEVENT\r\nUID:o@x\r\nDTSTAMP:20261001T000000Z\r\nRECURRENCE-ID:20300513T090000Z\r\n" + dt +
			"\r\nSUMMARY:OverrideTitle\r\nDESCRIPTION:OverrideDesc\r\nLOCATION:OverrideRoom\r\n" + overrideClass + "END:VEVENT\r\nEND:VCALENDAR\r\n"
	}
	cases := []struct {
		name, master, override     string
		masterShown, overrideShown bool
	}{
		{"private master, override without class", "CLASS:PRIVATE\r\n", "", false, false},
		{"confidential master, override says public", "CLASS:CONFIDENTIAL\r\n", "CLASS:PUBLIC\r\n", false, false},
		{"public master, private override", "", "CLASS:PRIVATE\r\n", true, false},
		{"public master, public override", "", "", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newPubEnv(t)
			if _, err := e.events.Create(pubOwner, &bongov1.Event{Guid: "r", CalendarId: "default", StartTime: 1, EndTime: 2, IcalRaw: []byte(obj(tc.master, tc.override))}); err != nil {
				t.Fatal(err)
			}
			body := e.get(pubOwner, "default", e.publish(t, "default", "full"), "", nil).Body.String()
			if got := strings.Contains(body, "MasterTitle"); got != tc.masterShown {
				t.Errorf("master title shown = %v, want %v:\n%s", got, tc.masterShown, body)
			}
			for _, s := range []string{"OverrideTitle", "OverrideDesc", "OverrideRoom"} {
				if got := strings.Contains(body, s); got != tc.overrideShown {
					t.Errorf("%s shown = %v, want %v:\n%s", s, got, tc.overrideShown, body)
				}
			}
			if !strings.Contains(body, "RECURRENCE-ID:20300513T090000Z") {
				t.Errorf("override dropped:\n%s", body)
			}
		})
	}
}

func TestFeedUndecodableRawIsBusy(t *testing.T) {
	e := newPubEnv(t)
	if _, err := e.events.Create(pubOwner, &bongov1.Event{Guid: "bad", CalendarId: "default", Title: "GarbageTitle", Description: "GarbageDesc", Location: "GarbageRoom",
		StartTime: 1792058400, EndTime: 1792062000, IcalRaw: []byte("BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nnot ical at all")}); err != nil {
		t.Fatal(err)
	}
	body := e.get(pubOwner, "default", e.publish(t, "default", "full"), "", nil).Body.String()
	for _, s := range []string{"GarbageTitle", "GarbageDesc", "GarbageRoom"} {
		if strings.Contains(body, s) {
			t.Errorf("leaked %q:\n%s", s, body)
		}
	}
	if !strings.Contains(body, "SUMMARY:Busy") {
		t.Errorf("undecodable event not published as busy:\n%s", body)
	}
}
