package handlers

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const importHead = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Other//EN\r\n"

// importICS runs ImportCalendar over body and returns the stored events and
// the redirect location.
func importICS(t *testing.T, body string) ([]*bongov1.Event, string) {
	t.Helper()
	env := newQuotaEnv(t)
	h := NewCalendarHandler(env.events, env.calendars, nil)
	w := httptest.NewRecorder()
	h.ImportCalendar(w, uploadReq(t, "/calendar/import", "ics", []byte(body), nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	evs, _, err := env.events.ListAll(quotaUser, 0, 0)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	return evs, w.Header().Get("Location")
}

func decodeRaw(t *testing.T, e *bongov1.Event) *ical.Calendar {
	t.Helper()
	if len(e.IcalRaw) == 0 {
		t.Fatal("ical_raw is empty")
	}
	cal, err := ical.NewDecoder(strings.NewReader(string(e.IcalRaw))).Decode()
	if err != nil {
		t.Fatalf("decode ical_raw: %v", err)
	}
	return cal
}

func TestImportKeepsRawWithProdID(t *testing.T) {
	evs, _ := importICS(t, importHead+
		"BEGIN:VEVENT\r\nUID:a@x\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301105T090000Z\r\nSUMMARY:One\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	raw := string(evs[0].IcalRaw)
	for _, want := range []string{"PRODID:-//Bongo-Go//Calendar//EN", "VERSION:2.0"} {
		if !strings.Contains(raw, want) {
			t.Errorf("ical_raw lacks %q:\n%s", want, raw)
		}
	}
}

func TestImportGroupsOverridesByUID(t *testing.T) {
	evs, _ := importICS(t, importHead+
		"BEGIN:VEVENT\r\nUID:s@x\r\nDTSTAMP:20301101T000000Z\r\nRECURRENCE-ID:20301112T090000Z\r\nDTSTART:20301112T110000Z\r\nSUMMARY:Moved1\r\nEND:VEVENT\r\n"+
		"BEGIN:VEVENT\r\nUID:s@x\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301105T090000Z\r\nRRULE:FREQ=WEEKLY\r\nSUMMARY:Weekly\r\nEND:VEVENT\r\n"+
		"BEGIN:VEVENT\r\nUID:s@x\r\nDTSTAMP:20301101T000000Z\r\nRECURRENCE-ID:20301119T090000Z\r\nDTSTART:20301119T110000Z\r\nSUMMARY:Moved2\r\nEND:VEVENT\r\n"+
		"END:VCALENDAR\r\n")
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	if evs[0].Title != "Weekly" || evs[0].Rrule != "FREQ=WEEKLY" {
		t.Errorf("title=%q rrule=%q, want the master's", evs[0].Title, evs[0].Rrule)
	}
	if n := len(decodeRaw(t, evs[0]).Events()); n != 3 {
		t.Errorf("ical_raw holds %d VEVENTs, want 3", n)
	}
}

func TestImportCarriesReferencedTimezoneOnly(t *testing.T) {
	evs, _ := importICS(t, importHead+
		"BEGIN:VTIMEZONE\r\nTZID:Europe/Amsterdam\r\nBEGIN:STANDARD\r\nDTSTART:19701025T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n"+
		"BEGIN:VTIMEZONE\r\nTZID:America/Chicago\r\nBEGIN:STANDARD\r\nDTSTART:19701101T020000\r\nTZOFFSETFROM:-0500\r\nTZOFFSETTO:-0600\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n"+
		"BEGIN:VEVENT\r\nUID:tz@x\r\nDTSTAMP:20301101T000000Z\r\nDTSTART;TZID=Europe/Amsterdam:20301105T090000\r\nSUMMARY:Zoned\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	raw := string(evs[0].IcalRaw)
	if !strings.Contains(raw, "TZID:Europe/Amsterdam") {
		t.Errorf("referenced VTIMEZONE missing:\n%s", raw)
	}
	if strings.Contains(raw, "America/Chicago") {
		t.Errorf("unreferenced VTIMEZONE copied:\n%s", raw)
	}
}

func TestImportAllDay(t *testing.T) {
	evs, _ := importICS(t, importHead+
		"BEGIN:VEVENT\r\nUID:d@x\r\nDTSTAMP:20301101T000000Z\r\nDTSTART;VALUE=DATE:20301105\r\nSUMMARY:Day\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	if len(evs) != 1 || !evs[0].AllDay {
		t.Fatalf("want one all-day event, got %v", evs)
	}
}

func TestImportCountsEncodeFailures(t *testing.T) {
	evs, loc := importICS(t, importHead+
		"BEGIN:VEVENT\r\nUID:ok@x\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301105T090000Z\r\nSUMMARY:Good\r\nEND:VEVENT\r\n"+
		"BEGIN:VEVENT\r\nUID:bad@x\r\nDTSTART:20301106T090000Z\r\nSUMMARY:No stamp\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	if len(evs) != 1 || evs[0].Title != "Good" {
		t.Fatalf("want only the good event stored, got %d", len(evs))
	}
	if !strings.Contains(loc, "Imported+1+events,+1+failed") {
		t.Errorf("Location = %q, want Imported+1+events,+1+failed", loc)
	}
}

// eventCreateFailApplier fails writes of events whose data carries
// "CreateFails" and passes the rest on.
type eventCreateFailApplier struct{ *directApplierTags }

func (a eventCreateFailApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if strings.Contains(string(cmd.Key), "/event/") && bytes.Contains(cmd.Value, []byte("CreateFails")) {
		return errors.New("injected event create failure")
	}
	return a.directApplierTags.ApplyCommand(cmd, d)
}

func TestImportCountsCreateFailures(t *testing.T) {
	env := newQuotaEnv(t)
	a := eventCreateFailApplier{&directApplierTags{ps: env.ps}}
	events := store.NewEventStore(env.ps, a)
	h := NewCalendarHandler(events, store.NewCalendarStore(env.ps, a), nil)
	body := importHead +
		"BEGIN:VEVENT\r\nUID:ok@x\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301105T090000Z\r\nSUMMARY:Good\r\nEND:VEVENT\r\n" +
		"BEGIN:VEVENT\r\nUID:no@x\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301106T090000Z\r\nSUMMARY:CreateFails\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	w := httptest.NewRecorder()
	h.ImportCalendar(w, uploadReq(t, "/calendar/import", "ics", []byte(body), nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "Imported+1+events,+1+failed") {
		t.Errorf("Location = %q, want Imported+1+events,+1+failed", loc)
	}
	evs, _, err := events.ListAll(quotaUser, 0, 0)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(evs) != 1 || evs[0].Title != "Good" {
		t.Fatalf("want only the good event stored, got %d", len(evs))
	}
}
