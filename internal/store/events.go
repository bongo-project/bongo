package store

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/teambition/rrule-go"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// EventStore manages user calendar events in Pebble via Raft.
type EventStore struct {
	pebble  *PebbleStore
	applier RaftApplier
	search  *SearchManager
	logger  *slog.Logger
}

// NewEventStore creates an EventStore.
func NewEventStore(pebble *PebbleStore, applier RaftApplier) *EventStore {
	return &EventStore{pebble: pebble, applier: applier}
}

// SetLogger sets the logger. Optional: a nil logger is tolerated everywhere,
// following the setter-injection pattern used for the other optional
// dependencies here.
func (s *EventStore) SetLogger(logger *slog.Logger) {
	s.logger = logger
}

// warn logs at warning level when a logger has been injected.
func (s *EventStore) warn(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(msg, args...)
	}
}

// SetSearch sets the search manager for indexing events.
func (s *EventStore) SetSearch(search *SearchManager) {
	s.search = search
}

func eventKey(userID, guid string) []byte {
	return []byte(fmt.Sprintf("/user/%s/event/%s", userID, guid))
}

func eventPrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/event/", userID))
}

// Create creates a new event and returns its GUID.
func (s *EventStore) Create(userID string, event *bongov1.Event) (string, error) {
	if event.Guid == "" {
		event.Guid = generateGUID()
	}
	now := time.Now().Unix()
	event.CreatedAt = now
	event.UpdatedAt = now

	data, err := proto.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("marshaling event: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   eventKey(userID, event.Guid),
		Value: data,
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return "", fmt.Errorf("applying event create: %w", err)
	}

	if s.search != nil {
		_ = s.search.IndexEvent(userID, event.Guid, event.Title, event.Description, event.Location, event.StartTime, event.EndTime)
	}

	return event.Guid, nil
}

// Get returns a single event by GUID.
func (s *EventStore) Get(userID, guid string) (*bongov1.Event, error) {
	data, err := s.pebble.Get(eventKey(userID, guid))
	if err != nil {
		return nil, fmt.Errorf("reading event: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("event not found")
	}

	var event bongov1.Event
	if err := proto.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("unmarshaling event: %w", err)
	}
	return &event, nil
}

// Update updates an existing event.
func (s *EventStore) Update(userID, guid string, event *bongov1.Event) error {
	event.Guid = guid
	event.UpdatedAt = time.Now().Unix()

	data, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   eventKey(userID, guid),
		Value: data,
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying event update: %w", err)
	}

	if s.search != nil {
		_ = s.search.IndexEvent(userID, event.Guid, event.Title, event.Description, event.Location, event.StartTime, event.EndTime)
	}

	return nil
}

// Delete removes an event by GUID.
func (s *EventStore) Delete(userID, guid string) error {
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: eventKey(userID, guid),
	}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying event delete: %w", err)
	}

	if s.search != nil {
		_ = s.search.DeleteEventIndex(userID, guid)
	}

	return nil
}

// ExpandRecurrence generates occurrence instances of a recurring event
// within the given time range. Each occurrence gets a unique GUID suffix
// based on its date.
func ExpandRecurrence(event *bongov1.Event, rangeStart, rangeEnd int64) ([]*bongov1.Event, error) {
	if event.Rrule == "" {
		return []*bongov1.Event{event}, nil
	}

	r, err := rrule.StrToROption(event.Rrule)
	if err != nil {
		return nil, fmt.Errorf("parsing rrule %q: %w", event.Rrule, err)
	}

	eventStart := time.Unix(event.StartTime, 0).UTC()
	r.Dtstart = eventStart

	rule, err := rrule.NewRRule(*r)
	if err != nil {
		return nil, fmt.Errorf("creating rrule: %w", err)
	}

	duration := event.EndTime - event.StartTime

	// Build exdate set for fast lookup.
	exdateSet := make(map[string]bool, len(event.Exdates))
	for _, ex := range event.Exdates {
		exdateSet[ex] = true
	}

	start := time.Unix(rangeStart, 0).UTC()
	end := time.Unix(rangeEnd, 0).UTC()
	occurrences := rule.Between(start.Add(-time.Duration(duration)*time.Second), end, true)

	var events []*bongov1.Event
	for _, occ := range occurrences {
		occStart := occ.Unix()
		occEnd := occStart + duration

		// Skip if outside range.
		if occStart >= rangeEnd || occEnd <= rangeStart {
			continue
		}

		// Skip exception dates.
		dateKey := occ.Format("2006-01-02")
		if exdateSet[dateKey] {
			continue
		}

		instance := &bongov1.Event{
			Guid:            fmt.Sprintf("%s_%s", event.Guid, dateKey),
			CalendarId:      event.CalendarId,
			Title:           event.Title,
			Description:     event.Description,
			Location:        event.Location,
			StartTime:       occStart,
			EndTime:         occEnd,
			AllDay:          event.AllDay,
			Rrule:           event.Rrule,
			Timezone:        event.Timezone,
			ReminderMinutes: event.ReminderMinutes,
			CreatedAt:       event.CreatedAt,
			UpdatedAt:       event.UpdatedAt,
		}
		events = append(events, instance)
	}

	return events, nil
}

// ListByDateRange returns events for a user within a time range,
// optionally filtered by calendar ID. Recurring events are expanded.
func (s *EventStore) ListByDateRange(userID string, start, end int64, calendarID string) ([]*bongov1.Event, error) {
	iter, err := s.pebble.PrefixScan(eventPrefix(userID))
	if err != nil {
		return nil, fmt.Errorf("scanning events: %w", err)
	}
	defer iter.Close()

	var events []*bongov1.Event
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var event bongov1.Event
		if err := proto.Unmarshal(val, &event); err != nil {
			continue
		}

		// Filter by calendar if specified.
		if calendarID != "" && event.CalendarId != calendarID {
			continue
		}

		// Expand recurring events.
		//
		// A recurrence that will not parse falls back to the base occurrence
		// rather than being skipped. Skipping meant an event with an RRULE this
		// parser rejects vanished from every view -- agenda, month, CalDAV --
		// with no error to the user and no log line, so it read as data loss.
		// Showing the single base occurrence is wrong about the repeats but
		// keeps the event, its time and its title visible, which is the far
		// better failure.
		if event.Rrule != "" {
			expanded, err := ExpandRecurrence(&event, start, end)
			if err == nil {
				events = append(events, expanded...)
				continue
			}
			s.warn("calendar: recurrence rule did not parse, showing base occurrence only",
				"event", event.Guid, "rrule", event.Rrule, "error", err)
		}

		// Check time range overlap: event overlaps range if event.start < range.end AND event.end > range.start.
		if event.StartTime < end && event.EndTime > start {
			events = append(events, &event)
		}
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].StartTime < events[j].StartTime
	})

	return events, nil
}

// ListAll returns events for a user with pagination.
func (s *EventStore) ListAll(userID string, offset, limit int) ([]*bongov1.Event, int, error) {
	iter, err := s.pebble.PrefixScan(eventPrefix(userID))
	if err != nil {
		return nil, 0, fmt.Errorf("scanning events: %w", err)
	}
	defer iter.Close()

	var events []*bongov1.Event
	total := 0
	for iter.Next() {
		total++
		if total <= offset || (limit > 0 && len(events) >= limit) {
			continue
		}
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var event bongov1.Event
		if err := proto.Unmarshal(val, &event); err != nil {
			continue
		}
		events = append(events, &event)
	}

	return events, total, nil
}
