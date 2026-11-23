package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/fetch"
	"github.com/bongo-project/bongo/internal/fetch/credcrypt"
	"github.com/bongo-project/bongo/internal/fetch/pop3"
	"github.com/bongo-project/bongo/internal/netx"
	bongosmtp "github.com/bongo-project/bongo/internal/smtp"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	fwUser     = "11111111-1111-1111-1111-111111111111"
	fwEmail    = "alice@example.test"
	fwPassword = "pw-hunter2-secret"
)

// fwApplier applies commands to Pebble the way the FSM does for plain keys. It
// can fail every write to a fetchseen key, or answer NotLeaderError after n
// calls.
type fwApplier struct {
	ps             *store.PebbleStore
	mu             sync.Mutex
	calls          int
	notLeaderAfter int // 0 = never
	failSeen       bool
	keys           []string
}

func (a *fwApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	a.mu.Lock()
	a.calls++
	n := a.calls
	a.mu.Unlock()
	if a.notLeaderAfter > 0 && n > a.notLeaderAfter {
		return &cluster.NotLeaderError{}
	}
	if a.failSeen && strings.Contains(string(cmd.Key), "/fetchseen/") {
		return errors.New("simulated raft failure")
	}
	a.mu.Lock()
	a.keys = append(a.keys, string(cmd.Key))
	a.mu.Unlock()
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

func (a *fwApplier) wrote(sub string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, k := range a.keys {
		if strings.Contains(k, sub) {
			n++
		}
	}
	return n
}

type fwMsg struct {
	id     uint32 // IMAP UID or POP3 number
	uidl   string
	body   string
	size   int64 // listed size; -1 = len(body); -2 = missing from the answer
	lie    bool  // the body overflows whatever max says
	listed bool
}

func (m fwMsg) listedSize() (int64, bool) {
	switch m.size {
	case -2:
		return 0, false
	case 0, -1:
		return int64(len(m.body)), true
	}
	return m.size, true
}

// fwRemote is the fake server behind both protocol interfaces.
type fwRemote struct {
	mu          sync.Mutex
	dials       int
	dialErr     error
	dialErrs    []error // consumed one per dial before dialErr
	msgs        []fwMsg
	uidValidity uint32
	uidlErr     error
	blockDial   bool
	dialGate    chan struct{} // when set, a dial waits for it to close
	// tarpit makes a dial for that remote user name wait for tarpitGate (or
	// the context) and counts how many wait at once.
	tarpit        string
	tarpitGate    chan struct{}
	tarpitNow     atomic.Int32
	tarpitMaxSeen atomic.Int32
	params        []fetch.Params

	selectedRO   []bool
	selectedName []string
	fetched      []uint32
	deleted      []uint32
	popDeleted   []int
	sizeAsked    [][]uint32
	onFetch      func(id uint32)
	onClose      func()
	onDelete     func()
	closeCount   int
	listEmpty    bool
	listCalled   int
	failListUIDs error
}

func (r *fwRemote) DialIMAP(ctx context.Context, p fetch.Params) (fetchIMAP, error) {
	if err := r.dial(ctx, p); err != nil {
		return nil, err
	}
	return &fwIMAP{r: r}, nil
}

func (r *fwRemote) DialPOP3(ctx context.Context, p fetch.Params) (fetchPOP3, error) {
	if err := r.dial(ctx, p); err != nil {
		return nil, err
	}
	return &fwPOP3{r: r}, nil
}

func (r *fwRemote) dial(ctx context.Context, p fetch.Params) error {
	r.mu.Lock()
	r.dials++
	r.params = append(r.params, p)
	block := r.blockDial
	var err error
	if len(r.dialErrs) > 0 {
		err, r.dialErrs = r.dialErrs[0], r.dialErrs[1:]
	} else {
		err = r.dialErr
	}
	r.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	if r.tarpit != "" && p.Username == r.tarpit {
		n := r.tarpitNow.Add(1)
		for {
			m := r.tarpitMaxSeen.Load()
			if n <= m || r.tarpitMaxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		defer r.tarpitNow.Add(-1)
		select {
		case <-r.tarpitGate:
		case <-ctx.Done():
			return ctx.Err()
		}
		return errors.New("tarpit released")
	}
	if r.dialGate != nil {
		<-r.dialGate
	}
	return err
}

func (r *fwRemote) dialCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dials
}

type fwIMAP struct{ r *fwRemote }

func (s *fwIMAP) Select(folder string, ro bool) (uint32, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.r.selectedRO = append(s.r.selectedRO, ro)
	s.r.selectedName = append(s.r.selectedName, folder)
	if s.r.uidValidity == 0 {
		return 7, nil
	}
	return s.r.uidValidity, nil
}

func (s *fwIMAP) UIDs() ([]uint32, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if s.r.failListUIDs != nil {
		return nil, s.r.failListUIDs
	}
	var out []uint32
	if !s.r.listEmpty {
		for _, m := range s.r.msgs {
			out = append(out, m.id)
		}
	}
	return out, nil
}

func (s *fwIMAP) Sizes(uids []uint32) (map[uint32]int64, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.r.sizeAsked = append(s.r.sizeAsked, append([]uint32(nil), uids...))
	out := map[uint32]int64{}
	// An unrequested UID in the answer must be ignored by the worker.
	out[999999] = 1
	for _, id := range uids {
		for _, m := range s.r.msgs {
			if m.id == id {
				if sz, ok := m.listedSize(); ok {
					out[id] = sz
				}
			}
		}
	}
	return out, nil
}

func (s *fwIMAP) FetchBody(uid uint32, max int64, fn func(io.Reader) error) error {
	s.r.mu.Lock()
	var msg *fwMsg
	for i := range s.r.msgs {
		if s.r.msgs[i].id == uid {
			msg = &s.r.msgs[i]
		}
	}
	hook := s.r.onFetch
	s.r.mu.Unlock()
	if msg == nil {
		return fetch.ErrProtocol
	}
	if msg.lie || int64(len(msg.body)) > max {
		return fetch.ErrTooLarge
	}
	err := fn(strings.NewReader(msg.body))
	if err != nil {
		return err
	}
	s.r.mu.Lock()
	s.r.fetched = append(s.r.fetched, uid)
	s.r.mu.Unlock()
	if hook != nil {
		hook(uid)
	}
	return nil
}

func (s *fwIMAP) DeleteUIDs(uids []uint32) (bool, error) {
	s.r.mu.Lock()
	hook := s.r.onDelete
	s.r.deleted = append(s.r.deleted, uids...)
	s.r.mu.Unlock()
	if hook != nil {
		hook()
	}
	return true, nil
}

func (s *fwIMAP) Close() {
	s.r.mu.Lock()
	s.r.closeCount++
	hook := s.r.onClose
	s.r.mu.Unlock()
	if hook != nil {
		hook()
	}
}

type fwPOP3 struct{ r *fwRemote }

func (s *fwPOP3) UIDL() ([]pop3.Entry, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if s.r.uidlErr != nil {
		return nil, s.r.uidlErr
	}
	var out []pop3.Entry
	if !s.r.listEmpty {
		for _, m := range s.r.msgs {
			out = append(out, pop3.Entry{Num: int(m.id), UIDL: m.uidl})
		}
	}
	return out, nil
}

func (s *fwPOP3) Sizes() (map[int]int64, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	out := map[int]int64{}
	for _, m := range s.r.msgs {
		if sz, ok := m.listedSize(); ok {
			out[int(m.id)] = sz
		}
	}
	return out, nil
}

func (s *fwPOP3) Retr(num int, listed, max int64, fn func(io.Reader) error) error {
	s.r.mu.Lock()
	var msg *fwMsg
	for i := range s.r.msgs {
		if int(s.r.msgs[i].id) == num {
			msg = &s.r.msgs[i]
		}
	}
	s.r.mu.Unlock()
	if msg == nil {
		return fetch.ErrProtocol
	}
	if msg.lie || int64(len(msg.body)) > max {
		return fetch.ErrTooLarge
	}
	if err := fn(strings.NewReader(msg.body)); err != nil {
		return err
	}
	s.r.mu.Lock()
	s.r.fetched = append(s.r.fetched, uint32(num)) //nolint:gosec // test ids are small
	s.r.mu.Unlock()
	return nil
}

func (s *fwPOP3) Dele(num int) error {
	s.r.mu.Lock()
	s.r.popDeleted = append(s.r.popDeleted, num)
	s.r.mu.Unlock()
	return nil
}

func (s *fwPOP3) Quit() error { return nil }

func (s *fwPOP3) Close() {
	s.r.mu.Lock()
	s.r.closeCount++
	hook := s.r.onClose
	s.r.mu.Unlock()
	if hook != nil {
		hook()
	}
}

// fwDeliver is a fake pipeline.
type fwDeliver struct {
	mu       sync.Mutex
	blobs    map[string]string // hash -> content
	envs     []fwEnv
	storeErr error
	enqErr   error
	order    []string
}

type fwEnv struct {
	from       string
	recipients []string
	hash       string
	meta       bongosmtp.InboundMeta
}

func (d *fwDeliver) StoreBlob(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("storing: %w", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.storeErr != nil {
		return "", d.storeErr
	}
	if d.blobs == nil {
		d.blobs = map[string]string{}
	}
	h := fmt.Sprintf("blob%04d", len(d.blobs))
	d.blobs[h] = string(b)
	d.order = append(d.order, "blob")
	return h, nil
}

func (d *fwDeliver) EnqueueLocal(from string, rcpts []string, hash string, meta *bongosmtp.InboundMeta) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.enqErr != nil {
		return d.enqErr
	}
	d.envs = append(d.envs, fwEnv{from: from, recipients: rcpts, hash: hash, meta: *meta})
	d.order = append(d.order, "env")
	return nil
}

func (d *fwDeliver) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.envs)
}

type fwQuota struct {
	mu    sync.Mutex
	over  func(add int64) bool
	err   error
	asked []int64
}

func (q *fwQuota) RecipientWouldExceedQuota(_ string, add int64) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.asked = append(q.asked, add)
	if q.err != nil {
		return false, q.err
	}
	return q.over != nil && q.over(add), nil
}

type fwEnvT struct {
	t       *testing.T
	ps      *store.PebbleStore
	app     *fwApplier
	fs      *store.FetchStore
	kr      *credcrypt.Keyring
	oldKey  []byte
	w       *fetchWorker
	remote  *fwRemote
	deliver *fwDeliver
	quota   *fwQuota
	log     *lockedBuffer
	now     time.Time
	leader  atomic.Bool
	low     atomic.Bool
	profile func(string) (string, bool)
	seq     int
}

func newFWEnv(t *testing.T) *fwEnvT {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ps.Close() })
	e := &fwEnvT{t: t, ps: ps, app: &fwApplier{ps: ps}, remote: &fwRemote{}, deliver: &fwDeliver{}, quota: &fwQuota{}, log: &lockedBuffer{}}
	e.fs = store.NewFetchStore(ps, e.app)
	cur := make([]byte, 32)
	_, _ = rand.Read(cur)
	e.oldKey = make([]byte, 32)
	_, _ = rand.Read(e.oldKey)
	e.kr, err = credcrypt.NewKeyring(cur, [][]byte{e.oldKey})
	if err != nil {
		t.Fatal(err)
	}
	e.now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e.leader.Store(true)
	e.profile = func(id string) (string, bool) {
		if id == fwUser {
			return fwEmail, true
		}
		return "", false
	}
	e.w = &fetchWorker{
		users:      func() ([]string, error) { return []string{fwUser}, nil },
		profile:    func(id string) (string, bool) { return e.profile(id) },
		store:      e.fs,
		keyring:    e.kr,
		dial:       e.remote,
		deliver:    e.deliver,
		quota:      e.quota,
		storageLow: func() bool { return e.low.Load() },
		isLeader:   func() bool { return e.leader.Load() },
		maxMessage: 1 << 20,
		hostname:   "mx.bongo.test",
		now:        func() time.Time { return e.now },
		jitter:     func() time.Duration { return 0 },
		logger:     slog.New(slog.NewTextHandler(e.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		runTimeout: 30 * time.Second,
		pruneMax:   fetchPruneMax,
		sweepMax:   fetchSweepMax,
		sightings:  map[string]int{},
	}
	return e
}

func (e *fwEnvT) newGUID() string {
	e.seq++
	return fmt.Sprintf("%032x", e.seq)
}

// addAcct stores an IMAP account sealed under the current key.
func (e *fwEnvT) addAcct(mut ...func(*bongov1.FetchAccount)) *bongov1.FetchAccount {
	e.t.Helper()
	a := &bongov1.FetchAccount{
		Guid: e.newGUID(), Protocol: fetch.ProtocolIMAP, Security: fetch.SecurityTLS,
		Host: "imap.example", Port: 993, Username: "remote-user-xyz", Folder: "",
		IntervalMinutes: 15, Generation: 1, CreatedAt: 1, UpdatedAt: 1,
	}
	for _, m := range mut {
		m(a)
	}
	if a.Credential == nil {
		cred, err := e.kr.Seal(fwUser, a, fwPassword)
		if err != nil {
			e.t.Fatal(err)
		}
		a.Credential = cred
	}
	if err := e.fs.Put(fwUser, a); err != nil {
		e.t.Fatal(err)
	}
	return a
}

func (e *fwEnvT) pass() {
	e.t.Helper()
	e.w.pass(context.Background())
}

func (e *fwEnvT) state(a *bongov1.FetchAccount) *bongov1.FetchState {
	e.t.Helper()
	st, ok, err := e.fs.GetState(fwUser, a.Guid)
	if err != nil {
		e.t.Fatal(err)
	}
	if !ok {
		return nil
	}
	return st
}

func (e *fwEnvT) mustState(a *bongov1.FetchAccount) *bongov1.FetchState {
	e.t.Helper()
	st := e.state(a)
	if st == nil {
		e.t.Fatal("no state written")
	}
	return st
}

func (e *fwEnvT) seen(a *bongov1.FetchAccount) map[string]*bongov1.FetchSeen {
	e.t.Helper()
	m, err := e.fs.SeenSet(fwUser, a.Guid)
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func (e *fwEnvT) addMsgs(n int, bodyPrefix string) {
	for i := 1; i <= n; i++ {
		e.remote.msgs = append(e.remote.msgs, fwMsg{
			id: uint32(i), uidl: fmt.Sprintf("uidl-%d", i), //nolint:gosec // test ids are small
			body: fmt.Sprintf("From: bob@remote.test\r\nSubject: s%d\r\n\r\n%s body %d\r\n", i, bodyPrefix, i),
		})
	}
}

func (e *fwEnvT) advance(d time.Duration) { e.now = e.now.Add(d) }

func (e *fwEnvT) updateAcct(a *bongov1.FetchAccount, f func(*bongov1.FetchAccount)) {
	e.t.Helper()
	cur, ok, err := e.fs.Get(fwUser, a.Guid)
	if err != nil || !ok {
		e.t.Fatalf("get: %v %v", ok, err)
	}
	f(cur)
	if err := e.fs.Put(fwUser, cur); err != nil {
		e.t.Fatal(err)
	}
}

func TestFetchOnlyOnLeader(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(1, "x")
	e.leader.Store(false)
	before := e.app.calls
	e.pass()
	if e.remote.dialCount() != 0 {
		t.Fatal("a follower dialled")
	}
	if e.app.calls != before || e.state(a) != nil {
		t.Fatal("a follower wrote")
	}
	e.leader.Store(true)
	e.pass()
	if e.remote.dialCount() != 1 {
		t.Fatalf("leader dials = %d, want 1", e.remote.dialCount())
	}
}

func TestFetchSkipsDisabledAndNonPersonUsers(t *testing.T) {
	for _, name := range []string{"disabled", "resource or shared", "missing profile"} {
		t.Run(name, func(t *testing.T) {
			e := newFWEnv(t)
			a := e.addAcct()
			e.addMsgs(1, "x")
			e.profile = func(string) (string, bool) { return "", false } // the adapter folds all three into ok=false
			e.pass()
			if e.remote.dialCount() != 0 {
				t.Fatal("dialled for a user that must be skipped")
			}
			if e.state(a) != nil {
				t.Fatal("wrote state for a skipped user")
			}
		})
	}
}

func TestFetchProfileAdapter(t *testing.T) {
	tests := []struct {
		name   string
		prof   *bongov1.UserProfile
		wantOK bool
	}{
		{"person", &bongov1.UserProfile{Email: fwEmail}, true},
		{"disabled", &bongov1.UserProfile{Email: fwEmail, Disabled: true}, false},
		{"resource", &bongov1.UserProfile{Email: fwEmail, Kind: "resource"}, false},
		{"shared", &bongov1.UserProfile{Email: fwEmail, Kind: "shared"}, false},
		{"no email", &bongov1.UserProfile{}, false},
		{"missing", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := store.NewPebbleStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer ps.Close()
			if tt.prof != nil {
				b, _ := proto.Marshal(tt.prof)
				if err := ps.Set([]byte("/user/"+fwUser+"/profile"), b); err != nil {
					t.Fatal(err)
				}
			}
			email, ok := fetchProfileLookup(ps)(fwUser)
			if ok != tt.wantOK || (ok && email != fwEmail) {
				t.Fatalf("lookup = %q, %v", email, ok)
			}
		})
	}
}

func TestFetchSkipsOnLowStorage(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(1, "x")
	e.low.Store(true)
	e.pass()
	if e.remote.dialCount() != 0 || e.state(a) != nil {
		t.Fatal("ran with low storage")
	}
	e.low.Store(false)
	e.pass()
	if e.remote.dialCount() != 1 {
		t.Fatal("did not run once storage recovered")
	}
}

func TestFetchDueAndInterval(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct(func(a *bongov1.FetchAccount) { a.IntervalMinutes = 1; a.Port = 25 })
	e.addMsgs(1, "x")
	e.pass()
	if e.remote.dialCount() != 1 {
		t.Fatal("new account was not due at once")
	}
	if got := e.remote.params[0].Port; got != 993 {
		t.Fatalf("dialled port %d, want 993 derived from protocol and security", got)
	}
	st := e.mustState(a)
	if want := e.now.Add(15 * time.Minute).Unix(); st.NextRunAt != want {
		t.Fatalf("next_run_at = %d, want %d (interval 1 clamped to 15 min)", st.NextRunAt, want)
	}
	e.advance(14 * time.Minute)
	e.pass()
	if e.remote.dialCount() != 1 {
		t.Fatal("ran before next_run_at")
	}
	e.advance(time.Minute)
	e.pass()
	if e.remote.dialCount() != 2 {
		t.Fatal("did not run at next_run_at")
	}
}

func TestFetchPortZeroIsProtocolError(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct(func(a *bongov1.FetchAccount) { a.Security = "plain" })
	e.pass()
	if e.remote.dialCount() != 0 {
		t.Fatal("dialled with no derivable port")
	}
	if st := e.mustState(a); st.LastError != fetch.CodeProtocolError {
		t.Fatalf("last_error = %q", st.LastError)
	}
}

func TestFetchSeenPreventsReimport(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
	e.addMsgs(3, "x")
	e.pass()
	if got := e.deliver.count(); got != 3 {
		t.Fatalf("imported %d, want 3", got)
	}
	if got := e.mustState(a).LastRunImported; got != 3 {
		t.Fatalf("last_run_imported = %d", got)
	}
	e.advance(time.Hour)
	e.pass()
	if got := e.deliver.count(); got != 3 {
		t.Fatalf("second run imported more: %d", got)
	}
	if got := e.mustState(a); got.LastRunImported != 0 || got.ImportedTotal != 3 {
		t.Fatalf("state after second run = %+v", got)
	}
	if len(e.seen(a)) != 3 {
		t.Fatalf("seen = %d", len(e.seen(a)))
	}
}

func TestFetchEnvelopeIsUntrustedNullSender(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(1, "x")
	e.pass()
	if len(e.deliver.envs) != 1 {
		t.Fatal("no envelope")
	}
	env := e.deliver.envs[0]
	if env.from != "" || len(env.recipients) != 1 || env.recipients[0] != fwEmail {
		t.Fatalf("envelope = %+v", env)
	}
	m := env.meta
	if m.Trusted || m.AuthUserID != "" || m.SourceIP != "" || m.SPFResult != "" || m.FetchAccount != a.Guid {
		t.Fatalf("meta = %+v", m)
	}
}

func TestFetchBlobPrefix(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(1, "x")
	e.pass()
	blob := e.deliver.blobs[e.deliver.envs[0].hash]
	tok := e.kr.LoopTokens(a.Guid)[0]
	if !strings.HasPrefix(blob, "Received: from imap.example by mx.bongo.test with IMAPS (Bongo fetch); ") {
		t.Fatalf("blob starts %q", blob[:min(120, len(blob))])
	}
	if !strings.Contains(blob, "\r\nX-Bongo-Fetched: "+tok+"\r\n") {
		t.Fatalf("blob lacks the loop token:\n%s", blob)
	}
	if !strings.Contains(blob, "Subject: s1") {
		t.Fatal("original message missing")
	}
}

func TestFetchHeaderInjectionInHostIsNeutralised(t *testing.T) {
	e := newFWEnv(t)
	e.addAcct(func(a *bongov1.FetchAccount) { a.Host = "evil.example\r\nBcc: x@y.z" })
	e.addMsgs(1, "x")
	e.pass()
	blob := e.deliver.blobs[e.deliver.envs[0].hash]
	if strings.Contains(strings.SplitN(blob, "\r\n\r\n", 2)[0], "\r\nBcc:") {
		t.Fatal("host injected a header")
	}
}

func TestFetchDeleteOnlyAfterSeen(t *testing.T) {
	for _, proto := range []string{fetch.ProtocolIMAP, fetch.ProtocolPOP3} {
		t.Run(proto+" mark seen fails", func(t *testing.T) {
			e := newFWEnv(t)
			a := e.addAcct(func(a *bongov1.FetchAccount) {
				a.Protocol = proto
				if proto == fetch.ProtocolPOP3 {
					a.Host = "pop.example"
					a.Port = 995
				}
			})
			e.addMsgs(2, "x")
			e.app.failSeen = true
			e.pass()
			if e.deliver.count() != 1 {
				t.Fatalf("spooled %d, want 1 (the run stops at the failed MarkSeen)", e.deliver.count())
			}
			if len(e.remote.deleted) != 0 || len(e.remote.popDeleted) != 0 {
				t.Fatalf("deleted remotely without a seen record: %v %v", e.remote.deleted, e.remote.popDeleted)
			}
			if st := e.mustState(a); st.LastError == "" {
				t.Fatal("the failure was not recorded")
			}
		})
		t.Run(proto+" success deletes", func(t *testing.T) {
			e := newFWEnv(t)
			e.addAcct(func(a *bongov1.FetchAccount) {
				a.Protocol = proto
				if proto == fetch.ProtocolPOP3 {
					a.Host = "pop.example"
					a.Port = 995
				}
			})
			e.addMsgs(2, "x")
			e.pass()
			if proto == fetch.ProtocolIMAP && len(e.remote.deleted) != 2 {
				t.Fatalf("deleted = %v", e.remote.deleted)
			}
			if proto == fetch.ProtocolPOP3 && len(e.remote.popDeleted) != 2 {
				t.Fatalf("popDeleted = %v", e.remote.popDeleted)
			}
		})
	}
}

// The remote delete must come after the seen record, not merely after the
// spool: the order of writes is seen first.
func TestFetchSeenWrittenBeforeRemoteDelete(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(1, "x")
	var seenAtDelete int
	e.remote.onDelete = func() { seenAtDelete = len(e.seen(a)) }
	e.pass()
	if seenAtDelete != 1 {
		t.Fatalf("seen records at delete time = %d, want 1", seenAtDelete)
	}
}

func TestFetchLeaveOnServerNeverDeletes(t *testing.T) {
	for _, proto := range []string{fetch.ProtocolIMAP, fetch.ProtocolPOP3} {
		t.Run(proto, func(t *testing.T) {
			e := newFWEnv(t)
			e.addAcct(func(a *bongov1.FetchAccount) {
				a.Protocol = proto
				a.LeaveOnServer = true
				if proto == fetch.ProtocolPOP3 {
					a.Host = "pop.example"
					a.Port = 995
				}
			})
			e.addMsgs(2, "x")
			e.pass()
			if e.deliver.count() != 2 {
				t.Fatalf("imported %d", e.deliver.count())
			}
			if len(e.remote.deleted) != 0 || len(e.remote.popDeleted) != 0 {
				t.Fatal("deleted in leave-on-server mode")
			}
			if proto == fetch.ProtocolIMAP && (len(e.remote.selectedRO) != 1 || !e.remote.selectedRO[0]) {
				t.Fatalf("select read-only = %v, want [true]", e.remote.selectedRO)
			}
		})
	}
}

func TestFetchDeleteModeSelectsReadWriteAndDefaultFolder(t *testing.T) {
	e := newFWEnv(t)
	e.addAcct()
	e.addMsgs(1, "x")
	e.pass()
	if len(e.remote.selectedRO) != 1 || e.remote.selectedRO[0] {
		t.Fatalf("select read-only = %v, want [false]", e.remote.selectedRO)
	}
	if e.remote.selectedName[0] != "INBOX" {
		t.Fatalf("folder = %q, want INBOX", e.remote.selectedName[0])
	}
}

func TestFetchTooLargeSkippedKeptRemote(t *testing.T) {
	e := newFWEnv(t)
	e.w.maxMessage = 1000
	a := e.addAcct()
	e.remote.msgs = []fwMsg{
		{id: 1, body: "Subject: small\r\n\r\nx\r\n"},
		{id: 2, body: "Subject: big\r\n\r\nx\r\n", size: 5000},
	}
	e.pass()
	if e.deliver.count() != 1 {
		t.Fatalf("imported %d, want 1", e.deliver.count())
	}
	st := e.mustState(a)
	if st.LastRunTooLarge != 1 || st.LastRunImported != 1 {
		t.Fatalf("state = %+v", st)
	}
	var tooLarge int
	for _, s := range e.seen(a) {
		if s.Status == store.FetchSeenTooLarge {
			tooLarge++
		}
	}
	if tooLarge != 1 {
		t.Fatalf("too_large seen records = %d", tooLarge)
	}
	for _, id := range e.remote.deleted {
		if id == 2 {
			t.Fatal("the too-large message was deleted remotely")
		}
	}
	if len(e.remote.deleted) != 1 {
		t.Fatalf("deleted = %v, want just the small one", e.remote.deleted)
	}
}

// The server said the message was small and it was not: the body overflows
// and the session is dead. Without a seen record the next run would hit the
// same message forever.
func TestFetchLyingSizeMarkedTooLarge(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.remote.msgs = []fwMsg{
		{id: 1, body: "Subject: ok\r\n\r\nx\r\n"},
		{id: 2, body: "Subject: liar\r\n\r\nx\r\n", size: 10, lie: true},
		{id: 3, body: "Subject: after\r\n\r\nx\r\n"},
	}
	e.pass()
	if e.deliver.count() != 1 {
		t.Fatalf("imported %d, want 1 (the run ends at the liar)", e.deliver.count())
	}
	var status uint32
	for h, s := range e.seen(a) {
		if h == store.IMAPSeenHash("INBOX", 7, 2) {
			status = s.Status
		}
	}
	if status != store.FetchSeenTooLarge {
		t.Fatalf("liar seen status = %d, want too_large", status)
	}
	st := e.mustState(a)
	if st.LastError != "" || st.LastRunTooLarge != 1 {
		t.Fatalf("state = %+v: the run is a success with a backlog", st)
	}
	if want := e.now.Add(5 * time.Minute).Unix(); st.NextRunAt != want {
		t.Fatalf("next_run_at = %d, want backlog %d", st.NextRunAt, want)
	}
	e.advance(5 * time.Minute)
	e.pass()
	if e.deliver.count() != 2 {
		t.Fatalf("second run imported %d total, want 2: it must move on", e.deliver.count())
	}
	for _, id := range e.remote.deleted {
		if id == 2 {
			t.Fatal("the liar was deleted")
		}
	}
}

func TestFetchPerRunCaps(t *testing.T) {
	t.Run("message cap", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
		e.addMsgs(600, "x")
		e.pass()
		if got := e.deliver.count(); got != 500 {
			t.Fatalf("imported %d, want 500", got)
		}
		st := e.mustState(a)
		if want := e.now.Add(5 * time.Minute).Unix(); st.NextRunAt != want {
			t.Fatalf("next_run_at = %d, want backlog %d", st.NextRunAt, want)
		}
		e.advance(5 * time.Minute)
		e.pass()
		if got := e.deliver.count(); got != 600 {
			t.Fatalf("after second run: %d, want 600", got)
		}
		if want := e.now.Add(15 * time.Minute).Unix(); e.mustState(a).NextRunAt != want {
			t.Fatal("a finished backlog must return to the interval")
		}
	})
	t.Run("sizes are asked only for the capped candidates", func(t *testing.T) {
		e := newFWEnv(t)
		e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
		e.addMsgs(600, "x")
		e.pass()
		total := 0
		for _, ask := range e.remote.sizeAsked {
			total += len(ask)
		}
		if total != 500 {
			t.Fatalf("Sizes asked for %d UIDs, want 500", total)
		}
	})
	t.Run("byte cap", func(t *testing.T) {
		e := newFWEnv(t)
		e.w.maxMessage = 150 << 20
		a := e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
		for i := 1; i <= 3; i++ {
			e.remote.msgs = append(e.remote.msgs, fwMsg{id: uint32(i), body: "Subject: x\r\n\r\ny\r\n", size: 100 << 20}) //nolint:gosec // small
		}
		e.pass()
		if got := e.deliver.count(); got != 2 {
			t.Fatalf("imported %d, want 2 under the 256 MiB cap", got)
		}
		if want := e.now.Add(5 * time.Minute).Unix(); e.mustState(a).NextRunAt != want {
			t.Fatal("no backlog delay after the byte cap")
		}
	})
	t.Run("missing size counts as the maximum", func(t *testing.T) {
		e := newFWEnv(t)
		e.w.maxMessage = 150 << 20
		e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
		for i := 1; i <= 3; i++ {
			e.remote.msgs = append(e.remote.msgs, fwMsg{id: uint32(i), body: "Subject: x\r\n\r\ny\r\n", size: -2}) //nolint:gosec // small
		}
		e.pass()
		if got := e.deliver.count(); got != 1 {
			t.Fatalf("imported %d, want 1 (each missing size is assumed 150 MiB)", got)
		}
	})
	t.Run("the first message is always allowed", func(t *testing.T) {
		e := newFWEnv(t)
		e.w.maxMessage = 400 << 20
		e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
		e.remote.msgs = []fwMsg{{id: 1, body: "Subject: x\r\n\r\ny\r\n", size: 300 << 20}}
		e.pass()
		if e.deliver.count() != 1 {
			t.Fatal("a single message over the byte cap never gets through")
		}
	})
}

func TestFetchQuotaStopsWithoutSeen(t *testing.T) {
	t.Run("up front", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addAcct()
		e.addMsgs(2, "x")
		e.quota.over = func(add int64) bool { return add == 0 }
		e.pass()
		if e.remote.dialCount() != 0 {
			t.Fatal("dialled for an over-quota user")
		}
		st := e.mustState(a)
		if st.LastError != fetch.CodeQuotaExceeded {
			t.Fatalf("last_error = %q", st.LastError)
		}
		if want := e.now.Add(15 * time.Minute).Unix(); st.NextRunAt != want {
			t.Fatalf("next_run_at = %d, want next interval %d", st.NextRunAt, want)
		}
	})
	t.Run("per message", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addAcct()
		if err := e.fs.PutState(fwUser, a.Guid, &bongov1.FetchState{Generation: 1, AuthFailures: 2, Failures: 3}); err != nil {
			t.Fatal(err)
		}
		e.addMsgs(2, "x")
		e.quota.over = func(add int64) bool { return add > 0 }
		e.pass()
		if e.deliver.count() != 0 || len(e.seen(a)) != 0 || len(e.remote.deleted) != 0 {
			t.Fatal("over-quota message was imported, marked seen or deleted")
		}
		st := e.mustState(a)
		if st.LastError != fetch.CodeQuotaExceeded || st.AuthFailures != 2 {
			t.Fatalf("state = %+v", st)
		}
		if st.Failures != 3 {
			t.Fatalf("failures = %d, want 3 unchanged", st.Failures)
		}
	})
	t.Run("a store error fails open", func(t *testing.T) {
		e := newFWEnv(t)
		e.addAcct()
		e.addMsgs(1, "x")
		e.quota.err = errors.New("pebble hiccup")
		e.pass()
		if e.deliver.count() != 1 {
			t.Fatal("a quota lookup error blocked delivery")
		}
	})
}

func TestFetchPausesAfterFiveAuthFailures(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(1, "x")
	e.remote.dialErr = fetch.ErrAuth
	for i := 1; i <= 4; i++ {
		e.pass()
		st := e.mustState(a)
		if st.Paused || st.AuthFailures != uint32(i) || st.LastError != fetch.CodeAuthFailed { //nolint:gosec // small
			t.Fatalf("after %d failures: %+v", i, st)
		}
		e.advance(15 * time.Minute)
	}
	e.pass()
	st := e.mustState(a)
	if !st.Paused || st.PausedReason != "auth_failed" {
		t.Fatalf("after 5 failures: %+v", st)
	}
	dials := e.remote.dialCount()
	for range 3 {
		e.advance(24 * time.Hour)
		e.pass()
	}
	if e.remote.dialCount() != dials {
		t.Fatal("a paused account was dialled")
	}
	// Resume: the user's save bumps the generation, the old state is ignored.
	e.remote.dialErr = nil
	e.updateAcct(a, func(a *bongov1.FetchAccount) { a.Generation++ })
	e.pass()
	if e.remote.dialCount() != dials+1 {
		t.Fatal("resume did not make the account due")
	}
	if st := e.mustState(a); st.Paused || st.AuthFailures != 0 {
		t.Fatalf("after resume: %+v", st)
	}
}

func TestFetchAuthCounterResetsOnSuccess(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(1, "x")
	fail := func(n int) {
		e.remote.dialErr = fetch.ErrAuth
		for range n {
			e.pass()
			e.advance(15 * time.Minute)
		}
	}
	fail(4)
	e.remote.dialErr = nil
	e.pass()
	e.advance(15 * time.Minute)
	if st := e.mustState(a); st.AuthFailures != 0 {
		t.Fatalf("auth_failures = %d after a success", st.AuthFailures)
	}
	fail(4)
	if st := e.mustState(a); st.Paused {
		t.Fatalf("paused after 4+1+4: %+v", st)
	}
}

func TestFetchWorkerPauseCannotOverwriteResume(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct(func(a *bongov1.FetchAccount) { a.Generation = 3 })
	e.addMsgs(1, "x")
	old := &bongov1.FetchState{Generation: 2, Paused: true, PausedReason: "auth_failed", AuthFailures: 5, NextRunAt: e.now.Add(48 * time.Hour).Unix()}
	if err := e.fs.PutState(fwUser, a.Guid, old); err != nil {
		t.Fatal(err)
	}
	e.pass()
	if e.remote.dialCount() != 1 {
		t.Fatal("a stale paused state blocked the account")
	}
	st := e.mustState(a)
	if st.Generation != 3 || st.Paused || st.AuthFailures != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestFetchNeedsPasswordOnUnknownKey(t *testing.T) {
	e := newFWEnv(t)
	other := make([]byte, 32)
	_, _ = rand.Read(other)
	foreign, _ := credcrypt.NewKeyring(other, nil)
	a := e.addAcct(func(a *bongov1.FetchAccount) {
		cred, err := foreign.Seal(fwUser, a, fwPassword)
		if err != nil {
			t.Fatal(err)
		}
		a.Credential = cred
	})
	e.pass()
	if e.remote.dialCount() != 0 {
		t.Fatal("dialled with an unopenable credential")
	}
	st := e.mustState(a)
	if !st.Paused || st.PausedReason != fetch.CodeNeedsPassword || st.LastError != fetch.CodeNeedsPassword {
		t.Fatalf("state = %+v", st)
	}
	e.advance(24 * time.Hour)
	e.pass()
	if e.remote.dialCount() != 0 {
		t.Fatal("dialled on a later pass")
	}
}

func (e *fwEnvT) addOldKeyAcct(mut ...func(*bongov1.FetchAccount)) *bongov1.FetchAccount {
	e.t.Helper()
	oldKr, err := credcrypt.NewKeyring(e.oldKey, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.addAcct(append([]func(*bongov1.FetchAccount){func(a *bongov1.FetchAccount) {
		cred, err := oldKr.Seal(fwUser, a, fwPassword)
		if err != nil {
			e.t.Fatal(err)
		}
		a.Credential = cred
	}}, mut...)...)
}

func TestFetchRotationReseals(t *testing.T) {
	t.Run("reseals under the current key without a new generation", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addOldKeyAcct()
		e.addMsgs(1, "x")
		e.pass()
		got, ok, err := e.fs.Get(fwUser, a.Guid)
		if err != nil || !ok {
			t.Fatal(err)
		}
		if got.Credential.KeyId != e.kr.CurrentID() {
			t.Fatalf("credential key = %q, want the current %q", got.Credential.KeyId, e.kr.CurrentID())
		}
		if got.Generation != a.Generation || got.UpdatedAt != a.UpdatedAt {
			t.Fatalf("rotation changed generation or updated_at: %d %d", got.Generation, got.UpdatedAt)
		}
		if pw, err := e.kr.Open(fwUser, got); err != nil || pw != fwPassword {
			t.Fatalf("resealed credential does not open: %v", err)
		}
		if e.dials() != 1 {
			t.Fatal("did not run")
		}
	})
	t.Run("a generation change before the Put means no Put", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addOldKeyAcct()
		e.addMsgs(1, "x")
		bumped := false
		e.remote.onClose = func() {
			if bumped {
				return
			}
			bumped = true
			e.updateAcct(a, func(a *bongov1.FetchAccount) { a.Generation++ })
		}
		e.pass()
		got, _, _ := e.fs.Get(fwUser, a.Guid)
		if got.Generation != 2 {
			t.Fatalf("generation = %d", got.Generation)
		}
		if got.Credential.KeyId == e.kr.CurrentID() {
			t.Fatal("re-sealed over an account that changed during the run")
		}
	})
	t.Run("no rotation on a failed run", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addOldKeyAcct()
		e.remote.dialErr = fetch.ErrConnect
		e.pass()
		got, _, _ := e.fs.Get(fwUser, a.Guid)
		if got.Credential.KeyId == e.kr.CurrentID() {
			t.Fatal("rotated without a successful sign-in")
		}
	})
}

func (e *fwEnvT) dials() int { return e.remote.dialCount() }

func TestFetchDeleteRechecksAccount(t *testing.T) {
	t.Run("switched to leave-on-server mid-run", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addAcct()
		e.addMsgs(2, "x")
		e.remote.onFetch = func(uint32) {
			e.updateAcct(a, func(a *bongov1.FetchAccount) { a.LeaveOnServer = true; a.Generation++ })
		}
		e.pass()
		if e.deliver.count() != 2 {
			t.Fatalf("imported %d", e.deliver.count())
		}
		if len(e.remote.deleted) != 0 {
			t.Fatalf("deleted %v after the user chose leave-on-server", e.remote.deleted)
		}
	})
	t.Run("account deleted mid-run", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addAcct()
		e.addMsgs(1, "x")
		e.remote.onFetch = func(uint32) { _ = e.fs.Delete(fwUser, a.Guid) }
		e.pass()
		if len(e.remote.deleted) != 0 {
			t.Fatal("deleted for a deleted account")
		}
	})
}

func TestFetchUIDValidityChangePrunes(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
	e.addMsgs(3, "x")
	e.remote.uidValidity = 7
	e.pass()
	if e.deliver.count() != 3 || len(e.seen(a)) != 3 {
		t.Fatal("setup run failed")
	}
	e.advance(time.Hour)
	e.remote.uidValidity = 8
	e.pass()
	if e.deliver.count() != 6 {
		t.Fatalf("imported %d, want all 3 again under the new UIDVALIDITY", e.deliver.count())
	}
	seen := e.seen(a)
	if len(seen) != 3 {
		t.Fatalf("seen = %d, want only the 3 new-validity records", len(seen))
	}
	for _, id := range []uint32{1, 2, 3} {
		if _, ok := seen[store.IMAPSeenHash("INBOX", 7, id)]; ok {
			t.Fatal("an old-validity record survived")
		}
	}
	if got := e.mustState(a).UidValidity; got != 8 {
		t.Fatalf("uid_validity = %d", got)
	}
}

func TestFetchPruneIsCapped(t *testing.T) {
	e := newFWEnv(t)
	e.w.pruneMax = 20
	a := e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
	for i := 0; i < 20+50; i++ {
		h := store.POP3SeenHash(fmt.Sprintf("gone-%d", i))
		if err := e.fs.MarkSeen(fwUser, a.Guid, h, &bongov1.FetchSeen{Status: store.FetchSeenImported}); err != nil {
			t.Fatal(err)
		}
	}
	e.addMsgs(1, "x")
	e.pass()
	// 1 new record, and at most 20 of the 70 stale ones removed.
	if got := len(e.seen(a)); got != 1+50 {
		t.Fatalf("seen = %d, want 51", got)
	}
}

func TestFetchPOP3PrunesVanishedUIDL(t *testing.T) {
	pop := func(a *bongov1.FetchAccount) {
		a.Protocol = fetch.ProtocolPOP3
		a.Host = "pop.example"
		a.Port = 995
		a.LeaveOnServer = true
	}
	t.Run("vanished uidl is pruned", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addAcct(pop)
		e.addMsgs(3, "x")
		e.pass()
		e.advance(time.Hour)
		e.remote.msgs = e.remote.msgs[:2]
		e.pass()
		seen := e.seen(a)
		if len(seen) != 2 {
			t.Fatalf("seen = %d, want 2", len(seen))
		}
		if _, ok := seen[store.POP3SeenHash("uidl-3")]; ok {
			t.Fatal("vanished uidl kept")
		}
	})
	t.Run("no prune when UIDL failed", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addAcct(pop)
		e.addMsgs(3, "x")
		e.pass()
		e.advance(time.Hour)
		e.remote.uidlErr = fetch.ErrNoUIDL
		e.pass()
		if len(e.seen(a)) != 3 {
			t.Fatal("pruned after a failed listing")
		}
		if st := e.mustState(a); st.LastError != fetch.CodeNoUIDL {
			t.Fatalf("last_error = %q", st.LastError)
		}
	})
	t.Run("no prune on an empty listing", func(t *testing.T) {
		e := newFWEnv(t)
		a := e.addAcct(pop)
		e.addMsgs(3, "x")
		e.pass()
		e.advance(time.Hour)
		e.remote.listEmpty = true
		e.pass()
		if len(e.seen(a)) != 3 {
			t.Fatal("an empty listing pruned every seen record")
		}
		if e.deliver.count() != 3 {
			t.Fatal("re-imported")
		}
	})
}

func TestFetchLoopTokenSkips(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct(func(a *bongov1.FetchAccount) { a.LeaveOnServer = true })
	other := e.newGUID()
	own := e.kr.LoopTokens(a.Guid)
	foreign := e.kr.LoopTokens(other)
	oldOnly, _ := credcrypt.NewKeyring(e.oldKey, nil)
	oldTok := oldOnly.LoopTokens(a.Guid)[0]
	mk := func(tok string) string {
		return "Received: x\r\nX-Bongo-Fetched: " + tok + "\r\nSubject: loop\r\n\r\nbody\r\n"
	}
	e.remote.msgs = []fwMsg{
		{id: 1, body: mk(own[0])},
		{id: 2, body: mk(foreign[0])},
		{id: 3, body: mk(oldTok)},
		{id: 4, body: "Subject: x\r\n\r\nX-Bongo-Fetched: " + own[0] + "\r\n"}, // in the body, not the header
	}
	e.pass()
	if e.deliver.count() != 2 {
		t.Fatalf("spooled %d, want 2 (foreign token and body-only text)", e.deliver.count())
	}
	if got := len(e.seen(a)); got != 4 {
		t.Fatalf("seen = %d, want all 4 marked", got)
	}
	if st := e.mustState(a); st.LastRunImported != 2 {
		t.Fatalf("imported = %d", st.LastRunImported)
	}
}

func TestFetchLeadershipLostMidRun(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(5, "x")
	// Writes are MarkSeen only until the first state write, so the third
	// write is message 3's seen record.
	e.app.notLeaderAfter = e.app.calls + 2
	e.pass()
	if got := e.deliver.count(); got != 3 {
		t.Fatalf("spooled %d, want 3: messages 4 and 5 must not be spooled", got)
	}
	if len(e.remote.deleted) != 0 {
		t.Fatalf("deleted %v after leadership was lost", e.remote.deleted)
	}
	if got := e.app.wrote("/fetchseen/"); got != 2 {
		t.Fatalf("seen writes = %d, want 2", got)
	}
	_ = a
}

func TestFetchSweepSecondSighting(t *testing.T) {
	e := newFWEnv(t)
	orphan := e.newGUID()
	if err := e.fs.PutState(fwUser, orphan, &bongov1.FetchState{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := e.fs.MarkSeen(fwUser, orphan, store.POP3SeenHash(fmt.Sprint(i)), &bongov1.FetchSeen{Status: 1}); err != nil {
			t.Fatal(err)
		}
	}
	// A second orphan whose account appears between the sweeps keeps its keys.
	late := e.newGUID()
	if err := e.fs.PutState(fwUser, late, &bongov1.FetchState{Generation: 1}); err != nil {
		t.Fatal(err)
	}
	e.w.sweep(context.Background())
	if _, ok, _ := e.fs.GetState(fwUser, orphan); !ok {
		t.Fatal("deleted on the first sighting")
	}
	if m, _ := e.fs.SeenSet(fwUser, orphan); len(m) != 3 {
		t.Fatal("seen deleted on the first sighting")
	}
	e.addAcct(func(a *bongov1.FetchAccount) { a.Guid = late })
	e.w.sweep(context.Background())
	if _, ok, _ := e.fs.GetState(fwUser, orphan); ok {
		t.Fatal("state survived the second sighting")
	}
	if m, _ := e.fs.SeenSet(fwUser, orphan); len(m) != 0 {
		t.Fatal("seen survived the second sighting")
	}
	if _, ok, _ := e.fs.GetState(fwUser, late); !ok {
		t.Fatal("state of an account created between sweeps was deleted")
	}
}

func TestFetchSweepCap(t *testing.T) {
	e := newFWEnv(t)
	e.w.sweepMax = 30
	orphan := e.newGUID()
	for i := 0; i < 30+10; i++ {
		_ = e.fs.MarkSeen(fwUser, orphan, store.POP3SeenHash(fmt.Sprint(i)), &bongov1.FetchSeen{Status: 1})
	}
	e.w.sweep(context.Background())
	e.w.sweep(context.Background())
	m, _ := e.fs.SeenSet(fwUser, orphan)
	if len(m) != 10 {
		t.Fatalf("left %d, want 10 after one capped sweep", len(m))
	}
}

func TestFetchConnectErrorsCollapse(t *testing.T) {
	errs := []error{
		fmt.Errorf("dial: %w", netx.ErrForbiddenAddress),
		&net.DNSError{Err: "no such host", Name: "imap.example"},
		fmt.Errorf("dial: %w", context.DeadlineExceeded),
		errors.New("550 the password " + fwPassword + " hunter2 was refused for remote-user-xyz"),
	}
	for i, derr := range errs {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			e := newFWEnv(t)
			a := e.addAcct()
			e.remote.dialErr = derr
			e.pass()
			st := e.mustState(a)
			if i < 3 && st.LastError != fetch.CodeConnectFailed {
				t.Fatalf("last_error = %q, want connect_failed", st.LastError)
			}
			valid := false
			for _, c := range fetch.Codes {
				valid = valid || st.LastError == c
			}
			if !valid {
				t.Fatalf("last_error %q is not a code", st.LastError)
			}
			raw, _ := proto.Marshal(st)
			for _, secret := range []string{"hunter2", fwPassword, "remote-user-xyz", "no such host", "was refused"} {
				if bytes.Contains(raw, []byte(secret)) || strings.Contains(e.log.String(), secret) {
					t.Fatalf("%q leaked into state or logs", secret)
				}
			}
			if st.Failures != 1 {
				t.Fatalf("failures = %d", st.Failures)
			}
		})
	}
}

func TestFetchBackoff(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.remote.dialErr = fetch.ErrConnect
	want := []time.Duration{30 * time.Minute, 60 * time.Minute, 120 * time.Minute}
	for i, w := range want {
		e.pass()
		st := e.mustState(a)
		if got := time.Unix(st.NextRunAt, 0).Sub(e.now); got != w {
			t.Fatalf("failure %d: next in %v, want %v", i+1, got, w)
		}
		e.advance(w)
	}
	for range 12 {
		e.pass()
		e.advance(25 * time.Hour)
	}
	e.pass()
	st := e.mustState(a)
	if got := time.Unix(st.NextRunAt, 0).Sub(e.now); got != 24*time.Hour {
		t.Fatalf("capped backoff = %v, want 24h", got)
	}
}

// A run that never ends must end at its own deadline, not hang the pass.
func TestFetchRunHasADeadline(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.remote.blockDial = true
	e.w.runTimeout = 50 * time.Millisecond
	done := make(chan struct{})
	go func() { e.pass(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not end at its timeout")
	}
	if st := e.mustState(a); st.LastError != fetch.CodeConnectFailed {
		t.Fatalf("last_error = %q", st.LastError)
	}
}

func TestFetchShutdownWritesNothing(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.remote.blockDial = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.w.pass(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if e.state(a) != nil {
		t.Fatal("wrote state for a run cut short by shutdown")
	}
}

func TestFetchLogsNoAccountValues(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct(func(a *bongov1.FetchAccount) { a.Folder = "Secret-Folder"; a.Label = "my label" })
	e.addMsgs(2, "BODYMARKER")
	e.pass()
	out := e.log.String()
	if !strings.Contains(out, "fetch: run") || !strings.Contains(out, a.Guid) {
		t.Fatalf("no run line:\n%s", out)
	}
	ct := base64.StdEncoding.EncodeToString(a.Credential.Ciphertext)
	for _, secret := range []string{fwPassword, "remote-user-xyz", "Secret-Folder", "my label", "BODYMARKER", "Subject: s1", ct,
		hex.EncodeToString(a.Credential.Nonce), "Params", "ciphertext"} {
		if strings.Contains(out, secret) {
			t.Fatalf("log leaks %q:\n%s", secret, out)
		}
	}
	// Even the dial parameters that reached the fake never print the password.
	if s := fmt.Sprintf("%v %+v", e.remote.params[0], e.remote.params[0]); strings.Contains(s, fwPassword) {
		t.Fatal("Params prints the password")
	}
}

// The leadership flag is read before every message, not only before the pass:
// a node deposed mid-run must not spool another message.
func TestFetchStopsWhenLeaderFlagDrops(t *testing.T) {
	e := newFWEnv(t)
	a := e.addAcct()
	e.addMsgs(5, "x")
	e.remote.onFetch = func(uid uint32) {
		if uid == 2 {
			e.leader.Store(false)
		}
	}
	e.pass()
	if got := e.deliver.count(); got != 2 {
		t.Fatalf("spooled %d, want 2", got)
	}
	if len(e.remote.deleted) != 0 {
		t.Fatalf("deleted %v after leadership was lost", e.remote.deleted)
	}
	if e.state(a) != nil {
		t.Fatal("a deposed leader wrote state")
	}
}
