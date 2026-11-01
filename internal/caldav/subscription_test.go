package caldav

import (
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	subCalID  = "feedCal"
	subGUID   = "ev-feed"
	subSecret = "https://feeds.example.com/s3cret-token/cal.ics?key=hunter2"
)

// subEnv adds a subscribed calendar to the sharing environment: the owner's,
// shared at write level with wendy and at read level with rita, holding one
// feed event. calB is an ordinary calendar of the owner's.
func subEnv(t *testing.T) *shEnv {
	t.Helper()
	e := newShEnv(t)
	if err := e.cals.Update(shOwner, subCalID, &bongov1.Calendar{
		Guid: subCalID, Name: "Feed", Description: "plain description",
		Subscription: &bongov1.CalendarSubscription{SourceUrl: subSecret},
	}); err != nil {
		t.Fatal(err)
	}
	for who, level := range map[string]string{shWrite: store.GrantLevelWrite, shRead: store.GrantLevelRead} {
		if err := e.acl.SetCalendarGrant(shOwner, subCalID, who, level, store.GrantStateAccepted, shOwner, time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.events.Create(shOwner, &bongov1.Event{Guid: subGUID, CalendarId: subCalID, Title: "From the feed", StartTime: 1792058400, EndTime: 1792062000}); err != nil {
		t.Fatal(err)
	}
	e.app.writes = 0
	return e
}

func ownPath(cal, guid string) string {
	return "/caldav/" + shOwner + "/calendars/" + cal + "/" + guid + ".ics"
}

func TestSubscribedCollectionIsReadOnlyOverCalDAV(t *testing.T) {
	put := func(e *shEnv, u, path, uid string) int {
		c, _ := e.do(u, "PUT", path, shICS(uid, "Changed"), map[string]string{"Content-Type": "text/calendar"})
		return c
	}
	sharedObj := func(u, guid string) string { return sharedPath(u, subCalID) + guid + ".ics" }
	cases := []struct {
		name string
		run  func(e *shEnv) int
	}{
		{"owner PUT new object into the subscription", func(e *shEnv) int { return put(e, shOwner, ownPath(subCalID, "new1"), "new1") }},
		{"owner PUT update in the subscription", func(e *shEnv) int { return put(e, shOwner, ownPath(subCalID, subGUID), subGUID) }},
		// The upsert acts by guid: the feed event is reachable through the
		// owner's other collections too.
		{"owner PUT the feed event's guid through another collection", func(e *shEnv) int {
			return put(e, shOwner, ownPath(shCalB, subGUID), subGUID)
		}},
		{"owner PUT the feed event's guid through the default collection", func(e *shEnv) int {
			return put(e, shOwner, ownPath("default", subGUID), subGUID)
		}},
		{"owner DELETE in the subscription", func(e *shEnv) int {
			c, _ := e.do(shOwner, "DELETE", ownPath(subCalID, subGUID), "", nil)
			return c
		}},
		{"owner DELETE the feed event's guid through another collection", func(e *shEnv) int {
			c, _ := e.do(shOwner, "DELETE", ownPath(shCalB, subGUID), "", nil)
			return c
		}},
		{"write grantee PUT new", func(e *shEnv) int { return put(e, shWrite, sharedObj(shWrite, "new2"), "new2") }},
		{"write grantee PUT update", func(e *shEnv) int { return put(e, shWrite, sharedObj(shWrite, subGUID), subGUID) }},
		{"write grantee DELETE", func(e *shEnv) int {
			c, _ := e.do(shWrite, "DELETE", sharedObj(shWrite, subGUID), "", nil)
			return c
		}},
		{"read grantee PUT", func(e *shEnv) int { return put(e, shRead, sharedObj(shRead, subGUID), subGUID) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := subEnv(t)
			if got := tc.run(e); got != 403 {
				t.Fatalf("status = %d, want 403", got)
			}
			if e.app.writes != 0 {
				t.Errorf("a refused request wrote %d commands", e.app.writes)
			}
			if title := e.title(shOwner, subGUID); title != "From the feed" {
				t.Errorf("feed event is now %q", title)
			}
			if _, n, _ := e.events.ListAll(shOwner, 0, 0); n != 3 {
				t.Errorf("owner holds %d events, want the 3 seeded", n)
			}
		})
	}
}

// The negative cases above are only worth anything if the same requests
// succeed against an ordinary calendar.
func TestOrdinaryCalendarStillWritableOverCalDAV(t *testing.T) {
	e := subEnv(t)
	if c, body := e.do(shOwner, "PUT", ownPath(shCalB, "ordinary"), shICS("ordinary", "Fine"), map[string]string{"Content-Type": "text/calendar"}); c != 201 && c != 204 {
		t.Fatalf("PUT into an ordinary calendar = %d: %s", c, body)
	}
	if c, _ := e.do(shOwner, "DELETE", ownPath(shCalB, "ordinary"), "", nil); c != 204 {
		t.Fatalf("DELETE from an ordinary calendar = %d", c)
	}
	if c, _ := e.do(shWrite, "PUT", sharedPath(shWrite, shCalA)+"w1.ics", shICS("w1", "Fine"), map[string]string{"Content-Type": "text/calendar"}); c != 201 {
		t.Fatalf("write grantee PUT into an ordinary shared calendar = %d", c)
	}
}

func TestSubscribedCollectionCanBeRead(t *testing.T) {
	e := subEnv(t)
	if c, body := e.do(shOwner, "GET", ownPath(subCalID, subGUID), "", nil); c != 200 || !strings.Contains(body, "From the feed") {
		t.Fatalf("owner GET = %d", c)
	}
	if c, _ := e.do(shWrite, "GET", sharedPath(shWrite, subCalID)+subGUID+".ics", "", nil); c != 200 {
		t.Fatalf("write grantee GET = %d", c)
	}
	if c, body := e.propfind(shWrite, sharedPath(shWrite, subCalID), "1"); c != 207 || !strings.Contains(body, subGUID) {
		t.Fatalf("PROPFIND = %d", c)
	}
}

// PROPFIND tells clients not to offer edits, for the owner and a write
// grantee alike, and never carries the feed URL.
func TestSubscribedCollectionPrivileges(t *testing.T) {
	e := subEnv(t)
	for _, tc := range []struct{ user, path string }{
		{shOwner, "/caldav/" + shOwner + "/calendars/" + subCalID + "/"},
		{shWrite, sharedPath(shWrite, subCalID)},
	} {
		c, body := e.propfind(tc.user, tc.path, "0")
		if c != 207 {
			t.Fatalf("%s: status %d", tc.user, c)
		}
		if p := privileges(t, body)[tc.path]; !strings.Contains(p, "read") || strings.Contains(p, "write") {
			t.Errorf("%s: privileges %q, want read only", tc.user, p)
		}
		for _, secret := range []string{"s3cret", "hunter2", "feeds.example.com"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s: PROPFIND leaks %q", tc.user, secret)
			}
		}
	}
	// An ordinary own collection keeps write.
	c, body := e.propfind(shOwner, "/caldav/"+shOwner+"/calendars/"+shCalB+"/", "0")
	if c != 207 {
		t.Fatalf("status %d", c)
	}
	if p := privileges(t, body)["/caldav/"+shOwner+"/calendars/"+shCalB+"/"]; !strings.Contains(p, "write") {
		t.Errorf("ordinary collection lost write: %q", p)
	}
}

func TestSubscriptionURLNotInCalDAVListing(t *testing.T) {
	e := subEnv(t)
	for _, u := range []string{shOwner, shWrite} {
		c, body := e.propfind(u, "/caldav/"+u+"/calendars/", "1")
		if c != 207 {
			t.Fatalf("status %d", c)
		}
		for _, secret := range []string{"s3cret", "hunter2", "feeds.example.com"} {
			if strings.Contains(body, secret) {
				t.Fatalf("%s: home listing leaks %q", u, secret)
			}
		}
	}
}
