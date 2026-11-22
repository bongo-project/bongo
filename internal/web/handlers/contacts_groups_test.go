package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

type groupEnv struct {
	h        *ContactsHandler
	contacts *store.ContactStore
	books    *store.AddressBookStore
}

func newGroupEnv(t *testing.T) *groupEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &directApplierTags{ps: ps}
	e := &groupEnv{books: store.NewAddressBookStore(ps, app), contacts: store.NewContactStore(ps, app)}
	e.h = NewContactsHandler(e.contacts, nil)
	e.h.SetAddressBooks(e.books)
	return e
}

// call runs a handler as user with the URL parameters and form values given.
func call(h http.HandlerFunc, user, method, path string, params map[string]string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	ctx := middleware.WithUserContext(req.Context(), user, user+"@example.test")
	ctx = middleware.WithLocale(ctx, "en")
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr
}

func (e *groupEnv) person(t *testing.T, user, guid, name, email, book string) {
	t.Helper()
	c := &bongov1.Contact{Guid: guid, DisplayName: name, Email: email, AddressbookId: book}
	syncQuickFieldsToVCard(c)
	if _, err := e.contacts.Create(user, c); err != nil {
		t.Fatal(err)
	}
}

func (e *groupEnv) newGroup(t *testing.T, user, name, book string) string {
	t.Helper()
	form := url.Values{"name": {name}}
	if book != "" {
		form.Set("addressbook", book)
	}
	rr := call(e.h.CreateContactGroup, user, http.MethodPost, "/contacts/groups", nil, form)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create group: %d %s", rr.Code, rr.Body.String())
	}
	groups, _ := e.contacts.ListContactGroups(user, "")
	for _, g := range groups {
		if g.DisplayName == name {
			return g.Guid
		}
	}
	t.Fatal("created group not listed")
	return ""
}

func (e *groupEnv) add(user, member, group string) *httptest.ResponseRecorder {
	return call(e.h.AddToContactGroup, user, http.MethodPost, "/contacts/"+member+"/add-to-group",
		map[string]string{"guid": member}, url.Values{"group": {group}})
}

func g32(c string) string { return strings.Repeat(c, 32) }

func TestContactGroupCreateRenameDelete(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	e.person(t, u, g32("a"), "Ann", "ann@example.com", "")
	gid := e.newGroup(t, u, "Team", "")

	g, _ := e.contacts.Get(u, gid)
	if g.Kind != "group" || g.DisplayName != "Team" {
		t.Fatalf("group = %+v", g)
	}
	if !strings.Contains(string(g.VcardRaw), "VERSION:3.0") || !strings.Contains(string(g.VcardRaw), "X-ADDRESSBOOKSERVER-KIND:group") {
		t.Errorf("a web group is written in Apple's form, VERSION 3.0:\n%s", g.VcardRaw)
	}

	if rr := e.add(u, g32("a"), gid); rr.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", rr.Code, rr.Body.String())
	}

	rr := call(e.h.RenameContactGroup, u, http.MethodPost, "/", map[string]string{"guid": gid}, url.Values{"name": {"Crew"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("rename: %d", rr.Code)
	}
	g, _ = e.contacts.Get(u, gid)
	if g.DisplayName != "Crew" || !strings.Contains(string(g.VcardRaw), "FN:Crew") || len(g.Members) != 1 {
		t.Errorf("after rename: %q members=%v\n%s", g.DisplayName, g.Members, g.VcardRaw)
	}
	if rr := call(e.h.RenameContactGroup, u, http.MethodPost, "/", map[string]string{"guid": gid}, url.Values{"name": {"  "}}); rr.Code != http.StatusBadRequest {
		t.Errorf("blank name: %d, want 400", rr.Code)
	}

	// Delete removes the group card only; the member stays.
	rr = call(e.h.DeleteContactGroup, u, http.MethodPost, "/", map[string]string{"guid": gid}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", rr.Code)
	}
	if _, err := e.contacts.Get(u, gid); err == nil {
		t.Error("the group survived its delete")
	}
	if _, err := e.contacts.Get(u, g32("a")); err != nil {
		t.Errorf("deleting a group deleted its member: %v", err)
	}
}

func TestContactGroupAddMemberRefusals(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	book := store.NewAddressBookID()
	if _, err := e.books.Create(u, book, "Work", ""); err != nil {
		t.Fatal(err)
	}
	e.person(t, u, g32("a"), "Ann", "ann@example.com", "")
	e.person(t, u, g32("b"), "Bob", "bob@example.com", book)
	e.person(t, "mallory", g32("c"), "Mal", "mal@example.com", "")
	gid := e.newGroup(t, u, "Team", "")
	other := e.newGroup(t, u, "Other", "")

	full := e.newGroup(t, u, "Full", "")
	fg, _ := e.contacts.Get(u, full)
	var err error
	for i := 1; i <= store.MaxContactGroupMembers; i++ {
		if fg.VcardRaw, err = store.AddContactGroupMember(fg.VcardRaw, fmt.Sprintf("urn:uuid:%032x", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.contacts.Update(u, full, fg); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name          string
		user          string
		member, group string
		want          int
	}{
		{"another user's contact", u, g32("c"), gid, http.StatusNotFound},
		{"unknown contact", u, g32("9"), gid, http.StatusNotFound},
		{"another user's group", "mallory", g32("c"), gid, http.StatusNotFound},
		{"guid of a person as the group", u, g32("a"), g32("b"), http.StatusNotFound},
		{"the contact in another book", u, g32("b"), gid, http.StatusBadRequest},
		{"a group as member", u, other, gid, http.StatusBadRequest},
		{"the group itself", u, gid, gid, http.StatusBadRequest},
		{"group is full", u, g32("a"), full, http.StatusBadRequest},
		{"fine", u, g32("a"), gid, http.StatusSeeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rr := e.add(tc.user, tc.member, tc.group); rr.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", rr.Code, tc.want, rr.Body.String())
			}
			// A refused add writes nothing, not even a UID into the member card.
			if tc.want != http.StatusSeeOther && tc.user == u {
				if m, err := e.contacts.Get(u, tc.member); err == nil && strings.Contains(string(m.VcardRaw), "\nUID:") && tc.name != "a group as member" && tc.name != "the group itself" {
					t.Errorf("a refused add wrote a UID into %s:\n%s", tc.name, m.VcardRaw)
				}
			}
		})
	}
	g, _ := e.contacts.Get(u, gid)
	if len(g.Members) != 1 {
		t.Errorf("only the valid add may have landed: %v", g.Members)
	}
	fg, _ = e.contacts.Get(u, full)
	if len(fg.Members) != store.MaxContactGroupMembers {
		t.Errorf("a refused add changed the full group: %d members", len(fg.Members))
	}
}

func TestContactGroupAddMemberWritesUID(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	// A card imported with no UID and a GUID that is not 32 hex.
	imported := &bongov1.Contact{Guid: "legacy-1", DisplayName: "Ann", Email: "ann@example.com",
		VcardRaw: []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ann\r\nEMAIL:ann@example.com\r\nEND:VCARD\r\n")}
	if _, err := e.contacts.Create(u, imported); err != nil {
		t.Fatal(err)
	}
	e.person(t, u, g32("b"), "Bob", "bob@example.com", "")
	gid := e.newGroup(t, u, "Team", "")

	if rr := e.add(u, "legacy-1", gid); rr.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", rr.Code, rr.Body.String())
	}
	if rr := e.add(u, g32("b"), gid); rr.Code != http.StatusSeeOther {
		t.Fatalf("add: %d", rr.Code)
	}
	// Adding twice is idempotent.
	if rr := e.add(u, g32("b"), gid); rr.Code != http.StatusSeeOther {
		t.Fatalf("add again: %d", rr.Code)
	}

	ann, _ := e.contacts.Get(u, "legacy-1")
	uid := ""
	for _, line := range strings.Split(string(ann.VcardRaw), "\r\n") {
		if strings.HasPrefix(line, "UID:") {
			uid = strings.TrimPrefix(line, "UID:")
		}
	}
	if uid == "" {
		t.Fatalf("the member card gained no UID:\n%s", ann.VcardRaw)
	}
	g, _ := e.contacts.Get(u, gid)
	if !strings.Contains(string(g.VcardRaw), "X-ADDRESSBOOKSERVER-MEMBER:urn:uuid:"+uid) {
		t.Errorf("the group does not name the UID %q:\n%s", uid, g.VcardRaw)
	}
	if len(g.Members) != 2 {
		t.Errorf("Members = %v, want 2", g.Members)
	}
	bob, _ := e.contacts.Get(u, g32("b"))
	if !strings.Contains(string(bob.VcardRaw), "UID:") {
		t.Errorf("Bob's card has no UID:\n%s", bob.VcardRaw)
	}
	if !strings.Contains(string(g.VcardRaw), "urn:uuid:"+store.ContactMemberRef(g32("b"))[0:8]) {
		t.Errorf("Bob is not in the group by his GUID-derived UID:\n%s", g.VcardRaw)
	}
}

func TestContactGroupRemoveMember(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	e.person(t, u, g32("a"), "Ann", "ann@example.com", "")
	gid := e.newGroup(t, u, "Team", "")
	e.add(u, g32("a"), gid)
	g, _ := e.contacts.Get(u, gid)
	ref := g.Members[0]

	if rr := call(e.h.RemoveContactGroupMember, u, http.MethodPost, "/", map[string]string{"guid": gid}, url.Values{"member": {""}}); rr.Code != http.StatusBadRequest {
		t.Errorf("empty member: %d", rr.Code)
	}
	if rr := call(e.h.RemoveContactGroupMember, u, http.MethodPost, "/", map[string]string{"guid": gid}, url.Values{"member": {ref}}); rr.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", rr.Code)
	}
	g, _ = e.contacts.Get(u, gid)
	if len(g.Members) != 0 {
		t.Errorf("Members = %v after remove", g.Members)
	}
	if _, err := e.contacts.Get(u, g32("a")); err != nil {
		t.Error("removing a member deleted the contact")
	}
}

// Another user's guid is one 404 on every group route, and nothing is written.
func TestContactGroupForeignGuid404(t *testing.T) {
	e := newGroupEnv(t)
	e.person(t, "alice", g32("a"), "Ann", "ann@example.com", "")
	gid := e.newGroup(t, "alice", "Team", "")
	e.add("alice", g32("a"), gid)
	before, _ := e.contacts.Get("alice", gid)

	p := map[string]string{"guid": gid}
	for name, rr := range map[string]*httptest.ResponseRecorder{
		"rename": call(e.h.RenameContactGroup, "bob", http.MethodPost, "/", p, url.Values{"name": {"Pwn"}}),
		"delete": call(e.h.DeleteContactGroup, "bob", http.MethodPost, "/", p, nil),
		"remove": call(e.h.RemoveContactGroupMember, "bob", http.MethodPost, "/", p, url.Values{"member": {before.Members[0]}}),
		"add":    e.add("bob", g32("a"), gid),
	} {
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s as another user: %d, want 404", name, rr.Code)
		}
	}
	after, err := e.contacts.Get("alice", gid)
	if err != nil || after.DisplayName != "Team" || len(after.Members) != 1 {
		t.Errorf("a foreign request changed the group: %v %+v", err, after)
	}
	// A person's guid on a group route is the same 404.
	if rr := call(e.h.DeleteContactGroup, "alice", http.MethodPost, "/", map[string]string{"guid": g32("a")}, nil); rr.Code != http.StatusNotFound {
		t.Errorf("person guid on delete-group: %d", rr.Code)
	}
	if _, err := e.contacts.Get("alice", g32("a")); err != nil {
		t.Error("delete-group deleted a person")
	}
}

func TestUpdateContactRefusesGroup(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	gid := e.newGroup(t, u, "Team", "")
	before, _ := e.contacts.Get(u, gid)

	rr := call(e.h.UpdateContact, u, http.MethodPost, "/contacts/"+gid, map[string]string{"guid": gid},
		url.Values{"display_name": {"Team"}, "email": {"x@example.com"}, "phone": {"555"}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
	after, _ := e.contacts.Get(u, gid)
	if string(after.VcardRaw) != string(before.VcardRaw) || after.Email != "" {
		t.Errorf("the person form changed a group:\n%s", after.VcardRaw)
	}
}

func TestDeleteContactDropsFromGroups(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	e.person(t, u, g32("a"), "Ann", "ann@example.com", "")
	e.person(t, u, g32("b"), "Bob", "bob@example.com", "")
	gid := e.newGroup(t, u, "Team", "")
	e.add(u, g32("a"), gid)
	e.add(u, g32("b"), gid)

	rr := call(e.h.DeleteContact, u, http.MethodDelete, "/contacts/"+g32("a"), map[string]string{"guid": g32("a")}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d", rr.Code)
	}
	g, _ := e.contacts.Get(u, gid)
	if len(g.Members) != 1 || strings.Contains(string(g.VcardRaw), store.ContactMemberRef(g32("a"))[:8]) {
		t.Errorf("the deleted contact is still a member: %v\n%s", g.Members, g.VcardRaw)
	}
	res, _ := e.contacts.ResolveContactGroup(u, g)
	if len(res.Members) != 1 || res.Members[0].Contact.Guid != g32("b") {
		t.Errorf("resolved members wrong: %+v", res.Members)
	}
}

// A group in an address book other than the default lives and is listed there.
func TestContactGroupInABook(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	book := store.NewAddressBookID()
	if _, err := e.books.Create(u, book, "Work", ""); err != nil {
		t.Fatal(err)
	}
	gid := e.newGroup(t, u, "Work team", book)
	g, _ := e.contacts.Get(u, gid)
	if g.AddressbookId != book {
		t.Fatalf("group filed in %q, want %q", g.AddressbookId, book)
	}
	if rr := call(e.h.CreateContactGroup, u, http.MethodPost, "/", nil, url.Values{"name": {"X"}, "addressbook": {g32("9")}}); rr.Code != http.StatusBadRequest {
		t.Errorf("another user's or unknown book: %d, want 400", rr.Code)
	}
	if rr := call(e.h.CreateContactGroup, u, http.MethodPost, "/", nil, url.Values{"name": {" "}}); rr.Code != http.StatusBadRequest {
		t.Errorf("blank name: %d, want 400", rr.Code)
	}
	inBook, _ := e.contacts.ListContactGroups(u, book)
	inDefault, _ := e.contacts.ListContactGroups(u, store.DefaultAddressBookID)
	if len(inBook) != 1 || len(inDefault) != 0 {
		t.Errorf("book listing: %d in book, %d in default", len(inBook), len(inDefault))
	}
}

// A group name is one line: control characters, a CRLF in particular, cannot
// start another property in the card, on create or on rename.
func TestContactGroupNameCannotInjectProperties(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	const evil = "A\r\nEMAIL:x@y.example"
	check := func(what, guid string) {
		t.Helper()
		g, err := e.contacts.Get(u, guid)
		if err != nil {
			t.Fatal(err)
		}
		raw := string(g.VcardRaw)
		if strings.Contains(raw, "EMAIL") && strings.Contains(raw, "\r\nEMAIL:") || g.Email != "" {
			t.Errorf("%s: a property was injected:\n%s", what, raw)
		}
		for _, line := range strings.Split(strings.TrimRight(raw, "\r\n"), "\r\n") {
			if strings.HasPrefix(line, "EMAIL") {
				t.Errorf("%s: EMAIL line %q", what, line)
			}
		}
		if strings.ContainsAny(g.DisplayName, "\r\n") {
			t.Errorf("%s: DisplayName %q", what, g.DisplayName)
		}
	}
	rr0 := call(e.h.CreateContactGroup, u, http.MethodPost, "/contacts/groups", nil, url.Values{"name": {evil}})
	if rr0.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", rr0.Code)
	}
	groups, _ := e.contacts.ListContactGroups(u, "")
	if len(groups) != 1 {
		t.Fatalf("groups = %d", len(groups))
	}
	gid := groups[0].Guid
	check("create", gid)

	rr := call(e.h.RenameContactGroup, u, http.MethodPost, "/", map[string]string{"guid": gid}, url.Values{"name": {"B\r\nEMAIL:z@y.example"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("rename: %d", rr.Code)
	}
	check("rename", gid)
}

func TestAddToContactGroupUnknownContactMessage(t *testing.T) {
	e := newGroupEnv(t)
	gid := e.newGroup(t, "alice", "Team", "")
	rr := e.add("alice", g32("9"), gid)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), "Contact not found") {
		t.Errorf("%d %q, want 404 with the contact-not-found message", rr.Code, rr.Body.String())
	}
}

// A member card whose UID would start another property gets a fresh UID, and
// the group names that, not the unsafe text.
func TestContactGroupAddMemberWithUnsafeUID(t *testing.T) {
	e := newGroupEnv(t)
	const u = "alice"
	bad := &bongov1.Contact{Guid: g32("a"), DisplayName: "Ann", Email: "ann@example.com",
		VcardRaw: []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ann\r\nUID:abc\\nEMAIL:evil@example.com\r\nEND:VCARD\r\n")}
	if _, err := e.contacts.Create(u, bad); err != nil {
		t.Fatal(err)
	}
	gid := e.newGroup(t, u, "Team", "")
	if rr := e.add(u, g32("a"), gid); rr.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", rr.Code, rr.Body.String())
	}
	g, _ := e.contacts.Get(u, gid)
	if strings.Contains(string(g.VcardRaw), "evil") || strings.Contains(string(g.VcardRaw), "abc") || len(g.Members) != 1 {
		t.Errorf("group:\n%s", g.VcardRaw)
	}
	ann, _ := e.contacts.Get(u, g32("a"))
	if strings.Contains(string(ann.VcardRaw), "abc") {
		t.Errorf("the member's unsafe UID was kept:\n%s", ann.VcardRaw)
	}
}
