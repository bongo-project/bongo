package store

import (
	"errors"
	"fmt"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Refusals of CheckCalendarDeletable. Web and CalDAV map them the same way:
// protected is 403, the other two 409.
var (
	// ErrCalendarProtected is the Tasks calendar and the "default" calendar.
	ErrCalendarProtected = errors.New("calendar cannot be deleted")
	// ErrCalendarSubscribed is a subscription; it is removed by unsubscribing.
	ErrCalendarSubscribed = errors.New("calendar is a subscription")
	// ErrLastEventCalendar is the user's only calendar that holds events and
	// is not a subscription.
	ErrLastEventCalendar = errors.New("last event calendar")
)

// defaultCalendarGUID is the ID EnsureDefault gives a new user's event
// calendar, and the one an empty event calendar_id means.
const defaultCalendarGUID = "default"

// CheckCalendarDeletable says whether the user's calendar calID may be deleted
// by its owner. cals is the owner's full calendar list. It returns an error
// wrapping ErrNotFound when calID is not in cals, and otherwise one of
// ErrCalendarProtected, ErrCalendarSubscribed or ErrLastEventCalendar.
func CheckCalendarDeletable(cals []*bongov1.Calendar, calID string) error {
	var cal *bongov1.Calendar
	for _, c := range cals {
		if c.Guid == calID {
			cal = c
			break
		}
	}
	switch {
	case cal == nil:
		return fmt.Errorf("calendar %q: %w", calID, ErrNotFound)
	case protectedCalendar(calID):
		return ErrCalendarProtected
	case IsSubscribed(cal):
		return ErrCalendarSubscribed
	}
	for _, c := range cals {
		if c.Guid != calID && CalendarAllows(c, ComponentEvent) && !IsSubscribed(c) {
			return nil
		}
	}
	return ErrLastEventCalendar
}

func protectedCalendar(calID string) bool {
	return calID == "" || calID == defaultCalendarGUID || calID == TasksCalendarGUID
}

// PurgeCalendar deletes the user's calendar calID, its grants, and every event
// and task filed in it. Publishing is state on the calendar record, so the
// public feed goes with it. ts may be nil (no task store): tasks are then left
// alone. It refuses "default" and the Tasks calendar, whose events and tasks
// an empty calendar_id also means, but does not apply the other rules of
// CheckCalendarDeletable: UnsubscribeCalendar uses it for a subscription.
//
// The writes are separate Raft commands and are not atomic. The calendar goes
// first (CalendarStore.Delete revokes its grants, then deletes it), the events
// and tasks after. The subscription worker's state write finds the calendar
// gone and purges what a refresh in flight created, so this order cannot leave
// a running refresh's events behind; do not reverse it. A failure after the
// calendar delete leaves events or tasks whose calendar is gone. Web views
// still list them (unfiltered date-range queries) and the user can delete them
// one by one, but CalDAV cannot see them. A retry answers not found, because
// the calendar is already gone, and the request answers 500 so the failure is
// known. A later MKCALENDAR with the same client-chosen segment would adopt the
// leftovers. A request that writes an event after the listing is not
// excluded; the existence check of event writes narrows that window to the gap
// between check and write. Check and delete are not serialised either: two
// concurrent deletes of the last two event calendars can both pass the check,
// and EnsureDefault recreates a Personal calendar on the next need. A large
// calendar costs one Raft command per event, so the request may outlast a
// client's timeout; the deletion carries on server side.
func PurgeCalendar(cs *CalendarStore, es *EventStore, ts *TaskStore, userID, calID string) error {
	if protectedCalendar(calID) {
		return ErrCalendarProtected
	}
	if err := cs.Delete(userID, calID); err != nil {
		return fmt.Errorf("deleting calendar: %w", err)
	}
	events, _, err := es.ListAll(userID, 0, 0)
	if err != nil {
		return fmt.Errorf("listing events to remove: %w", err)
	}
	for _, e := range events {
		if e.CalendarId != calID {
			continue
		}
		if err := es.Delete(userID, e.Guid); err != nil {
			return fmt.Errorf("removing event: %w", err)
		}
	}
	if ts == nil {
		return nil
	}
	tasks, err := ts.ListAll(userID)
	if err != nil {
		return fmt.Errorf("listing tasks to remove: %w", err)
	}
	for _, t := range tasks {
		if t.CalendarId != calID {
			continue
		}
		if err := ts.Delete(userID, t.Guid); err != nil {
			return fmt.Errorf("removing task: %w", err)
		}
	}
	return nil
}
