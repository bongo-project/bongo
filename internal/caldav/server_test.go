package caldav

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-ical"
	webdav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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

// testCalendar builds a minimal iCal object with the given uid and summary.
func testCalendar(uid, summary string) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropVersion, "2.0")
	cal.Props.SetText(ical.PropProductID, "-//Test//Test//EN")

	vevent := ical.NewEvent()
	vevent.Props.SetText(ical.PropUID, uid)
	vevent.Props.SetText(ical.PropSummary, summary)
	vevent.Props.SetDateTime(ical.PropDateTimeStart, time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC))
	vevent.Props.SetDateTime(ical.PropDateTimeEnd, time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC))
	vevent.Props.SetDateTime(ical.PropDateTimeStamp, time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC))
	cal.Children = append(cal.Children, vevent.Component)
	return cal
}

// assertPreconditionFailed checks the error is the 412 this backend returns.
// go-webdav's HTTPError is in its internal package so the status code is not
// inspectable here; ErrPreconditionFailed is wrapped into every one of them
// precisely so this assertion does not depend on message text.
func assertPreconditionFailed(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected a 412, got nil", what)
	}
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Errorf("%s: error = %v, want a precondition failure", what, err)
	}
}

// TestCalDAVETagChangesWithContent is the foundation the conditional requests
// below rest on.
//
// The ETag used to be the record's UpdatedAt in whole seconds. Two edits
// landing in the same second produced identical ETags, so a client could not
// see the second change and an If-Match built on it could be satisfied by a
// version the client had never seen. Honouring If-Match on top of that would
// have looked correct and protected nothing.
func TestCalDAVETagChangesWithContent(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := "/caldav/" + userID + "/calendars/default/e1.ics"

	first, err := backend.PutCalendarObject(ctx, path, testCalendar("e1", "Original"), nil)
	if err != nil {
		t.Fatalf("first put: %v", err)
	}
	// Same second, different content.
	second, err := backend.PutCalendarObject(ctx, path, testCalendar("e1", "Changed"), nil)
	if err != nil {
		t.Fatalf("second put: %v", err)
	}

	if first.ETag == "" || second.ETag == "" {
		t.Fatal("ETag is empty")
	}
	if first.ETag == second.ETag {
		t.Errorf("ETag did not change after an edit in the same second (%q): "+
			"a second-resolution ETag cannot support If-Match", first.ETag)
	}
}

// TestCalDAVIfMatchRejectsStaleWrite covers the lost-update case: two clients
// hold the same event, both edit it, and the second write must be refused
// rather than silently overwriting the first.
func TestCalDAVIfMatchRejectsStaleWrite(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := "/caldav/" + userID + "/calendars/default/e1.ics"

	original, err := backend.PutCalendarObject(ctx, path, testCalendar("e1", "Original"), nil)
	if err != nil {
		t.Fatalf("seed put: %v", err)
	}
	staleETag := original.ETag

	// Client A writes, moving the resource on.
	if _, err := backend.PutCalendarObject(ctx, path, testCalendar("e1", "From A"), nil); err != nil {
		t.Fatalf("client A put: %v", err)
	}

	// Client B still holds the original ETag.
	_, err = backend.PutCalendarObject(ctx, path, testCalendar("e1", "From B"),
		&caldav.PutCalendarObjectOptions{IfMatch: webdav.ConditionalMatch(staleETag)})
	if err == nil {
		t.Fatal("a stale If-Match was accepted: client B silently overwrote client A's edit")
	}
	assertPreconditionFailed(t, err, "conditional write")

	// Client A's version must still be there.
	got, err := backend.GetCalendarObject(ctx, path, nil)
	if err != nil {
		t.Fatalf("get after refused write: %v", err)
	}
	summary, _ := got.Data.Events()[0].Props.Text(ical.PropSummary)
	if summary != "From A" {
		t.Errorf("stored summary = %q, want \"From A\": the refused write was applied anyway", summary)
	}
}

// TestCalDAVIfMatchAcceptsCurrentETag is the control. Without it, refusing
// every conditional write would satisfy the test above while breaking all
// well-behaved clients.
func TestCalDAVIfMatchAcceptsCurrentETag(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := "/caldav/" + userID + "/calendars/default/e1.ics"

	original, err := backend.PutCalendarObject(ctx, path, testCalendar("e1", "Original"), nil)
	if err != nil {
		t.Fatalf("seed put: %v", err)
	}

	if _, err := backend.PutCalendarObject(ctx, path, testCalendar("e1", "Updated"),
		&caldav.PutCalendarObjectOptions{IfMatch: webdav.ConditionalMatch(original.ETag)}); err != nil {
		t.Fatalf("a current If-Match was refused: %v", err)
	}
}

// TestCalDAVIfNoneMatchWildcard covers create-only semantics: the header exists
// so two clients cannot both believe they created the same event.
func TestCalDAVIfNoneMatchWildcard(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := "/caldav/" + userID + "/calendars/default/e1.ics"

	// Creating something new is allowed.
	if _, err := backend.PutCalendarObject(ctx, path, testCalendar("e1", "New"),
		&caldav.PutCalendarObjectOptions{IfNoneMatch: webdav.ConditionalMatch("*")}); err != nil {
		t.Fatalf("If-None-Match: * on a new object was refused: %v", err)
	}

	// Doing it again must fail: the object now exists.
	_, err := backend.PutCalendarObject(ctx, path, testCalendar("e1", "Clobber"),
		&caldav.PutCalendarObjectOptions{IfNoneMatch: webdav.ConditionalMatch("*")})
	if err == nil {
		t.Fatal("If-None-Match: * overwrote an existing object")
	}
	assertPreconditionFailed(t, err, "conditional write")
}

// TestCalDAVIfMatchOnMissingObject: a conditional update of something that has
// been deleted must fail rather than quietly recreating it.
func TestCalDAVIfMatchOnMissingObject(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := "/caldav/" + userID + "/calendars/default/gone.ics"

	_, err := backend.PutCalendarObject(ctx, path, testCalendar("gone", "Ghost"),
		&caldav.PutCalendarObjectOptions{IfMatch: webdav.ConditionalMatch(`"deadbeef"`)})
	if err == nil {
		t.Fatal("If-Match against a nonexistent object was accepted")
	}
	assertPreconditionFailed(t, err, "conditional write")
}

// --- Cross-account scoping -------------------------------------------------
//
// Every CalDAV URL contains a {userID} segment, and the backend deliberately
// ignores it: the owner comes from the Basic-Auth principal in the request
// context, and response paths are re-synthesised from that. Nothing else
// enforces the boundary -- go-webdav hands REPORT multiget hrefs straight to
// GetCalendarObject with no path validation -- so that one convention is the
// whole cross-account guarantee for calendar data.
//
// It had no test. These cover each verb: a request from victim's own session
// works, and the identical request made by an attacker who simply points at
// the victim's URL must not reach the victim's data.

// twoUserBackend returns a backend plus an event owned by "victim".
func twoUserBackend(t *testing.T) (*Backend, string, string, string) {
	t.Helper()
	backend, attacker := newTestBackend(t)
	victim := "victim-user"

	if _, err := backend.calendars.EnsureDefault(victim); err != nil {
		t.Fatalf("EnsureDefault for victim: %v", err)
	}
	victimCtx := WithUserID(context.Background(), victim)
	if _, err := backend.PutCalendarObject(victimCtx,
		objectPath(victim, "default", "secret-1"),
		testCalendar("secret-1", "Victim private meeting"), nil); err != nil {
		t.Fatalf("seeding victim event: %v", err)
	}
	return backend, attacker, victim, "secret-1"
}

func TestCalDAVReadIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, guid := twoUserBackend(t)

	// Control: the victim can read their own event, so a failure below means
	// scoping and not a broken fixture.
	if _, err := backend.GetCalendarObject(WithUserID(context.Background(), victim),
		objectPath(victim, "default", guid), nil); err != nil {
		t.Fatalf("victim cannot read their own event: %v", err)
	}

	// The attack: authenticated as attacker, ask for the victim's URL.
	obj, err := backend.GetCalendarObject(WithUserID(context.Background(), attacker),
		objectPath(victim, "default", guid), nil)
	if err == nil {
		t.Fatalf("attacker read the victim's event by requesting its URL path: %+v", obj)
	}
}

func TestCalDAVWriteIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, guid := twoUserBackend(t)

	// Overwrite attempt against the victim's existing event.
	_, err := backend.PutCalendarObject(WithUserID(context.Background(), attacker),
		objectPath(victim, "default", guid),
		testCalendar(guid, "Overwritten by attacker"), nil)

	// Whether the PUT errors or is redirected into the attacker's own
	// keyspace, what must never happen is the victim's event changing.
	victimEvent, gerr := backend.events.Get(victim, guid)
	if gerr != nil {
		t.Fatalf("victim's event disappeared entirely (put err was %v): %v", err, gerr)
	}
	if victimEvent.Title != "Victim private meeting" {
		t.Errorf("attacker overwrote the victim's event via its URL path: title is now %q",
			victimEvent.Title)
	}
}

func TestCalDAVDeleteIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, guid := twoUserBackend(t)

	_ = backend.DeleteCalendarObject(WithUserID(context.Background(), attacker),
		objectPath(victim, "default", guid))

	if _, err := backend.events.Get(victim, guid); err != nil {
		t.Errorf("attacker deleted the victim's event by requesting its URL path: %v", err)
	}
}

func TestCalDAVCalendarMetadataIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, _ := twoUserBackend(t)

	// GetCalendar has no direct test anywhere in the repo; it is reachable via
	// PROPFIND and would leak calendar existence and naming across accounts.
	//
	// Note what correct looks like here: the request is NOT refused. The
	// backend ignores the URL's user segment and re-synthesises the response
	// path from the session, so the attacker is answered with their OWN
	// calendar. The assertion is therefore about identity, not about getting
	// an error -- an earlier version of this test checked only for a non-nil
	// result and failed against correct code.
	cal, err := backend.GetCalendar(WithUserID(context.Background(), attacker),
		calPath(victim, "default"))
	if err != nil {
		return // refusing outright is also correctly scoped
	}
	if strings.Contains(cal.Path, victim) {
		t.Errorf("attacker was served a calendar under the victim's path: %+v", cal)
	}
	if !strings.Contains(cal.Path, attacker) {
		t.Errorf("returned calendar path %q belongs to neither party; the owner is not "+
			"being taken from the authenticated session", cal.Path)
	}
}

func TestCalDAVListingIsScopedToPrincipal(t *testing.T) {
	backend, attacker, victim, _ := twoUserBackend(t)

	objs, err := backend.ListCalendarObjects(WithUserID(context.Background(), attacker),
		calPath(victim, "default"), nil)
	if err != nil {
		return // refusing outright is a valid way to be scoped
	}
	for _, o := range objs {
		t.Errorf("attacker listed an object from the victim's calendar: %s", o.Path)
	}
}

// TestCalDAVServesStoredRawICal covers fidelity rather than scoping: a client's
// own ATTENDEE, VALARM, RRULE and X- properties must come back as stored. The
// backend keeps the raw iCal precisely so a round-trip does not silently
// discard everything the proto has no field for.
func TestCalDAVServesStoredRawICal(t *testing.T) {
	backend, userID := newTestBackend(t)
	ctx := WithUserID(context.Background(), userID)
	path := objectPath(userID, "default", "e1")

	cal := testCalendar("e1", "With extras")
	cal.Children[0].Props.SetText("X-CUSTOM-MARKER", "keep-me")

	if _, err := backend.PutCalendarObject(ctx, path, cal, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := backend.GetCalendarObject(ctx, path, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	marker, _ := got.Data.Events()[0].Props.Text("X-CUSTOM-MARKER")
	if marker != "keep-me" {
		t.Errorf("X-CUSTOM-MARKER = %q, want keep-me: the server synthesised the event "+
			"from its proto fields instead of serving the stored iCal, which drops every "+
			"property the proto has no field for", marker)
	}
}
