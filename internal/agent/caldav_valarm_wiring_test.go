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
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A VEVENT with a VALARM that a CalDAV client stores creates no server
// reminder: the alarm stays in the client's own copy. The positive control
// gives the same event a reminder through the store and shows it is due at the
// same instant, so "nothing due" is not just an empty result.
func TestCalDAVPutWithValarmSetsNoServerReminder(t *testing.T) {
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false
	a := runWiringAgent(t, cfg, testLogger(), nil)

	userID, err := auth.CreateUser(a, a.Metadata, "alarm@localhost", "Alarm", "pass1234", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	start := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	end := start.Add(time.Hour)
	const stamp = "20060102T150405Z"
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VEVENT\r\nUID:valarm1\r\n" +
		"DTSTAMP:" + start.Format(stamp) + "\r\nDTSTART:" + start.Format(stamp) + "\r\nDTEND:" + end.Format(stamp) + "\r\n" +
		"SUMMARY:Alarmed\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nDESCRIPTION:Alarmed\r\nTRIGGER:-PT15M\r\nEND:VALARM\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"

	url := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port) + "/caldav/" + userID + "/calendars/default/valarm1.ics"
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(ics))
	req.SetBasicAuth("alarm@localhost", "pass1234")
	req.Header.Set("Content-Type", "text/calendar")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT = %d: %s", resp.StatusCode, body)
	}

	events, err := a.Events.ListByDateRange(userID, start.Unix()-60, end.Unix()+60, "")
	if err != nil {
		t.Fatal(err)
	}
	var stored *bongov1.Event
	for _, e := range events {
		if e.Title == "Alarmed" {
			stored = e
		}
	}
	if stored == nil {
		t.Fatalf("the PUT event is not stored (%d events in range)", len(events))
	}
	if stored.ReminderMinutes != 0 || stored.ReminderEmail != bongov1.ReminderDelivery_REMINDER_DELIVERY_BROWSER {
		t.Errorf("VALARM set reminder minutes %d, delivery %v; want none", stored.ReminderMinutes, stored.ReminderEmail)
	}

	fireAt := start.Add(-10 * time.Minute)
	if got := a.dueReminders(fireAt, []string{userID}); len(got) != 0 {
		t.Errorf("%d reminders due for a VALARM-only event, want 0", len(got))
	}

	// Positive control: the same event with a server reminder is due.
	stored.ReminderMinutes = 15
	if err := a.Events.Update(userID, stored.Guid, stored); err != nil {
		t.Fatal(err)
	}
	if got := a.dueReminders(fireAt, []string{userID}); len(got) != 1 {
		t.Errorf("%d reminders due with ReminderMinutes set, want 1 (control)", len(got))
	}
}
