package handlers

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	internalmail "github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	unsubToken  = "SECRETTOKEN9f3a"
	unsubOneURL = "https://news.example.org/u/" + unsubToken + "?id=SECRETQUERY"
	oneClickHdr = "List-Unsubscribe: <" + unsubOneURL + ">, <mailto:leave@news.example.org?subject=Leave%20me&body=please&cc=extra@x.test>\r\n" +
		"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"
)

type fakePoster struct {
	mu    sync.Mutex
	urls  []*url.URL
	err   error
	calls int
}

func (p *fakePoster) PostOneClick(_ context.Context, u *url.URL) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.urls = append(p.urls, u)
	return p.err
}

type unsubFixture struct {
	t         *testing.T
	h         *MailHandler
	router    http.Handler
	mailboxes *store.MailboxStore
	metadata  *store.MetadataManager
	bs        *store.BlobStore
	ps        *store.PebbleStore
	poster    *fakePoster
	logs      *bytes.Buffer
	verified  bool // result of the DKIM check
	verifies  int
}

func newUnsubFixture(t *testing.T) *unsubFixture {
	t.Helper()
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatal(err)
	}
	md, err := store.NewMetadataManager(dir + "/meta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { md.Close() })
	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	applier.mailboxes = mailboxes

	f := &unsubFixture{t: t, mailboxes: mailboxes, metadata: md, bs: bs, ps: ps, poster: &fakePoster{}, logs: &bytes.Buffer{}, verified: true}
	h := NewMailHandler(ps, bs, mailboxes, slog.New(slog.NewTextHandler(f.logs, nil)))
	h.SetApplier(applier)
	h.SetMetadata(md)
	h.SetUnsubscribe(f.poster, func(context.Context, []byte) bool { f.verifies++; return f.verified }, internalmail.NewRateLimiter(100))
	f.h = h

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user := req.Header.Get("X-Test-User")
			if user == "" {
				user = "user1"
			}
			next.ServeHTTP(w, req.WithContext(middleware.WithUserContext(req.Context(), user, user+"@example.com")))
		})
	})
	r.Get("/mail/view/lists", h.ListMailingLists)
	r.Get("/mail/view/lists/{listID}/unsubscribe", h.ListUnsubscribe)
	r.Get("/mail/view/lists/{listID}", h.ListMailingList)
	r.Get("/mail/{mailbox}/{uid}", h.ShowMessage)
	r.Post("/mail/{mailbox}/{uid}/unsubscribe", h.Unsubscribe)
	f.router = r
	return f
}

type unsubMsg struct {
	user, mailbox, guid, headers, listID string
	spam                                 float32
	at                                   int64
}

func (f *unsubFixture) deliver(m unsubMsg) uint32 {
	f.t.Helper()
	if m.user == "" {
		m.user = "user1"
	}
	raw := "From: News <news@news.example.org>\r\nTo: user1@example.com\r\nSubject: subj-" + m.guid +
		"\r\nMessage-ID: <" + m.guid + "@news.example.org>\r\n" + m.headers + "\r\nbody text\r\n"
	hash, err := f.bs.Put([]byte(raw))
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.mailboxes.CreateMailbox(m.user, m.mailbox); err != nil && !strings.Contains(err.Error(), "exist") {
		f.t.Fatal(err)
	}
	meta := &bongov1.MailMeta{
		Guid: m.guid, Subject: "subj-" + m.guid, From: "news@news.example.org", To: []string{m.user + "@example.com"},
		ReceivedAt: m.at, SpamScore: m.spam, ListId: m.listID, RawMessage: &bongov1.BlobRef{Hash: hash},
	}
	uid, err := f.mailboxes.DeliverMessage(m.user, m.mailbox, meta, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.metadata.IndexMail(m.user, m.guid, meta.Subject, meta.From, m.at, 1); err != nil {
		f.t.Fatal(err)
	}
	mine := map[string]bool{m.user + "@example.com": true}
	if err := f.metadata.SetMailHeaders(m.user, m.guid, store.MailHeaderFields(meta, mine)); err != nil {
		f.t.Fatal(err)
	}
	return uid
}

func (f *unsubFixture) do(method, path, user string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func TestShowMessageUnsubscribePanel(t *testing.T) {
	tests := []struct {
		name         string
		mailbox      string
		headers      string
		spam         float32
		noPoster     bool
		wantOneClick bool
		wantLink     bool
		wantMailto   bool
		wantHint     bool
	}{
		{"one-click offer", "INBOX", oneClickHdr, 0, false, true, false, false, false},
		{"one-click without poster becomes a link", "INBOX", oneClickHdr, 0, true, false, true, false, false},
		{"https without post header", "INBOX", "List-Unsubscribe: <" + unsubOneURL + ">\r\n", 0, false, false, true, false, false},
		{"mailto only", "INBOX", "List-Unsubscribe: <mailto:leave@news.example.org>\r\n", 0, false, false, false, true, false},
		{"http only", "INBOX", "List-Unsubscribe: <http://news.example.org/u/1>\r\n", 0, false, false, false, false, false},
		{"no header", "INBOX", "", 0, false, false, false, false, false},
		{"junk", "Junk", oneClickHdr, 0, false, false, false, false, true},
		{"below junk", "Junk/Old", oneClickHdr, 0, false, false, false, false, true},
		{"trash is not junk", "Trash", oneClickHdr, 0, false, true, false, false, false},
		{"tagged spam in inbox", "INBOX", oneClickHdr, 0.9, false, false, false, false, true},
		{"spam without offer shows nothing", "INBOX", "", 0.9, false, false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newUnsubFixture(t)
			if tt.noPoster {
				f.h.unsubPoster = nil
			}
			uid := f.deliver(unsubMsg{mailbox: tt.mailbox, guid: "g1", headers: tt.headers, spam: tt.spam})
			w := f.do(http.MethodGet, fmt.Sprintf("/mail/%s/%d", url.PathEscape(strings.ReplaceAll(tt.mailbox, "/", "%2F")), uid), "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			check := func(name string, got, want bool) {
				if got != want {
					t.Errorf("%s = %v, want %v", name, got, want)
				}
			}
			check("one-click form", strings.Contains(body, `data-action="unsubscribe"`), tt.wantOneClick)
			check("link disclosure", strings.Contains(body, `class="unsubscribe-link"`), tt.wantLink)
			check("mailto link", strings.Contains(body, `data-action="unsubscribe-mailto"`), tt.wantMailto)
			check("spam hint", strings.Contains(body, "Report it as spam instead"), tt.wantHint)
			// The URI path and query are never a form field or a link on a
			// one-click control; the link variant shows the validated URL.
			if tt.wantOneClick && (strings.Contains(body, unsubToken) || strings.Contains(body, "SECRETQUERY")) {
				t.Error("one-click control leaks the URI")
			}
			if tt.wantHint && (strings.Contains(body, unsubToken) || strings.Contains(body, "leave@news.example.org")) {
				t.Error("blocked message still carries the offer")
			}
			if tt.wantLink {
				if !strings.Contains(body, `rel="noopener noreferrer"`) || !strings.Contains(body, "news.example.org") {
					t.Errorf("link panel lacks rel or host: %s", body)
				}
			}
		})
	}
}

func TestUnsubscribePostSuccess(t *testing.T) {
	f := newUnsubFixture(t)
	uid := f.deliver(unsubMsg{mailbox: "INBOX", guid: "g1", headers: oneClickHdr})
	// A url field in the form is ignored: the stored blob decides.
	w := f.do(http.MethodPost, fmt.Sprintf("/mail/INBOX/%d/unsubscribe", uid), "", url.Values{"url": {"https://attacker.example/steal"}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if f.poster.calls != 1 {
		t.Fatalf("poster calls = %d, want 1", f.poster.calls)
	}
	if got := f.poster.urls[0].String(); got != unsubOneURL {
		t.Errorf("posted to %q, want the URI from the stored message", got)
	}
	if !strings.Contains(w.Body.String(), "Unsubscribe request sent") || !strings.Contains(w.Body.String(), `id="unsubscribe-panel"`) {
		t.Errorf("body = %s", w.Body.String())
	}
	logs := f.logs.String()
	for _, secret := range []string{unsubToken, "SECRETQUERY", "attacker.example"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs leak %q: %s", secret, logs)
		}
	}
	if !strings.Contains(logs, "host=news.example.org") || !strings.Contains(logs, "guid=g1") || !strings.Contains(logs, "user=user1") {
		t.Errorf("logs lack user, guid or host: %s", logs)
	}
}

func TestUnsubscribePostFailureShowsFixedText(t *testing.T) {
	f := newUnsubFixture(t)
	f.poster.err = fmt.Errorf("unsubscribing at news.example.org: status 500 <script>REMOTEBODY</script>")
	uid := f.deliver(unsubMsg{mailbox: "INBOX", guid: "g1", headers: oneClickHdr})
	w := f.do(http.MethodPost, fmt.Sprintf("/mail/INBOX/%d/unsubscribe", uid), "", url.Values{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "did not go through") || strings.Contains(body, "REMOTEBODY") {
		t.Errorf("body = %s", body)
	}
	if strings.Contains(f.logs.String(), unsubToken) || strings.Contains(f.logs.String(), "SECRETQUERY") {
		t.Errorf("logs leak the URI: %s", f.logs.String())
	}
}

func TestUnsubscribePostRefusals(t *testing.T) {
	tests := []struct {
		name    string
		user    string // requesting user; "" is the owner
		mailbox string
		headers string
		spam    float32
		verify  bool
		want    int
	}{
		{"other user", "user2", "INBOX", oneClickHdr, 0, true, http.StatusNotFound},
		{"junk", "", "Junk", oneClickHdr, 0, true, http.StatusForbidden},
		{"below junk", "", "Junk/Old", oneClickHdr, 0, true, http.StatusForbidden},
		{"spam score at threshold", "", "INBOX", oneClickHdr, 0.5, true, http.StatusForbidden},
		{"no one-click offer", "", "INBOX", "List-Unsubscribe: <" + unsubOneURL + ">\r\n", 0, true, http.StatusNotFound},
		{"two List-Unsubscribe headers", "", "INBOX", "List-Unsubscribe: <https://evil.example/u>\r\n" + oneClickHdr, 0, true, http.StatusNotFound},
		{"mailto only", "", "INBOX", "List-Unsubscribe: <mailto:a@news.example.org>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n", 0, true, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newUnsubFixture(t)
			f.verified = tt.verify
			uid := f.deliver(unsubMsg{mailbox: tt.mailbox, guid: "g1", headers: tt.headers, spam: tt.spam})
			// user2 exists but holds no such message: the lookup reads the
			// session user's keyspace only, so user1's uid is a 404 for them.
			f.deliver(unsubMsg{user: "user2", mailbox: "Other", guid: "g2", headers: tt.headers})
			path := fmt.Sprintf("/mail/%s/%d/unsubscribe", strings.ReplaceAll(tt.mailbox, "/", "%2F"), uid)
			w := f.do(http.MethodPost, path, tt.user, url.Values{})
			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
			if f.poster.calls != 0 {
				t.Errorf("poster called %d times for a refused request", f.poster.calls)
			}
		})
	}
}

func TestUnsubscribeUnsignedFallsBackToLink(t *testing.T) {
	f := newUnsubFixture(t)
	f.verified = false
	uid := f.deliver(unsubMsg{mailbox: "INBOX", guid: "g1", headers: oneClickHdr})
	w := f.do(http.MethodPost, fmt.Sprintf("/mail/INBOX/%d/unsubscribe", uid), "", url.Values{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if f.poster.calls != 0 {
		t.Fatal("poster called without a valid signature")
	}
	if f.verifies != 1 {
		t.Errorf("verifier ran %d times, want 1", f.verifies)
	}
	body := w.Body.String()
	if !strings.Contains(body, "could not check the sender") || !strings.Contains(body, `rel="noopener noreferrer"`) || !strings.Contains(body, "news.example.org") {
		t.Errorf("body = %s", body)
	}
}

func TestUnsubscribeRateLimit(t *testing.T) {
	f := newUnsubFixture(t)
	f.h.unsubLimiter = internalmail.NewRateLimiter(2)
	uid := f.deliver(unsubMsg{mailbox: "INBOX", guid: "g1", headers: oneClickHdr})
	path := fmt.Sprintf("/mail/INBOX/%d/unsubscribe", uid)
	for i := range 2 {
		if w := f.do(http.MethodPost, path, "", url.Values{}); w.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i, w.Code)
		}
	}
	callsBefore, verifiesBefore := f.poster.calls, f.verifies
	// The budget is charged before the message is looked up: a uid that does
	// not exist is a 429, not a 404, and nothing is read, verified or sent.
	for _, p := range []string{path, "/mail/INBOX/9999/unsubscribe"} {
		w := f.do(http.MethodPost, p, "", url.Values{})
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("%s status = %d, want 429", p, w.Code)
		}
		if w.Header().Get("Retry-After") == "" {
			t.Errorf("%s: no Retry-After", p)
		}
	}
	if f.poster.calls != callsBefore || f.verifies != verifiesBefore {
		t.Error("a limited request still reached the verifier or the poster")
	}
	// Another user has their own budget.
	f.deliver(unsubMsg{user: "user2", mailbox: "INBOX", guid: "g2", headers: oneClickHdr})
	if w := f.do(http.MethodPost, "/mail/INBOX/1/unsubscribe", "user2", url.Values{}); w.Code != http.StatusOK {
		t.Errorf("other user status = %d", w.Code)
	}
}

func TestMailingListUnsubscribeUsesNewestMessage(t *testing.T) {
	f := newUnsubFixture(t)
	const list = "<news.example.org>"
	// An older message in INBOX and a newer one in Junk: Junk is excluded from
	// the list views, so INBOX wins.
	inbox := f.deliver(unsubMsg{mailbox: "INBOX", guid: "old", headers: oneClickHdr + "List-Id: News <news.example.org>\r\n", listID: "news.example.org", at: 100})
	f.deliver(unsubMsg{mailbox: "Junk", guid: "new", headers: oneClickHdr + "List-Id: News <news.example.org>\r\n", listID: "news.example.org", at: 200})
	_ = list

	w := f.do(http.MethodGet, "/mail/view/lists/news.example.org/unsubscribe", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if want := fmt.Sprintf("/mail/INBOX/%d/unsubscribe", inbox); !strings.Contains(w.Body.String(), want) {
		t.Errorf("page does not act on the INBOX message %q: %s", want, w.Body.String())
	}

	// The overview offers the action per list.
	ov := f.do(http.MethodGet, "/mail/view/lists", "", nil)
	if !strings.Contains(ov.Body.String(), `data-action="list-unsubscribe"`) || !strings.Contains(ov.Body.String(), "/mail/view/lists/news.example.org/unsubscribe") {
		t.Errorf("overview lacks the per-list action: %s", ov.Body.String())
	}

	// A list with nothing to show, or an id outside the stored character set.
	for _, p := range []string{"/mail/view/lists/other.example/unsubscribe", "/mail/view/lists/%3Cscript%3E/unsubscribe"} {
		if w := f.do(http.MethodGet, p, "", nil); w.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", p, w.Code)
		}
	}
	// Another user sees nothing of this list.
	if w := f.do(http.MethodGet, "/mail/view/lists/news.example.org/unsubscribe", "user2", nil); w.Code != http.StatusNotFound {
		t.Errorf("other user status = %d, want 404", w.Code)
	}
}

func TestMailingListUnsubscribeSpamNewest(t *testing.T) {
	f := newUnsubFixture(t)
	f.deliver(unsubMsg{mailbox: "INBOX", guid: "m1", headers: oneClickHdr, listID: "news.example.org", at: 100, spam: 0.9})
	w := f.do(http.MethodGet, "/mail/view/lists/news.example.org/unsubscribe", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Report it as spam instead") || strings.Contains(w.Body.String(), "/unsubscribe\"") && strings.Contains(w.Body.String(), `data-action="unsubscribe"`) {
		t.Errorf("status %d body %s", w.Code, w.Body.String())
	}
}

func TestMailingListUnsubscribeEscapesListName(t *testing.T) {
	f := newUnsubFixture(t)
	// A list name is header text: markup in it must not reach the page raw.
	f.deliver(unsubMsg{mailbox: "INBOX", guid: "m1", headers: oneClickHdr, listID: `"<img src=x onerror=alert(1)>" <news.example.org>`, at: 100})
	w := f.do(http.MethodGet, "/mail/view/lists/news.example.org/unsubscribe", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "<img src=x") {
		t.Errorf("list name rendered as markup: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "&lt;img") {
		t.Errorf("list name missing from the page, the test proves nothing: %s", w.Body.String())
	}
}

func TestUnsubscribeBlocked(t *testing.T) {
	tests := []struct {
		name    string
		mailbox string
		score   float32
		tag     func() float64
		want    bool
	}{
		{"inbox clean", "INBOX", 0, nil, false},
		{"junk", "Junk", 0, nil, true},
		{"below junk", "Junk/x", 0, nil, true},
		{"junkyard is not junk", "Junkyard", 0, nil, false},
		{"trash", "Trash", 0, nil, false},
		{"default threshold", "INBOX", 0.5, nil, true},
		{"custom threshold below", "INBOX", 0.7, func() float64 { return 0.8 }, false},
		{"custom threshold reached", "INBOX", 0.8, func() float64 { return 0.8 }, true},
		{"zero threshold falls back", "INBOX", 0.6, func() float64 { return 0 }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unsubscribeBlocked(tt.mailbox, &bongov1.MailMeta{SpamScore: tt.score}, tt.tag); got != tt.want {
				t.Errorf("blocked = %v, want %v", got, tt.want)
			}
		})
	}
}
