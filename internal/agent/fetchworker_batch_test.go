package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/fetch"
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const fwUserB = "22222222-2222-2222-2222-222222222222"

// A user whose four accounts all hang holds one slot, not every slot: another
// user's account runs in the same pass, and the hanging user's accounts never
// run two at once.
func TestFetchOneRunPerUserAndSlowUserDoesNotStarveOthers(t *testing.T) {
	e := newFWEnv(t)
	e.w.users = func() ([]string, error) { return []string{fwUser, fwUserB}, nil }
	e.profile = func(id string) (string, bool) { return id + "@example.test", true }
	e.remote.tarpit = "tarpit-user"
	e.remote.tarpitGate = make(chan struct{})
	e.addMsgs(1, "b")

	for i := range 4 {
		e.addAcct(func(a *bongov1.FetchAccount) { a.Username = "tarpit-user"; a.Host = fmt.Sprintf("slow%d.example", i) })
	}
	b := &bongov1.FetchAccount{
		Guid: e.newGUID(), Protocol: fetch.ProtocolIMAP, Security: fetch.SecurityTLS, Host: "imap.example",
		Port: 993, Username: "fast-user", IntervalMinutes: 15, Generation: 1, CreatedAt: 1, UpdatedAt: 1,
	}
	cred, err := e.kr.Seal(fwUserB, b, fwPassword)
	if err != nil {
		t.Fatal(err)
	}
	b.Credential = cred
	if err := e.fs.Put(fwUserB, b); err != nil {
		t.Fatal(err)
	}

	finished := make(chan struct{})
	go func() { e.w.pass(context.Background()); close(finished) }()
	deadline := time.Now().Add(5 * time.Second)
	for e.deliver.count() == 0 {
		if time.Now().After(deadline) {
			close(e.remote.tarpitGate)
			<-finished
			t.Fatal("the other user's account did not run while the first user's accounts hang")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st, ok, _ := e.fs.GetState(fwUserB, b.Guid); !ok && st == nil {
		// The state is written right after the run; give it a moment.
		time.Sleep(50 * time.Millisecond)
	}
	close(e.remote.tarpitGate)
	<-finished
	if got := e.remote.tarpitMaxSeen.Load(); got != 1 {
		t.Fatalf("the hanging user had %d runs at once, want 1", got)
	}
	// All four of that user's accounts still ran, once each, one after another.
	tarpitDials := 0
	for _, p := range e.remote.params {
		if p.Username == "tarpit-user" {
			tarpitDials++
		}
	}
	if tarpitDials != 4 {
		t.Fatalf("hanging accounts dialled %d times, want 4 (once each)", tarpitDials)
	}
}

func TestFetchTooLargeSkipsPerRunAreCapped(t *testing.T) {
	e := newFWEnv(t)
	e.w.maxMessage = 1000
	e.w.tooLargeMax = 3
	a := e.addAcct()
	for i := 1; i <= 10; i++ {
		e.remote.msgs = append(e.remote.msgs, fwMsg{id: uint32(i), body: "Subject: big\r\n\r\nx\r\n", size: 5000}) //nolint:gosec // small ids
	}
	e.remote.msgs = append(e.remote.msgs, fwMsg{id: 11, body: "Subject: ok\r\n\r\nx\r\n"})
	e.pass()
	st := e.mustState(a)
	if st.LastRunTooLarge != 3 {
		t.Fatalf("too-large skips in one run = %d, want the cap of 3", st.LastRunTooLarge)
	}
	if got := len(e.seen(a)); got != 3 {
		t.Fatalf("seen records = %d, want 3", got)
	}
	if e.app.wrote("/fetchseen/") != 3 {
		t.Fatalf("seen writes = %d", e.app.wrote("/fetchseen/"))
	}
	// The rest is a backlog: the next run is due in five minutes and goes on.
	if want := e.now.Add(fetchBacklogDelay).Unix(); st.NextRunAt != want {
		t.Fatalf("next run %d, want the backlog delay %d", st.NextRunAt, want)
	}
	e.advance(6 * time.Minute)
	e.pass()
	if got := len(e.seen(a)); got != 6 {
		t.Fatalf("after the second run seen = %d, want 6", got)
	}
}

func TestFetchSeenCapStopsImportingAndShowsCode(t *testing.T) {
	e := newFWEnv(t)
	e.w.seenMax = 5
	a := e.addAcct()
	for i := 1; i <= 8; i++ {
		e.remote.msgs = append(e.remote.msgs, fwMsg{id: uint32(i), body: fmt.Sprintf("Subject: m%d\r\n\r\nx\r\n", i)}) //nolint:gosec // small ids
	}
	// Five handled messages are already on record.
	for i := 1; i <= 5; i++ {
		hash := store.IMAPSeenHash("INBOX", 1, uint32(i)) //nolint:gosec // small ids
		if err := e.fs.MarkSeen(fwUser, a.Guid, hash, &bongov1.FetchSeen{At: 1, Status: store.FetchSeenImported, UidValidity: 1}); err != nil {
			t.Fatal(err)
		}
	}
	e.remote.uidValidity = 1
	e.pass()
	if e.deliver.count() != 0 {
		t.Fatalf("imported %d messages over the seen cap", e.deliver.count())
	}
	st := e.mustState(a)
	if st.LastError != fetch.CodeSeenLimit {
		t.Fatalf("last_error = %q, want %q", st.LastError, fetch.CodeSeenLimit)
	}
	// Under the cap again, the account imports as usual.
	e.w.seenMax = 0
	e.advance(25 * time.Hour)
	e.pass()
	if e.deliver.count() != 3 {
		t.Fatalf("imported %d after the cap was lifted, want 3", e.deliver.count())
	}
}

// The fetch page in the production router has the real connection tester: a
// test of a loopback host reaches the safe dialer and is refused as
// connect_failed. Without SetTester in agent.go the page would answer 503.
func TestFetchPageWiredWithTester(t *testing.T) {
	cfg := fetchWiringConfig(t, "dev", randomFetchKey(t))
	isolatePorts(t, cfg)
	runWiringAgent(t, cfg, testLogger(), nil)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)
	login, err := client.PostForm(base+"/login", url.Values{"email": {"admin@localhost"}, "password": {"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = login.Body.Close()
	resp, err := client.PostForm(base+"/settings/fetch/test", url.Values{
		"protocol": {"imap"}, "security": {"tls"}, "host": {"127.0.0.1"}, "username": {"u"}, "password": {"p"},
		"folder": {"INBOX"}, "interval": {"60"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d (503 means no tester is wired): %s", resp.StatusCode, b)
	}
	if want := i18n.T("en", "fetch.err.connect_failed"); !strings.Contains(string(b), want) {
		t.Fatalf("page lacks %q", want)
	}
}

// At the seen cap the run still prunes records of messages that are gone from
// the folder, so the next run is under the cap and imports.
func TestFetchSeenCapPrunesVanishedMessages(t *testing.T) {
	e := newFWEnv(t)
	e.w.seenMax = 5
	a := e.addAcct()
	e.remote.uidValidity = 1
	for i := 1; i <= 6; i++ {
		e.remote.msgs = append(e.remote.msgs, fwMsg{id: uint32(i), body: fmt.Sprintf("Subject: m%d\r\n\r\nx\r\n", i)}) //nolint:gosec // small ids
	}
	// Messages 1-3 are listed and handled; 101 and 102 are handled and gone.
	for _, uid := range []uint32{1, 2, 3, 101, 102} {
		hash := store.IMAPSeenHash("INBOX", 1, uid)
		if err := e.fs.MarkSeen(fwUser, a.Guid, hash, &bongov1.FetchSeen{At: 1, Status: store.FetchSeenImported, UidValidity: 1}); err != nil {
			t.Fatal(err)
		}
	}
	e.pass()
	if e.deliver.count() != 0 {
		t.Fatalf("imported %d at the cap", e.deliver.count())
	}
	if got := e.mustState(a).LastError; got != fetch.CodeSeenLimit {
		t.Fatalf("last_error = %q", got)
	}
	if got := len(e.seen(a)); got != 3 {
		t.Fatalf("seen records after the capped run = %d, want 3 (the two vanished ones pruned)", got)
	}
	e.advance(25 * time.Hour)
	e.pass()
	if e.deliver.count() != 3 {
		t.Fatalf("imported %d on the next run, want 3", e.deliver.count())
	}
}
