package imap

import (
	"testing"

	"github.com/emersion/go-imap/v2"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A shared inbox is an owner like any other for IMAP: its member, holding the
// Write preset on a folder, lists and selects it read-write, cannot create
// folders beneath it (no k), and loses it at the next command once the grant
// is gone.
func TestSharedInboxMemberOverTheWire(t *testing.T) {
	e := newSharedEnv(t)
	p, err := proto.Marshal(&bongov1.UserProfile{Email: shOwnerAddr, Kind: store.SharedInboxKind, SharedMembers: []string{store.GranteeUser(pollTestUser)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Set([]byte("/user/"+shOwner+"/profile"), p); err != nil {
		t.Fatal(err)
	}
	e.grant("INBOX", store.MailboxRightsWrite)

	c := dialShared(t, e.startServer())
	listed, err := c.List("", "Other Users/*", nil).Collect()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range listed {
		if l.Mailbox == shInbox {
			found = true
		}
	}
	if !found {
		t.Fatalf("LIST did not show %s: %+v", shInbox, listed)
	}
	data, err := c.Select(shInbox, nil).Wait()
	if err != nil || data.ReadOnly {
		t.Fatalf("SELECT = %+v, %v; want read-write", data, err)
	}
	if err := c.Create(shInbox+"/Child", nil).Wait(); err == nil {
		t.Fatal("CREATE beneath a shared inbox folder was allowed without k")
	}

	e.revoke("INBOX")
	_, err = c.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{UID: true}).Collect()
	if !hasCode(err, imap.ResponseCodeNonExistent) {
		t.Fatalf("FETCH after the member was removed = %v, want NONEXISTENT", err)
	}
}
