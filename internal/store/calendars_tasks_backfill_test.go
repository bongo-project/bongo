package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Every account has a Tasks list: EnsureDefault back-fills it for a user with
// no VTODO-capable, non-subscription calendar, cap or no cap.
func TestEnsureDefaultBackfillsTasks(t *testing.T) {
	ev := func(guid string) *bongov1.Calendar { return &bongov1.Calendar{Guid: guid, Name: "Cal " + guid} }
	feed := &bongov1.Calendar{Guid: "sub", Name: "Feed", Components: []string{ComponentTodo}, Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://example.org/f.ics"}}
	personal := func() *bongov1.Calendar { return &bongov1.Calendar{Guid: "default", Name: "Personal"} }
	tests := []struct {
		name      string
		seed      []*bongov1.Calendar
		wantTasks bool
		wantCount int
	}{
		{"personal only", []*bongov1.Calendar{personal()}, true, 2},
		{"client VTODO calendar", []*bongov1.Calendar{personal(), {Guid: "abc", Name: "Lists", Components: []string{ComponentTodo}}}, false, 2},
		{"events and tasks calendar", []*bongov1.Calendar{{Guid: "default", Name: "Personal", Components: []string{ComponentEvent, ComponentTodo}}}, false, 1},
		{"only VTODO is a subscription", []*bongov1.Calendar{personal(), feed}, true, 3},
		{"five event calendars exceed the cap", []*bongov1.Calendar{personal(), ev("w"), ev("x"), ev("y"), ev("z")}, true, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cs := newCalendarTestEnv(t)
			for _, c := range tc.seed {
				if err := cs.Update("u", c.Guid, c); err != nil {
					t.Fatal(err)
				}
			}
			guid, err := cs.EnsureDefault("u")
			if err != nil || guid != "default" {
				t.Fatalf("EnsureDefault = %q, %v", guid, err)
			}
			list, _ := cs.List("u")
			if len(list) != tc.wantCount {
				t.Fatalf("%d calendars, want %d", len(list), tc.wantCount)
			}
			c, err := cs.Get("u", TasksCalendarGUID)
			if !tc.wantTasks {
				if err == nil {
					t.Errorf("unexpected tasks calendar %+v", c)
				}
				return
			}
			if err != nil || len(c.Components) != 1 || c.Components[0] != ComponentTodo {
				t.Fatalf("tasks = %+v, %v", c, err)
			}
			if _, err := cs.EnsureDefault("u"); err != nil {
				t.Fatal(err)
			}
			if again, _ := cs.List("u"); len(again) != tc.wantCount {
				t.Errorf("second call: %d calendars, want %d", len(again), tc.wantCount)
			}
		})
	}
}

// A VEVENT calendar a client made at guid "tasks" is never overwritten.
func TestEnsureDefaultDoesNotOverwriteEventCalendarAtTasksGUID(t *testing.T) {
	cs := newCalendarTestEnv(t)
	mine := &bongov1.Calendar{Guid: TasksCalendarGUID, Name: "Mine", Color: "#111111"}
	if err := cs.Update("u", TasksCalendarGUID, mine); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.EnsureDefault("u"); err != nil {
		t.Fatal(err)
	}
	c, err := cs.Get("u", TasksCalendarGUID)
	if err != nil || c.Name != "Mine" || c.Color != "#111111" || len(c.Components) != 0 {
		t.Errorf("tasks record changed: %+v, %v", c, err)
	}
}

type refusingApplier struct{}

func (refusingApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error {
	return errors.New("not leader")
}

// EnsureDefault runs on read paths: a node that cannot write must still answer
// for a user who has a default calendar, and must fail for one who has none.
func TestEnsureDefaultBackfillFailure(t *testing.T) {
	cs := newCalendarTestEnv(t)
	if err := cs.Update("u", "default", &bongov1.Calendar{Guid: "default", Name: "Personal"}); err != nil {
		t.Fatal(err)
	}
	cs.applier = refusingApplier{}
	if guid, err := cs.EnsureDefault("u"); err != nil || guid != "default" {
		t.Errorf("user with a default: EnsureDefault = %q, %v", guid, err)
	}
	if guid, err := cs.EnsureDefault("new"); err == nil {
		t.Errorf("user with no default: EnsureDefault = %q, want an error", guid)
	}
}

// tasksRefusingApplier refuses only the Tasks calendar write.
type tasksRefusingApplier struct {
	RaftApplier
	writes int
}

func (a *tasksRefusingApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	a.writes++
	if strings.HasSuffix(string(cmd.Key), "/calendar/"+TasksCalendarGUID) {
		return errors.New("not leader")
	}
	return a.RaftApplier.ApplyCommand(cmd, d)
}

// A user with no default still fails when only the Tasks write is refused, so
// a new account is not left without Tasks and told all is well.
func TestEnsureDefaultNewUserTasksFailureIsReturned(t *testing.T) {
	cs := newCalendarTestEnv(t)
	cs.applier = &tasksRefusingApplier{RaftApplier: cs.applier}
	if guid, err := cs.EnsureDefault("new"); err == nil {
		t.Errorf("EnsureDefault = %q, want the Tasks write error", guid)
	}
}

// Once Tasks exists a repeat call writes nothing, so a rename survives.
func TestEnsureDefaultRepeatCallWritesNothing(t *testing.T) {
	cs := newCalendarTestEnv(t)
	if err := cs.Update("u", "default", &bongov1.Calendar{Guid: "default", Name: "Personal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.EnsureDefault("u"); err != nil {
		t.Fatal(err)
	}
	counter := &tasksRefusingApplier{RaftApplier: cs.applier}
	cs.applier = counter
	if _, err := cs.EnsureDefault("u"); err != nil {
		t.Fatal(err)
	}
	if counter.writes != 0 {
		t.Errorf("repeat call made %d writes", counter.writes)
	}
}
