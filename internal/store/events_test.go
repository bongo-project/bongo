package store

import (
	"strings"
	"testing"
	"time"

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

func TestExpandRecurrence_Weekly(t *testing.T) {
	// A weekly event on Mondays starting Jan 6 2025 (Monday), 1hr duration.
	start := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)
	event := &bongov1.Event{
		Guid:       "recurring-1",
		CalendarId: "default",
		Title:      "Weekly Standup",
		StartTime:  start.Unix(),
		EndTime:    start.Add(time.Hour).Unix(),
		Rrule:      "FREQ=WEEKLY;BYDAY=MO",
	}

	// Query for January 2025.
	rangeStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	rangeEnd := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC).Unix()

	occurrences, err := ExpandRecurrence(event, rangeStart, rangeEnd)
	if err != nil {
		t.Fatalf("ExpandRecurrence: %v", err)
	}

	// Jan 2025 Mondays: 6, 13, 20, 27 → 4 occurrences.
	if len(occurrences) != 4 {
		t.Fatalf("got %d occurrences, want 4", len(occurrences))
	}

	// Check first occurrence date.
	first := time.Unix(occurrences[0].StartTime, 0).UTC()
	if first.Day() != 6 || first.Month() != time.January {
		t.Errorf("first occurrence: got %v, want Jan 6", first)
	}

	// Check GUID format includes date.
	if !strings.HasPrefix(occurrences[0].Guid, "recurring-1_2025-01-06") {
		t.Errorf("GUID: got %q, want prefix recurring-1_2025-01-06", occurrences[0].Guid)
	}
}

func TestExpandRecurrence_Exdates(t *testing.T) {
	start := time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)
	event := &bongov1.Event{
		Guid:      "recurring-2",
		Title:     "Daily Check-in",
		StartTime: start.Unix(),
		EndTime:   start.Add(30 * time.Minute).Unix(),
		Rrule:     "FREQ=DAILY",
		Exdates:   []string{"2025-01-03", "2025-01-05"},
	}

	rangeStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	rangeEnd := time.Date(2025, 1, 8, 0, 0, 0, 0, time.UTC).Unix()

	occurrences, err := ExpandRecurrence(event, rangeStart, rangeEnd)
	if err != nil {
		t.Fatalf("ExpandRecurrence: %v", err)
	}

	// 7 days minus 2 exdates = 5.
	if len(occurrences) != 5 {
		t.Errorf("got %d occurrences, want 5", len(occurrences))
	}

	// Verify excluded dates are not present.
	for _, occ := range occurrences {
		d := time.Unix(occ.StartTime, 0).UTC().Format("2006-01-02")
		if d == "2025-01-03" || d == "2025-01-05" {
			t.Errorf("exdate %s should have been skipped", d)
		}
	}
}

func TestExpandRecurrence_NoRrule(t *testing.T) {
	event := &bongov1.Event{
		Guid:      "single-1",
		Title:     "One-off",
		StartTime: 1700000000,
		EndTime:   1700003600,
	}

	occurrences, err := ExpandRecurrence(event, 1699900000, 1700100000)
	if err != nil {
		t.Fatalf("ExpandRecurrence: %v", err)
	}
	if len(occurrences) != 1 {
		t.Errorf("got %d occurrences, want 1", len(occurrences))
	}
	if occurrences[0].Guid != "single-1" {
		t.Errorf("GUID: got %q, want %q", occurrences[0].Guid, "single-1")
	}
}

func TestEventStore_ListByDateRange_Recurring(t *testing.T) {
	es := newEventTestEnv(t)
	userID := "test-user-recur"

	// Create a weekly Monday event starting Jan 6 2025.
	start := time.Date(2025, 1, 6, 10, 0, 0, 0, time.UTC)
	es.Create(userID, &bongov1.Event{
		CalendarId: "default",
		Title:      "Weekly",
		StartTime:  start.Unix(),
		EndTime:    start.Add(time.Hour).Unix(),
		Rrule:      "FREQ=WEEKLY;BYDAY=MO",
	})

	// Also create a normal non-recurring event in January.
	es.Create(userID, &bongov1.Event{
		CalendarId: "default",
		Title:      "One-off Jan 15",
		StartTime:  time.Date(2025, 1, 15, 14, 0, 0, 0, time.UTC).Unix(),
		EndTime:    time.Date(2025, 1, 15, 15, 0, 0, 0, time.UTC).Unix(),
	})

	rangeStart := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	rangeEnd := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC).Unix()

	events, err := es.ListByDateRange(userID, rangeStart, rangeEnd, "")
	if err != nil {
		t.Fatalf("ListByDateRange: %v", err)
	}

	// 4 weekly occurrences + 1 one-off = 5 events.
	if len(events) != 5 {
		t.Errorf("got %d events, want 5", len(events))
	}

	// Should be sorted by start_time.
	for i := 1; i < len(events); i++ {
		if events[i].StartTime < events[i-1].StartTime {
			t.Errorf("events not sorted: [%d].StartTime=%d < [%d].StartTime=%d",
				i, events[i].StartTime, i-1, events[i-1].StartTime)
		}
	}
}
