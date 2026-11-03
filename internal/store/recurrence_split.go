package store

import (
	"fmt"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// InstancesBefore counts the instances of the rule of a recurring event that
// start before slot, the start of one of its instances. It counts the rule
// alone: EXDATE, overrides and cancellations do not change it, which is what
// RFC 5545 COUNT does too (an excluded instance still counts towards it). A
// caller that splits a series at slot uses it to tell the first instance
// (zero) and to carry a COUNT over to the part that follows.
func InstancesBefore(event *bongov1.Event, slot time.Time) (int, error) {
	if event.Rrule == "" {
		return 0, fmt.Errorf("event has no recurrence rule")
	}
	s, err := newRecurrenceSet(event)
	if err != nil {
		return 0, err
	}
	first := time.Unix(event.StartTime, 0).In(s.loc)
	if !slot.After(first) {
		return 0, nil
	}
	return len(s.rule.Between(first, slot.Add(-time.Second).In(s.loc), true)), nil
}

// VisibleInstancesBefore counts the instances that start before slot and are
// still shown: the rule's instances less those excluded by EXDATE (structured
// or in ical_raw) and those an override cancels. A moved override still
// counts. Zero means a series split at slot would keep nothing visible. Use
// InstancesBefore, not this, to carry a COUNT over.
func VisibleInstancesBefore(event *bongov1.Event, slot time.Time) (int, error) {
	if event.Rrule == "" {
		return 0, fmt.Errorf("event has no recurrence rule")
	}
	s, err := newRecurrenceSet(event)
	if err != nil {
		return 0, err
	}
	first := time.Unix(event.StartTime, 0).In(s.loc)
	if !slot.After(first) {
		return 0, nil
	}
	n := 0
	for _, occ := range s.rule.Between(first, slot.Add(-time.Second).In(s.loc), true) {
		if s.excluded(occ) {
			continue
		}
		if o, ok := s.overrides[s.key(occ)]; ok && o.cancelled {
			continue
		}
		n++
	}
	return n, nil
}

// NewEventGUID returns a fresh event identifier, for a caller that must know
// the GUID (it becomes the UID of the iCalendar object) before the event is
// created.
func NewEventGUID() string { return generateGUID() }
