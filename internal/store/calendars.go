package store

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// CalendarStore manages user calendars in Pebble via Raft.
type CalendarStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewCalendarStore creates a CalendarStore.
func NewCalendarStore(pebble *PebbleStore, applier RaftApplier) *CalendarStore {
	return &CalendarStore{pebble: pebble, applier: applier}
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

// Delete removes a calendar by GUID.
func (s *CalendarStore) Delete(userID, guid string) error {
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
	iter, err := s.pebble.PrefixScan(calendarPrefix(userID))
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

// SharedCalendar holds a calendar and its owner's userID.
type SharedCalendar struct {
	OwnerID  string
	Calendar *bongov1.Calendar
}

// ListSharedCalendars returns calendars shared with the given userID.
// It scans all users' calendar prefixes to find shared_with matches.
func (s *CalendarStore) ListSharedCalendars(userID string) ([]SharedCalendar, error) {
	// Scan all /user/*/calendar/* keys.
	iter, err := s.pebble.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scanning for shared calendars: %w", err)
	}
	defer iter.Close()

	var results []SharedCalendar
	for iter.Next() {
		key := string(iter.Key())

		// Only process calendar keys: /user/{ownerID}/calendar/{calID}
		if len(key) < 7 {
			continue
		}
		rest := key[6:] // after "/user/"
		slashIdx := 0
		for slashIdx < len(rest) && rest[slashIdx] != '/' {
			slashIdx++
		}
		if slashIdx == 0 || slashIdx >= len(rest)-1 {
			continue
		}
		ownerID := rest[:slashIdx]
		remainder := rest[slashIdx+1:]

		// Only look at calendar keys, skip events/contacts/etc.
		if len(remainder) < 9 || remainder[:9] != "calendar/" {
			continue
		}

		// Skip own calendars.
		if ownerID == userID {
			continue
		}

		val, err := iter.Value()
		if err != nil {
			continue
		}
		var cal bongov1.Calendar
		if err := proto.Unmarshal(val, &cal); err != nil {
			continue
		}

		for _, u := range cal.SharedWith {
			if u == userID {
				results = append(results, SharedCalendar{
					OwnerID:  ownerID,
					Calendar: &cal,
				})
				break
			}
		}
	}

	return results, nil
}
