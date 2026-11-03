package handlers

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// viewFixture is a mail handler over real stores, routed as agent.go routes
// the header views. The request's user is chosen by the X-Test-User header.
type viewFixture struct {
	t         *testing.T
	router    http.Handler
	mailboxes *store.MailboxStore
	metadata  *store.MetadataManager
	contacts  *store.ContactStore
	prefs     *store.PreferenceStore
	logs      *bytes.Buffer // what the mail handler logged
}

func newViewFixture(t *testing.T) *viewFixture {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	meta, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { meta.Close() })

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	applier.mailboxes = mailboxes
	contacts := store.NewContactStore(ps, applier)

	logs := &bytes.Buffer{}
	h := NewMailHandler(ps, nil, mailboxes, slog.New(slog.NewTextHandler(logs, nil)))
	h.SetApplier(applier)
	h.SetMetadata(meta)
	h.SetContacts(contacts)
	prefs := store.NewPreferenceStore(ps, applier)
	h.SetPreferences(prefs)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user := req.Header.Get("X-Test-User")
			if user == "" {
				user = "user1"
			}
			ctx := middleware.WithUserContext(req.Context(), user, user+"@example.com")
			if loc := req.Header.Get("X-Test-Locale"); loc != "" {
				ctx = middleware.WithLocale(ctx, loc)
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/mail/to-me", h.ListToMe)
	r.Get("/mail/from-contacts", h.ListFromContacts)
	r.Get("/mail/lists", h.ListMailingLists)
	r.Get("/mail/lists/{listID}", h.ListMailingList)
	r.Get("/mail/{mailbox}", h.ListMessages)
	r.Post("/mail/{mailbox}/bulk", h.BulkAction)

	return &viewFixture{t: t, router: r, mailboxes: mailboxes, metadata: meta, contacts: contacts, prefs: prefs, logs: logs}
}

type seedMsg struct {
	user, mailbox, guid string
	from                string
	to                  []string
	listID              string
	at                  int64
	flags               uint32
}

// deliver stores a message and indexes it the way the FSM does.
func (f *viewFixture) deliver(m seedMsg) uint32 {
	f.t.Helper()
	if m.user == "" {
		m.user = "user1"
	}
	if m.from == "" {
		m.from = "stranger@elsewhere.test"
	}
	if err := f.mailboxes.CreateMailbox(m.user, m.mailbox); err != nil && !strings.Contains(err.Error(), "exist") {
		f.t.Fatalf("CreateMailbox: %v", err)
	}
	meta := &bongov1.MailMeta{
		Guid: m.guid, Subject: "subj-" + m.guid, From: m.from, To: m.to, ListId: m.listID, ReceivedAt: m.at,
	}
	uid, err := f.mailboxes.DeliverMessage(m.user, m.mailbox, meta, m.flags)
	if err != nil {
		f.t.Fatalf("DeliverMessage: %v", err)
	}
	if err := f.metadata.IndexMail(m.user, m.guid, meta.Subject, m.from, m.at, 1); err != nil {
		f.t.Fatal(err)
	}
	mine := map[string]bool{m.user + "@example.com": true}
	if err := f.metadata.SetMailHeaders(m.user, m.guid, store.MailHeaderFields(meta, mine)); err != nil {
		f.t.Fatal(err)
	}
	return uid
}

func (f *viewFixture) get(path, user string, htmx bool) (int, string) {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func (f *viewFixture) post(path, user string, form url.Values) (int, string) {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// subjectsInOrder returns the "subj-" subjects found in body, in page order.
func subjectsInOrder(body string) []string {
	type hit struct {
		pos  int
		name string
	}
	var hits []hit
	seen := map[string]bool{}
	for i := 0; i < len(body); {
		j := strings.Index(body[i:], "subj-")
		if j < 0 {
			break
		}
		start := i + j
		end := start + len("subj-")
		for end < len(body) && (body[end] == '-' || body[end] == '_' || body[end] >= '0' && body[end] <= '9' || body[end] >= 'a' && body[end] <= 'z' || body[end] >= 'A' && body[end] <= 'Z') {
			end++
		}
		name := body[start:end]
		if !seen[name] {
			seen[name] = true
			hits = append(hits, hit{start, name})
		}
		i = end
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].pos < hits[b].pos })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func seedViewMail(f *viewFixture) {
	me := []string{"Me <user1@example.com>"}
	other := []string{"someone@else.test"}
	const list = "<golang.example.org>"
	// To me, spread over folders; dates do not follow folder scan order.
	f.deliver(seedMsg{mailbox: "INBOX", guid: "A", to: me, at: 100})
	f.deliver(seedMsg{mailbox: "Projects", guid: "B", to: me, at: 300})
	f.deliver(seedMsg{mailbox: "Archive", guid: "C", to: me, at: 200})
	// Matching mail in excluded folders must not show.
	f.deliver(seedMsg{mailbox: "Junk", guid: "Jk", to: me, at: 900, listID: list, from: "friend@example.com"})
	f.deliver(seedMsg{mailbox: "Trash", guid: "Tr", to: me, at: 901, listID: list, from: "friend@example.com"})
	f.deliver(seedMsg{mailbox: "Sent", guid: "Se", to: me, at: 902, listID: list, from: "friend@example.com"})
	f.deliver(seedMsg{mailbox: "Drafts", guid: "Dr", to: me, at: 903, listID: list, from: "friend@example.com"})
	// Not to me.
	f.deliver(seedMsg{mailbox: "INBOX", guid: "H", to: other, at: 400})
	// List and contact mail.
	f.deliver(seedMsg{mailbox: "INBOX", guid: "L1", to: other, at: 110, listID: list, from: "Friend <Friend@Example.com>"})
	f.deliver(seedMsg{mailbox: "Projects", guid: "L2", to: other, at: 310, listID: list})
}

func TestHeaderViewsListFromRealFoldersNewestFirst(t *testing.T) {
	f := newViewFixture(t)
	seedViewMail(f)
	tests := []struct {
		name, path string
		want       []string
	}{
		{"to me", "/mail/to-me", []string{"subj-B", "subj-C", "subj-A"}},
		{"one list", "/mail/lists/golang.example.org", []string{"subj-L2", "subj-L1"}},
		{"list through the encoded mailbox name", "/mail/lists%2Fgolang.example.org", []string{"subj-L2", "subj-L1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, htmx := range []bool{false, true} {
				code, body := f.get(tt.path, "", htmx)
				if code != http.StatusOK {
					t.Fatalf("status = %d: %s", code, body)
				}
				if got := subjectsInOrder(body); !equalStrings(got, tt.want) {
					t.Errorf("htmx=%v subjects = %v, want %v", htmx, got, tt.want)
				}
			}
		})
	}
}

func TestFromContactsFollowsContactsWithoutReindex(t *testing.T) {
	f := newViewFixture(t)
	seedViewMail(f)

	_, body := f.get("/mail/from-contacts", "", false)
	if got := subjectsInOrder(body); len(got) != 0 {
		t.Fatalf("before the contact exists the view shows %v", got)
	}
	if _, err := f.contacts.Create("user1", &bongov1.Contact{DisplayName: "Friend", Email: "friend@example.com"}); err != nil {
		t.Fatal(err)
	}
	// Nothing re-indexed: the same SQLite rows now match. Junk, Trash, Sent and
	// Drafts mail from the same contact stays out.
	_, body = f.get("/mail/from-contacts", "", false)
	if got := subjectsInOrder(body); !equalStrings(got, []string{"subj-L1"}) {
		t.Fatalf("after adding the contact the view shows %v, want [subj-L1]", got)
	}
	// Another user's contact book does not widen this one.
	if _, err := f.contacts.Create("user2", &bongov1.Contact{Email: "stranger@elsewhere.test"}); err != nil {
		t.Fatal(err)
	}
	_, body = f.get("/mail/from-contacts", "", false)
	if got := subjectsInOrder(body); !equalStrings(got, []string{"subj-L1"}) {
		t.Fatalf("user2's contact changed user1's view: %v", got)
	}
}

func TestHeaderViewPaginatesLikeAMailbox(t *testing.T) {
	f := newViewFixture(t)
	me := []string{"user1@example.com"}
	for i := range 30 {
		f.deliver(seedMsg{mailbox: "INBOX", guid: fmt.Sprintf("p%02d", i), to: me, at: int64(1000 + i), listID: "<pages.example.org>"})
	}
	for _, path := range []string{"/mail/to-me", "/mail/lists/pages.example.org"} {
		t.Run(path, func(t *testing.T) {
			_, p1 := f.get(path, "", false)
			got := subjectsInOrder(p1)
			if len(got) != 25 || got[0] != "subj-p29" || got[24] != "subj-p05" {
				t.Fatalf("page 1 = %v", got)
			}
			if !strings.Contains(p1, "page=2") && !strings.Contains(p1, "Page 1 of 2") && !strings.Contains(p1, "1 of 2") {
				t.Errorf("page 1 has no pagination to page 2")
			}
			_, p2 := f.get(path+"?page=2", "", false)
			if got := subjectsInOrder(p2); !equalStrings(got, []string{"subj-p04", "subj-p03", "subj-p02", "subj-p01", "subj-p00"}) {
				t.Fatalf("page 2 = %v", got)
			}
		})
	}
	// Page 2 of a list is requested as /mail/lists%2F<id>?page=2&partial=1 and
	// reaches ListMessages: it must render the same list view.
	code, part := f.get("/mail/lists%2Fpages.example.org?page=2&partial=1", "", false)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got := subjectsInOrder(part); len(got) != 5 || got[0] != "subj-p04" {
		t.Fatalf("partial page 2 = %v", got)
	}
	if strings.Contains(part, "<html") {
		t.Error("partial response carries a full page")
	}
}

func TestMailingListsOverview(t *testing.T) {
	f := newViewFixture(t)
	other := []string{"someone@else.test"}
	f.deliver(seedMsg{mailbox: "INBOX", guid: "g1", to: other, at: 10, listID: `"Go Users" <golang.example.org>`})
	f.deliver(seedMsg{mailbox: "Projects", guid: "g2", to: other, at: 20, listID: `"Go Users" <golang.example.org>`})
	f.deliver(seedMsg{mailbox: "Trash", guid: "g3", to: other, at: 30, listID: `"Go Users" <golang.example.org>`})
	f.deliver(seedMsg{mailbox: "Trash", guid: "r1", to: other, at: 30, listID: `"Rust" <rust.example.org>`})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "x1", to: other, at: 40, listID: `"<b>Bold</b>" <html.example.org>`})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "n1", to: other, at: 50, listID: `<noname.example.org>`})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "z1", to: other, at: 60})

	for _, htmx := range []bool{false, true} {
		code, body := f.get("/mail/lists", "", htmx)
		if code != http.StatusOK {
			t.Fatalf("status %d: %s", code, body)
		}
		if !strings.Contains(body, "Go Users") {
			t.Errorf("htmx=%v: list name missing", htmx)
		}
		if !strings.Contains(body, `href="/mail/lists/golang.example.org"`) {
			t.Errorf("htmx=%v: group does not link to its view", htmx)
		}
		if !strings.Contains(body, `aria-label="Messages: 2"`) {
			t.Errorf("htmx=%v: Go Users must count 2 (INBOX and Projects; the Trash copy is excluded): %s", htmx, body)
		}
		if strings.Contains(body, "rust.example.org") || strings.Contains(body, "Rust") {
			t.Errorf("htmx=%v: a list with only Trash mail must not be shown", htmx)
		}
		if strings.Contains(body, "<b>Bold</b>") || !strings.Contains(body, "&lt;b&gt;Bold&lt;/b&gt;") {
			t.Errorf("htmx=%v: list name is not escaped", htmx)
		}
		// A list without a name is shown by its id.
		if !strings.Contains(body, ">noname.example.org<") && !strings.Contains(body, "noname.example.org\n") {
			t.Errorf("htmx=%v: nameless list not shown by id", htmx)
		}
		if htmx == strings.Contains(body, "<html") {
			t.Errorf("htmx=%v: wrong response shape", htmx)
		}
	}
}

func TestMailingListsEmptyState(t *testing.T) {
	f := newViewFixture(t)
	code, body := f.get("/mail/lists", "", false)
	if code != http.StatusOK || !strings.Contains(body, "No mailing lists") {
		t.Fatalf("status %d, body lacks the empty state: %s", code, body)
	}
}

func TestInvalidListIDIsNotFound(t *testing.T) {
	f := newViewFixture(t)
	f.deliver(seedMsg{mailbox: "INBOX", guid: "g1", listID: "<golang.example.org>"})
	for _, path := range []string{
		"/mail/lists/Bad%20Id",
		"/mail/lists/UPPER.example.org",
		"/mail/lists/..%2F..%2Fadmin",
		"/mail/lists/a%3Cscript%3E",
	} {
		if code, _ := f.get(path, "", false); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
}

func TestListIDIsNeverStoredOrLinkedWhenUnsafe(t *testing.T) {
	f := newViewFixture(t)
	f.deliver(seedMsg{mailbox: "INBOX", guid: "e1", listID: `Evil <x/../y.example.org>`})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "e2", listID: `Evil <a"onmouseover="x.example.org>`})
	_, body := f.get("/mail/lists", "", false)
	if strings.Contains(body, "Evil") || strings.Contains(body, "onmouseover") {
		t.Errorf("an unsafe List-Id reached the overview: %s", body)
	}
}

func TestHeaderViewsAreIsolatedBetweenUsers(t *testing.T) {
	f := newViewFixture(t)
	const list = "<shared.example.org>"
	f.deliver(seedMsg{user: "user1", mailbox: "INBOX", guid: "u1a", to: []string{"user1@example.com"}, listID: list, at: 10})
	f.deliver(seedMsg{user: "user1", mailbox: "Private", guid: "u1p", to: []string{"user1@example.com"}, listID: "<secret.example.org>", at: 20})
	f.deliver(seedMsg{user: "user2", mailbox: "INBOX", guid: "u2a", to: []string{"user2@example.com"}, listID: list, at: 15})

	// The shared list: each user sees their own copy only.
	for user, want := range map[string]string{"user1": "subj-u1a", "user2": "subj-u2a"} {
		_, body := f.get("/mail/lists/shared.example.org", user, false)
		if got := subjectsInOrder(body); !equalStrings(got, []string{want}) {
			t.Errorf("%s sees %v on the shared list, want [%s]", user, got, want)
		}
	}
	// The overview: user2 has one message and has never heard of user1's list.
	_, body := f.get("/mail/lists", "user2", false)
	if strings.Contains(body, "secret.example.org") {
		t.Error("user2's overview names user1's private list")
	}
	if !strings.Contains(body, `aria-label="Messages: 1"`) {
		t.Errorf("user2's count for the shared list must be 1: %s", body)
	}
	// Asking for the other user's list id directly finds nothing.
	_, body = f.get("/mail/lists/secret.example.org", "user2", false)
	if got := subjectsInOrder(body); len(got) != 0 {
		t.Errorf("user2 reads user1's list: %v", got)
	}
	_, body = f.get("/mail/to-me", "user2", false)
	if got := subjectsInOrder(body); !equalStrings(got, []string{"subj-u2a"}) {
		t.Errorf("user2 to-me = %v", got)
	}

	// A bulk request naming user1's folder and UID, sent as user2, changes nothing.
	code, _ := f.post("/mail/to-me/bulk", "user2", url.Values{"action": {"flag"}, "targets": {"1:Private"}})
	if code != http.StatusBadRequest {
		t.Errorf("bulk on another user's mailbox = %d, want 400", code)
	}
	if msg, _ := f.mailboxes.GetMessage("user1", "Private", 1); msg.Flags&store.FlagFlagged != 0 {
		t.Error("user2 flagged user1's message")
	}
	// Naming INBOX/1 as user2 acts on user2's INBOX, never user1's.
	if code, _ := f.post("/mail/to-me/bulk", "user2", url.Values{"action": {"flag"}, "targets": {"1:INBOX"}}); code != http.StatusOK {
		t.Fatalf("bulk on own mailbox = %d", code)
	}
	m1, _ := f.mailboxes.GetMessage("user1", "INBOX", 1)
	m2, _ := f.mailboxes.GetMessage("user2", "INBOX", 1)
	if m1.Flags&store.FlagFlagged != 0 || m2.Flags&store.FlagFlagged == 0 {
		t.Errorf("flags user1=%d user2=%d: only user2's message may change", m1.Flags, m2.Flags)
	}
}

// UIDs are only unique within a mailbox. Two to-me messages share UID 1; an
// action on one must change that one only.
func TestHeaderViewActionsActOnTheRealMailbox(t *testing.T) {
	f := newViewFixture(t)
	me := []string{"user1@example.com"}
	if uid := f.deliver(seedMsg{mailbox: "INBOX", guid: "X", to: me, at: 100}); uid != 1 {
		t.Fatalf("uid = %d", uid)
	}
	if uid := f.deliver(seedMsg{mailbox: "Projects", guid: "Y", to: me, at: 200}); uid != 1 {
		t.Fatalf("uid = %d", uid)
	}
	flagged := func(box string) bool {
		m, err := f.mailboxes.GetMessage("user1", box, 1)
		if err != nil {
			t.Fatal(err)
		}
		return m.Flags&store.FlagFlagged != 0
	}

	// The rows carry the real mailbox in every URL an action uses.
	_, body := f.get("/mail/to-me", "", false)
	for _, want := range []string{
		`data-sel-key="1:Projects"`, `data-sel-key="1:INBOX"`,
		`hx-post="/mail/Projects/1/flags"`, `hx-post="/mail/INBOX/1/flags"`,
		`href="/mail/Projects/1"`, `href="/mail/INBOX/1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rows lack %s", want)
		}
	}
	if strings.Contains(body, "/mail/to-me/1") {
		t.Error("a row URL still uses the view name")
	}

	// A plain UID is ambiguous in a header view, so it is not accepted even
	// though the same request flags the message in a real folder.
	if code, _ := f.post("/mail/to-me/bulk", "", url.Values{"action": {"flag"}, "uids": {"1"}}); code != http.StatusBadRequest {
		t.Errorf("uids-only bulk in a header view = %d, want 400", code)
	}
	if flagged("INBOX") || flagged("Projects") {
		t.Fatal("an ambiguous UID changed a message")
	}
	if code, _ := f.post("/mail/INBOX/bulk", "", url.Values{"action": {"unflag"}, "uids": {"1"}}); code != http.StatusOK {
		t.Errorf("uids in a real folder = %d, want 200", code)
	}

	// Flag the Projects message only.
	if code, body := f.post("/mail/to-me/bulk", "", url.Values{"action": {"flag"}, "targets": {"1:Projects"}}); code != http.StatusOK {
		t.Fatalf("bulk = %d %s", code, body)
	}
	if flagged("INBOX") || !flagged("Projects") {
		t.Fatalf("INBOX flagged=%v Projects flagged=%v, want only Projects", flagged("INBOX"), flagged("Projects"))
	}

	// Delete the INBOX one from the view: it lands in Trash, the other stays.
	if code, body := f.post("/mail/to-me/bulk", "", url.Values{"action": {"delete"}, "targets": {"1:INBOX"}}); code != http.StatusOK {
		t.Fatalf("bulk delete = %d %s", code, body)
	}
	if _, err := f.mailboxes.GetMessage("user1", "Projects", 1); err != nil {
		t.Errorf("the Projects message went with it: %v", err)
	}
	if _, body := f.get("/mail/to-me", "", false); !equalStrings(subjectsInOrder(body), []string{"subj-Y"}) {
		t.Errorf("view after delete = %v", subjectsInOrder(body))
	}

	// A mailbox that is not one of the user's own is dropped.
	if code, _ := f.post("/mail/to-me/bulk", "", url.Values{"action": {"flag"}, "targets": {"1:Nope", "x:INBOX", "1"}}); code != http.StatusBadRequest {
		t.Errorf("malformed targets = %d, want 400", code)
	}
}

// The bulk-action log line counts the messages acted on, also in a header view
// where the request carries targets and no uids.
func TestHeaderViewBulkActionLogsTargetCount(t *testing.T) {
	f := newViewFixture(t)
	u1 := f.deliver(seedMsg{mailbox: "INBOX", guid: "g1", to: []string{"user1@example.com"}, at: 100})
	u2 := f.deliver(seedMsg{mailbox: "INBOX", guid: "g2", to: []string{"user1@example.com"}, at: 200})
	f.deliver(seedMsg{mailbox: "INBOX", guid: "g3", to: []string{"user1@example.com"}, at: 300})
	f.logs.Reset()
	code, body := f.post("/mail/to-me/bulk", "", url.Values{"action": {"flag"}, "targets": {
		fmt.Sprintf("%d:INBOX", u1), fmt.Sprintf("%d:INBOX", u2),
	}})
	if code != http.StatusOK {
		t.Fatalf("bulk = %d %s", code, body)
	}
	if out := f.logs.String(); !strings.Contains(out, "web: bulk action") || !strings.Contains(out, "count=2") {
		t.Errorf("log line = %q, want count=2", out)
	}
}
