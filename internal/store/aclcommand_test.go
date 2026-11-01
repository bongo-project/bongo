package store

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type aclEnv struct {
	ps  *PebbleStore
	cal *CalendarStore
	acl *ACLStore
}

func newACLEnv(t *testing.T) *aclEnv {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	ap := &directApplier{store: ps}
	return &aclEnv{ps: ps, cal: NewCalendarStore(ps, ap), acl: NewACLStore(ps, ap)}
}

func (e *aclEnv) calendar(t *testing.T, owner, guid string) {
	t.Helper()
	if _, err := e.cal.Create(owner, &bongov1.Calendar{Guid: guid, Name: guid}); err != nil {
		t.Fatalf("Create calendar: %v", err)
	}
}

func (e *aclEnv) grant(t *testing.T, owner, cal, grantee, level, state string) {
	t.Helper()
	if err := e.acl.SetCalendarGrant(owner, cal, grantee, level, state, owner, time.Unix(1000, 0)); err != nil {
		t.Fatalf("SetCalendarGrant: %v", err)
	}
}

// aclKeys returns every key under /acl/ and /aclrev/, sorted.
func aclKeys(t *testing.T, ps *PebbleStore) []string {
	t.Helper()
	var out []string
	for _, prefix := range []string{"/acl/", "/aclrev/"} {
		keys, err := ps.scanKeys([]byte(prefix))
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, k := range keys {
			out = append(out, string(k))
		}
	}
	slices.Sort(out)
	return out
}

func TestACLSetAndRevokeWriteBothKeysOrNeither(t *testing.T) {
	e := newACLEnv(t)

	set, err := BuildACLCommand(ACLRequest{Op: ACLOpSet, Kind: ACLKindCalendar, Owner: "alice", Resource: "cal1", Grantee: "bob",
		Level: GrantLevelRead, State: GrantStateAccepted, GrantedBy: "alice", At: 100})
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := BuildACLCommand(ACLRequest{Op: ACLOpRevoke, Kind: ACLKindCalendar, Owner: "alice", Resource: "cal1", Grantee: "bob"})
	if err != nil {
		t.Fatal(err)
	}

	// An aborted commit leaves nothing behind: every write is inside the batch.
	orig := commitSyncBatch
	t.Cleanup(func() { commitSyncBatch = orig })
	errAbort := errors.New("crash before commit")
	commitSyncBatch = func(ps *PebbleStore, fn func(batch *pebble.Batch) error) error {
		batch := ps.db.NewBatch()
		defer batch.Close()
		if err := fn(batch); err != nil {
			return err
		}
		return errAbort
	}
	if err := ApplyACLCommand(e.ps, set); !errors.Is(err, errAbort) {
		t.Fatalf("aborted set err = %v", err)
	}
	if keys := aclKeys(t, e.ps); len(keys) != 0 {
		t.Fatalf("aborted set left keys: %v", keys)
	}

	commitSyncBatch = orig
	if err := ApplyACLCommand(e.ps, set); err != nil {
		t.Fatalf("set: %v", err)
	}
	want := []string{
		"/acl/calendar/alice/cal1/u:bob",
		"/aclrev/u:bob/calendar/alice/cal1",
	}
	if got := aclKeys(t, e.ps); !slices.Equal(got, want) {
		t.Fatalf("keys after set = %v, want %v", got, want)
	}

	commitSyncBatch = func(ps *PebbleStore, fn func(batch *pebble.Batch) error) error {
		batch := ps.db.NewBatch()
		defer batch.Close()
		if err := fn(batch); err != nil {
			return err
		}
		return errAbort
	}
	if err := ApplyACLCommand(e.ps, revoke); !errors.Is(err, errAbort) {
		t.Fatalf("aborted revoke err = %v", err)
	}
	if got := aclKeys(t, e.ps); !slices.Equal(got, want) {
		t.Fatalf("aborted revoke changed keys: %v", got)
	}

	commitSyncBatch = orig
	if err := ApplyACLCommand(e.ps, revoke); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := aclKeys(t, e.ps); len(got) != 0 {
		t.Fatalf("keys after revoke = %v", got)
	}
}

func TestBuildACLCommandRefusals(t *testing.T) {
	ok := ACLRequest{Op: ACLOpSet, Kind: ACLKindCalendar, Owner: "a", Resource: "c", Grantee: "b", Level: GrantLevelRead, State: GrantStateAccepted}
	mod := func(f func(r *ACLRequest)) ACLRequest { r := ok; f(&r); return r }
	tests := []struct {
		name string
		req  ACLRequest
	}{
		{"level empty", mod(func(r *ACLRequest) { r.Level = "" })},
		{"level admin", mod(func(r *ACLRequest) { r.Level = "admin" })},
		{"level uppercase", mod(func(r *ACLRequest) { r.Level = "READ" })},
		{"state empty", mod(func(r *ACLRequest) { r.State = "" })},
		{"state rejected", mod(func(r *ACLRequest) { r.State = "rejected" })},
		{"kind mailbox", mod(func(r *ACLRequest) { r.Kind = "mailbox" })},
		{"kind empty", mod(func(r *ACLRequest) { r.Kind = "" })},
		{"owner empty", mod(func(r *ACLRequest) { r.Owner = "" })},
		{"resource empty", mod(func(r *ACLRequest) { r.Resource = "" })},
		{"grantee empty", mod(func(r *ACLRequest) { r.Grantee = "" })},
		{"unknown op", mod(func(r *ACLRequest) { r.Op = "grant" })},
		{"revoke without grantee", ACLRequest{Op: ACLOpRevoke, Kind: ACLKindCalendar, Owner: "a", Resource: "c"}},
		{"revoke_resource without resource", ACLRequest{Op: ACLOpRevokeResource, Kind: ACLKindCalendar, Owner: "a"}},
		{"revoke_owner without owner", ACLRequest{Op: ACLOpRevokeOwner, Kind: ACLKindCalendar}},
		{"revoke_grantee without grantee", ACLRequest{Op: ACLOpRevokeGrantee, Kind: ACLKindCalendar}},
	}
	if _, err := BuildACLCommand(ok); err != nil {
		t.Fatalf("baseline refused: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildACLCommand(tt.req); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// A payload built by hand, bypassing BuildACLCommand, must be refused by the
// planner too: the FSM is the last line of defence.
func TestPlanACLRefusesHandBuiltPayload(t *testing.T) {
	get := func([]byte) ([]byte, error) { return nil, ErrNotFound }
	scan := func([]byte) ([][]byte, error) { return nil, nil }
	for name, payload := range map[string]aclPayload{
		"bad level": {Op: ACLOpSet, Kind: ACLKindCalendar, Owner: "a", Resource: "c", Grantee: "b", Level: "owner", State: GrantStateAccepted},
		"bad state": {Op: ACLOpSet, Kind: ACLKindCalendar, Owner: "a", Resource: "c", Grantee: "b", Level: GrantLevelRead, State: "x"},
		"bad kind":  {Op: ACLOpRevoke, Kind: "mailbox", Owner: "a", Resource: "c", Grantee: "b"},
		"no owner":  {Op: ACLOpRevokeOwner, Kind: ACLKindCalendar},
	} {
		t.Run(name, func(t *testing.T) {
			val, _ := json.Marshal(payload)
			if _, _, err := PlanACL(get, scan, &bongov1.RaftCommand{Key: []byte(aclPrefix + payload.Op), Value: val}); err == nil {
				t.Error("planner accepted a bad payload")
			}
		})
	}
	if _, _, err := PlanACL(get, scan, &bongov1.RaftCommand{Key: []byte("/other"), Value: []byte("{}")}); err == nil {
		t.Error("planner accepted a non-acl key")
	}
	if _, _, err := PlanACL(get, scan, &bongov1.RaftCommand{Key: []byte(aclPrefix + "set"), Value: []byte("not json")}); err == nil {
		t.Error("planner accepted malformed json")
	}
}

func TestACLRevokeOperations(t *testing.T) {
	seed := func(t *testing.T) *aclEnv {
		e := newACLEnv(t)
		e.grant(t, "alice", "c1", "bob", GrantLevelRead, GrantStateAccepted)
		e.grant(t, "alice", "c1", "carl", GrantLevelWrite, GrantStateAccepted)
		e.grant(t, "alice", "c2", "bob", GrantLevelFreeBusy, GrantStateAccepted)
		e.grant(t, "dave", "d1", "bob", GrantLevelRead, GrantStateAccepted)
		e.grant(t, "dave", "d1", "alice", GrantLevelRead, GrantStateAccepted)
		return e
	}
	tests := []struct {
		name string
		do   func(e *aclEnv) error
		want []string
	}{
		{"revoke one", func(e *aclEnv) error { return e.acl.RevokeCalendarGrant("alice", "c1", "bob") }, []string{
			"/acl/calendar/alice/c1/u:carl", "/acl/calendar/alice/c2/u:bob", "/acl/calendar/dave/d1/u:alice", "/acl/calendar/dave/d1/u:bob",
			"/aclrev/u:alice/calendar/dave/d1", "/aclrev/u:bob/calendar/alice/c2", "/aclrev/u:bob/calendar/dave/d1", "/aclrev/u:carl/calendar/alice/c1",
		}},
		{"revoke absent", func(e *aclEnv) error { return e.acl.RevokeCalendarGrant("alice", "c1", "nobody") }, nil},
		{"revoke resource", func(e *aclEnv) error { return e.acl.RevokeCalendar("alice", "c1") }, []string{
			"/acl/calendar/alice/c2/u:bob", "/acl/calendar/dave/d1/u:alice", "/acl/calendar/dave/d1/u:bob",
			"/aclrev/u:alice/calendar/dave/d1", "/aclrev/u:bob/calendar/alice/c2", "/aclrev/u:bob/calendar/dave/d1",
		}},
		{"revoke owner", func(e *aclEnv) error { return RevokeUserGrants(e.acl.applier, "dave") }, []string{
			// dave owns d1 and holds nothing: only his own grants go.
			"/acl/calendar/alice/c1/u:bob", "/acl/calendar/alice/c1/u:carl", "/acl/calendar/alice/c2/u:bob",
			"/aclrev/u:bob/calendar/alice/c1", "/aclrev/u:bob/calendar/alice/c2", "/aclrev/u:carl/calendar/alice/c1",
		}},
		{"revoke grantee only", func(e *aclEnv) error {
			cmd, err := BuildACLCommand(ACLRequest{Op: ACLOpRevokeGrantee, Kind: ACLKindCalendar, Grantee: "bob"})
			if err != nil {
				return err
			}
			return e.acl.applier.ApplyCommand(cmd, time.Second)
		}, []string{
			"/acl/calendar/alice/c1/u:carl", "/acl/calendar/dave/d1/u:alice",
			"/aclrev/u:alice/calendar/dave/d1", "/aclrev/u:carl/calendar/alice/c1",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := seed(t)
			before := aclKeys(t, e.ps)
			if err := tt.do(e); err != nil {
				t.Fatal(err)
			}
			got := aclKeys(t, e.ps)
			want := tt.want
			if tt.name == "revoke absent" {
				want = before
			}
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("keys = %v\nwant   %v", got, want)
			}
			// Forward and reverse always agree.
			for _, k := range got {
				ref, ok := parseACLKey(k)
				if !ok {
					t.Fatalf("unparseable key %q", k)
				}
				if strings.HasPrefix(k, "/acl/") && !slices.Contains(got, string(ref.reverseKey())) {
					t.Errorf("forward %q has no reverse", k)
				}
				if strings.HasPrefix(k, "/aclrev/") && !slices.Contains(got, string(ref.forwardKey())) {
					t.Errorf("reverse %q has no forward", k)
				}
			}
			// Replaying the same command changes nothing.
			if err := tt.do(e); err != nil {
				t.Fatalf("replay: %v", err)
			}
			if again := aclKeys(t, e.ps); !slices.Equal(again, got) {
				t.Errorf("replay changed keys: %v", again)
			}
		})
	}
}

func TestACLEscapingAndPrefixBoundaries(t *testing.T) {
	e := newACLEnv(t)
	// IDs with separators must not steer into another owner's key space.
	e.grant(t, "a/b", "c:d", "e%2Ff", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "ab", "c1", "bob", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "abc", "c1", "bob", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "ab", "c10", "bob", GrantLevelRead, GrantStateAccepted)

	for _, k := range aclKeys(t, e.ps) {
		if strings.HasPrefix(k, "/acl/") && len(strings.Split(k, "/")) != 6 {
			t.Errorf("forward key %q has an unescaped separator", k)
		}
	}
	g, err := e.acl.GetCalendarGrant("a/b", "c:d", "e%2Ff")
	if err != nil || g == nil || g.Level != GrantLevelRead {
		t.Fatalf("escaped grant not readable: %v %v", g, err)
	}
	if n, _ := e.acl.CountCalendarGrants("ab", "c1"); n != 1 {
		t.Errorf("count(ab,c1) = %d, want 1 (c10 is another calendar)", n)
	}
	if err := e.acl.RevokeCalendar("ab", "c1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/acl/calendar/abc/c1/u:bob", "/acl/calendar/ab/c10/u:bob"} {
		if _, err := e.ps.Get([]byte(want)); err != nil {
			t.Errorf("revoking ab/c1 removed %s", want)
		}
	}
	if err := RevokeUserGrants(e.acl.applier, "ab"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ps.Get([]byte("/acl/calendar/abc/c1/u:bob")); err != nil {
		t.Error("revoking owner ab removed owner abc's grant")
	}
	// A grantee that is a prefix of another ("bob" and "bobby") is not the same
	// grantee: revoking one must leave the other's grants.
	e.grant(t, "zed", "z1", "bob", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "zed", "z1", "bobby", GrantLevelRead, GrantStateAccepted)
	cmd, err := BuildACLCommand(ACLRequest{Op: ACLOpRevokeGrantee, Kind: ACLKindCalendar, Grantee: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.acl.applier.ApplyCommand(cmd, time.Second); err != nil {
		t.Fatal(err)
	}
	if g, _ := e.acl.GetCalendarGrant("zed", "z1", "bob"); g != nil {
		t.Error("revoke_grantee bob left bob's grant")
	}
	if g, _ := e.acl.GetCalendarGrant("zed", "z1", "bobby"); g == nil {
		t.Error("revoke_grantee bob removed bobby's grant")
	}
	// Round trip through the parser for the awkward IDs.
	ref, ok := parseACLKey(string(ACLKey("calendar", "a/b", "c:d", "e%2Ff")))
	if !ok || ref.Owner != "a/b" || ref.Resource != "c:d" || ref.Grantee != "e%2Ff" {
		t.Errorf("parse round trip = %+v %v", ref, ok)
	}
}

func TestACLSetKeepsCreatedAt(t *testing.T) {
	e := newACLEnv(t)
	if err := e.acl.SetCalendarGrant("a", "c", "b", GrantLevelFreeBusy, GrantStatePending, "a", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := e.acl.SetCalendarGrant("a", "c", "b", GrantLevelWrite, GrantStateAccepted, "a", time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	g, _ := e.acl.GetCalendarGrant("a", "c", "b")
	if g.CreatedAt != 100 || g.UpdatedAt != 200 || g.Level != GrantLevelWrite || g.State != GrantStateAccepted || g.GrantedBy != "a" {
		t.Errorf("grant = %+v", g)
	}
}

func TestHasCalendarAccess(t *testing.T) {
	e := newACLEnv(t)
	for _, c := range [][2]string{{"owner", "live"}, {"owner", "other"}, {"ownerx", "cal"}, {"o2", "cal"}} {
		e.calendar(t, c[0], c[1])
	}
	e.grant(t, "owner", "live", "fb", GrantLevelFreeBusy, GrantStateAccepted)
	e.grant(t, "owner", "live", "rd", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "owner", "live", "wr", GrantLevelWrite, GrantStateAccepted)
	e.grant(t, "owner", "live", "pend", GrantLevelWrite, GrantStatePending)
	e.grant(t, "owner", "gone", "ghost", GrantLevelRead, GrantStateAccepted) // calendar never existed
	e.grant(t, "ownerx", "cal", "prefix", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "o2", "cal", "slash", GrantLevelRead, GrantStateAccepted)

	tests := []struct {
		name, owner, who, min string
		want                  bool
	}{
		{"freebusy satisfies freebusy", "owner", "fb", GrantLevelFreeBusy, true},
		{"read satisfies freebusy", "owner", "rd", GrantLevelFreeBusy, true},
		{"write satisfies freebusy", "owner", "wr", GrantLevelFreeBusy, true},
		{"freebusy does not satisfy read", "owner", "fb", GrantLevelRead, false},
		{"read does not satisfy write", "owner", "rd", GrantLevelWrite, false},
		{"write satisfies write", "owner", "wr", GrantLevelWrite, true},
		{"pending write grants nothing", "owner", "pend", GrantLevelFreeBusy, false},
		{"grant on missing calendar", "owner", "ghost", GrantLevelFreeBusy, false},
		{"grantee of owner x is not owner", "owner", "prefix", GrantLevelFreeBusy, false},
		{"owner x itself works", "ownerx", "prefix", GrantLevelFreeBusy, true},
		{"slash owner steering", "o2/cal", "slash", GrantLevelFreeBusy, false},
		{"stranger", "owner", "nobody", GrantLevelFreeBusy, false},
		{"empty owner", "", "fb", GrantLevelFreeBusy, false},
		{"empty grantee", "owner", "", GrantLevelFreeBusy, false},
		{"unknown min level", "owner", "wr", "admin", false},
		{"case differs", "owner", "FB", GrantLevelFreeBusy, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.acl.HasCalendarAccess(tt.owner, tt.who, tt.min)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalendarDeleteRevokesGrants(t *testing.T) {
	e := newACLEnv(t)
	e.calendar(t, "alice", "c1")
	e.calendar(t, "alice", "c2")
	e.grant(t, "alice", "c1", "bob", GrantLevelRead, GrantStateAccepted)
	e.grant(t, "alice", "c2", "bob", GrantLevelRead, GrantStateAccepted)

	if err := e.cal.Delete("alice", "c1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/acl/calendar/alice/c2/u:bob", "/aclrev/u:bob/calendar/alice/c2"}
	if got := aclKeys(t, e.ps); !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	if _, err := e.cal.Get("alice", "c1"); err == nil {
		t.Error("calendar still present")
	}
}

type failACLApplier struct{ inner *directApplier }

func (f failACLApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if IsACLSyntheticCommand(cmd.Key) {
		return errors.New("raft unavailable")
	}
	return f.inner.ApplyCommand(cmd, d)
}

// If the revoke fails the calendar must stay, so a retry can finish the job.
func TestCalendarDeleteStopsWhenRevokeFails(t *testing.T) {
	e := newACLEnv(t)
	e.calendar(t, "alice", "c1")
	cs := NewCalendarStore(e.ps, failACLApplier{inner: &directApplier{store: e.ps}})
	if err := cs.Delete("alice", "c1"); err == nil {
		t.Fatal("Delete succeeded although the revoke failed")
	}
	if _, err := cs.Get("alice", "c1"); err != nil {
		t.Errorf("calendar deleted despite failed revoke: %v", err)
	}
}

func TestGrantOrphaned(t *testing.T) {
	e := newACLEnv(t)
	put := func(k string) {
		if err := e.ps.Set([]byte(k), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	put("/user/o/profile")
	put("/user/g/profile")
	e.calendar(t, "o", "c")
	ref := GrantRef{Kind: ACLKindCalendar, Owner: "o", Resource: "c", Grantee: "g"}
	if orphan, err := e.acl.GrantOrphaned(ref); err != nil || orphan {
		t.Fatalf("healthy grant orphaned=%v err=%v", orphan, err)
	}
	for name, r := range map[string]GrantRef{
		"owner gone":    {Kind: ACLKindCalendar, Owner: "x", Resource: "c", Grantee: "g"},
		"calendar gone": {Kind: ACLKindCalendar, Owner: "o", Resource: "x", Grantee: "g"},
		"grantee gone":  {Kind: ACLKindCalendar, Owner: "o", Resource: "c", Grantee: "x"},
	} {
		if orphan, err := e.acl.GrantOrphaned(r); err != nil || !orphan {
			t.Errorf("%s: orphaned=%v err=%v", name, orphan, err)
		}
	}
	refs, err := e.acl.ListGrantRefs()
	if err != nil || len(refs) != 0 {
		t.Errorf("ListGrantRefs = %v %v", refs, err)
	}
}

// Replaying a set against a grant the user cannot parse must not wedge.
func TestACLSetOverCorruptGrant(t *testing.T) {
	e := newACLEnv(t)
	if err := e.ps.Set(ACLKey("calendar", "a", "c", "b"), []byte{0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	e.grant(t, "a", "c", "b", GrantLevelRead, GrantStateAccepted)
	g, err := e.acl.GetCalendarGrant("a", "c", "b")
	if err != nil || g.Level != GrantLevelRead {
		t.Fatalf("grant = %v %v", g, err)
	}
}
