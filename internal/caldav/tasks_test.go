package caldav

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-webdav/caldav"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	tdOwner = "aaaaaaaa-2222-4333-8444-555555555555"
	tdRead  = "rita"
	tdWrite = "wendy"
	// calendars of the owner
	tdPersonal = "personal" // VEVENT (components empty)
	tdTasks    = "tasks"    // VTODO
	tdBoth     = "both"     // VEVENT and VTODO
)

type tdEnv struct {
	t      *testing.T
	ps     *store.PebbleStore
	app    *grantApplier
	events *store.EventStore
	tasks  *store.TaskStore
	cals   *store.CalendarStore
	quota  shQuota
	srv    *httptest.Server
}

func newTdEnv(t *testing.T) *tdEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &grantApplier{ps: ps}
	e := &tdEnv{
		t: t, ps: ps, app: app,
		events: store.NewEventStore(ps, app),
		tasks:  store.NewTaskStore(ps, app),
		cals:   store.NewCalendarStore(ps, app),
		quota:  shQuota{},
	}
	acl := store.NewACLStore(ps, app)
	for _, c := range []*bongov1.Calendar{
		{Guid: tdPersonal, Name: "Personal"},
		{Guid: tdTasks, Name: "Tasks", Components: []string{"VTODO"}},
		{Guid: tdBoth, Name: "Both", Components: []string{"VEVENT", "VTODO"}},
	} {
		if err := e.cals.Update(tdOwner, c.Guid, c); err != nil {
			t.Fatal(err)
		}
	}
	for who, level := range map[string]string{tdRead: store.GrantLevelRead, tdWrite: store.GrantLevelWrite} {
		if err := acl.SetCalendarGrant(tdOwner, tdTasks, who, level, store.GrantStateAccepted, tdOwner, time.Unix(1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend := NewBackend(e.events, e.cals, logger)
	backend.SetGrants(acl)
	backend.SetTaskStore(e.tasks)
	backend.SetQuotaChecker(e.quota)
	h := PrivilegeFilter(&caldav.Handler{Backend: backend, Prefix: "/caldav"}, backend.ReadOnlyShare, logger)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), r.Header.Get("X-Test-User"))))
	}))
	t.Cleanup(e.srv.Close)
	app.writes = 0
	return e
}

func (e *tdEnv) do(user, method, path, body string, hdr map[string]string) (int, string) {
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

func tdPath(cal, guid string) string {
	return "/caldav/" + tdOwner + "/calendars/" + cal + "/" + guid + ".ics"
}

func tdSharedPath(user, cal, guid string) string {
	return "/caldav/" + user + "/calendars/" + sharedSegment(tdOwner, cal) + "/" + guid + ".ics"
}

func vcal(components ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//T//EN\r\n" + strings.Join(components, "") + "END:VCALENDAR\r\n"
}

func vtodo(uid, summary string, extra ...string) string {
	return "BEGIN:VTODO\r\nUID:" + uid + "\r\nDTSTAMP:20301101T000000Z\r\nSUMMARY:" + summary + "\r\n" +
		strings.Join(extra, "") + "END:VTODO\r\n"
}

func vevent(uid, summary string) string {
	return "BEGIN:VEVENT\r\nUID:" + uid + "\r\nDTSTAMP:20301101T000000Z\r\nDTSTART:20301105T090000Z\r\nDTEND:20301105T100000Z\r\nSUMMARY:" + summary + "\r\nEND:VEVENT\r\n"
}

func (e *tdEnv) put(user, path, body string, hdr map[string]string) int {
	code, _ := e.do(user, "PUT", path, body, hdr)
	return code
}

func TestVTodoStoredAsTaskNotEvent(t *testing.T) {
	e := newTdEnv(t)
	body := vcal(vtodo("t1", "Buy milk",
		"DUE:20301110T120000Z\r\nDTSTART:20301109T120000Z\r\nSTATUS:IN-PROCESS\r\nPRIORITY:3\r\nPERCENT-COMPLETE:40\r\nDESCRIPTION:Semi-skimmed\r\n"))
	if code := e.put(tdOwner, tdPath(tdTasks, "t1"), body, nil); code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", code)
	}
	task, err := e.tasks.Get(tdOwner, "t1")
	if err != nil {
		t.Fatalf("task not stored: %v", err)
	}
	if task.Summary != "Buy milk" || task.Description != "Semi-skimmed" || task.Status != "IN-PROCESS" ||
		task.Priority != 3 || task.Percent != 40 || task.CalendarId != tdTasks {
		t.Errorf("task fields = %+v", task)
	}
	if want := time.Date(2030, 11, 10, 12, 0, 0, 0, time.UTC).Unix(); task.Due != want {
		t.Errorf("due = %d, want %d", task.Due, want)
	}
	if want := time.Date(2030, 11, 9, 12, 0, 0, 0, time.UTC).Unix(); task.Start != want {
		t.Errorf("start = %d, want %d", task.Start, want)
	}
	if len(task.IcalRaw) == 0 {
		t.Error("ical_raw not kept")
	}
	if _, err := e.events.Get(tdOwner, "t1"); err == nil {
		t.Error("a VTODO PUT also created an event: the 1970 blank event bug")
	}

	// Read back, list, delete.
	code, got := e.do(tdOwner, "GET", tdPath(tdTasks, "t1"), "", nil)
	if code != http.StatusOK || !strings.Contains(got, "BEGIN:VTODO") || !strings.Contains(got, "Buy milk") {
		t.Fatalf("GET = %d %q", code, got)
	}
	_, list := e.do(tdOwner, "PROPFIND", "/caldav/"+tdOwner+"/calendars/"+tdTasks+"/", "", map[string]string{"Depth": "1"})
	if !strings.Contains(list, "t1.ics") {
		t.Errorf("PROPFIND listing lacks the task: %s", list)
	}
	if code, _ := e.do(tdOwner, "DELETE", tdPath(tdTasks, "t1"), "", nil); code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", code)
	}
	if _, err := e.tasks.Get(tdOwner, "t1"); err == nil {
		t.Error("task still stored after DELETE")
	}
}

func TestCompletedTaskRoundTrip(t *testing.T) {
	e := newTdEnv(t)
	done := vcal(vtodo("t1", "Done", "STATUS:COMPLETED\r\nCOMPLETED:20301110T120000Z\r\nPERCENT-COMPLETE:100\r\n"))
	if code := e.put(tdOwner, tdPath(tdTasks, "t1"), done, nil); code != http.StatusCreated {
		t.Fatalf("PUT = %d", code)
	}
	task, err := e.tasks.Get(tdOwner, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "COMPLETED" || task.Percent != 100 || task.Completed != time.Date(2030, 11, 10, 12, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("completed task = %+v", task)
	}
}

// The precondition body is built by hand around a library helper that hard-codes
// 409, so pin both the status and the XML element: a go-webdav upgrade that
// changes how errors are rendered turns this red.
func TestRefusedComponentIs403WithPrecondition(t *testing.T) {
	cases := []struct {
		name, cal, body string
	}{
		{"VTODO into an event calendar", tdPersonal, vcal(vtodo("x", "No"))},
		{"VEVENT into a task calendar", tdTasks, vcal(vevent("x", "No"))},
		{"VJOURNAL anywhere", tdBoth, vcal("BEGIN:VJOURNAL\r\nUID:x\r\nDTSTAMP:20301101T000000Z\r\nEND:VJOURNAL\r\n")},
		{"VTODO into the default event calendar of an old user", tdPersonal, vcal("BEGIN:VTIMEZONE\r\nTZID:Europe/Berlin\r\nEND:VTIMEZONE\r\n", vtodo("x", "No"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTdEnv(t)
			code, body := e.do(tdOwner, "PUT", tdPath(tc.cal, "x"), tc.body, nil)
			if code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body %q)", code, body)
			}
			if !strings.Contains(body, "supported-calendar-component") {
				t.Errorf("body lacks the supported-calendar-component precondition: %q", body)
			}
			if !strings.Contains(body, "urn:ietf:params:xml:ns:caldav") {
				t.Errorf("precondition not in the CalDAV namespace: %q", body)
			}
			// Nothing is stored, as an event or as a task.
			if _, err := e.events.Get(tdOwner, "x"); err == nil {
				t.Error("refused PUT stored an event")
			}
			if _, err := e.tasks.Get(tdOwner, "x"); err == nil {
				t.Error("refused PUT stored a task")
			}
		})
	}
}

func TestMixedComponentPutRefused(t *testing.T) {
	e := newTdEnv(t)
	body := vcal(vevent("m", "Event"), vtodo("m", "Todo"))
	code, resp := e.do(tdOwner, "PUT", tdPath(tdBoth, "m"), body, nil)
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
	if !strings.Contains(resp, "valid-calendar-object-resource") {
		t.Errorf("body lacks valid-calendar-object-resource: %q", resp)
	}
}

func TestEventAndTodoBothAcceptedInMixedCalendar(t *testing.T) {
	e := newTdEnv(t)
	if code := e.put(tdOwner, tdPath(tdBoth, "ev"), vcal(vevent("ev", "E")), nil); code != http.StatusCreated {
		t.Fatalf("VEVENT into both = %d", code)
	}
	if code := e.put(tdOwner, tdPath(tdBoth, "td"), vcal(vtodo("td", "T")), nil); code != http.StatusCreated {
		t.Fatalf("VTODO into both = %d", code)
	}
	_, list := e.do(tdOwner, "PROPFIND", "/caldav/"+tdOwner+"/calendars/"+tdBoth+"/", "", map[string]string{"Depth": "1"})
	if !strings.Contains(list, "ev.ics") || !strings.Contains(list, "td.ics") {
		t.Errorf("mixed calendar listing = %s", list)
	}
}

func TestSupportedComponentSetPerCalendar(t *testing.T) {
	e := newTdEnv(t)
	prop := `<?xml version="1.0"?><propfind xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><prop><C:supported-calendar-component-set/></prop></propfind>`
	code, body := e.do(tdOwner, "PROPFIND", "/caldav/"+tdOwner+"/calendars/", prop,
		map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	if code != http.StatusMultiStatus {
		t.Fatalf("status = %d: %s", code, body)
	}
	seg := func(cal string) string {
		i := strings.Index(body, "/calendars/"+cal+"/")
		if i < 0 {
			t.Fatalf("no response for %s in %s", cal, body)
		}
		rest := body[i:]
		if j := strings.Index(rest[1:], "<response>"); j >= 0 {
			rest = rest[:j+1]
		} else if j := strings.Index(rest[1:], "</response>"); j >= 0 {
			rest = rest[:j+1]
		}
		return rest
	}
	for cal, want := range map[string][]string{
		tdPersonal: {`name="VEVENT"`},
		tdTasks:    {`name="VTODO"`},
		tdBoth:     {`name="VEVENT"`, `name="VTODO"`},
	} {
		got := seg(cal)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %s in %s", cal, w, got)
			}
		}
	}
	if strings.Contains(seg(tdTasks), `name="VEVENT"`) {
		t.Error("Tasks reports VEVENT")
	}
	if strings.Contains(seg(tdPersonal), `name="VTODO"`) {
		t.Error("Personal reports VTODO")
	}

	// Shared collection reports the owner's calendar component set.
	_, sbody := e.do(tdWrite, "PROPFIND", "/caldav/"+tdWrite+"/calendars/", prop,
		map[string]string{"Depth": "1", "Content-Type": "application/xml"})
	i := strings.Index(sbody, sharedSegment(tdOwner, tdTasks))
	if i < 0 {
		t.Fatalf("shared Tasks not listed: %s", sbody)
	}
	if !strings.Contains(sbody[i:], `name="VTODO"`) {
		t.Errorf("shared Tasks does not report VTODO: %s", sbody[i:])
	}
	// And a single-collection PROPFIND (GetCalendar).
	_, one := e.do(tdOwner, "PROPFIND", "/caldav/"+tdOwner+"/calendars/"+tdTasks+"/", prop,
		map[string]string{"Depth": "0", "Content-Type": "application/xml"})
	if !strings.Contains(one, `name="VTODO"`) {
		t.Errorf("Depth 0 PROPFIND lacks VTODO: %s", one)
	}
}

func TestTaskSharedCalendarAuthorisation(t *testing.T) {
	e := newTdEnv(t)
	body := vcal(vtodo("s1", "Shared task"))

	// Read-only grantee: refused, nothing written.
	if code := e.put(tdRead, tdSharedPath(tdRead, tdTasks, "s1"), body, nil); code != http.StatusForbidden {
		t.Errorf("read grantee PUT = %d, want 403", code)
	}
	if _, err := e.tasks.Get(tdOwner, "s1"); err == nil {
		t.Error("read-only grantee wrote a task")
	}
	// Write grantee: the task lands in the owner's key space.
	if code := e.put(tdWrite, tdSharedPath(tdWrite, tdTasks, "s1"), body, nil); code != http.StatusCreated {
		t.Fatalf("write grantee PUT = %d, want 201", code)
	}
	if _, err := e.tasks.Get(tdOwner, "s1"); err != nil {
		t.Errorf("task not in the owner's key space: %v", err)
	}
	if _, err := e.tasks.Get(tdWrite, "s1"); err == nil {
		t.Error("task written to the grantee's own key space")
	}
	// The read grantee can read it, but not delete it.
	if code, _ := e.do(tdRead, "GET", tdSharedPath(tdRead, tdTasks, "s1"), "", nil); code != http.StatusOK {
		t.Errorf("read grantee GET = %d, want 200", code)
	}
	if code, _ := e.do(tdRead, "DELETE", tdSharedPath(tdRead, tdTasks, "s1"), "", nil); code != http.StatusForbidden {
		t.Errorf("read grantee DELETE = %d, want 403", code)
	}
	if _, err := e.tasks.Get(tdOwner, "s1"); err != nil {
		t.Error("read grantee deleted the task")
	}
}

// A grant covers one calendar. A task the owner keeps in another VTODO
// calendar must not be reachable, replaceable or deletable through it.
func TestTaskSharedCalendarGuidInOtherCalendar(t *testing.T) {
	e := newTdEnv(t)
	if _, err := e.tasks.Create(tdOwner, &bongov1.Task{Guid: "secret", CalendarId: tdBoth, Summary: "Private"}); err != nil {
		t.Fatal(err)
	}
	app := e.app
	app.writes = 0

	if code := e.put(tdWrite, tdSharedPath(tdWrite, tdTasks, "secret"), vcal(vtodo("secret", "Overwritten")), nil); code != http.StatusForbidden {
		t.Errorf("PUT over a task in another calendar = %d, want 403", code)
	}
	if code, _ := e.do(tdWrite, "GET", tdSharedPath(tdWrite, tdTasks, "secret"), "", nil); code != http.StatusNotFound {
		t.Errorf("GET of a task in another calendar = %d, want 404", code)
	}
	if code, _ := e.do(tdWrite, "DELETE", tdSharedPath(tdWrite, tdTasks, "secret"), "", nil); code != http.StatusNotFound {
		t.Errorf("DELETE of a task in another calendar = %d, want 404", code)
	}
	got, err := e.tasks.Get(tdOwner, "secret")
	if err != nil || got.Summary != "Private" || got.CalendarId != tdBoth {
		t.Errorf("task changed: %+v, %v", got, err)
	}
	if app.writes != 0 {
		t.Errorf("refusals wrote %d commands", app.writes)
	}
}

func TestTaskAndEventGuidsDoNotCollide(t *testing.T) {
	e := newTdEnv(t)
	if code := e.put(tdOwner, tdPath(tdBoth, "same"), vcal(vevent("same", "Event")), nil); code != http.StatusCreated {
		t.Fatalf("seed event = %d", code)
	}
	if code := e.put(tdOwner, tdPath(tdBoth, "same"), vcal(vtodo("same", "Todo")), nil); code != http.StatusForbidden {
		t.Errorf("VTODO PUT onto an event guid = %d, want 403", code)
	}
	if _, err := e.tasks.Get(tdOwner, "same"); err == nil {
		t.Error("task stored under an event guid")
	}

	if code := e.put(tdOwner, tdPath(tdBoth, "other"), vcal(vtodo("other", "Todo")), nil); code != http.StatusCreated {
		t.Fatalf("seed task = %d", code)
	}
	if code := e.put(tdOwner, tdPath(tdBoth, "other"), vcal(vevent("other", "Event")), nil); code != http.StatusForbidden {
		t.Errorf("VEVENT PUT onto a task guid = %d, want 403", code)
	}
	if _, err := e.events.Get(tdOwner, "other"); err == nil {
		t.Error("event stored under a task guid")
	}
}

func TestTaskPreconditionsAndQuota(t *testing.T) {
	e := newTdEnv(t)
	body := vcal(vtodo("p1", "One"))
	if code := e.put(tdOwner, tdPath(tdTasks, "p1"), body, map[string]string{"If-None-Match": "*"}); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	if code := e.put(tdOwner, tdPath(tdTasks, "p1"), vcal(vtodo("p1", "Two")), map[string]string{"If-None-Match": "*"}); code != http.StatusPreconditionFailed {
		t.Errorf("If-None-Match: * on an existing task = %d, want 412", code)
	}
	if code := e.put(tdOwner, tdPath(tdTasks, "p1"), vcal(vtodo("p1", "Two")), map[string]string{"If-Match": `"stale"`}); code != http.StatusPreconditionFailed {
		t.Errorf("stale If-Match = %d, want 412", code)
	}
	if got, _ := e.tasks.Get(tdOwner, "p1"); got.Summary != "One" {
		t.Errorf("a refused PUT changed the task: %q", got.Summary)
	}
	// The wire form of the ETag is the library's business (it quotes the
	// backend's already-quoted value, for events as well); the condition is
	// the strong ETag of the stored bytes.
	cur, err := e.tasks.Get(tdOwner, "p1")
	if err != nil {
		t.Fatal(err)
	}
	etag := store.ContentETag(cur.IcalRaw, cur.UpdatedAt)
	if code := e.put(tdOwner, tdPath(tdTasks, "p1"), vcal(vtodo("p1", "Two")), map[string]string{"If-Match": etag}); code != http.StatusCreated {
		t.Errorf("matching If-Match = %d, want 201", code)
	}

	// Quota is the owner's: a full owner refuses a new task, from the owner and from a grantee.
	e.quota[tdOwner] = true
	if code := e.put(tdOwner, tdPath(tdTasks, "q1"), vcal(vtodo("q1", "Big")), nil); code != http.StatusInsufficientStorage {
		t.Errorf("PUT over quota = %d, want 507", code)
	}
	if code := e.put(tdWrite, tdSharedPath(tdWrite, tdTasks, "q2"), vcal(vtodo("q2", "Big")), nil); code != http.StatusInsufficientStorage {
		t.Errorf("grantee PUT over the owner's quota = %d, want 507", code)
	}
	if code, _ := e.do(tdOwner, "DELETE", tdPath(tdTasks, "p1"), "", nil); code != http.StatusNoContent {
		t.Errorf("DELETE at quota = %d, want 204", code)
	}
}

func TestSubscribedTaskCalendarIsReadOnly(t *testing.T) {
	e := newTdEnv(t)
	if err := e.cals.Update(tdOwner, "subtasks", &bongov1.Calendar{
		Guid: "subtasks", Name: "Feed", Components: []string{"VTODO"},
		Subscription: &bongov1.CalendarSubscription{SourceUrl: "https://feeds.example.com/s3cret"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Create(tdOwner, &bongov1.Task{Guid: "feed1", CalendarId: "subtasks", Summary: "Feed task"}); err != nil {
		t.Fatal(err)
	}
	if code := e.put(tdOwner, tdPath("subtasks", "n1"), vcal(vtodo("n1", "New")), nil); code != http.StatusForbidden {
		t.Errorf("PUT into a subscription = %d, want 403", code)
	}
	// The stored calendar decides, not the path: the same guid through another collection.
	if code := e.put(tdOwner, tdPath(tdBoth, "feed1"), vcal(vtodo("feed1", "Hijack")), nil); code != http.StatusForbidden {
		t.Errorf("PUT over a subscribed task through another collection = %d, want 403", code)
	}
	if code, _ := e.do(tdOwner, "DELETE", tdPath(tdBoth, "feed1"), "", nil); code != http.StatusForbidden && code != http.StatusNotFound {
		t.Errorf("DELETE of a subscribed task = %d, want 403", code)
	}
	if got, err := e.tasks.Get(tdOwner, "feed1"); err != nil || got.Summary != "Feed task" {
		t.Errorf("subscribed task changed: %+v %v", got, err)
	}
}

func TestListingReturnsOnlyAllowedComponents(t *testing.T) {
	e := newTdEnv(t)
	// A web-created event filed in the Tasks calendar must not appear in the VTODO collection.
	if _, err := e.events.Create(tdOwner, &bongov1.Event{Guid: "stray", CalendarId: tdTasks, Title: "Stray", StartTime: 1, EndTime: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.tasks.Create(tdOwner, &bongov1.Task{Guid: "real", CalendarId: tdTasks, Summary: "Real"}); err != nil {
		t.Fatal(err)
	}
	// A task filed in an event calendar is not listed there either.
	if _, err := e.tasks.Create(tdOwner, &bongov1.Task{Guid: "lost", CalendarId: tdPersonal, Summary: "Lost"}); err != nil {
		t.Fatal(err)
	}
	_, tl := e.do(tdOwner, "PROPFIND", "/caldav/"+tdOwner+"/calendars/"+tdTasks+"/", "", map[string]string{"Depth": "1"})
	if !strings.Contains(tl, "real.ics") || strings.Contains(tl, "stray.ics") {
		t.Errorf("Tasks listing = %s", tl)
	}
	_, pl := e.do(tdOwner, "PROPFIND", "/caldav/"+tdOwner+"/calendars/"+tdPersonal+"/", "", map[string]string{"Depth": "1"})
	if strings.Contains(pl, "lost.ics") {
		t.Errorf("Personal listing holds a task: %s", pl)
	}
}

func TestTodoCalendarQueryTimeRange(t *testing.T) {
	e := newTdEnv(t)
	for guid, extra := range map[string]string{
		"due-in":     "DUE:20301110T120000Z\r\n",
		"due-out":    "DUE:20310110T120000Z\r\n",
		"start-in":   "DTSTART:20301112T120000Z\r\n",
		"both-cover": "DTSTART:20301001T120000Z\r\nDUE:20301201T120000Z\r\n",
		"undated":    "",
		"done-in":    "COMPLETED:20301115T120000Z\r\n",
		"done-out":   "COMPLETED:20250115T120000Z\r\n",
	} {
		if code := e.put(tdOwner, tdPath(tdTasks, guid), vcal(vtodo(guid, guid, extra)), nil); code != http.StatusCreated {
			t.Fatalf("seed %s = %d", guid, code)
		}
	}
	query := func(timeRange string) string {
		body := `<?xml version="1.0"?><C:calendar-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav"><D:prop><D:getetag/></D:prop>` +
			`<C:filter><C:comp-filter name="VCALENDAR"><C:comp-filter name="VTODO">` + timeRange + `</C:comp-filter></C:comp-filter></C:filter></C:calendar-query>`
		code, resp := e.do(tdOwner, "REPORT", "/caldav/"+tdOwner+"/calendars/"+tdTasks+"/", body,
			map[string]string{"Depth": "1", "Content-Type": "application/xml"})
		if code != http.StatusMultiStatus {
			t.Fatalf("REPORT = %d: %s", code, resp)
		}
		return resp
	}

	// No time range: every task.
	all := query("")
	for _, g := range []string{"due-in", "due-out", "undated", "done-out"} {
		if !strings.Contains(all, g+".ics") {
			t.Errorf("plain query lacks %s", g)
		}
	}

	// November 2030.
	got := query(`<C:time-range start="20301101T000000Z" end="20301201T000000Z"/>`)
	if got == "" {
		t.Fatal("empty response")
	}
	for _, g := range []string{"due-in", "start-in", "both-cover", "undated", "done-in"} {
		if !strings.Contains(got, "/"+g+".ics") {
			t.Errorf("time-range query lacks %s (VTODO time-range silently matched nothing?)", g)
		}
	}
	for _, g := range []string{"due-out", "done-out"} {
		if strings.Contains(got, "/"+g+".ics") {
			t.Errorf("time-range query wrongly includes %s", g)
		}
	}
}
