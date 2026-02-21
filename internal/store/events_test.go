package store

import (
	"testing"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func newEventTestEnv(t *testing.T) *EventStore {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewEventStore(ps, &directApplier{store: ps})
}

func TestEventStore_CRUDRoundTrip(t *testing.T) {
	es := newEventTestEnv(t)
	userID := "test-user-1"

	event := &bongov1.Event{
		CalendarId:  "default",
		Title:       "Team Meeting",
		Description: "Weekly sync",
		Location:    "Room 42",
		StartTime:   1700000000,
		EndTime:     1700003600,
		Timezone:    "America/New_York",
	}

	guid, err := es.Create(userID, event)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if guid == "" {
		t.Fatal("Create returned empty GUID")
	}

	got, err := es.Get(userID, guid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Team Meeting" {
		t.Errorf("Title: got %q, want %q", got.Title, "Team Meeting")
	}
	if got.Description != "Weekly sync" {
		t.Errorf("Description: got %q, want %q", got.Description, "Weekly sync")
	}
	if got.Location != "Room 42" {
		t.Errorf("Location: got %q, want %q", got.Location, "Room 42")
	}
	if got.StartTime != 1700000000 {
		t.Errorf("StartTime: got %d, want %d", got.StartTime, 1700000000)
	}
	if got.EndTime != 1700003600 {
		t.Errorf("EndTime: got %d, want %d", got.EndTime, 1700003600)
	}
	if got.CalendarId != "default" {
		t.Errorf("CalendarId: got %q, want %q", got.CalendarId, "default")
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt should be set")
	}
	if got.UpdatedAt == 0 {
		t.Error("UpdatedAt should be set")
	}
}

func TestEventStore_Update(t *testing.T) {
	es := newEventTestEnv(t)
	userID := "test-user-2"

	guid, err := es.Create(userID, &bongov1.Event{
		Title:     "Original",
		StartTime: 1700000000,
		EndTime:   1700003600,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := &bongov1.Event{
		Title:     "Updated",
		StartTime: 1700010000,
		EndTime:   1700013600,
	}
	if err := es.Update(userID, guid, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := es.Get(userID, guid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Updated" {
		t.Errorf("Title: got %q, want %q", got.Title, "Updated")
	}
	if got.StartTime != 1700010000 {
		t.Errorf("StartTime: got %d, want %d", got.StartTime, 1700010000)
	}
}

func TestEventStore_Delete(t *testing.T) {
	es := newEventTestEnv(t)
	userID := "test-user-3"

	guid, err := es.Create(userID, &bongov1.Event{
		Title:     "To Delete",
		StartTime: 1700000000,
		EndTime:   1700003600,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := es.Delete(userID, guid); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := es.Get(userID, guid)
	if err == nil && got != nil {
		t.Error("Get after delete should fail or return nil")
	}
}

func TestEventStore_ListByDateRange(t *testing.T) {
	es := newEventTestEnv(t)
	userID := "test-user-4"

	// Event 1: Jan 15 10:00-11:00
	es.Create(userID, &bongov1.Event{
		CalendarId: "cal1",
		Title:      "Jan Event",
		StartTime:  1705312800, // 2024-01-15 10:00 UTC
		EndTime:    1705316400, // 2024-01-15 11:00 UTC
	})

	// Event 2: Feb 15 10:00-11:00
	es.Create(userID, &bongov1.Event{
		CalendarId: "cal1",
		Title:      "Feb Event",
		StartTime:  1707991200, // 2024-02-15 10:00 UTC
		EndTime:    1707994800, // 2024-02-15 11:00 UTC
	})

	// Event 3: Feb 20 14:00-15:00 (different calendar)
	es.Create(userID, &bongov1.Event{
		CalendarId: "cal2",
		Title:      "Feb Event Cal2",
		StartTime:  1708437600, // 2024-02-20 14:00 UTC
		EndTime:    1708441200, // 2024-02-20 15:00 UTC
	})

	// Query February: should find events 2 and 3.
	febStart := int64(1706745600) // 2024-02-01 00:00 UTC
	febEnd := int64(1709251200)   // 2024-03-01 00:00 UTC

	events, err := es.ListByDateRange(userID, febStart, febEnd, "")
	if err != nil {
		t.Fatalf("ListByDateRange: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("got %d events, want 2", len(events))
	}

	// Query February, calendar cal1 only.
	events, err = es.ListByDateRange(userID, febStart, febEnd, "cal1")
	if err != nil {
		t.Fatalf("ListByDateRange with calendarID: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("got %d events, want 1", len(events))
	}
	if len(events) > 0 && events[0].Title != "Feb Event" {
		t.Errorf("Title: got %q, want %q", events[0].Title, "Feb Event")
	}
}

func TestEventStore_ListAll(t *testing.T) {
	es := newEventTestEnv(t)
	userID := "test-user-5"

	for i := 0; i < 5; i++ {
		es.Create(userID, &bongov1.Event{
			Title:     "Event " + string(rune('A'+i)),
			StartTime: int64(1700000000 + i*3600),
			EndTime:   int64(1700003600 + i*3600),
		})
	}

	events, total, err := es.ListAll(userID, 2, 2)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if total != 5 {
		t.Errorf("total: got %d, want 5", total)
	}
	if len(events) != 2 {
		t.Errorf("events: got %d, want 2", len(events))
	}
}
