package handlers

import (
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// availEnv is the sharing fixture plus address records, a resource on the
// callers' domain and one on another, and events that must not leak.
type availEnv struct {
	*sharedEnv
	day time.Time // midnight UTC of the day alice's events are on
}

const availSecret = "SECRET-LOCATION-AND-NOTES"

func newAvailEnv(t *testing.T, zone *time.Location) *availEnv {
	t.Helper()
	e := &availEnv{sharedEnv: newSharedEnv(t)}
	as := store.NewAddressStore(e.ps, e.app, nil)
	e.h.SetAddressStore(as)
	e.h.SetProfiles(e.ps)
	e.h.SetZoneSource(fixedZone{zone})

	put := func(addr, user string, verified bool) {
		t.Helper()
		if err := as.Put(addr, &bongov1.AddressRecord{UserId: user, Domain: addr[strings.Index(addr, "@")+1:], Kind: "primary", Verified: verified}); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []string{sharedOwner, actNone, actFreeBusy, actPending, actRead, actWrite} {
		put(u+"@d.test", u, true)
	}
	put("alice.alt@d.test", sharedOwner, false) // unverified send-as record
	for id, mail := range map[string]string{"room1": "room@d.test", "room2": "room@other.test"} {
		data, _ := proto.Marshal(&bongov1.UserProfile{Email: mail, Kind: "resource"})
		if err := e.ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
		put(mail, id, true)
	}
	start := time.Unix(sharedEvAWhn, 0).UTC()
	e.day = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)

	at := func(h, m int) int64 {
		return e.day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).Unix()
	}
	for _, ev := range []*bongov1.Event{
		{Guid: "tr", CalendarId: sharedCalA, Title: "Transparent", StartTime: at(13, 0), EndTime: at(14, 0), Transparent: true},
		{Guid: "ca", CalendarId: sharedCalA, Title: "Cancelled", StartTime: at(15, 0), EndTime: at(16, 0), Status: "CANCELLED"},
		{Guid: "de", CalendarId: sharedCalA, Title: "Declined", StartTime: at(17, 0), EndTime: at(18, 0),
			Attendees: []*bongov1.Attendee{{Address: "alice@d.test", Partstat: "DECLINED"}}},
	} {
		if _, err := e.events.Create(sharedOwner, ev); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{sharedEvA, sharedEvB} {
		ev := e.event(sharedOwner, id)
		ev.Location, ev.Description = availSecret, availSecret
		if err := e.events.Update(sharedOwner, id, ev); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"room1", "room2"} {
		if _, err := e.events.Create(id, &bongov1.Event{Title: "Booked", StartTime: at(9, 0), EndTime: at(9, 30)}); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *availEnv) get(caller string, q url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest("GET", "/calendar/freebusy?"+q.Encode(), nil)
	req = req.WithContext(middleware.WithUserContext(req.Context(), caller, caller+"@d.test"))
	rr := httptest.NewRecorder()
	e.h.FreeBusy(rr, req)
	return rr
}

func (e *availEnv) query(attendees string, extra ...string) url.Values {
	q := url.Values{"attendees": {attendees}, "date": {e.day.Format("2006-01-02")}}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	return q
}

const busyAlice = "Busy 10:00–11:00"

func TestAvailabilityShowsBusyBlocksWithoutTitles(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	tests := []struct {
		name         string
		caller       string
		extra        []string
		wantConflict bool
	}{
		{"self", sharedOwner, []string{"start_time", "12:00", "end_time", "12:30"}, false},
		{"accepted freebusy grantee", actFreeBusy, []string{"start_time", "12:00", "end_time", "12:30"}, false},
		{"proposed time overlaps", actFreeBusy, []string{"start_time", "10:30", "end_time", "11:30"}, true},
		{"all-day proposal overlaps", actFreeBusy, []string{"all_day", "on"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := e.get(tt.caller, e.query("alice@d.test", tt.extra...))
			body := rr.Body.String()
			if rr.Code != 200 || strings.Count(body, busyAlice) != 2 {
				t.Fatalf("status %d, want two %q blocks: %s", rr.Code, busyAlice, body)
			}
			for _, leak := range []string{"Team standup", "Private dentist", availSecret, "Transparent", "Cancelled", "Declined", "13:00", "15:00"} {
				if strings.Contains(body, leak) {
					t.Errorf("body contains %q: %s", leak, body)
				}
			}
			if got := strings.Contains(body, "conflict"); got != tt.wantConflict {
				t.Errorf("conflict marked = %v, want %v", got, tt.wantConflict)
			}
		})
	}
}

// Everyone the caller may not see renders the same as an external address.
func TestAvailabilityRefusalsAreIndistinguishable(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	norm := func(body, addr string) string { return strings.ReplaceAll(body, addr, "ADDR") }

	ref := e.get(actFreeBusy, e.query("someone@elsewhere.test"))
	want := norm(ref.Body.String(), "someone@elsewhere.test")
	if !strings.Contains(want, "Availability not shared") || strings.Contains(want, "Busy") {
		t.Fatalf("external reference: %s", want)
	}
	tests := []struct{ name, caller, addr string }{
		{"no grant", actNone, "alice@d.test"},
		{"pending grant", actPending, "alice@d.test"},
		{"user who shared nothing", actRead, "fred@d.test"},
		{"unverified address of a shared user", actFreeBusy, "alice.alt@d.test"},
		{"unknown local address", actFreeBusy, "nobody@d.test"},
		{"resource on another domain", actFreeBusy, "room@other.test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := e.get(tt.caller, e.query(tt.addr))
			if rr.Code != ref.Code {
				t.Errorf("status %d, want %d", rr.Code, ref.Code)
			}
			if got := norm(rr.Body.String(), tt.addr); got != want {
				t.Errorf("fragment differs from an external address:\n got %s\nwant %s", got, want)
			}
		})
	}
	if vis := e.get(actFreeBusy, e.query("alice@d.test")).Body.String(); norm(vis, "alice@d.test") == want {
		t.Error("an authorised attendee renders like a refused one")
	}
}

// A transparent, cancelled or declined event makes nobody busy, as in the
// JSON lookup (busyIntervals decides for both).
func TestAvailabilityLeavesOutEventsThatAreNotBusy(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	body := e.get(sharedOwner, e.query("alice@d.test")).Body.String()
	for _, at := range []string{"13:00", "15:00", "17:00"} {
		if strings.Contains(body, "Busy "+at) {
			t.Errorf("busy block at %s: %s", at, body)
		}
	}
}

func TestAvailabilityResources(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	rr := e.get(actNone, e.query("room@d.test"))
	if !strings.Contains(rr.Body.String(), "Busy 09:00–09:30") {
		t.Errorf("same-domain resource not shown: %s", rr.Body.String())
	}
}

func TestAvailabilityAllDayInCallersZone(t *testing.T) {
	zone := time.FixedZone("UTC+10", 10*3600)
	e := newAvailEnv(t, zone)
	// An all-day event is stored at UTC midnight of its date.
	if _, err := e.events.Create(sharedOwner, &bongov1.Event{Guid: "ad", CalendarId: sharedCalA, Title: "Holiday", AllDay: true,
		StartTime: e.day.Unix(), EndTime: e.day.AddDate(0, 0, 1).Unix()}); err != nil {
		t.Fatal(err)
	}
	day := func(d time.Time) string {
		return e.get(actFreeBusy, e.query("alice@d.test", "date", d.Format("2006-01-02"))).Body.String()
	}
	if body := day(e.day); !strings.Contains(body, "Busy 00:00–24:00") || strings.Contains(body, "Holiday") {
		t.Errorf("all-day event does not fill its local day: %s", body)
	}
	if body := day(e.day.AddDate(0, 0, -1)); strings.Contains(body, "Busy 00:00–24:00") {
		t.Errorf("all-day event leaks into the previous day: %s", body)
	}
	if body := day(e.day.AddDate(0, 0, 1)); strings.Contains(body, "Busy 00:00–24:00") {
		t.Errorf("all-day event leaks into the next day: %s", body)
	}
}

func TestAvailabilityExcludesTheEditedEventOnly(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	tests := []struct {
		name, caller, event string
		want                int
	}{
		{"no event named", sharedOwner, "", 2},
		{"own event left out", sharedOwner, sharedEvA, 1},
		{"unknown guid excludes nothing", sharedOwner, "nope", 2},
		{"forged guid of another user's event excludes nothing", actFreeBusy, sharedEvA, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := e.query("alice@d.test")
			if tt.event != "" {
				q.Set("event", tt.event)
			}
			body := e.get(tt.caller, q).Body.String()
			if got := strings.Count(body, busyAlice); got != tt.want {
				t.Errorf("%d busy blocks, want %d: %s", got, tt.want, body)
			}
		})
	}
}

func TestAvailabilityEmptyAndCapped(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	if body := e.get(actFreeBusy, e.query("")).Body.String(); strings.TrimSpace(body) != "" {
		t.Errorf("no attendees: %q, want an empty fragment", body)
	}
	if body := e.get(actFreeBusy, e.query("alice@d.test", "date", "not-a-date")).Body.String(); strings.TrimSpace(body) != "" {
		t.Errorf("bad date: %q, want an empty fragment", body)
	}
	var list []string
	for i := range 25 {
		list = append(list, "x"+strconv.Itoa(i)+"@elsewhere.test")
	}
	rr := e.get(actFreeBusy, e.query(strings.Join(list, ", ")))
	rows := strings.Count(rr.Body.String(), `class="availability-row"`)
	if rr.Code != 200 || rows != maxAvailabilityAttendees {
		t.Errorf("status %d, rows %d, want %d", rr.Code, rows, maxAvailabilityAttendees)
	}
}

type countLimiter struct{ left int }

func (c *countLimiter) Allow(string) bool {
	if c.left <= 0 {
		return false
	}
	c.left--
	return true
}
func (c *countLimiter) RetryAfter(string) time.Duration { return 90 * time.Second }

func TestAvailabilityIsRateLimitedPerRequest(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	e.h.SetFreeBusyLimiter(&countLimiter{left: 1})
	if rr := e.get(actFreeBusy, e.query("alice@d.test")); rr.Code != 200 {
		t.Fatalf("first request: %d", rr.Code)
	}
	rr := e.get(actFreeBusy, e.query("anyone@elsewhere.test"))
	if rr.Code != 429 || rr.Header().Get("Retry-After") != "90" {
		t.Errorf("over the limit: %d, Retry-After %q", rr.Code, rr.Header().Get("Retry-After"))
	}
	// The JSON lookup by user ID is not an address lookup and keeps its 403.
	req := httptest.NewRequest("GET", "/calendar/freebusy?user="+sharedOwner+"&start=0&end=9999999999", nil)
	req = req.WithContext(middleware.WithUserContext(req.Context(), actNone, "x@d.test"))
	jr := httptest.NewRecorder()
	e.h.FreeBusy(jr, req)
	if jr.Code != 403 {
		t.Errorf("legacy JSON path: %d, want 403", jr.Code)
	}
}

var busyBlockRe = regexp.MustCompile(`Busy \d\d:\d\d–\d\d:\d\d`)

// busyBlocks is the whole set of blocks a fragment shows, in order.
func busyBlocks(body string) []string { return busyBlockRe.FindAllString(body, -1) }

// A grantee reads the attendee's free/busy through the target's own addresses,
// not their own: an event the attendee declined is not busy for them either.
func TestAvailabilityGranteeDoesNotSeeADeclinedEventAsBusy(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	// Whose addresses are the target's: the declined event names alice@d.test.
	e.h.owned = ownedAddrs{"alice@d.test"}
	want := []string{busyAlice, busyAlice}
	for _, caller := range []string{sharedOwner, actFreeBusy} {
		t.Run(caller, func(t *testing.T) {
			body := e.get(caller, e.query("alice@d.test")).Body.String()
			if got := busyBlocks(body); !slices.Equal(got, want) {
				t.Errorf("blocks = %v, want exactly %v (no transparent, cancelled or declined event): %s", got, want, body)
			}
		})
	}
}

// The meeting being edited is left out of the attendees' rows by its UID: the
// attendee's own copy has another GUID and carries the UID only in ical_raw.
func TestAvailabilityExcludesTheEditedEventByUIDThroughIcalRaw(t *testing.T) {
	e := newAvailEnv(t, time.UTC)
	e.h.owned = ownedAddrs{"alice@d.test"}
	raw := func(uid string) []byte {
		return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//C//EN\r\nBEGIN:VEVENT\r\nUID:" + uid +
			"\r\nDTSTAMP:20300101T000000Z\r\nDTSTART:20300101T200000Z\r\nDTEND:20300101T210000Z\r\nSUMMARY:Sync\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	}
	at := func(h int) int64 { return e.day.Add(time.Duration(h) * time.Hour).Unix() }
	// The organiser's copy (GUID org-copy) and alice's (GUID alice-copy) share
	// a UID; another meeting of alice's has a different one.
	if _, err := e.events.Create(actFreeBusy, &bongov1.Event{Guid: "org-copy", CalendarId: "default", Title: "Sync",
		StartTime: at(20), EndTime: at(21), IcalRaw: raw("meeting-1@d.test")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.events.Create(sharedOwner, &bongov1.Event{Guid: "alice-copy", CalendarId: sharedCalA, Title: "Sync",
		StartTime: at(20), EndTime: at(21), IcalRaw: raw("meeting-1@d.test")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.events.Create(sharedOwner, &bongov1.Event{Guid: "alice-other", CalendarId: sharedCalA, Title: "Other",
		StartTime: at(22), EndTime: at(23), IcalRaw: raw("meeting-2@d.test")}); err != nil {
		t.Fatal(err)
	}
	// alice-copy shows only with no event named; alice-other always does.
	for _, tt := range []struct {
		name, event string
		want        []string
	}{
		{"no event named", "", []string{busyAlice, busyAlice, "Busy 20:00–21:00", "Busy 22:00–23:00"}},
		{"the organiser's event", "org-copy", []string{busyAlice, busyAlice, "Busy 22:00–23:00"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := e.query("alice@d.test")
			if tt.event != "" {
				q.Set("event", tt.event)
			}
			body := e.get(actFreeBusy, q).Body.String()
			if got := busyBlocks(body); !slices.Equal(got, tt.want) {
				t.Errorf("blocks = %v, want %v: %s", got, tt.want, body)
			}
		})
	}
}
