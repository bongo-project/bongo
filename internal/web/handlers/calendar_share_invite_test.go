package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sentMail is one DeliverGenerated call.
type sentMail struct {
	userID, sessionEmail, from string
	rcpts                      []string
	raw                        []byte
}

type fakeSender struct {
	mu   sync.Mutex
	sent []sentMail
	fail error
	miss []string
}

func (f *fakeSender) DeliverGenerated(userID, sessionEmail, from string, rcpts []string, raw []byte) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMail{userID, sessionEmail, from, rcpts, raw})
	return f.miss, f.fail
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type fakePrefs map[string]string

func (f fakePrefs) Get(id string) *bongov1.UserPreferences {
	return &bongov1.UserPreferences{Locale: f[id]}
}

// inviteEnv is a shareEnv with an inviter whose worker the test drives
// directly, so nothing sleeps.
type inviteEnv struct {
	*shareEnv
	sender  *fakeSender
	inviter *ShareInviter
}

func newInviteEnv(t *testing.T) *inviteEnv {
	t.Helper()
	e := &inviteEnv{shareEnv: newShareEnv(t, true), sender: &fakeSender{}}
	e.inviter = NewShareInviter(e.sender, fakePrefs{"bob": "de"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.h.SetShareInviter(e.inviter)
	e.h.SetAuthservID("mail.d.test")
	return e
}

// drain runs every queued job on the calling goroutine.
func (e *inviteEnv) drain() {
	for {
		select {
		case j := <-e.inviter.jobs:
			e.inviter.process(j)
		default:
			return
		}
	}
}

func (e *inviteEnv) grantState(owner, cal, who string) string {
	g, _ := e.acl.GetCalendarGrant(owner, cal, who)
	if g == nil {
		return ""
	}
	return g.State
}

func TestShareCreatesPendingGrantAndInvites(t *testing.T) {
	e := newInviteEnv(t)
	if rr := e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelRead); rr.Code != http.StatusOK {
		t.Fatalf("share = %d %s", rr.Code, rr.Body.String())
	}
	if got := e.grantState("alice", e.calID, "bob"); got != store.GrantStatePending {
		t.Fatalf("new grant state = %q, want pending", got)
	}
	// The request path sent nothing; the worker does.
	if e.sender.count() != 0 {
		t.Fatal("invitation sent on the request path")
	}
	e.drain()
	if e.sender.count() != 1 {
		t.Fatalf("invitations sent = %d, want 1", e.sender.count())
	}
	m := e.sender.sent[0]
	if m.userID != "alice" || m.from != "alice@d.test" || m.sessionEmail != "alice@d.test" || len(m.rcpts) != 1 || m.rcpts[0] != "bob@d.test" {
		t.Errorf("delivery = %+v", m)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(m.raw)))
	if err != nil {
		t.Fatal(err)
	}
	// Bob's stored locale is German, not the owner's.
	subject := msg.Header.Get("Subject")
	if !strings.Contains(subject, "alice@d.test") || !strings.Contains(subject, "Work") || !strings.Contains(subject, "geteilt") {
		t.Errorf("subject = %q", subject)
	}
	body, _ := io.ReadAll(msg.Body)
	if !strings.Contains(string(body), "https://mail.d.test/calendar/shares") {
		t.Errorf("body lacks the link: %q", body)
	}
}

func TestShareInviteFallsBackToEnglish(t *testing.T) {
	e := newInviteEnv(t)
	e.inviter.prefs = fakePrefs{}
	e.shareLevel("alice", e.calID, "bob@d.test", "")
	e.drain()
	if e.sender.count() != 1 {
		t.Fatalf("sent = %d", e.sender.count())
	}
	msg, _ := mail.ReadMessage(strings.NewReader(string(e.sender.sent[0].raw)))
	if got := msg.Header.Get("Subject"); !strings.Contains(got, "has shared the calendar") {
		t.Errorf("fallback subject = %q, want English", got)
	}
}

// Only a newly created grant sends mail: a level change or a repost must not
// make the form a way to send repeated emails.
func TestShareInviteOnlyForNewGrant(t *testing.T) {
	e := newInviteEnv(t)
	e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelRead)
	e.drain()
	e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelRead)  // same level
	e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelWrite) // level change
	e.drain()
	if e.sender.count() != 1 {
		t.Errorf("invitations = %d, want 1", e.sender.count())
	}
	if len(e.inviter.jobs) != 0 {
		t.Error("jobs left queued")
	}
}

// A failed send is best-effort: the grant stands, pending.
func TestShareInviteFailureKeepsGrant(t *testing.T) {
	for name, sender := range map[string]*fakeSender{
		"error":       {fail: errors.New("spool down")},
		"not reached": {miss: []string{"bob@d.test"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newInviteEnv(t)
			e.sender = sender
			e.inviter.sender = sender
			if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusOK {
				t.Fatalf("share = %d", rr.Code)
			}
			e.drain()
			if sender.count() != 1 {
				t.Fatalf("delivery attempts = %d", sender.count())
			}
			if got := e.grantState("alice", e.calID, "bob"); got != store.GrantStatePending {
				t.Errorf("grant state after failed send = %q, want pending", got)
			}
		})
	}
}

func TestShareWithoutInviterOrWithFullQueueKeepsGrant(t *testing.T) {
	e := newInviteEnv(t)
	e.h.SetShareInviter(nil)
	if rr := e.share("alice", e.calID, "bob@d.test"); rr.Code != http.StatusOK {
		t.Fatalf("no inviter: share = %d", rr.Code)
	}
	if got := e.grantState("alice", e.calID, "bob"); got != store.GrantStatePending {
		t.Errorf("state = %q", got)
	}

	e = newInviteEnv(t)
	for range shareInviteQueueSize {
		if !e.inviter.Submit(shareInviteJob{}) {
			t.Fatal("queue refused early")
		}
	}
	// Does not block, and the grant is created.
	done := make(chan int, 1)
	go func() { done <- e.share("alice", e.calID, "bob@d.test").Code }()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Errorf("full queue: share = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("share blocked on a full invitation queue")
	}
	if got := e.grantState("alice", e.calID, "bob"); got != store.GrantStatePending {
		t.Errorf("state = %q", got)
	}
}

func TestShareInviteRunDrainsOnCancel(t *testing.T) {
	e := newInviteEnv(t)
	e.share("alice", e.calID, "bob@d.test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { e.inviter.Run(ctx); close(done) }()
	<-done
	if e.sender.count() != 1 || !e.inviter.idle() {
		t.Errorf("sent = %d idle = %v after drain", e.sender.count(), e.inviter.idle())
	}
}

// A level change keeps the grant's state (the batch-2 gap), pending or accepted.
func TestShareLevelChangeKeepsState(t *testing.T) {
	for _, state := range []string{store.GrantStatePending, store.GrantStateAccepted} {
		t.Run(state, func(t *testing.T) {
			e := newInviteEnv(t)
			if err := e.acl.SetCalendarGrant("alice", e.calID, "bob", store.GrantLevelRead, state, "alice", e.now); err != nil {
				t.Fatal(err)
			}
			if rr := e.shareLevel("alice", e.calID, "bob@d.test", store.GrantLevelWrite); rr.Code != http.StatusOK {
				t.Fatalf("share = %d", rr.Code)
			}
			g, _ := e.acl.GetCalendarGrant("alice", e.calID, "bob")
			if g.State != state || g.Level != store.GrantLevelWrite {
				t.Errorf("grant = %+v, want state %s level write", g, state)
			}
		})
	}
}

// Every negative outcome is byte-identical, whatever the address is.
func TestShareRefusalsIdentical(t *testing.T) {
	e := newInviteEnv(t)
	var first *httptest.ResponseRecorder
	for _, target := range []string{"nobody@d.test", "not an address", "alice@d.test", "Bob <bob@d.test>"} {
		rr := e.share("alice", e.calID, target)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%q = %d", target, rr.Code)
		}
		if first == nil {
			first = rr
			continue
		}
		if rr.Body.String() != first.Body.String() {
			t.Errorf("%q body %q differs from %q", target, rr.Body.String(), first.Body.String())
		}
	}
	// At the cap.
	rr := e.share("alice", e.fullID, "bob@d.test")
	if rr.Code != http.StatusBadRequest || rr.Body.String() != first.Body.String() {
		t.Errorf("full calendar = %d %q", rr.Code, rr.Body.String())
	}
	if e.sender.count() != 0 || len(e.inviter.jobs) != 0 {
		t.Error("a refused share queued mail")
	}
}

// The calendar name and owner label are user-controlled: neither may inject a
// header.
func TestInviteMessageHasNoInjectedHeader(t *testing.T) {
	sender := &fakeSender{}
	s := NewShareInviter(sender, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.send(shareInviteJob{
		ownerID: "alice", ownerEmail: "alice@d.test", granteeID: "bob", recipient: "bob@d.test",
		calendarName: "Work\r\nBcc: evil@x.test\r\nX-Injected: 1", host: "mail.d.test",
	})
	if sender.count() != 1 {
		t.Fatal("not sent")
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(sender.sent[0].raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"Bcc", "X-Injected"} {
		if v := msg.Header.Get(h); v != "" {
			t.Errorf("injected header %s = %q", h, v)
		}
	}
	if strings.Contains(string(sender.sent[0].raw), "\r\nBcc:") {
		t.Error("raw message carries a Bcc line")
	}
}

func TestLinkHost(t *testing.T) {
	cases := []struct {
		name, configured, reqHost, want string
	}{
		{"configured wins over the request", "mail.d.test", "evil.test", "mail.d.test"},
		{"dev falls back to the request", "", "localhost:8080", "localhost:8080"},
		{"forged request host refused", "", "evil.test/x@y", ""},
		{"configured but invalid is not replaced", "bad host", "evil.test", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &CalendarHandler{authservID: tc.configured}
			req := httptest.NewRequest("POST", "/", nil)
			req.Host = tc.reqHost
			if got := h.linkHost(req); got != tc.want {
				t.Errorf("linkHost = %q, want %q", got, tc.want)
			}
		})
	}
	// The invitation never uses the request host when one is configured.
	e := newInviteEnv(t)
	rr := e.shareLevel("alice", e.calID, "bob@d.test", "")
	if rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	select {
	case j := <-e.inviter.jobs:
		if j.host != "mail.d.test" {
			t.Errorf("job host = %q", j.host)
		}
	default:
		t.Error("no invitation queued on the configured host")
	}
}

// answer posts to AcceptShare or DeclineShare as caller.
func (e *inviteEnv) answer(accept bool, caller string, form url.Values) *httptest.ResponseRecorder {
	req := withRoute(sFormReq(http.MethodPost, "/calendar/shares/x", form), caller, nil)
	rr := httptest.NewRecorder()
	if accept {
		e.h.AcceptShare(rr, req)
	} else {
		e.h.DeclineShare(rr, req)
	}
	return rr
}

func (e *inviteEnv) form() url.Values {
	return url.Values{"owner": {"alice"}, "calendar": {e.calID}}
}

func TestAcceptAndDecline(t *testing.T) {
	t.Run("accept makes the grant authorise", func(t *testing.T) {
		e := newInviteEnv(t)
		e.share("alice", e.calID, "bob@d.test")
		if lvl, _ := e.acl.CalendarAccessLevel("alice", e.calID, "bob"); lvl != "" {
			t.Fatalf("pending grant authorises %q", lvl)
		}
		if rr := e.answer(true, "bob", e.form()); rr.Code != http.StatusOK {
			t.Fatalf("accept = %d %s", rr.Code, rr.Body.String())
		}
		if lvl, _ := e.acl.CalendarAccessLevel("alice", e.calID, "bob"); lvl != store.GrantLevelFreeBusy {
			t.Errorf("accepted level = %q", lvl)
		}
	})
	t.Run("decline deletes and the owner sees it disappear", func(t *testing.T) {
		e := newInviteEnv(t)
		e.share("alice", e.calID, "bob@d.test")
		if rr := e.answer(false, "bob", e.form()); rr.Code != http.StatusOK {
			t.Fatalf("decline = %d", rr.Code)
		}
		grants, _ := e.acl.ListCalendarGrants("alice", e.calID)
		if len(grants) != 0 {
			t.Errorf("owner still sees %+v", grants)
		}
	})
	t.Run("another user cannot answer for the grantee", func(t *testing.T) {
		for _, accept := range []bool{true, false} {
			e := newInviteEnv(t)
			e.share("alice", e.calID, "bob@d.test")
			// carol posts alice's grant for bob, naming bob as grantee too.
			f := e.form()
			f.Set("grantee", "bob")
			f.Set("user", "bob")
			rr := e.answer(accept, "carol", f)
			if rr.Code != http.StatusNotFound {
				t.Errorf("accept=%v: carol = %d, want 404", accept, rr.Code)
			}
			if got := e.grantState("alice", e.calID, "bob"); got != store.GrantStatePending {
				t.Errorf("accept=%v: bob's grant = %q after carol's request", accept, got)
			}
		}
	})
	t.Run("absent grant is 404 and writes nothing", func(t *testing.T) {
		e := newInviteEnv(t)
		e.h.acl = store.NewACLStore(e.ps, failingApplier{})
		for _, accept := range []bool{true, false} {
			if rr := e.answer(accept, "bob", e.form()); rr.Code != http.StatusNotFound {
				t.Errorf("accept=%v: = %d, want 404", accept, rr.Code)
			}
		}
	})
	t.Run("malformed owner or calendar is 404", func(t *testing.T) {
		e := newInviteEnv(t)
		e.share("alice", e.calID, "bob@d.test")
		for _, f := range []url.Values{{}, {"owner": {"alice"}}, {"owner": {"alice/x"}, "calendar": {e.calID}}} {
			if rr := e.answer(true, "bob", f); rr.Code != http.StatusNotFound {
				t.Errorf("%v = %d", f, rr.Code)
			}
		}
	})
	t.Run("an accepted grant cannot be declined or re-accepted here", func(t *testing.T) {
		e := newInviteEnv(t)
		e.share("alice", e.calID, "bob@d.test")
		e.answer(true, "bob", e.form())
		if rr := e.answer(false, "bob", e.form()); rr.Code != http.StatusNotFound {
			t.Errorf("decline of accepted = %d", rr.Code)
		}
		if got := e.grantState("alice", e.calID, "bob"); got != store.GrantStateAccepted {
			t.Errorf("state = %q", got)
		}
	})
	t.Run("write failures are 500", func(t *testing.T) {
		for _, accept := range []bool{true, false} {
			e := newInviteEnv(t)
			e.share("alice", e.calID, "bob@d.test")
			e.h.acl = store.NewACLStore(e.ps, failingApplier{})
			if rr := e.answer(accept, "bob", e.form()); rr.Code != http.StatusInternalServerError {
				t.Errorf("accept=%v failing store = %d, want 500", accept, rr.Code)
			}
		}
	})
}

func TestShareInvitationsPageListsOnlyOwnPending(t *testing.T) {
	e := newInviteEnv(t)
	e.share("alice", e.calID, "bob@d.test")
	if err := e.acl.SetCalendarGrant("alice", e.fullID, "bob", store.GrantLevelRead, store.GrantStateAccepted, "alice", e.now); err != nil {
		t.Fatal(err)
	}
	get := func(who string) string {
		rr := httptest.NewRecorder()
		e.h.ShareInvitations(rr, withRoute(httptest.NewRequest("GET", "/calendar/shares", nil), who, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET = %d", rr.Code)
		}
		return rr.Body.String()
	}
	body := get("bob")
	for _, want := range []string{"alice@d.test", "Work", `hx-post="/calendar/shares/accept"`, `hx-post="/calendar/shares/decline"`, `name="_csrf"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Count(body, `hx-post="/calendar/shares/accept"`) != 1 {
		t.Error("an accepted grant is listed as pending")
	}
	if strings.Contains(get("carol"), "Work") {
		t.Error("another user sees bob's invitation")
	}
}

func TestSettingsCardShowsGrantState(t *testing.T) {
	e := newInviteEnv(t)
	e.share("alice", e.calID, "bob@d.test")
	rr := httptest.NewRecorder()
	e.h.CalendarSettings(rr, withRoute(httptest.NewRequest("GET", "/calendar/settings", nil), "alice", nil))
	if !strings.Contains(rr.Body.String(), "Pending") {
		t.Error("settings card does not show the pending state")
	}
}

// The public link offer is rendered whenever the calendar is published, never
// depending on how a share attempt ended.
func TestSettingsCardOffersPublicLinkWhenPublished(t *testing.T) {
	e := newInviteEnv(t)
	render := func() string {
		rr := httptest.NewRecorder()
		e.h.CalendarSettings(rr, withRoute(httptest.NewRequest("GET", "/calendar/settings", nil), "alice", nil))
		return rr.Body.String()
	}
	marker := `hx-post="/calendar/` + e.calID + `/publish/send"`
	if strings.Contains(render(), marker) {
		t.Error("offered for an unpublished calendar")
	}
	if err := e.calendars.Mutate("alice", e.calID, func(c *bongov1.Calendar) error {
		c.Published = true
		c.PublishToken = make([]byte, publishTokenBytes)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(render(), marker) {
		t.Error("not offered for a published calendar")
	}
}

func (e *inviteEnv) sendLink(caller, calID, to string) *httptest.ResponseRecorder {
	req := withRoute(sFormReq(http.MethodPost, "/calendar/"+calID+"/publish/send", url.Values{"send_to": {to}}), caller, map[string]string{"calendarID": calID})
	rr := httptest.NewRecorder()
	e.h.SendPublicLink(rr, req)
	return rr
}

func (e *inviteEnv) publishCal(t *testing.T, owner, calID string) {
	t.Helper()
	if err := e.calendars.Mutate(owner, calID, func(c *bongov1.Calendar) error {
		c.Published = true
		c.PublishToken = []byte("0123456789abcdef0123456789abcdef")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSendPublicLink(t *testing.T) {
	e := newInviteEnv(t)
	e.h.SetInviteSender(e.sender)

	if rr := e.sendLink("alice", e.calID, "friend@ext.test"); rr.Code != http.StatusConflict {
		t.Errorf("unpublished = %d, want 409", rr.Code)
	}
	e.publishCal(t, "alice", e.calID)
	// Unpublished with a stale token still left on the record: refused too.
	if err := e.calendars.Mutate("alice", e.calID, func(c *bongov1.Calendar) error { c.Published = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if rr := e.sendLink("alice", e.calID, "friend@ext.test"); rr.Code != http.StatusConflict {
		t.Errorf("unpublished with a token = %d, want 409", rr.Code)
	}
	e.publishCal(t, "alice", e.calID)
	e.publishCal(t, "bob", e.bobCalID)
	if rr := e.sendLink("bob", e.calID, "friend@ext.test"); rr.Code != http.StatusNotFound {
		t.Errorf("non-owner = %d, want 404", rr.Code)
	}
	for _, bad := range []string{"", "not an address", "A <a@ext.test>"} {
		if rr := e.sendLink("alice", e.calID, bad); rr.Code != http.StatusBadRequest {
			t.Errorf("%q = %d, want 400", bad, rr.Code)
		}
	}
	if e.sender.count() != 0 {
		t.Fatal("a refused request sent mail")
	}
	rr := e.sendLink("alice", e.calID, "friend@ext.test")
	if rr.Code != http.StatusOK {
		t.Fatalf("send = %d %s", rr.Code, rr.Body.String())
	}
	if e.sender.count() != 1 {
		t.Fatalf("sent = %d", e.sender.count())
	}
	m := e.sender.sent[0]
	if m.from != "alice@d.test" || m.rcpts[0] != "friend@ext.test" {
		t.Errorf("delivery = %+v", m)
	}
	if !strings.Contains(string(m.raw), "https://mail.d.test/pub/calendar/alice/"+e.calID+"/") {
		t.Errorf("message lacks the feed link on the configured host: %s", m.raw)
	}
	// No grant is created.
	if grants, _ := e.acl.ListCalendarGrants("alice", e.calID); len(grants) != 0 {
		t.Errorf("send-link created grants: %+v", grants)
	}
	// Delivery failure is reported, not hidden.
	e.sender.miss = []string{"friend@ext.test"}
	if rr := e.sendLink("alice", e.calID, "friend@ext.test"); rr.Code != http.StatusBadGateway {
		t.Errorf("not reached = %d, want 502", rr.Code)
	}
}

func TestSendPublicLinkRateLimited(t *testing.T) {
	e := newInviteEnv(t)
	e.h.SetInviteSender(e.sender)
	e.publishCal(t, "alice", e.calID)
	var last int
	for range 21 {
		last = e.sendLink("alice", e.calID, "friend@ext.test").Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("21st request = %d, want 429", last)
	}
	if e.sender.count() != 20 {
		t.Errorf("sent = %d, want 20", e.sender.count())
	}
}

func TestSendPublicLinkNeverLogsLink(t *testing.T) {
	// The handler logs through slog's default logger; a failure path must not
	// carry the link or the address.
	var buf strings.Builder
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	e := newInviteEnv(t)
	e.sender.miss = []string{"friend@ext.test"}
	e.h.SetInviteSender(e.sender)
	e.publishCal(t, "alice", e.calID)
	e.sendLink("alice", e.calID, "friend@ext.test")
	if out := buf.String(); strings.Contains(out, "/pub/calendar/") || strings.Contains(out, "friend@ext.test") || strings.Contains(out, "MDEyMzQ1") {
		t.Errorf("log leaks the link or address: %s", out)
	}
}

// Every outcome of the send-link form is shown to the user: a failure carries
// a translated message in its body (the page script puts it in the form's
// status area, since htmx does not swap an error response), and success
// answers a translated confirmation fragment instead of a page refresh.
func TestSendPublicLinkReportsToUser(t *testing.T) {
	locale := func(req *http.Request, l string) *http.Request {
		return req.WithContext(middleware.WithLocale(req.Context(), l))
	}
	send := func(e *inviteEnv, caller, calID, to, l string) *httptest.ResponseRecorder {
		req := locale(withRoute(sFormReq(http.MethodPost, "/calendar/"+calID+"/publish/send", url.Values{"send_to": {to}}), caller, map[string]string{"calendarID": calID}), l)
		rr := httptest.NewRecorder()
		e.h.SendPublicLink(rr, req)
		return rr
	}
	e := newInviteEnv(t)
	e.h.SetInviteSender(e.sender)

	cases := []struct {
		name string
		code int
		key  string
		run  func(l string) *httptest.ResponseRecorder
	}{
		{"unpublished", http.StatusConflict, "calendar.publicLinkNotPublished", func(l string) *httptest.ResponseRecorder {
			return send(e, "alice", e.calID, "friend@ext.test", l)
		}},
		{"unknown calendar", http.StatusNotFound, "calendar.publicLinkNotFound", func(l string) *httptest.ResponseRecorder {
			return send(e, "alice", "nope", "friend@ext.test", l)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, l := range []string{"en", "de"} {
				rr := tc.run(l)
				want := i18n.T(l, tc.key)
				if rr.Code != tc.code || strings.TrimSpace(rr.Body.String()) != want {
					t.Errorf("%s: %d %q, want %d %q", l, rr.Code, rr.Body.String(), tc.code, want)
				}
			}
		})
	}

	e.publishCal(t, "alice", e.calID)
	if rr := send(e, "alice", e.calID, "not an address", "de"); rr.Code != http.StatusBadRequest || strings.TrimSpace(rr.Body.String()) != i18n.T("de", "calendar.publicLinkInvalidAddress") {
		t.Errorf("invalid address: %d %q", rr.Code, rr.Body.String())
	}
	e.sender.miss = []string{"friend@ext.test"}
	if rr := send(e, "alice", e.calID, "friend@ext.test", "en"); rr.Code != http.StatusBadGateway || strings.TrimSpace(rr.Body.String()) != i18n.T("en", "calendar.publicLinkNotDelivered") {
		t.Errorf("not delivered: %d %q", rr.Code, rr.Body.String())
	}
	e.sender.miss = nil
	rr := send(e, "alice", e.calID, "friend@ext.test", "en")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), i18n.T("en", "calendar.publicLinkSent")) {
		t.Errorf("success: %d %q", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("HX-Refresh") != "" {
		t.Error("success refreshed the page, which would wipe the confirmation")
	}
}
