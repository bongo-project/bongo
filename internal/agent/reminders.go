package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongoimap "github.com/bongo-project/bongo/internal/imap"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// runReminderChecker scans events every 60 seconds and fires SSE reminders
// for events that start within reminder_minutes of the current time.
func (a *Agent) runReminderChecker(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Track which reminders we've already sent to avoid duplicates.
	var mu sync.Mutex
	sent := make(map[string]bool) // key: "userID:guid:minutesBefore"

	// Clean old entries every hour.
	cleanTicker := time.NewTicker(1 * time.Hour)
	defer cleanTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanTicker.C:
			mu.Lock()
			sent = make(map[string]bool)
			mu.Unlock()
		case <-ticker.C:
			a.checkReminders(time.Now(), &mu, sent)
		}
	}
}

// maxZoneOffset is the largest UTC offset in use (Pacific/Kiritimati, UTC+14).
const maxZoneOffset = 14 * time.Hour

// reminderZone returns the owner's preference time zone, UTC when preferences
// are unavailable (edge and agent roles) or the stored name is invalid.
func (a *Agent) reminderZone(userID string) *time.Location {
	if a.Preferences == nil {
		return time.UTC
	}
	return a.Preferences.Location(userID)
}

// reminderStart is the instant a reminder counts down to. A timed event
// starts at its stored time. An all-day event starts at midnight of its date
// in the owner's zone; its stored time is a UTC midnight (or a legacy local
// midnight, which AllDayDate maps back to the intended date).
func reminderStart(e *bongov1.Event, loc *time.Location) time.Time {
	if e.AllDay {
		d := icalevent.AllDayDate(e.StartTime)
		return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	}
	return time.Unix(e.StartTime, 0)
}

func (a *Agent) checkReminders(now time.Time, mu *sync.Mutex, sent map[string]bool) {
	if a.Events == nil || a.Notifier == nil || a.Store == nil {
		return
	}

	// An all-day event is stored at UTC midnight but starts at midnight in its
	// owner's zone, up to 14 hours either side of that. Reminders are at most
	// 60 minutes, so a window of -14h..+15h holds every event that can fire
	// now; the fire test below decides which actually do.
	rangeStart := now.Add(-maxZoneOffset).Unix()
	rangeEnd := now.Add(maxZoneOffset + time.Hour).Unix()

	// Scan all users' events via Pebble prefix scan.
	// We iterate the user prefix to find user IDs, then check their events.
	iter, err := a.Store.PrefixScan([]byte("/user/"))
	if err != nil {
		return
	}

	usersSeen := make(map[string]bool)
	for iter.Next() {
		key := string(iter.Key())
		// Extract userID from /user/{userID}/...
		if len(key) < 7 {
			continue
		}
		rest := key[6:] // after "/user/"
		idx := 0
		for idx < len(rest) && rest[idx] != '/' {
			idx++
		}
		if idx == 0 {
			continue
		}
		userID := rest[:idx]
		if usersSeen[userID] {
			continue
		}
		usersSeen[userID] = true
	}
	if err := iter.Close(); err != nil {
		a.logger.Warn("reminders: failed to close user iterator", "error", err)
	}

	for userID := range usersSeen {
		loc := a.reminderZone(userID)
		events, err := a.Events.ListByDateRange(userID, rangeStart, rangeEnd, "")
		if err != nil {
			continue
		}

		for _, e := range events {
			if e.ReminderMinutes <= 0 {
				continue
			}

			start := reminderStart(e, loc)
			reminderTime := start.Add(-time.Duration(e.ReminderMinutes) * time.Minute)
			// Fire if reminder time is in the past but event hasn't started yet.
			if now.After(reminderTime) && now.Before(start) {
				// Occurrences share the series guid, so the start tells them
				// apart: without it every occurrence of an HOURLY series
				// inside the cleared-hourly window would share one key and
				// only the first would ever be announced.
				sentKey := fmt.Sprintf("%s:%s:%d:%d", userID, e.Guid, e.ReminderMinutes, start.Unix())
				mu.Lock()
				alreadySent := sent[sentKey]
				if !alreadySent {
					sent[sentKey] = true
				}
				mu.Unlock()

				if alreadySent {
					continue
				}

				// Send reminder notification via INBOX channel (SSE listens on INBOX).
				a.Notifier.Notify(userID, "INBOX", bongoimap.Notification{
					Type:          bongoimap.NotifEventReminder,
					EventTitle:    e.Title,
					EventStart:    start.Unix(),
					MinutesBefore: e.ReminderMinutes,
				})
			}
		}
	}
}
