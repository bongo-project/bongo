package store

import (
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newCalendarTestEnv(t *testing.T) *CalendarStore {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewCalendarStore(ps, &directApplier{store: ps})
}

func TestCalendarStore_CRUDRoundTrip(t *testing.T) {
	cs := newCalendarTestEnv(t)
	userID := "test-user-1"

	cal := &bongov1.Calendar{
		Name:        "Work",
		Description: "Work calendar",
		Color:       "#FF0000",
		Timezone:    "America/New_York",
	}

	guid, err := cs.Create(userID, cal)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if guid == "" {
		t.Fatal("Create returned empty GUID")
	}

	got, err := cs.Get(userID, guid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Work" {
		t.Errorf("Name: got %q, want %q", got.Name, "Work")
	}
	if got.Description != "Work calendar" {
		t.Errorf("Description: got %q, want %q", got.Description, "Work calendar")
	}
	if got.Color != "#FF0000" {
		t.Errorf("Color: got %q, want %q", got.Color, "#FF0000")
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
	if got.UpdatedAt == 0 {
		t.Error("UpdatedAt should be set")
	}
}

func TestCalendarStore_DefaultCalendar(t *testing.T) {
	cs := newCalendarTestEnv(t)
	userID := "test-user-2"

	// First call creates the default calendar.
	guid, err := cs.EnsureDefault(userID)
	if err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	if guid != "default" {
		t.Errorf("GUID: got %q, want %q", guid, "default")
	}

	// Second call returns existing default.
	guid2, err := cs.EnsureDefault(userID)
	if err != nil {
		t.Fatalf("EnsureDefault (second): %v", err)
	}
	if guid2 != "default" {
		t.Errorf("GUID (second): got %q, want %q", guid2, "default")
	}

	// Verify the default calendar.
	cal, err := cs.Get(userID, "default")
	if err != nil {
		t.Fatalf("Get default: %v", err)
	}
	if cal.Name != "Personal" {
		t.Errorf("Name: got %q, want %q", cal.Name, "Personal")
	}
	if cal.Color != "#4A90D9" {
		t.Errorf("Color: got %q, want %q", cal.Color, "#4A90D9")
	}
}

func TestCalendarStore_List(t *testing.T) {
	cs := newCalendarTestEnv(t)
	userID := "test-user-3"

	cs.Create(userID, &bongov1.Calendar{Name: "Cal A"})
	cs.Create(userID, &bongov1.Calendar{Name: "Cal B"})
	cs.Create(userID, &bongov1.Calendar{Name: "Cal C"})

	calendars, err := cs.List(userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(calendars) != 3 {
		t.Errorf("got %d calendars, want 3", len(calendars))
	}
}

func TestCalendarStore_Update(t *testing.T) {
	cs := newCalendarTestEnv(t)
	userID := "test-user-4"

	guid, err := cs.Create(userID, &bongov1.Calendar{
		Name:  "Original",
		Color: "#000000",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := &bongov1.Calendar{
		Name:  "Updated",
		Color: "#FFFFFF",
	}
	if err := cs.Update(userID, guid, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := cs.Get(userID, guid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Updated" {
		t.Errorf("Name: got %q, want %q", got.Name, "Updated")
	}
	if got.Color != "#FFFFFF" {
		t.Errorf("Color: got %q, want %q", got.Color, "#FFFFFF")
	}
}

func TestCalendarStore_Delete(t *testing.T) {
	cs := newCalendarTestEnv(t)
	userID := "test-user-5"

	guid, err := cs.Create(userID, &bongov1.Calendar{Name: "To Delete"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := cs.Delete(userID, guid); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := cs.Get(userID, guid)
	if err == nil && got != nil {
		t.Error("Get after delete should fail or return nil")
	}
}

func TestCalendarAllows(t *testing.T) {
	for name, tc := range map[string]struct {
		cal         *bongov1.Calendar
		event, todo bool
	}{
		"nil":          {nil, true, false},
		"empty":        {&bongov1.Calendar{}, true, false},
		"events":       {&bongov1.Calendar{Components: []string{"VEVENT"}}, true, false},
		"todos":        {&bongov1.Calendar{Components: []string{"VTODO"}}, false, true},
		"both":         {&bongov1.Calendar{Components: []string{"VEVENT", "VTODO"}}, true, true},
		"unknown only": {&bongov1.Calendar{Components: []string{"VJOURNAL"}}, false, false},
	} {
		if got := CalendarAllows(tc.cal, ComponentEvent); got != tc.event {
			t.Errorf("%s: VEVENT = %v, want %v", name, got, tc.event)
		}
		if got := CalendarAllows(tc.cal, ComponentTodo); got != tc.todo {
			t.Errorf("%s: VTODO = %v, want %v", name, got, tc.todo)
		}
	}
}

func TestEnsureDefaultCreatesTasksCalendarForNewUser(t *testing.T) {
	cs := newCalendarTestEnv(t)
	if _, err := cs.EnsureDefault("fresh"); err != nil {
		t.Fatal(err)
	}
	tasks, err := cs.Get("fresh", TasksCalendarGUID)
	if err != nil {
		t.Fatalf("no Tasks calendar: %v", err)
	}
	if tasks.Name != "Tasks" || len(tasks.Components) != 1 || tasks.Components[0] != "VTODO" {
		t.Errorf("Tasks calendar = %+v", tasks)
	}
	personal, err := cs.Get("fresh", "default")
	if err != nil || !CalendarAllows(personal, ComponentEvent) || CalendarAllows(personal, ComponentTodo) {
		t.Errorf("Personal = %+v, %v", personal, err)
	}
	// A second call neither duplicates nor changes the set.
	if _, err := cs.EnsureDefault("fresh"); err != nil {
		t.Fatal(err)
	}
	if cals, _ := cs.List("fresh"); len(cals) != 2 {
		t.Errorf("calendars = %d, want 2", len(cals))
	}
}

// A tasks-only calendar must never be chosen as the event default, or web
// and iMIP events would be filed into a VTODO collection.
func TestEnsureDefaultNeverReturnsTasksOnlyCalendar(t *testing.T) {
	cs := newCalendarTestEnv(t)
	if err := cs.Update("u", TasksCalendarGUID, NewTasksCalendar()); err != nil {
		t.Fatal(err)
	}
	guid, err := cs.EnsureDefault("u")
	if err != nil {
		t.Fatal(err)
	}
	if guid == TasksCalendarGUID {
		t.Fatal("EnsureDefault returned the VTODO-only calendar")
	}
	cal, err := cs.Get("u", guid)
	if err != nil || !CalendarAllows(cal, ComponentEvent) {
		t.Errorf("default = %+v, %v", cal, err)
	}

	// An event calendar that sorts after Tasks is still chosen.
	cs2 := newCalendarTestEnv(t)
	for _, c := range []*bongov1.Calendar{NewTasksCalendar(), {Guid: "zzz", Name: "Late"}} {
		if err := cs2.Update("v", c.Guid, c); err != nil {
			t.Fatal(err)
		}
	}
	if guid, err := cs2.EnsureDefault("v"); err != nil || guid != "zzz" {
		t.Errorf("EnsureDefault = %q, %v; want zzz", guid, err)
	}
}
