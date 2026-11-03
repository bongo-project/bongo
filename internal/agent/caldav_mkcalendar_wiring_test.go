package agent

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/config"
)

// go-webdav answers MKCALENDAR with 405, so calendar creation over CalDAV
// exists only because Run wraps the DAV handler in MkcalendarHandler. The
// caldav package tests build that chain themselves; this one goes through
// the real listener so a dropped wiring line fails here.
func TestCalDAVMkcalendarWiredIntoAgent(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	a := runWiringAgent(t, cfg, testLogger(), nil)

	adminID, err := a.Auth.Authenticate("admin@localhost", "admin")
	if err != nil {
		t.Fatal(err)
	}
	body := `<?xml version="1.0" encoding="utf-8"?>` +
		`<C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:set><D:prop>` +
		`<D:displayname>Wired</D:displayname>` +
		`<C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set>` +
		`</D:prop></D:set></C:mkcalendar>`
	url := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port) + "/caldav/" + adminID + "/calendars/wired-cal/"
	req, _ := http.NewRequest("MKCALENDAR", url, strings.NewReader(body))
	req.SetBasicAuth("admin@localhost", "admin")
	req.Header.Set("Content-Type", "application/xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MKCALENDAR: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("MKCALENDAR = %d, want 201: %s", resp.StatusCode, b)
	}

	cal, err := a.Calendars.Get(adminID, "wired-cal")
	if err != nil {
		t.Fatalf("calendar not stored after MKCALENDAR: %v", err)
	}
	if cal.Name != "Wired" {
		t.Errorf("stored calendar name = %q, want Wired", cal.Name)
	}
}
