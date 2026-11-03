package caldav

import (
	"net/http"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestPutIntoMissingCollectionIsConflict(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"event", vcal(vevent("x1", "Lunch"))},
		{"task", vcal(vtodo("x1", "Buy milk"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTdEnv(t)
			if code := e.put(tdOwner, tdPath("nope", "x"), tc.body, nil); code != http.StatusConflict {
				t.Fatalf("PUT status = %d, want 409", code)
			}
			evs, _, _ := e.events.ListAll(tdOwner, 0, 0)
			if len(evs) != 0 {
				t.Fatalf("events stored: %d", len(evs))
			}
		})
	}
}

func TestPutExistingGuidViaMissingCollectionDoesNotMoveIt(t *testing.T) {
	e := newTdEnv(t)
	if code := e.put(tdOwner, tdPath(tdPersonal, "x"), vcal(vevent("x", "Lunch")), nil); code != http.StatusCreated {
		t.Fatalf("seed PUT = %d", code)
	}
	if code := e.put(tdOwner, tdPath("nope", "x"), vcal(vevent("x", "Moved")), nil); code != http.StatusConflict {
		t.Fatalf("PUT = %d, want 409", code)
	}
	ev, err := e.events.Get(tdOwner, "x")
	if err != nil {
		t.Fatal(err)
	}
	if ev.CalendarId != tdPersonal || ev.Title != "Lunch" {
		t.Fatalf("event = %q in %q, want Lunch in %s", ev.Title, ev.CalendarId, tdPersonal)
	}
}

func TestEmptyCalendarIDListedInOwnDefault(t *testing.T) {
	e := newTdEnv(t)
	if err := e.cals.Update(tdOwner, "default", &bongov1.Calendar{Guid: "default", Name: "Default"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.events.Create(tdOwner, &bongov1.Event{
		Guid: "legacy", Title: "Legacy", CalendarId: "",
		StartTime: 1900000000, EndTime: 1900003600,
	}); err != nil {
		t.Fatal(err)
	}
	base := "/caldav/" + tdOwner + "/calendars/"
	query := `<?xml version="1.0"?><C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:getetag/></D:prop><C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT"/></C:comp-filter></C:filter></C:calendar-query>`
	propfind := `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:prop><D:getetag/></D:prop></D:propfind>`
	for _, tc := range []struct {
		cal  string
		want bool
	}{{"default", true}, {tdPersonal, false}} {
		for _, m := range []struct{ method, body string }{{"PROPFIND", propfind}, {"REPORT", query}} {
			code, body := e.do(tdOwner, m.method, base+tc.cal+"/", m.body, map[string]string{"Depth": "1", "Content-Type": "application/xml"})
			if code != http.StatusMultiStatus {
				t.Fatalf("%s %s = %d", m.method, tc.cal, code)
			}
			if got := strings.Contains(body, "legacy.ics"); got != tc.want {
				t.Errorf("%s %s lists legacy = %v, want %v\n%s", m.method, tc.cal, got, tc.want, body)
			}
		}
	}
}
