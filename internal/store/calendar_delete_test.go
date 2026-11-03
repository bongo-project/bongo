package store

import (
	"errors"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestCheckCalendarDeletable(t *testing.T) {
	sub := &bongov1.CalendarSubscription{SourceUrl: "https://example.com/f.ics"}
	def := &bongov1.Calendar{Guid: "default"}
	work := &bongov1.Calendar{Guid: "work"}
	tasks := NewTasksCalendar()
	todoOnly := &bongov1.Calendar{Guid: "groceries", Components: []string{ComponentTodo}}
	mixed := &bongov1.Calendar{Guid: "mixed", Components: []string{ComponentEvent, ComponentTodo}}
	feed := &bongov1.Calendar{Guid: "feed", Subscription: sub}

	tests := []struct {
		name string
		cals []*bongov1.Calendar
		id   string
		want error
		nf   bool
	}{
		{"ordinary with another event calendar", []*bongov1.Calendar{def, work, tasks}, "work", nil, false},
		{"todo-only calendar with an event calendar", []*bongov1.Calendar{def, todoOnly, tasks}, "groceries", nil, false},
		{"mixed counts as event calendar", []*bongov1.Calendar{mixed, work}, "work", nil, false},
		{"tasks", []*bongov1.Calendar{def, work, tasks}, "tasks", ErrCalendarProtected, false},
		{"default", []*bongov1.Calendar{def, work, tasks}, "default", ErrCalendarProtected, false},
		{"subscription", []*bongov1.Calendar{def, feed}, "feed", ErrCalendarSubscribed, false},
		{"last event calendar", []*bongov1.Calendar{work, tasks, todoOnly}, "work", ErrLastEventCalendar, false},
		{"subscription is not an event calendar", []*bongov1.Calendar{work, feed}, "work", ErrLastEventCalendar, false},
		{"missing", []*bongov1.Calendar{def, work}, "gone", nil, true},
		{"empty id", []*bongov1.Calendar{def, work}, "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckCalendarDeletable(tt.cals, tt.id)
			switch {
			case tt.nf:
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want not found", err)
				}
			case !errors.Is(err, tt.want) || (tt.want == nil && err != nil):
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func purgeEnv(t *testing.T) (*CalendarStore, *EventStore, *TaskStore, *ACLStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &directApplier{store: ps}
	return NewCalendarStore(ps, app), NewEventStore(ps, app), NewTaskStore(ps, app), NewACLStore(ps, app)
}

func TestPurgeCalendar(t *testing.T) {
	for _, withTasks := range []bool{true, false} {
		name := "with task store"
		if !withTasks {
			name = "nil task store"
		}
		t.Run(name, func(t *testing.T) {
			cs, es, ts, acl := purgeEnv(t)
			const u = "alice"
			for _, g := range []string{"default", "work", "other"} {
				if _, err := cs.Create(u, &bongov1.Calendar{Guid: g, Name: g}); err != nil {
					t.Fatal(err)
				}
			}
			for _, e := range []*bongov1.Event{
				{Guid: "e-work", CalendarId: "work", Title: "w", StartTime: 1000, EndTime: 2000},
				{Guid: "e-work2", CalendarId: "work", Title: "w2", StartTime: 1000, EndTime: 2000},
				{Guid: "e-other", CalendarId: "other", Title: "o", StartTime: 1000, EndTime: 2000},
				{Guid: "e-default", Title: "d", StartTime: 1000, EndTime: 2000},
			} {
				if _, err := es.Create(u, e); err != nil {
					t.Fatal(err)
				}
			}
			for _, tk := range []*bongov1.Task{
				{Guid: "t-work", CalendarId: "work", Summary: "w"},
				{Guid: "t-other", CalendarId: "other", Summary: "o"},
				{Guid: "t-none", Summary: "n"},
			} {
				if _, err := ts.Create(u, tk); err != nil {
					t.Fatal(err)
				}
			}
			if err := acl.SetCalendarGrant(u, "work", "bob", GrantLevelRead, GrantStateAccepted, u, time.Unix(1, 0)); err != nil {
				t.Fatal(err)
			}
			if err := acl.SetCalendarGrant(u, "other", "bob", GrantLevelRead, GrantStateAccepted, u, time.Unix(1, 0)); err != nil {
				t.Fatal(err)
			}

			tsArg := ts
			if !withTasks {
				tsArg = nil
			}
			if err := PurgeCalendar(cs, es, tsArg, u, "work"); err != nil {
				t.Fatal(err)
			}

			if ok, _ := cs.Exists(u, "work"); ok {
				t.Error("calendar still there")
			}
			if ok, _ := cs.Exists(u, "other"); !ok {
				t.Error("other calendar gone")
			}
			if lvl, _ := acl.CalendarAccessLevel(u, "work", "bob"); lvl != "" {
				t.Errorf("grant on work survives: %q", lvl)
			}
			// A grant on a deleted calendar authorises nothing, so count the
			// rows: the level alone cannot tell a revoked grant from a stale one.
			if n, _ := acl.CountCalendarGrants(u, "work"); n != 0 {
				t.Errorf("grant rows on work = %d, want 0", n)
			}
			if lvl, _ := acl.CalendarAccessLevel(u, "other", "bob"); lvl == "" {
				t.Error("grant on other revoked")
			}
			events, _, _ := es.ListAll(u, 0, 0)
			var got []string
			for _, e := range events {
				got = append(got, e.Guid)
			}
			if len(got) != 2 {
				t.Errorf("events = %v, want e-other and e-default", got)
			}
			tasks, _ := ts.ListAll(u)
			wantTasks := 2
			if !withTasks {
				wantTasks = 3
			}
			if len(tasks) != wantTasks {
				t.Errorf("tasks = %d, want %d", len(tasks), wantTasks)
			}
			for _, tk := range tasks {
				if withTasks && tk.CalendarId == "work" {
					t.Errorf("task %s in work survives", tk.Guid)
				}
			}
		})
	}
}

func TestPurgeCalendarRefusesProtected(t *testing.T) {
	cs, es, ts, _ := purgeEnv(t)
	for _, g := range []string{"default", "tasks"} {
		if _, err := cs.Create("alice", &bongov1.Calendar{Guid: g, Name: g}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := es.Create("alice", &bongov1.Event{Guid: "e", Title: "d", StartTime: 1, EndTime: 2}); err != nil {
		t.Fatal(err)
	}
	for _, g := range []string{"default", "tasks", ""} {
		if err := PurgeCalendar(cs, es, ts, "alice", g); !errors.Is(err, ErrCalendarProtected) {
			t.Errorf("PurgeCalendar(%q) = %v", g, err)
		}
	}
	if ok, _ := cs.Exists("alice", "default"); !ok {
		t.Error("default deleted")
	}
	if _, n, _ := es.ListAll("alice", 0, 0); n != 1 {
		t.Errorf("events = %d", n)
	}
}
