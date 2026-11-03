package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func seedCalendars(t *testing.T, cs *CalendarStore, user string, n int) {
	t.Helper()
	for i := range n {
		if _, err := cs.Create(user, &bongov1.Calendar{Guid: fmt.Sprintf("seed%d", i), Name: "Seed"}); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
}

func listLen(t *testing.T, cs *CalendarStore, user string) int {
	t.Helper()
	cals, err := cs.List(user)
	if err != nil {
		t.Fatal(err)
	}
	return len(cals)
}

func TestCalendarStore_CreateNewLimit(t *testing.T) {
	const user = "u1"
	tests := []struct {
		name    string
		seeded  int
		guid    string
		wantErr error
		wantLen int
	}{
		{name: "below the cap", seeded: MaxCalendarsPerUser - 1, guid: "new", wantLen: MaxCalendarsPerUser},
		{name: "at the cap", seeded: MaxCalendarsPerUser, guid: "new", wantErr: ErrCalendarLimit, wantLen: MaxCalendarsPerUser},
		{name: "past the cap", seeded: MaxCalendarsPerUser + 1, guid: "new", wantErr: ErrCalendarLimit, wantLen: MaxCalendarsPerUser + 1},
		{name: "existing GUID wins at the cap", seeded: MaxCalendarsPerUser, guid: "seed0", wantErr: ErrExists, wantLen: MaxCalendarsPerUser},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cs := newCalendarTestEnv(t)
			seedCalendars(t, cs, user, tc.seeded)
			err := cs.CreateNew(user, &bongov1.Calendar{Guid: tc.guid, Name: "New"})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("CreateNew = %v, want %v", err, tc.wantErr)
			}
			if got := listLen(t, cs, user); got != tc.wantLen {
				t.Errorf("calendars = %d, want %d", got, tc.wantLen)
			}
			if tc.wantErr != nil && tc.guid == "new" {
				if _, err := cs.Get(user, "new"); err == nil {
					t.Error("a refused create wrote the calendar")
				}
			}
		})
	}
}

// Another user's calendars do not count toward the cap.
func TestCalendarStore_CreateNewLimitIsPerUser(t *testing.T) {
	cs := newCalendarTestEnv(t)
	seedCalendars(t, cs, "u1", MaxCalendarsPerUser)
	if err := cs.CreateNew("u2", &bongov1.Calendar{Guid: "mine", Name: "Mine"}); err != nil {
		t.Fatalf("CreateNew for another user = %v", err)
	}
}

// EnsureDefault is exempt: five tasks-only calendars plus the automatic
// Personal and Tasks calendars give a user more than the cap.
func TestCalendarStore_EnsureDefaultExempt(t *testing.T) {
	cs := newCalendarTestEnv(t)
	for i := range MaxCalendarsPerUser {
		if _, err := cs.Create("u1", &bongov1.Calendar{
			Guid: fmt.Sprintf("t%d", i), Name: "T", Components: []string{ComponentTodo},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cs.EnsureDefault("u1"); err != nil {
		t.Fatalf("EnsureDefault at the cap: %v", err)
	}
	if got := listLen(t, cs, "u1"); got <= MaxCalendarsPerUser {
		t.Errorf("EnsureDefault left %d calendars, want more than %d", got, MaxCalendarsPerUser)
	}
}

// A calendar shared with the user is a grant, not a record the user owns.
func TestCalendarStore_SharedCalendarsDoNotCount(t *testing.T) {
	e := newACLEnv(t)
	for i := range 3 {
		e.calendar(t, "owner", fmt.Sprintf("o%d", i))
		e.grant(t, "owner", fmt.Sprintf("o%d", i), "me", GrantLevelWrite, GrantStateAccepted)
	}
	seedCalendars(t, e.cal, "me", MaxCalendarsPerUser-1)
	if err := e.cal.CreateNew("me", &bongov1.Calendar{Guid: "fifth", Name: "Fifth"}); err != nil {
		t.Fatalf("CreateNew with three shared calendars = %v", err)
	}
	if err := e.cal.CreateNew("me", &bongov1.Calendar{Guid: "sixth", Name: "Sixth"}); !errors.Is(err, ErrCalendarLimit) {
		t.Fatalf("sixth = %v, want ErrCalendarLimit", err)
	}
}

// Concurrent creates with different GUIDs take different GUID locks; only the
// per-user lock makes the count check and the write one step.
func TestCalendarStore_CreateNewLimitConcurrent(t *testing.T) {
	const user = "u1"
	cs := newCalendarTestEnv(t)
	seedCalendars(t, cs, user, MaxCalendarsPerUser-1)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	start := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = cs.CreateNew(user, &bongov1.Calendar{Guid: fmt.Sprintf("race%d", i), Name: "R"})
		}()
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrCalendarLimit):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d creates succeeded, want 1", ok)
	}
	if got := listLen(t, cs, user); got != MaxCalendarsPerUser {
		t.Errorf("calendars = %d, want %d", got, MaxCalendarsPerUser)
	}
}
