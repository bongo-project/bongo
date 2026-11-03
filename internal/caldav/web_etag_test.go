package caldav

import (
	"net/http"
	"strings"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// webEvent stores an event the way the web does: no ical_raw.
func (e *tdEnv) webEvent(title string) string {
	e.t.Helper()
	guid, err := e.events.Create(tdOwner, &bongov1.Event{
		Guid: "web1", CalendarId: tdPersonal, Title: title,
		StartTime: 1792058400, EndTime: 1792062000,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return guid
}

func (e *tdEnv) serveETag(path string) string {
	e.t.Helper()
	code, _, h := e.doHdr("GET", path, "", nil)
	if code != http.StatusOK {
		e.t.Fatalf("GET %s = %d", path, code)
	}
	return h.Get("ETag")
}

func TestWebCreatedEventETag(t *testing.T) {
	e := newTdEnv(t)
	guid := e.webEvent("Standup")
	path := tdPath(tdPersonal, guid)

	etag := e.serveETag(path)
	if !wireETag.MatchString(etag) {
		t.Fatalf("GET ETag = %q, want 32 hex in quotes", etag)
	}
	code, body, _ := e.doHdr("PROPFIND", path, `<?xml version="1.0"?><propfind xmlns="DAV:"><prop><getetag/></prop></propfind>`,
		map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	if code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND = %d: %s", code, body)
	}
	m := getETagProp.FindStringSubmatch(body)
	if m == nil || strings.ReplaceAll(m[1], "&#34;", `"`) != etag {
		t.Errorf("getetag = %v, want %q", m, etag)
	}

	// Stable across a second boundary: the served DTSTAMP must not be "now".
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)) + 20*time.Millisecond)
	if again := e.serveETag(path); again != etag {
		t.Errorf("ETag changed with the clock: %q then %q", etag, again)
	}

	// If-Match with the served value works; a stale one does not.
	body1 := vcal(vevent(guid, "Standup"))
	if c := e.put(tdOwner, path, body1, map[string]string{"If-Match": `"` + strings.Repeat("0", 32) + `"`}); c != http.StatusPreconditionFailed {
		t.Errorf("stale If-Match = %d, want 412", c)
	}
	if c := e.put(tdOwner, path, body1, map[string]string{"If-Match": etag}); c < 200 || c > 299 {
		t.Errorf("If-Match with served ETag = %d, want 2xx", c)
	}
}

func TestWebCreatedEventETagChangesWithContent(t *testing.T) {
	e := newTdEnv(t)
	guid := e.webEvent("Standup")
	path := tdPath(tdPersonal, guid)
	before := e.serveETag(path)

	// A same-second update may leave UpdatedAt as it was; the content differs.
	ev, err := e.events.Get(tdOwner, guid)
	if err != nil {
		t.Fatal(err)
	}
	ev.Title = "Retro"
	if err := e.events.Update(tdOwner, guid, ev); err != nil {
		t.Fatal(err)
	}
	if after := e.serveETag(path); after == before {
		t.Errorf("ETag unchanged after a title change: %q", after)
	}
}

func TestMissingObjectOnDefaultCalendarAnswers404(t *testing.T) {
	for _, method := range []string{"GET", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			e := newTdEnv(t)
			code, _ := e.do(tdOwner, method, "/caldav/"+tdOwner+"/calendars/default/missing.ics", "", nil)
			if code != http.StatusNotFound {
				t.Errorf("%s = %d, want 404", method, code)
			}
			if e.app.writes != 0 {
				t.Errorf("%s wrote %d Raft commands, want 0", method, e.app.writes)
			}
		})
	}
}

func TestSharedCalDAVDeleteReadErrorIs500(t *testing.T) {
	e := newShEnv(t)
	// Garbage proto bytes: non-empty, so the read fails without being not-found.
	if err := e.ps.Set([]byte("/user/"+shOwner+"/event/broken"), []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	e.app.writes = 0
	path := sharedPath(shWrite, shCalA)
	if c, _ := e.do(shWrite, "DELETE", path+"broken.ics", "", nil); c != http.StatusInternalServerError {
		t.Errorf("DELETE of an unreadable event = %d, want 500", c)
	}
	if c, _ := e.do(shWrite, "DELETE", path+"nope.ics", "", nil); c != http.StatusNotFound {
		t.Errorf("DELETE missing = %d, want 404", c)
	}
	if c, _ := e.do(shWrite, "DELETE", path+"ev-b.ics", "", nil); c != http.StatusNotFound {
		t.Errorf("DELETE owner event in another calendar = %d, want 404", c)
	}
	if e.app.writes != 0 {
		t.Errorf("wrote %d Raft commands, want 0", e.app.writes)
	}
}
