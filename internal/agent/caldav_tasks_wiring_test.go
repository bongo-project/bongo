package agent

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
)

// A task must reach the task store over the real CalDAV endpoint, a new user
// must have a Tasks calendar that reports VTODO, and a component the target
// calendar does not take must be refused. Deleting the SetTaskStore call in
// agent.go turns this red while the per-package tests stay green.
func TestCalDAVTasksWiredIntoAgent(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	a := runWiringAgent(t, cfg, testLogger(), nil)

	userID, err := auth.CreateUser(a, a.Metadata, "tasker@localhost", "Tasker", "pass1234", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	do := func(method, path, body string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.SetBasicAuth("tasker@localhost", "pass1234")
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
	home := "/caldav/" + userID + "/calendars/"
	prop := `<?xml version="1.0"?><propfind xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><prop><displayname/><C:supported-calendar-component-set/></prop></propfind>`
	xml := map[string]string{"Depth": "1", "Content-Type": "application/xml"}

	c, body := do("PROPFIND", home, prop, xml)
	if c != http.StatusMultiStatus {
		t.Fatalf("PROPFIND home = %d: %s", c, body)
	}
	i := strings.Index(body, home+"tasks/")
	if i < 0 {
		t.Fatalf("a new user has no Tasks calendar: %s", body)
	}
	if rest := body[i:]; !strings.Contains(rest[:min(len(rest), 900)], `name="VTODO"`) {
		t.Errorf("Tasks does not report VTODO: %s", rest)
	}

	todo := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VTODO\r\nUID:w1\r\nDTSTAMP:20301101T000000Z\r\nSUMMARY:Wired task\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	ct := map[string]string{"Content-Type": "text/calendar"}
	if c, b := do("PUT", home+"tasks/w1.ics", todo, ct); c != http.StatusCreated {
		t.Fatalf("PUT VTODO into Tasks = %d: %s", c, b)
	}
	if task, err := a.Tasks.Get(userID, "w1"); err != nil || task.Summary != "Wired task" {
		t.Errorf("task not stored: %+v, %v", task, err)
	}
	c, body = do("PUT", home+"default/w2.ics", todo, ct)
	if c != http.StatusForbidden || !strings.Contains(body, "supported-calendar-component") {
		t.Errorf("PUT VTODO into Personal = %d %q, want 403 with the precondition", c, body)
	}
	if c, _ := do("DELETE", home+"tasks/w1.ics", "", nil); c != http.StatusNoContent {
		t.Errorf("DELETE task = %d, want 204", c)
	}
	if _, err := a.Tasks.Get(userID, "w1"); err == nil {
		t.Error("task still stored after DELETE")
	}
}
