package store

import (
	"testing"
	"time"
)

const shareBookA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const shareBookB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func (e *groupACLEnv) book(t *testing.T, owner, id string) {
	t.Helper()
	if err := e.ps.Set(addressBookKey(owner, id), []byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestAddressBookLevelUnion(t *testing.T) {
	e := newGroupACLEnv(t)
	now := time.Now().Unix()
	e.book(t, "owner", shareBookA)

	level := func(book, user string) string {
		t.Helper()
		got, err := e.acl.AddressBookLevel("owner", book, user)
		if err != nil {
			t.Fatalf("AddressBookLevel: %v", err)
		}
		return got
	}

	e.set(t, ACLKindAddressBook, shareBookA, GranteeUser("m"), GrantLevelRead, "", GrantStatePending, now)
	if got := level(shareBookA, "m"); got != "" {
		t.Errorf("pending grant level = %q, want none", got)
	}
	e.set(t, ACLKindAddressBook, shareBookA, GranteeUser("m"), GrantLevelRead, "", GrantStateAccepted, now)
	if got := level(shareBookA, "m"); got != GrantLevelRead {
		t.Errorf("accepted read = %q", got)
	}
	gid := e.group(t, "team@example.com", "m")
	e.set(t, ACLKindAddressBook, shareBookA, GranteeGroup(gid), GrantLevelWrite, "", GrantStateAccepted, now)
	if got := level(shareBookA, "m"); got != GrantLevelWrite {
		t.Errorf("read plus group write = %q, want write", got)
	}
	if got := level(shareBookA, "n"); got != "" {
		t.Errorf("non-member level = %q", got)
	}
	if got := level(DefaultAddressBookID, "m"); got != "" {
		t.Errorf("default book level = %q", got)
	}
	if got := level(shareBookB, "m"); got != "" {
		t.Errorf("ungranted book level = %q", got)
	}
}

func TestListGrantsHeldDropsDeletedAddressBook(t *testing.T) {
	e := newGroupACLEnv(t)
	now := time.Now()
	e.book(t, "owner", shareBookA)
	e.book(t, "owner", shareBookB)
	e.set(t, ACLKindAddressBook, shareBookA, GranteeUser("m"), GrantLevelRead, "", GrantStatePending, now.Unix())
	e.set(t, ACLKindAddressBook, shareBookB, GranteeUser("m"), GrantLevelRead, "", GrantStateAccepted, now.Unix())
	if err := e.ps.Delete(addressBookKey("owner", shareBookA)); err != nil {
		t.Fatal(err)
	}
	if err := e.ps.Delete(addressBookKey("owner", shareBookB)); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{GrantStatePending, GrantStateAccepted} {
		held, err := e.acl.ListGrantsHeld("m", state, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(held) != 0 {
			t.Errorf("%s: grants on deleted books listed: %+v", state, held)
		}
	}
	if n, _ := e.acl.CountPendingGrantsHeld("m", now); n != 0 {
		t.Errorf("pending count = %d", n)
	}
}

func TestGrantOrphanedAddressBook(t *testing.T) {
	e := newGroupACLEnv(t)
	e.book(t, "owner", shareBookA)
	ref := GrantRef{Kind: ACLKindAddressBook, Owner: "owner", Resource: shareBookA, Principal: GranteeUser("m")}
	if got, err := e.acl.GrantOrphaned(ref); err != nil || got {
		t.Errorf("book present: orphaned = %v, %v", got, err)
	}
	if err := e.ps.Delete(addressBookKey("owner", shareBookA)); err != nil {
		t.Fatal(err)
	}
	if got, err := e.acl.GrantOrphaned(ref); err != nil || !got {
		t.Errorf("book gone: orphaned = %v, %v", got, err)
	}
}

func TestAddressBooksSharedWithSkipsOwnAndOtherKinds(t *testing.T) {
	e := newGroupACLEnv(t)
	now := time.Now()
	e.book(t, "owner", shareBookA)
	e.book(t, "m", shareBookB)
	e.calendar(t, "cal")
	e.set(t, ACLKindAddressBook, shareBookA, GranteeUser("m"), GrantLevelRead, "", GrantStateAccepted, now.Unix())
	e.set(t, ACLKindCalendar, "cal", GranteeUser("m"), GrantLevelRead, "", GrantStateAccepted, now.Unix())
	// A group grant on a book m owns: reaches m only through the group.
	gid := e.group(t, "team@example.com", "m")
	if err := e.acl.apply(ACLRequest{Op: ACLOpSet, Kind: ACLKindAddressBook, Owner: "m", Resource: shareBookB,
		Grantee: GranteeGroup(gid), Level: GrantLevelRead, State: GrantStateAccepted, GrantedBy: "m", At: now.Unix()}); err != nil {
		t.Fatal(err)
	}
	got, err := e.acl.AddressBooksSharedWith("m", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Owner != "owner" || got[0].Resource != shareBookA || got[0].Kind != ACLKindAddressBook {
		t.Fatalf("shared = %+v", got)
	}
	pending, _ := e.acl.AddressBooksSharedWith("n", now)
	if len(pending) != 0 {
		t.Errorf("n sees %+v", pending)
	}
}
