package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Markers for the two one-time steps of the tasks start-up job. Each is
// written after its step succeeds, so a failure is retried by the next pass.
var (
	blankEventsMarkerKey   = []byte("/system/migrations/vtodo-blank-events-v1")
	tasksCalendarMarkerKey = []byte("/system/migrations/tasks-calendar-v1")
)

// tasksMigrationInterval is how often a node that has not yet seen both
// markers checks whether it is leader and the job is due. A variable so tests
// can shorten it.
var tasksMigrationInterval = 30 * time.Second

func (a *Agent) markerSet(key []byte) (bool, error) {
	_, err := a.Store.Get(key)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return false, err
}

func (a *Agent) setMarker(key []byte) error {
	var applier store.RaftApplier = a
	if a.migrationApplier != nil {
		applier = a.migrationApplier
	}
	return applier.ApplyCommand(&bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: key, Value: []byte("1")}, 5*time.Second)
}

// migrateTasksPass does the two one-time jobs that come with tasks:
//
//  1. Lists, in the log, the events a VTODO PUT left behind before CalDAV
//     refused them: an event whose iCalendar holds no VEVENT but a VTODO or
//     VJOURNAL. It logs the user, guid and calendar only, never the summary
//     or description, and deletes nothing.
//  2. Gives every existing user who already has calendars a Tasks calendar
//     for VTODO. go-webdav serves neither MKCALENDAR nor a component set on
//     MKCOL, so without it an existing account has nowhere to put a task.
//     A user with no calendars gets one on the first EnsureDefault.
//
// It writes only when isLeader reports true, checked before anything is read.
// It reports whether both steps are finished (marked). It returns false on a
// follower and on any error; the caller retries. Startup does not fail on it.
func (a *Agent) migrateTasksPass(isLeader func() bool) (done bool) {
	if a.Store == nil || a.Calendars == nil || !isLeader() {
		return false
	}
	blankDone, err := a.markerSet(blankEventsMarkerKey)
	if err != nil {
		a.logger.Warn("tasks-migration: blank-events marker unreadable", "error", err)
		return false
	}
	calDone, err := a.markerSet(tasksCalendarMarkerKey)
	if err != nil {
		a.logger.Warn("tasks-migration: tasks-calendar marker unreadable", "error", err)
		return false
	}
	if blankDone && calDone {
		return true
	}

	users, blank, err := a.scanForTasksMigration(!blankDone, isLeader)
	if err != nil {
		a.logger.Warn("tasks-migration: scan failed", "error", err)
		return false
	}
	if !blankDone {
		if blank > 0 {
			a.logger.Warn("tasks-migration: events stored from a VTODO or VJOURNAL PUT are listed above; they were left in place",
				"count", blank)
		}
		if err := a.setMarker(blankEventsMarkerKey); err != nil {
			a.logger.Warn("tasks-migration: blank-events marker not written", "error", err)
			return false
		}
	}
	if !calDone {
		added := 0
		for _, userID := range users {
			if !isLeader() {
				return false
			}
			ok, err := a.addTasksCalendar(userID)
			if err != nil {
				a.logger.Warn("tasks-migration: tasks calendar not added", "user", userID, "error", err)
				return false
			}
			if ok {
				added++
			}
		}
		if added > 0 {
			a.logger.Info("tasks-migration: added the Tasks calendar", "users", added)
		}
		if err := a.setMarker(tasksCalendarMarkerKey); err != nil {
			a.logger.Warn("tasks-migration: tasks-calendar marker not written", "error", err)
			return false
		}
	}
	return true
}

// scanForTasksMigration streams the user key space once. It returns the IDs
// of the users that have a profile and, when logBlank is set, logs each blank
// event as it is met and returns how many there were. Nothing but the user
// IDs is kept.
func (a *Agent) scanForTasksMigration(logBlank bool, isLeader func() bool) (users []string, blank int, err error) {
	iter, err := a.Store.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, 0, fmt.Errorf("scanning users: %w", err)
	}
	defer iter.Close()

	for iter.Next() {
		parts := strings.SplitN(strings.TrimPrefix(string(iter.Key()), "/user/"), "/", 3)
		if len(parts) < 2 {
			continue
		}
		switch {
		case len(parts) == 2 && parts[1] == "profile":
			users = append(users, parts[0])
		case logBlank && len(parts) == 3 && parts[1] == "event":
			val, verr := iter.Value()
			if verr != nil {
				continue
			}
			var ev bongov1.Event
			if proto.Unmarshal(val, &ev) != nil || !isBlankTodoEvent(ev.IcalRaw) {
				continue
			}
			blank++
			a.logger.Warn("tasks-migration: event holds no VEVENT, left in place",
				"user", parts[0], "guid", parts[2], "calendar", ev.CalendarId)
		}
	}
	return users, blank, nil
}

// isBlankTodoEvent reports whether raw is an iCalendar object with no VEVENT
// but a VTODO or VJOURNAL: what a PUT of one of those left behind as an event.
func isBlankTodoEvent(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return false
	}
	other := false
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompEvent:
			return false
		case ical.CompToDo, ical.CompJournal:
			other = true
		}
	}
	return other
}

// addTasksCalendar creates the Tasks calendar for a user who has calendars
// and none that holds tasks. It reports whether it created one. A user with
// no calendars is left to EnsureDefault.
func (a *Agent) addTasksCalendar(userID string) (bool, error) {
	cals, err := a.Calendars.List(userID)
	if err != nil {
		return false, fmt.Errorf("listing calendars: %w", err)
	}
	if len(cals) == 0 {
		return false, nil
	}
	for _, c := range cals {
		if c.Guid == store.TasksCalendarGUID || store.CalendarAllows(c, store.ComponentTodo) {
			return false, nil
		}
	}
	if _, err := a.Calendars.Create(userID, store.NewTasksCalendar()); err != nil {
		return false, fmt.Errorf("creating tasks calendar: %w", err)
	}
	return true, nil
}

// runTasksMigration retries migrateTasksPass on every tick until it reports
// done or ctx ends, so a node that becomes leader after boot still runs the
// job. Followers poll until the markers replicate to them, then stop.
func (a *Agent) runTasksMigration(ctx context.Context, interval time.Duration, isLeader func() bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.migrateTasksPass(isLeader) {
				return
			}
		}
	}
}
