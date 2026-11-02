package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// summaryNow is the clock of the summary tests. The invitations of icsOpts
// start on 2030-05-06, five days later.
var summaryNow = time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC)

// summaryEnv is the real summary handler over real stores. The request's user
// is chosen by the X-Test-User header: quotaUser by default.
type summaryEnv struct {
	*imipEnv
	meta  *store.MetadataManager
	inbox *store.ScheduleInboxStore
	tasks *store.TaskStore
	h     *SummaryHandler
	n     int
}

func newSummaryEnv(t testing.TB) *summaryEnv {
	t.Helper()
	e := &summaryEnv{imipEnv: newImipEnv(t)}
	meta, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { meta.Close() })
	e.meta = meta

	a := &directApplierTags{ps: e.q.ps}
	e.inbox = store.NewScheduleInboxStore(e.q.ps, a)
	e.tasks = store.NewTaskStore(e.q.ps, a)
	e.cal.now = func() time.Time { return summaryNow }
	e.cal.SetTaskStore(e.tasks)

	e.h = NewSummaryHandler(discardLogger())
	e.h.SetSidebar(e.q.mailboxes, nil)
	e.h.SetMailSource(NewMailSummary(e.q.mailboxes, meta, e.q.ps, nil))
	cs := NewCalendarSummary(e.cal, e.q.mailboxes, e.q.ps)
	cs.SetScheduleInbox(e.inbox)
	e.h.SetInvitations(cs)
	e.h.SetUpcoming(cs)
	return e
}

func (e *summaryEnv) router() http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user, email := quotaUser, quotaEmail
			if u := req.Header.Get("X-Test-User"); u == otherUser {
				user, email = otherUser, otherEmail
			}
			ctx := middleware.WithUserContext(req.Context(), user, email)
			if loc := req.Header.Get("X-Test-Locale"); loc != "" {
				ctx = middleware.WithLocale(ctx, loc)
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/summary", e.h.Summary)
	return r
}

// get returns the status and body of GET /summary as the given user.
func (e *summaryEnv) get(user string, htmx bool) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "/summary", nil)
	req.Header.Set("X-Test-User", user)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	e.router().ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// page is GET /summary as quotaUser, which must succeed.
func (e *summaryEnv) page(t *testing.T) string {
	t.Helper()
	code, body := e.get(quotaUser, false)
	if code != http.StatusOK {
		t.Fatalf("GET /summary = %d: %s", code, body)
	}
	return body
}

// mailSeed describes a message to store and index the way the FSM does.
type mailSeed struct {
	user, mailbox, subject, from string
	flags                        uint32
	toMe                         bool
	at                           int64
}

func (e *summaryEnv) mail(t testing.TB, m mailSeed) uint32 {
	t.Helper()
	if m.user == "" {
		m.user = quotaUser
	}
	if m.mailbox == "" {
		m.mailbox = "INBOX"
	}
	if m.from == "" {
		m.from = "stranger@elsewhere.test"
	}
	email := quotaEmail
	if m.user == otherUser {
		email = otherEmail
	}
	if err := e.q.mailboxes.CreateMailbox(m.user, m.mailbox); err != nil && !strings.Contains(err.Error(), "exist") {
		t.Fatalf("CreateMailbox: %v", err)
	}
	e.n++
	guid := fmt.Sprintf("g-%s-%d", m.user, e.n)
	if m.at == 0 {
		m.at = summaryNow.Unix() - int64(e.n)
	}
	meta := &bongov1.MailMeta{Guid: guid, Subject: m.subject, From: m.from, ReceivedAt: m.at}
	if m.toMe {
		meta.To = []string{email}
	} else {
		meta.To = []string{"list@elsewhere.test"}
	}
	uid, err := e.q.mailboxes.DeliverMessage(m.user, m.mailbox, meta, m.flags)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	if err := e.meta.IndexMail(m.user, guid, m.subject, m.from, m.at, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.meta.SetMailHeaders(m.user, guid, store.MailHeaderFields(meta, map[string]bool{email: true})); err != nil {
		t.Fatal(err)
	}
	return uid
}

func (e *summaryEnv) event(t testing.TB, user string, ev *bongov1.Event) {
	t.Helper()
	if ev.CalendarId == "" {
		ev.CalendarId = "default"
	}
	if ev.EndTime == 0 {
		ev.EndTime = ev.StartTime + 3600
	}
	if _, err := e.q.events.Create(user, ev); err != nil {
		t.Fatalf("Create event: %v", err)
	}
}

func (e *summaryEnv) task(t testing.TB, summary string, due time.Time) {
	t.Helper()
	task := &bongov1.Task{Guid: store.NewTaskGUID(), CalendarId: store.TasksCalendarGUID, Summary: summary, Due: due.Unix()}
	raw, err := icalevent.BuildTodo(task, false, summaryNow)
	if err != nil {
		t.Fatal(err)
	}
	task.IcalRaw = raw
	if _, err := e.tasks.Create(quotaUser, task); err != nil {
		t.Fatalf("Create task: %v", err)
	}
}

// place puts an iTIP object in a user's schedule inbox.
func (e *summaryEnv) place(t testing.TB, user, ics string) {
	t.Helper()
	e.n++
	if err := e.inbox.Place(user, fmt.Sprintf("%016x", e.n), []byte(ics)); err != nil {
		t.Fatalf("Place: %v", err)
	}
}

// section returns the HTML of one section of the page.
func section(t *testing.T, body, id string) string {
	t.Helper()
	marker := `data-summary-section="` + id + `"`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no %s section in:\n%s", id, body)
	}
	rest := body[i+len(marker):]
	if j := strings.Index(rest, "<section"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func rowCount(sec string) int { return strings.Count(sec, `class="summary-row-title"`) }

type templatesRow = templates.SummaryRow

var allSections = []string{"to-me", "starred", "invitations", "upcoming"}

func TestSummaryAllFourSectionsRender(t *testing.T) {
	e := newSummaryEnv(t)
	inboxUID := e.mail(t, mailSeed{subject: "Unread for me", toMe: true})
	e.mail(t, mailSeed{subject: "Already read", toMe: true, flags: store.FlagSeen})
	e.mail(t, mailSeed{subject: "Unread but not to me"})
	e.mail(t, mailSeed{subject: "Starred one", flags: store.FlagFlagged | store.FlagSeen})
	e.mail(t, mailSeed{mailbox: "Projects/Client A", subject: "Starred elsewhere", flags: store.FlagFlagged})
	e.place(t, quotaUser, icsOpts{uid: "inv-1@example.org", summary: "Board meeting"}.build("REQUEST"))
	e.event(t, quotaUser, &bongov1.Event{Guid: "ev-1", Title: "Standup", StartTime: summaryNow.Add(48 * time.Hour).Unix()})
	e.event(t, quotaUser, &bongov1.Event{Guid: "ev-far", Title: "Next month", StartTime: summaryNow.Add(20 * 24 * time.Hour).Unix()})
	e.task(t, "File taxes", summaryNow.Add(24*time.Hour))

	body := e.page(t)

	toMe := section(t, body, "to-me")
	if !strings.Contains(toMe, "Unread for me") {
		t.Errorf("to-me lacks the unread message: %s", toMe)
	}
	for _, not := range []string{"Already read", "Unread but not to me"} {
		if strings.Contains(toMe, not) {
			t.Errorf("to-me lists %q", not)
		}
	}
	if !strings.Contains(toMe, fmt.Sprintf(`href="/mail/INBOX/%d"`, inboxUID)) || !strings.Contains(toMe, `href="/mail/to-me"`) {
		t.Errorf("to-me links wrong: %s", toMe)
	}

	starred := section(t, body, "starred")
	for _, want := range []string{"Starred one", "Starred elsewhere", `href="/mail/Projects%2FClient%20A/`, `href="/mail/starred"`} {
		if !strings.Contains(starred, want) {
			t.Errorf("starred lacks %q: %s", want, starred)
		}
	}

	inv := section(t, body, "invitations")
	for _, want := range []string{"Board meeting", "olive@example.org", `href="/mail/INBOX"`} {
		if !strings.Contains(inv, want) {
			t.Errorf("invitations lack %q: %s", want, inv)
		}
	}

	up := section(t, body, "upcoming")
	for _, want := range []string{"Standup", "File taxes", `href="/calendar/agenda"`, "/tasks/"} {
		if !strings.Contains(up, want) {
			t.Errorf("upcoming lacks %q: %s", want, up)
		}
	}
	if strings.Contains(up, "Next month") {
		t.Error("upcoming lists an event 20 days away")
	}
	for _, id := range allSections {
		if strings.Contains(section(t, body, id), `data-summary-state="empty"`) {
			t.Errorf("section %s shows its empty state", id)
		}
	}
}

func TestSummarySectionsShowAtMostTenRows(t *testing.T) {
	e := newSummaryEnv(t)
	for i := range 14 {
		e.mail(t, mailSeed{subject: fmt.Sprintf("to-me %d", i), toMe: true})
		e.mail(t, mailSeed{subject: fmt.Sprintf("star %d", i), flags: store.FlagFlagged})
		e.place(t, quotaUser, icsOpts{uid: fmt.Sprintf("inv-%d@example.org", i), summary: fmt.Sprintf("invite %d", i)}.build("REQUEST"))
		e.event(t, quotaUser, &bongov1.Event{Guid: fmt.Sprintf("ev-%d", i), Title: fmt.Sprintf("event %d", i), StartTime: summaryNow.Add(time.Duration(i+1) * time.Hour).Unix()})
	}
	body := e.page(t)
	for _, id := range allSections {
		if n := rowCount(section(t, body, id)); n != summaryLimit {
			t.Errorf("section %s has %d rows, want %d", id, n, summaryLimit)
		}
	}
}

func TestSummaryToMeKeepsTheNewestTenUnreadNotTheUnreadOfTheNewestTen(t *testing.T) {
	e := newSummaryEnv(t)
	// The twelve newest are read; the unread ones are older. A filter applied
	// after paging would show nothing.
	for i := range 12 {
		e.mail(t, mailSeed{subject: fmt.Sprintf("read %d", i), toMe: true, flags: store.FlagSeen, at: summaryNow.Unix() - int64(i)})
	}
	for i := range 3 {
		e.mail(t, mailSeed{subject: fmt.Sprintf("old unread %d", i), toMe: true, at: summaryNow.Unix() - 1000 - int64(i)})
	}
	sec := section(t, e.page(t), "to-me")
	if n := rowCount(sec); n != 3 {
		t.Errorf("to-me has %d rows, want the 3 unread: %s", n, sec)
	}
}

func TestSummaryEmptyStates(t *testing.T) {
	e := newSummaryEnv(t)
	body := e.page(t)
	for _, id := range allSections {
		sec := section(t, body, id)
		if !strings.Contains(sec, `data-summary-state="empty"`) || rowCount(sec) != 0 {
			t.Errorf("section %s is not an empty state: %s", id, sec)
		}
		if strings.Contains(sec, `data-summary-state="error"`) {
			t.Errorf("section %s shows an error", id)
		}
	}
}

// failing fakes: one section fails, the other three still render.
type fakeSummaryMail struct {
	toMe, starred       []string
	toMeErr, starredErr error
}

func rowsOf(titles []string) []templatesRow {
	var out []templatesRow
	for _, s := range titles {
		out = append(out, templatesRow{Title: s, Href: "/x"})
	}
	return out
}

func (f fakeSummaryMail) UnreadToMe(string, int) ([]templatesRow, error) {
	return rowsOf(f.toMe), f.toMeErr
}
func (f fakeSummaryMail) Starred(string, int) ([]templatesRow, error) {
	return rowsOf(f.starred), f.starredErr
}

type fakeSummaryInv struct {
	titles []string
	err    error
}

func (f fakeSummaryInv) Awaiting(string, string, int) ([]templatesRow, error) {
	return rowsOf(f.titles), f.err
}

type fakeSummaryUp struct {
	titles []string
	err    error
}

func (f fakeSummaryUp) Upcoming(r *http.Request, _, _ string, _, _ int) (*http.Request, []templatesRow, error) {
	return r, rowsOf(f.titles), f.err
}

func TestSummaryOneFailingSectionDoesNotFailThePage(t *testing.T) {
	const secret = "pebble: /var/lib/bongo/secret-path: disk on fire"
	boom := errors.New(secret)
	cases := []struct {
		fail string
		mail fakeSummaryMail
		inv  fakeSummaryInv
		up   fakeSummaryUp
	}{
		{fail: "to-me", mail: fakeSummaryMail{toMeErr: boom, starred: []string{"S"}}, inv: fakeSummaryInv{titles: []string{"I"}}, up: fakeSummaryUp{titles: []string{"U"}}},
		{fail: "starred", mail: fakeSummaryMail{toMe: []string{"T"}, starredErr: boom}, inv: fakeSummaryInv{titles: []string{"I"}}, up: fakeSummaryUp{titles: []string{"U"}}},
		{fail: "invitations", mail: fakeSummaryMail{toMe: []string{"T"}, starred: []string{"S"}}, inv: fakeSummaryInv{err: boom}, up: fakeSummaryUp{titles: []string{"U"}}},
		{fail: "upcoming", mail: fakeSummaryMail{toMe: []string{"T"}, starred: []string{"S"}}, inv: fakeSummaryInv{titles: []string{"I"}}, up: fakeSummaryUp{err: boom}},
	}
	want := map[string]string{"to-me": "T", "starred": "S", "invitations": "I", "upcoming": "U"}
	for _, tc := range cases {
		t.Run(tc.fail, func(t *testing.T) {
			e := newSummaryEnv(t)
			e.h.SetMailSource(tc.mail)
			e.h.SetInvitations(tc.inv)
			e.h.SetUpcoming(tc.up)
			code, body := e.get(quotaUser, false)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if strings.Contains(body, "secret-path") || strings.Contains(body, "disk on fire") {
				t.Error("the page shows the internal error text")
			}
			for _, id := range allSections {
				sec := section(t, body, id)
				if id == tc.fail {
					if !strings.Contains(sec, `data-summary-state="error"`) || rowCount(sec) != 0 {
						t.Errorf("failing section %s: %s", id, sec)
					}
					continue
				}
				if strings.Contains(sec, `data-summary-state="error"`) || !strings.Contains(sec, ">"+want[id]+"<") {
					t.Errorf("healthy section %s was affected: %s", id, sec)
				}
			}
		})
	}
}

func TestSummaryHandlerCapsRowsOfAnySource(t *testing.T) {
	e := newSummaryEnv(t)
	var many []string
	for i := range 25 {
		many = append(many, fmt.Sprintf("row%d", i))
	}
	e.h.SetMailSource(fakeSummaryMail{toMe: many, starred: many})
	e.h.SetInvitations(fakeSummaryInv{titles: many})
	e.h.SetUpcoming(fakeSummaryUp{titles: many})
	body := e.page(t)
	for _, id := range allSections {
		if n := rowCount(section(t, body, id)); n != summaryLimit {
			t.Errorf("section %s has %d rows, want %d", id, n, summaryLimit)
		}
	}
}

func TestSummaryIsolatesUsers(t *testing.T) {
	e := newSummaryEnv(t)
	e.mail(t, mailSeed{user: otherUser, subject: "B secret to me", toMe: true})
	e.mail(t, mailSeed{user: otherUser, subject: "B secret star", flags: store.FlagFlagged})
	e.place(t, otherUser, icsOpts{uid: "b-inv@example.org", summary: "B secret invite", attendee: otherEmail}.build("REQUEST"))
	e.event(t, otherUser, &bongov1.Event{Guid: "b-ev", Title: "B secret event", StartTime: summaryNow.Add(time.Hour).Unix()})
	e.mail(t, mailSeed{subject: "A visible", toMe: true})

	code, body := e.get(quotaUser, false)
	if code != http.StatusOK || !strings.Contains(body, "A visible") {
		t.Fatalf("user A page = %d, lacks own mail", code)
	}
	if strings.Contains(body, "B secret") {
		t.Error("user A sees user B's data")
	}
	// And B sees B's own, not A's.
	_, bBody := e.get(otherUser, false)
	for _, want := range []string{"B secret to me", "B secret star", "B secret invite", "B secret event"} {
		if !strings.Contains(bBody, want) {
			t.Errorf("user B page lacks %q", want)
		}
	}
	if strings.Contains(bBody, "A visible") {
		t.Error("user B sees user A's mail")
	}
}

func TestSummaryHTMXReturnsThePartial(t *testing.T) {
	e := newSummaryEnv(t)
	e.mail(t, mailSeed{subject: "Partial check", toMe: true})
	code, body := e.get(quotaUser, true)
	if code != http.StatusOK || !strings.Contains(body, "Partial check") {
		t.Fatalf("partial = %d: %s", code, body)
	}
	if strings.Contains(body, "<html") || strings.Contains(body, `id="sidebar"`) {
		t.Error("the HTMX response carries the page chrome")
	}
	_, full := e.get(quotaUser, false)
	if !strings.Contains(full, `id="sidebar"`) || !strings.Contains(full, `href="/summary"`) {
		t.Error("the full page lacks the sidebar and its Summary entry")
	}
}

func TestSummaryWithNoSourcesRendersEmptyStates(t *testing.T) {
	h := NewSummaryHandler(nil)
	req := withUserContext(httptest.NewRequest(http.MethodGet, "/summary", nil), quotaUser, quotaEmail)
	w := httptest.NewRecorder()
	h.Summary(w, req)
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), `data-summary-state="empty"`) != 4 {
		t.Fatalf("status %d, empty states %d", w.Code, strings.Count(w.Body.String(), `data-summary-state="empty"`))
	}
}

func TestSummaryEscapesAndCapsUntrustedText(t *testing.T) {
	e := newSummaryEnv(t)
	long := strings.Repeat("A", 500)
	e.mail(t, mailSeed{subject: "<script>alert(1)</script>" + long, from: "\"<img src=x onerror=y>\" <evil@x.test>", toMe: true})
	e.place(t, quotaUser, icsOpts{uid: "x@example.org", summary: "<b>bold</b>" + long}.build("REQUEST"))
	body := e.page(t)
	for _, bad := range []string{"<script>alert(1)", "<img src=x", "<b>bold</b>"} {
		if strings.Contains(body, bad) {
			t.Errorf("page contains unescaped %q", bad)
		}
	}
	if strings.Contains(body, strings.Repeat("A", 200)) {
		t.Error("a 500 character title was not capped")
	}
}
