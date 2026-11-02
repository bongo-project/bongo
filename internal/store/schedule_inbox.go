package store

import (
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Schedule inbox (RFC 6638): the iTIP messages a CalDAV client finds in its
// scheduling inbox collection.
//
//	/user/{uid}/schedinbox/{20-digit unix nanos}-{id}
//
// The value is the sanitised iCalendar object (METHOD kept, alarms removed),
// not a protobuf. The zero-padded time puts a prefix scan in arrival order,
// oldest first. The id is chosen by the caller from the delivery that
// produced the item, so a delivery that is retried after a crash places its
// item once. Every scanner of the /user/ tree filters by the type segment, so
// a non-protobuf value under "schedinbox" is never decoded.
//
// Writes are single PUT and DELETE commands: an older node applies them
// correctly, and no composite command is involved.

// MaxScheduleInboxItems is the most items one user keeps; placing one more
// drops the oldest.
const MaxScheduleInboxItems = 500

// ErrScheduleItemNotFound means no inbox item has the requested name.
var ErrScheduleItemNotFound = errors.New("schedule inbox item not found")

// scheduleItemName is the shape of an item name: the padded nanoseconds, a
// hyphen and a lower-case hex id. Names reach keys, so nothing else is let in.
var scheduleItemName = regexp.MustCompile(`^[0-9]{20}-[0-9a-f]{8,64}$`)

// ValidScheduleItemName reports whether name can be an inbox item name.
func ValidScheduleItemName(name string) bool { return scheduleItemName.MatchString(name) }

// ValidScheduleItemID reports whether id can be the id part of an item.
func ValidScheduleItemID(id string) bool {
	return scheduleItemName.MatchString("00000000000000000000-" + id)
}

// ScheduleItem is one inbox item.
type ScheduleItem struct {
	// Name is the key suffix, and the resource name without ".ics".
	Name string
	// Received is the time the item was placed.
	Received time.Time
	// Data is the iCalendar object.
	Data []byte
}

// ScheduleInboxStore keeps the schedule inboxes.
type ScheduleInboxStore struct {
	pebble  *PebbleStore
	applier RaftApplier
	now     func() time.Time
	// locks serialise place-and-trim per user on this node. The cap is soft
	// across nodes: two nodes placing at once may leave a few items over it
	// until the next placement trims them.
	locks [64]sync.Mutex
}

// NewScheduleInboxStore creates a ScheduleInboxStore.
func NewScheduleInboxStore(pebble *PebbleStore, applier RaftApplier) *ScheduleInboxStore {
	return &ScheduleInboxStore{pebble: pebble, applier: applier, now: time.Now}
}

func scheduleInboxPrefix(userID string) []byte {
	return []byte("/user/" + userID + "/schedinbox/")
}

func scheduleInboxKey(userID, name string) []byte {
	return append(scheduleInboxPrefix(userID), name...)
}

func (s *ScheduleInboxStore) lockFor(userID string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(userID))
	return &s.locks[h.Sum32()%uint32(len(s.locks))]
}

// Place adds an item to the user's inbox unless an item with this id is
// already there, then drops the oldest items beyond MaxScheduleInboxItems.
// A failed trim is reported after the item itself was stored.
func (s *ScheduleInboxStore) Place(userID, id string, data []byte) error {
	if !ValidScheduleItemID(id) {
		return errors.New("schedule inbox: invalid item id")
	}
	mu := s.lockFor(userID)
	mu.Lock()
	defer mu.Unlock()

	items, err := s.list(userID, false)
	if err != nil {
		return err
	}
	for _, it := range items {
		if strings.HasSuffix(it.Name, "-"+id) {
			return nil
		}
	}
	name := fmt.Sprintf("%020d-%s", s.now().UnixNano(), id)
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: scheduleInboxKey(userID, name), Value: data}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying schedule inbox put: %w", err)
	}
	items = append(items, ScheduleItem{Name: name})
	var trimErr error
	for len(items) > MaxScheduleInboxItems {
		if err := s.remove(userID, items[0].Name); err != nil {
			trimErr = err
			break
		}
		items = items[1:]
	}
	return trimErr
}

// List returns the user's items, oldest first.
func (s *ScheduleInboxStore) List(userID string) ([]ScheduleItem, error) {
	return s.list(userID, true)
}

func (s *ScheduleInboxStore) list(userID string, withData bool) ([]ScheduleItem, error) {
	prefix := scheduleInboxPrefix(userID)
	iter, err := s.pebble.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("listing schedule inbox: %w", err)
	}
	defer iter.Close()
	var out []ScheduleItem
	for iter.Next() {
		name := string(iter.Key()[len(prefix):])
		if !ValidScheduleItemName(name) {
			continue
		}
		it := ScheduleItem{Name: name, Received: scheduleItemTime(name)}
		if withData {
			v, err := iter.Value()
			if err != nil {
				return nil, fmt.Errorf("reading schedule inbox item: %w", err)
			}
			it.Data = append([]byte(nil), v...)
		}
		out = append(out, it)
	}
	return out, nil
}

// Get returns one item.
func (s *ScheduleInboxStore) Get(userID, name string) (*ScheduleItem, error) {
	if !ValidScheduleItemName(name) {
		return nil, ErrScheduleItemNotFound
	}
	v, err := s.pebble.Get(scheduleInboxKey(userID, name))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrScheduleItemNotFound
		}
		return nil, fmt.Errorf("reading schedule inbox item: %w", err)
	}
	return &ScheduleItem{Name: name, Received: scheduleItemTime(name), Data: v}, nil
}

// Delete removes one item. Removing a missing item is not an error.
func (s *ScheduleInboxStore) Delete(userID, name string) error {
	if !ValidScheduleItemName(name) {
		return ErrScheduleItemNotFound
	}
	return s.remove(userID, name)
}

func (s *ScheduleInboxStore) remove(userID, name string) error {
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: scheduleInboxKey(userID, name)}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying schedule inbox delete: %w", err)
	}
	return nil
}

func scheduleItemTime(name string) time.Time {
	n, err := strconv.ParseInt(name[:20], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, n)
}
