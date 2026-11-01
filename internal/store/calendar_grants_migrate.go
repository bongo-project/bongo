package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// calendarGrantsMigrationKey marks the one-off shared_with migration complete.
var calendarGrantsMigrationKey = []byte("/system/migrations/calendar-grants-v1")

// CalendarGrantsMigrationDone reports whether the marker is present.
func CalendarGrantsMigrationDone(ps *PebbleStore) (bool, error) {
	_, err := ps.Get(calendarGrantsMigrationKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("calendar grants migration marker: %w", err)
	}
	return true, nil
}

// MarkCalendarGrantsMigrationDone records, via Raft, that the migration
// completed. It is the caller's last write.
func MarkCalendarGrantsMigrationDone(applier RaftApplier) error {
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: calendarGrantsMigrationKey, Value: []byte("done")}
	if err := applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("calendar grants migration marker: raft: %w", err)
	}
	return nil
}

// MigrateCalendarGrants turns every legacy Calendar.shared_with entry into a
// freebusy grant in state accepted (granted_by the owner), then clears the
// field. It returns the number of grants written.
//
// An entry that is empty, contains "/" or names the owner is dropped without a
// grant. A grant that already exists is never overwritten, so a level changed
// after an earlier partial run is not downgraded. A calendar is cleared only
// after all its grants were written, so a crash part-way is resumed by the
// next run, and a rerun is harmless whether or not the marker exists.
//
// Errors are collected and the run continues; the caller writes the marker
// only when the returned error is nil. The calendar is re-read just before the
// whole-record PUT that clears shared_with, so a calendar a user has since
// deleted is skipped and a rename made since the scan is kept. A small window
// between that read and the PUT remains: there is no compare-and-set, the same
// as MigrateFirstMatch and CalendarStore.Update.
//
// The grants travel in /__acl__/ commands, so every node must run this version
// before the leader migrates.
func MigrateCalendarGrants(ps *PebbleStore, applier RaftApplier, now time.Time) (int, error) {
	iter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return 0, fmt.Errorf("scanning calendars for grant migration: %w", err)
	}
	type pending struct {
		owner, guid string
		entries     []string
	}
	var todo []pending
	for iter.Next() {
		parts := strings.Split(string(iter.Key()), "/")
		// "", "user", {owner}, "calendar", {guid}
		if len(parts) != 5 || parts[3] != "calendar" {
			continue
		}
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		var cal bongov1.Calendar
		if proto.Unmarshal(val, &cal) != nil || len(cal.SharedWith) == 0 {
			continue
		}
		todo = append(todo, pending{owner: parts[2], guid: parts[4], entries: append([]string(nil), cal.SharedWith...)})
	}
	if err := iter.Close(); err != nil {
		return 0, fmt.Errorf("closing calendar scan: %w", err)
	}

	acl := NewACLStore(ps, applier)
	written := 0
	var errs []error
	for _, p := range todo {
		failed := false
		for _, entry := range p.entries {
			if entry == "" || strings.Contains(entry, "/") || entry == p.owner {
				continue
			}
			existing, gErr := acl.GetCalendarGrant(p.owner, p.guid, entry)
			if gErr != nil {
				errs = append(errs, fmt.Errorf("calendar %s/%s: %w", p.owner, p.guid, gErr))
				failed = true
				continue
			}
			if existing != nil {
				continue
			}
			if sErr := acl.SetCalendarGrant(p.owner, p.guid, entry, GrantLevelFreeBusy, GrantStateAccepted, p.owner, now); sErr != nil {
				errs = append(errs, fmt.Errorf("calendar %s/%s: %w", p.owner, p.guid, sErr))
				failed = true
				continue
			}
			written++
		}
		if failed {
			continue
		}

		cur, gErr := ps.Get(calendarKey(p.owner, p.guid))
		if errors.Is(gErr, ErrNotFound) {
			continue
		}
		if gErr != nil {
			errs = append(errs, fmt.Errorf("re-reading calendar %s/%s: %w", p.owner, p.guid, gErr))
			continue
		}
		var cal bongov1.Calendar
		if err := proto.Unmarshal(cur, &cal); err != nil {
			errs = append(errs, fmt.Errorf("parsing calendar %s/%s: %w", p.owner, p.guid, err))
			continue
		}
		if len(cal.SharedWith) == 0 {
			continue
		}
		cal.SharedWith = nil
		data, mErr := proto.Marshal(&cal)
		if mErr != nil {
			errs = append(errs, fmt.Errorf("marshaling calendar %s/%s: %w", p.owner, p.guid, mErr))
			continue
		}
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: calendarKey(p.owner, p.guid), Value: data}
		if aErr := applier.ApplyCommand(cmd, 5*time.Second); aErr != nil {
			errs = append(errs, fmt.Errorf("clearing shared_with on %s/%s: %w", p.owner, p.guid, aErr))
		}
	}
	return written, errors.Join(errs...)
}

// MigrationDone reports whether the calendar-grants marker is present.
func (s *ACLStore) MigrationDone() (bool, error) { return CalendarGrantsMigrationDone(s.pebble) }

// MarkMigrationDone writes the calendar-grants marker through the store's
// applier.
func (s *ACLStore) MarkMigrationDone() error { return MarkCalendarGrantsMigrationDone(s.applier) }

// MigrateShares runs MigrateCalendarGrants through the store's applier.
func (s *ACLStore) MigrateShares(now time.Time) (int, error) {
	return MigrateCalendarGrants(s.pebble, s.applier, now)
}
