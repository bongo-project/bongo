package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/bongo-project/bongo/internal/security"
)

const fakeSecret = "fake-session-secret-0123456789abcdef"

type received struct {
	rcpt, id, xid string
	sum           string
	size          int
}

// fakeServer stands in for the control listener and the SMTP listener of a
// Bongo node, both on loopback.
type fakeServer struct {
	t      *testing.T
	admin  *httptest.Server
	smtpLn net.Listener
	// imap serves APPEND for the non-INBOX folders; it knows the accounts
	// this fake's control listener created.
	imap *imapFake

	mu        sync.Mutex
	users     map[string]string // email -> password as received
	passwords []string          // every password received, in order
	msgs      []received
	onMessage func(n int) // called with the count after each stored message
	flood     int         // answer this many DATA commands with 421
	forget    bool        // answer 201 but keep no account
	fail500   bool        // answer every create with 500
	badSig    atomic.Int64
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{t: t, users: map[string]string{}}
	f.imap = newIMAPFake(t, "plain")
	f.imap.lookup = func(email string) (string, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		pw, ok := f.users[email]
		return pw, ok
	}
	f.admin = httptest.NewServer(http.HandlerFunc(f.handleCreate))
	t.Cleanup(f.admin.Close)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.smtpLn = ln
	srv := smtp.NewServer(smtp.BackendFunc(func(*smtp.Conn) (smtp.Session, error) { return &fakeSession{f: f}, nil }))
	srv.Domain = "fake.test"
	srv.MaxMessageBytes = 16 << 20
	srv.ReadTimeout, srv.WriteTimeout = 30*time.Second, 30*time.Second
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

func (f *fakeServer) controlAddr() string { return strings.TrimPrefix(f.admin.URL, "http://") }
func (f *fakeServer) smtpAddr() string    { return f.smtpLn.Addr().String() }

func (f *fakeServer) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/internal/user/create" {
		http.NotFound(w, r)
		return
	}
	// The route's real check; it also restores the body.
	if !security.VerifyInternalRequest(r, []byte(fakeSecret)) {
		f.badSig.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Admin    bool   `json:"admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Password == "" || req.Admin {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail500 {
		http.Error(w, "boom "+req.Password, http.StatusInternalServerError)
		return
	}
	if _, ok := f.users[req.Email]; ok {
		http.Error(w, "exists", http.StatusConflict)
		return
	}
	if f.forget {
		w.WriteHeader(http.StatusCreated)
		return
	}
	f.users[req.Email] = req.Password
	f.passwords = append(f.passwords, req.Password)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"user_id":"x"}`))
}

type fakeSession struct {
	f    *fakeServer
	rcpt string
}

func (s *fakeSession) Reset()        {}
func (s *fakeSession) Logout() error { return nil }
func (s *fakeSession) Mail(string, *smtp.MailOptions) error {
	return nil
}

func (s *fakeSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.f.mu.Lock()
	_, ok := s.f.users[to]
	s.f.mu.Unlock()
	if !ok {
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "no such user"}
	}
	s.rcpt = to
	return nil
}

func (s *fakeSession) Data(r io.Reader) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f := s.f
	f.mu.Lock()
	if f.flood > 0 {
		f.flood--
		f.mu.Unlock()
		return &smtp.SMTPError{Code: 421, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "too many messages from your address"}
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		f.mu.Unlock()
		return err
	}
	sum := sha256.Sum256(raw)
	f.msgs = append(f.msgs, received{
		rcpt: s.rcpt, id: msg.Header.Get("Message-ID"), xid: msg.Header.Get("X-Loadgen-Id"),
		sum: hex.EncodeToString(sum[:]), size: len(raw),
	})
	n, hook := len(f.msgs), f.onMessage
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	return nil
}

func (f *fakeServer) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.msgs))
	for i, m := range f.msgs {
		out[i] = m.id
	}
	return out
}

// seedEnv is one configured run against a fakeServer.
type seedEnv struct {
	t     *testing.T
	f     *fakeServer
	cfg   Config
	state string
	deps  seedDeps
	dials atomic.Int64
}

func newSeedEnv(t *testing.T, f *fakeServer, mod func(*SeedOptions)) *seedEnv {
	t.Helper()
	o := testOptions()
	o.SizeMix = "2KB:60,40KB:40"
	o.FolderMix = "INBOX:80,Archive:20"
	o.Users, o.MessagesPerUser, o.Concurrency = 6, 8, 3
	o.ControlAddr, o.SMTPAddr = f.controlAddr(), f.smtpAddr()
	o.IMAPAddr, o.IMAPTLS = f.imap.addr(), imapTLSPlain
	e := &seedEnv{t: t, f: f, state: filepath.Join(t.TempDir(), "seed.state")}
	o.StateFile = e.state
	if mod != nil {
		mod(&o)
	}
	var allow []Target
	for _, a := range []string{f.controlAddr(), f.smtpAddr(), f.imap.addr()} {
		tg, err := parseTarget(a)
		if err != nil {
			t.Fatal(err)
		}
		allow = append(allow, tg)
	}
	e.cfg = Config{Allow: allow, TestDomain: "seed.test", Seed: o}
	base := defaultDial()
	e.deps = seedDeps{
		dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			e.dials.Add(1)
			return base(ctx, network, addr)
		},
		secret: []byte(fakeSecret), newPassword: NewPassword,
		backoff: time.Millisecond, tries: 6,
	}
	return e
}

func (e *seedEnv) run(ctx context.Context) (code int, stdout, stderr string) {
	e.t.Helper()
	if err := Validate(e.cfg); err != nil {
		e.t.Fatalf("test config refused by Validate: %v", err)
	}
	var out, errb bytes.Buffer
	code = seedMain(ctx, e.cfg, e.deps, &out, &errb)
	return code, out.String(), errb.String()
}

// planIDs are the Message-IDs of the INBOX messages in the plan, and how many
// messages it puts in other folders.
func planIDs(t *testing.T, o SeedOptions) (inbox []string, folders int) {
	t.Helper()
	spec, err := newPlanSpec("seed.test", o)
	if err != nil {
		t.Fatal(err)
	}
	for i := range spec.Users {
		for _, m := range spec.PlanUser(i).Messages {
			if m.Folder == inboxFolder {
				inbox = append(inbox, m.ID)
			} else {
				folders++
			}
		}
	}
	return inbox, folders
}

// checkFolders reads every user's folders back from the IMAP server and
// requires each planned non-INBOX message to be there exactly once.
func (e *seedEnv) checkFolders(im *imapFake) {
	e.t.Helper()
	spec, err := newPlanSpec("seed.test", e.cfg.Seed)
	if err != nil {
		e.t.Fatal(err)
	}
	st, err := openState(e.state, spec.header())
	if err != nil {
		e.t.Fatal(err)
	}
	defer st.Close()
	for i := range spec.Users {
		want := map[string][]string{}
		for _, mp := range spec.PlanUser(i).Messages {
			if mp.Folder != inboxFolder {
				want[mp.Folder] = append(want[mp.Folder], mp.ID)
			}
		}
		u := st.User(i)
		for folder, ids := range want {
			var got []string
			for _, m := range im.inspect(u.email, u.pw, folder) {
				got = append(got, m.ID)
			}
			sameSet(e.t, got, ids)
		}
		if n := len(im.inspect(u.email, u.pw, inboxFolder)); n != 0 {
			e.t.Errorf("user %d: %d messages in INBOX that APPEND must not have put there", i, n)
		}
	}
}

func sameSet(t *testing.T, got, want []string) {
	t.Helper()
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("%s delivered %d times", id, n)
		}
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
		if seen[w] == 0 {
			t.Errorf("%s never delivered", w)
		}
	}
	for g := range seen {
		if !wantSet[g] {
			t.Errorf("%s delivered but not in the plan", g)
		}
	}
}

func TestSeedEndToEnd(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, nil)
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	want, folders := planIDs(t, e.cfg.Seed)
	if len(want) == 0 || folders == 0 {
		t.Fatalf("test plan is degenerate: %d inbox, %d in folders", len(want), folders)
	}
	sameSet(t, f.ids(), want)
	if len(f.users) != 6 {
		t.Fatalf("%d users created, want 6", len(f.users))
	}
	for _, m := range f.msgs {
		if m.id != m.xid || m.id == "" {
			t.Errorf("Message-ID %q and X-Loadgen-Id %q differ", m.id, m.xid)
		}
		if _, ok := f.users[m.rcpt]; !ok {
			t.Errorf("delivered to unknown %s", m.rcpt)
		}
	}
	if !strings.Contains(stdout, "6 created") || !strings.Contains(stdout, fmt.Sprintf("%d appended to folders", folders)) {
		t.Errorf("summary: %s", stdout)
	}
	// The other folders are filled over IMAP, once each, and INBOX gets
	// nothing from APPEND: its mail came over SMTP.
	if got := f.imap.count("APPEND"); got != int64(folders) {
		t.Errorf("server saw %d APPENDs, want %d", got, folders)
	}
	e.checkFolders(f.imap)

	// The state file's password is the one the server holds.
	spec, _ := newPlanSpec("seed.test", e.cfg.Seed)
	st, err := openState(e.state, spec.header())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := range spec.Users {
		u := st.User(i)
		if u.pw == "" || u.pw != f.users[spec.email(i)] || !u.done {
			t.Errorf("user %d: state %+v does not match the server", i, u)
		}
	}
}

func TestSeedSamePlanSameMessages(t *testing.T) {
	var sums [2]map[string]string
	for run := range 2 {
		f := newFakeServer(t)
		e := newSeedEnv(t, f, nil)
		if code, _, stderr := e.run(context.Background()); code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		sums[run] = map[string]string{}
		for _, m := range f.msgs {
			sums[run][m.id] = m.sum
		}
	}
	if len(sums[0]) == 0 {
		t.Fatal("nothing delivered")
	}
	for id, s := range sums[0] {
		if sums[1][id] != s {
			t.Errorf("%s: bytes differ between two runs of one seed", id)
		}
	}
}

func TestSeedResumeAfterInterruptionCreatesNoDuplicates(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, func(o *SeedOptions) { o.FolderMix = "INBOX:1" })
	want, _ := planIDs(t, e.cfg.Seed)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const k = 7
	f.onMessage = func(n int) {
		if n == k {
			cancel()
		}
	}
	code, stdout, _ := e.run(ctx)
	if code != exitFail || !strings.Contains(stdout, "interrupted") {
		t.Fatalf("first run: exit %d, stdout %q", code, stdout)
	}
	first := f.ids()
	if len(first) < k || len(first) >= len(want) {
		t.Fatalf("interruption was not mid-stream: %d of %d delivered", len(first), len(want))
	}
	if len(first) > k+e.cfg.Seed.Concurrency {
		t.Errorf("%d delivered after cancelling at %d: in-flight drain exceeded concurrency", len(first), k)
	}
	partial := false
	perUser := map[string]int{}
	for _, m := range f.msgs {
		perUser[m.rcpt]++
	}
	for _, n := range perUser {
		if n > 0 && n < e.cfg.Seed.MessagesPerUser {
			partial = true
		}
	}
	if !partial {
		t.Fatal("no user was interrupted part-way")
	}

	f.onMessage = nil
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("second run: exit %d, stderr %s", code, stderr)
	}
	sameSet(t, f.ids(), want)
	if !strings.Contains(stdout, "0 resent") {
		t.Errorf("a clean interrupt should resend nothing: %s", stdout)
	}
	// A third run has nothing left to do.
	n := len(f.ids())
	if code, _, _ := e.run(context.Background()); code != exitOK || len(f.ids()) != n {
		t.Fatalf("completed seed was not a no-op: exit %d, %d -> %d messages", code, n, len(f.ids()))
	}
}

func TestSeedResumeAfterHardKillResendsUnconfirmed(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, func(o *SeedOptions) { o.FolderMix = "INBOX:1"; o.Users = 1; o.MessagesPerUser = 3 })
	spec, _ := newPlanSpec("seed.test", e.cfg.Seed)
	// State as a kill -9 leaves it: the account was created, message 0 is
	// done, message 1 was being sent.
	st, err := openState(e.state, spec.header())
	if err != nil {
		t.Fatal(err)
	}
	f.users[spec.email(0)] = "earlier-pw"
	for _, err := range []error{
		st.Pending(0, spec.email(0), "earlier-pw"), st.UserDone(0),
		st.Sending(0, 0), st.MsgDone(0, 0), st.Sending(0, 1),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	ids := f.ids()
	if len(ids) != 2 || ids[0] != spec.messageID(0, 1) || ids[1] != spec.messageID(0, 2) {
		t.Fatalf("delivered %v, want messages 1 and 2 only", ids)
	}
	if !strings.Contains(stdout, "1 resent") {
		t.Errorf("unconfirmed send not reported: %s", stdout)
	}
}

func TestSeedConflictHandling(t *testing.T) {
	t.Run("pending record from an earlier run keeps the stored password", func(t *testing.T) {
		f := newFakeServer(t)
		e := newSeedEnv(t, f, func(o *SeedOptions) { o.Users = 2; o.MessagesPerUser = 2; o.FolderMix = "INBOX:1" })
		spec, _ := newPlanSpec("seed.test", e.cfg.Seed)
		st, err := openState(e.state, spec.header())
		if err != nil {
			t.Fatal(err)
		}
		// The server accepted the create, then the run died before user-done.
		f.users[spec.email(0)] = "stored-pw-0"
		if err := st.Pending(0, spec.email(0), "stored-pw-0"); err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
		code, stdout, stderr := e.run(context.Background())
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if !strings.Contains(stdout, "1 already existed") {
			t.Errorf("summary: %s", stdout)
		}
		st, err = openState(e.state, spec.header())
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		if u := st.User(0); !u.done || u.pw != "stored-pw-0" || f.users[spec.email(0)] != "stored-pw-0" {
			t.Fatalf("stored password no longer matches the account: %+v", u)
		}
		if got := len(f.ids()); got != 4 {
			t.Errorf("%d messages delivered, want 4", got)
		}
	})
	t.Run("a stranger's account is an error", func(t *testing.T) {
		f := newFakeServer(t)
		e := newSeedEnv(t, f, func(o *SeedOptions) { o.Users = 2; o.MessagesPerUser = 2; o.Concurrency = 1 })
		spec, _ := newPlanSpec("seed.test", e.cfg.Seed)
		f.users[spec.email(0)] = "someone-elses"
		code, _, stderr := e.run(context.Background())
		if code != exitFail || !strings.Contains(stderr, "already exists") {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		if len(f.ids()) != 0 {
			t.Error("messages were sent to an account the seed does not own")
		}
	})
}

func TestSeedNoPasswordInOutput(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, nil)
	code, stdout, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// Not vacuous: the server did get a password for every user, and the
	// state file did record each of them.
	if len(f.passwords) != 6 {
		t.Fatalf("server received %d passwords, want 6", len(f.passwords))
	}
	state, err := os.ReadFile(e.state)
	if err != nil {
		t.Fatal(err)
	}
	for _, pw := range f.passwords {
		if len(pw) < 20 {
			t.Fatalf("password %q is too short to be a real one", pw)
		}
		if !bytes.Contains(state, []byte(pw)) {
			t.Error("a password the server received is missing from the state file")
		}
		for name, out := range map[string]string{"stdout": stdout, "stderr": stderr} {
			if strings.Contains(out, pw) {
				t.Errorf("a password appears in %s", name)
			}
		}
	}
	for name, out := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if strings.Contains(out, fakeSecret) || strings.Contains(out, e.state) {
			t.Errorf("secret or state path in %s", name)
		}
	}
}

func TestSeedFailureOutputHasNoSecrets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*fakeServer, *seedEnv)
		stderr string
	}{
		{"wrong session secret", func(f *fakeServer, e *seedEnv) { e.deps.secret = []byte("a-different-secret-entirely-0000") }, "401"},
		{"server error", func(f *fakeServer, e *seedEnv) { f.fail500 = true }, "500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeServer(t)
			e := newSeedEnv(t, f, nil)
			tc.setup(f, e)
			code, stdout, stderr := e.run(context.Background())
			if code != exitFail || !strings.Contains(stderr, tc.stderr) {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			if tc.stderr == "401" && f.badSig.Load() == 0 {
				t.Fatal("the fake never rejected a signature, so the check is vacuous")
			}
			// Every password was persisted before its create call, and the
			// failed requests carried them.
			state, _ := os.ReadFile(e.state)
			var pws []string
			for _, line := range strings.Split(string(state), "\n") {
				var r record
				if json.Unmarshal([]byte(line), &r) == nil && r.Pw != "" {
					pws = append(pws, r.Pw)
				}
			}
			if len(pws) == 0 {
				t.Fatal("no password was persisted before the create call")
			}
			for _, s := range []string{stdout, stderr} {
				for _, pw := range pws {
					if strings.Contains(s, pw) {
						t.Error("a password leaked into the output of a failed run")
					}
				}
				if strings.Contains(s, "different-secret") || strings.Contains(s, fakeSecret) {
					t.Error("the session secret leaked into the output of a failed run")
				}
			}
			if len(f.ids()) != 0 {
				t.Error("messages were sent after the create failed")
			}
		})
	}
}

func TestSeedRefusedTargetDialsNothing(t *testing.T) {
	f := newFakeServer(t)
	for name, mod := range map[string]func(*seedEnv){
		"smtp not allowed":    func(e *seedEnv) { e.cfg.Allow = e.cfg.Allow[:1] },
		"control not allowed": func(e *seedEnv) { e.cfg.Allow = e.cfg.Allow[1:] },
		"production smtp": func(e *seedEnv) {
			e.cfg.Seed.SMTPAddr = "mail.example.com:25"
			tg, _ := parseTarget("mail.example.com:25")
			e.cfg.Allow = append(e.cfg.Allow, tg)
		},
	} {
		e := newSeedEnv(t, f, nil)
		mod(e)
		var out, errb bytes.Buffer
		code := seedMain(context.Background(), e.cfg, e.deps, &out, &errb)
		if code != exitRefuse {
			t.Errorf("%s: exit %d, want %d", name, code, exitRefuse)
		}
		if n := e.dials.Load(); n != 0 {
			t.Errorf("%s: %d dials", name, n)
		}
		if _, err := os.Stat(e.state); err == nil {
			t.Errorf("%s: state file created before the refusal", name)
		}
	}
	if len(f.users)+len(f.ids()) != 0 {
		t.Error("the fake saw traffic")
	}
}

func TestGuardedDialRefusesBeforeDialling(t *testing.T) {
	var dials int
	base := func(context.Context, string, string) (net.Conn, error) {
		dials++
		return nil, errors.New("should not be reached")
	}
	cfg := Config{Allow: []Target{{Host: "127.0.0.1", Port: 9, IP: true}}, TestDomain: "seed.test"}
	d := guardedDial(cfg, base)
	if _, err := d(context.Background(), "tcp", "127.0.0.1:10"); err == nil || dials != 0 {
		t.Fatalf("other port: err %v, dials %d", err, dials)
	}
	if _, err := d(context.Background(), "tcp", "10.9.9.9:9"); err == nil || dials != 0 {
		t.Fatalf("other host: err %v, dials %d", err, dials)
	}
	if _, err := d(context.Background(), "tcp", "127.0.0.1:9"); err == nil || dials != 1 {
		t.Fatalf("allowed target should reach base once: err %v, dials %d", err, dials)
	}
}

func TestControlClientDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	// The redirect target is allowed too, so only the redirect policy stands
	// between the client and it.
	addr := strings.TrimPrefix(redirector.URL, "http://")
	tg, _ := parseTarget(addr)
	tg2, _ := parseTarget(strings.TrimPrefix(target.URL, "http://"))
	cfg := Config{Allow: []Target{tg, tg2}, TestDomain: "seed.test"}
	c := newControlClient(addr, []byte(fakeSecret), guardedDial(cfg, defaultDial()))
	if _, err := c.create(context.Background(), "u@seed.test", "pw-pw-pw-pw"); err == nil {
		t.Fatal("a redirect was treated as success")
	}
	if hits.Load() != 0 {
		t.Fatal("a redirect was followed")
	}
}

func TestSeedRetriesTransientSMTPRefusal(t *testing.T) {
	f := newFakeServer(t)
	f.flood = 3
	e := newSeedEnv(t, f, func(o *SeedOptions) { o.FolderMix = "INBOX:1"; o.Users = 2; o.MessagesPerUser = 3 })
	code, _, stderr := e.run(context.Background())
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want, _ := planIDs(t, e.cfg.Seed)
	sameSet(t, f.ids(), want)
	if f.flood != 0 {
		t.Fatal("the 421 answers were never exercised")
	}
}

func TestSeedPermanentSMTPRefusalStops(t *testing.T) {
	f := newFakeServer(t)
	e := newSeedEnv(t, f, func(o *SeedOptions) { o.FolderMix = "INBOX:1"; o.Users = 1; o.MessagesPerUser = 2 })
	// RCPT answers 550 for an account the server never kept.
	f.forget = true
	code, _, stderr := e.run(context.Background())
	if code != exitFail || !strings.Contains(stderr, "550") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestSessionSecretLoading(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	loose := filepath.Join(dir, "loose")
	if err := os.WriteFile(good, []byte(" s3cret \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose, []byte("s3cret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == sessionEnvVar {
				return v
			}
			return ""
		}
	}
	if s, err := loadSessionSecret(good, env("")); err != nil || string(s) != "s3cret" {
		t.Errorf("file: %q %v", s, err)
	}
	if s, err := loadSessionSecret("", env("fromenv")); err != nil || string(s) != "fromenv" {
		t.Errorf("env: %q %v", s, err)
	}
	if _, err := loadSessionSecret(loose, env("")); err == nil {
		t.Error("a group/world readable secret file was accepted")
	}
	if _, err := loadSessionSecret("", env("")); err == nil {
		t.Error("no secret was accepted")
	}
	if _, err := loadSessionSecret(filepath.Join(dir, "missing"), env("x")); err == nil {
		t.Error("a missing secret file fell back to the environment")
	}
}

func TestSeedFlagsAndHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"seed", "-h"}, &out, &errb); code != exitOK {
		t.Fatalf("-h exit %d", code)
	}
	help := out.String() + errb.String()
	for _, want := range []string{defaultSizeMix, defaultFolderMix, "POST /internal/user/create", sessionEnvVar, "90 seconds", "-users", "-state-file", "% of all messages", "-imap-addr", "-imap-tls", "-imap-insecure-tls", "IMAP APPEND", "bypasses attachment deduplication"} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	if strings.Contains(help, "%!") || strings.Contains(help, "%[") {
		t.Errorf("help has a format error:\n%s", help)
	}
	// Validate still runs first: no allow list, no seed.
	out.Reset()
	errb.Reset()
	if code := run([]string{"seed", "--users", "1"}, &out, &errb); code != exitRefuse || !strings.Contains(errb.String(), "refused") {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
	if _, ok := subcommandFlags["seed"]; !ok {
		t.Error("seed flags are not registered through subcommandFlags")
	}
}
