package caldav

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emersion/go-webdav/caldav"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	mkUser  = "11111111-1111-4111-8111-111111111111"
	mkOther = "22222222-2222-4222-8222-222222222222"
)

type mkEnv struct {
	t    *testing.T
	cals *store.CalendarStore
	srv  *httptest.Server
}

func newMkEnv(t *testing.T, withTasks bool) *mkEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &grantApplier{ps: ps}
	cals := store.NewCalendarStore(ps, app)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := NewBackend(store.NewEventStore(ps, app), cals, logger)
	if withTasks {
		b.SetTaskStore(store.NewTaskStore(ps, app))
	}
	h := MkcalendarHandler(PrivilegeFilter(&caldav.Handler{Backend: b, Prefix: "/caldav"}, b.ReadOnlyShare, logger), b, logger)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), r.Header.Get("X-Test-User"))))
	}))
	t.Cleanup(srv.Close)
	return &mkEnv{t: t, cals: cals, srv: srv}
}

func (e *mkEnv) do(user, method, path, body string) (int, string, http.Header) {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("X-Test-User", user)
	switch method {
	case "PUT":
		req.Header.Set("Content-Type", "text/calendar")
	case "PROPFIND":
		req.Header.Set("Depth", "0")
		req.Header.Set("Content-Type", "application/xml")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func mkPath(seg string) string { return "/caldav/" + mkUser + "/calendars/" + seg + "/" }

func mkcalBody(name string, comps ...string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><C:mkcalendar xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:A="http://apple.com/ns/ical/"><D:set><D:prop>`)
	sb.WriteString(`<D:displayname>` + name + `</D:displayname><C:calendar-description>Things</C:calendar-description><A:calendar-color>#FF0000FF</A:calendar-color><A:calendar-order>3</A:calendar-order>`)
	if comps != nil {
		sb.WriteString(`<C:supported-calendar-component-set>`)
		for _, c := range comps {
			if c != "" {
				sb.WriteString(`<C:comp name="` + c + `"/>`)
			}
		}
		sb.WriteString(`</C:supported-calendar-component-set>`)
	}
	sb.WriteString(`</D:prop></D:set></C:mkcalendar>`)
	return sb.String()
}

func mkcolBody(name string, comps ...string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?><D:mkcol xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:set><D:prop><D:resourcetype><D:collection/><C:calendar/></D:resourcetype>`)
	sb.WriteString(`<D:displayname>` + name + `</D:displayname><C:calendar-description>Things</C:calendar-description><C:supported-calendar-component-set>`)
	for _, c := range comps {
		sb.WriteString(`<C:comp name="` + c + `"/>`)
	}
	sb.WriteString(`</C:supported-calendar-component-set></D:prop></D:set></D:mkcol>`)
	return sb.String()
}

func (e *mkEnv) stored(seg string) (name, desc string, comps []string) {
	e.t.Helper()
	cal, err := e.cals.Get(mkUser, seg)
	if err != nil {
		e.t.Fatalf("calendar %q not stored: %v", seg, err)
	}
	return cal.Name, cal.Description, store.CalendarComponents(cal)
}

func TestMkcalendarGroceriesEndToEnd(t *testing.T) {
	e := newMkEnv(t, true)
	code, body, hdr := e.do(mkUser, "MKCALENDAR", mkPath("groceries"), mkcalBody("Groceries", "VTODO"))
	if code != http.StatusCreated || body != "" {
		t.Fatalf("MKCALENDAR = %d %q", code, body)
	}
	if got := hdr.Get("Location"); got != mkPath("groceries") {
		t.Errorf("Location = %q", got)
	}
	code, body, _ = e.do(mkUser, "PROPFIND", mkPath("groceries"), `<?xml version="1.0"?><D:propfind xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:displayname/><C:supported-calendar-component-set/></D:prop></D:propfind>`)
	if code != http.StatusMultiStatus || !strings.Contains(body, "Groceries") || !strings.Contains(body, `name="VTODO"`) || strings.Contains(body, `name="VEVENT"`) {
		t.Fatalf("PROPFIND = %d: %s", code, body)
	}
	todo := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\nBEGIN:VTODO\r\nUID:milk\r\nDTSTAMP:20301101T000000Z\r\nSUMMARY:Milk\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	if code, body, _ = e.do(mkUser, "PUT", mkPath("groceries")+"x.ics", todo); code != http.StatusCreated {
		t.Fatalf("VTODO PUT = %d: %s", code, body)
	}
	if _, desc, _ := e.stored("groceries"); desc != "Things" {
		t.Errorf("description = %q", desc)
	}
	// A client's first write must not leave the user with only this calendar.
	for _, g := range []string{"default", "tasks"} {
		e.stored(g)
	}
}

func TestMkcalendarComponents(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
		want   []string
	}{
		{"empty body", "", 201, []string{"VEVENT"}},
		{"no set", mkcalBody("X"), 201, []string{"VEVENT"}},
		{"both", mkcalBody("X", "VEVENT", "VTODO"), 201, []string{"VEVENT", "VTODO"}},
		{"lower case and duplicates", mkcalBody("X", "vtodo", "VTODO", "vevent"), 201, []string{"VTODO", "VEVENT"}},
		{"journal dropped", mkcalBody("X", "VJOURNAL", "VTODO"), 201, []string{"VTODO"}},
		{"journal only", mkcalBody("X", "VJOURNAL"), 403, nil},
		{"empty set", mkcalBody("X", ""), 403, nil},
		{"malformed", `<C:mkcalendar`, 400, nil},
		{"wrong root", mkcolBody("X", "VTODO"), 400, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newMkEnv(t, true)
			code, body, _ := e.do(mkUser, "MKCALENDAR", mkPath("lst"), tt.body)
			if code != tt.status {
				t.Fatalf("MKCALENDAR = %d: %s", code, body)
			}
			if tt.status == 403 && !strings.Contains(body, "supported-calendar-component") {
				t.Errorf("403 lacks the precondition: %s", body)
			}
			if tt.status != 201 {
				if _, err := e.cals.Get(mkUser, "lst"); err == nil {
					t.Error("a refused request left a calendar behind")
				}
				return
			}
			_, _, got := e.stored("lst")
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("components = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMkcalendarTodoOnlyWithoutTaskStore(t *testing.T) {
	e := newMkEnv(t, false)
	if code, body, _ := e.do(mkUser, "MKCALENDAR", mkPath("lst"), mkcalBody("X", "VTODO")); code != 403 || !strings.Contains(body, "supported-calendar-component") {
		t.Fatalf("VTODO-only without a task store = %d: %s", code, body)
	}
	if code, _, _ := e.do(mkUser, "MKCALENDAR", mkPath("lst"), mkcalBody("X", "VEVENT", "VTODO")); code != 201 {
		t.Fatalf("both without a task store = %d", code)
	}
}

func TestMkcalendarExistingIsNotOverwritten(t *testing.T) {
	e := newMkEnv(t, true)
	if code, _, _ := e.do(mkUser, "MKCALENDAR", mkPath("groceries"), mkcalBody("Groceries", "VTODO")); code != 201 {
		t.Fatal(code)
	}
	if err := e.cals.Mutate(mkUser, "groceries", func(c *bongov1.Calendar) error { c.PublishToken = []byte("tok"); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, seg := range []string{"groceries", "default", "tasks"} {
		code, body, _ := e.do(mkUser, "MKCALENDAR", mkPath(seg), mkcalBody("Replaced", "VEVENT"))
		if code != http.StatusMethodNotAllowed || !strings.Contains(body, "resource-must-be-null") {
			t.Errorf("MKCALENDAR over %s = %d: %s", seg, code, body)
		}
		if code, _, _ := e.do(mkUser, "MKCOL", mkPath(seg), mkcolBody("Replaced", "VEVENT")); code != http.StatusMethodNotAllowed {
			t.Errorf("MKCOL over %s = %d", seg, code)
		}
	}
	cal, err := e.cals.Get(mkUser, "groceries")
	if err != nil || cal.Name != "Groceries" || string(cal.PublishToken) != "tok" {
		t.Errorf("existing calendar was overwritten: %+v %v", cal, err)
	}
}

func TestMkcalendarRefusals(t *testing.T) {
	tests := []struct {
		name, path string
	}{
		{"another user", "/caldav/" + mkOther + "/calendars/groceries/"},
		{"shared", mkPath("shared-" + mkOther + "-x")},
		{"shared prefix only", mkPath("shared-foo")},
		{"space", mkPath("a%20b")},
		{"ics", mkPath("x.ics")},
		{"dot first", mkPath(".hidden")},
		{"sub prefix", mkPath("sub-abc")},
		{"too long", mkPath(strings.Repeat("a", 129))},
	}
	for _, tt := range tests {
		for _, method := range []string{"MKCALENDAR", "MKCOL"} {
			t.Run(tt.name+" "+method, func(t *testing.T) {
				e := newMkEnv(t, true)
				body := mkcalBody("X", "VTODO")
				if method == "MKCOL" {
					body = mkcolBody("X", "VTODO")
				}
				// The same request on a valid path succeeds, so a 403 here is
				// the path's doing.
				if code, b, _ := e.do(mkUser, method, mkPath("fine"), body); code != 201 {
					t.Fatalf("control %s = %d: %s", method, code, b)
				}
				if code, _, _ := e.do(mkUser, method, tt.path, body); code != http.StatusForbidden {
					t.Errorf("%s %s = %d, want 403", method, tt.path, code)
				}
			})
		}
	}
}

func TestMkcalendarOutsideCollectionPath(t *testing.T) {
	e := newMkEnv(t, true)
	code, body, _ := e.do(mkUser, "MKCALENDAR", "/caldav/"+mkUser+"/calendars/", mkcalBody("X"))
	if code != http.StatusForbidden || !strings.Contains(body, "calendar-collection-location-ok") {
		t.Errorf("MKCALENDAR on the home set = %d: %s", code, body)
	}
}

func TestMkcalendarKeepsUpperCaseUUID(t *testing.T) {
	e := newMkEnv(t, true)
	seg := "9F1C2B3A-0000-4000-8000-ABCDEF123456"
	if code, _, _ := e.do(mkUser, "MKCALENDAR", mkPath(seg), mkcalBody("Upper")); code != 201 {
		t.Fatal(code)
	}
	if name, _, _ := e.stored(seg); name != "Upper" {
		t.Errorf("name = %q", name)
	}
}

func TestMkcalendarNameFallbackAndCap(t *testing.T) {
	e := newMkEnv(t, true)
	if code, _, _ := e.do(mkUser, "MKCALENDAR", mkPath("plain"), ""); code != 201 {
		t.Fatal(code)
	}
	if name, _, _ := e.stored("plain"); name != "plain" {
		t.Errorf("name = %q, want the segment", name)
	}
	// 400 runes with a tab in the middle: capped, control character gone.
	long := strings.Repeat("é", 300) + "&#9;" + strings.Repeat("é", 100)
	if code, _, _ := e.do(mkUser, "MKCALENDAR", mkPath("long"), mkcalBody(long)); code != 201 {
		t.Fatal(code)
	}
	name, _, _ := e.stored("long")
	if len([]rune(name)) != maxCalendarName || strings.ContainsRune(name, '\t') {
		t.Errorf("name has %d runes, tab %v", len([]rune(name)), strings.ContainsRune(name, '\t'))
	}
}

func TestMkcolCreatesTheSameCalendar(t *testing.T) {
	e := newMkEnv(t, true)
	if code, body, _ := e.do(mkUser, "MKCOL", mkPath("groceries"), mkcolBody("Groceries", "VTODO")); code != 201 {
		t.Fatalf("MKCOL = %d: %s", code, body)
	}
	if code, _, _ := e.do(mkUser, "MKCALENDAR", mkPath("groceries2"), mkcalBody("Groceries", "VTODO")); code != 201 {
		t.Fatal(code)
	}
	n1, d1, c1 := e.stored("groceries")
	n2, d2, c2 := e.stored("groceries2")
	if n1 != n2 || d1 != d2 || strings.Join(c1, ",") != strings.Join(c2, ",") || strings.Join(c1, ",") != "VTODO" {
		t.Errorf("MKCOL %q %q %v, MKCALENDAR %q %q %v", n1, d1, c1, n2, d2, c2)
	}
	// MKCOL without the calendar resourcetype is not a calendar.
	plain := `<?xml version="1.0"?><D:mkcol xmlns:D="DAV:"><D:set><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop></D:set></D:mkcol>`
	if code, _, _ := e.do(mkUser, "MKCOL", mkPath("plain"), plain); code != http.StatusForbidden {
		t.Errorf("plain MKCOL = %d", code)
	}
}
