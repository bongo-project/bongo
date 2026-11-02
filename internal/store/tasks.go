package store

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The iCalendar component kinds a calendar can hold.
const (
	ComponentEvent = "VEVENT"
	ComponentTodo  = "VTODO"
)

// CalendarComponents is the component set a calendar holds. An empty
// Calendar.components means VEVENT only, so a calendar written before the
// field existed keeps its meaning.
func CalendarComponents(cal *bongov1.Calendar) []string {
	if cal == nil || len(cal.Components) == 0 {
		return []string{ComponentEvent}
	}
	return cal.Components
}

// CalendarAllows reports whether cal accepts the iCalendar component kind
// comp (for example "VEVENT" or "VTODO").
func CalendarAllows(cal *bongov1.Calendar, comp string) bool {
	return slices.Contains(CalendarComponents(cal), comp)
}

// CalendarHoldsComponent reports whether the user's calendar calID accepts the
// component kind comp. A missing calendar is allowed, as for
// CalendarIsSubscribed, so the caller's own not-found handling stays in
// charge; a read error is returned so a write can be refused rather than
// guessed.
func (s *CalendarStore) CalendarHoldsComponent(userID, calID, comp string) (bool, error) {
	if calID == "" {
		return true, nil
	}
	data, err := s.pebble.Get(calendarKey(userID, calID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return true, nil
		}
		return false, fmt.Errorf("reading calendar: %w", err)
	}
	var cal bongov1.Calendar
	if err := proto.Unmarshal(data, &cal); err != nil {
		return false, fmt.Errorf("unmarshaling calendar: %w", err)
	}
	return CalendarAllows(&cal, comp), nil
}

// TaskStore manages user tasks (VTODO) in Pebble via Raft.
type TaskStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewTaskStore creates a TaskStore.
func NewTaskStore(pebble *PebbleStore, applier RaftApplier) *TaskStore {
	return &TaskStore{pebble: pebble, applier: applier}
}

// ErrTaskNotFound is returned by Get when no task has the guid.
var ErrTaskNotFound = errors.New("task not found")

func taskKey(userID, guid string) []byte {
	return []byte(fmt.Sprintf("/user/%s/task/%s", userID, guid))
}

func taskPrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/task/", userID))
}

// Create stores a new task, or overwrites the one with the same guid, and
// stamps both timestamps.
func (s *TaskStore) Create(userID string, task *bongov1.Task) (string, error) {
	if task.Guid == "" {
		task.Guid = generateGUID()
	}
	now := time.Now().Unix()
	task.CreatedAt = now
	task.UpdatedAt = now
	if err := s.put(userID, task); err != nil {
		return "", fmt.Errorf("applying task create: %w", err)
	}
	return task.Guid, nil
}

// Update replaces an existing task and stamps updated_at. The caller carries
// created_at over.
func (s *TaskStore) Update(userID, guid string, task *bongov1.Task) error {
	task.Guid = guid
	task.UpdatedAt = time.Now().Unix()
	if err := s.put(userID, task); err != nil {
		return fmt.Errorf("applying task update: %w", err)
	}
	return nil
}

func (s *TaskStore) put(userID string, task *bongov1.Task) error {
	data, err := proto.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshaling task: %w", err)
	}
	return s.applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   taskKey(userID, task.Guid),
		Value: data,
	}, 5*time.Second)
}

// Get returns a single task by GUID.
func (s *TaskStore) Get(userID, guid string) (*bongov1.Task, error) {
	data, err := s.pebble.Get(taskKey(userID, guid))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading task: %w", err)
	}
	if len(data) == 0 {
		return nil, ErrTaskNotFound
	}
	var task bongov1.Task
	if err := proto.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("unmarshaling task: %w", err)
	}
	return &task, nil
}

// Delete removes a task by GUID.
func (s *TaskStore) Delete(userID, guid string) error {
	err := s.applier.ApplyCommand(&bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: taskKey(userID, guid),
	}, 5*time.Second)
	if err != nil {
		return fmt.Errorf("applying task delete: %w", err)
	}
	return nil
}

// ListAll returns every task of a user, in key order.
func (s *TaskStore) ListAll(userID string) ([]*bongov1.Task, error) {
	iter, err := s.pebble.PrefixScan(taskPrefix(userID))
	if err != nil {
		return nil, fmt.Errorf("scanning tasks: %w", err)
	}
	defer iter.Close()

	var tasks []*bongov1.Task
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var task bongov1.Task
		if err := proto.Unmarshal(val, &task); err != nil {
			continue
		}
		tasks = append(tasks, &task)
	}
	return tasks, nil
}

// NewTaskGUID returns a fresh task GUID. A caller that serialises the task
// (UID = guid) before storing it needs the GUID first.
func NewTaskGUID() string { return generateGUID() }
