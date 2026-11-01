package agent

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A calendar shared with a user must reach them over the real CalDAV
// endpoint, with the grant checked on every request and the privilege set
// rewritten for a read share. Deleting the SetGrants call or the
// PrivilegeFilter wrapper in agent.go turns a probe red while the
// per-package tests stay green.
func TestCalDAVSharedCalendarWiredIntoAgent(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	a := runWiringAgent(t, cfg, testLogger(), nil)

	ownerID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	readerID, err := auth.CreateUser(a, a.Metadata, "reader@localhost", "Reader", "pass1234", false)
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}
	writerID, err := auth.CreateUser(a, a.Metadata, "writer@localhost", "Writer", "pass1234", false)
	if err != nil {
		t.Fatalf("create writer: %v", err)
	}
	calID, err := a.Calendars.EnsureDefault(ownerID)
	if err != nil {
		t.Fatalf("default calendar: %v", err)
	}
	if err := a.ACL.SetCalendarGrant(ownerID, calID, readerID, store.GrantLevelRead, store.GrantStateAccepted, ownerID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := a.ACL.SetCalendarGrant(ownerID, calID, writerID, store.GrantLevelWrite, store.GrantStateAccepted, ownerID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Events.Create(ownerID, &bongov1.Event{Guid: "shared-ev", CalendarId: calID, Title: "Shared", StartTime: 1792058400, EndTime: 1792062000}); err != nil {
		t.Fatal(err)
	}

	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	do := func(user, method, path, body string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.SetBasicAuth(user, "pass1234")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	seg := "shared-" + ownerID + "-" + calID
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VEVENT\r\nUID:w\r\nDTSTAMP:20240115T100000Z\r\nDTSTART:20240115T100000Z\r\nSUMMARY:Wired\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	ct := map[string]string{"Content-Type": "text/calendar"}

	// Read share: listed, readable, not writable, reported read-only.
	rpath := "/caldav/" + readerID + "/calendars/" + seg + "/"
	if c, _ := do("reader@localhost", "GET", rpath+"shared-ev.ics", "", nil); c != http.StatusOK {
		t.Errorf("reader GET = %d, want 200", c)
	}
	if c, _ := do("reader@localhost", "PUT", rpath+"w.ics", ics, ct); c != http.StatusForbidden {
		t.Errorf("reader PUT = %d, want 403", c)
	}
	if c, _ := do("reader@localhost", "DELETE", rpath+"shared-ev.ics", "", nil); c != http.StatusForbidden {
		t.Errorf("reader DELETE = %d, want 403", c)
	}
	pf := `<?xml version="1.0"?><propfind xmlns="DAV:"><prop><current-user-privilege-set/></prop></propfind>`
	c, body := do("reader@localhost", "PROPFIND", rpath, pf, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	if c != http.StatusMultiStatus || !strings.Contains(body, "read") || strings.Contains(body, "write") {
		t.Errorf("reader privilege set: status %d body %s", c, body)
	}

	// Write share: the same routes succeed and the privilege stays read-write.
	wpath := "/caldav/" + writerID + "/calendars/" + seg + "/"
	c, body = do("writer@localhost", "PROPFIND", wpath, pf, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	if c != http.StatusMultiStatus || !strings.Contains(body, "write") {
		t.Errorf("writer privilege set: status %d body %s", c, body)
	}
	if c, _ := do("writer@localhost", "PUT", wpath+"w.ics", ics, ct); c != http.StatusCreated && c != http.StatusNoContent {
		t.Errorf("writer PUT = %d, want 201/204", c)
	}
	if ev, err := a.Events.Get(ownerID, "w"); err != nil || ev.CalendarId != calID {
		t.Errorf("writer's event not stored under the owner: %+v, %v", ev, err)
	}

	// A path under the owner's home is not served to a grantee.
	if c, _ := do("writer@localhost", "GET", "/caldav/"+ownerID+"/calendars/"+calID+"/shared-ev.ics", "", nil); c != http.StatusForbidden {
		t.Errorf("writer GET under owner's home = %d, want 403", c)
	}

	// Revoking takes effect on the next request.
	if err := a.ACL.RevokeCalendarGrant(ownerID, calID, readerID); err != nil {
		t.Fatal(err)
	}
	if c, _ := do("reader@localhost", "GET", rpath+"shared-ev.ics", "", nil); c != http.StatusNotFound {
		t.Errorf("reader GET after revoke = %d, want 404", c)
	}
}
