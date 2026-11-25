package handlers

import (
	"fmt"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sharedGAL is the contact-group compose env plus the grant and book stores
// and a second user, "u-owner", who shares books with the caller "u-alice".
// The contact store has a Bleve index, as in production: owner contacts are
// indexed there alongside mail, which is why autocomplete must never query it
// for another user.
type sharedGAL struct {
	*contactGroupCompose
	acl   *store.ACLStore
	books *store.AddressBookStore
	now   time.Time
}

func newSharedGAL(t *testing.T) *sharedGAL {
	t.Helper()
	c := newContactGroupCompose(t)
	s := &sharedGAL{
		contactGroupCompose: c,
		acl:                 store.NewACLStore(c.env.ps, c.env.applier),
		books:               store.NewAddressBookStore(c.env.ps, c.env.applier),
		now:                 time.Now(),
	}
	c.env.handler.SetACLStore(s.acl)
	return s
}

func (s *sharedGAL) book(t *testing.T, owner, name string) string {
	t.Helper()
	id := store.NewAddressBookID()
	if _, err := s.books.Create(owner, id, name, ""); err != nil {
		t.Fatal(err)
	}
	return id
}

func (s *sharedGAL) contact(t *testing.T, owner, book, guid, name, email string) {
	t.Helper()
	p := &bongov1.Contact{Guid: guid, DisplayName: name, Email: email, AddressbookId: book}
	syncQuickFieldsToVCard(p)
	if _, err := s.contacts.Create(owner, p); err != nil {
		t.Fatal(err)
	}
}

func (s *sharedGAL) share(t *testing.T, owner, book, who, state string) {
	t.Helper()
	if err := s.acl.SetGrantKeepState(store.ACLKindAddressBook, owner, book, store.GranteeUser(who), store.GrantLevelRead, "", store.GrantStatePending, owner, s.now); err != nil {
		t.Fatal(err)
	}
	if state == store.GrantStateAccepted {
		if err := s.acl.AcceptGrant(store.ACLKindAddressBook, owner, book, store.GranteeUser(who), s.now); err != nil {
			t.Fatal(err)
		}
	}
}

// emails returns the addresses of the contacts-source entries of an answer.
func emails(got []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, e := range got {
		if e["source"] == "contacts" {
			if s, _ := e["email"].(string); s != "" {
				out[s] = true
			}
		}
	}
	return out
}

func TestGALSearchSharedBooks(t *testing.T) {
	s := newSharedGAL(t)
	shared := s.book(t, "u-owner", "Clients")
	unshared := s.book(t, "u-owner", "Private")
	s.contact(t, "u-owner", shared, g32("1"), "Zelda Client", "zelda@client.test")
	s.contact(t, "u-owner", shared, g32("2"), "Zed NoMail", "")
	s.contact(t, "u-owner", unshared, g32("3"), "Zoe Private", "zoe@private.test")
	s.contact(t, "u-owner", "", g32("4"), "Zack Default", "zack@default.test")
	s.group(t, "u-owner", g32("5"), "Zulu Team", "urn:uuid:"+g32("1"))

	// Pending authorises nothing, so nothing is offered.
	s.share(t, "u-owner", shared, "u-alice", store.GrantStatePending)
	if got := emails(s.gal(t, "u-alice", "q=z&contact_groups=1")); len(got) != 0 {
		t.Fatalf("a pending share is offered: %v", got)
	}

	// Accepted: the book's contacts with an address, and nothing else.
	if err := s.acl.AcceptGrant(store.ACLKindAddressBook, "u-owner", shared, store.GranteeUser("u-alice"), s.now); err != nil {
		t.Fatal(err)
	}
	got := s.gal(t, "u-alice", "q=z&contact_groups=1")
	if em := emails(got); len(em) != 1 || !em["zelda@client.test"] {
		t.Fatalf("accepted share offers %v, want only zelda@client.test", em)
	}
	for _, e := range got {
		if e["kind"] == "contact-group" {
			t.Errorf("a contact group of a shared book is offered: %v", e)
		}
		if e["source"] != "contacts" {
			t.Errorf("source = %v, want contacts", e["source"])
		}
	}
	// Matching is on the name and the address, case-insensitively.
	if em := emails(s.gal(t, "u-alice", "q=CLIENT.TEST")); !em["zelda@client.test"] {
		t.Errorf("an address match is missed: %v", em)
	}
	if em := emails(s.gal(t, "u-alice", "q=zelda")); !em["zelda@client.test"] {
		t.Errorf("a name match is missed: %v", em)
	}
	// The owner's unshared book and default book never appear, even for a query
	// that names them exactly.
	for _, q := range []string{"zoe", "private.test", "zack", "default.test"} {
		if em := emails(s.gal(t, "u-alice", "q="+q)); len(em) != 0 {
			t.Errorf("q=%s offers %v from an unshared book", q, em)
		}
	}
	// Another user gets nothing from alice's share.
	if em := emails(s.gal(t, "u-carol", "q=zelda")); len(em) != 0 {
		t.Errorf("a user without a grant gets %v", em)
	}

	// A revoke takes effect on the next call.
	if err := s.acl.RevokeGrant(store.ACLKindAddressBook, "u-owner", shared, store.GranteeUser("u-alice")); err != nil {
		t.Fatal(err)
	}
	if em := emails(s.gal(t, "u-alice", "q=zelda")); len(em) != 0 {
		t.Errorf("a revoked share still offers %v", em)
	}
}

// An address the caller already has is offered once, from their own book.
func TestGALSearchSharedBooksDeduplicate(t *testing.T) {
	s := newSharedGAL(t)
	shared := s.book(t, "u-owner", "Clients")
	s.contact(t, "u-owner", shared, g32("1"), "Zelda Shared", "zelda@client.test")
	s.contact(t, "u-alice", "", g32("2"), "Zelda Mine", "ZELDA@client.test")
	s.share(t, "u-owner", shared, "u-alice", store.GrantStateAccepted)
	n, name := 0, ""
	for _, e := range s.gal(t, "u-alice", "q=zelda") {
		if e["email"] != "" {
			n++
			name, _ = e["display_name"].(string)
		}
	}
	if n != 1 || name != "Zelda Mine" {
		t.Errorf("entries = %d (%q), want the caller's own contact once", n, name)
	}
}

func TestGALSearchSharedBooksLimits(t *testing.T) {
	t.Run("ten hits", func(t *testing.T) {
		s := newSharedGAL(t)
		shared := s.book(t, "u-owner", "Many")
		for i := range 15 {
			s.contact(t, "u-owner", shared, fmt.Sprintf("%032x", i+1), fmt.Sprintf("Zed %02d", i), fmt.Sprintf("zed%02d@client.test", i))
		}
		s.share(t, "u-owner", shared, "u-alice", store.GrantStateAccepted)
		if em := emails(s.gal(t, "u-alice", "q=zed")); len(em) != 10 {
			t.Errorf("shared hits = %d, want 10", len(em))
		}
	})
	t.Run("twenty books", func(t *testing.T) {
		s := newSharedGAL(t)
		// 21 books over two owners: one user may own at most 20.
		for i := range 21 {
			owner := "u-owner"
			if i >= 11 {
				owner = "u-owner2"
			}
			book := s.book(t, owner, fmt.Sprintf("Book %02d", i))
			s.contact(t, owner, book, fmt.Sprintf("%032x", i+1), fmt.Sprintf("Yan %02d", i), fmt.Sprintf("yan%02d@client.test", i))
			s.share(t, owner, book, "u-alice", store.GrantStateAccepted)
		}
		// A consumer that takes no hit sees every candidate the scan offers: one
		// per book, for at most 20 books.
		seen := 0
		s.env.handler.eachSharedBookContact("u-alice", "yan", func(*bongov1.Contact) bool { seen++; return false })
		if seen != 20 {
			t.Errorf("contacts scanned = %d, want 20 (one per book, at most 20 books)", seen)
		}
	})
}

// A pending grant older than the TTL offers nothing, before and after an
// accept attempt (the FSM refuses to accept an expired one).
func TestGALSearchSharedBooksExpiredPending(t *testing.T) {
	s := newSharedGAL(t)
	book := s.book(t, "u-owner", "Clients")
	s.contact(t, "u-owner", book, g32("1"), "Zelda Client", "zelda@client.test")
	old := s.now.Add(-2 * time.Duration(store.PendingGrantTTL) * time.Second)
	grantee := store.GranteeUser("u-alice")
	if err := s.acl.SetGrantKeepState(store.ACLKindAddressBook, "u-owner", book, grantee, store.GrantLevelRead, "", store.GrantStatePending, "u-owner", old); err != nil {
		t.Fatal(err)
	}
	g, err := s.acl.GetGrant(store.ACLKindAddressBook, "u-owner", book, grantee)
	if err != nil || g == nil || g.State != store.GrantStatePending || !store.PendingGrantExpired(g, s.now.Unix()) {
		t.Fatalf("seed grant = %v, %v; want an expired pending grant", g, err)
	}
	if em := emails(s.gal(t, "u-alice", "q=zelda")); len(em) != 0 {
		t.Fatalf("an expired pending share is offered: %v", em)
	}
	if err := s.acl.AcceptGrant(store.ACLKindAddressBook, "u-owner", book, grantee, s.now); err != nil {
		t.Fatal(err)
	}
	if em := emails(s.gal(t, "u-alice", "q=zelda")); len(em) != 0 {
		t.Fatalf("an expired pending share is offered after an accept attempt: %v", em)
	}

	// Positive control: a live accepted share on another book is offered.
	live := s.book(t, "u-owner", "Live")
	s.contact(t, "u-owner", live, g32("2"), "Zelda Live", "zelda@live.test")
	s.share(t, "u-owner", live, "u-alice", store.GrantStateAccepted)
	if em := emails(s.gal(t, "u-alice", "q=zelda")); len(em) != 1 || !em["zelda@live.test"] {
		t.Fatalf("control: offers %v, want only zelda@live.test", em)
	}
}
