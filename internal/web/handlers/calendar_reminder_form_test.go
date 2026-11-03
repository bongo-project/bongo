package handlers

import (
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func reminderForm(minutes, delivery string) url.Values {
	f := url.Values{
		"title": {"Remind me"}, "calendar_id": {"default"}, "rrule": {"FREQ=WEEKLY"},
		"start_date": {"2030-10-14"}, "start_time": {"09:00"}, "end_date": {"2030-10-14"}, "end_time": {"10:00"},
		"reminder_minutes": {minutes},
	}
	if delivery != "-" {
		f.Set("reminder_delivery", delivery)
	}
	return f
}

func TestReminderFormValues(t *testing.T) {
	cases := []struct {
		name, minutes, delivery string
		wantStatus              int
		wantMinutes             int32
		wantDelivery            bongov1.ReminderDelivery
	}{
		{"browser", "15", "browser", http.StatusSeeOther, 15, bongov1.ReminderDelivery_REMINDER_DELIVERY_BROWSER},
		{"absent means browser", "15", "-", http.StatusSeeOther, 15, bongov1.ReminderDelivery_REMINDER_DELIVERY_BROWSER},
		{"email", "30", "email", http.StatusSeeOther, 30, bongov1.ReminderDelivery_REMINDER_DELIVERY_EMAIL},
		{"both", "60", "both", http.StatusSeeOther, 60, bongov1.ReminderDelivery_REMINDER_DELIVERY_BOTH},
		{"none", "0", "email", http.StatusSeeOther, 0, bongov1.ReminderDelivery_REMINDER_DELIVERY_EMAIL},
		{"unknown delivery", "15", "sms", http.StatusBadRequest, 0, 0},
		{"enum number is not a delivery", "15", "1", http.StatusBadRequest, 0, 0},
		{"minutes over the largest option", "61", "email", http.StatusBadRequest, 0, 0},
		{"huge minutes", "1440", "email", http.StatusBadRequest, 0, 0},
		{"ninety minutes", "90", "email", http.StatusBadRequest, 0, 0},
		{"negative minutes", "-5", "email", http.StatusBadRequest, 0, 0},
		{"unoffered minutes", "7", "email", http.StatusBadRequest, 0, 0},
		{"non-numeric minutes", "abc", "email", http.StatusBadRequest, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newInstEnv(t)
			w := httptest.NewRecorder()
			e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", reminderForm(tc.minutes, tc.delivery)))
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			all, _, err := e.events.ListAll(quotaUser, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantStatus != http.StatusSeeOther {
				if len(all) != 0 {
					t.Fatalf("a refused form stored %d events", len(all))
				}
				return
			}
			if len(all) != 1 {
				t.Fatalf("events = %d", len(all))
			}
			if all[0].ReminderMinutes != tc.wantMinutes || all[0].ReminderEmail != tc.wantDelivery {
				t.Errorf("stored %d %v, want %d %v", all[0].ReminderMinutes, all[0].ReminderEmail, tc.wantMinutes, tc.wantDelivery)
			}
		})
	}
}

// The delivery survives an edit of the series, and an invalid edit leaves the
// stored event alone; "this and following" carries the form's value to the new
// series.
func TestReminderDeliverySurvivesEditAndSplit(t *testing.T) {
	e := newInstEnv(t)
	w := httptest.NewRecorder()
	e.h.CreateEvent(w, formReq(http.MethodPost, "/calendar/event", reminderForm("30", "email")))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	all, _, _ := e.events.ListAll(quotaUser, 0, 0)
	guid := all[0].Guid

	// Edit the whole series to both.
	if w := e.update(guid, reminderForm("15", "both")); w.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d %s", w.Code, w.Body.String())
	}
	if got := e.get(t, guid); got.ReminderEmail != bongov1.ReminderDelivery_REMINDER_DELIVERY_BOTH || got.ReminderMinutes != 15 {
		t.Fatalf("after edit: %v %d", got.ReminderEmail, got.ReminderMinutes)
	}

	// An invalid edit is refused and changes nothing.
	if w := e.update(guid, reminderForm("15", "sms")); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid edit: %d", w.Code)
	}
	if w := e.update(guid, reminderForm("999", "email")); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid minutes edit: %d", w.Code)
	}
	if got := e.get(t, guid); got.ReminderEmail != bongov1.ReminderDelivery_REMINDER_DELIVERY_BOTH {
		t.Fatalf("refused edit changed the event: %v", got.ReminderEmail)
	}

	// Split from the 4 November occurrence with email only.
	f := reminderForm("10", "email")
	f.Set("start_date", "2030-11-04")
	f.Set("end_date", "2030-11-04")
	f.Set("occurrence", ridNov04Timed)
	f.Set("scope", "following")
	if w := e.update(guid, f); w.Code != http.StatusSeeOther {
		t.Fatalf("split: %d %s", w.Code, w.Body.String())
	}
	ns := e.get(t, e.otherGuid(t, guid))
	if ns.ReminderEmail != bongov1.ReminderDelivery_REMINDER_DELIVERY_EMAIL || ns.ReminderMinutes != 10 {
		t.Errorf("new series: %v %d, want email 10", ns.ReminderEmail, ns.ReminderMinutes)
	}
	if got := e.get(t, guid); got.ReminderEmail != bongov1.ReminderDelivery_REMINDER_DELIVERY_BOTH {
		t.Errorf("the original series changed: %v", got.ReminderEmail)
	}
}

// The reminder names the zone by its IANA name and the date in the user's
// language, never Go's "MST" abbreviation or English month names.
func TestReminderMailerLocaleDate(t *testing.T) {
	denver := mustZone(t, "America/Denver")
	berlin := mustZone(t, "Europe/Berlin")
	cases := []struct {
		name, locale, start string
		allDay              bool
		zone                *time.Location
		want                string
	}{
		// 2030-01-07 16:00Z is 09:00 on Monday 7 January in Denver (MST).
		{"timed winter", "de", "2030-01-07T16:00:00Z", false, denver, "Mo 7 Jan 2030 09:00 (America/Denver)"},
		// 2030-03-11 is Monday; MDT is in force, so 15:00Z is 09:00.
		{"timed march", "de", "2030-03-11T15:00:00Z", false, denver, "Mo 11 Mär 2030 09:00 (America/Denver)"},
		{"timed may", "de", "2030-05-06T15:00:00Z", false, denver, "Mo 6 Mai 2030 09:00 (America/Denver)"},
		// An all-day start is local midnight in the owner's zone, as
		// reminderStart (internal/agent/reminders.go) passes it. In Berlin that
		// is 22:00Z the day before, so the date must be read in the zone.
		{"all day", "de", "2030-05-06", true, denver, "Mo 6 Mai 2030"},
		{"all day east of UTC", "de", "2030-05-06", true, berlin, "Mo 6 Mai 2030"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSender{}
			m := NewReminderMailer(f, fakePrefs{"u1": tc.locale}, nil)
			ev := &bongov1.Event{Title: "Standup", AllDay: tc.allDay}
			var start time.Time
			if tc.allDay {
				d, err := time.ParseInLocation(time.DateOnly, tc.start, tc.zone)
				if err != nil {
					t.Fatal(err)
				}
				start = d
			} else {
				start = mustTime(t, tc.start)
			}
			if err := m.SendReminder("u1", "me@example.com", ev, start, tc.zone); err != nil {
				t.Fatal(err)
			}
			decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(f.sent[0].raw))))
			if err != nil {
				t.Fatal(err)
			}
			raw := string(decoded)
			if !strings.Contains(raw, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, raw)
			}
			for _, bad := range []string{"MST", "MDT", "January", "March", "May ", "Monday"} {
				if strings.Contains(raw, bad) {
					t.Errorf("%q in:\n%s", bad, raw)
				}
			}
			if tc.allDay && (strings.Contains(raw, "Denver") || strings.Contains(raw, "Berlin")) {
				t.Errorf("all-day reminder names a zone:\n%s", raw)
			}
		})
	}
}

func TestReminderMailerMessage(t *testing.T) {
	cases := []struct {
		name, locale, title, location string
		wantSubject, wantWhen         string
	}{
		{"english", "en", "Standup", "Room 1", "Reminder: Standup", "Mon 6 May 2030 09:00 (UTC)"},
		{"german", "de", "Standup", "", "Erinnerung: Standup", "Mo 6 Mai 2030 09:00 (UTC)"},
		{"header injection", "en", "Hi\r\nBcc: evil@example.net", "L\r\nX-Evil: 1", "Reminder: Hi  Bcc: evil@example.net", "Mon 6 May 2030 09:00 (UTC)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSender{}
			m := NewReminderMailer(f, fakePrefs{"u1": tc.locale}, nil)
			ev := &bongov1.Event{Title: tc.title, Location: tc.location, Description: "secret description"}
			start := mustTime(t, "2030-05-06T09:00:00Z")
			if err := m.SendReminder("u1", "me@example.com", ev, start, nil); err != nil {
				t.Fatal(err)
			}
			if f.count() != 1 {
				t.Fatalf("sent %d", f.count())
			}
			got := f.sent[0]
			if got.from != "me@example.com" || len(got.rcpts) != 1 || got.rcpts[0] != "me@example.com" {
				t.Errorf("envelope = %q -> %v", got.from, got.rcpts)
			}
			raw := string(got.raw)
			if !strings.Contains(raw, "Auto-Submitted: auto-generated\r\n") {
				t.Errorf("no Auto-Submitted header:\n%s", raw)
			}
			if !strings.Contains(raw, "Subject: "+tc.wantSubject+"\r\n") {
				t.Errorf("subject want %q in:\n%s", tc.wantSubject, raw)
			}
			if strings.Contains(raw, "\r\nBcc:") || strings.Contains(raw, "\r\nX-Evil:") {
				t.Errorf("header injection:\n%s", raw)
			}
			if strings.Contains(raw, "secret description") {
				t.Error("the description was mailed")
			}
			if !strings.Contains(raw, tc.wantWhen) {
				t.Errorf("start %q missing:\n%s", tc.wantWhen, raw)
			}
		})
	}

	t.Run("not reached is an error", func(t *testing.T) {
		f := &fakeSender{miss: []string{"me@example.com"}}
		m := NewReminderMailer(f, nil, nil)
		if err := m.SendReminder("u1", "me@example.com", &bongov1.Event{Title: "x"}, mustTime(t, "2030-05-06T09:00:00Z"), nil); err == nil {
			t.Error("a refused delivery returned nil")
		}
	})
	t.Run("invalid address", func(t *testing.T) {
		f := &fakeSender{}
		m := NewReminderMailer(f, nil, nil)
		if err := m.SendReminder("u1", "bad\r\naddr@example.com", &bongov1.Event{Title: "x"}, mustTime(t, "2030-05-06T09:00:00Z"), nil); err == nil || f.count() != 0 {
			t.Error("an address with control characters was accepted")
		}
	})
}
