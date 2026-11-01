package store

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// CalendarStore manages user calendars in Pebble via Raft.
type CalendarStore struct {
	pebble  *PebbleStore
	applier RaftApplier
	// scan is the prefix scan used by List. It defaults to
	// pebble.PrefixScan and exists so tests can record which prefixes are read.
	scan func(prefix []byte) (Iterator, error)
}

// NewCalendarStore creates a CalendarStore.
func NewCalendarStore(pebble *PebbleStore, applier RaftApplier) *CalendarStore {
	return &CalendarStore{pebble: pebble, applier: applier, scan: pebble.PrefixScan}
}

func calendarKey(userID, guid string) []byte {
	return []byte(fmt.Sprintf("/user/%s/calendar/%s", userID, guid))
}

func calendarPrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/calendar/", userID))
}

const defaultCalendarName = "Personal"
const defaultCalendarColor = "#4A90D9"

// EnsureDefault creates the default calendar if none exist for the user.
// Returns the default calendar's GUID.
func (s *CalendarStore) EnsureDefault(userID string) (string, error) {
	calendars, err := s.List(userID)
	if err != nil {
		return "", fmt.Errorf("listing calendars: %w", err)
	}
	if len(calendars) > 0 {
		return calendars[0].Guid, nil
	}

	cal := &bongov1.Calendar{
		Guid:  "default",
		Name:  defaultCalendarName,
		Color: defaultCalendarColor,
	}
	guid, err := s.Create(userID, cal)
	if err != nil {
		return "", fmt.Errorf("creating default calendar: %w", err)
	}
	return guid, nil
}

// Create creates a new calendar and returns its GUID.
func (s *CalendarStore) Create(userID string, cal *bongov1.Calendar) (string, error) {
	if cal.Guid == "" {
		cal.Guid = generateGUID()
	}
	now := time.Now().Unix()
	cal.CreatedAt = now
	cal.UpdatedAt = now

	data, err := proto.Marshal(cal)
	if err != nil {
		return "", fmt.Errorf("marshaling calendar: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   calendarKey(userID, cal.Guid),
		Value: data,
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return "", fmt.Errorf("applying calendar create: %w", err)
	}

	return cal.Guid, nil
}

// Get returns a single calendar by GUID.
func (s *CalendarStore) Get(userID, guid string) (*bongov1.Calendar, error) {
	data, err := s.pebble.Get(calendarKey(userID, guid))
	if err != nil {
		return nil, fmt.Errorf("reading calendar: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("calendar not found")
	}

	var cal bongov1.Calendar
	if err := proto.Unmarshal(data, &cal); err != nil {
		return nil, fmt.Errorf("unmarshaling calendar: %w", err)
	}
	return &cal, nil
}

// Update updates an existing calendar.
func (s *CalendarStore) Update(userID, guid string, cal *bongov1.Calendar) error {
	cal.Guid = guid
	cal.UpdatedAt = time.Now().Unix()

	data, err := proto.Marshal(cal)
	if err != nil {
		return fmt.Errorf("marshaling calendar: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   calendarKey(userID, guid),
		Value: data,
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying calendar update: %w", err)
	}

	return nil
}

// Delete removes a calendar by GUID and revokes every grant on it.
//
// These are two Raft commands, not one: the revoke goes first, then the
// calendar DELETE. A failure between them leaves a calendar with no grants,
// which loses access rather than leaking it, and a retry finishes the job. The
// reverse order could leave grants on a deleted calendar; HasCalendarAccess
// ignores those and the leader sweep removes them, but revoking first never
// relies on either.
func (s *CalendarStore) Delete(userID, guid string) error {
	revoke, err := BuildACLCommand(ACLRequest{Op: ACLOpRevokeResource, Kind: ACLKindCalendar, Owner: userID, Resource: guid})
	if err != nil {
		return fmt.Errorf("building calendar grant revoke: %w", err)
	}
	if err := s.applier.ApplyCommand(revoke, 5*time.Second); err != nil {
		return fmt.Errorf("revoking calendar grants: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: calendarKey(userID, guid),
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying calendar delete: %w", err)
	}

	return nil
}

// List returns all calendars for a user.
func (s *CalendarStore) List(userID string) ([]*bongov1.Calendar, error) {
	iter, err := s.scan(calendarPrefix(userID))
	if err != nil {
		return nil, fmt.Errorf("scanning calendars: %w", err)
	}
	defer iter.Close()

	var calendars []*bongov1.Calendar
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var cal bongov1.Calendar
		if err := proto.Unmarshal(val, &cal); err != nil {
			continue
		}
		calendars = append(calendars, &cal)
	}

	return calendars, nil
}
