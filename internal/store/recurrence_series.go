package store

import (
	"sort"
	"time"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Series is a recurring event opened to look at its exceptions, so a caller
// that compares many occurrences reads the stored object once.
type Series struct{ set *recurrenceSet }

// OpenSeries opens a recurring event. It reports false for an event with no
// rule or one whose rule does not parse.
func OpenSeries(event *bongov1.Event) (*Series, bool) {
	if event == nil || event.Rrule == "" {
		return nil, false
	}
	s, err := newRecurrenceSet(event)
	if err != nil {
		return nil, false
	}
	return &Series{set: s}, true
}

// Exceptions lists the RECURRENCE-IDs of the series' exceptions, sorted:
// cancelled are the occurrences an EXDATE removes (a date-only EXDATE on a
// timed series resolves to the occurrence of that day) and those an override
// with STATUS:CANCELLED removes; overridden are those a live override
// replaces. EXDATE and RECURRENCE-ID values are matched by instant, whatever
// their TZID, trailing Z or VALUE=DATE. An EXDATE that names no occurrence of
// the rule is left out.
func (sr *Series) Exceptions() (cancelled, overridden []string) {
	s := sr.set
	seen := map[string]bool{}
	addCancelled := func(key int64) {
		t := time.Unix(key, 0).In(s.loc)
		if len(s.rule.Between(t, t, true)) == 0 {
			return
		}
		if rid := s.recurrenceID(key); !seen[rid] {
			seen[rid] = true
			cancelled = append(cancelled, rid)
		}
	}
	for key := range s.exInstants {
		addCancelled(key)
	}
	for day := range s.exDays {
		d, err := time.ParseInLocation("20060102", day, s.loc)
		if err != nil {
			continue
		}
		for _, occ := range s.rule.Between(d, d.AddDate(0, 0, 1), false) {
			if occ.In(s.loc).Format("20060102") == day {
				addCancelled(s.key(occ))
			}
		}
	}
	for key, o := range s.overrides {
		rid := s.recurrenceID(key)
		if seen[rid] {
			continue
		}
		seen[rid] = true
		if o.cancelled {
			cancelled = append(cancelled, rid)
		} else {
			overridden = append(overridden, rid)
		}
	}
	sort.Strings(cancelled)
	sort.Strings(overridden)
	return cancelled, overridden
}

// Occurrence returns the occurrence named by rid twice: plain as the rule
// alone makes it (nil when rid is not a slot of the rule), and effective as the
// series shows it, with its override applied (nil when it is excluded or
// cancelled, or not an occurrence at all). Unlike FindOccurrence, the
// effective occurrence of an override that lists ATTENDEEs has that list in
// place of the master's (RFC 5545) and carries the larger of the series' and
// the override's SEQUENCE. FindOccurrence is left as it is: whose REPLY is
// accepted and what views show are decided from the master's list.
func (sr *Series) Occurrence(rid string) (plain, effective *bongov1.Event) {
	s := sr.set
	t, ok := s.parseRecurrenceID(rid)
	if !ok {
		return nil, nil
	}
	key := s.key(t)
	at := t.In(s.loc)
	onRule := len(s.rule.Between(at, at, true)) > 0
	if onRule {
		plain = s.instance(key, t.Unix(), t.Unix()+s.duration)
	}
	if o, ok := s.overrides[key]; ok {
		if o.cancelled {
			return plain, nil
		}
		effective = s.overrideInstance(key, o)
		if len(o.attendees) > 0 {
			effective.Attendees = o.attendees
		}
		effective.Sequence = max(effective.Sequence, icalevent.ClampSequence(o.sequence))
		return plain, effective
	}
	if onRule && !s.excluded(at) {
		effective = s.instance(key, t.Unix(), t.Unix()+s.duration)
	}
	return plain, effective
}
