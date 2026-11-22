package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	kindsMailboxID = "0123456789abcdef0123456789abcdef"
	kindsBookID    = "fedcba9876543210fedcba9876543210"
)

// newKindsACL returns an ACL store whose applier serialises applies, as the
// FSM does, and dispatches grant commands (directApplier does).
func newKindsACL(t *testing.T) (*PebbleStore, *ACLStore) {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps, NewACLStore(ps, &serialApplier{inner: &directApplier{store: ps}})
}

func TestBuildACLCommandPerKind(t *testing.T) {
	set := func(kind, res, level, rights string) ACLRequest {
		return ACLRequest{Op: ACLOpSet, Kind: kind, Owner: "o", Resource: res, Grantee: GranteeUser("b"),
			Level: level, Rights: rights, State: GrantStatePending, At: 1}
	}
	tests := []struct {
		name string
		req  ACLRequest
		ok   bool
	}{
		{"calendar read", set(ACLKindCalendar, "default", GrantLevelRead, ""), true},
		{"calendar freebusy", set(ACLKindCalendar, "c", GrantLevelFreeBusy, ""), true},
		{"calendar with rights", set(ACLKindCalendar, "c", GrantLevelRead, "lr"), false},
		{"calendar bad level", set(ACLKindCalendar, "c", "admin", ""), false},
		{"addressbook read", set(ACLKindAddressBook, kindsBookID, GrantLevelRead, ""), true},
		{"addressbook write", set(ACLKindAddressBook, kindsBookID, GrantLevelWrite, ""), true},
		{"addressbook freebusy", set(ACLKindAddressBook, kindsBookID, GrantLevelFreeBusy, ""), false},
		{"addressbook with rights", set(ACLKindAddressBook, kindsBookID, GrantLevelRead, "lr"), false},
		{"addressbook bad id", set(ACLKindAddressBook, "Book", GrantLevelRead, ""), false},
		{"mailbox rights", set(ACLKindMailbox, kindsMailboxID, "", "lrswipkxtea"), true},
		{"mailbox subset", set(ACLKindMailbox, kindsMailboxID, "", "lr"), true},
		{"mailbox empty rights", set(ACLKindMailbox, kindsMailboxID, "", ""), false},
		{"mailbox duplicate letter", set(ACLKindMailbox, kindsMailboxID, "", "lrl"), false},
		{"mailbox other letter", set(ACLKindMailbox, kindsMailboxID, "", "lrz"), false},
		{"mailbox upper case", set(ACLKindMailbox, kindsMailboxID, "", "LR"), false},
		{"mailbox with level", set(ACLKindMailbox, kindsMailboxID, GrantLevelRead, "lr"), false},
		{"mailbox bad id", set(ACLKindMailbox, "INBOX", "", "lr"), false},
		{"unknown kind", set("task", "c", GrantLevelRead, ""), false},
		{"group grantee accepted", ACLRequest{Op: ACLOpSet, Kind: ACLKindCalendar, Owner: "o", Resource: "c",
			Grantee: GranteeGroup("g"), Level: GrantLevelRead, State: GrantStatePending}, true},
		{"untyped grantee", ACLRequest{Op: ACLOpRevoke, Kind: ACLKindCalendar, Owner: "o", Resource: "c", Grantee: "bob"}, false},
		{"mailbox revoke bad id", ACLRequest{Op: ACLOpRevoke, Kind: ACLKindMailbox, Owner: "o", Resource: "x", Grantee: GranteeUser("b")}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildACLCommand(tt.req)
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

// A group grantee is accepted on every op when its id has the group shape, and
// refused when it does not, through the builder and through hand-built
// payloads (the FSM backstop).
func TestACLGroupGranteeShape(t *testing.T) {
	for _, op := range []string{ACLOpSet, ACLOpAccept, ACLOpRevoke, ACLOpRevokeGrantee} {
		for _, tt := range []struct {
			grantee string
			ok      bool
		}{
			{GranteeGroup("g"), true},
			{GranteeGroup("Bad/Id"), false},
			{GranteeGroup(""), false},
			{GranteeGroup("UPPER"), false},
		} {
			t.Run(op+"/"+tt.grantee, func(t *testing.T) {
				_, err := BuildACLCommand(ACLRequest{Op: op, Kind: ACLKindCalendar, Owner: "o", Resource: "c",
					Grantee: tt.grantee, Level: GrantLevelRead, State: GrantStatePending})
				if (err == nil) != tt.ok {
					t.Errorf("builder err = %v, want ok=%v", err, tt.ok)
				}
				raw, _ := json.Marshal(aclPayload{Op: op, Kind: ACLKindCalendar, Owner: "o", Resource: "c",
					Grantee: tt.grantee, Level: GrantLevelRead, State: GrantStatePending})
				cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(aclPrefix + op), Value: raw}
				if _, _, err := PlanACL(noGet, noScan, cmd); (err == nil) != tt.ok {
					t.Errorf("PlanACL err = %v, want ok=%v", err, tt.ok)
				}
			})
		}
	}
}

func noGet([]byte) ([]byte, error)    { return nil, ErrNotFound }
func noScan([]byte) ([][]byte, error) { return nil, nil }

func TestPlanACLRefusesBadPayloads(t *testing.T) {
	tests := []struct {
		name string
		p    aclPayload
	}{
		{"unknown kind", aclPayload{Op: ACLOpRevokeOwner, Kind: "task", Owner: "o"}},
		{"mailbox bad letters", aclPayload{Op: ACLOpSet, Kind: ACLKindMailbox, Owner: "o", Resource: kindsMailboxID, Grantee: "b", Rights: "lz", State: GrantStateAccepted}},
		{"mailbox duplicates", aclPayload{Op: ACLOpSet, Kind: ACLKindMailbox, Owner: "o", Resource: kindsMailboxID, Grantee: "b", Rights: "ll", State: GrantStateAccepted}},
		{"mailbox empty rights", aclPayload{Op: ACLOpSet, Kind: ACLKindMailbox, Owner: "o", Resource: kindsMailboxID, Grantee: "b", State: GrantStateAccepted}},
		{"mailbox level", aclPayload{Op: ACLOpSet, Kind: ACLKindMailbox, Owner: "o", Resource: kindsMailboxID, Grantee: "b", Level: "read", Rights: "l", State: GrantStateAccepted}},
		{"mailbox bad resource", aclPayload{Op: ACLOpSet, Kind: ACLKindMailbox, Owner: "o", Resource: "inbox", Grantee: "b", Rights: "l", State: GrantStateAccepted}},
		{"calendar rights", aclPayload{Op: ACLOpSet, Kind: ACLKindCalendar, Owner: "o", Resource: "c", Grantee: "b", Level: "read", Rights: "l", State: GrantStateAccepted}},
		{"addressbook freebusy", aclPayload{Op: ACLOpSet, Kind: ACLKindAddressBook, Owner: "o", Resource: kindsBookID, Grantee: "b", Level: "freebusy", State: GrantStateAccepted}},
		{"addressbook rights", aclPayload{Op: ACLOpSet, Kind: ACLKindAddressBook, Owner: "o", Resource: kindsBookID, Grantee: "b", Level: "read", Rights: "l", State: GrantStateAccepted}},
		{"addressbook bad resource", aclPayload{Op: ACLOpRevokeResource, Kind: ACLKindAddressBook, Owner: "o", Resource: "short"}},
		{"accept bad mailbox id", aclPayload{Op: ACLOpAccept, Kind: ACLKindMailbox, Owner: "o", Resource: "x", Grantee: "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(tt.p)
			cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(aclPrefix + tt.p.Op), Value: raw}
			if _, _, err := PlanACL(noGet, noScan, cmd); err == nil {
				t.Fatal("PlanACL accepted the payload")
			}
		})
	}
}

// A payload exactly as a v0.15 node wrote it (bare grantee, no rights field)
// still plans the same keys, so replay of the existing log is unchanged.
func TestPlanACLReplaysV015CalendarPayload(t *testing.T) {
	raw := []byte(`{"op":"set","kind":"calendar","owner":"alice","resource":"default","grantee":"bob","level":"read","state":"accepted","granted_by":"alice","at":100}`)
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: []byte(aclPrefix + "set"), Value: raw}
	sets, dels, err := PlanACL(noGet, noScan, cmd)
	if err != nil || len(dels) != 0 || len(sets) != 2 {
		t.Fatalf("PlanACL = %v %v %v", sets, dels, err)
	}
	if string(sets[0].Key) != "/acl/calendar/alice/default/u:bob" || string(sets[1].Key) != "/aclrev/u:bob/calendar/alice/default" {
		t.Errorf("keys = %q %q", sets[0].Key, sets[1].Key)
	}
}

// BuildACLCommand keeps the bare user id on the wire.
func TestBuildACLCommandWireGranteeIsBare(t *testing.T) {
	cmd, err := BuildACLCommand(ACLRequest{Op: ACLOpRevoke, Kind: ACLKindCalendar, Owner: "o", Resource: "c", Grantee: GranteeUser("bob")})
	if err != nil {
		t.Fatal(err)
	}
	var p aclPayload
	if err := json.Unmarshal(cmd.Value, &p); err != nil || p.Grantee != "bob" {
		t.Errorf("wire grantee = %q %v", p.Grantee, err)
	}
}

func TestACLKeyGranteeTypes(t *testing.T) {
	// User keys are byte-identical to the keys written before typed grantees.
	if got := string(ACLKey("calendar", "a/b", "c:d", GranteeUser("e/f"))); got != "/acl/calendar/a%2Fb/c:d/u:e%2Ff" {
		t.Errorf("forward user key = %s", got)
	}
	if got := string(ACLRevKey("calendar", "a/b", "c:d", GranteeUser("e/f"))); got != "/aclrev/u:e%2Ff/calendar/a%2Fb/c:d" {
		t.Errorf("reverse user key = %s", got)
	}
	for _, grantee := range []string{GranteeUser("bob"), GranteeUser("e/f"), GranteeGroup("staff"), GranteeGroup("a b")} {
		for _, kind := range []string{ACLKindCalendar, ACLKindMailbox, ACLKindAddressBook} {
			for name, key := range map[string][]byte{
				"forward": ACLKey(kind, "o/w", "r:1", grantee),
				"reverse": ACLRevKey(kind, "o/w", "r:1", grantee),
			} {
				ref, ok := parseACLKey(string(key))
				want := GrantRef{Kind: kind, Owner: "o/w", Resource: "r:1", Principal: grantee}
				if !ok || ref != want {
					t.Errorf("%s %s %s round trip = %+v %v", name, kind, grantee, ref, ok)
				}
			}
		}
	}
	for _, bad := range []string{"/acl/calendar/o/r/x:bob", "/acl/calendar/o/r/u:", "/acl/calendar/o/r/bob", "/aclrev/g:/calendar/o/r"} {
		if _, ok := parseACLKey(bad); ok {
			t.Errorf("parseACLKey(%q) accepted", bad)
		}
	}
	if id, ok := (GrantRef{Principal: GranteeUser("u1")}).UserID(); !ok || id != "u1" {
		t.Errorf("UserID user = %q %v", id, ok)
	}
	if _, ok := (GrantRef{Principal: GranteeGroup("g1")}).UserID(); ok {
		t.Error("UserID accepted a group")
	}
	// A prefix scan for a user never reaches a group with the same id.
	if string(aclGranteePrefix(GranteeUser("x"))) == string(aclGranteePrefix(GranteeGroup("x"))) {
		t.Error("user and group prefixes collide")
	}
}

func TestListGrantRefsSeesGroupGrant(t *testing.T) {
	ps, acl := newKindsACL(t)
	if err := ps.Set(ACLKey(ACLKindMailbox, "o", kindsMailboxID, GranteeGroup("staff")), []byte{}); err != nil {
		t.Fatal(err)
	}
	refs, err := acl.ListGrantRefs()
	if err != nil || len(refs) != 1 || refs[0].Principal != "g:staff" || refs[0].Kind != ACLKindMailbox {
		t.Fatalf("refs = %+v %v", refs, err)
	}
}

// The new kinds go through set, keep_state, accept, expiry and every revoke
// via a serialising applier.
func TestACLNewKindsLifecycle(t *testing.T) {
	kinds := []struct {
		kind, res, level, rights string
	}{
		{ACLKindMailbox, kindsMailboxID, "", "lrs"},
		{ACLKindAddressBook, kindsBookID, GrantLevelWrite, ""},
	}
	for _, k := range kinds {
		t.Run(k.kind, func(t *testing.T) {
			ps, acl := newKindsACL(t)
			bob := GranteeUser("bob")
			do := func(req ACLRequest) {
				t.Helper()
				req.Kind, req.Owner, req.Resource = k.kind, "alice", k.res
				if req.Op == ACLOpSet {
					req.Level, req.Rights = k.level, k.rights
				}
				if err := acl.apply(req); err != nil {
					t.Fatalf("%s: %v", req.Op, err)
				}
			}
			get := func(grantee string) *bongov1.Grant {
				t.Helper()
				g, err := acl.GetGrant(k.kind, "alice", k.res, grantee)
				if err != nil {
					t.Fatal(err)
				}
				return g
			}

			do(ACLRequest{Op: ACLOpSet, Grantee: bob, State: GrantStatePending, GrantedBy: "alice", At: 100})
			g := get(bob)
			if g == nil || g.Rights != k.rights || g.Level != k.level || g.State != GrantStatePending || g.CreatedAt != 100 {
				t.Fatalf("stored grant = %v", g)
			}
			// keep_state leaves a pending grant pending and an accepted one accepted.
			do(ACLRequest{Op: ACLOpAccept, Grantee: bob, At: 200})
			do(ACLRequest{Op: ACLOpSet, Grantee: bob, State: GrantStatePending, GrantedBy: "alice", At: 300, KeepState: true})
			if g := get(bob); g.State != GrantStateAccepted || g.UpdatedAt != 300 || g.CreatedAt != 100 {
				t.Errorf("after keep_state = %v", g)
			}

			// An expired pending grant is not accepted.
			carl := GranteeUser("carl")
			do(ACLRequest{Op: ACLOpSet, Grantee: carl, State: GrantStatePending, GrantedBy: "alice", At: 1000})
			do(ACLRequest{Op: ACLOpAccept, Grantee: carl, At: 1000 + PendingGrantTTL + 1})
			if g := get(carl); g.State != GrantStatePending {
				t.Errorf("expired grant accepted: %v", g)
			}
			do(ACLRequest{Op: ACLOpAccept, Grantee: carl, At: 1000 + PendingGrantTTL})
			if g := get(carl); g.State != GrantStateAccepted {
				t.Errorf("grant at the boundary not accepted: %v", g)
			}

			// Both key families, and they agree.
			want := []string{
				string(ACLKey(k.kind, "alice", k.res, bob)), string(ACLKey(k.kind, "alice", k.res, carl)),
				string(ACLRevKey(k.kind, "alice", k.res, bob)), string(ACLRevKey(k.kind, "alice", k.res, carl)),
			}
			slices.Sort(want)
			if got := aclKeys(t, ps); !slices.Equal(got, want) {
				t.Fatalf("keys = %v, want %v", got, want)
			}

			do(ACLRequest{Op: ACLOpRevoke, Grantee: carl})
			if get(carl) != nil || len(aclKeys(t, ps)) != 2 {
				t.Errorf("revoke left %v", aclKeys(t, ps))
			}
			do(ACLRequest{Op: ACLOpRevokeResource})
			if len(aclKeys(t, ps)) != 0 {
				t.Errorf("revoke_resource left %v", aclKeys(t, ps))
			}

			do(ACLRequest{Op: ACLOpSet, Grantee: bob, State: GrantStateAccepted, At: 400})
			do(ACLRequest{Op: ACLOpRevokeOwner})
			if len(aclKeys(t, ps)) != 0 {
				t.Errorf("revoke_owner left %v", aclKeys(t, ps))
			}

			do(ACLRequest{Op: ACLOpSet, Grantee: bob, State: GrantStateAccepted, At: 400})
			do(ACLRequest{Op: ACLOpRevokeGrantee, Owner: "ignored", Grantee: bob})
			if len(aclKeys(t, ps)) != 0 {
				t.Errorf("revoke_grantee left %v", aclKeys(t, ps))
			}
		})
	}
}

func TestGrantCapReachedPerKind(t *testing.T) {
	now := time.Unix(10_000_000, 0)
	res := map[string]string{ACLKindCalendar: "work", ACLKindMailbox: kindsMailboxID, ACLKindAddressBook: kindsBookID}
	for _, kind := range []string{ACLKindCalendar, ACLKindMailbox, ACLKindAddressBook} {
		t.Run(kind, func(t *testing.T) {
			ps, acl := newKindsACL(t)
			level, rights := GrantLevelRead, ""
			if kind == ACLKindMailbox {
				level, rights = "", "lr"
			}
			put := func(user, state string, at int64) {
				t.Helper()
				if err := acl.apply(ACLRequest{Op: ACLOpSet, Kind: kind, Owner: "o", Resource: res[kind], Grantee: GranteeUser(user),
					Level: level, Rights: rights, State: state, At: at}); err != nil {
					t.Fatal(err)
				}
			}
			for i := range MaxGrantsPerResource - 1 {
				put(fmt.Sprintf("u%02d", i), GrantStateAccepted, now.Unix())
			}
			full, err := acl.GrantCapReached(kind, "o", res[kind], now)
			if err != nil || full {
				t.Fatalf("49 grants: full=%v err=%v", full, err)
			}
			// An expired pending grant is outside the cap.
			put("old", GrantStatePending, now.Unix()-PendingGrantTTL-10)
			if full, _ := acl.GrantCapReached(kind, "o", res[kind], now); full {
				t.Error("expired pending grant counted")
			}
			put("last", GrantStatePending, now.Unix())
			if full, _ := acl.GrantCapReached(kind, "o", res[kind], now); !full {
				t.Error("50 live grants not full")
			}
			// The count agrees with the cap check.
			n, err := acl.CountActiveGrants(kind, "o", res[kind], now)
			if err != nil || n != MaxGrantsPerResource {
				t.Errorf("count = %d %v", n, err)
			}
			// Another resource of the same kind is separate.
			if full, _ := acl.GrantCapReached(kind, "o", "other", now); full {
				t.Error("cap leaked across resources")
			}
			_ = ps
		})
	}
}

func TestGrantOrphanedPerKind(t *testing.T) {
	e := newMailboxIDEnv(t)
	acl := NewACLStore(e.ps, &serialApplier{inner: &directApplier{store: e.ps}})
	put := func(k string) {
		t.Helper()
		if err := e.ps.Set([]byte(k), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	put("/user/o/profile")
	put("/user/b/profile")
	put("/system/group/staff")
	e.create(t, "o", "Work")
	id, err := e.ms.EnsureMailboxID("o", "Work")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Set(calendarKey("o", "cal"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Set(addressBookKey("o", kindsBookID), []byte("x")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		ref    GrantRef
		orphan bool
	}{
		{"mailbox healthy", GrantRef{Kind: ACLKindMailbox, Owner: "o", Resource: id, Principal: GranteeUser("b")}, false},
		{"mailbox gone", GrantRef{Kind: ACLKindMailbox, Owner: "o", Resource: kindsMailboxID, Principal: GranteeUser("b")}, true},
		{"mailbox owner gone", GrantRef{Kind: ACLKindMailbox, Owner: "x", Resource: id, Principal: GranteeUser("b")}, true},
		{"mailbox grantee gone", GrantRef{Kind: ACLKindMailbox, Owner: "o", Resource: id, Principal: GranteeUser("x")}, true},
		{"addressbook healthy", GrantRef{Kind: ACLKindAddressBook, Owner: "o", Resource: kindsBookID, Principal: GranteeUser("b")}, false},
		{"addressbook owner gone", GrantRef{Kind: ACLKindAddressBook, Owner: "x", Resource: kindsBookID, Principal: GranteeUser("b")}, true},
		{"addressbook grantee gone", GrantRef{Kind: ACLKindAddressBook, Owner: "o", Resource: kindsBookID, Principal: GranteeUser("x")}, true},
		{"calendar healthy", GrantRef{Kind: ACLKindCalendar, Owner: "o", Resource: "cal", Principal: GranteeUser("b")}, false},
		{"calendar gone", GrantRef{Kind: ACLKindCalendar, Owner: "o", Resource: "nope", Principal: GranteeUser("b")}, true},
		{"group grant, group present", GrantRef{Kind: ACLKindMailbox, Owner: "o", Resource: id, Principal: GranteeGroup("staff")}, false},
		{"group grant, group gone", GrantRef{Kind: ACLKindMailbox, Owner: "o", Resource: id, Principal: GranteeGroup("vanished")}, true},
		{"group grant, owner gone", GrantRef{Kind: ACLKindAddressBook, Owner: "x", Resource: kindsBookID, Principal: GranteeGroup("staff")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := acl.GrantOrphaned(tt.ref)
			if err != nil || got != tt.orphan {
				t.Errorf("orphaned = %v, %v; want %v", got, err, tt.orphan)
			}
		})
	}

	// A mailbox deleted after the grant is orphaned; a stale id key alone
	// (the mailbox now carries another id) is too.
	if err := e.ps.Delete(mailboxMetaKey("o", "Work")); err != nil {
		t.Fatal(err)
	}
	ref := GrantRef{Kind: ACLKindMailbox, Owner: "o", Resource: id, Principal: GranteeUser("b")}
	if got, err := acl.GrantOrphaned(ref); err != nil || !got {
		t.Errorf("deleted mailbox: orphaned = %v, %v", got, err)
	}
	meta := &bongov1.MailboxMeta{Name: "Work", Id: kindsBookID}
	data, _ := proto.Marshal(meta)
	if err := e.ps.Set(mailboxMetaKey("o", "Work"), data); err != nil {
		t.Fatal(err)
	}
	if got, err := acl.GrantOrphaned(ref); err != nil || !got {
		t.Errorf("re-id'd mailbox: orphaned = %v, %v", got, err)
	}
}
