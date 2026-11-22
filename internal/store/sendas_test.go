package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func seedSendAsRecords(t *testing.T, as *AddressStore) {
	t.Helper()
	for _, r := range testutil.SendAsRecords {
		rec := &bongov1.AddressRecord{UserId: r.UserID, Kind: r.Kind, CanSend: r.CanSend, Verified: r.Verified}
		if err := as.Put(r.Addr, rec); err != nil {
			t.Fatalf("Put %s: %v", r.Addr, err)
		}
	}
}

func seedSendAsProfiles(t *testing.T, as *AddressStore) {
	t.Helper()
	for _, sp := range testutil.SendAsProfiles {
		putSharedProfile(t, as, sp.UserID, sp.Email, sp.Kind, sp.Members, false)
	}
}

func putSharedProfile(t *testing.T, as *AddressStore, id, email, kind string, members []string, disabled bool) {
	t.Helper()
	data, err := proto.Marshal(&bongov1.UserProfile{Email: email, Kind: kind, SharedMembers: members, Disabled: disabled})
	if err != nil {
		t.Fatal(err)
	}
	if err := as.pebble.Set([]byte("/user/"+id+"/profile"), data); err != nil {
		t.Fatal(err)
	}
}

func TestCanSendAs(t *testing.T) {
	as := newAddressTestEnv(t)
	seedSendAsRecords(t, as)
	seedSendAsProfiles(t, as)

	for _, tc := range testutil.SendAsCases {
		if tc.Addr == "" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			got, err := CanSendAs(as, testutil.SendAsUserID, "Alice@Example.com", tc.Addr)
			if err != nil {
				t.Fatalf("CanSendAs: %v", err)
			}
			if got != tc.Want {
				t.Fatalf("CanSendAs(%q) = %v, want %v", tc.Addr, got, tc.Want)
			}
		})
	}
}

func TestCanSendAsEdges(t *testing.T) {
	as := newAddressTestEnv(t)
	seedSendAsRecords(t, as)

	if ok, _ := CanSendAs(as, "u-alice", "alice@example.com", "  "); ok {
		t.Error("blank address must be refused")
	}
	if ok, _ := CanSendAs(nil, "u-alice", "alice@example.com", "verified@gmail.com"); ok {
		t.Error("nil store must permit only the primary")
	}
	if ok, _ := CanSendAs(nil, "u-alice", "alice@example.com", "ALICE@example.com"); !ok {
		t.Error("nil store must still permit the primary")
	}
	if ok, _ := CanSendAs(as, "u-alice", "", "alice@example.com"); ok {
		t.Error("empty primary must not match anything")
	}
}

func TestSendAsPolicyProfileHandling(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	as := NewAddressStore(ps, &directApplier{store: ps}, nil)
	pol := &SendAsPolicy{Addresses: as, Pebble: ps}

	// Missing profile: refused without an error, even for a verified
	// identity that is still on file.
	seedSendAsRecords(t, as)
	if ok, err := pol.CanSendAs("u-alice", "verified@gmail.com"); ok || err != nil {
		t.Fatalf("missing profile: ok=%v err=%v, want false,nil", ok, err)
	}

	data, err := proto.Marshal(&bongov1.UserProfile{Email: "Alice@Example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.Set([]byte("/user/u-alice/profile"), data); err != nil {
		t.Fatal(err)
	}
	if ok, err := pol.CanSendAs("u-alice", "alice@example.com"); !ok || err != nil {
		t.Fatalf("primary from profile: ok=%v err=%v", ok, err)
	}
	if ok, _ := pol.CanSendAs("u-alice", "verified@gmail.com"); !ok {
		t.Fatal("verified identity should be allowed")
	}

	if err := ps.Set([]byte("/user/u-bad/profile"), []byte{0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	if _, err := pol.CanSendAs("u-bad", "x@y.example"); err == nil {
		t.Fatal("corrupt profile must return an error (fail closed with retry)")
	}
}

func TestCanSendAsStoreReadErrorFailsClosed(t *testing.T) {
	as := newAddressTestEnv(t)
	// A corrupt record makes AddressStore.Get return an error, not a miss.
	if err := as.pebble.Set(addressKey("broken@gmail.com"), []byte{0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	ok, err := CanSendAs(as, "u-alice", "alice@example.com", "broken@gmail.com")
	if ok || err == nil {
		t.Fatalf("CanSendAs on unreadable record = (%v, %v), want (false, non-nil)", ok, err)
	}
}

func TestSendAsPolicyProfileReadErrorFailsClosed(t *testing.T) {
	ps, err := NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	as := NewAddressStore(ps, &directApplier{store: ps}, nil)
	seedSendAsRecords(t, as)
	pol := &SendAsPolicy{
		Addresses: as,
		Pebble:    ps,
		get:       func([]byte) ([]byte, error) { return nil, errors.New("pebble get: io failure") },
	}
	// A verified identity is on file, so a fail-open bug would grant it.
	ok, err := pol.CanSendAs("u-alice", "verified@gmail.com")
	if ok || err == nil {
		t.Fatalf("CanSendAs on failing profile read = (%v, %v), want (false, non-nil)", ok, err)
	}
}

func TestCanSendAsSharedMember(t *testing.T) {
	const shared = "support@example.com"
	put := func(t *testing.T, as *AddressStore, kind string, members []string, disabled, canSend, verified bool) {
		t.Helper()
		putSharedProfile(t, as, "u-support", shared, kind, members, disabled)
		if err := as.Put(shared, testRec("u-support", "example.com", "primary", canSend, verified)); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name     string
		kind     string
		members  []string
		disabled bool
		canSend  bool
		verified bool
		user     string
		want     bool
	}{
		{"direct member", SharedInboxKind, []string{"u:u-alice"}, false, true, true, "u-alice", true},
		{"member through a group", SharedInboxKind, []string{"g:" + strings.Repeat("a", 32)}, false, true, true, "u-alice", true},
		{"not a member", SharedInboxKind, []string{"u:u-bob"}, false, true, true, "u-alice", false},
		{"empty member list", SharedInboxKind, nil, false, true, true, "u-alice", false},
		{"disabled shared account", SharedInboxKind, []string{"u:u-alice"}, true, true, true, "u-alice", false},
		{"record without can_send", SharedInboxKind, []string{"u:u-alice"}, false, false, true, "u-alice", false},
		{"record not verified", SharedInboxKind, []string{"u:u-alice"}, false, true, false, "u-alice", false},
		{"owner is a person, members set", "", []string{"u:u-alice"}, false, true, true, "u-alice", false},
		{"owner is a resource, members set", "resource", []string{"u:u-alice"}, false, true, true, "u-alice", false},
		{"member list names the user as a group id", SharedInboxKind, []string{"g:u-alice"}, false, true, true, "u-alice", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			as := newAddressTestEnv(t)
			put(t, as, tc.kind, tc.members, tc.disabled, tc.canSend, tc.verified)
			gid := strings.Repeat("a", 32)
			if err := as.pebble.Set(GroupMemberKey("u-alice", gid), nil); err != nil {
				t.Fatal(err)
			}
			got, err := CanSendAs(as, tc.user, "alice@example.com", shared)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("CanSendAs = %v, want %v", got, tc.want)
			}
		})
	}
}

// Folder rights alone never permit sending as the shared address.
func TestCanSendAsSharedRightsAloneNotEnough(t *testing.T) {
	as := newAddressTestEnv(t)
	putSharedProfile(t, as, "u-support", "support@example.com", SharedInboxKind, []string{"u:u-bob"}, false)
	if err := as.Put("support@example.com", testRec("u-support", "example.com", "primary", true, true)); err != nil {
		t.Fatal(err)
	}
	acl := NewACLStore(as.pebble, &serialApplier{inner: &directApplier{store: as.pebble}})
	id := strings.Repeat("b", 32)
	if err := acl.SetGrant(ACLKindMailbox, "u-support", id, GranteeUser("u-alice"), "", MailboxRightsWrite, GrantStateAccepted, "u-support", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if ok, err := CanSendAs(as, "u-alice", "alice@example.com", "support@example.com"); err != nil || ok {
		t.Fatalf("grant holder who is not a member: ok=%v err=%v, want false", ok, err)
	}
}

func TestCanSendAsSharedRemovalImmediate(t *testing.T) {
	as := newAddressTestEnv(t)
	gid := strings.Repeat("c", 32)
	if err := as.Put("support@example.com", testRec("u-support", "example.com", "primary", true, true)); err != nil {
		t.Fatal(err)
	}
	can := func() bool {
		t.Helper()
		ok, err := CanSendAs(as, "u-alice", "alice@example.com", "support@example.com")
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	putSharedProfile(t, as, "u-support", "support@example.com", SharedInboxKind, []string{"u:u-alice"}, false)
	if !can() {
		t.Fatal("member refused")
	}
	putSharedProfile(t, as, "u-support", "support@example.com", SharedInboxKind, []string{"u:u-bob"}, false)
	if can() {
		t.Fatal("removed member still allowed")
	}

	putSharedProfile(t, as, "u-support", "support@example.com", SharedInboxKind, []string{"g:" + gid}, false)
	if err := as.pebble.Set(GroupMemberKey("u-alice", gid), nil); err != nil {
		t.Fatal(err)
	}
	if !can() {
		t.Fatal("group member refused")
	}
	if err := as.pebble.Delete(GroupMemberKey("u-alice", gid)); err != nil {
		t.Fatal(err)
	}
	if can() {
		t.Fatal("user removed from the group still allowed")
	}
}

// Membership widens sending only: the shared address is never an own address.
func TestCanSendAsOwnExcludesSharedMembership(t *testing.T) {
	as := newAddressTestEnv(t)
	seedSendAsRecords(t, as)
	seedSendAsProfiles(t, as)
	for addr, want := range map[string]bool{
		"alice@example.com":   true,  // primary
		"verified@gmail.com":  true,  // own verified identity
		"support@example.com": false, // member of a shared inbox, not own
		"bob@example.com":     false,
	} {
		own, err := CanSendAsOwn(as, testutil.SendAsUserID, "alice@example.com", addr)
		if err != nil || own != want {
			t.Errorf("CanSendAsOwn(%s) = %v, %v; want %v", addr, own, err, want)
		}
	}
	if ok, err := CanSendAs(as, testutil.SendAsUserID, "alice@example.com", "support@example.com"); err != nil || !ok {
		t.Errorf("CanSendAs(support@) = %v, %v; want true", ok, err)
	}
}
