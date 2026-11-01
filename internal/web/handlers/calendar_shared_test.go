package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The actors of every sharing test. Each is a user with a valid session.
const (
	actNone      = "mallory" // no grant at all
	actFreeBusy  = "fred"    // freebusy grant
	actPending   = "pat"     // write grant, still pending
	actRead      = "rita"    // read grant
	actWrite     = "wendy"   // write grant
	sharedOwner  = "alice"
	sharedCalA   = "calA" // shared with the actors above
	sharedCalB   = "calB" // shared with nobody
	sharedEvA    = "ev-a" // in calA
	sharedEvB    = "ev-b" // in calB
	sharedEvAWhn = int64(1792058400)
)

// countingApplier counts every write, so "refused" can be proved as "nothing
// was written" and not only as a status code.
type countingApplier struct {
	*directApplierTags
	writes int
}

func (c *countingApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	c.writes++
	return c.directApplierTags.ApplyCommand(cmd, d)
}

type sharedEnv struct {
	t      *testing.T
	h      *CalendarHandler
	ps     *store.PebbleStore
	app    *countingApplier
	events *store.EventStore
	cals   *store.CalendarStore
	acl    *store.ACLStore
	quota  *fakeQuota
}

func newSharedEnv(t *testing.T) *sharedEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &countingApplier{directApplierTags: &directApplierTags{ps: ps}}
	e := &sharedEnv{
		t: t, ps: ps, app: app,
		events: store.NewEventStore(ps, app),
		cals:   store.NewCalendarStore(ps, app),
		acl:    store.NewACLStore(ps, app),
		quota:  &fakeQuota{headroom: map[string]int64{}},
	}
	for _, u := range []string{sharedOwner, actNone, actFreeBusy, actPending, actRead, actWrite} {
		data, _ := proto.Marshal(&bongov1.UserProfile{Email: u + "@d.test"})
		if err := ps.Set([]byte("/user/"+u+"/profile"), data); err != nil {
			t.Fatal(err)
		}
	}
	e.mustCal(sharedOwner, &bongov1.Calendar{Guid: sharedCalA, Name: "Team", Color: "#EF4444"})
	e.mustCal(sharedOwner, &bongov1.Calendar{Guid: sharedCalB, Name: "Private", Color: "#22C55E"})
	e.mustCal(actWrite, &bongov1.Calendar{Guid: "default", Name: "Wendy default"})
	for _, g := range []struct{ who, level, state string }{
		{actFreeBusy, store.GrantLevelFreeBusy, store.GrantStateAccepted},
		{actPending, store.GrantLevelWrite, store.GrantStatePending},
		{actRead, store.GrantLevelRead, store.GrantStateAccepted},
		{actWrite, store.GrantLevelWrite, store.GrantStateAccepted},
	} {
		if err := e.acl.SetCalendarGrant(sharedOwner, sharedCalA, g.who, g.level, g.state, sharedOwner, time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	for _, ev := range []*bongov1.Event{
		{Guid: sharedEvA, CalendarId: sharedCalA, Title: "Team standup", StartTime: sharedEvAWhn, EndTime: sharedEvAWhn + 3600},
		{Guid: sharedEvB, CalendarId: sharedCalB, Title: "Private dentist", StartTime: sharedEvAWhn, EndTime: sharedEvAWhn + 3600},
	} {
		if _, err := e.events.Create(sharedOwner, ev); err != nil {
			t.Fatal(err)
		}
	}
	e.h = NewCalendarHandler(e.events, e.cals, nil)
	e.h.SetACL(e.acl)
	e.h.SetQuotaChecker(e.quota)
	e.h.now = func() time.Time { return time.Unix(sharedEvAWhn, 0).UTC() }
	app.writes = 0
	return e
}

func (e *sharedEnv) mustCal(owner string, c *bongov1.Calendar) {
	e.t.Helper()
	if err := e.cals.Update(owner, c.Guid, c); err != nil {
		e.t.Fatal(err)
	}
}

func (e *sharedEnv) event(owner, guid string) *bongov1.Event {
	e.t.Helper()
	ev, err := e.events.Get(owner, guid)
	if err != nil {
		e.t.Fatalf("event %s/%s: %v", owner, guid, err)
	}
	return ev
}

func (e *sharedEnv) eventCount(owner string) int {
	e.t.Helper()
	_, n, err := e.events.ListAll(owner, 0, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

// withRoute puts the user and chi URL params on a request.
func withRoute(req *http.Request, actor string, params map[string]string) *http.Request {
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	ctx := middleware.WithUserContext(req.Context(), actor, actor+"@d.test")
	return req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
}

func sFormReq(method, target string, form url.Values) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func sEventForm(calendarID string) url.Values {
	return url.Values{
		"title": {"Edited"}, "calendar_id": {calendarID},
		"start_date": {"2026-10-14"}, "start_time": {"10:00"},
		"end_date": {"2026-10-14"}, "end_time": {"11:00"},
	}
}

func icsUpload(t *testing.T, actor, calendarID string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("ics", "x.ics")
	_, _ = fw.Write([]byte(importHead +
		"BEGIN:VEVENT\r\nUID:imp@x\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301105T090000Z\r\nSUMMARY:Imported\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"))
	if calendarID != "" {
		_ = mw.WriteField("calendar_id", calendarID)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/calendar/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return withRoute(req, actor, nil)
}

type sharedRoute struct {
	name string
	run  func(e *sharedEnv, actor string) *httptest.ResponseRecorder
	// want maps each actor to the status; success is the status a write
	// grant gets (and a read grant on a read route).
	want map[string]int
}

func wants(none, fb, pend, read, write int) map[string]int {
	return map[string]int{actNone: none, actFreeBusy: fb, actPending: pend, actRead: read, actWrite: write}
}

func sharedRoutes() []sharedRoute {
	owner := url.Values{"owner": {sharedOwner}}
	return []sharedRoute{
		{"ShowEvent", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.ShowEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/"+sharedEvA+"?owner="+sharedOwner, nil), a, map[string]string{"guid": sharedEvA}))
			return w
		}, wants(404, 404, 404, 200, 200)},
		{"NewEvent shared target", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.NewEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/new?calendar_id="+url.QueryEscape("shared:alice:"+sharedCalA), nil), a, nil))
			return w
		}, wants(404, 404, 404, 403, 200)},
		{"CreateEvent", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.CreateEvent(w, withRoute(sFormReq("POST", "/calendar/event", sEventForm("shared:alice:"+sharedCalA)), a, nil))
			return w
		}, wants(404, 404, 404, 403, 303)},
		{"CreateEvent in unshared calendar", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.CreateEvent(w, withRoute(sFormReq("POST", "/calendar/event", sEventForm("shared:alice:"+sharedCalB)), a, nil))
			return w
		}, wants(404, 404, 404, 404, 404)},
		{"UpdateEvent", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			f := sEventForm("shared:alice:" + sharedCalA)
			f.Set("owner", sharedOwner)
			w := httptest.NewRecorder()
			e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+sharedEvA, f), a, map[string]string{"guid": sharedEvA}))
			return w
		}, wants(404, 404, 404, 403, 303)},
		{"UpdateEvent in unshared calendar", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			f := sEventForm("shared:alice:" + sharedCalB)
			f.Set("owner", sharedOwner)
			w := httptest.NewRecorder()
			e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+sharedEvB, f), a, map[string]string{"guid": sharedEvB}))
			return w
		}, wants(404, 404, 404, 404, 404)},
		{"UpdateEvent moving into unshared calendar", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			f := sEventForm("shared:alice:" + sharedCalB)
			f.Set("owner", sharedOwner)
			w := httptest.NewRecorder()
			e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+sharedEvA, f), a, map[string]string{"guid": sharedEvA}))
			return w
		}, wants(404, 404, 404, 403, 403)},
		{"RescheduleEvent", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			f := url.Values{"owner": {sharedOwner}, "start_time": {strconv.FormatInt(sharedEvAWhn+7200, 10)}, "end_time": {strconv.FormatInt(sharedEvAWhn+10800, 10)}}
			w := httptest.NewRecorder()
			e.h.RescheduleEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+sharedEvA+"/reschedule", f), a, map[string]string{"guid": sharedEvA}))
			return w
		}, wants(404, 404, 404, 403, 200)},
		{"RescheduleEvent in unshared calendar", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			f := url.Values{"owner": {sharedOwner}, "start_time": {"1792065600"}, "end_time": {"1792069200"}}
			w := httptest.NewRecorder()
			e.h.RescheduleEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+sharedEvB+"/reschedule", f), a, map[string]string{"guid": sharedEvB}))
			return w
		}, wants(404, 404, 404, 404, 404)},
		{"DeleteEvent", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.DeleteEvent(w, withRoute(httptest.NewRequest("DELETE", "/calendar/event/"+sharedEvA+"?"+owner.Encode(), nil), a, map[string]string{"guid": sharedEvA}))
			return w
		}, wants(404, 404, 404, 403, 303)},
		{"DeleteEvent in unshared calendar", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.DeleteEvent(w, withRoute(httptest.NewRequest("DELETE", "/calendar/event/"+sharedEvB+"?"+owner.Encode(), nil), a, map[string]string{"guid": sharedEvB}))
			return w
		}, wants(404, 404, 404, 404, 404)},
		{"ImportCalendar", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.ImportCalendar(w, icsUpload(e.t, a, "shared:alice:"+sharedCalA))
			return w
		}, wants(404, 404, 404, 403, 303)},
		{"ImportCalendar into unshared calendar", func(e *sharedEnv, a string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.ImportCalendar(w, icsUpload(e.t, a, "shared:alice:"+sharedCalB))
			return w
		}, wants(404, 404, 404, 404, 404)},
	}
}

// Each route, for a user with no grant, a freebusy grant, a pending grant, a
// read grant and a write grant. Every refusal must also write nothing.
func TestSharedRouteAuthorisation(t *testing.T) {
	for _, rt := range sharedRoutes() {
		for _, actor := range []string{actNone, actFreeBusy, actPending, actRead, actWrite} {
			t.Run(rt.name+"/"+actor, func(t *testing.T) {
				e := newSharedEnv(t)
				before := e.eventCount(sharedOwner)
				w := rt.run(e, actor)
				want := rt.want[actor]
				if w.Code != want {
					t.Fatalf("status = %d, want %d (body %q)", w.Code, want, w.Body.String())
				}
				if want >= 400 {
					if e.app.writes != 0 {
						t.Errorf("refused request wrote %d commands", e.app.writes)
					}
					if n := e.eventCount(sharedOwner); n != before {
						t.Errorf("owner events %d -> %d after refusal", before, n)
					}
				}
			})
		}
	}
}

func TestSharedWriteLandsInOwnerKeySpaceAndChargesOwner(t *testing.T) {
	e := newSharedEnv(t)
	// The actor is out of quota; the owner is not. A write that charged the
	// actor would be refused.
	e.quota.headroom[actWrite] = 0

	w := httptest.NewRecorder()
	e.h.CreateEvent(w, withRoute(sFormReq("POST", "/calendar/event", sEventForm("shared:alice:"+sharedCalA)), actWrite, nil))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d: %s", w.Code, w.Body.String())
	}
	if n := e.eventCount(actWrite); n != 0 {
		t.Errorf("recipient's own key space holds %d events, want 0", n)
	}
	evs, _, _ := e.events.ListAll(sharedOwner, 0, 0)
	var created *bongov1.Event
	for _, ev := range evs {
		if ev.Title == "Edited" {
			created = ev
		}
	}
	if created == nil || created.CalendarId != sharedCalA {
		t.Fatalf("created event = %+v, want one in %s under the owner", created, sharedCalA)
	}

	// The owner is now full: the same write is refused with 507, nothing stored.
	e.quota.headroom[actWrite] = 1 << 30
	e.quota.headroom[sharedOwner] = 0
	before := e.eventCount(sharedOwner)
	w = httptest.NewRecorder()
	e.h.CreateEvent(w, withRoute(sFormReq("POST", "/calendar/event", sEventForm("shared:alice:"+sharedCalA)), actWrite, nil))
	if w.Code != http.StatusInsufficientStorage {
		t.Errorf("create at owner quota = %d, want 507", w.Code)
	}
	w = httptest.NewRecorder()
	e.h.ImportCalendar(w, icsUpload(t, actWrite, "shared:alice:"+sharedCalA))
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Location"), "flash=") {
		t.Errorf("import at owner quota = %d %q", w.Code, w.Header().Get("Location"))
	}
	if n := e.eventCount(sharedOwner); n != before {
		t.Errorf("owner events %d -> %d despite owner quota", before, n)
	}

	// An update that grows the record is charged to the owner too.
	f := sEventForm("shared:alice:" + sharedCalA)
	f.Set("owner", sharedOwner)
	f.Set("description", strings.Repeat("grow ", 200))
	w = httptest.NewRecorder()
	e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+sharedEvA, f), actWrite, map[string]string{"guid": sharedEvA}))
	if w.Code != http.StatusInsufficientStorage {
		t.Errorf("growing update at owner quota = %d, want 507", w.Code)
	}
}

func TestSharedImportLandsUnderOwner(t *testing.T) {
	e := newSharedEnv(t)
	w := httptest.NewRecorder()
	e.h.ImportCalendar(w, icsUpload(t, actWrite, "shared:alice:"+sharedCalA))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	evs, _, _ := e.events.ListAll(sharedOwner, 0, 0)
	found := false
	for _, ev := range evs {
		if ev.Title == "Imported" {
			found = true
			if ev.CalendarId != sharedCalA {
				t.Errorf("imported into %q, want %q", ev.CalendarId, sharedCalA)
			}
		}
	}
	if !found {
		t.Error("imported event not stored under the owner")
	}
	if n := e.eventCount(actWrite); n != 0 {
		t.Errorf("recipient holds %d events, want 0", n)
	}
}

// A write grant on calendar A must not reach the owner's event in calendar B
// by guid, whichever calendar the client claims.
func TestSharedWriteGrantDoesNotReachOtherCalendar(t *testing.T) {
	e := newSharedEnv(t)
	before := proto.Clone(e.event(sharedOwner, sharedEvB))
	for _, claim := range []string{sharedCalA, sharedCalB} {
		f := sEventForm("shared:alice:" + claim)
		f.Set("owner", sharedOwner)
		w := httptest.NewRecorder()
		e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+sharedEvB, f), actWrite, map[string]string{"guid": sharedEvB}))
		if w.Code != http.StatusNotFound {
			t.Errorf("update of ev-b claiming %s = %d, want 404", claim, w.Code)
		}
	}
	w := httptest.NewRecorder()
	e.h.DeleteEvent(w, withRoute(httptest.NewRequest("DELETE", "/calendar/event/"+sharedEvB+"?owner="+sharedOwner, nil), actWrite, map[string]string{"guid": sharedEvB}))
	if w.Code != http.StatusNotFound {
		t.Errorf("delete of ev-b = %d, want 404", w.Code)
	}
	if !proto.Equal(before, e.event(sharedOwner, sharedEvB)) {
		t.Error("ev-b changed")
	}
}

func TestSharedMoveCannotChangeOwner(t *testing.T) {
	e := newSharedEnv(t)
	// The recipient's own event cannot be moved into the shared calendar.
	if _, err := e.events.Create(actWrite, &bongov1.Event{Guid: "mine", CalendarId: "default", Title: "Mine", StartTime: sharedEvAWhn, EndTime: sharedEvAWhn + 60}); err != nil {
		t.Fatal(err)
	}
	e.app.writes = 0
	w := httptest.NewRecorder()
	e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/mine", sEventForm("shared:alice:"+sharedCalA)), actWrite, map[string]string{"guid": "mine"}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("own event into shared calendar = %d, want 400", w.Code)
	}
	// A shared event cannot be moved out to another owner's calendar.
	f := sEventForm("shared:wendy:default")
	f.Set("owner", sharedOwner)
	w = httptest.NewRecorder()
	e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+sharedEvA, f), actWrite, map[string]string{"guid": sharedEvA}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("shared event to another owner = %d, want 400", w.Code)
	}
	if e.app.writes != 0 {
		t.Errorf("refused moves wrote %d commands", e.app.writes)
	}
}

func TestSharedGrantOnDeletedCalendarAuthorisesNothing(t *testing.T) {
	e := newSharedEnv(t)
	// Remove only the calendar record, leaving the grant (the revoke is a
	// separate command and may not have landed).
	if err := e.ps.Delete([]byte("/user/" + sharedOwner + "/calendar/" + sharedCalA)); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.h.ShowEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/"+sharedEvA+"?owner="+sharedOwner, nil), actWrite, map[string]string{"guid": sharedEvA}))
	if w.Code != http.StatusNotFound {
		t.Errorf("show on deleted calendar = %d, want 404", w.Code)
	}
	w = httptest.NewRecorder()
	e.h.CreateEvent(w, withRoute(sFormReq("POST", "/calendar/event", sEventForm("shared:alice:"+sharedCalA)), actWrite, nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("create on deleted calendar = %d, want 404", w.Code)
	}
}

func TestSharedRevokeIsEffectiveOnNextRequest(t *testing.T) {
	e := newSharedEnv(t)
	show := func() int {
		w := httptest.NewRecorder()
		e.h.ShowEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/"+sharedEvA+"?owner="+sharedOwner, nil), actWrite, map[string]string{"guid": sharedEvA}))
		return w.Code
	}
	if c := show(); c != http.StatusOK {
		t.Fatalf("before revoke = %d", c)
	}
	if err := e.acl.RevokeCalendarGrant(sharedOwner, sharedCalA, actWrite); err != nil {
		t.Fatal(err)
	}
	if c := show(); c != http.StatusNotFound {
		t.Errorf("after revoke = %d, want 404", c)
	}
	// Downgrade is as immediate.
	if err := e.acl.SetCalendarGrant(sharedOwner, sharedCalA, actWrite, store.GrantLevelRead, store.GrantStateAccepted, sharedOwner, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.h.DeleteEvent(w, withRoute(httptest.NewRequest("DELETE", "/calendar/event/"+sharedEvA+"?owner="+sharedOwner, nil), actWrite, map[string]string{"guid": sharedEvA}))
	if w.Code != http.StatusForbidden {
		t.Errorf("delete after downgrade to read = %d, want 403", w.Code)
	}
}

// A guessed owner must not let a user reach their own data differently, and a
// path-like owner must not steer the key prefix.
func TestSharedOwnerParameterIsNotTrusted(t *testing.T) {
	e := newSharedEnv(t)
	for _, owner := range []string{"alice/event/x", "../alice", "alice\r\nx", sharedOwner + "x"} {
		w := httptest.NewRecorder()
		e.h.ShowEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/"+sharedEvA+"?owner="+url.QueryEscape(owner), nil), actWrite, map[string]string{"guid": sharedEvA}))
		if w.Code != http.StatusNotFound {
			t.Errorf("owner %q = %d, want 404", owner, w.Code)
		}
	}
	// The actor naming themselves is their own data, no grant needed.
	if _, err := e.events.Create(actNone, &bongov1.Event{Guid: "own", CalendarId: "default", Title: "Own", StartTime: 1, EndTime: 2}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.h.ShowEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/own?owner="+actNone, nil), actNone, map[string]string{"guid": "own"}))
	if w.Code != http.StatusOK {
		t.Errorf("own event with owner=self = %d, want 200", w.Code)
	}
}

func viewBody(t *testing.T, e *sharedEnv, actor, view string) string {
	t.Helper()
	w := httptest.NewRecorder()
	var req *http.Request
	switch view {
	case "month":
		req = withRoute(httptest.NewRequest("GET", "/calendar/2026/10", nil), actor, map[string]string{"year": "2026", "month": "10"})
		e.h.MonthView(w, req)
	case "week":
		req = withRoute(httptest.NewRequest("GET", "/calendar/week/2026/42", nil), actor, map[string]string{"year": "2026", "week": "42"})
		e.h.WeekView(w, req)
	case "day":
		req = withRoute(httptest.NewRequest("GET", "/calendar/day/2026/10/15", nil), actor, map[string]string{"year": "2026", "month": "10", "day": "15"})
		e.h.DayView(w, req)
	case "agenda":
		req = withRoute(httptest.NewRequest("GET", "/calendar/agenda?from=2026-10-12", nil), actor, nil)
		e.h.AgendaView(w, req)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("%s view as %s = %d: %s", view, actor, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestSharedCalendarsInViews(t *testing.T) {
	for _, view := range []string{"month", "week", "day", "agenda"} {
		for _, tc := range []struct {
			actor string
			shown bool
		}{
			{actNone, false}, {actFreeBusy, false}, {actPending, false}, {actRead, true}, {actWrite, true},
		} {
			t.Run(view+"/"+tc.actor, func(t *testing.T) {
				e := newSharedEnv(t)
				body := viewBody(t, e, tc.actor, view)
				if got := strings.Contains(body, "Team standup"); got != tc.shown {
					t.Errorf("shared event shown = %v, want %v", got, tc.shown)
				}
				if strings.Contains(body, "Private dentist") {
					t.Error("event of an unshared calendar is shown")
				}
				if tc.shown {
					for _, want := range []string{
						`data-cal-id="shared:alice:calA"`,
						"cal-colour-red", // the owner's colour
						"owner=alice",    // the link carries the owner
					} {
						if !strings.Contains(body, want) {
							t.Errorf("body lacks %q", want)
						}
					}
					if view == "month" && !strings.Contains(body, "Shared with me") {
						t.Error("month view lacks the Shared with me group")
					}
				}
			})
		}
	}
}

// The recipient's own "default" calendar and the owner's must not cross-wire.
func TestSharedDisplayKeyDoesNotCollideWithOwnCalendar(t *testing.T) {
	e := newSharedEnv(t)
	if err := e.acl.SetCalendarGrant(sharedOwner, "default", actWrite, store.GrantLevelRead, store.GrantStateAccepted, sharedOwner, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	e.mustCal(sharedOwner, &bongov1.Calendar{Guid: "default", Name: "Alice default", Color: "#8B5CF6"})
	if _, err := e.events.Create(sharedOwner, &bongov1.Event{Guid: "ev-d", CalendarId: "default", Title: "Alice default event", StartTime: sharedEvAWhn, EndTime: sharedEvAWhn + 60}); err != nil {
		t.Fatal(err)
	}
	body := viewBody(t, e, actWrite, "day")
	if !strings.Contains(body, `data-cal-id="shared:alice:default"`) {
		t.Error("shared default calendar not keyed by owner")
	}
}

func TestSettingsCardListsGrants(t *testing.T) {
	e := newSharedEnv(t)
	e.h.SetShareResolver(stubResolver{})
	w := httptest.NewRecorder()
	e.h.CalendarSettings(w, withRoute(httptest.NewRequest("GET", "/calendar/settings", nil), sharedOwner, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"wendy@d.test", "rita@d.test", "fred@d.test", `hx-post="/calendar/calA/share"`, `hx-post="/calendar/calA/unshare"`, `name="_csrf"`} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	// The unshare form posts the grantee's user ID.
	if !strings.Contains(body, `name="share_with" value="wendy"`) {
		t.Error("unshare form does not carry the grantee ID")
	}
}

type stubResolver struct{}

func (stubResolver) ResolveAccount(email string) (string, error) {
	return strings.TrimSuffix(email, "@d.test"), nil
}

// sharedWeekly stores a weekly series in the owner's shared calendar and
// returns its guid and the RECURRENCE-ID of its second occurrence.
func sharedWeekly(e *sharedEnv) (guid, rid string) {
	e.t.Helper()
	guid = "ev-weekly"
	_, err := e.events.Create(sharedOwner, &bongov1.Event{
		Guid: guid, CalendarId: sharedCalA, Title: "Weekly sync", Rrule: "FREQ=WEEKLY",
		StartTime: sharedEvAWhn, EndTime: sharedEvAWhn + 3600,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	e.app.writes = 0
	return guid, time.Unix(sharedEvAWhn+7*86400, 0).UTC().Format("20060102T150405Z")
}

func (e *sharedEnv) scopedEdit(actor, guid, rid string) *httptest.ResponseRecorder {
	f := sEventForm(sharedCalA)
	f.Set("owner", sharedOwner)
	f.Set("rrule", "FREQ=WEEKLY")
	f.Set("occurrence", rid)
	f.Set("scope", "this")
	f.Set("title", "Moved once")
	w := httptest.NewRecorder()
	e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+guid, f), actor, map[string]string{"guid": guid}))
	return w
}

func (e *sharedEnv) scopedDelete(actor, guid, rid string) *httptest.ResponseRecorder {
	q := url.Values{"owner": {sharedOwner}, "occurrence": {rid}, "scope": {"this"}}
	w := httptest.NewRecorder()
	e.h.DeleteEvent(w, withRoute(httptest.NewRequest("DELETE", "/calendar/event/"+guid+"?"+q.Encode(), nil), actor, map[string]string{"guid": guid}))
	return w
}

func TestSharedScopedWriteLandsUnderOwner(t *testing.T) {
	e := newSharedEnv(t)
	guid, rid := sharedWeekly(e)

	if w := e.scopedEdit(actWrite, guid, rid); w.Code != http.StatusSeeOther {
		t.Fatalf("scoped edit = %d: %s", w.Code, w.Body.String())
	}
	got := e.event(sharedOwner, guid)
	if !strings.Contains(string(got.IcalRaw), "RECURRENCE-ID") || !strings.Contains(string(got.IcalRaw), "Moved once") {
		t.Errorf("owner's series lacks the override:\n%s", got.IcalRaw)
	}
	if n := e.eventCount(actWrite); n != 0 {
		t.Errorf("grantee's own key space holds %d events", n)
	}

	if w := e.scopedDelete(actWrite, guid, rid); w.Code != http.StatusSeeOther {
		t.Fatalf("scoped delete = %d: %s", w.Code, w.Body.String())
	}
	got = e.event(sharedOwner, guid)
	if !strings.Contains(string(got.IcalRaw), "EXDATE") || strings.Contains(string(got.IcalRaw), "Moved once") {
		t.Errorf("owner's series lacks the exclusion:\n%s", got.IcalRaw)
	}
	if n := e.eventCount(actWrite); n != 0 {
		t.Errorf("grantee's own key space holds %d events", n)
	}
}

func TestSharedScopedWriteNeedsWriteAccess(t *testing.T) {
	for _, actor := range []string{actRead, actPending, actFreeBusy, actNone} {
		t.Run(actor, func(t *testing.T) {
			e := newSharedEnv(t)
			guid, rid := sharedWeekly(e)
			before := e.event(sharedOwner, guid)

			if w := e.scopedEdit(actor, guid, rid); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
				t.Errorf("scoped edit = %d, want 403 or 404", w.Code)
			} else if actor == actRead && w.Code != http.StatusForbidden {
				t.Errorf("read grant: scoped edit = %d, want 403", w.Code)
			}
			if w := e.scopedDelete(actor, guid, rid); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
				t.Errorf("scoped delete = %d, want 403 or 404", w.Code)
			} else if actor == actRead && w.Code != http.StatusForbidden {
				t.Errorf("read grant: scoped delete = %d, want 403", w.Code)
			}
			if e.app.writes != 0 {
				t.Errorf("refused requests wrote %d commands", e.app.writes)
			}
			if after := e.event(sharedOwner, guid); !proto.Equal(before, after) {
				t.Error("the owner's series changed")
			}
		})
	}
}
