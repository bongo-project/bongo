package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type sharedInboxEnv struct {
	ps  *PebbleStore
	mbx *MailboxStore
	acl *ACLStore
	gs  *GroupStore
}

func newSharedInboxEnv(t *testing.T) *sharedInboxEnv {
	t.Helper()
	ps, direct, gs := newGroupTestEnv(t)
	app := &serialApplier{inner: direct}
	mbx := NewMailboxStore(ps, app)
	direct.mailboxes = mbx
	e := &sharedInboxEnv{ps: ps, mbx: mbx, acl: NewACLStore(ps, app), gs: gs}
	seedGroupUser(t, ps, "alice", "")
	seedGroupUser(t, ps, "bob", "")
	seedSharedAccount(t, ps, "support", nil)
	if err := mbx.EnsureDefaultMailboxes("support"); err != nil {
		t.Fatal(err)
	}
	return e
}

func seedSharedAccount(t *testing.T, ps *PebbleStore, id string, members []string) {
	t.Helper()
	data, err := proto.Marshal(&bongov1.UserProfile{Email: id + "@example.com", Kind: SharedInboxKind, SharedMembers: members})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
		t.Fatal(err)
	}
}

func (e *sharedInboxEnv) folderID(t *testing.T, name string) string {
	t.Helper()
	id, err := e.mbx.EnsureMailboxID("support", name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *sharedInboxEnv) rights(t *testing.T, user, folder string) string {
	t.Helper()
	r, err := e.acl.MailboxRights("support", e.folderID(t, folder), user, time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSyncSharedInboxGrantsAccepted(t *testing.T) {
	e := newSharedInboxEnv(t)
	g, err := e.gs.CreateGroup(&bongov1.Group{Address: "team@example.com", MemberIds: []string{"bob"}})
	if err != nil {
		t.Fatal(err)
	}
	members := []string{GranteeUser("alice"), GranteeGroup(g.Id)}
	if err := SyncSharedInboxGrants(e.acl, e.mbx, "support", members, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	for _, f := range DefaultMailboxes {
		for _, u := range []string{"alice", "bob"} {
			if got := e.rights(t, u, f); got != MailboxRightsWrite {
				t.Errorf("%s on %s: rights %q, want %q", u, f, got, MailboxRightsWrite)
			}
		}
		grants, err := e.acl.ListResourceGrants(ACLKindMailbox, "support", e.folderID(t, f))
		if err != nil {
			t.Fatal(err)
		}
		if len(grants) != 2 {
			t.Fatalf("%s: %d grants, want 2", f, len(grants))
		}
		for _, gr := range grants {
			if gr.Grant.State != GrantStateAccepted {
				t.Errorf("%s: grant %s state %q, want accepted", f, gr.Principal, gr.Grant.State)
			}
		}
	}
	// Idempotent.
	if err := SyncSharedInboxGrants(e.acl, e.mbx, "support", members, time.Unix(300, 0)); err != nil {
		t.Fatal(err)
	}
	// The shared account is not a person: the member sees the folders through
	// the held-share listing the sidebar and IMAP LIST use.
	held, err := e.acl.MailboxesSharedWith("alice", time.Unix(200, 0))
	if err != nil || len(held) != len(DefaultMailboxes) {
		t.Fatalf("held = %d, err %v, want %d", len(held), err, len(DefaultMailboxes))
	}
}

func TestSyncRevokesNonMembers(t *testing.T) {
	e := newSharedInboxEnv(t)
	both := []string{GranteeUser("alice"), GranteeUser("bob")}
	if err := SyncSharedInboxGrants(e.acl, e.mbx, "support", both, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	// A grant made some other way for someone not listed goes too.
	seedGroupUser(t, e.ps, "carol", "")
	if err := e.acl.SetGrant(ACLKindMailbox, "support", e.folderID(t, "INBOX"), GranteeUser("carol"), "", "lrs", GrantStateAccepted, "support", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := SyncSharedInboxGrants(e.acl, e.mbx, "support", []string{GranteeUser("alice")}, time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	for _, f := range DefaultMailboxes {
		if got := e.rights(t, "bob", f); got != "" {
			t.Errorf("removed member on %s: rights %q", f, got)
		}
		if got := e.rights(t, "carol", f); got != "" {
			t.Errorf("unlisted grantee on %s: rights %q", f, got)
		}
		if got := e.rights(t, "alice", f); got != MailboxRightsWrite {
			t.Errorf("kept member on %s: rights %q", f, got)
		}
	}
}

func TestSyncReturnsRevokeFailure(t *testing.T) {
	e := newSharedInboxEnv(t)
	if err := SyncSharedInboxGrants(e.acl, e.mbx, "support", []string{GranteeUser("alice")}, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	failing := &recordingApplier{inner: &serialApplier{inner: &directApplier{store: e.ps, mailboxes: e.mbx}}, failACL: errors.New("raft down")}
	acl := NewACLStore(e.ps, failing)
	if err := SyncSharedInboxGrants(acl, e.mbx, "support", nil, time.Unix(101, 0)); err == nil {
		t.Fatal("a failed revoke was not reported")
	}
}

func TestSyncSkipsVanished(t *testing.T) {
	e := newSharedInboxEnv(t)
	gid := strings.Repeat("d", 32)
	members := []string{GranteeUser("ghost"), GranteeGroup(gid), GranteeUser("alice")}
	if err := SyncSharedInboxGrants(e.acl, e.mbx, "support", members, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	for _, f := range DefaultMailboxes {
		grants, err := e.acl.ListResourceGrants(ACLKindMailbox, "support", e.folderID(t, f))
		if err != nil {
			t.Fatal(err)
		}
		if len(grants) != 1 || grants[0].Principal != GranteeUser("alice") {
			t.Fatalf("%s: grants %+v, want alice only", f, grants)
		}
	}
}

func TestSyncCap(t *testing.T) {
	e := newSharedInboxEnv(t)
	var members []string
	for i := range MaxSharedMembers + 1 {
		id := "m" + strings.Repeat("x", i)
		seedGroupUser(t, e.ps, id, "")
		members = append(members, GranteeUser(id))
	}
	err := SyncSharedInboxGrants(e.acl, e.mbx, "support", members, time.Unix(100, 0))
	if !errors.Is(err, ErrTooManySharedMembers) {
		t.Fatalf("err = %v, want ErrTooManySharedMembers", err)
	}
	refs, err := e.acl.ListGrantRefs()
	if err != nil || len(refs) != 0 {
		t.Fatalf("grants written before the refusal: %d, err %v", len(refs), err)
	}
	if err := SyncSharedInboxGrants(e.acl, e.mbx, "support", members[:MaxSharedMembers], time.Unix(100, 0)); err != nil {
		t.Fatalf("exactly the cap: %v", err)
	}
}

func TestGrantOrphanedSharedNonMember(t *testing.T) {
	e := newSharedInboxEnv(t)
	seedSharedAccount(t, e.ps, "support", []string{GranteeUser("alice")})
	id := e.folderID(t, "INBOX")
	for _, u := range []string{"alice", "bob"} {
		if err := e.acl.SetGrant(ACLKindMailbox, "support", id, GranteeUser(u), "", "lrs", GrantStateAccepted, "support", time.Unix(100, 0)); err != nil {
			t.Fatal(err)
		}
	}
	// A person's folder with the same grants is never orphaned for this reason.
	if err := e.mbx.EnsureDefaultMailboxes("alice"); err != nil {
		t.Fatal(err)
	}
	pid, err := e.mbx.EnsureMailboxID("alice", "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.acl.SetGrant(ACLKindMailbox, "alice", pid, GranteeUser("bob"), "", "lrs", GrantStateAccepted, "alice", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ref  GrantRef
		want bool
	}{
		{"member", GrantRef{ACLKindMailbox, "support", id, GranteeUser("alice")}, false},
		{"non-member", GrantRef{ACLKindMailbox, "support", id, GranteeUser("bob")}, true},
		{"person owner", GrantRef{ACLKindMailbox, "alice", pid, GranteeUser("bob")}, false},
	}
	for _, tc := range cases {
		got, err := e.acl.GrantOrphaned(tc.ref)
		if err != nil || got != tc.want {
			t.Errorf("%s: orphaned=%v err=%v, want %v", tc.name, got, err, tc.want)
		}
	}
}

// A shared account must resolve as a folder owner (IMAP Other Users, web
// shared tree); a resource must not.
func TestOwnerByPrimaryAddressResolvesShared(t *testing.T) {
	e := newSharedInboxEnv(t)
	seedGroupUser(t, e.ps, "room", "resource")
	for id := range map[string]bool{"support": true, "room": true, "alice": true} {
		rec, _ := proto.Marshal(&bongov1.AddressRecord{UserId: id, Kind: "primary", CanSend: true, Verified: true})
		if err := e.ps.Set(addressKey(id+"@example.com"), rec); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := e.acl.OwnerByPrimaryAddress("support@example.com")
	if err != nil || got != "support" {
		t.Fatalf("shared: id=%q err=%v", got, err)
	}
	if _, _, err := e.acl.OwnerByPrimaryAddress("room@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resource: err=%v, want ErrNotFound", err)
	}
}

// A shared account cannot belong to a group.
func TestGroupRefusesSharedMember(t *testing.T) {
	e := newSharedInboxEnv(t)
	_, err := e.gs.CreateGroup(&bongov1.Group{Address: "team@example.com", MemberIds: []string{"support"}})
	if !errors.Is(err, ErrGroupInvalid) {
		t.Fatalf("err = %v, want ErrGroupInvalid", err)
	}
}
