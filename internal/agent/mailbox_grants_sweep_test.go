package agent

import (
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
)

// shareTestMailbox gives grantee a pending-free accepted grant on a mailbox of
// owner through the real ACL command, never by writing /acl/ keys.
func shareTestMailbox(t *testing.T, a *Agent, owner, name, grantee string) string {
	t.Helper()
	id, err := a.Mailboxes.EnsureMailboxID(owner, name)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := store.BuildACLCommand(store.ACLRequest{
		Op: store.ACLOpSet, Kind: store.ACLKindMailbox, Owner: owner, Resource: id,
		Grantee: store.GranteeUser(grantee), Rights: "lrs", State: store.GrantStateAccepted,
		GrantedBy: owner, At: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyCommand(cmd, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	return id
}

func mailboxSweepAgent(t *testing.T) *Agent {
	t.Helper()
	a := blobGCLeaderTestAgent(t)
	for _, u := range []string{"owner", "bob"} {
		if err := a.Store.Set([]byte("/user/"+u+"/profile"), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"Live", "Doomed"} {
		if err := a.Mailboxes.CreateMailbox("owner", n); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func hasMailboxGrant(t *testing.T, a *Agent, id string) bool {
	t.Helper()
	g, err := a.ACL.GetGrant(store.ACLKindMailbox, "owner", id, store.GranteeUser("bob"))
	if err != nil {
		t.Fatal(err)
	}
	return g != nil
}

// A mailbox deleted behind the grants' back is swept on the second sighting,
// reverse key included, and a healthy mailbox grant is left alone.
func TestSweepOrphanedGrants_MailboxGone(t *testing.T) {
	a := mailboxSweepAgent(t)
	live := shareTestMailbox(t, a, "owner", "Live", "bob")
	doomed := shareTestMailbox(t, a, "owner", "Doomed", "bob")

	// Delete the meta directly so no revoke runs: this proves the sweep.
	if err := a.Store.Delete([]byte("/user/owner/mailbox/Doomed/meta")); err != nil {
		t.Fatal(err)
	}

	prev := a.sweepOrphanedGrants(map[string]bool{})
	if len(prev) != 1 {
		t.Fatalf("first pass marked %d, want 1", len(prev))
	}
	if !hasMailboxGrant(t, a, doomed) {
		t.Fatal("grant deleted on the first sighting")
	}
	a.sweepOrphanedGrants(prev)
	if hasMailboxGrant(t, a, doomed) {
		t.Error("orphaned mailbox grant survived the second pass")
	}
	if !hasMailboxGrant(t, a, live) {
		t.Error("healthy mailbox grant was swept")
	}
	refs, err := a.ACL.ListGrantRefs()
	if err != nil || len(refs) != 1 {
		t.Errorf("grants left = %v, %v; want 1", refs, err)
	}
	// The reverse key went too.
	it, err := a.Store.PrefixScan([]byte("/aclrev/"))
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	revs := 0
	for it.Next() {
		revs++
	}
	if revs != 1 {
		t.Errorf("reverse keys left = %d, want 1", revs)
	}
}

// A read error that is not ErrNotFound never counts as gone.
func TestSweepOrphanedGrants_MailboxReadErrorKeepsGrant(t *testing.T) {
	a := mailboxSweepAgent(t)
	id := shareTestMailbox(t, a, "owner", "Doomed", "bob")
	// 0xff is not a valid protobuf tag, so parsing the meta fails.
	if err := a.Store.Set([]byte("/user/owner/mailbox/Doomed/meta"), []byte{0xff}); err != nil {
		t.Fatal(err)
	}
	prev := map[string]bool{}
	for i := range 4 {
		prev = a.sweepOrphanedGrants(prev)
		if len(prev) != 0 {
			t.Fatalf("pass %d marked the grant: %v", i, prev)
		}
		if !hasMailboxGrant(t, a, id) {
			t.Fatalf("pass %d deleted the grant on a transient error", i)
		}
	}
}
