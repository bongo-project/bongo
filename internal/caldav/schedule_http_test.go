package caldav

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const principalPropfind = `<?xml version="1.0" encoding="UTF-8"?>
<A:propfind xmlns:A="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">
 <A:prop>
  <A:current-user-principal/>
  <C:calendar-home-set/>
  <C:schedule-inbox-URL/>
  <C:schedule-outbox-URL/>
  <C:calendar-user-address-set/>
  <A:displayname/>
 </A:prop>
</A:propfind>`

// propstats returns, per HTTP status line, the local names of the properties
// of the first response of a multistatus body.
func propstats(t *testing.T, body string) map[string][]string {
	t.Helper()
	var ms struct {
		Responses []struct {
			Href      string `xml:"href"`
			Propstats []struct {
				Prop struct {
					Inner []struct {
						XMLName xml.Name
						Hrefs   []string `xml:"href"`
					} `xml:",any"`
				} `xml:"prop"`
				Status string `xml:"status"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal([]byte(body), &ms); err != nil {
		t.Fatalf("multistatus does not parse: %v\n%s", err, body)
	}
	out := map[string][]string{}
	if len(ms.Responses) == 0 {
		t.Fatalf("no response in\n%s", body)
	}
	for _, ps := range ms.Responses[0].Propstats {
		for _, p := range ps.Prop.Inner {
			v := p.XMLName.Local
			if len(p.Hrefs) > 0 {
				v += "=" + strings.Join(p.Hrefs, "|")
			}
			out[ps.Status] = append(out[ps.Status], v)
		}
	}
	return out
}

func TestOptionsAdvertisesAutoSchedule(t *testing.T) {
	e := newSchedEnv(t, true)
	for _, path := range []string{"/caldav/", "/caldav/" + schUser + "/", "/caldav/" + schUser + "/calendars/default/", "/caldav/" + schUser + "/inbox/", "/caldav/" + schUser + "/outbox/"} {
		code, _, hdr := e.do(schUser, "OPTIONS", path, "", nil)
		dav := strings.Join(hdr.Values("DAV"), ",")
		if code/100 != 2 || !strings.Contains(dav, scheduleToken) || !strings.Contains(dav, "calendar-access") {
			t.Errorf("OPTIONS %s = %d, DAV %q", path, code, dav)
		}
	}
}

func TestPrincipalReportsSchedulingProperties(t *testing.T) {
	e := newSchedEnv(t, true)
	code, body, _ := e.do(schUser, "PROPFIND", "/caldav/"+schUser+"/", principalPropfind, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	if code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND = %d: %s", code, body)
	}
	got := propstats(t, body)
	ok := strings.Join(got["HTTP/1.1 200 OK"], " ")
	for _, want := range []string{
		"schedule-inbox-URL=/caldav/" + schUser + "/inbox/",
		"schedule-outbox-URL=/caldav/" + schUser + "/outbox/",
		"calendar-user-address-set=mailto:" + schEmail + "|mailto:alice.work@d.test",
		"calendar-home-set",
	} {
		if !strings.Contains(ok, want) {
			t.Errorf("200 propstat lacks %q: %v", want, got)
		}
	}
	if strings.Contains(ok, "sales@") {
		t.Errorf("address set holds an alias: %v", got)
	}
	notFound := strings.Join(got["HTTP/1.1 404 Not Found"], " ")
	if strings.Contains(notFound, "schedule") || strings.Contains(notFound, "address-set") {
		t.Errorf("scheduling properties still listed as missing: %v", got)
	}
	if !strings.Contains(notFound, "displayname") {
		t.Errorf("an unrelated missing property was dropped: %v", got)
	}
}

func TestPrincipalPropfindUntouchedWithoutScheduler(t *testing.T) {
	e := newSchedEnv(t, false)
	_, body, _ := e.do(schUser, "PROPFIND", "/caldav/"+schUser+"/", principalPropfind, map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	got := propstats(t, body)
	if strings.Contains(strings.Join(got["HTTP/1.1 200 OK"], " "), "schedule") {
		t.Errorf("scheduling properties reported without a scheduler: %v", got)
	}
}

func TestOtherUsersCollectionsAreRefused(t *testing.T) {
	e := newSchedEnv(t, true)
	for _, tc := range []struct{ method, path string }{
		{"PROPFIND", "/caldav/bob-id/inbox/"},
		{"PROPFIND", "/caldav/bob-id/outbox/"},
		{"GET", "/caldav/bob-id/inbox/00000000000000000001-0123456789abcdef.ics"},
		{"DELETE", "/caldav/bob-id/inbox/00000000000000000001-0123456789abcdef.ics"},
		{"POST", "/caldav/bob-id/outbox/"},
	} {
		if code, _, _ := e.do(schUser, tc.method, tc.path, "", nil); code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", tc.method, tc.path, code)
		}
	}
}

const itemRequest = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:inv-1\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\n" +
	"ORGANIZER:mailto:zed@elsewhere.test\r\nATTENDEE:mailto:" + schEmail + "\r\nSUMMARY:Invitation\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"

func TestScheduleInboxCollection(t *testing.T) {
	e := newSchedEnv(t, true)
	if err := e.inbox.Place(schUser, "0123456789abcdef", []byte(itemRequest)); err != nil {
		t.Fatal(err)
	}
	if err := e.inbox.Place("bob-id", "fedcba9876543210", []byte(itemRequest)); err != nil {
		t.Fatal(err)
	}
	inbox := "/caldav/" + schUser + "/inbox/"

	// The collection and its one item, and nothing of bob's.
	code, body, _ := e.do(schUser, "PROPFIND", inbox, `<propfind xmlns="DAV:"><prop><resourcetype/><getetag/></prop></propfind>`,
		map[string]string{"Depth": "1"})
	if code != http.StatusMultiStatus || !strings.Contains(body, "schedule-inbox") {
		t.Fatalf("PROPFIND inbox = %d: %s", code, body)
	}
	if strings.Count(body, ".ics</") != 1 {
		t.Errorf("want exactly one item listed:\n%s", body)
	}
	items, _ := e.inbox.List(schUser)
	href := inbox + items[0].Name + ".ics"

	// GET serves the stored text.
	code, got, hdr := e.do(schUser, "GET", href, "", nil)
	if code != http.StatusOK || got != itemRequest || !strings.HasPrefix(hdr.Get("Content-Type"), "text/calendar") || hdr.Get("ETag") == "" {
		t.Errorf("GET = %d %q %v", code, got, hdr)
	}
	// calendar-multiget returns the data; an unknown href is a 404 response.
	code, body, _ = e.do(schUser, "REPORT", inbox, `<C:calendar-multiget xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:D="DAV:"><D:prop><D:getetag/><C:calendar-data/></D:prop>`+
		`<D:href>`+href+`</D:href><D:href>`+inbox+`00000000000000000009-aaaaaaaaaaaaaaaa.ics</D:href></C:calendar-multiget>`, nil)
	if code != http.StatusMultiStatus || !strings.Contains(body, "UID:inv-1") || !strings.Contains(body, "404 Not Found") {
		t.Errorf("multiget = %d: %s", code, body)
	}
	// Another user's item is not reachable by guessing its name.
	otherItems, _ := e.inbox.List("bob-id")
	if code, _, _ := e.do(schUser, "GET", inbox+otherItems[0].Name+".ics", "", nil); code != http.StatusNotFound {
		t.Errorf("GET of another user's item name = %d, want 404", code)
	}
	// DELETE removes the item and only it.
	if code, _, _ := e.do(schUser, "DELETE", href, "", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d", code)
	}
	if left, _ := e.inbox.List(schUser); len(left) != 0 {
		t.Errorf("%d items left", len(left))
	}
	if left, _ := e.inbox.List("bob-id"); len(left) != 1 {
		t.Errorf("bob's inbox has %d items, want 1", len(left))
	}
	if code, _, _ := e.do(schUser, "DELETE", href, "", nil); code != http.StatusNotFound {
		t.Errorf("second DELETE = %d, want 404", code)
	}
	// Names that are not item names never reach the store.
	if code, _, _ := e.do(schUser, "GET", inbox+"..%2Fprofile.ics", "", nil); code != http.StatusNotFound {
		t.Errorf("GET of a crafted name = %d, want 404", code)
	}
	if code, _, _ := e.do(schUser, "PUT", inbox+"x.ics", itemRequest, nil); code != http.StatusMethodNotAllowed {
		t.Errorf("PUT into the inbox = %d, want 405", code)
	}
}

const (
	fbBase = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//EN\r\nMETHOD:%s\r\nBEGIN:%s\r\nUID:fb-1\r\nDTSTAMP:20300101T000000Z\r\n" +
		"DTSTART:20300506T000000Z\r\nDTEND:20300507T000000Z\r\nORGANIZER:mailto:%s\r\n%sEND:%s\r\nEND:VCALENDAR\r\n"
)

func freeBusyPost(method, comp, organiser string, attendees ...string) string {
	var sb strings.Builder
	for _, a := range attendees {
		sb.WriteString("ATTENDEE:mailto:" + a + "\r\n")
	}
	return fmt.Sprintf(fbBase, method, comp, organiser, sb.String(), comp)
}

func TestOutboxFreeBusy(t *testing.T) {
	e := newSchedEnv(t, true)
	// bob is local and visible; carol is local but not shared with alice;
	// nobody@ is not a user. The last two must be indistinguishable.
	e.sched.busy["bob@d.test"] = [][2]int64{
		{1904288400, 1904292000}, // 2030-05-06 09:00-10:00
		{1904290800, 1904295600}, // 09:30-11:00 overlaps the first: one block
		{1904000000, 1904000100}, // before the range: dropped
	}
	outbox := "/caldav/" + schUser + "/outbox/"

	code, body, _ := e.do(schUser, "POST", outbox, freeBusyPost("REQUEST", "VFREEBUSY", schEmail, "bob@d.test", "carol@d.test", "nobody@d.test"),
		map[string]string{"Content-Type": "text/calendar"})
	if code != http.StatusOK || !strings.Contains(body, "schedule-response") {
		t.Fatalf("POST = %d: %s", code, body)
	}
	if n := strings.Count(body, "2.0;Success"); n != 1 {
		t.Errorf("%d successes, want 1 (bob)", n)
	}
	if n := strings.Count(body, "3.7;Invalid calendar user"); n != 2 {
		t.Errorf("%d invalid-user answers, want 2 (carol and nobody look alike)", n)
	}
	if n := strings.Count(body, "FREEBUSY;"); n != 1 || !strings.Contains(body, "FBTYPE=BUSY:20300506T090000Z/20300506T110000Z") {
		t.Errorf("busy periods wrong (want one merged 09:00-11:00 block, none for the period before the range):\n%s", body)
	}
	if !strings.Contains(body, "METHOD:REPLY") || !strings.Contains(body, "UID:fb-1") {
		t.Errorf("reply shape wrong:\n%s", body)
	}
}

func TestOutboxRefusals(t *testing.T) {
	e := newSchedEnv(t, true)
	outbox := "/caldav/" + schUser + "/outbox/"
	hdr := map[string]string{"Content-Type": "text/calendar"}
	many := make([]string, 0, 101)
	for i := range 101 {
		many = append(many, fmt.Sprintf("p%d@d.test", i))
	}
	longRange := strings.Replace(freeBusyPost("REQUEST", "VFREEBUSY", schEmail, "bob@d.test"), "DTEND:20300507T000000Z", "DTEND:20320507T000000Z", 1)
	backwards := strings.Replace(freeBusyPost("REQUEST", "VFREEBUSY", schEmail, "bob@d.test"), "DTEND:20300507T000000Z", "DTEND:20300505T000000Z", 1)
	tests := []struct {
		name string
		body string
		code int
		want string
	}{
		{"an invitation through the outbox", freeBusyPost("REQUEST", "VEVENT", schEmail, "bob@d.test"), 403, "valid-scheduling-message"},
		{"a cancellation through the outbox", freeBusyPost("CANCEL", "VEVENT", schEmail, "bob@d.test"), 403, "valid-scheduling-message"},
		{"a reply through the outbox", freeBusyPost("REPLY", "VEVENT", schEmail, "bob@d.test"), 403, "valid-scheduling-message"},
		{"a free/busy reply through the outbox", freeBusyPost("REPLY", "VFREEBUSY", schEmail, "bob@d.test"), 403, "valid-scheduling-message"},
		{"a free/busy request carrying an invitation too", strings.Replace(freeBusyPost("REQUEST", "VFREEBUSY", schEmail, "bob@d.test"), "END:VCALENDAR",
			"BEGIN:VEVENT\r\nUID:x\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300506T090000Z\r\nEND:VEVENT\r\nEND:VCALENDAR", 1), 403, "valid-scheduling-message"},
		{"a publish", freeBusyPost("PUBLISH", "VFREEBUSY", schEmail, "bob@d.test"), 403, "valid-scheduling-message"},
		{"an organiser the user may not send as", freeBusyPost("REQUEST", "VFREEBUSY", "mallory@d.test", "bob@d.test"), 403, "valid-organizer"},
		{"an alias as organiser", freeBusyPost("REQUEST", "VFREEBUSY", "sales@d.test", "bob@d.test"), 403, "valid-organizer"},
		{"101 recipients", freeBusyPost("REQUEST", "VFREEBUSY", schEmail, many...), 403, "recipient-limit"},
		{"a range over a year", longRange, 403, "valid-scheduling-message"},
		{"an end before the start", backwards, 403, "valid-scheduling-message"},
		{"no recipient", freeBusyPost("REQUEST", "VFREEBUSY", schEmail), 403, "valid-scheduling-message"},
		{"not iCalendar", "hello", 400, "valid-calendar-data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body, _ := e.do(schUser, "POST", outbox, tt.body, hdr)
			if code != tt.code || !strings.Contains(body, tt.want) {
				t.Errorf("POST = %d %q, want %d containing %q", code, body, tt.code, tt.want)
			}
		})
	}
	// 100 recipients is the last one accepted.
	if code, body, _ := e.do(schUser, "POST", outbox, freeBusyPost("REQUEST", "VFREEBUSY", schEmail, many[:100]...), hdr); code != http.StatusOK {
		t.Errorf("100 recipients = %d: %s", code, body)
	}
	if o, r := e.sched.counts(); o != 0 || r != 0 {
		t.Errorf("the outbox sent iTIP: organise=%d reply=%d", o, r)
	}
}

func TestParseSchedulePath(t *testing.T) {
	tests := []struct {
		path string
		kind scheduleKind
		item string
	}{
		{"/caldav/u/inbox/", scheduleInbox, ""},
		{"/caldav/u/inbox", scheduleInbox, ""},
		{"/caldav/u/outbox/", scheduleOutbox, ""},
		{"/caldav/u/inbox/abc.ics", scheduleInboxItem, "abc"},
		{"/caldav/u/inbox/abc", scheduleNone, ""},
		{"/caldav/u/outbox/abc.ics", scheduleNone, ""},
		{"/caldav/u/inbox/a/b.ics", scheduleNone, ""},
		{"/caldav/u/calendars/inbox/", scheduleNone, ""},
		{"/caldav/inbox/", scheduleNone, ""},
		{"/caldav//inbox/", scheduleNone, ""},
		{"/other/u/inbox/", scheduleNone, ""},
	}
	for _, tt := range tests {
		if got := parseSchedulePath(tt.path); got.kind != tt.kind || got.item != tt.item {
			t.Errorf("parseSchedulePath(%q) = %+v, want kind %d item %q", tt.path, got, tt.kind, tt.item)
		}
	}
}
