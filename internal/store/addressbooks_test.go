package store

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type addressBookEnv struct {
	ps       *PebbleStore
	books    *AddressBookStore
	contacts *ContactStore
	app      RaftApplier
}

func newAddressBookEnv(t *testing.T, wrap func(RaftApplier) RaftApplier) *addressBookEnv {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	var app RaftApplier = &directApplier{store: ps}
	if wrap != nil {
		app = wrap(app)
	}
	return &addressBookEnv{ps: ps, books: NewAddressBookStore(ps, app), contacts: NewContactStore(ps, app), app: app}
}

func TestCanonicalAddressBookSegment(t *testing.T) {
	const hex = "0123456789abcdef0123456789abcdef"
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"default", "default", "default", true},
		{"lower hex", hex, hex, true},
		{"upper hex", strings.ToUpper(hex), hex, true},
		{"dashed uuid", "01234567-89ab-cdef-0123-456789abcdef", hex, true},
		{"upper dashed uuid", "01234567-89AB-CDEF-0123-456789ABCDEF", hex, true},
		{"traversal", "../x", "", false},
		{"nested", "default/..", "", false},
		{"empty", "", "", false},
		{"31 characters", hex[:31], "", false},
		{"33 characters", hex + "a", "", false},
		{"upper default", "DEFAULT", "", false},
		{"non hex", strings.Repeat("g", 32), "", false},
		{"misplaced hyphens", "0123456-789abc-def0123456789abcdef0", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CanonicalAddressBookSegment(tt.in)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("CanonicalAddressBookSegment(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
			}
		})
	}
	if ValidAddressBookID("default") || ValidAddressBookID(strings.ToUpper(hex)) || !ValidAddressBookID(hex) {
		t.Fatal("ValidAddressBookID accepts only lower-case 32 hex")
	}
}

func TestAddressBookCRUD(t *testing.T) {
	env := newAddressBookEnv(t, nil)
	const u = "alice"
	idB, idA := NewAddressBookID(), NewAddressBookID()

	if _, err := env.books.Create(u, "not-an-id", "x", ""); err == nil {
		t.Fatal("create with an invalid id succeeded")
	}
	if _, err := env.books.Create(u, idB, "Work", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.books.Create(u, idB, "Again", ""); !errors.Is(err, ErrAddressBookExists) {
		t.Fatalf("duplicate create err = %v, want ErrAddressBookExists", err)
	}
	if _, err := env.books.Create(u, idA, "Family", ""); err != nil {
		t.Fatal(err)
	}

	list, err := env.books.List(u)
	if err != nil || len(list) != 2 || list[0].Name != "Family" || list[1].Name != "Work" {
		t.Fatalf("List = %v, %v; want Family, Work", list, err)
	}
	if other, _ := env.books.List("bob"); len(other) != 0 {
		t.Fatalf("another user sees %d books", len(other))
	}

	got, err := env.books.Get(u, idB)
	if err != nil || got.Description != "d" || got.CreatedAt == 0 {
		t.Fatalf("Get = %v, %v", got, err)
	}
	if _, err := env.books.Get("bob", idB); !errors.Is(err, ErrAddressBookNotFound) || !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user's Get err = %v, want not found", err)
	}
	if _, err := env.books.Get(u, "default"); !errors.Is(err, ErrAddressBookNotFound) {
		t.Fatalf("Get default err = %v, want not found", err)
	}

	if err := env.books.Rename(u, idB, "Office"); err != nil {
		t.Fatal(err)
	}
	if got, _ := env.books.Get(u, idB); got.Name != "Office" || got.Description != "d" || got.UpdatedAt < got.CreatedAt {
		t.Fatalf("after rename: %v", got)
	}
	if err := env.books.Rename(u, NewAddressBookID(), "x"); !errors.Is(err, ErrAddressBookNotFound) {
		t.Fatalf("rename of a missing book err = %v", err)
	}

	if err := env.books.Delete(u, idB); err != nil {
		t.Fatal(err)
	}
	if _, err := env.books.Get(u, idB); !errors.Is(err, ErrAddressBookNotFound) {
		t.Fatalf("Get after delete err = %v", err)
	}
}

func TestAddressBookCap(t *testing.T) {
	env := newAddressBookEnv(t, nil)
	const u = "alice"
	for i := range MaxAddressBooksPerUser {
		if _, err := env.books.Create(u, NewAddressBookID(), fmt.Sprintf("b%02d", i), ""); err != nil {
			t.Fatalf("book %d: %v", i, err)
		}
	}
	if _, err := env.books.Create(u, NewAddressBookID(), "one too many", ""); !errors.Is(err, ErrAddressBookLimit) {
		t.Fatalf("21st book err = %v, want ErrAddressBookLimit", err)
	}
	if _, err := env.books.Create("bob", NewAddressBookID(), "mine", ""); err != nil {
		t.Fatalf("the cap is per user: %v", err)
	}
}

func TestAddressBookKeyPrefixDoesNotMatchSibling(t *testing.T) {
	if !strings.HasSuffix(string(addressBookPrefix("u")), "/") {
		t.Fatal("prefix must end in a slash")
	}
}

func TestListByBook(t *testing.T) {
	env := newAddressBookEnv(t, nil)
	const u = "alice"
	book := NewAddressBookID()
	for _, c := range []*bongov1.Contact{
		{Guid: "d1", DisplayName: "d1"},
		{Guid: "d2", DisplayName: "d2", AddressbookId: "default"},
		{Guid: "w1", DisplayName: "w1", AddressbookId: book},
		{Guid: "w2", DisplayName: "w2", AddressbookId: book},
		{Guid: "w3", DisplayName: "w3", AddressbookId: book},
	} {
		if _, err := env.contacts.Create(u, c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.contacts.Create("bob", &bongov1.Contact{Guid: "b1", AddressbookId: book}); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"", "default"} {
		got, total, err := env.contacts.ListByBook(u, id, 0, 0)
		if err != nil || total != 2 || len(got) != 2 {
			t.Fatalf("ListByBook(%q) = %d contacts, total %d, %v; want 2", id, len(got), total, err)
		}
	}
	page, total, _ := env.contacts.ListByBook(u, book, 1, 1)
	if total != 3 || len(page) != 1 {
		t.Fatalf("page = %d, total %d; want 1 of 3", len(page), total)
	}
	if all, total, _ := env.contacts.List(u, 0, 0); total != 5 || len(all) != 5 {
		t.Fatalf("List must stay book-agnostic: %d", total)
	}
}

// TestContactsInAnyBookFeedAddresses proves the autocomplete and "from my
// contacts" source reads every book.
func TestContactsInAnyBookFeedAddresses(t *testing.T) {
	env := newAddressBookEnv(t, nil)
	const u = "alice"
	if _, err := env.contacts.Create(u, &bongov1.Contact{Guid: "a", Email: "default@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.contacts.Create(u, &bongov1.Contact{Guid: "b", Email: "work@example.com", AddressbookId: NewAddressBookID()}); err != nil {
		t.Fatal(err)
	}
	// A contact whose book is gone is still counted.
	if _, err := env.contacts.Create(u, &bongov1.Contact{Guid: "c", Email: "orphan@example.com", AddressbookId: NewAddressBookID()}); err != nil {
		t.Fatal(err)
	}
	addrs, err := env.contacts.Addresses(u)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"default@example.com", "work@example.com", "orphan@example.com"} {
		if !addrs[want] {
			t.Errorf("Addresses misses %s", want)
		}
	}
}

func TestPurgeAddressBook(t *testing.T) {
	env := newAddressBookEnv(t, nil)
	const u = "alice"
	book, other := NewAddressBookID(), NewAddressBookID()
	for _, id := range []string{book, other} {
		if _, err := env.books.Create(u, id, "b", ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []*bongov1.Contact{
		{Guid: "in1", AddressbookId: book}, {Guid: "in2", AddressbookId: book},
		{Guid: "out", AddressbookId: other}, {Guid: "def"},
	} {
		if _, err := env.contacts.Create(u, c); err != nil {
			t.Fatal(err)
		}
	}
	grant, err := BuildACLCommand(ACLRequest{Op: ACLOpSet, Kind: ACLKindAddressBook, Owner: u, Resource: book,
		Grantee: GranteeUser("bob"), Level: GrantLevelRead, State: GrantStateAccepted, GrantedBy: u, At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.app.ApplyCommand(grant, time.Second); err != nil {
		t.Fatal(err)
	}
	acl := NewACLStore(env.ps, env.app)
	if g, _ := acl.GetGrant(ACLKindAddressBook, u, book, GranteeUser("bob")); g == nil {
		t.Fatal("grant was not written; the test applier does not dispatch ACL commands")
	}

	if err := PurgeAddressBook(env.books, env.contacts, u, "default"); !errors.Is(err, ErrAddressBookProtected) {
		t.Fatalf("default err = %v, want protected", err)
	}
	if err := PurgeAddressBook(env.books, env.contacts, u, NewAddressBookID()); !errors.Is(err, ErrAddressBookNotFound) {
		t.Fatalf("missing err = %v, want not found", err)
	}
	if err := PurgeAddressBook(env.books, env.contacts, u, book); err != nil {
		t.Fatal(err)
	}
	if _, err := env.books.Get(u, book); !errors.Is(err, ErrAddressBookNotFound) {
		t.Fatalf("book survives: %v", err)
	}
	for _, g := range []string{"in1", "in2"} {
		if _, err := env.contacts.Get(u, g); err == nil {
			t.Errorf("contact %s survives", g)
		}
	}
	for _, g := range []string{"out", "def"} {
		if _, err := env.contacts.Get(u, g); err != nil {
			t.Errorf("contact %s was removed: %v", g, err)
		}
	}
	if _, err := env.books.Get(u, other); err != nil {
		t.Errorf("other book removed: %v", err)
	}
	if g, _ := acl.GetGrant(ACLKindAddressBook, u, book, GranteeUser("bob")); g != nil {
		t.Error("grant survives the purge")
	}
}

// nthFailApplier fails the nth ApplyCommand that matches pred, then recovers
// when healed.
type nthFailApplier struct {
	inner RaftApplier
	mu    sync.Mutex
	pred  func(*bongov1.RaftCommand) bool
	nth   int
	seen  int
	heal  bool
}

var errBookInjected = errors.New("injected apply failure")

func (f *nthFailApplier) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	f.mu.Lock()
	fail := false
	if !f.heal && f.pred(cmd) {
		f.seen++
		fail = f.seen >= f.nth
	}
	f.mu.Unlock()
	if fail {
		return errBookInjected
	}
	return f.inner.ApplyCommand(cmd, d)
}

func (f *nthFailApplier) healUp() {
	f.mu.Lock()
	f.heal = true
	f.mu.Unlock()
}

func TestPurgeAddressBookResumesAfterFailure(t *testing.T) {
	var fa *nthFailApplier
	env := newAddressBookEnv(t, func(inner RaftApplier) RaftApplier {
		fa = &nthFailApplier{inner: inner, nth: 3, pred: func(c *bongov1.RaftCommand) bool {
			return c.Op == bongov1.Op_OP_DELETE && strings.Contains(string(c.Key), "/contact/")
		}}
		return fa
	})
	const u = "alice"
	book := NewAddressBookID()
	if _, err := env.books.Create(u, book, "b", ""); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, err := env.contacts.Create(u, &bongov1.Contact{Guid: fmt.Sprintf("c%d", i), AddressbookId: book}); err != nil {
			t.Fatal(err)
		}
	}

	err := PurgeAddressBook(env.books, env.contacts, u, book)
	if !errors.Is(err, errBookInjected) {
		t.Fatalf("err = %v, want the injected failure", err)
	}
	if _, err := env.books.Get(u, book); err != nil {
		t.Fatalf("the book key must survive a failed purge: %v", err)
	}
	left, _, _ := env.contacts.ListByBook(u, book, 0, 0)
	if len(left) == 0 || len(left) == 5 {
		t.Fatalf("%d contacts remain, want some but not all", len(left))
	}

	fa.healUp()
	if err := PurgeAddressBook(env.books, env.contacts, u, book); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if left, _, _ := env.contacts.ListByBook(u, book, 0, 0); len(left) != 0 {
		t.Fatalf("%d contacts remain after the retry", len(left))
	}
	if _, err := env.books.Get(u, book); !errors.Is(err, ErrAddressBookNotFound) {
		t.Fatalf("book survives the retry: %v", err)
	}
}

func TestPurgeAddressBookGrantRevokeFailureIsReported(t *testing.T) {
	env := newAddressBookEnv(t, func(inner RaftApplier) RaftApplier {
		return &nthFailApplier{inner: inner, nth: 1, pred: func(c *bongov1.RaftCommand) bool {
			return IsACLSyntheticCommand(c.Key)
		}}
	})
	const u = "alice"
	book := NewAddressBookID()
	if _, err := env.books.Create(u, book, "b", ""); err != nil {
		t.Fatal(err)
	}
	err := PurgeAddressBook(env.books, env.contacts, u, book)
	if !errors.Is(err, ErrAddressBookGrantsNotRevoked) || !errors.Is(err, errBookInjected) {
		t.Fatalf("err = %v, want ErrAddressBookGrantsNotRevoked wrapping the cause", err)
	}
	if _, err := env.books.Get(u, book); !errors.Is(err, ErrAddressBookNotFound) {
		t.Fatalf("the book must be gone: %v", err)
	}
}
