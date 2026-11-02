package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongoimap "github.com/bongo-project/bongo/internal/imap"
	"github.com/bongo-project/bongo/internal/store"
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

// dueReminder is one occurrence whose reminder time has passed and whose
// start has not.
type dueReminder struct {
	userID string
	ev     *bongov1.Event
	start  time.Time
}

// ownerAddresses returns the lower-cased addresses the user owns: the profile
// address plus every address in the address index.
func (a *Agent) ownerAddresses(userID, profileEmail string) map[string]bool {
	mine := map[string]bool{}
	if e := store.NormaliseAddress(profileEmail); e != "" {
		mine[e] = true
	}
	if a.AddressStore != nil {
		list, err := a.AddressStore.ListByUser(userID)
		if err != nil {
			a.logger.Warn("reminders: listing the user's addresses failed", "user", userID, "error", err)
		}
		for _, addr := range list {
			mine[store.NormaliseAddress(addr)] = true
		}
	}
	return mine
}

// userProfile reads a user's profile; nil when it is missing or unreadable.
func (a *Agent) userProfile(userID string) *bongov1.UserProfile {
	data, err := a.Store.Get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		return nil
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		return nil
	}
	return &p
}

// hasDeclinedAttendee is the cheap test that decides whether the owner's
// addresses are needed at all.
func hasDeclinedAttendee(e *bongov1.Event) bool {
	for _, at := range e.GetAttendees() {
		if at.Partstat == "DECLINED" {
			return true
		}
	}
	return false
}

// dueReminders is the one computation both reminder paths share. For each user
// it expands the events around now with ListByDateRange (which applies EXDATEs
// and overrides and drops cancelled overrides) and returns the occurrences
// whose reminder time has passed and whose start has not. Cancelled events and
// events the owner declined are skipped, for every delivery mode.
func (a *Agent) dueReminders(now time.Time, userIDs []string) []dueReminder {
	// An all-day event is stored at UTC midnight but starts at midnight in its
	// owner's zone, up to 14 hours either side of that. Reminders are at most
	// 60 minutes, so a window of -14h..+15h holds every event that can fire
	// now; the fire test below decides which actually do.
	rangeStart := now.Add(-maxZoneOffset).Unix()
	rangeEnd := now.Add(maxZoneOffset + time.Hour).Unix()

	var out []dueReminder
	for _, userID := range userIDs {
		loc := a.reminderZone(userID)
		events, err := a.Events.ListByDateRange(userID, rangeStart, rangeEnd, "")
		if err != nil {
			continue
		}
		var mine map[string]bool
		for _, e := range events {
			if e.ReminderMinutes <= 0 || e.Status == "CANCELLED" {
				continue
			}
			start := reminderStart(e, loc)
			reminderTime := start.Add(-time.Duration(e.ReminderMinutes) * time.Minute)
			// Fire if reminder time is in the past but event hasn't started yet.
			if !now.After(reminderTime) || !now.Before(start) {
				continue
			}
			if hasDeclinedAttendee(e) {
				if mine == nil {
					email := ""
					if p := a.userProfile(userID); p != nil {
						email = p.Email
					}
					mine = a.ownerAddresses(userID, email)
				}
				if store.OwnerPartstat(e, mine) == "DECLINED" {
					continue
				}
			}
			out = append(out, dueReminder{userID: userID, ev: e, start: start})
		}
	}
	return out
}

// usersWithData lists the IDs found under the /user/ key prefix.
func (a *Agent) usersWithData() []string {
	iter, err := a.Store.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var ids []string
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
		if id := rest[:idx]; !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if err := iter.Close(); err != nil {
		a.logger.Warn("reminders: failed to close user iterator", "error", err)
	}
	return ids
}

// checkReminders fires the browser (SSE) reminders due at now. Events set to
// email-only are left to the email worker.
func (a *Agent) checkReminders(now time.Time, mu *sync.Mutex, sent map[string]bool) {
	if a.Events == nil || a.Notifier == nil || a.Store == nil {
		return
	}
	for _, d := range a.dueReminders(now, a.usersWithData()) {
		e := d.ev
		if e.ReminderEmail == bongov1.ReminderDelivery_REMINDER_DELIVERY_EMAIL {
			continue
		}
		// Occurrences share the series guid, so the start tells them
		// apart: without it every occurrence of an HOURLY series
		// inside the cleared-hourly window would share one key and
		// only the first would ever be announced.
		sentKey := fmt.Sprintf("%s:%s:%d:%d", d.userID, e.Guid, e.ReminderMinutes, d.start.Unix())
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
		a.Notifier.Notify(d.userID, "INBOX", bongoimap.Notification{
			Type:          bongoimap.NotifEventReminder,
			EventTitle:    e.Title,
			EventStart:    d.start.Unix(),
			MinutesBefore: e.ReminderMinutes,
		})
	}
}

// reminderMailTick is how often the email worker looks for due reminders.
const reminderMailTick = 60 * time.Second

// reminderApplyTimeout bounds the record write.
const reminderApplyTimeout = 5 * time.Second

// reminderBarrierTimeout bounds the Raft barrier a new leader runs first.
const reminderBarrierTimeout = 10 * time.Second

// reminderSender builds and delivers one reminder email to the owner's primary
// address through the local pipeline. *handlers.ReminderMailer satisfies it.
type reminderSender interface {
	SendReminder(userID, to string, ev *bongov1.Event, start time.Time, loc *time.Location) error
}

// emailReminderPass is the leader-only email reminder worker. It holds the
// only in-memory state (the term already barriered); everything that stops a
// duplicate lives in Pebble.
type emailReminderPass struct {
	a      *Agent
	sender reminderSender
	// apply writes through Raft; leader reports whether this node leads and
	// the current term; barrier waits until the FSM has applied every entry
	// committed before it. All three are the Agent's, replaceable in tests.
	apply     func(*bongov1.RaftCommand, time.Duration) error
	leader    func() (term string, ok bool)
	barrier   func(time.Duration) error
	barriered string
}

func (a *Agent) newEmailReminderPass(sender reminderSender) *emailReminderPass {
	return &emailReminderPass{
		a:      a,
		sender: sender,
		apply:  a.ApplyCommand,
		leader: func() (string, bool) {
			if !a.IsLeader() {
				return "", false
			}
			return a.Raft.Raft.Stats()["term"], true
		},
		barrier: func(d time.Duration) error { return a.Raft.Raft.Barrier(d).Error() },
	}
}

// runEmailReminders ticks the pass until ctx is cancelled.
func (a *Agent) runEmailReminders(ctx context.Context, p *emailReminderPass) {
	ticker := time.NewTicker(reminderMailTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			p.run(time.Now())
		}
	}
}

// run sends the email reminders due at now and returns how many it sent.
//
// For each one the record is written first, and only a successful write lets
// the mail go out: a failed write skips the send (the next tick tries again),
// so a reminder is never sent twice. A new leader runs a Raft barrier before
// its first pass of a term, because IsLeader can be true before the FSM has
// applied the previous leader's records.
func (p *emailReminderPass) run(now time.Time) int {
	a := p.a
	if a.Events == nil || a.Store == nil || p.sender == nil {
		return 0
	}
	term, ok := p.leader()
	if !ok {
		p.barriered = ""
		return 0
	}
	if p.barriered != term {
		if err := p.barrier(reminderBarrierTimeout); err != nil {
			a.logger.Warn("reminders: raft barrier failed, skipping email pass", "error", err)
			return 0
		}
		p.barriered = term
	}

	userIDs, err := a.Metadata.System.ListUserIDs()
	if err != nil {
		a.logger.Warn("reminders: listing users failed", "error", err)
		return 0
	}
	profiles := map[string]*bongov1.UserProfile{}
	sentCount := 0
	for _, d := range a.dueReminders(now, userIDs) {
		if d.ev.ReminderEmail == bongov1.ReminderDelivery_REMINDER_DELIVERY_BROWSER {
			continue
		}
		prof, seen := profiles[d.userID]
		if !seen {
			prof = a.userProfile(d.userID)
			profiles[d.userID] = prof
		}
		if prof == nil || prof.Disabled || prof.Email == "" {
			continue
		}
		key := store.ReminderSentKey(d.userID, d.ev.Guid, d.start.Unix())
		if _, err := a.Store.Get(key); err == nil {
			continue
		} else if !errors.Is(err, store.ErrNotFound) {
			a.logger.Warn("reminders: reading the sent record failed", "user", d.userID, "event", d.ev.Guid, "error", err)
			continue
		}
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: store.EncodeReminderSent(now)}
		if err := p.apply(cmd, reminderApplyTimeout); err != nil {
			a.logger.Warn("reminders: recording the reminder failed, not sending", "user", d.userID, "event", d.ev.Guid, "error", err)
			continue
		}
		if err := p.sender.SendReminder(d.userID, prof.Email, d.ev, d.start, a.reminderZone(d.userID)); err != nil {
			// The record stays: delivery is at-most-once.
			a.logger.Warn("reminders: email not delivered", "user", d.userID, "event", d.ev.Guid, "occurrence", strconv.FormatInt(d.start.Unix(), 10), "error", err)
			continue
		}
		sentCount++
	}
	return sentCount
}
