package agent

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A CalDAV collection DELETE must purge a calendar completely through the real
// listener. It guards these lines in agent.go: MkcalendarHandler wrapping the
// DAV handler (go-webdav alone refuses to delete a collection), SetTaskStore
// on the CalDAV backend (PurgeCalendar skips tasks when it is nil), the ACL on
// the CalendarStore (grants go with the calendar), and the /pub/calendar route
// that must answer 404 once the calendar and its token are gone.
func TestCalDAVCollectionDeleteWiredIntoAgent(t *testing.T) {
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
	// A real grantee: a made-up ID could be swept by the leader's orphan-grant
	// sweep and make the "grant gone" check pass for the wrong reason.
	readerID, err := auth.CreateUser(a, a.Metadata, "reader@localhost", "Reader", "pass1234", false)
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}
	// A second event calendar, or the delete is refused as the last one.
	if _, err := a.Calendars.EnsureDefault(ownerID); err != nil {
		t.Fatal(err)
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Calendars.Create(ownerID, &bongov1.Calendar{Guid: "work", Name: "Work", Published: true, PublishToken: token}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Events.Create(ownerID, &bongov1.Event{Guid: "wired-ev", CalendarId: "work", Title: "W", StartTime: 1792058400, EndTime: 1792062000}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Tasks.Create(ownerID, &bongov1.Task{Guid: "wired-task", CalendarId: "work", Summary: "T"}); err != nil {
		t.Fatal(err)
	}
	if err := a.ACL.SetCalendarGrant(ownerID, "work", readerID, store.GrantLevelRead, store.GrantStateAccepted, ownerID, time.Now()); err != nil {
		t.Fatal(err)
	}

	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	feed := base + "/pub/calendar/" + ownerID + "/work/" + base64.RawURLEncoding.EncodeToString(token) + ".ics"
	status := func(method, url string, auth bool) int {
		t.Helper()
		req, _ := http.NewRequest(method, url, nil)
		if auth {
			req.SetBasicAuth("admin@localhost", "admin")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, url, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if c := status(http.MethodGet, feed, false); c != http.StatusOK {
		t.Fatalf("feed before delete = %d, want 200", c)
	}

	del := base + "/caldav/" + ownerID + "/calendars/work/"
	if c := status(http.MethodDelete, del, true); c != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", c)
	}

	if _, err := a.Calendars.Get(ownerID, "work"); err == nil {
		t.Error("calendar still stored")
	}
	if _, err := a.Events.Get(ownerID, "wired-ev"); err == nil {
		t.Error("event of the calendar survives")
	}
	tasks, err := a.Tasks.ListAll(ownerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tasks {
		if tk.Guid == "wired-task" {
			t.Error("task of the calendar survives: SetTaskStore not wired")
		}
	}
	if lvl, _ := a.ACL.CalendarAccessLevel(ownerID, "work", readerID); lvl != "" {
		t.Errorf("grantee still has %q", lvl)
	}
	if c := status(http.MethodGet, feed, false); c != http.StatusNotFound {
		t.Errorf("feed after delete = %d, want 404", c)
	}
	if c := status(http.MethodDelete, del, true); c != http.StatusNotFound {
		t.Errorf("second DELETE = %d, want 404", c)
	}
}
