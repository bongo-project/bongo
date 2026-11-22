package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Guids of the contacts the shared tests use, all 32 hex.
var (
	sharedAnn     = g32("a") // in the shared book
	sharedGroup   = g32("c") // contact group in the shared book
	sharedOther   = g32("d") // in alice's second book
	sharedPrivate = g32("e") // in alice's default book
)

// seedSharedContacts files contacts in alice's shared book, a second book and
// the default book, and returns the second book's id.
func (e *bookShareEnv) seedSharedContacts(t *testing.T) (book2 string) {
	t.Helper()
	book2 = store.NewAddressBookID()
	if _, err := e.books.Create("alice", book2, "Private", ""); err != nil {
		t.Fatal(err)
	}
	person := func(guid, name, email, book string) {
		c := &bongov1.Contact{Guid: guid, DisplayName: name, Email: email, AddressbookId: book}
		syncQuickFieldsToVCard(c)
		if _, err := e.contacts.Create("alice", c); err != nil {
			t.Fatal(err)
		}
	}
	person(sharedAnn, "Ann Client", "ann@client.test", e.book)
	person(sharedOther, "Otto Other", "otto@private.test", book2)
	person(sharedPrivate, "Pat Private", "pat@private.test", "")
	raw, err := store.NewContactGroupCard("Clients Team", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"urn:uuid:" + sharedAnn, "mailto:solo@client.test"} {
		if raw, err = store.AddContactGroupMember(raw, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.contacts.Create("alice", &bongov1.Contact{Guid: sharedGroup, DisplayName: "Clients Team", VcardRaw: raw, AddressbookId: e.book}); err != nil {
		t.Fatal(err)
	}
	return book2
}

func (e *bookShareEnv) listShared(who, owner, book string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.ch.SharedList(rr, withRoute(httptest.NewRequest(http.MethodGet, "/shared/contacts/"+owner+"/"+book, nil), who,
		map[string]string{"owner": owner, "book": book}))
	return rr
}

func (e *bookShareEnv) showShared(who, owner, book, guid string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.ch.SharedShow(rr, withRoute(httptest.NewRequest(http.MethodGet, "/shared/contacts/"+owner+"/"+book+"/"+guid, nil), who,
		map[string]string{"owner": owner, "book": book, "guid": guid}))
	return rr
}

// mainOf returns the page's main content, which is what the read-only claim is
// about: the layout and the sidebar carry the viewer's own forms and links.
func mainOf(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "<main")
	end := strings.Index(body, "</main>")
	if start < 0 || end < start {
		t.Fatalf("no <main> in page: %.300q", body)
	}
	return body[start:end]
}

func TestSharedBookRoutesRequireGrant(t *testing.T) {
	old := func(e *bookShareEnv) time.Time { return e.now.Add(-(store.PendingGrantTTL + time.Hour)) }
	cases := []struct {
		name  string
		setup func(t *testing.T, e *bookShareEnv)
		who   string
		owner string
		book  func(e *bookShareEnv) string
	}{
		{"no grant", func(*testing.T, *bookShareEnv) {}, "bob", "alice", nil},
		{"pending", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStatePending, e.now)
		}, "bob", "alice", nil},
		{"expired pending", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStatePending, old(e))
		}, "bob", "alice", nil},
		{"revoked", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
			if err := e.acl.RevokeGrant(store.ACLKindAddressBook, "alice", e.book, store.GranteeUser("bob")); err != nil {
				t.Fatal(err)
			}
		}, "bob", "alice", nil},
		{"own book", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
		}, "alice", "alice", nil},
		{"own book through a group", func(t *testing.T, e *bookShareEnv) {
			if err := e.acl.SetGrant(store.ACLKindAddressBook, "alice", e.book, store.GranteeGroup(e.teamID), store.GrantLevelRead, "", store.GrantStateAccepted, "alice", e.now); err != nil {
				t.Fatal(err)
			}
			// alice joins the group: a grant must not make the owner a grantee.
			if _, err := e.groups.UpdateGroup(&bongov1.Group{Id: e.teamID, MemberIds: []string{"alice"}}); err != nil {
				t.Fatal(err)
			}
		}, "alice", "alice", nil},
		{"other book of the owner", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
		}, "bob", "alice", func(e *bookShareEnv) string { return e.seedBook2(t) }},
		{"group the user left", func(t *testing.T, e *bookShareEnv) {
			if err := e.acl.SetGrant(store.ACLKindAddressBook, "alice", e.book, store.GranteeGroup(e.teamID), store.GrantLevelRead, "", store.GrantStateAccepted, "alice", e.now); err != nil {
				t.Fatal(err)
			}
			if _, err := e.groups.UpdateGroup(&bongov1.Group{Id: e.teamID, MemberIds: []string{}}); err != nil {
				t.Fatal(err)
			}
		}, "bob", "alice", nil},
		{"deleted book with a live grant", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
			if err := e.books.Delete("alice", e.book); err != nil {
				t.Fatal(err)
			}
		}, "bob", "alice", nil},
		{"wrong owner", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
		}, "bob", "dave", nil},
		{"default book", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
		}, "bob", "alice", func(*bookShareEnv) string { return store.DefaultAddressBookID }},
		{"malformed book", func(*testing.T, *bookShareEnv) {}, "bob", "alice", func(*bookShareEnv) string { return "../etc" }},
		{"upper case book", func(t *testing.T, e *bookShareEnv) {
			e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
		}, "bob", "alice", func(e *bookShareEnv) string { return strings.ToUpper(e.book) }},
		{"malformed owner", func(*testing.T, *bookShareEnv) {}, "bob", "al/ice", nil},
		{"empty owner", func(*testing.T, *bookShareEnv) {}, "bob", "", nil},
	}
	routes := []struct {
		name string
		call func(e *bookShareEnv, who, owner, book string) *httptest.ResponseRecorder
	}{
		{"list", func(e *bookShareEnv, who, owner, book string) *httptest.ResponseRecorder {
			return e.listShared(who, owner, book)
		}},
		{"show", func(e *bookShareEnv, who, owner, book string) *httptest.ResponseRecorder {
			return e.showShared(who, owner, book, sharedAnn)
		}},
	}
	for _, rt := range routes {
		for _, tc := range cases {
			t.Run(rt.name+"/"+tc.name, func(t *testing.T) {
				e := newBookShareEnv(t)
				e.seedSharedContacts(t)
				tc.setup(t, e)
				book := e.book
				if tc.book != nil {
					book = tc.book(e)
				}
				rr := rt.call(e, tc.who, tc.owner, book)
				if rr.Code != http.StatusNotFound {
					t.Fatalf("status = %d %.200q, want 404", rr.Code, rr.Body.String())
				}
				if got := strings.TrimSpace(rr.Body.String()); got != sharedBookNotFound {
					t.Errorf("body = %q, want the constant %q", got, sharedBookNotFound)
				}
			})
		}
	}
}

// seedBook2 returns the id of alice's second book, creating it when needed.
func (e *bookShareEnv) seedBook2(t *testing.T) string {
	t.Helper()
	books, err := e.books.List("alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range books {
		if b.Name == "Private" {
			return b.Id
		}
	}
	id := store.NewAddressBookID()
	if _, err := e.books.Create("alice", id, "Private", ""); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSharedBookReadGrantRendersReadOnly(t *testing.T) {
	e := newBookShareEnv(t)
	e.seedSharedContacts(t)
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)

	for name, rr := range map[string]*httptest.ResponseRecorder{
		"list": e.listShared("bob", "alice", e.book),
		"show": e.showShared("bob", "alice", e.book, sharedAnn),
	} {
		if rr.Code != http.StatusOK {
			t.Fatalf("%s: status = %d %.200q", name, rr.Code, rr.Body.String())
		}
		body := rr.Body.String()
		content := mainOf(t, body)
		if !strings.Contains(content, "Ann Client") {
			t.Errorf("%s: the contact is not shown", name)
		}
		for _, bad := range []string{"<form", "<button", `"/contacts`, "/edit", "hx-post", "hx-delete", "contenteditable", "<input", "<textarea"} {
			if strings.Contains(content, bad) {
				t.Errorf("%s: read-only content contains %q", name, bad)
			}
		}
		// Nothing of the owner's other books, the default book or their groups' book leaks.
		for _, leak := range []string{"Otto Other", "Pat Private", "otto@private.test"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: page leaks %q", name, leak)
			}
		}
		if !strings.Contains(content, "Clients") {
			t.Errorf("%s: the book is not named", name)
		}
	}
	list := mainOf(t, e.listShared("bob", "alice", e.book).Body.String())
	if !strings.Contains(list, "/shared/contacts/alice/"+e.book+"/"+sharedAnn) {
		t.Error("list rows do not link to the shared contact page")
	}
	// A write grant gets the same read-only web view: writes are CardDAV only.
	e2 := newBookShareEnv(t)
	e2.seedSharedContacts(t)
	e2.grant(t, store.ACLKindAddressBook, e2.book, "bob", store.GrantLevelWrite, "", store.GrantStateAccepted, e2.now)
	content := mainOf(t, e2.showShared("bob", "alice", e2.book, sharedAnn).Body.String())
	for _, bad := range []string{"<form", `"/contacts`, "/edit"} {
		if strings.Contains(content, bad) {
			t.Errorf("write grant: read-only content contains %q", bad)
		}
	}
}

// A contact filed in another book, in the default book, or not there at all
// answers the same 404 as a book the viewer cannot see.
func TestSharedBookContactInAnotherBook404(t *testing.T) {
	e := newBookShareEnv(t)
	e.seedSharedContacts(t)
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
	for name, guid := range map[string]string{
		"other book":   sharedOther,
		"default book": sharedPrivate,
		"missing":      g32("f"),
		"malformed":    "../x",
		"empty":        "",
	} {
		rr := e.showShared("bob", "alice", e.book, guid)
		if rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != sharedBookNotFound {
			t.Errorf("%s: %d %.100q, want the constant 404", name, rr.Code, rr.Body.String())
		}
	}
	if rr := e.showShared("bob", "alice", e.book, sharedAnn); rr.Code != http.StatusOK {
		t.Fatalf("the shared contact itself = %d", rr.Code)
	}
}

func TestSharedBookGroupCardListsMembersByName(t *testing.T) {
	e := newBookShareEnv(t)
	e.seedSharedContacts(t)
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
	rr := e.showShared("bob", "alice", e.book, sharedGroup)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	content := mainOf(t, rr.Body.String())
	for _, want := range []string{"Clients Team", "Ann Client", "solo@client.test"} {
		if !strings.Contains(content, want) {
			t.Errorf("group card lacks %q", want)
		}
	}
	for _, bad := range []string{"<form", `"/contacts`, "contact_group", `<a href="/compose`} {
		if strings.Contains(content, bad) {
			t.Errorf("group card contains %q", bad)
		}
	}
}

func TestSharedBookRevokeTakesEffectNextRequest(t *testing.T) {
	e := newBookShareEnv(t)
	e.seedSharedContacts(t)
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
	if rr := e.listShared("bob", "alice", e.book); rr.Code != http.StatusOK {
		t.Fatalf("before = %d", rr.Code)
	}
	if err := e.acl.RevokeGrant(store.ACLKindAddressBook, "alice", e.book, store.GranteeUser("bob")); err != nil {
		t.Fatal(err)
	}
	if rr := e.listShared("bob", "alice", e.book); rr.Code != http.StatusNotFound {
		t.Fatalf("after revoke = %d, want 404", rr.Code)
	}
	if rr := e.showShared("bob", "alice", e.book, sharedAnn); rr.Code != http.StatusNotFound {
		t.Fatalf("show after revoke = %d, want 404", rr.Code)
	}

	// A group grant ends when the member leaves the group.
	if err := e.acl.SetGrant(store.ACLKindAddressBook, "alice", e.book, store.GranteeGroup(e.teamID), store.GrantLevelRead, "", store.GrantStateAccepted, "alice", e.now); err != nil {
		t.Fatal(err)
	}
	if rr := e.listShared("bob", "alice", e.book); rr.Code != http.StatusOK {
		t.Fatalf("group member = %d", rr.Code)
	}
	if _, err := e.groups.UpdateGroup(&bongov1.Group{Id: e.teamID, MemberIds: []string{}}); err != nil {
		t.Fatal(err)
	}
	if rr := e.listShared("bob", "alice", e.book); rr.Code != http.StatusNotFound {
		t.Fatalf("after leaving the group = %d, want 404", rr.Code)
	}
}

// A disabled owner's shared book stays reachable, as shared calendars do.
func TestSharedBookDisabledOwnerStaysReachable(t *testing.T) {
	e := newBookShareEnv(t)
	e.seedSharedContacts(t)
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStateAccepted, e.now)
	data, _ := proto.Marshal(&bongov1.UserProfile{Email: "alice@d.test", Disabled: true})
	if err := e.ps.Set([]byte("/user/alice/profile"), data); err != nil {
		t.Fatal(err)
	}
	if rr := e.listShared("bob", "alice", e.book); rr.Code != http.StatusOK {
		t.Fatalf("disabled owner = %d, want 200", rr.Code)
	}
}

func TestContactsSidebarListsSharedBooks(t *testing.T) {
	e := newBookShareEnv(t)
	e.seedSharedContacts(t)
	page := func(who string) string {
		rr := httptest.NewRecorder()
		e.ch.ListContacts(rr, withRoute(httptest.NewRequest(http.MethodGet, "/contacts", nil), who, nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
		return rr.Body.String()
	}
	link := "/shared/contacts/alice/" + e.book
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStatePending, e.now)
	if body := page("bob"); strings.Contains(body, link) || strings.Contains(body, "Clients") {
		t.Error("a pending share is listed in the sidebar")
	}
	if err := e.acl.AcceptGrant(store.ACLKindAddressBook, "alice", e.book, store.GranteeUser("bob"), e.now); err != nil {
		t.Fatal(err)
	}
	body := page("bob")
	if !strings.Contains(body, link) || !strings.Contains(body, "Clients") {
		t.Error("an accepted share is not listed in the sidebar")
	}
	if strings.Contains(page("dave"), link) {
		t.Error("another user's sidebar lists the share")
	}
	if strings.Contains(page("alice"), link) {
		t.Error("the owner's sidebar lists their own book as shared")
	}
	// A deleted book drops out.
	if err := e.books.Delete("alice", e.book); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page("bob"), link) {
		t.Error("a deleted book is still listed")
	}
}

// /shares names an address book by its name, and a deleted book has no row.
func TestSharesPageNamesAddressBook(t *testing.T) {
	e := newBookShareEnv(t)
	e.grant(t, store.ACLKindAddressBook, e.book, "bob", store.GrantLevelRead, "", store.GrantStatePending, e.now)
	body := e.page("bob")
	if !strings.Contains(body, "Clients") {
		t.Fatalf("the pending row does not name the book")
	}
	if strings.Contains(body, e.book+"</span>") || strings.Contains(body, "Address book "+e.book) {
		t.Error("the row shows the raw id")
	}
	if rr := e.post("accept", "bob", url.Values{"kind": {"addressbook"}, "owner": {"alice"}, "resource": {e.book}}); rr.Code != http.StatusOK {
		t.Fatalf("accept = %d %s", rr.Code, rr.Body.String())
	}
	if body := e.page("bob"); !strings.Contains(body, "Clients") || !strings.Contains(body, "/shares/leave") {
		t.Error("the joined row does not name the book")
	}
	if err := e.books.Delete("alice", e.book); err != nil {
		t.Fatal(err)
	}
	if body := e.page("bob"); strings.Contains(body, "Clients") || strings.Contains(body, e.book) {
		t.Error("a deleted book still has a row")
	}
}
