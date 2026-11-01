package handlers

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const pubOwner = "u-pub"

type pubEnv struct {
	*quotaEnv
	feed   *CalendarPublishHandler
	cal    *CalendarHandler
	router http.Handler
}

func newPubEnv(t *testing.T) *pubEnv {
	t.Helper()
	env := newQuotaEnv(t)
	e := &pubEnv{quotaEnv: env}
	e.feed = NewCalendarPublishHandler(env.events, env.calendars)
	e.cal = NewCalendarHandler(env.events, env.calendars, nil)
	r := chi.NewRouter()
	r.Use(middleware.SecurityHeaders())
	r.Get("/pub/calendar/{owner}/{calendar}/{token}.ics", e.feed.ServeFeed)
	e.router = r
	if _, err := env.calendars.EnsureDefault(pubOwner); err != nil {
		t.Fatal(err)
	}
	return e
}

// mutateAs runs one of the settings mutations as user.
func (e *pubEnv) mutateAs(t *testing.T, user, calID, action string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	handlers := map[string]http.HandlerFunc{
		"publish":    e.cal.PublishCalendar,
		"regenerate": e.cal.RegeneratePublish,
		"unpublish":  e.cal.UnpublishCalendar,
		"password":   e.cal.SetPublishPassword,
	}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withChiParam(withUserContext(req, user, user+"@example.com"), "calendarID", calID)
	w := httptest.NewRecorder()
	handlers[action](w, req)
	return w
}

func (e *pubEnv) token(t *testing.T, calID string) string {
	t.Helper()
	c, err := e.calendars.Get(pubOwner, calID)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(c.PublishToken)
}

func (e *pubEnv) publish(t *testing.T, calID, detail string) string {
	t.Helper()
	if w := e.mutateAs(t, pubOwner, calID, "publish", url.Values{"detail": {detail}}); w.Code != http.StatusOK {
		t.Fatalf("publish %s: %d %s", calID, w.Code, w.Body.String())
	}
	return e.token(t, calID)
}

func (e *pubEnv) get(owner, calID, token, ip string, mod func(*http.Request)) *httptest.ResponseRecorder {
	u := fmt.Sprintf("/pub/calendar/%s/%s/%s.ics", url.PathEscape(owner), url.PathEscape(calID), token)
	req := httptest.NewRequest(http.MethodGet, u, nil)
	if ip != "" {
		req.RemoteAddr = ip + ":4000"
	}
	if mod != nil {
		mod(req)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// snapshot is everything a client sees of a response.
type snapshot struct {
	Code   int
	Header http.Header
	Body   string
}

func snap(w *httptest.ResponseRecorder) snapshot {
	return snapshot{w.Code, w.Header().Clone(), w.Body.String()}
}

func (e *pubEnv) addCalendar(t *testing.T, guid string) {
	t.Helper()
	if _, err := e.calendars.Create(pubOwner, &bongov1.Calendar{Guid: guid, Name: guid}); err != nil {
		t.Fatal(err)
	}
}

func TestFeedUniform404(t *testing.T) {
	e := newPubEnv(t)
	e.addCalendar(t, "other")
	e.addCalendar(t, "unpub")
	tokA := e.publish(t, "default", "full")
	tokB := e.publish(t, "other", "full")
	// "unpub" carries a token but is not published.
	if err := e.calendars.Mutate(pubOwner, "unpub", func(c *bongov1.Calendar) error {
		c.PublishToken = []byte(strings.Repeat("k", 32))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	unpubTok := e.token(t, "unpub")

	cases := []struct {
		name              string
		owner, cal, token string
	}{
		{"wrong token", pubOwner, "default", base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("z", 32)))},
		{"malformed base64", pubOwner, "default", "!!!notbase64!!!"},
		{"short token", pubOwner, "default", "AAAA"},
		{"long token", pubOwner, "default", tokA + tokA},
		{"unpublished calendar", pubOwner, "unpub", unpubTok},
		{"unknown calendar", pubOwner, "nope", tokA},
		{"unknown owner", "u-nobody", "default", tokA},
		{"token of another calendar", pubOwner, "default", tokB},
		{"all-zero token, unknown calendar", pubOwner, "nope", base64.RawURLEncoding.EncodeToString(make([]byte, 32))},
		{"all-zero token, unpublished calendar", pubOwner, "unpub", base64.RawURLEncoding.EncodeToString(make([]byte, 32))},
		{"malformed token, unknown calendar", pubOwner, "nope", "!!!"},
		{"malformed token, unpublished calendar", pubOwner, "unpub", "!!!"},
		{"owner with separator", "u-pub%2Fx", "default", tokA},
	}
	var want snapshot
	for i, tc := range cases {
		w := e.get(tc.owner, tc.cal, tc.token, "", nil)
		got := snap(w)
		if got.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", tc.name, got.Code)
		}
		if i == 0 {
			want = got
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: response differs from baseline:\n got %+v\nwant %+v", tc.name, got, want)
		}
	}

	// The valid token works, then dies on Regenerate and on Unpublish.
	if w := e.get(pubOwner, "default", tokA, "", nil); w.Code != http.StatusOK {
		t.Fatalf("valid token = %d", w.Code)
	}
	if w := e.mutateAs(t, pubOwner, "default", "regenerate", nil); w.Code != http.StatusOK {
		t.Fatalf("regenerate = %d", w.Code)
	}
	if got := snap(e.get(pubOwner, "default", tokA, "", nil)); !reflect.DeepEqual(got, want) {
		t.Errorf("old token after regenerate: %+v, want the uniform 404", got)
	}
	newTok := e.token(t, "default")
	if newTok == tokA {
		t.Fatal("regenerate kept the token")
	}
	if w := e.get(pubOwner, "default", newTok, "", nil); w.Code != http.StatusOK {
		t.Fatalf("new token = %d", w.Code)
	}
	if w := e.mutateAs(t, pubOwner, "default", "unpublish", nil); w.Code != http.StatusOK {
		t.Fatalf("unpublish = %d", w.Code)
	}
	if got := snap(e.get(pubOwner, "default", newTok, "", nil)); !reflect.DeepEqual(got, want) {
		t.Errorf("token after unpublish: %+v, want the uniform 404", got)
	}
	if c, _ := e.calendars.Get(pubOwner, "default"); len(c.PublishToken) != 0 || c.Published {
		t.Errorf("unpublish left published=%v token=%d bytes", c.Published, len(c.PublishToken))
	}
}

const rawFull = "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\nBEGIN:STANDARD\r\nDTSTART:19701025T030000\r\nTZOFFSETFROM:+0200\r\nTZOFFSETTO:+0100\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:Asia/Tokyo\r\nBEGIN:STANDARD\r\nDTSTART:19700101T000000\r\nTZOFFSETFROM:+0900\r\nTZOFFSETTO:+0900\r\nEND:STANDARD\r\nEND:VTIMEZONE\r\n" +
	"BEGIN:VEVENT\r\nUID:meet@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=Europe/Berlin:20261024T101700\r\nDTEND;TZID=Europe/Berlin:20261024T113100\r\n" +
	"SUMMARY:SecretTitle\r\nDESCRIPTION:SecretDesc\r\nLOCATION:SecretRoom\r\nORGANIZER:mailto:boss@example.com\r\nATTENDEE:mailto:a@example.com\r\nX-CUSTOM:SecretX\r\n" +
	"RRULE:FREQ=WEEKLY;COUNT=3\r\nEXDATE;TZID=Europe/Berlin:20261031T101700\r\n" +
	"BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT10M\r\nDESCRIPTION:SecretAlarm\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:priv@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261101T100000Z\r\nDTEND:20261101T110000Z\r\nSUMMARY:PrivateTitle\r\nCLASS:PRIVATE\r\nLOCATION:PrivateRoom\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:free@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261102T100000Z\r\nDTEND:20261102T110000Z\r\nSUMMARY:FreeTitle\r\nTRANSP:TRANSPARENT\r\nEND:VEVENT\r\n" +
	"BEGIN:VEVENT\r\nUID:gone@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART;TZID=Asia/Tokyo:20261103T100000\r\nDTEND;TZID=Asia/Tokyo:20261103T110000\r\nSUMMARY:CancelledTitle\r\nSTATUS:CANCELLED\r\nEND:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func (e *pubEnv) seedEvents(t *testing.T) {
	t.Helper()
	mk := func(ev *bongov1.Event) {
		if _, err := e.events.Create(pubOwner, ev); err != nil {
			t.Fatal(err)
		}
	}
	mk(&bongov1.Event{Guid: "raw1", CalendarId: "default", Title: "SecretTitle", StartTime: 1792058400, EndTime: 1792062000, IcalRaw: []byte(rawFull)})
	// No calendar id: belongs to the default calendar.
	mk(&bongov1.Event{Guid: "web1", Title: "WebTitle", Description: "WebDesc", Location: "WebRoom", StartTime: 1792144800, EndTime: 1792148400,
		Attendees: []*bongov1.Attendee{{Address: "att@example.com"}}, Organizer: "org@example.com"})
	mk(&bongov1.Event{Guid: "elsewhere", CalendarId: "other", Title: "OtherCalTitle", StartTime: 1792231200, EndTime: 1792234800})
}

func TestFeedContent(t *testing.T) {
	// Substrings that must be absent in every mode.
	always := []string{"ATTENDEE", "ORGANIZER", "VALARM", "SecretAlarm", "att@example.com", "boss@example.com", "OtherCalTitle"}
	cases := []struct {
		name    string
		detail  string
		present []string
		absent  []string
	}{
		{"full", "full",
			[]string{"SecretTitle", "SecretDesc", "SecretRoom", "X-CUSTOM:SecretX", "WebTitle", "WebRoom", "FreeTitle", "CancelledTitle", "RRULE:FREQ=WEEKLY", "TZID:Europe/Berlin", "TZID:Asia/Tokyo", "UID:priv@x", "SUMMARY:Busy"},
			[]string{"PrivateTitle", "PrivateRoom", "CLASS:PRIVATE"}},
		{"busy", "busy",
			[]string{"SUMMARY:Busy", "DTSTART", "DTEND", "RRULE:FREQ=WEEKLY", "EXDATE", "TZID:Europe/Berlin", "UID:priv@x", "UID:meet@x", "UID:web1"},
			[]string{"SecretTitle", "SecretDesc", "SecretRoom", "SecretX", "X-CUSTOM", "WebTitle", "WebDesc", "WebRoom", "PrivateTitle", "PrivateRoom",
				"FreeTitle", "UID:free@x", "CancelledTitle", "UID:gone@x", "TZID:Asia/Tokyo", "DESCRIPTION", "LOCATION", "CLASS"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newPubEnv(t)
			e.seedEvents(t)
			tok := e.publish(t, "default", tc.detail)
			w := e.get(pubOwner, "default", tok, "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d", w.Code)
			}
			body := w.Body.String()
			if ct := w.Header().Get("Content-Type"); ct != "text/calendar; charset=utf-8" {
				t.Errorf("Content-Type = %q", ct)
			}
			for _, s := range tc.present {
				if !strings.Contains(body, s) {
					t.Errorf("missing %q in:\n%s", s, body)
				}
			}
			for _, s := range append(append([]string{}, tc.absent...), always...) {
				if strings.Contains(body, s) {
					t.Errorf("leaked %q in:\n%s", s, body)
				}
			}
			if tc.detail == "busy" {
				// Every kept event has only the allow-listed properties.
				if n := strings.Count(body, "SUMMARY:Busy"); n != 3 {
					t.Errorf("busy events = %d, want 3 (meet, priv, web1):\n%s", n, body)
				}
			}
		})
	}
}

func TestFeedDefaultCalendarOnlyEmptyCalendarID(t *testing.T) {
	e := newPubEnv(t)
	e.seedEvents(t)
	e.addCalendar(t, "other")
	tok := e.publish(t, "other", "full")
	body := e.get(pubOwner, "other", tok, "", nil).Body.String()
	if !strings.Contains(body, "OtherCalTitle") || strings.Contains(body, "WebTitle") || strings.Contains(body, "SecretTitle") {
		t.Errorf("non-default calendar feed wrong:\n%s", body)
	}
}

func TestFeedCachingAndConditionalGet(t *testing.T) {
	e := newPubEnv(t)
	e.seedEvents(t)
	tok := e.publish(t, "default", "full")
	w := e.get(pubOwner, "default", tok, "", nil)
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=300" {
		t.Errorf("Cache-Control = %q", cc)
	}
	etag := w.Header().Get("ETag")
	if etag == "" || strings.Contains(etag, tok) {
		t.Fatalf("ETag = %q", etag)
	}
	// Stable across requests: synthesised DTSTAMPs must not use the clock.
	time.Sleep(1100 * time.Millisecond)
	if again := e.get(pubOwner, "default", tok, "", nil); again.Header().Get("ETag") != etag {
		t.Errorf("ETag changed between identical requests")
	}
	w304 := e.get(pubOwner, "default", tok, "", func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if w304.Code != http.StatusNotModified || w304.Body.Len() != 0 {
		t.Errorf("If-None-Match = %d with %d body bytes, want 304 and none", w304.Code, w304.Body.Len())
	}
	if w304.Header().Get("ETag") != etag || w304.Header().Get("Cache-Control") != "private, max-age=300" {
		t.Errorf("304 headers: %v", w304.Header())
	}
	if w := e.get(pubOwner, "default", tok, "", func(r *http.Request) { r.Header.Set("If-None-Match", `"other"`) }); w.Code != http.StatusOK {
		t.Errorf("stale validator = %d, want 200", w.Code)
	}
}

func TestFeedPassword(t *testing.T) {
	e := newPubEnv(t)
	e.seedEvents(t)
	tok := e.publish(t, "default", "full")
	if w := e.mutateAs(t, pubOwner, "default", "password", url.Values{"password": {"hunter2-hunter2"}}); w.Code != http.StatusOK {
		t.Fatalf("set password = %d %s", w.Code, w.Body.String())
	}
	c, _ := e.calendars.Get(pubOwner, "default")
	if bcrypt.CompareHashAndPassword([]byte(c.PublishPasswordHash), []byte("hunter2-hunter2")) != nil {
		t.Fatal("stored hash does not match")
	}
	auth := func(user, pw string) func(*http.Request) {
		return func(r *http.Request) { r.SetBasicAuth(user, pw) }
	}

	w := e.get(pubOwner, "default", tok, "10.0.0.1", nil)
	if w.Code != http.StatusUnauthorized || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic realm=") {
		t.Fatalf("no credentials = %d %v", w.Code, w.Header())
	}
	if strings.Contains(w.Body.String(), "SecretTitle") {
		t.Error("401 carried the feed")
	}
	for _, user := range []string{"", "anything", "someone@example.com"} {
		if w := e.get(pubOwner, "default", tok, "10.0.0.1", auth(user, "hunter2-hunter2")); w.Code != http.StatusOK {
			t.Errorf("user %q with the right password = %d", user, w.Code)
		}
	}
	if w := e.get(pubOwner, "default", tok, "10.0.0.1", auth("u", "wrong-password")); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password = %d", w.Code)
	}
	// A wrong token with credentials stays the uniform 404, never 401.
	if w := e.get(pubOwner, "default", "AAAA", "10.0.0.1", auth("u", "hunter2-hunter2")); w.Code != http.StatusNotFound {
		t.Errorf("wrong token with password = %d, want 404", w.Code)
	}

	// Removing the password opens the feed again.
	if w := e.mutateAs(t, pubOwner, "default", "password", url.Values{"remove_password": {"1"}}); w.Code != http.StatusOK {
		t.Fatalf("remove password = %d", w.Code)
	}
	if w := e.get(pubOwner, "default", tok, "10.0.0.1", nil); w.Code != http.StatusOK {
		t.Errorf("after removal = %d", w.Code)
	}
}

func TestFeedPasswordThrottle(t *testing.T) {
	setup := func(t *testing.T) (*pubEnv, string) {
		e := newPubEnv(t)
		tok := e.publish(t, "default", "full")
		if w := e.mutateAs(t, pubOwner, "default", "password", url.Values{"password": {"hunter2-hunter2"}}); w.Code != http.StatusOK {
			t.Fatal(w.Code)
		}
		return e, tok
	}
	wrong := func(r *http.Request) { r.SetBasicAuth("u", "nope-nope-nope") }
	right := func(r *http.Request) { r.SetBasicAuth("u", "hunter2-hunter2") }

	t.Run("per IP", func(t *testing.T) {
		e, tok := setup(t)
		// Spread the failures over two calendars so neither calendar key
		// reaches its own limit; only the IP key does.
		e.addCalendar(t, "second")
		tok2 := e.publish(t, "second", "full")
		if w := e.mutateAs(t, pubOwner, "second", "password", url.Values{"password": {"hunter2-hunter2"}}); w.Code != http.StatusOK {
			t.Fatal(w.Code)
		}
		for i := range publishMaxFailures {
			cal, t2 := "default", tok
			if i%2 == 1 {
				cal, t2 = "second", tok2
			}
			if w := e.get(pubOwner, cal, t2, "10.0.0.9", wrong); w.Code != http.StatusUnauthorized {
				t.Fatalf("failure = %d", w.Code)
			}
		}
		// The right password from the blocked IP is refused without a check.
		if w := e.get(pubOwner, "default", tok, "10.0.0.9", right); w.Code != http.StatusTooManyRequests {
			t.Errorf("blocked IP = %d, want 429", w.Code)
		}
		if w := e.get(pubOwner, "default", tok, "10.0.0.10", right); w.Code != http.StatusOK {
			t.Errorf("other IP = %d, want 200", w.Code)
		}
	})
	t.Run("per calendar from many IPs", func(t *testing.T) {
		e, tok := setup(t)
		for i := range publishMaxFailures {
			if w := e.get(pubOwner, "default", tok, fmt.Sprintf("10.1.0.%d", i+1), wrong); w.Code != http.StatusUnauthorized {
				t.Fatalf("failure %d = %d", i, w.Code)
			}
		}
		if w := e.get(pubOwner, "default", tok, "10.1.9.9", right); w.Code != http.StatusTooManyRequests {
			t.Errorf("fresh IP on a locked calendar = %d, want 429", w.Code)
		}
	})
	t.Run("other calendar unaffected", func(t *testing.T) {
		e, tok := setup(t)
		e.addCalendar(t, "second")
		tok2 := e.publish(t, "second", "full")
		if w := e.mutateAs(t, pubOwner, "second", "password", url.Values{"password": {"hunter2-hunter2"}}); w.Code != http.StatusOK {
			t.Fatal(w.Code)
		}
		for i := range publishMaxFailures {
			e.get(pubOwner, "default", tok, fmt.Sprintf("10.2.0.%d", i+1), wrong)
		}
		if w := e.get(pubOwner, "second", tok2, "10.2.9.9", right); w.Code != http.StatusOK {
			t.Errorf("second calendar = %d, want 200", w.Code)
		}
	})
	t.Run("missing credentials are not counted", func(t *testing.T) {
		e, tok := setup(t)
		for range 3 * publishMaxFailures {
			if w := e.get(pubOwner, "default", tok, "10.3.0.1", nil); w.Code != http.StatusUnauthorized {
				t.Fatalf("challenge = %d", w.Code)
			}
		}
		if w := e.get(pubOwner, "default", tok, "10.3.0.1", right); w.Code != http.StatusOK {
			t.Errorf("after challenges = %d, want 200", w.Code)
		}
	})
	t.Run("wrong token never counts or answers 429", func(t *testing.T) {
		e, tok := setup(t)
		for range 3 * publishMaxFailures {
			if w := e.get(pubOwner, "default", "AAAA", "10.4.0.1", wrong); w.Code != http.StatusNotFound {
				t.Fatalf("wrong token = %d", w.Code)
			}
		}
		if w := e.get(pubOwner, "default", tok, "10.4.0.1", right); w.Code != http.StatusOK {
			t.Errorf("valid request = %d, want 200", w.Code)
		}
	})
}

func TestPublishPasswordBounds(t *testing.T) {
	e := newPubEnv(t)
	e.publish(t, "default", "full")
	for _, tc := range []struct {
		name string
		pw   string
		code int
	}{
		{"short", "1234567", http.StatusBadRequest},
		{"min", "12345678", http.StatusOK},
		{"max", strings.Repeat("a", 72), http.StatusOK},
		{"too long", strings.Repeat("a", 73), http.StatusBadRequest},
		{"empty keeps", "", http.StatusOK},
	} {
		if w := e.mutateAs(t, pubOwner, "default", "password", url.Values{"password": {tc.pw}}); w.Code != tc.code {
			t.Errorf("%s: %d, want %d", tc.name, w.Code, tc.code)
		}
	}
	c, _ := e.calendars.Get(pubOwner, "default")
	if bcrypt.CompareHashAndPassword([]byte(c.PublishPasswordHash), []byte(strings.Repeat("a", 72))) != nil {
		t.Error("empty password changed the stored hash")
	}
}

// Only the owner may act: a grantee, a stranger and an unknown calendar all
// get 404 and leave the calendar untouched.
func TestPublishMutationsOwnerOnly(t *testing.T) {
	e := newPubEnv(t)
	tok := e.publish(t, "default", "full")
	before, _ := e.calendars.Get(pubOwner, "default")
	for _, action := range []string{"publish", "regenerate", "unpublish", "password"} {
		for _, tc := range []struct{ name, user, cal string }{
			{"other user", "u-intruder", "default"},
			{"owner, unknown calendar", pubOwner, "nope"},
		} {
			w := e.mutateAs(t, tc.user, tc.cal, action, url.Values{"detail": {"busy"}, "password": {"hunter2-hunter2"}, "remove_password": {"1"}})
			if w.Code != http.StatusNotFound {
				t.Errorf("%s as %s: %d, want 404", action, tc.name, w.Code)
			}
		}
	}
	after, _ := e.calendars.Get(pubOwner, "default")
	if !reflect.DeepEqual(before.PublishToken, after.PublishToken) || after.PublishDetail != "full" || !after.Published || after.PublishPasswordHash != "" {
		t.Errorf("calendar changed by a refused request: %+v", after)
	}
	if e.get(pubOwner, "default", tok, "", nil).Code != http.StatusOK {
		t.Error("feed broken by refused requests")
	}
}

func TestPublishMutationValidation(t *testing.T) {
	e := newPubEnv(t)
	if w := e.mutateAs(t, pubOwner, "default", "publish", url.Values{"detail": {"everything"}}); w.Code != http.StatusBadRequest {
		t.Errorf("bad detail = %d", w.Code)
	}
	if w := e.mutateAs(t, pubOwner, "default", "regenerate", nil); w.Code != http.StatusConflict {
		t.Errorf("regenerate when unpublished = %d, want 409", w.Code)
	}
	tok := e.publish(t, "default", "busy")
	if again := e.publish(t, "default", "full"); again != tok {
		t.Error("re-publishing changed the token")
	}
	if c, _ := e.calendars.Get(pubOwner, "default"); c.PublishDetail != "full" {
		t.Errorf("detail = %q", c.PublishDetail)
	}
	// Unpublish also drops the password so a later publish starts open.
	e.mutateAs(t, pubOwner, "default", "password", url.Values{"password": {"hunter2-hunter2"}})
	e.mutateAs(t, pubOwner, "default", "unpublish", nil)
	if c, _ := e.calendars.Get(pubOwner, "default"); c.PublishPasswordHash != "" {
		t.Error("unpublish kept the password hash")
	}
}

// A rename running in parallel with Regenerate must not bring the old token
// back: UpdateCalendar goes through the same lock.
func TestRenameDoesNotRevertRegeneratedToken(t *testing.T) {
	e := newPubEnv(t)
	e.publish(t, "default", "full")
	for i := range 200 {
		before := e.token(t, "default")
		start := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			<-start
			req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(url.Values{"name": {fmt.Sprintf("n%d", i)}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req = withChiParam(withUserContext(req, pubOwner, "e@x"), "calendarID", "default")
			e.cal.UpdateCalendar(httptest.NewRecorder(), req)
		}()
		close(start)
		e.mutateAs(t, pubOwner, "default", "regenerate", nil)
		<-done
		if after := e.token(t, "default"); after == before {
			t.Fatalf("round %d: a concurrent rename wrote the revoked token back", i)
		}
	}
}

func TestPublishTransformDirect(t *testing.T) {
	// Busy form is an allow-list: an unknown property is dropped.
	e := newPubEnv(t)
	raw := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VEVENT\r\nUID:u@x\r\nDTSTAMP:20261001T000000Z\r\nDTSTART:20261101T100000Z\r\nDTEND:20261101T110000Z\r\nSUMMARY:S\r\nURL:http://secret.example/\r\nATTACH:http://secret.example/a\r\nCATEGORIES:Secret\r\nCOMMENT:Secret\r\nCONTACT:Secret\r\nX-FOO:Secret\r\nCLASS:CONFIDENTIAL\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	if _, err := e.events.Create(pubOwner, &bongov1.Event{Guid: "r", CalendarId: "default", StartTime: 1, EndTime: 2, IcalRaw: []byte(raw)}); err != nil {
		t.Fatal(err)
	}
	for _, detail := range []string{"full", "busy"} {
		tok := e.publish(t, "default", detail)
		body := e.get(pubOwner, "default", tok, "", nil).Body.String()
		if strings.Contains(body, "Secret") || strings.Contains(body, "secret.example") || strings.Contains(body, "CLASS") {
			t.Errorf("%s leaked a private event property:\n%s", detail, body)
		}
		if !strings.Contains(body, "SUMMARY:Busy") {
			t.Errorf("%s: confidential event not reduced to Busy:\n%s", detail, body)
		}
	}
}

func TestFeedLinksAndSettings(t *testing.T) {
	https, webcal := PublishLinks("mail.example.com", "u 1", "g/1", []byte(strings.Repeat("a", 32)))
	tok := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	if want := "https://mail.example.com/pub/calendar/u%201/g%2F1/" + tok + ".ics"; https != want {
		t.Errorf("https link = %s", https)
	}
	if !strings.HasPrefix(webcal, "webcal://mail.example.com/") || strings.Contains(tok, ".") || strings.Contains(tok, "=") {
		t.Errorf("webcal link %s, token %s", webcal, tok)
	}
}
