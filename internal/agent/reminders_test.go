package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// reminderTestAgent boots a dev-mode Agent with its own gossip and control
// ports so it can run beside other tests.
func reminderTestAgent(t *testing.T) *Agent {
	t.Helper()
	cfg := config.Defaults()
	isolatePorts(t, cfg)
	cfg.Server.Role = "dev"
	cfg.SMTP.Enabled = false
	cfg.POP3.Enabled = false
	cfg.IMAP.Enabled = false

	a, err := NewAgent(cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !a.Ready() {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent did not become ready")
	}
	return a
}

// lastStart is the EventStart of the most recent notification fires saw.
var lastStart int64

// fires runs one reminder tick at now against a fresh sent map and reports
// whether the owner was notified.
func fires(t *testing.T, a *Agent, ch chan bongoimap.Notification, now time.Time) bool {
	t.Helper()
	var mu sync.Mutex
	a.checkReminders(now, &mu, map[string]bool{})
	select {
	case n := <-ch:
		if n.Type != bongoimap.NotifEventReminder {
			t.Fatalf("unexpected notification type %v", n.Type)
		}
		lastStart = n.EventStart
		return true
	default:
		return false
	}
}

func setZone(t *testing.T, a *Agent, user, zone string) {
	t.Helper()
	prefs := store.DefaultPreferences()
	prefs.Timezone = zone
	if err := a.Preferences.Set(user, prefs); err != nil {
		t.Fatal(err)
	}
}

func TestAllDayReminderUsesOwnerZone(t *testing.T) {
	a := reminderTestAgent(t)

	// All-day on 2026-11-05 (UTC midnight), 15 minute reminder.
	start := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name, zone string
		// local midnight of 5 November in the zone, as an instant
		localMidnight time.Time
	}{
		{"new york (EST)", "America/New_York", time.Date(2026, 11, 5, 5, 0, 0, 0, time.UTC)},
		{"kiritimati (UTC+14)", "Pacific/Kiritimati", time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)},
		{"Etc/GMT+12 (UTC-12)", "Etc/GMT+12", time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)},
		{"utc", "UTC", start},
		{"empty falls back to UTC", "", start},
		{"invalid falls back to UTC", "Not/AZone", start},
		{"Local is rejected", "Local", start},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := "u" + string(rune('a'+i))
			setZone(t, a, user, tc.zone)
			ch := a.Notifier.Subscribe(user, "INBOX")
			if _, err := a.Events.Create(user, &bongov1.Event{
				Title: "Holiday", CalendarId: "default", AllDay: true, ReminderMinutes: 15,
				StartTime: start.Unix(), EndTime: start.Add(24 * time.Hour).Unix(),
			}); err != nil {
				t.Fatal(err)
			}
			fireAt := tc.localMidnight.Add(-15 * time.Minute)
			if !fires(t, a, ch, fireAt.Add(time.Minute)) {
				t.Errorf("no reminder at %s", fireAt.Add(time.Minute))
			} else if lastStart != tc.localMidnight.Unix() {
				t.Errorf("EventStart = %d, want local midnight %d", lastStart, tc.localMidnight.Unix())
			}
			if fires(t, a, ch, fireAt.Add(-time.Minute)) {
				t.Errorf("reminder fired early at %s", fireAt.Add(-time.Minute))
			}
			if fires(t, a, ch, tc.localMidnight.Add(time.Minute)) {
				t.Errorf("reminder fired after local midnight")
			}
			// The pre-fix behaviour: 15 minutes before UTC midnight.
			if tc.localMidnight != start && fires(t, a, ch, start.Add(-10*time.Minute)) {
				t.Errorf("reminder fired relative to UTC midnight")
			}
		})
	}
}

// A zero-length all-day event (DTEND == DTSTART, or DURATION:PT0S) ends at its
// own UTC midnight. In a zone west of UTC its local midnight is later, so the
// overlap test only finds it when the query reaches back before now.
func TestZeroLengthAllDayReminderWestOfUTC(t *testing.T) {
	a := reminderTestAgent(t)
	setZone(t, a, "west", "Etc/GMT+12")
	ch := a.Notifier.Subscribe("west", "INBOX")
	start := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	if _, err := a.Events.Create("west", &bongov1.Event{
		Title: "Holiday", CalendarId: "default", AllDay: true, ReminderMinutes: 14,
		StartTime: start.Unix(), EndTime: start.Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	localMidnight := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if fires(t, a, ch, time.Date(2026, 11, 5, 11, 45, 0, 0, time.UTC)) {
		t.Error("fired before its window")
	}
	if !fires(t, a, ch, time.Date(2026, 11, 5, 11, 46, 30, 0, time.UTC)) {
		t.Error("zero-length all-day event did not fire at 11:46:30Z")
	}
	if fires(t, a, ch, localMidnight.Add(time.Minute)) {
		t.Error("fired after local midnight")
	}
}

func TestTimedReminderUnchanged(t *testing.T) {
	a := reminderTestAgent(t)
	setZone(t, a, "timed", "Pacific/Kiritimati")
	ch := a.Notifier.Subscribe("timed", "INBOX")
	start := time.Date(2026, 11, 5, 14, 37, 0, 0, time.UTC)
	if _, err := a.Events.Create("timed", &bongov1.Event{
		Title: "Call", CalendarId: "default", ReminderMinutes: 15,
		StartTime: start.Unix(), EndTime: start.Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if fires(t, a, ch, start.Add(-20*time.Minute)) {
		t.Error("fired before its window")
	}
	if !fires(t, a, ch, start.Add(-10*time.Minute)) {
		t.Error("did not fire inside its window")
	}
	if fires(t, a, ch, start.Add(time.Minute)) {
		t.Error("fired after start")
	}
}
