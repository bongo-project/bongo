package caldav

import (
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	webdav "github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	shOwner   = "11111111-2222-4333-8444-555555555555" // user IDs are UUIDs
	shCalA    = "calA"
	shCalB    = "calB"
	shNone    = "mallory"
	shFB      = "fred"
	shPending = "pat"
	shRead    = "rita"
	shWrite   = "wendy"
)

// grantApplier applies plain commands and the synthetic grant command.
type grantApplier struct {
	ps     *store.PebbleStore
	writes int
}

func (a *grantApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.writes++
	if store.IsACLSyntheticCommand(cmd.Key) {
		return store.ApplyACLCommand(a.ps, cmd)
	}
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.ps.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.ps.Delete(cmd.Key)
	}
	return nil
}

type shQuota map[string]bool // user -> full

func (q shQuota) RecipientWouldExceedQuota(userID string, _ int64) (bool, error) {
	return q[userID], nil
}

type shEnv struct {
	t      *testing.T
	ps     *store.PebbleStore
	app    *grantApplier
	events *store.EventStore
	cals   *store.CalendarStore
	acl    *store.ACLStore
	quota  shQuota
	srv    *httptest.Server
}

func newShEnv(t *testing.T) *shEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &grantApplier{ps: ps}
	e := &shEnv{
		t: t, ps: ps, app: app,
		events: store.NewEventStore(ps, app),
		cals:   store.NewCalendarStore(ps, app),
		acl:    store.NewACLStore(ps, app),
		quota:  shQuota{},
	}
	for _, c := range []*bongov1.Calendar{{Guid: shCalA, Name: "Team"}, {Guid: shCalB, Name: "Private"}} {
		if err := e.cals.Update(shOwner, c.Guid, c); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := proto.Marshal(&bongov1.UserProfile{Email: "alice@d.test"})
	if err := ps.Set([]byte("/user/"+shOwner+"/profile"), data); err != nil {
		t.Fatal(err)
	}
	for _, g := range []struct{ who, level, state string }{
		{shFB, store.GrantLevelFreeBusy, store.GrantStateAccepted},
		{shPending, store.GrantLevelWrite, store.GrantStatePending},
		{shRead, store.GrantLevelRead, store.GrantStateAccepted},
		{shWrite, store.GrantLevelWrite, store.GrantStateAccepted},
	} {
		if err := e.acl.SetCalendarGrant(shOwner, shCalA, g.who, g.level, g.state, shOwner, time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	for _, ev := range []*bongov1.Event{
		{Guid: "ev-a", CalendarId: shCalA, Title: "Team standup", StartTime: 1792058400, EndTime: 1792062000},
		{Guid: "ev-b", CalendarId: shCalB, Title: "Private dentist", StartTime: 1792058400, EndTime: 1792062000},
	} {
		if _, err := e.events.Create(shOwner, ev); err != nil {
			t.Fatal(err)
		}
	}

	backend := NewBackend(e.events, e.cals, slog.New(slog.NewTextHandler(io.Discard, nil)))
	backend.SetGrants(e.acl)
	backend.SetQuotaChecker(e.quota)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := PrivilegeFilter(&caldav.Handler{Backend: backend, Prefix: "/caldav"}, backend.ReadOnlyShare, logger)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The test's stand-in for basic auth: the user is the header.
		h.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), r.Header.Get("X-Test-User"))))
	}))
	t.Cleanup(e.srv.Close)
	app.writes = 0
	return e
}

func (e *shEnv) do(user, method, path, body string, hdr map[string]string) (int, string) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("X-Test-User", user)
	if method == "PUT" {
		req.Header.Set("Content-Type", "text/calendar")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *shEnv) propfind(user, path, depth string) (int, string) {
	return e.do(user, "PROPFIND", path, `<?xml version="1.0"?><propfind xmlns="DAV:"><prop><displayname/><current-user-privilege-set/></prop></propfind>`,
		map[string]string{"Depth": depth, "Content-Type": "application/xml"})
}

func shICS(uid, summary string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
		"\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301105T090000Z\r\nDTEND:20301105T100000Z\r\nSUMMARY:" + summary + "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
}

func sharedPath(user, cal string) string {
	return "/caldav/" + user + "/calendars/" + sharedSegment(shOwner, cal) + "/"
}

func (e *shEnv) title(owner, guid string) string {
	e.t.Helper()
	ev, err := e.events.Get(owner, guid)
	if err != nil {
		return ""
	}
	return ev.Title
}

func TestParseDAVPath(t *testing.T) {
	seg := sharedSegment(shOwner, "cal-1")
	tests := []struct {
		path               string
		kind               pathKind
		user, calSeg, guid string
		owner, calendar    string
	}{
		{"/caldav/", kindRoot, "", "", "", "", ""},
		{"/caldav", kindRoot, "", "", "", "", ""},
		{"/caldav/u", kindPrincipal, "u", "", "", "", ""},
		{"/caldav/u/", kindPrincipal, "u", "", "", "", ""},
		{"/caldav/u/calendars/", kindHome, "u", "", "", "", ""},
		{"/caldav/u/calendars/x", kindCollection, "u", "x", "", "", "x"},
		{"/caldav/u/calendars/x/", kindCollection, "u", "x", "", "", "x"},
		{"/caldav/u/calendars/x/g.ics", kindObject, "u", "x", "g", "", "x"},
		{"/caldav/u/calendars/" + seg + "/g.ics", kindObject, "u", seg, "g", shOwner, "cal-1"},
		{"/caldav/u/calendars/" + seg, kindCollection, "u", seg, "", shOwner, "cal-1"},
		// Refused.
		{"/caldav/u/calendars/x/g.ics/extra", kindInvalid, "", "", "", "", ""},
		{"/caldav/u/other/x", kindInvalid, "", "", "", "", ""},
		{"/caldav/u/calendars/shared-notauuid-cal", kindInvalid, "", "", "", "", ""},
		{"/caldav/u/calendars/shared-" + shOwner, kindInvalid, "", "", "", "", ""},
		{"/caldav/u/calendars/shared-" + shOwner + "-", kindInvalid, "", "", "", "", ""},
		{"/caldav/u/calendars/shared-" + shOwner + "x" + "cal", kindInvalid, "", "", "", "", ""},
		{"/caldav/u/calendars/../x", kindInvalid, "", "", "", "", ""},
		{"/caldav/u/calendars/x/.ics", kindInvalid, "", "", "", "", ""},
		{"/other/u", kindInvalid, "", "", "", "", ""},
	}
	for _, tt := range tests {
		got := parseDAVPath(tt.path)
		if got.kind != tt.kind || got.user != tt.user || got.calSeg != tt.calSeg || got.guid != tt.guid || got.owner != tt.owner || got.calendar != tt.calendar {
			t.Errorf("parseDAVPath(%q) = %+v", tt.path, got)
		}
	}
}

type shCase struct {
	name string
	run  func(e *shEnv, user string) int
	want map[string]int // by user
}

func shWant(none, fb, pend, read, write int) map[string]int {
	return map[string]int{shNone: none, shFB: fb, shPending: pend, shRead: read, shWrite: write}
}

func TestSharedCalDAVAuthorisation(t *testing.T) {
	evPath := func(u, guid string) string { return sharedPath(u, shCalA) + guid + ".ics" }
	cases := []shCase{
		{"PROPFIND collection", func(e *shEnv, u string) int { c, _ := e.propfind(u, sharedPath(u, shCalA), "1"); return c },
			shWant(404, 404, 404, 207, 207)},
		{"GET object", func(e *shEnv, u string) int { c, _ := e.do(u, "GET", evPath(u, "ev-a"), "", nil); return c },
			shWant(404, 404, 404, 200, 200)},
		{"GET owner event of an unshared calendar", func(e *shEnv, u string) int { c, _ := e.do(u, "GET", evPath(u, "ev-b"), "", nil); return c },
			shWant(404, 404, 404, 404, 404)},
		{"PUT create", func(e *shEnv, u string) int {
			c, _ := e.do(u, "PUT", evPath(u, "new1"), shICS("new1", "New"), map[string]string{"Content-Type": "text/calendar"})
			return c
		}, shWant(404, 404, 404, 403, 201)},
		{"PUT update", func(e *shEnv, u string) int {
			c, _ := e.do(u, "PUT", evPath(u, "ev-a"), shICS("ev-a", "Changed"), map[string]string{"Content-Type": "text/calendar"})
			return c
		}, shWant(404, 404, 404, 403, 201)},
		{"PUT over an owner event of an unshared calendar", func(e *shEnv, u string) int {
			c, _ := e.do(u, "PUT", evPath(u, "ev-b"), shICS("ev-b", "Hijacked"), map[string]string{"Content-Type": "text/calendar"})
			return c
		}, shWant(404, 404, 404, 403, 403)},
		{"DELETE", func(e *shEnv, u string) int { c, _ := e.do(u, "DELETE", evPath(u, "ev-a"), "", nil); return c },
			shWant(404, 404, 404, 403, 204)},
		{"DELETE owner event of an unshared calendar", func(e *shEnv, u string) int { c, _ := e.do(u, "DELETE", evPath(u, "ev-b"), "", nil); return c },
			shWant(404, 404, 404, 403, 404)},
		{"REPORT calendar-query", func(e *shEnv, u string) int {
			c, _ := e.do(u, "REPORT", sharedPath(u, shCalA), `<?xml version="1.0"?><C:calendar-query xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:D="DAV:"><D:prop><D:getetag/></D:prop><C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT"/></C:comp-filter></C:filter></C:calendar-query>`,
				map[string]string{"Depth": "1", "Content-Type": "application/xml"})
			return c
		}, shWant(404, 404, 404, 207, 207)},
		// calB belongs to the same owner and is shared with nobody: a grant on
		// calA must not reach it, whatever the access level.
		{"PROPFIND an unshared calendar of the same owner", func(e *shEnv, u string) int {
			c, _ := e.propfind(u, sharedPath(u, shCalB), "1")
			return c
		}, shWant(404, 404, 404, 404, 404)},
		{"REPORT an unshared calendar of the same owner", func(e *shEnv, u string) int {
			c, _ := e.do(u, "REPORT", sharedPath(u, shCalB), `<?xml version="1.0"?><C:calendar-query xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:D="DAV:"><D:prop><D:getetag/></D:prop><C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT"/></C:comp-filter></C:filter></C:calendar-query>`,
				map[string]string{"Depth": "1", "Content-Type": "application/xml"})
			return c
		}, shWant(404, 404, 404, 404, 404)},
		{"PUT a new object into an unshared calendar of the same owner", func(e *shEnv, u string) int {
			c, _ := e.do(u, "PUT", sharedPath(u, shCalB)+"new2.ics", shICS("new2", "New"), map[string]string{"Content-Type": "text/calendar"})
			return c
		}, shWant(404, 404, 404, 404, 404)},
	}
	for _, tc := range cases {
		for _, user := range []string{shNone, shFB, shPending, shRead, shWrite} {
			t.Run(tc.name+"/"+user, func(t *testing.T) {
				e := newShEnv(t)
				evA, evB := e.title(shOwner, "ev-a"), e.title(shOwner, "ev-b")
				got := tc.run(e, user)
				want := tc.want[user]
				if got != want {
					t.Fatalf("status = %d, want %d", got, want)
				}
				if want >= 400 {
					if e.app.writes != 0 {
						t.Errorf("refused request wrote %d commands", e.app.writes)
					}
					if e.title(shOwner, "ev-a") != evA || e.title(shOwner, "ev-b") != evB {
						t.Error("owner events changed after a refusal")
					}
				}
			})
		}
	}
}

// The per-calendar filter is a confidentiality guard: a collection listing of
// a shared calendar must never include the owner's events from other calendars.
func TestSharedCalDAVListingFiltersToSharedCalendar(t *testing.T) {
	report := `<?xml version="1.0"?><C:calendar-query xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:D="DAV:"><D:prop><D:getetag/><C:calendar-data/></D:prop><C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VEVENT"/></C:comp-filter></C:filter></C:calendar-query>`
	for _, user := range []string{shRead, shWrite} {
		for _, op := range []string{"PROPFIND", "REPORT"} {
			t.Run(op+"/"+user, func(t *testing.T) {
				e := newShEnv(t)
				var code int
				var body string
				if op == "PROPFIND" {
					code, body = e.propfind(user, sharedPath(user, shCalA), "1")
				} else {
					code, body = e.do(user, "REPORT", sharedPath(user, shCalA), report,
						map[string]string{"Depth": "1", "Content-Type": "application/xml"})
				}
				if code != 207 {
					t.Fatalf("status = %d", code)
				}
				if !strings.Contains(body, "ev-a.ics") {
					t.Errorf("shared event missing from listing: %s", body)
				}
				if strings.Contains(body, "ev-b.ics") || strings.Contains(body, "Private dentist") {
					t.Errorf("listing leaks an event of an unshared calendar: %s", body)
				}
			})
		}
	}
}

func TestSharedCalDAVNegativeLeavesObjectIntactWhenRefusedOnBody(t *testing.T) {
	// A read grant holder PUTs the same guid: the stored object is unchanged.
	e := newShEnv(t)
	c, _ := e.do(shRead, "PUT", sharedPath(shRead, shCalA)+"ev-a.ics", shICS("ev-a", "Changed"), nil)
	if c != 403 {
		t.Fatalf("status = %d", c)
	}
	if got := e.title(shOwner, "ev-a"); got != "Team standup" {
		t.Errorf("title = %q after refused PUT", got)
	}
}

func TestSharedCalDAVWritesGoToOwnerAndQuotaToOwner(t *testing.T) {
	e := newShEnv(t)
	e.quota[shWrite] = true // the recipient is full; the owner is not
	c, _ := e.do(shWrite, "PUT", sharedPath(shWrite, shCalA)+"new1.ics", shICS("new1", "From wendy"), nil)
	if c != 201 {
		t.Fatalf("PUT status = %d, want 201", c)
	}
	ev, err := e.events.Get(shOwner, "new1")
	if err != nil || ev.CalendarId != shCalA || ev.Title != "From wendy" {
		t.Fatalf("owner event = %+v, %v", ev, err)
	}
	if _, err := e.events.Get(shWrite, "new1"); err == nil {
		t.Error("event stored in the recipient's key space")
	}

	e.quota[shWrite] = false
	e.quota[shOwner] = true
	c, _ = e.do(shWrite, "PUT", sharedPath(shWrite, shCalA)+"new2.ics", shICS("new2", "Too big"), nil)
	if c != http.StatusInsufficientStorage {
		t.Errorf("PUT at owner quota = %d, want 507", c)
	}
	if _, err := e.events.Get(shOwner, "new2"); err == nil {
		t.Error("event stored despite owner quota")
	}
}

func TestSharedCalDAVRevokeAndDeletedCalendar(t *testing.T) {
	e := newShEnv(t)
	get := func() int { c, _ := e.do(shWrite, "GET", sharedPath(shWrite, shCalA)+"ev-a.ics", "", nil); return c }
	if c := get(); c != 200 {
		t.Fatalf("before revoke = %d", c)
	}
	if err := e.acl.SetCalendarGrant(shOwner, shCalA, shWrite, store.GrantLevelRead, store.GrantStateAccepted, shOwner, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.do(shWrite, "DELETE", sharedPath(shWrite, shCalA)+"ev-a.ics", "", nil); c != 403 {
		t.Errorf("DELETE after downgrade = %d, want 403", c)
	}
	if err := e.acl.RevokeCalendarGrant(shOwner, shCalA, shWrite); err != nil {
		t.Fatal(err)
	}
	if c := get(); c != 404 {
		t.Errorf("after revoke = %d, want 404", c)
	}

	// A grant that outlives its calendar authorises nothing.
	e2 := newShEnv(t)
	if err := e2.ps.Delete([]byte("/user/" + shOwner + "/calendar/" + shCalA)); err != nil {
		t.Fatal(err)
	}
	if c, _ := e2.do(shWrite, "GET", sharedPath(shWrite, shCalA)+"ev-a.ics", "", nil); c != 404 {
		t.Errorf("GET on deleted calendar = %d, want 404", c)
	}
	if c, body := e2.propfind(shWrite, "/caldav/"+shWrite+"/calendars/", "1"); c != 207 || strings.Contains(body, "shared-") {
		t.Errorf("home listing after calendar delete = %d, lists a shared collection: %v", c, strings.Contains(body, "shared-"))
	}
}

func TestSharedCalDAVPathUserIsChecked(t *testing.T) {
	e := newShEnv(t)
	// Someone else's home is never served, with or without a grant.
	for _, p := range []string{
		"/caldav/" + shOwner + "/calendars/" + shCalA + "/",
		"/caldav/" + shOwner + "/calendars/" + shCalA + "/ev-a.ics",
		"/caldav/" + shOwner + "/calendars/" + sharedSegment(shOwner, shCalA) + "/ev-a.ics",
		"/caldav/" + shRead + "/calendars/" + shCalA + "/ev-a.ics", // own home path, but not the user
	} {
		user := shWrite
		if strings.HasPrefix(p, "/caldav/"+shRead) {
			user = shNone
		}
		for _, method := range []string{"GET", "DELETE"} {
			if c, _ := e.do(user, method, p, "", nil); c != 403 && c != 404 {
				t.Errorf("%s %s as %s = %d, want 403 or 404", method, p, user, c)
			}
		}
		if c, _ := e.do(user, "PUT", p, shICS("x", "x"), nil); c != 403 && c != 404 {
			t.Errorf("PUT %s as %s = %d", p, user, c)
		}
	}
	if c, _ := e.propfind(shWrite, "/caldav/"+shOwner+"/calendars/"+shCalA+"/", "1"); c != 403 {
		t.Errorf("PROPFIND on another user's collection = %d, want 403", c)
	}
	// Another user's calendar home is not listed either: a refusal, or at most
	// the caller's own view, never the owner's calendars.
	for _, user := range []string{shWrite, shNone} {
		c, body := e.propfind(user, "/caldav/"+shOwner+"/calendars/", "1")
		if c != 403 && c != 404 && c != 207 {
			t.Errorf("PROPFIND on another user's calendar home as %s = %d", user, c)
		}
		if c == 207 {
			for _, leak := range []string{shCalB, "Private", "ev-b"} {
				if strings.Contains(body, leak) {
					t.Errorf("another user's calendar home leaks %q to %s: %s", leak, user, body)
				}
			}
		}
	}
	// Control: the owner does see those calendars in the same listing, so the
	// absence above means something.
	if c, body := e.propfind(shOwner, "/caldav/"+shOwner+"/calendars/", "1"); c != 207 || !strings.Contains(body, shCalB) {
		t.Errorf("control: the owner's own home lists %d %s", c, body)
	}
	if e.title(shOwner, "ev-a") != "Team standup" || e.title(shOwner, "ev-b") != "Private dentist" {
		t.Error("owner events changed")
	}
	// A recipient cannot reach the owner's own default or other calendars by
	// naming them under their own home either.
	if c, _ := e.do(shWrite, "GET", "/caldav/"+shWrite+"/calendars/"+shCalB+"/ev-b.ics", "", nil); c == 200 {
		t.Error("owner event served through the recipient's own collection path")
	}
}

func TestSharedCalDAVMalformedSharedSegmentsRefused(t *testing.T) {
	e := newShEnv(t)
	for _, seg := range []string{
		"shared-" + shOwner,                // no calendar
		"shared-" + shOwner + "-",          // empty calendar
		"shared-not-a-uuid-calA",           // owner is not a UUID
		"shared-" + shOwner + "x" + shCalA, // separator missing
	} {
		p := "/caldav/" + shWrite + "/calendars/" + seg + "/ev-a.ics"
		if c, _ := e.do(shWrite, "GET", p, "", nil); c != 404 {
			t.Errorf("GET %s = %d, want 404", seg, c)
		}
	}
}

func TestSharedCalDAVHomeListsOnlyReadAndWriteShares(t *testing.T) {
	for _, tc := range []struct {
		user   string
		listed bool
	}{{shNone, false}, {shFB, false}, {shPending, false}, {shRead, true}, {shWrite, true}} {
		e := newShEnv(t)
		c, body := e.propfind(tc.user, "/caldav/"+tc.user+"/calendars/", "1")
		if c != 207 {
			t.Fatalf("%s: status = %d", tc.user, c)
		}
		if got := strings.Contains(body, sharedSegment(shOwner, shCalA)); got != tc.listed {
			t.Errorf("%s: shared collection listed = %v, want %v", tc.user, got, tc.listed)
		}
		if strings.Contains(body, sharedSegment(shOwner, shCalB)) {
			t.Errorf("%s: unshared calendar listed", tc.user)
		}
		if tc.listed && !strings.Contains(body, "Team (alice@d.test)") {
			t.Errorf("%s: display name does not show the owner", tc.user)
		}
	}
}

type msResp struct {
	Href     string `xml:"href"`
	Propstat []struct {
		Prop struct {
			Privs []struct {
				Inner string `xml:",innerxml"`
			} `xml:"current-user-privilege-set>privilege"`
		} `xml:"prop"`
	} `xml:"propstat"`
}

func privileges(t *testing.T, body string) map[string]string {
	t.Helper()
	var ms struct {
		Responses []msResp `xml:"response"`
	}
	if err := xml.Unmarshal([]byte(body), &ms); err != nil {
		t.Fatalf("multistatus does not parse: %v\n%s", err, body)
	}
	out := map[string]string{}
	for _, r := range ms.Responses {
		for _, ps := range r.Propstat {
			for _, p := range ps.Prop.Privs {
				out[r.Href] += p.Inner
			}
		}
	}
	return out
}

func TestSharedCalDAVPrivilegeSet(t *testing.T) {
	e := newShEnv(t)

	// Depth 0 on the shared collection.
	c, body := e.propfind(shRead, sharedPath(shRead, shCalA), "0")
	if c != 207 {
		t.Fatalf("status = %d: %s", c, body)
	}
	p := privileges(t, body)[sharedPath(shRead, shCalA)]
	if !strings.Contains(p, "read") || strings.Contains(p, "write") {
		t.Errorf("read share privileges = %q, want read only", p)
	}
	c, body = e.propfind(shWrite, sharedPath(shWrite, shCalA), "0")
	if c != 207 {
		t.Fatalf("status = %d", c)
	}
	if p := privileges(t, body)[sharedPath(shWrite, shCalA)]; !strings.Contains(p, "read") || !strings.Contains(p, "write") {
		t.Errorf("write share privileges = %q, want read and write", p)
	}

	// Depth 1 on the home set: the read share loses write, own calendars keep it.
	e.cals.Create(shRead, &bongov1.Calendar{Name: "Rita own"})
	c, body = e.propfind(shRead, "/caldav/"+shRead+"/calendars/", "1")
	if c != 207 {
		t.Fatalf("status = %d", c)
	}
	privs := privileges(t, body)
	if len(privs) < 2 {
		t.Fatalf("got %d privilege sets, want own and shared: %v", len(privs), privs)
	}
	for href, p := range privs {
		isShared := strings.Contains(href, "shared-")
		if isShared && strings.Contains(p, "write") {
			t.Errorf("%s: read share reports write: %q", href, p)
		}
		if !isShared && !strings.Contains(p, "write") {
			t.Errorf("%s: own collection lost write: %q", href, p)
		}
	}
}

// The rewrite must not corrupt an answer for the go-webdav client either.
func TestSharedCalDAVRewriteParsesInClient(t *testing.T) {
	e := newShEnv(t)
	client, err := caldav.NewClient(&authClient{user: shRead}, e.srv.URL+"/caldav/")
	if err != nil {
		t.Fatal(err)
	}
	cals, err := client.FindCalendars(t.Context(), "/caldav/"+shRead+"/calendars/")
	if err != nil {
		t.Fatalf("FindCalendars: %v", err)
	}
	found := false
	for _, c := range cals {
		if strings.Contains(c.Path, "shared-") {
			found = true
			if !strings.Contains(c.Name, "Team") {
				t.Errorf("name = %q", c.Name)
			}
		}
	}
	if !found {
		t.Errorf("shared calendar not found by the client: %+v", cals)
	}
}

type authClient struct{ user string }

func (a *authClient) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-Test-User", a.user)
	return http.DefaultClient.Do(req)
}

var _ webdav.HTTPClient = (*authClient)(nil)

func TestRewritePrivilegesKeepsUnrelatedAnswersByteForByte(t *testing.T) {
	in := `<?xml version="1.0"?><multistatus xmlns="DAV:"><response><href>/x/</href><propstat><prop><current-user-privilege-set><privilege><read></read><write></write></privilege></current-user-privilege-set></prop></propstat></response></multistatus>`
	out, err := rewritePrivileges([]byte(in), func(string) bool { return false })
	if err != nil || string(out) != in {
		t.Errorf("unchanged answer was altered: %v\n%s", err, out)
	}
	out, err = rewritePrivileges([]byte(in), func(h string) bool { return h == "/x/" })
	if err != nil || strings.Contains(string(out), "write") || !strings.Contains(string(out), "read") {
		t.Errorf("rewritten = %v\n%s", err, out)
	}
}
