package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	subCal       = "subCal"
	subEvent     = "ev-sub"
	subSecretURL = "https://feeds.example.com/s3cret-token/cal.ics?key=hunter2"
)

// subscribedEnv is the shared environment plus a subscribed calendar of the
// owner's, shared at write level with wendy and at read level with rita, and
// one event in it.
func subscribedEnv(t *testing.T) *sharedEnv {
	t.Helper()
	e := newSharedEnv(t)
	e.mustCal(sharedOwner, &bongov1.Calendar{
		Guid: subCal, Name: "Feed", Color: "#123456",
		Subscription: &bongov1.CalendarSubscription{SourceUrl: subSecretURL},
	})
	for who, level := range map[string]string{actWrite: store.GrantLevelWrite, actRead: store.GrantLevelRead} {
		if err := e.acl.SetCalendarGrant(sharedOwner, subCal, who, level, store.GrantStateAccepted, sharedOwner, time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	e.mustCal(sharedOwner, &bongov1.Calendar{Guid: "default", Name: "Personal"})
	for _, ev := range []*bongov1.Event{
		{Guid: subEvent, CalendarId: subCal, Title: "From the feed", StartTime: sharedEvAWhn, EndTime: sharedEvAWhn + 3600},
		{Guid: "ev-own", CalendarId: "default", Title: "Mine", StartTime: sharedEvAWhn, EndTime: sharedEvAWhn + 3600},
	} {
		if _, err := e.events.Create(sharedOwner, ev); err != nil {
			t.Fatal(err)
		}
	}
	e.app.writes = 0
	return e
}

// subRoute runs one write route against the subscribed calendar as actor. key
// is how that actor names the calendar.
type subRoute struct {
	name string
	run  func(e *sharedEnv, actor, key string) *httptest.ResponseRecorder
}

func subRoutes() []subRoute {
	ownerQ := url.Values{"owner": {sharedOwner}}
	form := func(actor string, f url.Values) url.Values {
		if actor != sharedOwner {
			f.Set("owner", sharedOwner)
		}
		return f
	}
	return []subRoute{
		{"UpdateEvent", func(e *sharedEnv, a, key string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+subEvent, form(a, sEventForm(key))), a, map[string]string{"guid": subEvent}))
			return w
		}},
		{"RescheduleEvent", func(e *sharedEnv, a, _ string) *httptest.ResponseRecorder {
			f := form(a, url.Values{"start_time": {strconv.FormatInt(sharedEvAWhn+7200, 10)}, "end_time": {strconv.FormatInt(sharedEvAWhn+10800, 10)}})
			w := httptest.NewRecorder()
			e.h.RescheduleEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+subEvent+"/reschedule", f), a, map[string]string{"guid": subEvent}))
			return w
		}},
		{"DeleteEvent", func(e *sharedEnv, a, _ string) *httptest.ResponseRecorder {
			q := ""
			if a != sharedOwner {
				q = "?" + ownerQ.Encode()
			}
			w := httptest.NewRecorder()
			e.h.DeleteEvent(w, withRoute(httptest.NewRequest("DELETE", "/calendar/event/"+subEvent+q, nil), a, map[string]string{"guid": subEvent}))
			return w
		}},
		{"UpdateEvent occurrence edit", func(e *sharedEnv, a, key string) *httptest.ResponseRecorder {
			f := form(a, sEventForm(key))
			f.Set("occurrence", "20261014T100000Z")
			f.Set("scope", "this")
			w := httptest.NewRecorder()
			e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+subEvent, f), a, map[string]string{"guid": subEvent}))
			return w
		}},
		{"CreateEvent into the subscription", func(e *sharedEnv, a, key string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.CreateEvent(w, withRoute(sFormReq("POST", "/calendar/event", sEventForm(key)), a, nil))
			return w
		}},
		{"ImportCalendar into the subscription", func(e *sharedEnv, a, key string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.ImportCalendar(w, icsUpload(e.t, a, key))
			return w
		}},
		{"NewEvent targeting the subscription", func(e *sharedEnv, a, key string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			e.h.NewEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/new?calendar_id="+url.QueryEscape(key), nil), a, nil))
			return w
		}},
	}
}

// Every write route is refused for everyone, the owner and a write grantee
// included, and refusing writes nothing.
func TestSubscribedCalendarIsReadOnlyForEveryone(t *testing.T) {
	actors := []struct{ who, key string }{
		{sharedOwner, subCal},
		{actWrite, "shared:alice:" + subCal},
	}
	for _, rt := range subRoutes() {
		for _, a := range actors {
			t.Run(rt.name+"/"+a.who, func(t *testing.T) {
				e := subscribedEnv(t)
				before := e.eventCount(sharedOwner)
				w := rt.run(e, a.who, a.key)
				if w.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403 (body %q)", w.Code, w.Body.String())
				}
				if e.app.writes != 0 {
					t.Errorf("refused request wrote %d commands", e.app.writes)
				}
				if n := e.eventCount(sharedOwner); n != before {
					t.Errorf("events %d -> %d", before, n)
				}
				if got := e.event(sharedOwner, subEvent); got.Title != "From the feed" {
					t.Errorf("event changed: %q", got.Title)
				}
			})
		}
	}
}

// An event of the owner's own calendar cannot be moved into a subscription.
func TestMoveIntoSubscriptionRefused(t *testing.T) {
	for _, tc := range []struct{ who, key, guid string }{
		{sharedOwner, subCal, "ev-own"},
		{actWrite, "shared:alice:" + subCal, sharedEvA}, // calA is shared with wendy at write level
	} {
		t.Run(tc.who, func(t *testing.T) {
			e := subscribedEnv(t)
			f := sEventForm(tc.key)
			if tc.who != sharedOwner {
				f.Set("owner", sharedOwner)
			}
			w := httptest.NewRecorder()
			e.h.UpdateEvent(w, withRoute(sFormReq("POST", "/calendar/event/"+tc.guid, f), tc.who, map[string]string{"guid": tc.guid}))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (%s)", w.Code, w.Body.String())
			}
			if e.app.writes != 0 {
				t.Errorf("wrote %d commands", e.app.writes)
			}
			if got := e.event(sharedOwner, tc.guid); got.Title == "Edited" {
				t.Error("event moved")
			}
		})
	}
}

// A read is still allowed, and the owner is told it is read-only.
func TestSubscribedEventCanBeRead(t *testing.T) {
	e := subscribedEnv(t)
	for _, who := range []string{sharedOwner, actWrite, actRead} {
		w := httptest.NewRecorder()
		q := ""
		if who != sharedOwner {
			q = "?owner=" + sharedOwner
		}
		e.h.ShowEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/"+subEvent+q, nil), who, map[string]string{"guid": subEvent}))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: ShowEvent status = %d", who, w.Code)
		}
		body := w.Body.String()
		if strings.Contains(body, "s3cret") || strings.Contains(body, "hunter2") {
			t.Fatalf("%s: event page leaks the feed URL", who)
		}
		if strings.Contains(body, `hx-delete`) {
			t.Errorf("%s: the page offers delete on a read-only event", who)
		}
	}
	w := httptest.NewRecorder()
	e.h.ShowEvent(w, withRoute(httptest.NewRequest("GET", "/calendar/event/"+subEvent, nil), sharedOwner, map[string]string{"guid": subEvent}))
	if !strings.Contains(w.Body.String(), "comes from a subscribed calendar") {
		t.Error("owner is not told the event is read-only")
	}
}

// Renaming or recolouring a subscription is allowed and keeps its state.
func TestSubscribedCalendarCanBeRenamed(t *testing.T) {
	e := subscribedEnv(t)
	w := httptest.NewRecorder()
	e.h.UpdateCalendar(w, withRoute(sFormReq("PUT", "/calendar/"+subCal, url.Values{"name": {"Holidays"}}), sharedOwner, map[string]string{"calendarID": subCal}))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	c, _ := e.cals.Get(sharedOwner, subCal)
	if c.Name != "Holidays" || c.Subscription.GetSourceUrl() != subSecretURL {
		t.Fatalf("calendar after rename: %+v", c)
	}
}

func subscribe(e *sharedEnv, actor, feed string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.h.SubscribeCalendar(w, withRoute(sFormReq("POST", "/calendar/subscribe", url.Values{"url": {feed}, "name": {"Work feed"}}), actor, nil))
	return w
}

func TestSubscribeCreatesCalendar(t *testing.T) {
	e := newSharedEnv(t)
	w := subscribe(e, actWrite, "webcal://feeds.example.com/s3cret/cal.ics")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	subs, err := e.cals.ListSubscribed(actWrite)
	if err != nil || len(subs) != 1 {
		t.Fatalf("subscriptions = %v, %v", subs, err)
	}
	c := subs[0]
	if c.Name != "Work feed" || c.Subscription.SourceUrl != "https://feeds.example.com/s3cret/cal.ics" {
		t.Fatalf("calendar: %+v", c)
	}
	if c.Subscription.NextRefreshAt != 0 || c.Subscription.LastAttemptAt != 0 {
		t.Fatal("a new subscription must be due at once and nothing fetched in the request")
	}
	// HTMX gets a refresh instead of a redirect.
	req := withRoute(sFormReq("POST", "/calendar/subscribe", url.Values{"url": {"https://other.example.com/x.ics"}}), actWrite, nil)
	req.Header.Set("HX-Request", "true")
	w = httptest.NewRecorder()
	e.h.SubscribeCalendar(w, req)
	if w.Code != http.StatusOK || w.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("htmx subscribe: %d %v", w.Code, w.Header())
	}
	// A blank name falls back to the host.
	subs, _ = e.cals.ListSubscribed(actWrite)
	found := false
	for _, s := range subs {
		if s.Name == "other.example.com" {
			found = true
		}
	}
	if !found {
		t.Error("blank name did not default to the host")
	}
}

// No network happens in the request, and a bad URL writes nothing.
func TestSubscribeRefusals(t *testing.T) {
	bad := []string{
		"", "not a url", "ftp://example.com/a.ics", "file:///etc/passwd",
		"https://user:pw@example.com/a.ics", "https://example.com:22/a.ics",
		"https://example.com:8080/a.ics", "javascript:alert(1)", "https://exa\r\nmple.com/x",
	}
	for _, u := range bad {
		t.Run(fmt.Sprintf("%q", u), func(t *testing.T) {
			e := newSharedEnv(t)
			w := subscribe(e, actWrite, u)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", w.Code)
			}
			if e.app.writes != 0 {
				t.Errorf("wrote %d commands", e.app.writes)
			}
		})
	}
}

func TestSubscribeDuplicateAndLimit(t *testing.T) {
	e := newSharedEnv(t)
	if w := subscribe(e, actWrite, "https://a.example.com/x.ics"); w.Code != http.StatusSeeOther {
		t.Fatalf("first: %d", w.Code)
	}
	before := e.app.writes
	if w := subscribe(e, actWrite, "https://a.example.com/x.ics"); w.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d", w.Code)
	}
	if e.app.writes != before {
		t.Error("duplicate wrote")
	}

	for i := 1; i < store.MaxSubscriptionsPerUser; i++ {
		if w := subscribe(e, actWrite, fmt.Sprintf("https://h%d.example.com/x.ics", i)); w.Code != http.StatusSeeOther {
			t.Fatalf("subscription %d: %d", i+1, w.Code)
		}
	}
	before = e.app.writes
	w := subscribe(e, actWrite, "https://one-too-many.example.com/x.ics")
	if w.Code != http.StatusConflict {
		t.Fatalf("21st subscription: %d, want 409", w.Code)
	}
	if e.app.writes != before {
		t.Error("the refused subscription wrote")
	}
	// The cap is per user.
	if w := subscribe(e, actRead, "https://one-too-many.example.com/x.ics"); w.Code != http.StatusSeeOther {
		t.Fatalf("another user at the same URL: %d", w.Code)
	}
	// Ordinary calendars do not count against it.
	n, _ := e.cals.ListSubscribed(actWrite)
	if len(n) != store.MaxSubscriptionsPerUser {
		t.Fatalf("subscriptions = %d", len(n))
	}
}

// A failing applier proves "no write": the request must fail, not report a
// subscription that was never stored.
func TestSubscribeWriteFailureFailsRequest(t *testing.T) {
	e := newSharedEnv(t)
	failing := &failingApplier{}
	e.h = NewCalendarHandler(e.events, store.NewCalendarStore(e.ps, failing), nil)
	w := subscribe(e, actWrite, "https://a.example.com/x.ics")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestSettingsShowHostNeverURL(t *testing.T) {
	e := subscribedEnv(t)
	for _, who := range []string{sharedOwner, actWrite, actRead} {
		w := httptest.NewRecorder()
		e.h.CalendarSettings(w, withRoute(httptest.NewRequest("GET", "/calendar/settings", nil), who, nil))
		body := w.Body.String()
		for _, secret := range []string{"s3cret-token", "hunter2", "cal.ics"} {
			if strings.Contains(body, secret) {
				t.Fatalf("%s: settings page leaks %q", who, secret)
			}
		}
		if who == sharedOwner && !strings.Contains(body, "feeds.example.com") {
			t.Error("owner does not see the feed host")
		}
	}
}

func TestSettingsShowRefreshState(t *testing.T) {
	e := subscribedEnv(t)
	if err := e.cals.PatchSubscription(sharedOwner, subCal, func(s *bongov1.CalendarSubscription) {
		s.LastSuccessAt = time.Date(2026, 10, 14, 9, 30, 0, 0, time.Local).Unix()
		s.LastError = "fetching feeds.example.com: status 503"
		s.EventCount = 7
	}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.h.CalendarSettings(w, withRoute(httptest.NewRequest("GET", "/calendar/settings", nil), sharedOwner, nil))
	body := w.Body.String()
	for _, want := range []string{"2026-10-14 09:30", "status 503", "Events: 7", "/calendar/" + subCal + "/unsubscribe", "/calendar/subscribe"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
}

// The ICS export and the event pages carry no feed URL.
func TestExportCarriesNoFeedURL(t *testing.T) {
	e := subscribedEnv(t)
	w := httptest.NewRecorder()
	e.h.ExportCalendar(w, withRoute(httptest.NewRequest("GET", "/calendar/export", nil), sharedOwner, nil))
	body := w.Body.String()
	if !strings.Contains(body, "From the feed") {
		t.Fatalf("export lacks the event: %s", body)
	}
	for _, secret := range []string{"s3cret-token", "hunter2", "feeds.example.com"} {
		if strings.Contains(body, secret) {
			t.Fatalf("export leaks %q", secret)
		}
	}
}

func TestUnsubscribe(t *testing.T) {
	e := subscribedEnv(t)
	run := func(actor, cal string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		e.h.UnsubscribeCalendar(w, withRoute(sFormReq("POST", "/calendar/"+cal+"/unsubscribe", url.Values{}), actor, map[string]string{"calendarID": cal}))
		return w
	}
	t.Run("not the owner", func(t *testing.T) {
		before := e.app.writes
		if w := run(actWrite, subCal); w.Code != http.StatusNotFound {
			t.Fatalf("status %d", w.Code)
		}
		if e.app.writes != before {
			t.Error("a non-owner's unsubscribe wrote")
		}
	})
	t.Run("an ordinary calendar", func(t *testing.T) {
		before := e.app.writes
		if w := run(sharedOwner, sharedCalA); w.Code != http.StatusNotFound {
			t.Fatalf("status %d", w.Code)
		}
		if e.app.writes != before {
			t.Error("unsubscribing an ordinary calendar wrote")
		}
		if _, err := e.cals.Get(sharedOwner, sharedCalA); err != nil {
			t.Fatal("ordinary calendar deleted")
		}
	})
	t.Run("the owner", func(t *testing.T) {
		if w := run(sharedOwner, subCal); w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if _, err := e.cals.Get(sharedOwner, subCal); err == nil {
			t.Fatal("calendar still there")
		}
		if _, err := e.events.Get(sharedOwner, subEvent); err == nil {
			t.Fatal("subscription event still there")
		}
		if _, err := e.events.Get(sharedOwner, "ev-own"); err != nil {
			t.Fatal("an event outside the subscription was deleted")
		}
		if lvl, _ := e.acl.CalendarAccessLevel(sharedOwner, subCal, actWrite); lvl != "" {
			t.Fatalf("grant survived: %q", lvl)
		}
	})
}
