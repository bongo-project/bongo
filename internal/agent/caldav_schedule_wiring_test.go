package agent

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

// CalDAV scheduling reaches a real client only through four lines of Run: the
// scheduler on the backend, the schedule inbox on the backend, the
// ScheduleHandler around the DAV handler and the inbox placer on the delivery
// stage. Deleting any one of them leaves the per-package tests green, so each
// is probed here over the real endpoint or the real delivery stage.
func TestCalDAVSchedulingWiredIntoAgent(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	a := runWiringAgent(t, cfg, testLogger(), nil)

	if a.caldavBackend == nil {
		t.Fatal("Run did not build the CalDAV backend")
	}
	if !a.caldavBackend.HasScheduler() {
		t.Error("CalDAV backend has no scheduler after Run: clients would keep sending iMIP themselves")
	}
	if a.deliverProc == nil || a.deliverProc.ScheduleInbox() == nil {
		t.Error("the delivery stage has no schedule inbox after Run: incoming invitations never reach CalDAV clients")
	}

	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	do := func(method, path, body string, hdr map[string]string) (int, string, http.Header) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.SetBasicAuth("admin@localhost", "admin")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}

	// The DAV header names the capability.
	_, _, hdr := do("OPTIONS", "/caldav/", "", nil)
	if !strings.Contains(strings.Join(hdr.Values("DAV"), ","), "calendar-auto-schedule") {
		t.Errorf("DAV header %v does not advertise calendar-auto-schedule", hdr.Values("DAV"))
	}

	// The principal reports where the collections are and who the user is.
	adminID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatal(err)
	}
	code, body, _ := do("PROPFIND", "/caldav/"+adminID+"/", `<propfind xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><prop>`+
		`<C:schedule-inbox-URL/><C:schedule-outbox-URL/><C:calendar-user-address-set/></prop></propfind>`,
		map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	for _, want := range []string{"/caldav/" + adminID + "/inbox/", "/caldav/" + adminID + "/outbox/", "mailto:admin@localhost"} {
		if code != http.StatusMultiStatus || !strings.Contains(body, want) {
			t.Errorf("principal PROPFIND = %d, missing %q:\n%s", code, want, body)
		}
	}

	// The inbox and outbox are served by the wrapper, not go-webdav.
	code, body, _ = do("PROPFIND", "/caldav/"+adminID+"/inbox/", "", map[string]string{"Depth": "0"})
	if code != http.StatusMultiStatus || !strings.Contains(body, "schedule-inbox") {
		t.Errorf("inbox PROPFIND = %d: %s", code, body)
	}
	// An item the delivery stage placed is what the collection lists.
	item := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:w1\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\n" +
		"ORGANIZER:mailto:zed@elsewhere.test\r\nATTENDEE:mailto:admin@localhost\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if err := a.ScheduleInbox.Place(adminID, "0123456789abcdef", []byte(item)); err != nil {
		t.Fatal(err)
	}
	code, body, _ = do("PROPFIND", "/caldav/"+adminID+"/inbox/", `<propfind xmlns="DAV:"><prop><getetag/></prop></propfind>`, map[string]string{"Depth": "1"})
	if code != http.StatusMultiStatus || !strings.Contains(body, "-0123456789abcdef.ics") {
		t.Errorf("inbox listing = %d, lacks the placed item:\n%s", code, body)
	}
	// The outbox answers free/busy for the user's own address and refuses an
	// invitation, through the real scheduler and address store.
	fb := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//EN\r\nMETHOD:REQUEST\r\nBEGIN:VFREEBUSY\r\nUID:w\r\nDTSTAMP:20300101T000000Z\r\n" +
		"DTSTART:20300506T000000Z\r\nDTEND:20300507T000000Z\r\nORGANIZER:mailto:admin@localhost\r\nATTENDEE:mailto:admin@localhost\r\nATTENDEE:mailto:nobody@localhost\r\nEND:VFREEBUSY\r\nEND:VCALENDAR\r\n"
	code, body, _ = do("POST", "/caldav/"+adminID+"/outbox/", fb, map[string]string{"Content-Type": "text/calendar"})
	if code != http.StatusOK || !strings.Contains(body, "2.0;Success") || !strings.Contains(body, "3.7;Invalid calendar user") {
		t.Errorf("outbox free/busy = %d: %s", code, body)
	}
	if code, _, _ := do("POST", "/caldav/"+adminID+"/outbox/", strings.ReplaceAll(strings.ReplaceAll(fb, "VFREEBUSY", "VEVENT"), "DTEND", "DTSTART"),
		map[string]string{"Content-Type": "text/calendar"}); code != http.StatusForbidden {
		t.Errorf("an invitation through the outbox = %d, want 403", code)
	}
}
