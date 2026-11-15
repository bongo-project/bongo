package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// recordingApplier counts the synthetic ACL commands that pass through it and
// can fail them, while delegating everything to an applier that dispatches them
// as the FSM does.
type recordingApplier struct {
	inner   RaftApplier
	failACL error
	// beforeDelete, when set, runs once just before a mailbox delete command
	// is applied: the seam for a race between DeleteMailbox's read and its
	// delete.
	beforeDelete func()

	mu     sync.Mutex
	aclCmd int
}

func (a *recordingApplier) ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error {
	if hook := a.beforeDelete; hook != nil && strings.HasPrefix(string(cmd.Key), mailboxDeletePrefix) {
		a.beforeDelete = nil
		hook()
	}
	if IsACLSyntheticCommand(cmd.Key) {
		a.mu.Lock()
		a.aclCmd++
		a.mu.Unlock()
		if a.failACL != nil {
			return a.failACL
		}
	}
	return a.inner.ApplyCommand(cmd, timeout)
}

func (a *recordingApplier) aclCommands() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.aclCmd
}

// revokeEnv is a mailbox store whose applier is the recorder, over a real
// Pebble and an ACL store that writes through the same dispatching applier.
type revokeEnv struct {
	*mailboxIDEnv
	rec *recordingApplier
	acl *ACLStore
}

func newRevokeEnv(t *testing.T) *revokeEnv {
	t.Helper()
	e := newMailboxIDEnv(t)
	direct := &directApplier{store: e.ps}
	rec := &recordingApplier{inner: &serialApplier{inner: direct}}
	e.ms = NewMailboxStore(e.ps, rec)
	direct.mailboxes = e.ms
	return &revokeEnv{mailboxIDEnv: e, rec: rec, acl: NewACLStore(e.ps, &serialApplier{inner: direct})}
}

func (e *revokeEnv) shareMailbox(t *testing.T, owner, id, grantee string) {
	t.Helper()
	err := e.acl.apply(ACLRequest{
		Op: ACLOpSet, Kind: ACLKindMailbox, Owner: owner, Resource: id,
		Grantee: GranteeUser(grantee), Rights: "lrs", State: GrantStateAccepted, GrantedBy: owner, At: 100,
	})
	if err != nil {
		t.Fatalf("share: %v", err)
	}
}

func TestRevokeUserGrantsEveryKind(t *testing.T) {
	e := newRevokeEnv(t)
	users := []string{"u", "uu", "other"}
	ids := map[string]string{}
	for _, u := range users {
		e.create(t, u, "Box")
		id, err := e.ms.EnsureMailboxID(u, "Box")
		if err != nil {
			t.Fatal(err)
		}
		ids[u] = id
	}
	set := func(kind, owner, res, grantee string) {
		t.Helper()
		req := ACLRequest{Op: ACLOpSet, Kind: kind, Owner: owner, Resource: res, Grantee: GranteeUser(grantee), State: GrantStateAccepted, GrantedBy: owner, At: 100}
		if kind == ACLKindMailbox {
			req.Rights = "lrs"
		} else {
			req.Level = GrantLevelRead
		}
		if err := e.acl.apply(req); err != nil {
			t.Fatalf("set: %v", err)
		}
	}
	for _, kind := range []string{ACLKindCalendar, ACLKindMailbox, ACLKindAddressBook} {
		res := func(owner string) string {
			switch kind {
			case ACLKindMailbox:
				return ids[owner]
			case ACLKindAddressBook:
				return kindsBookID
			}
			return "cal"
		}
		// u as owner, u as grantee, and bystanders that share the prefix.
		set(kind, "u", res("u"), "other")
		set(kind, "other", res("other"), "u")
		set(kind, "uu", res("uu"), "other")
		set(kind, "other", res("other"), "uu")
	}

	if err := RevokeUserGrants(e.rec, "u"); err != nil {
		t.Fatal(err)
	}
	keys := aclKeys(t, e.ps)
	if len(keys) != 3*2*2 {
		t.Fatalf("%d acl keys left, want %d: %v", len(keys), 3*2*2, keys)
	}
	for _, k := range keys {
		ref, ok := parseACLKey(k)
		if !ok {
			t.Fatalf("unparseable key %q", k)
		}
		if ref.Owner == "u" || ref.Principal == GranteeUser("u") {
			t.Errorf("key %q still names u", k)
		}
	}
}

func TestDeleteMailboxRevokesGrants(t *testing.T) {
	e := newRevokeEnv(t)
	e.create(t, "o", "Work", "Keep")
	id, err := e.ms.EnsureMailboxID("o", "Work")
	if err != nil {
		t.Fatal(err)
	}
	keep, err := e.ms.EnsureMailboxID("o", "Keep")
	if err != nil {
		t.Fatal(err)
	}
	e.shareMailbox(t, "o", id, "b")
	e.shareMailbox(t, "o", id, "c")
	e.shareMailbox(t, "o", keep, "b")

	if err := e.ms.DeleteMailbox("o", "Work"); err != nil {
		t.Fatal(err)
	}
	if got := aclKeys(t, e.ps); len(got) != 2 {
		t.Fatalf("keys after delete = %v, want only the other mailbox's grant and its reverse key", got)
	}
	if g, _ := e.acl.GetGrant(ACLKindMailbox, "o", keep, GranteeUser("b")); g == nil {
		t.Error("another mailbox's grant was revoked")
	}
}

func TestDeleteMailboxWithoutIDIssuesNoACLCommand(t *testing.T) {
	e := newRevokeEnv(t)
	e.create(t, "o", "Plain")
	if err := e.ms.DeleteMailbox("o", "Plain"); err != nil {
		t.Fatal(err)
	}
	if n := e.rec.aclCommands(); n != 0 {
		t.Errorf("%d acl commands for a never-shared mailbox", n)
	}
	// Already gone: still nothing.
	if err := e.ms.DeleteMailbox("o", "Plain"); err != nil || e.rec.aclCommands() != 0 {
		t.Errorf("repeat delete: err %v, acl commands %d", err, e.rec.aclCommands())
	}
}

func TestDeleteMailboxRevokeFailure(t *testing.T) {
	e := newRevokeEnv(t)
	for _, u := range []string{"o", "b"} {
		if err := e.ps.Set([]byte("/user/"+u+"/profile"), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	e.create(t, "o", "Work")
	id, err := e.ms.EnsureMailboxID("o", "Work")
	if err != nil {
		t.Fatal(err)
	}
	e.shareMailbox(t, "o", id, "b")

	boom := errors.New("raft unavailable")
	e.rec.failACL = boom
	err = e.ms.DeleteMailbox("o", "Work")
	if !errors.Is(err, ErrMailboxGrantsNotRevoked) || !errors.Is(err, boom) {
		t.Fatalf("err = %v, want ErrMailboxGrantsNotRevoked wrapping the cause", err)
	}
	if _, err := e.ms.GetMailbox("o", "Work"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mailbox still there: %v", err)
	}
	ref := GrantRef{Kind: ACLKindMailbox, Owner: "o", Resource: id, Principal: GranteeUser("b")}
	if got, err := e.acl.GrantOrphaned(ref); err != nil || !got {
		t.Errorf("leftover grant orphaned = %v, %v; the sweep could not remove it", got, err)
	}
}

// A rename between DeleteMailbox's read of the id and its delete moves the
// mailbox away, so the delete removes nothing. The renamed mailbox keeps its
// grants and the caller hears that no such mailbox existed.
func TestDeleteMailboxRenamedMidwayKeepsGrants(t *testing.T) {
	e := newRevokeEnv(t)
	e.create(t, "o", "Work")
	id, err := e.ms.EnsureMailboxID("o", "Work")
	if err != nil {
		t.Fatal(err)
	}
	e.shareMailbox(t, "o", id, "b")
	e.rec.beforeDelete = func() {
		if err := e.ms.RenameMailbox("o", "Work", "Archive"); err != nil {
			t.Errorf("rename: %v", err)
		}
	}

	err = e.ms.DeleteMailbox("o", "Work")
	if !errors.Is(err, ErrNotFound) || errors.Is(err, ErrMailboxGrantsNotRevoked) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if name, err := e.ms.MailboxNameByID("o", id); err != nil || name != "Archive" {
		t.Fatalf("MailboxNameByID = %q, %v; the renamed mailbox is gone", name, err)
	}
	if g, err := e.acl.GetGrant(ACLKindMailbox, "o", id, GranteeUser("b")); err != nil || g == nil {
		t.Fatalf("the renamed mailbox lost its grant: %v, %v", g, err)
	}
	if n := e.rec.aclCommands(); n != 0 {
		t.Errorf("%d acl commands, want none", n)
	}
}

func TestRenameMailboxKeepsGrants(t *testing.T) {
	e := newRevokeEnv(t)
	for _, u := range []string{"o", "b"} {
		if err := e.ps.Set([]byte("/user/"+u+"/profile"), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	e.create(t, "o", "Work")
	id, err := e.ms.EnsureMailboxID("o", "Work")
	if err != nil {
		t.Fatal(err)
	}
	e.shareMailbox(t, "o", id, "b")
	if err := e.ms.RenameMailbox("o", "Work", "Archive/Work"); err != nil {
		t.Fatal(err)
	}
	if g, err := e.acl.GetGrant(ACLKindMailbox, "o", id, GranteeUser("b")); err != nil || g == nil {
		t.Fatalf("grant lost on rename: %v, %v", g, err)
	}
	ref := GrantRef{Kind: ACLKindMailbox, Owner: "o", Resource: id, Principal: GranteeUser("b")}
	if got, err := e.acl.GrantOrphaned(ref); err != nil || got {
		t.Errorf("renamed mailbox grant orphaned = %v, %v", got, err)
	}
	if name, err := e.ms.MailboxNameByID("o", id); err != nil || name != "Archive/Work" {
		t.Errorf("MailboxNameByID = %q, %v", name, err)
	}
	if e.rec.aclCommands() != 0 {
		t.Error("rename issued an acl command")
	}
}
