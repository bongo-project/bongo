package auth

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// touchApplier applies Raft commands to Pebble the way the FSM does, including
// the touch command: without that dispatch a touch would be stored as a
// literal key and every negative test about it would pass vacuously.
type touchApplier struct {
	ps      *store.PebbleStore
	touches atomic.Int32
	gate    chan struct{} // when non-nil, touches wait for it to close
}

func (a *touchApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && store.IsAppPasswordSyntheticCommand(cmd.Key) {
		a.touches.Add(1)
		if a.gate != nil {
			<-a.gate
		}
		return store.ApplyAppPasswordTouch(a.ps, cmd)
	}
	if cmd.Op == bongov1.Op_OP_DELETE {
		return a.ps.Delete(cmd.Key)
	}
	return a.ps.Set(cmd.Key, cmd.Value)
}

type appEnv struct {
	la      *LocalAuth
	apps    *AppPasswords
	recs    *store.AppPasswordStore
	applier *touchApplier
	kv      *mockKeyGetter
	mm      *store.MetadataManager
	ps      *store.PebbleStore
	logs    *bytes.Buffer
	compare atomic.Int32
	hashMu  sync.Mutex
	hashes  [][]byte // hash argument of each comparison since the last attempt
}

// newAppEnv builds a real LocalAuth with a real record store. bcrypt cost is
// lowered for speed; the dummy hash stays at the default cost, which is why
// the timing tests count comparisons instead of measuring time.
func newAppEnv(t *testing.T) *appEnv {
	t.Helper()
	oldCost := PasswordHashCost
	PasswordHashCost = bcrypt.MinCost
	t.Cleanup(func() { PasswordHashCost = oldCost })

	la, mm, kv := setupTestAuth(t)
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })

	e := &appEnv{la: la, kv: kv, mm: mm, ps: ps, logs: &bytes.Buffer{}}
	e.applier = &touchApplier{ps: ps}
	e.recs = store.NewAppPasswordStore(ps, e.applier)
	e.apps = NewAppPasswords(e.recs, slog.New(slog.NewTextHandler(&lockedWriter{w: e.logs}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	e.apps.now = func() time.Time { return time.Date(2026, 11, 5, 14, 30, 0, 0, time.UTC) }
	la.SetAppPasswords(e.apps)

	oldCompare := compareHash
	compareHash = func(h, p []byte) error {
		e.compare.Add(1)
		e.hashMu.Lock()
		e.hashes = append(e.hashes, h)
		e.hashMu.Unlock()
		return oldCompare(h, p)
	}
	t.Cleanup(func() { compareHash = oldCompare })
	return e
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func (e *appEnv) user(t *testing.T, id, email, password string) {
	t.Helper()
	createTestUser(t, e.kv, e.mm, id, email, password)
}

func (e *appEnv) mutateProfile(t *testing.T, id string, f func(p *bongov1.UserProfile)) {
	t.Helper()
	key := fmt.Sprintf("/user/%s/profile", id)
	var p bongov1.UserProfile
	if err := proto.Unmarshal(e.kv.data[key], &p); err != nil {
		t.Fatal(err)
	}
	f(&p)
	data, err := proto.Marshal(&p)
	if err != nil {
		t.Fatal(err)
	}
	e.kv.data[key] = data
}

func (e *appEnv) create(t *testing.T, userID, label string) string {
	t.Helper()
	pw, _, err := e.apps.Create(userID, label)
	if err != nil {
		t.Fatal(err)
	}
	return pw
}

// attempt runs one protocol authentication and returns how many bcrypt
// comparisons it cost.
func (e *appEnv) attempt(email, password string) (string, int, error) {
	e.compare.Store(0)
	e.hashMu.Lock()
	e.hashes = nil
	e.hashMu.Unlock()
	id, _, err := e.la.AuthenticateProtocol(email, password)
	return id, int(e.compare.Load()), err
}

func TestParseAppPassword(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		wantID string
		wantOK bool
	}{
		{"hyphenated", "abcdef-ghjkmn-pqrstu-vwxyza", "abcdef", true},
		{"no separator", "abcdefghjkmnpqrstuvwxyza", "abcdef", true},
		{"spaces", "abcdef ghjkmn pqrstu vwxyza", "abcdef", true},
		{"auto-capitalised", "Abcdef-Ghjkmn-Pqrstu-Vwxyza", "abcdef", true},
		{"upper", "ABCDEF-GHJKMN-PQRSTU-VWXYZA", "abcdef", true},
		{"too short", "abcdef-ghjkmn-pqrstu-vwxyz", "", false},
		{"too long", "abcdef-ghjkmn-pqrstu-vwxyzab", "", false},
		{"ambiguous letter i", "abcdef-ghjkmn-pqrstu-vwxyzi", "", false},
		{"digit", "abcdef-ghjkmn-pqrstu-vwxyz1", "", false},
		{"slash", "abcdef/ghjkmn-pqrstu-vwxyza", "", false},
		{"nul", "abcdef-ghjkmn-pqrstu-vwxyz\x00", "", false},
		{"non ascii", "abcdef-ghjkmn-pqrstu-vwxyzé", "", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, secret, ok := ParseAppPassword(tt.in)
			if ok != tt.wantOK || id != tt.wantID {
				t.Fatalf("ParseAppPassword(%q) = %q, %q, %v", tt.in, id, secret, ok)
			}
			if ok && FormatAppPassword(id+secret) != "abcdef-ghjkmn-pqrstu-vwxyza" {
				t.Errorf("round trip = %q", FormatAppPassword(id+secret))
			}
		})
	}
}

func TestCreateListRevoke(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")

	pw, info, err := e.apps.Create("u1", "  iPhone  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := ParseAppPassword(pw); !ok || len(pw) != 27 || strings.Count(pw, "-") != 3 {
		t.Errorf("password format = %q", pw)
	}
	if info.Label != "iPhone" || info.ID != pw[:6] {
		t.Errorf("info = %+v", info)
	}

	// Stored record: bcrypt of the secret, never the plaintext.
	rec, ok, err := e.recs.Get("u1", info.ID)
	if err != nil || !ok {
		t.Fatalf("record missing: %v", err)
	}
	_, secret, _ := ParseAppPassword(pw)
	if bcrypt.CompareHashAndPassword(rec.PasswordHash, []byte(secret)) != nil {
		t.Error("hash does not verify the secret")
	}
	if cost, _ := bcrypt.Cost(rec.PasswordHash); cost != PasswordHashCost {
		t.Errorf("hash cost = %d, want account cost %d", cost, PasswordHashCost)
	}
	for _, f := range []string{pw, secret, strings.ReplaceAll(pw, "-", "")} {
		if bytes.Contains(rec.PasswordHash, []byte(f)) || rec.Label == f {
			t.Errorf("record field equals plaintext %q", f)
		}
	}
	if rec.LastUsedAt != 0 {
		t.Errorf("new record last_used_at = %d", rec.LastUsedAt)
	}

	list, err := e.apps.List("u1")
	if err != nil || len(list) != 1 || list[0].ID != info.ID || !list[0].LastUsedAt.IsZero() {
		t.Fatalf("List = %+v %v", list, err)
	}

	if err := e.apps.Revoke("u1", info.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.apps.Revoke("u1", info.ID); err != nil {
		t.Errorf("second revoke: %v", err)
	}
	if list, _ := e.apps.List("u1"); len(list) != 0 {
		t.Errorf("List after revoke = %+v", list)
	}
	if err := e.apps.Revoke("u1", "../x"); !errors.Is(err, store.ErrInvalidAppPasswordID) {
		t.Errorf("revoke of malformed id = %v", err)
	}

	logs := e.logs.String()
	if !strings.Contains(logs, "app password created") || !strings.Contains(logs, "app password revoked") ||
		!strings.Contains(logs, "lookup="+info.ID) || !strings.Contains(logs, "label=iPhone") {
		t.Errorf("creation/revocation not logged with label and lookup: %s", logs)
	}
	assertNoSecrets(t, logs, pw, secret, rec.PasswordHash)
}

func assertNoSecrets(t *testing.T, logs, pw, secret string, hash []byte) {
	t.Helper()
	for _, s := range []string{pw, secret, strings.ReplaceAll(pw, "-", ""), string(hash)} {
		if s != "" && strings.Contains(logs, s) {
			t.Errorf("logs contain secret material %q", s)
		}
	}
}

func TestCreateLabelValidation(t *testing.T) {
	e := newAppEnv(t)
	for name, label := range map[string]string{
		"empty":      "",
		"blank":      "   ",
		"newline":    "a\nb",
		"escape":     "a\x1b[31m",
		"too long":   strings.Repeat("x", MaxAppPasswordLabelLen+1),
		"bad utf8":   "a\xffb",
		"tab":        "a\tb",
		"nul":        "a\x00",
		"del":        "a\x7f",
		"c1 control": "a\u0085b",
	} {
		if _, _, err := e.apps.Create("u1", label); !errors.Is(err, ErrInvalidAppPasswordLabel) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if recs, _ := e.recs.List("u1"); len(recs) != 0 {
		t.Errorf("records created for invalid labels: %d", len(recs))
	}
	if _, _, err := e.apps.Create("u1", strings.Repeat("é", MaxAppPasswordLabelLen)); err != nil {
		t.Errorf("label of exactly the maximum characters refused: %v", err)
	}
}

// collidingRecords reports every lookup as taken for the first n checks.
type collidingRecords struct {
	AppPasswordRecords
	left int
}

func (c *collidingRecords) Get(userID, id string) (*bongov1.AppPassword, bool, error) {
	if c.left > 0 {
		c.left--
		return &bongov1.AppPassword{Id: id}, true, nil
	}
	return c.AppPasswordRecords.Get(userID, id)
}

func TestCreateRegeneratesOnCollision(t *testing.T) {
	e := newAppEnv(t)
	c := &collidingRecords{AppPasswordRecords: e.recs, left: 3}
	p := NewAppPasswords(c, nil)
	if _, _, err := p.Create("u1", "x"); err != nil {
		t.Fatalf("create after collisions: %v", err)
	}
	if c.left != 0 {
		t.Errorf("collision checks left = %d", c.left)
	}
	c.left = 1000
	if _, _, err := p.Create("u1", "x"); err == nil {
		t.Error("create succeeded although every lookup id is taken")
	}
}

func TestAuthenticateProtocolCostsOneComparison(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "alice@example.com", "account-pw")
	e.user(t, "u2", "bob@example.com", "bob-pw")
	e.user(t, "u3", "dis@example.com", "dis-pw")
	e.mutateProfile(t, "u3", func(p *bongov1.UserProfile) { p.Disabled = true })
	e.user(t, "res", "room@example.com", "room-pw")
	e.mutateProfile(t, "res", func(p *bongov1.UserProfile) { p.Kind = ResourceKind })

	good := e.create(t, "u1", "phone")
	e.create(t, "u1", "laptop")
	e.create(t, "u1", "tablet")
	goodID := good[:6]
	// Same lookup id, wrong secret.
	wrongSecret := goodID + "-" + "aaaaaa-aaaaaa-aaaaaa"
	// Shape-matching password whose lookup id belongs to nobody.
	noRecord := "aaaaaa-aaaaaa-aaaaaa-aaaaaa"
	if goodID == "aaaaaa" {
		noRecord = "bbbbbb-bbbbbb-bbbbbb-bbbbbb"
	}
	bobGood := e.create(t, "u2", "bob phone")
	disGood := e.create(t, "u3", "dis phone")
	resGood := e.create(t, "res", "room")

	tests := []struct {
		name     string
		user     string
		password string
		wantID   string
	}{
		{"unknown user", "nobody@example.com", good, ""},
		{"unknown user plain password", "nobody@example.com", "x", ""},
		{"account password", "alice@example.com", "account-pw", "u1"},
		{"wrong plain password", "alice@example.com", "nope", ""},
		{"user without app passwords, shaped", "dis@example.com", noRecord, ""},
		{"disabled user, valid app password", "dis@example.com", disGood, ""},
		{"disabled user, account password", "dis@example.com", "dis-pw", ""},
		{"resource, valid app password", "room@example.com", resGood, ""},
		{"valid app password", "alice@example.com", good, "u1"},
		{"valid, typed capitalised without hyphens", "alice@example.com", strings.ToUpper(strings.ReplaceAll(good, "-", "")), "u1"},
		{"wrong secret on existing lookup, several records", "alice@example.com", wrongSecret, ""},
		{"shaped, no record", "alice@example.com", noRecord, ""},
		{"other user's app password", "alice@example.com", bobGood, ""},
		{"bob with own", "bob@example.com", bobGood, "u2"},
		{"bob with alice's", "bob@example.com", good, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, n, err := e.attempt(tt.user, tt.password)
			if n != 1 {
				t.Errorf("bcrypt comparisons = %d, want exactly 1", n)
			}
			if tt.wantID == "" {
				if !errors.Is(err, ErrBadCredentials) || id != "" {
					t.Errorf("got id=%q err=%v, want ErrBadCredentials", id, err)
				}
				return
			}
			if err != nil || id != tt.wantID {
				t.Errorf("got id=%q err=%v, want %q", id, err, tt.wantID)
			}
		})
	}
	e.apps.Wait()
}

// An app password presented under the same local part on another domain
// authenticates only its owner.
func TestAppPasswordAcrossDomains(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "a", "alice@one.example", "pw-a")
	e.user(t, "b", "alice@two.example", "pw-b")
	pw := e.create(t, "a", "phone")
	if id, _, err := e.attempt("alice@one.example", pw); err != nil || id != "a" {
		t.Fatalf("owner: %q %v", id, err)
	}
	if id, _, err := e.attempt("alice@two.example", pw); err == nil || id != "" {
		t.Fatalf("same local part, other domain authenticated as %q", id)
	}
	e.apps.Wait()
}

func TestAccountPasswordShapedLikeAppPasswordStillWorks(t *testing.T) {
	e := newAppEnv(t)
	shaped := "Abcdef Ghjkmn-Pqrstu-vwxyza"
	e.user(t, "u1", "a@example.com", shaped)
	e.create(t, "u1", "phone") // a record exists, with another lookup id
	id, n, err := e.attempt("a@example.com", shaped)
	if err != nil || id != "u1" || n != 1 {
		t.Fatalf("id=%q n=%d err=%v", id, n, err)
	}
	// Account passwords with hyphens, spaces and capitals keep working too.
	for _, pw := range []string{"My-Pass Word-1", "ABC-DEF"} {
		e.user(t, "x"+pw, "x"+pw+"@example.com", pw)
		if id, _, err := e.attempt("x"+pw+"@example.com", pw); err != nil || id != "x"+pw {
			t.Errorf("%q: %q %v", pw, id, err)
		}
	}
}

// Web login and re-authentication call Authenticate and AuthenticateProfile,
// which must refuse an app password even though a real one exists.
func TestAppPasswordRefusedOnRestrictiveMethods(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "account-pw")
	pw := e.create(t, "u1", "phone")

	if _, err := e.la.Authenticate("a@example.com", pw); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("Authenticate accepted an app password: %v", err)
	}
	if id, p, err := e.la.AuthenticateProfile("a@example.com", pw); !errors.Is(err, ErrBadCredentials) || id != "" || p != nil {
		t.Errorf("AuthenticateProfile accepted an app password: %q %v %v", id, p, err)
	}
	// Control: the same call with the account password succeeds.
	if _, err := e.la.Authenticate("a@example.com", "account-pw"); err != nil {
		t.Errorf("account password refused: %v", err)
	}
	// And the protocol method accepts what they refuse.
	if _, _, err := e.la.AuthenticateProtocol("a@example.com", pw); err != nil {
		t.Errorf("protocol method refused the app password: %v", err)
	}
	e.apps.Wait()
}

func TestRevokedAppPasswordFailsNextAttempt(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	keep := e.create(t, "u1", "keep")
	drop := e.create(t, "u1", "drop")
	for _, p := range []string{keep, drop} {
		if _, _, err := e.la.AuthenticateProtocol("a@example.com", p); err != nil {
			t.Fatal(err)
		}
	}
	e.apps.Wait()
	if err := e.apps.Revoke("u1", drop[:6]); err != nil {
		t.Fatal(err)
	}
	if _, n, err := e.attempt("a@example.com", drop); !errors.Is(err, ErrBadCredentials) || n != 1 {
		t.Errorf("revoked credential: n=%d err=%v", n, err)
	}
	if _, _, err := e.la.AuthenticateProtocol("a@example.com", keep); err != nil {
		t.Errorf("other credential disturbed: %v", err)
	}
	e.apps.Wait()
}

func TestUnreadableRecordIsIndistinguishable(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	pw := e.create(t, "u1", "phone")
	id := pw[:6]
	key := store.AppPasswordKey("u1", id)

	cases := map[string][]byte{
		"corrupt":    {0xff, 0xff, 0xff},
		"empty hash": mustMarshal(t, &bongov1.AppPassword{Id: id, Label: "x"}),
	}
	logged := 0
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if err := e.ps.Set(key, raw); err != nil {
				t.Fatal(err)
			}
			_, n, err := e.attempt("a@example.com", pw)
			if err != ErrBadCredentials { //nolint:errorlint // identity matters: no wrapped error may escape
				t.Errorf("err = %v, want exactly ErrBadCredentials", err)
			}
			if n != 1 {
				t.Errorf("comparisons = %d, want 1", n)
			}
			// bcrypt returns fast on an empty hash, so the comparison must run
			// against the dummy hash or the timing shows the record exists.
			e.hashMu.Lock()
			hs := e.hashes
			e.hashMu.Unlock()
			if len(hs) != 1 || !bytes.Equal(hs[0], dummyBcryptHash) {
				t.Errorf("compared against %q, want the dummy hash", hs)
			}
			logged++
			if n := strings.Count(e.logs.String(), "app password record unreadable"); n != logged {
				t.Errorf("unreadable-record log lines = %d, want %d", n, logged)
			}
		})
	}
	assertNoSecrets(t, e.logs.String(), pw, "", nil)
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	b, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWithoutAppPasswordsAccountPasswordOnly(t *testing.T) {
	la, mm, kv := setupTestAuth(t)
	createTestUser(t, kv, mm, "u1", "a@example.com", "pw")
	if _, _, err := la.AuthenticateProtocol("a@example.com", "pw"); err != nil {
		t.Errorf("account password: %v", err)
	}
	if _, _, err := la.AuthenticateProtocol("a@example.com", "aaaaaa-aaaaaa-aaaaaa-aaaaaa"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("shaped password: %v", err)
	}
}

func TestLastUsedWrittenOncePerDay(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	pw := e.create(t, "u1", "phone")
	id := pw[:6]
	login := func() {
		t.Helper()
		if _, _, err := e.la.AuthenticateProtocol("a@example.com", pw); err != nil {
			t.Fatal(err)
		}
		e.apps.Wait()
	}

	login()
	login()
	login()
	if got := e.applier.touches.Load(); got != 1 {
		t.Fatalf("touch commands after three logins on one day = %d, want 1", got)
	}
	rec, _, _ := e.recs.Get("u1", id)
	today := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC).Unix()
	if rec.LastUsedAt != today {
		t.Errorf("last_used_at = %d, want day start %d", rec.LastUsedAt, today)
	}

	e.apps.now = func() time.Time { return time.Date(2026, 11, 6, 0, 0, 1, 0, time.UTC) }
	login()
	login()
	if got := e.applier.touches.Load(); got != 2 {
		t.Errorf("touch commands after the next day = %d, want 2", got)
	}

	if n := strings.Count(e.logs.String(), "app password first used"); n != 1 {
		t.Errorf("first use logged %d times", n)
	}
	if !strings.Contains(e.logs.String(), "lookup="+id) {
		t.Error("first use log lacks the lookup id")
	}
	_, secret, _ := ParseAppPassword(pw)
	assertNoSecrets(t, e.logs.String(), pw, secret, rec.PasswordHash)
}

func TestConcurrentLoginsIssueOneTouch(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "u1", "a@example.com", "pw")
	pw := e.create(t, "u1", "phone")
	e.applier.gate = make(chan struct{})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := e.la.AuthenticateProtocol("a@example.com", pw); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait() // logins return while the touch is still blocked: it is detached
	close(e.applier.gate)
	e.apps.Wait()
	if got := e.applier.touches.Load(); got != 1 {
		t.Errorf("touch commands from concurrent logins = %d, want 1", got)
	}
}

// failingApplier models a follower or a leaderless cluster.
type failingApplier struct{ err error }

func (f failingApplier) ApplyCommand(*bongov1.RaftCommand, time.Duration) error { return f.err }

func TestFailedTouchDoesNotFailLoginAndIsLogged(t *testing.T) {
	notLeader := errors.New("not the raft leader")
	for _, tt := range []struct {
		name      string
		benign    bool
		wantLevel string
		absentLvl string
	}{
		{"expected failure is debug", true, "level=DEBUG", "level=WARN"},
		{"unexpected failure is warn", false, "level=WARN", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newAppEnv(t)
			e.user(t, "u1", "a@example.com", "pw")
			pw := e.create(t, "u1", "phone")
			e.apps.records = &failTouch{AppPasswordRecords: e.recs, err: notLeader}
			if tt.benign {
				e.apps.SetBenignTouchError(func(err error) bool { return errors.Is(err, notLeader) })
			}
			if _, _, err := e.la.AuthenticateProtocol("a@example.com", pw); err != nil {
				t.Fatalf("login failed because of the touch: %v", err)
			}
			e.apps.Wait()
			logs := e.logs.String()
			if !strings.Contains(logs, tt.wantLevel+" msg=\"app password last-used not recorded\"") {
				t.Errorf("logs = %s", logs)
			}
			if tt.absentLvl != "" && strings.Contains(logs, tt.absentLvl+" msg=\"app password last-used") {
				t.Errorf("logged at %s: %s", tt.absentLvl, logs)
			}
			if strings.Contains(logs, "first used") {
				t.Error("first use logged although the write failed")
			}
		})
	}
}

type failTouch struct {
	AppPasswordRecords
	err error
}

func (f *failTouch) Touch(string, string, int64) error { return f.err }

// Disabled and resource accounts are refused after the compare on the app
// password path, and no last-used write or first-use log follows.
func TestRefusedAccountsDoNotTouch(t *testing.T) {
	e := newAppEnv(t)
	e.user(t, "d", "d@example.com", "pw")
	e.user(t, "r", "r@example.com", "pw")
	dPW := e.create(t, "d", "phone")
	rPW := e.create(t, "r", "phone")
	e.mutateProfile(t, "d", func(p *bongov1.UserProfile) { p.Disabled = true })
	e.mutateProfile(t, "r", func(p *bongov1.UserProfile) { p.Kind = ResourceKind })
	for email, pw := range map[string]string{"d@example.com": dPW, "r@example.com": rPW} {
		if _, _, err := e.la.AuthenticateProtocol(email, pw); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("%s: %v", email, err)
		}
	}
	e.apps.Wait()
	if e.applier.touches.Load() != 0 || strings.Contains(e.logs.String(), "first used") {
		t.Error("refused account touched or logged first use")
	}
}
