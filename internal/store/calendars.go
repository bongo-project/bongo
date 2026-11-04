package store

import (
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
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
	// get is the key read used by Exists. It is nil in production (pebble.Get
	// is used); tests set it to inject read failures, since a closed
	// PebbleStore panics.
	get func(key []byte) ([]byte, error)
	// locks serialises Mutate per calendar (striped by key hash).
	locks [calendarLockStripes]sync.Mutex
	// userLocks serialises CreateNew per user so the calendar count check and
	// the write are one step. It is a separate array from locks: a user stripe
	// and a GUID stripe could collide and sync.Mutex is not re-entrant. Lock
	// order is user, then GUID.
	userLocks [calendarLockStripes]sync.Mutex
}

// MaxCalendarsPerUser is the number of calendar records one user may own
// (Personal, Tasks, created and subscribed). Calendars shared with the user are
// grants, not records under their key space, and do not count.
const MaxCalendarsPerUser = 5

// ErrCalendarLimit is returned by CreateNew when the user already owns
// MaxCalendarsPerUser calendars.
var ErrCalendarLimit = errors.New("calendar limit reached")

// NewCalendarGUID returns a fresh calendar GUID.
func NewCalendarGUID() string { return generateGUID() }

const calendarLockStripes = 64

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

// TasksCalendarGUID is the fixed ID of the VTODO calendar every user gets. A
// fixed ID makes two concurrent first calls write the same key, so there are
// never two. It sorts after "default" and after every generated hex ID.
const TasksCalendarGUID = "tasks"

// TasksCalendarName is the display name of the Tasks calendar.
const TasksCalendarName = "Tasks"

// NewTasksCalendar returns the VTODO-only Tasks calendar.
func NewTasksCalendar() *bongov1.Calendar {
	return &bongov1.Calendar{
		Guid:       TasksCalendarGUID,
		Name:       TasksCalendarName,
		Color:      defaultCalendarColor,
		Components: []string{ComponentTodo},
	}
}

// DefaultEventCalendar returns the GUID of the first calendar in cals that
// holds events and is not a read-only subscription, or "" when there is none.
// The cards and forms that preselect a calendar use it so they agree with
// EnsureDefault.
func DefaultEventCalendar(cals []*bongov1.Calendar) string {
	for _, c := range cals {
		if CalendarAllows(c, ComponentEvent) && !IsSubscribed(c) {
			return c.Guid
		}
	}
	return ""
}

// HasTaskCalendar reports whether cals holds a calendar that takes tasks and
// is not a read-only subscription.
func HasTaskCalendar(cals []*bongov1.Calendar) bool {
	for _, c := range cals {
		if CalendarAllows(c, ComponentTodo) && !IsSubscribed(c) {
			return true
		}
	}
	return false
}

func hasCalendarGUID(cals []*bongov1.Calendar, guid string) bool {
	for _, c := range cals {
		if c.Guid == guid {
			return true
		}
	}
	return false
}

// EnsureDefault returns the GUID of the user's default event calendar: the
// first calendar that holds events and is not a subscription. A user whose
// calendars all hold tasks only gets a Personal calendar created. Every call
// also back-fills the Tasks calendar for a user with no calendar that takes
// tasks (new and existing accounts alike). That write is exempt from the
// calendar cap, and a calendar already at the Tasks GUID is never replaced.
// A tasks-only calendar is never the default, or web and iMIP events would be
// filed into a VTODO collection.
func (s *CalendarStore) EnsureDefault(userID string) (string, error) {
	calendars, err := s.List(userID)
	if err != nil {
		return "", fmt.Errorf("listing calendars: %w", err)
	}
	guid := DefaultEventCalendar(calendars)
	// Tasks first: a failure between the two writes leaves a user with Tasks
	// only, and the next call then creates Personal. The check precedes the
	// early return so a user with only Personal gets Tasks too.
	if !HasTaskCalendar(calendars) && !hasCalendarGUID(calendars, TasksCalendarGUID) {
		if _, err := s.Create(userID, NewTasksCalendar()); err != nil {
			if guid == "" {
				return "", fmt.Errorf("creating tasks calendar: %w", err)
			}
			// Best effort for a user who already has a default: this runs on
			// read paths, which must keep working on a node that cannot write
			// (a follower, or no leader). The next call tries again.
			slog.Warn("calendars: back-filling tasks calendar failed", "user", userID, "error", err)
		}
	}
	if guid != "" {
		return guid, nil
	}

	cal := &bongov1.Calendar{
		Guid:  "default",
		Name:  defaultCalendarName,
		Color: defaultCalendarColor,
	}
	// A "default" that exists but cannot be the default (tasks-only or a
	// subscription) must not be overwritten by the blind PUT in Create.
	if hasCalendarGUID(calendars, cal.Guid) {
		cal.Guid = ""
	}
	guid, err = s.Create(userID, cal)
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

// ErrExists is returned by CreateNew when the calendar is already there.
var ErrExists = errors.New("calendar already exists")

// CreateNew creates cal under cal.Guid only if no calendar holds that GUID,
// returning ErrExists otherwise, and ErrCalendarLimit when the user already
// owns MaxCalendarsPerUser calendars (existence is checked first, so an
// existing path still answers ErrExists at the cap). EnsureDefault and the
// Tasks fallback use Create and are exempt. Create is a blind PUT and would overwrite an
// existing calendar with its publish token and subscription.
//
// It holds the same per-calendar lock as Mutate, so on this node two
// creations, or a creation racing a Mutate, cannot interleave between the
// existence check and the write. The lock is per node; cluster-wide
// serialisation is not provided (production runs one node), so the cap is
// per node as well.
func (s *CalendarStore) CreateNew(userID string, cal *bongov1.Calendar) error {
	if cal.Guid == "" {
		return fmt.Errorf("creating calendar: no GUID")
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID))
	ul := &s.userLocks[h.Sum32()%calendarLockStripes]
	ul.Lock()
	defer ul.Unlock()
	mu := s.lockFor(userID, cal.Guid)
	mu.Lock()
	defer mu.Unlock()

	data, err := s.pebble.Get(calendarKey(userID, cal.Guid))
	switch {
	case err == nil && len(data) > 0:
		return ErrExists
	case err != nil && !errors.Is(err, ErrNotFound):
		return fmt.Errorf("reading calendar: %w", err)
	}
	owned, err := s.List(userID)
	if err != nil {
		return fmt.Errorf("counting calendars: %w", err)
	}
	if len(owned) >= MaxCalendarsPerUser {
		return ErrCalendarLimit
	}
	_, err = s.Create(userID, cal)
	return err
}

func (s *CalendarStore) lockFor(userID, guid string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID + "\x00" + guid))
	return &s.locks[h.Sum32()%calendarLockStripes]
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

// Exists reports whether the user has a calendar with this GUID. Only a
// missing key is "no"; any other read error is returned so a caller can fail
// the request rather than treat a storage fault as a missing calendar.
func (s *CalendarStore) Exists(userID, guid string) (bool, error) {
	get := s.get
	if get == nil {
		get = s.pebble.Get
	}
	data, err := get(calendarKey(userID, guid))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("reading calendar: %w", err)
	}
	return len(data) > 0, nil
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

// Mutate reads a calendar, applies fn and writes the result back, holding a
// process-local lock keyed on the calendar so two read-modify-write cycles on
// this node cannot interleave. Without it a rename racing a token
// regeneration could write the old token back and revive a revoked link.
//
// The lock is per node. A write from another node (or a follower forwarding
// through the leader) is not serialised against this one: cluster-wide
// serialisation is not provided. If fn returns an error nothing is written.
func (s *CalendarStore) Mutate(userID, guid string, fn func(*bongov1.Calendar) error) error {
	mu := s.lockFor(userID, guid)
	mu.Lock()
	defer mu.Unlock()

	cal, err := s.Get(userID, guid)
	if err != nil {
		return err
	}
	if err := fn(cal); err != nil {
		return err
	}
	return s.Update(userID, guid, cal)
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
