package caldav

import (
	"net/http"
	"strings"
	"testing"
)

// A free/busy request may name its range in a Windows zone, as Outlook does.
// freeBusyInstant reads it through icalevent.Zone; go-ical's DateTime calls
// time.LoadLocation, which refuses the name and would refuse the request.
// bob's block lies inside the Berlin day but outside the same wall clock read
// as UTC, so a reading that ignores the zone fails too.
func TestOutboxFreeBusyWindowsZoneRange(t *testing.T) {
	e := newSchedEnv(t, true)
	e.sched.busy["bob@d.test"] = [][2]int64{
		{1904250600, 1904252400}, // 2030-05-05 22:30-23:00 UTC, 00:30-01:00 CEST on the 6th
	}
	body := freeBusyPost("REQUEST", "VFREEBUSY", schEmail, "bob@d.test")
	body = strings.Replace(body, "DTSTART:20300506T000000Z", "DTSTART;TZID=W. Europe Standard Time:20300506T000000", 1)
	body = strings.Replace(body, "DTEND:20300507T000000Z", "DTEND;TZID=W. Europe Standard Time:20300507T000000", 1)
	if !strings.Contains(body, "DTSTART;TZID=W. Europe Standard Time:") || !strings.Contains(body, "DTEND;TZID=W. Europe Standard Time:") {
		t.Fatalf("fixture not rewritten:\n%s", body)
	}
	code, resp, _ := e.do(schUser, "POST", "/caldav/"+schUser+"/outbox/", body, map[string]string{"Content-Type": "text/calendar"})
	if code != http.StatusOK {
		t.Fatalf("POST = %d: %s", code, resp)
	}
	if !strings.Contains(resp, "2.0;Success") {
		t.Errorf("no success for bob:\n%s", resp)
	}
	if !strings.Contains(resp, "FREEBUSY;FBTYPE=BUSY:20300505T223000Z/20300505T230000Z") {
		t.Errorf("busy block missing (the range was not read in the Berlin zone):\n%s", resp)
	}
}
