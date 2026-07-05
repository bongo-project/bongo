package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	bongoimap "github.com/bongo-go/bongo-go/internal/imap"
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
			a.checkReminders(&mu, sent)
		}
	}
}

func (a *Agent) checkReminders(mu *sync.Mutex, sent map[string]bool) {
	if a.Events == nil || a.Notifier == nil || a.Store == nil {
		return
	}

	now := time.Now()
	// Look 60 minutes ahead.
	rangeEnd := now.Add(60 * time.Minute).Unix()
	rangeStart := now.Unix()

	// Scan all users' events via Pebble prefix scan.
	// We iterate the user prefix to find user IDs, then check their events.
	iter, err := a.Store.PrefixScan([]byte("/user/"))
	if err != nil {
		return
	}
	defer iter.Close()

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
		events, err := a.Events.ListByDateRange(userID, rangeStart, rangeEnd, "")
		if err != nil {
			continue
		}

		for _, e := range events {
			if e.ReminderMinutes <= 0 {
				continue
			}

			reminderTime := time.Unix(e.StartTime, 0).Add(-time.Duration(e.ReminderMinutes) * time.Minute)
			// Fire if reminder time is in the past but event hasn't started yet.
			if now.After(reminderTime) && now.Before(time.Unix(e.StartTime, 0)) {
				sentKey := fmt.Sprintf("%s:%s:%d", userID, e.Guid, e.ReminderMinutes)
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
					EventStart:    e.StartTime,
					MinutesBefore: e.ReminderMinutes,
				})
			}
		}
	}
}
