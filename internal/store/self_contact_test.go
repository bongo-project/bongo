package store

import (
	"bytes"
	"errors"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type selfEnv struct {
	ps    *PebbleStore
	cs    *ContactStore
	prefs *PreferenceStore
	cnt   *countingDirectApplier
}

func newSelfEnv(t *testing.T) *selfEnv {
	t.Helper()
	ps, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	cnt := &countingDirectApplier{inner: &directApplier{store: ps}}
	return &selfEnv{ps: ps, cs: NewContactStore(ps, cnt), prefs: NewPreferenceStore(ps, cnt), cnt: cnt}
}

func (e *selfEnv) mark(t *testing.T, user, guid string) {
	t.Helper()
	if err := e.cs.SetSelfContact(user, guid); err != nil {
		t.Fatalf("SetSelfContact: %v", err)
	}
}

func TestSelfContactResolvesOwnCard(t *testing.T) {
	e := newSelfEnv(t)
	guid := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "Jane", Email: "jane@example.com"})
	if c, ok := e.cs.SelfContact("u1"); ok || c != nil {
		t.Fatal("unset preference must resolve to nothing")
	}
	e.mark(t, "u1", guid)
	c, ok := e.cs.SelfContact("u1")
	if !ok || c.Guid != guid {
		t.Fatalf("SelfContact = %v, %v", c, ok)
	}
	if _, ok := e.cs.SelfContact("u2"); ok {
		t.Error("another user must not resolve the card")
	}
}

func TestSelfContactDanglingIsUnset(t *testing.T) {
	e := newSelfEnv(t)
	p := DefaultPreferences()
	p.SelfContactGuid = "nosuchcard"
	if err := e.prefs.Set("u1", p); err != nil {
		t.Fatal(err)
	}
	if c, ok := e.cs.SelfContact("u1"); ok || c != nil {
		t.Fatalf("dangling guid resolved: %v", c)
	}
}

func TestSelfContactGroupIsUnset(t *testing.T) {
	e := newSelfEnv(t)
	guid := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "Jane"})
	e.mark(t, "u1", guid)
	if err := e.cs.Update("u1", guid, &bongov1.Contact{DisplayName: "Team", VcardRaw: vcard4Group("mailto:a@example.com")}); err != nil {
		t.Fatal(err)
	}
	if c, ok := e.cs.SelfContact("u1"); ok || c != nil {
		t.Fatal("a card turned into a group must read as unset")
	}
}

func TestSetSelfContactRefusesGroup(t *testing.T) {
	e := newSelfEnv(t)
	keep := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "Jane"})
	e.mark(t, "u1", keep)
	grp := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "Team", VcardRaw: vcard4Group("mailto:a@example.com")})
	if err := e.cs.SetSelfContact("u1", grp); !errors.Is(err, ErrSelfContactGroup) {
		t.Fatalf("err = %v, want ErrSelfContactGroup", err)
	}
	if got := e.prefs.Get("u1").SelfContactGuid; got != keep {
		t.Errorf("preference changed to %q", got)
	}
}

func TestSetSelfContactRefusesMissing(t *testing.T) {
	e := newSelfEnv(t)
	if err := e.cs.SetSelfContact("u1", "nope"); !errors.Is(err, ErrContactNotFound) {
		t.Fatalf("err = %v, want ErrContactNotFound", err)
	}
	// Another user's card is not found in this keyspace.
	other := mustCreate(t, e.cs, "u2", &bongov1.Contact{DisplayName: "X"})
	if err := e.cs.SetSelfContact("u1", other); !errors.Is(err, ErrContactNotFound) {
		t.Fatalf("err = %v, want ErrContactNotFound", err)
	}
	if e.prefs.Get("u1").SelfContactGuid != "" {
		t.Error("preference written")
	}
}

func TestSetSelfContactReplacesPrevious(t *testing.T) {
	e := newSelfEnv(t)
	a := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "A"})
	b := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "B"})
	e.mark(t, "u1", a)
	e.mark(t, "u1", b)
	if got := e.prefs.Get("u1").SelfContactGuid; got != b {
		t.Errorf("self = %q, want %q", got, b)
	}
}

func TestClearSelfContactOnlyMatching(t *testing.T) {
	e := newSelfEnv(t)
	a := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "A"})
	b := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "B"})
	e.mark(t, "u1", a)
	before := e.cnt.n
	if err := e.cs.ClearSelfContact("u1", b); err != nil {
		t.Fatal(err)
	}
	if e.prefs.Get("u1").SelfContactGuid != a {
		t.Error("a stale clear removed a different card")
	}
	if e.cnt.n != before {
		t.Error("a non-matching clear wrote")
	}
	if err := e.cs.ClearSelfContact("u1", a); err != nil {
		t.Fatal(err)
	}
	if e.prefs.Get("u1").SelfContactGuid != "" {
		t.Error("matching clear did not clear")
	}
}

func TestContactDeleteClearsSelfContact(t *testing.T) {
	e := newSelfEnv(t)
	guid := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "A"})
	p := DefaultPreferences()
	p.Theme = bongov1.Theme_THEME_DARK
	p.Signature = "sig"
	p.StartPage = "inbox"
	if err := e.prefs.Set("u1", p); err != nil {
		t.Fatal(err)
	}
	e.mark(t, "u1", guid)
	if err := e.cs.Delete("u1", guid); err != nil {
		t.Fatal(err)
	}
	got := e.prefs.Get("u1")
	if got.SelfContactGuid != "" {
		t.Error("self contact survives the delete")
	}
	if got.Theme != bongov1.Theme_THEME_DARK || got.Signature != "sig" || got.StartPage != "inbox" {
		t.Errorf("other preferences changed: %v", got)
	}
}

func TestContactDeleteOtherWritesNoPreference(t *testing.T) {
	e := newSelfEnv(t)
	a := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "A"})
	b := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "B"})
	e.mark(t, "u1", a)
	before := e.cnt.n
	if err := e.cs.Delete("u1", b); err != nil {
		t.Fatal(err)
	}
	if n := e.cnt.n - before; n != 1 {
		t.Errorf("delete issued %d commands, want 1", n)
	}
}

func TestContactDeleteNoPrefsRecordStaysAbsent(t *testing.T) {
	e := newSelfEnv(t)
	guid := mustCreate(t, e.cs, "u1", &bongov1.Contact{DisplayName: "A"})
	if err := e.cs.Delete("u1", guid); err != nil {
		t.Fatal(err)
	}
	if data, _ := e.ps.Get(prefKey("u1")); len(data) != 0 {
		t.Error("a preferences record was created by a contact delete")
	}
}

func TestSelfContactGUIDReuseAfterDelete(t *testing.T) {
	e := newSelfEnv(t)
	guid := mustCreate(t, e.cs, "u1", &bongov1.Contact{Guid: "fromhref", DisplayName: "A"})
	e.mark(t, "u1", guid)
	if err := e.cs.Delete("u1", guid); err != nil {
		t.Fatal(err)
	}
	// A CardDAV PUT at the same href creates a card under the same GUID.
	if _, err := e.cs.Create("u1", &bongov1.Contact{Guid: "fromhref", DisplayName: "Someone else"}); err != nil {
		t.Fatal(err)
	}
	if c, ok := e.cs.SelfContact("u1"); ok {
		t.Fatalf("the new card %q inherited This is me", c.DisplayName)
	}
}

func TestPurgeAddressBookClearsSelfContact(t *testing.T) {
	env := newAddressBookEnv(t, nil)
	const u = "alice"
	book := NewAddressBookID()
	if _, err := env.books.Create(u, book, "b", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := env.contacts.Create(u, &bongov1.Contact{Guid: "me", AddressbookId: book}); err != nil {
		t.Fatal(err)
	}
	if err := env.contacts.SetSelfContact(u, "me"); err != nil {
		t.Fatal(err)
	}
	if err := PurgeAddressBook(env.books, env.contacts, u, book); err != nil {
		t.Fatal(err)
	}
	if got := NewPreferenceStore(env.ps, env.app).Get(u).SelfContactGuid; got != "" {
		t.Errorf("self contact %q survives the book purge", got)
	}
}

func TestPreferencesKeepUnknownFields(t *testing.T) {
	e := newSelfEnv(t)
	raw, err := proto.Marshal(DefaultPreferences())
	if err != nil {
		t.Fatal(err)
	}
	extra := protowire.AppendTag(nil, 99, protowire.BytesType)
	extra = protowire.AppendString(extra, "from-a-newer-node")
	if err := e.ps.Set(prefKey("u1"), append(raw, extra...)); err != nil {
		t.Fatal(err)
	}
	p := e.prefs.Get("u1")
	p.Signature = "x"
	if err := e.prefs.Set("u1", p); err != nil {
		t.Fatal(err)
	}
	data, _ := e.ps.Get(prefKey("u1"))
	if !bytes.Contains(data, []byte("from-a-newer-node")) {
		t.Error("a Get then Set dropped an unknown field")
	}
}
