package store

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// calendarSharePruneSource is the part of PebbleStore PruneCalendarShares
// reads. Declared here so a test can make a profile read fail.
type calendarSharePruneSource interface {
	Get(key []byte) ([]byte, error)
	PrefixScan(prefix []byte) (Iterator, error)
}

// CalendarSharePrune reports the entries removed from one calendar.
type CalendarSharePrune struct {
	UserID     string
	CalendarID string
	Removed    int
}

// PruneCalendarShares removes every Calendar.SharedWith entry that is not the
// ID of an existing user profile. Shares are stored as user IDs, so older
// entries holding an address or other raw string never matched the grant lookup
// (and calendar-grants-v1 does not carry them over), so they only filled the
// per-calendar cap; deleting them changes nobody's access.
// Entries naming a deleted user go too.
//
// An entry counts as absent only when its profile read returns ErrNotFound,
// or when it is empty or contains "/" (so it cannot steer the key). Any other
// read error keeps the entry and is returned, so a transient Pebble failure
// cannot wipe real shares. A disabled user still has a profile and is kept.
//
// Each changed calendar is rewritten with one full-value PUT through Raft. The
// caller must run this before anything else can write calendars (startup,
// before the listeners open), because the PUT replaces the whole record and a
// concurrent share would be lost. It is idempotent: a second run finds
// nothing to remove and applies no commands. A calendar that cannot be read
// or written is skipped and reported in the returned error, with the rest
// still processed.
func PruneCalendarShares(ps calendarSharePruneSource, applier RaftApplier) ([]CalendarSharePrune, error) {
	type change struct {
		userID, guid string
		cal          *bongov1.Calendar
		removed      int
	}

	var (
		changes []change
		errs    []error
		exists  = map[string]bool{}
	)
	// live reports whether entry is the ID of an existing profile. ok is
	// false when the answer is unknown and the entry must be kept.
	live := func(entry string) (present, ok bool) {
		if entry == "" || strings.Contains(entry, "/") {
			return false, true
		}
		if v, cached := exists[entry]; cached {
			return v, true
		}
		_, err := ps.Get([]byte("/user/" + entry + "/profile"))
		switch {
		case err == nil:
			exists[entry] = true
			return true, true
		case errors.Is(err, ErrNotFound):
			exists[entry] = false
			return false, true
		default:
			errs = append(errs, fmt.Errorf("reading profile for share entry: %w", err))
			return true, false
		}
	}

	iter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scanning calendars: %w", err)
	}
	for iter.Next() {
		// Exactly /user/{uid}/calendar/{guid}: not events, mailboxes or
		// anything else under the user prefix.
		parts := strings.Split(string(iter.Key()), "/")
		if len(parts) != 5 || parts[3] != "calendar" {
			continue
		}
		val, err := iter.Value()
		if err != nil {
			errs = append(errs, fmt.Errorf("reading calendar %s: %w", parts[4], err))
			continue
		}
		var cal bongov1.Calendar
		if err := proto.Unmarshal(val, &cal); err != nil {
			errs = append(errs, fmt.Errorf("unmarshaling calendar %s: %w", parts[4], err))
			continue
		}
		if len(cal.SharedWith) == 0 {
			continue
		}
		before := len(cal.SharedWith)
		cal.SharedWith = slices.DeleteFunc(cal.SharedWith, func(entry string) bool {
			present, ok := live(entry)
			return ok && !present
		})
		if removed := before - len(cal.SharedWith); removed > 0 {
			changes = append(changes, change{userID: parts[2], guid: parts[4], cal: &cal, removed: removed})
		}
	}
	if err := iter.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing calendar scan: %w", err))
	}

	var done []CalendarSharePrune
	for _, c := range changes {
		c.cal.UpdatedAt = time.Now().Unix()
		data, err := proto.Marshal(c.cal)
		if err != nil {
			errs = append(errs, fmt.Errorf("marshaling calendar %s: %w", c.guid, err))
			continue
		}
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: calendarKey(c.userID, c.guid), Value: data}
		if err := applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			errs = append(errs, fmt.Errorf("applying calendar %s share prune: %w", c.guid, err))
			continue
		}
		done = append(done, CalendarSharePrune{UserID: c.userID, CalendarID: c.guid, Removed: c.removed})
	}
	return done, errors.Join(errs...)
}
