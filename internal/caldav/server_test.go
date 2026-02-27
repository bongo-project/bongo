package caldav

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/emersion/go-ical"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// directApplier applies commands directly to Pebble (no Raft).
type directApplier struct {
	store *store.PebbleStore
}

func (a *directApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.store.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.store.Delete(cmd.Key)
	}
	return nil
}

func newTestBackend(t *testing.T) (*Backend, string) {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	applier := &directApplier{store: ps}
	events := store.NewEventStore(ps, applier)
	calendars := store.NewCalendarStore(ps, applier)

	userID := "test-user-1"
	// Create default calendar.
	if _, err := calendars.EnsureDefault(userID); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}

	return NewBackend(events, calendars, slog.Default()), userID
}

func TestListCalendarObjects(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)

	// Create two events via the store.
	backend.events.Create(userID, &bongov1.Event{
		CalendarId: "default",
		Title:      "Event 1",
		StartTime:  1700000000,
		EndTime:    1700003600,
	})
	backend.events.Create(userID, &bongov1.Event{
		CalendarId: "default",
		Title:      "Event 2",
		StartTime:  1700010000,
		EndTime:    1700013600,
	})

	objects, err := backend.ListCalendarObjects(ctx, "/caldav/"+userID+"/calendars/default/", nil)
	if err != nil {
		t.Fatalf("ListCalendarObjects: %v", err)
	}
	if len(objects) != 2 {
		t.Errorf("got %d objects, want 2", len(objects))
	}
}

func TestPutAndGetCalendarObject(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)

	// Build an iCal object.
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Test//Test//EN")

	vevent := ical.NewEvent()
	vevent.Props.SetText(ical.PropUID, "test-event-1")
	vevent.Props.SetText(ical.PropSummary, "Test Event")
	vevent.Props.SetText(ical.PropDescription, "A test description")
	vevent.Props.SetText(ical.PropLocation, "Room 1")
	vevent.Props.SetDateTime(ical.PropDateTimeStart, time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC))
	vevent.Props.SetDateTime(ical.PropDateTimeEnd, time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC))
	vevent.Props.SetDateTime(ical.PropDateTimeStamp, time.Now().UTC())
	cal.Children = append(cal.Children, vevent.Component)

	path := "/caldav/" + userID + "/calendars/default/test-event-1.ics"

	// Put the calendar object.
	obj, err := backend.PutCalendarObject(ctx, path, cal, nil)
	if err != nil {
		t.Fatalf("PutCalendarObject: %v", err)
	}
	if obj == nil {
		t.Fatal("PutCalendarObject returned nil")
	}

	// Get it back.
	got, err := backend.GetCalendarObject(ctx, path, nil)
	if err != nil {
		t.Fatalf("GetCalendarObject: %v", err)
	}
	if got.Data == nil {
		t.Fatal("GetCalendarObject returned nil Data")
	}

	events := got.Data.Events()
	if len(events) == 0 {
		t.Fatal("no events in calendar object")
	}
	summary, _ := events[0].Props.Text(ical.PropSummary)
	if summary != "Test Event" {
		t.Errorf("Summary: got %q, want %q", summary, "Test Event")
	}
}

func TestDeleteCalendarObject(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)

	// Create an event.
	backend.events.Create(userID, &bongov1.Event{
		Guid:       "to-delete",
		CalendarId: "default",
		Title:      "Delete Me",
		StartTime:  1700000000,
		EndTime:    1700003600,
	})

	path := "/caldav/" + userID + "/calendars/default/to-delete.ics"

	// Delete it.
	if err := backend.DeleteCalendarObject(ctx, path); err != nil {
		t.Fatalf("DeleteCalendarObject: %v", err)
	}

	// Verify it's gone.
	_, err := backend.GetCalendarObject(ctx, path, nil)
	if err == nil {
		t.Error("GetCalendarObject should fail after delete")
	}
}
