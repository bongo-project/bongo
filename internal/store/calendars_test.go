package store

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

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

func TestCalendarStore_IsSharedWith(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	cs := NewCalendarStore(ps, &directApplier{store: ps})

	mk := func(owner, name string, with ...string) {
		t.Helper()
		if _, err := cs.Create(owner, &bongov1.Calendar{Name: name, SharedWith: with}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	mk("bob", "b1", "alice")
	mk("bob", "b2")
	mk("carol", "c1", "alice")
	mk("erin", "e1", "zed")
	for i := range 100 {
		if err := ps.Set([]byte(fmt.Sprintf("/user/dave/mail/%03d", i)), []byte("x")); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	// Raw keys the guards must keep unreachable: a slash-steered owner that
	// would otherwise match, and an empty-string share entry.
	raw, err := proto.Marshal(&bongov1.Calendar{Name: "steered", SharedWith: []string{"alice"}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := ps.Set([]byte("/user/bob/x/calendar/c1"), raw); err != nil {
		t.Fatalf("Set: %v", err)
	}
	mk("frank", "f1", "")

	var prefixes []string
	cs.scan = func(prefix []byte) (Iterator, error) {
		prefixes = append(prefixes, string(prefix))
		return ps.PrefixScan(prefix)
	}

	tests := []struct {
		name          string
		owner, caller string
		want          bool
	}{
		{"shared", "bob", "alice", true},
		{"other owner shares, not this one", "erin", "alice", false},
		{"not shared with caller", "bob", "zed", false},
		{"carol shares but bob asked", "dave", "alice", false},
		{"unknown owner", "nobody", "alice", false},
		{"empty owner", "", "alice", false},
		{"empty caller", "frank", "", false},
		{"owner with slash", "bob/x", "alice", false},
		{"case differs", "bob", "ALICE", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefixes = nil
			got, err := cs.IsSharedWith(tt.owner, tt.caller)
			if err != nil {
				t.Fatalf("IsSharedWith: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if strings.Contains(tt.owner, "/") && len(prefixes) != 0 {
				t.Errorf("slash owner must not scan, got %q", prefixes)
			}
			for _, p := range prefixes {
				if p != "/user/"+tt.owner+"/calendar/" {
					t.Errorf("scanned prefix %q outside the owner's calendars", p)
				}
			}
		})
	}
}
