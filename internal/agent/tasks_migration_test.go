package agent

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// logBuf is a goroutine-safe buffer for the logs a test inspects.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

const (
	tmEventsICS = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VEVENT\r\nUID:e\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301105T090000Z\r\nSUMMARY:Real event\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	tmTodoICS   = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VTODO\r\nUID:t\r\nDTSTAMP:20301101T000000Z\r\nSUMMARY:Secret todo title\r\nDESCRIPTION:Secret todo notes\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
)

type tmEnv struct {
	a    *Agent
	ps   *store.PebbleStore
	app  *recordingApplier
	logs *logBuf
}

func newTasksMigrationEnv(t *testing.T) *tmEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	app := &recordingApplier{ps: ps}
	logs := &logBuf{}
	a := &Agent{
		Store:            ps,
		Calendars:        store.NewCalendarStore(ps, app),
		migrationApplier: app,
		logger:           slog.New(slog.NewTextHandler(logs, nil)),
	}
	return &tmEnv{a: a, ps: ps, app: app, logs: logs}
}

func (e *tmEnv) profile(t *testing.T, user string) {
	t.Helper()
	data, _ := proto.Marshal(&bongov1.UserProfile{Email: user + "@d.test"})
	if err := e.ps.Set([]byte("/user/"+user+"/profile"), data); err != nil {
		t.Fatal(err)
	}
}

func (e *tmEnv) event(t *testing.T, user, guid, cal, ics string) {
	t.Helper()
	data, _ := proto.Marshal(&bongov1.Event{Guid: guid, CalendarId: cal, IcalRaw: []byte(ics)})
	if err := e.ps.Set([]byte("/user/"+user+"/event/"+guid), data); err != nil {
		t.Fatal(err)
	}
}

func (e *tmEnv) calendar(t *testing.T, user string, cal *bongov1.Calendar) {
	t.Helper()
	data, _ := proto.Marshal(cal)
	if err := e.ps.Set([]byte("/user/"+user+"/calendar/"+cal.Guid), data); err != nil {
		t.Fatal(err)
	}
}

func (e *tmEnv) hasKey(key string) bool {
	_, err := e.ps.Get([]byte(key))
	return err == nil
}

func TestMigrateTasksPass_Follower(t *testing.T) {
	e := newTasksMigrationEnv(t)
	e.profile(t, "u1")
	e.calendar(t, "u1", &bongov1.Calendar{Guid: "default", Name: "Personal"})
	if e.a.migrateTasksPass(always(false)) {
		t.Error("follower reported done")
	}
	if n := e.app.calls.Load(); n != 0 {
		t.Errorf("follower applied %d commands", n)
	}
}

// Existing users are not given a Tasks calendar; only the marker is written.
func TestMigrateTasksPass_DoesNotAddTasksCalendar(t *testing.T) {
	e := newTasksMigrationEnv(t)
	e.profile(t, "has-cals")
	e.calendar(t, "has-cals", &bongov1.Calendar{Guid: "default", Name: "Personal"})
	if !e.a.migrateTasksPass(always(true)) {
		t.Fatal("pass did not report done")
	}
	if e.hasKey("/user/has-cals/calendar/tasks") {
		t.Error("an existing user was given a Tasks calendar")
	}
	if !e.hasKey(string(blankEventsMarkerKey)) {
		t.Error("marker not written")
	}
	if e.hasKey("/system/migrations/tasks-calendar-v1") {
		t.Error("the retired tasks-calendar marker was written")
	}
}

func TestMigrateTasksPass_FailureLeavesMarkerUnset(t *testing.T) {
	e := newTasksMigrationEnv(t)
	e.profile(t, "u1")
	e.calendar(t, "u1", &bongov1.Calendar{Guid: "default", Name: "Personal"})
	e.app.failOn = 1 // the blank-events marker write is the first command
	if e.a.migrateTasksPass(always(true)) {
		t.Fatal("reported done after a failed write")
	}
	if e.hasKey(string(blankEventsMarkerKey)) {
		t.Error("marker written despite a failed write")
	}
	e.app.failOn = 0
	if !e.a.migrateTasksPass(always(true)) {
		t.Error("retry did not complete")
	}
	if !e.hasKey(string(blankEventsMarkerKey)) {
		t.Error("retry did not write the marker")
	}
}

func TestMigrateTasksPass_ListsBlankEventsWithoutContentOrDeleting(t *testing.T) {
	e := newTasksMigrationEnv(t)
	e.profile(t, "u1")
	e.event(t, "u1", "good", "default", tmEventsICS)
	e.event(t, "u1", "blank-todo", "default", tmTodoICS)
	e.event(t, "u1", "blank-journal", "default", strings.ReplaceAll(strings.ReplaceAll(tmTodoICS, "VTODO", "VJOURNAL"), "Secret todo", "Secret journal"))
	e.event(t, "u1", "junk", "default", "not an icalendar")

	if !e.a.migrateTasksPass(always(true)) {
		t.Fatal("pass did not report done")
	}
	out := e.logs.String()
	for _, want := range []string{"blank-todo", "blank-journal", "user=u1", "calendar=default"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"good", "junk"} {
		if strings.Contains(out, "guid="+not) {
			t.Errorf("log names %s, which is not a blank event:\n%s", not, out)
		}
	}
	if strings.Contains(out, "Secret") {
		t.Errorf("log carries user content:\n%s", out)
	}
	for _, g := range []string{"good", "blank-todo", "blank-journal", "junk"} {
		if !e.hasKey("/user/u1/event/" + g) {
			t.Errorf("event %s was deleted", g)
		}
	}

	// Listed once: a restart does not repeat it.
	e.logs.b.Reset()
	if !e.a.migrateTasksPass(always(true)) {
		t.Fatal("second pass did not report done")
	}
	if strings.Contains(e.logs.String(), "blank-todo") {
		t.Errorf("second pass listed the events again:\n%s", e.logs.String())
	}
}

func TestIsBlankTodoEvent(t *testing.T) {
	both := strings.Replace(tmEventsICS, "END:VEVENT\r\n", "END:VEVENT\r\nBEGIN:VTODO\r\nUID:t\r\nEND:VTODO\r\n", 1)
	for name, tc := range map[string]struct {
		raw  string
		want bool
	}{
		"vevent":        {tmEventsICS, false},
		"vtodo only":    {tmTodoICS, true},
		"both":          {both, false},
		"empty":         {"", false},
		"unparseable":   {"garbage", false},
		"no components": {"BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nEND:VCALENDAR\r\n", false},
	} {
		if got := isBlankTodoEvent([]byte(tc.raw)); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestRunTasksMigration_BecomesLeaderLater(t *testing.T) {
	e := newTasksMigrationEnv(t)
	e.profile(t, "u1")
	e.calendar(t, "u1", &bongov1.Calendar{Guid: "default", Name: "Personal"})

	var mu sync.Mutex
	leader := false
	isLeader := func() bool { mu.Lock(); defer mu.Unlock(); return leader }
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		e.a.runTasksMigration(context.Background(), 5*time.Millisecond, isLeader)
	}()
	time.Sleep(40 * time.Millisecond)
	if e.app.calls.Load() != 0 {
		t.Fatal("applied commands while follower")
	}
	mu.Lock()
	leader = true
	mu.Unlock()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("loop did not exit after the job ran")
	}
	if !e.hasKey(string(blankEventsMarkerKey)) {
		t.Error("job did not run after gaining leadership")
	}
}

// Run starts the job: deleting the line that wires it turns this red.
func TestTasksMigration_RunWiring(t *testing.T) {
	old := tasksMigrationInterval
	tasksMigrationInterval = time.Hour
	t.Cleanup(func() { tasksMigrationInterval = old })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := blobGCLeaderTestAgentSeeded(t, logger, func(a *Agent) {
		data, _ := proto.Marshal(&bongov1.UserProfile{Email: "w@d.test"})
		if err := a.Store.Set([]byte("/user/wire/profile"), data); err != nil {
			t.Fatal(err)
		}
		cal, _ := proto.Marshal(&bongov1.Calendar{Guid: "default", Name: "Personal"})
		if err := a.Store.Set([]byte("/user/wire/calendar/default"), cal); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := a.Store.Get(blankEventsMarkerKey); err != nil {
		t.Errorf("Run did not write the blank-events marker: %v", err)
	}
	a.bgMu.Lock()
	live := a.bgLive["tasks-migration"]
	a.bgMu.Unlock()
	if live != 1 {
		t.Errorf("tasks-migration workers running = %d, want 1", live)
	}
	if a.Tasks == nil {
		t.Error("Agent.Tasks not constructed")
	}
}
