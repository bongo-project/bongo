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

// blankEventsMarkerKey marks the one-time listing as done. It is written after
// the listing succeeds, so a failure is retried by the next pass.
var blankEventsMarkerKey = []byte("/system/migrations/vtodo-blank-events-v1")

// tasksMigrationInterval is how often a node that has not yet seen the
// marker checks whether it is leader and the job is due. A variable so tests
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

// migrateTasksPass does the one-time job that comes with tasks: it lists, in
// the log, the events a VTODO PUT left behind before CalDAV refused them, an
// event whose iCalendar holds no VEVENT but a VTODO or VJOURNAL. It logs the
// user, guid and calendar only, never the summary or description, and deletes
// nothing.
//
// No Tasks calendar is created here; EnsureDefault back-fills it for any user
// without one.
//
// It writes only when isLeader reports true, checked before anything is read.
// It reports whether the job is finished (marked). It returns false on a
// follower and on any error; the caller retries. Startup does not fail on it.
func (a *Agent) migrateTasksPass(isLeader func() bool) (done bool) {
	if a.Store == nil || !isLeader() {
		return false
	}
	blankDone, err := a.markerSet(blankEventsMarkerKey)
	if err != nil {
		a.logger.Warn("tasks-migration: blank-events marker unreadable", "error", err)
		return false
	}
	if blankDone {
		return true
	}

	blank, err := a.scanForBlankEvents()
	if err != nil {
		a.logger.Warn("tasks-migration: scan failed", "error", err)
		return false
	}
	if blank > 0 {
		a.logger.Warn("tasks-migration: events stored from a VTODO or VJOURNAL PUT are listed above; they were left in place",
			"count", blank)
	}
	if err := a.setMarker(blankEventsMarkerKey); err != nil {
		a.logger.Warn("tasks-migration: blank-events marker not written", "error", err)
		return false
	}
	return true
}

// scanForBlankEvents streams the event key space once, logs each blank event
// as it is met and returns how many there were. Nothing is kept.
func (a *Agent) scanForBlankEvents() (blank int, err error) {
	iter, err := a.Store.PrefixScan([]byte("/user/"))
	if err != nil {
		return 0, fmt.Errorf("scanning events: %w", err)
	}
	defer iter.Close()

	for iter.Next() {
		parts := strings.SplitN(strings.TrimPrefix(string(iter.Key()), "/user/"), "/", 3)
		if len(parts) != 3 || parts[1] != "event" {
			continue
		}
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
	return blank, nil
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
