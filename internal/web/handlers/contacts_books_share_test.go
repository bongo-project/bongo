package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
)

// bookShareEnv is the group share env plus real address-book and contact
// stores over the same Pebble, and the owner's and grantee's handlers. alice
// owns the book; bob, carol (disabled), dave (other domain) are the others.
// The appliers dispatch grant and group commands, so nothing is stored as a
// literal key.
type bookShareEnv struct {
	*groupShareEnv
	books    *store.AddressBookStore
	contacts *store.ContactStore
	ch       *ContactsHandler
	bh       *BookShareHandler
	resolver *countingResolver
	book     string // alice's book "Clients"
	bobBook  string // bob's book
}

func newBookShareEnv(t *testing.T) *bookShareEnv {
	t.Helper()
	e := &bookShareEnv{groupShareEnv: newGroupShareEnv(t)}
	app := &directApplierTags{ps: e.ps}
	e.books = store.NewAddressBookStore(e.ps, app)
	e.contacts = store.NewContactStore(e.ps, app)
	e.ch = NewContactsHandler(e.contacts, nil)
	e.ch.SetAddressBooks(e.books)
	e.ch.SetACL(e.acl)
	e.ch.now = func() time.Time { return e.now }
	e.h.SetAddressBooks(e.books)

	e.resolver = &countingResolver{inner: e.h.shares}
	e.bh = NewBookShareHandler(e.acl, e.books)
	e.bh.now = func() time.Time { return e.now }
	e.bh.SetContacts(e.ch)
	e.bh.SetShareResolver(e.resolver)
	e.bh.SetGroups(e.groups)
	e.bh.SetShareInviter(e.inviter)
	e.bh.SetAuthservID("mail.d.test")
	// One budget with the calendar form, the folder form and IMAP.
	e.bh.SetShareLimiter(e.h.ShareLimiter())

	e.book = store.NewAddressBookID()
	if _, err := e.books.Create("alice", e.book, "Clients", ""); err != nil {
		t.Fatal(err)
	}
	e.bobBook = store.NewAddressBookID()
	if _, err := e.books.Create("bob", e.bobBook, "Bobs", ""); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *bookShareEnv) share(who, book, target, level string) *httptest.ResponseRecorder {
	form := url.Values{"share_with": {target}}
	if level != "" {
		form.Set("level", level)
	}
	rr := httptest.NewRecorder()
	e.bh.ShareBook(rr, withRoute(sFormReq(http.MethodPost, "/contacts/books/"+book+"/share", form), who, map[string]string{"id": book}))
	return rr
}

func (e *bookShareEnv) unshare(who, book, entry string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.bh.UnshareBook(rr, withRoute(sFormReq(http.MethodPost, "/contacts/books/"+book+"/unshare",
		url.Values{"share_with": {entry}}), who, map[string]string{"id": book}))
	return rr
}

func (e *bookShareEnv) sharingPage(who, book string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.bh.ShowSharing(rr, withRoute(httptest.NewRequest(http.MethodGet, "/contacts/books/"+book+"/sharing", nil), who, map[string]string{"id": book}))
	return rr
}

// grantOn returns the grant on alice's book for the typed grantee, or nil.
func (e *bookShareEnv) grantOn(t *testing.T, grantee string) (state, level string, ok bool) {
	t.Helper()
	g, err := e.acl.GetGrant(store.ACLKindAddressBook, "alice", e.book, grantee)
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		return "", "", false
	}
	return g.State, g.Level, true
}

func (e *bookShareEnv) anyGrants(t *testing.T, owner, book string) int {
	t.Helper()
	gs, err := e.acl.ListResourceGrants(store.ACLKindAddressBook, owner, book)
	if err != nil {
		t.Fatal(err)
	}
	return len(gs)
}

func TestShareBookPendingInvited(t *testing.T) {
	for _, level := range []string{store.GrantLevelRead, store.GrantLevelWrite} {
		t.Run(level, func(t *testing.T) {
			e := newBookShareEnv(t)
			rr := e.share("alice", e.book, "bob@d.test", level)
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("status = %d %s", rr.Code, rr.Body.String())
			}
			if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/contacts/books/"+e.book+"/sharing") {
				t.Errorf("redirect = %q", loc)
			}
			state, got, ok := e.grantOn(t, store.GranteeUser("bob"))
			if !ok || state != store.GrantStatePending || got != level {
				t.Fatalf("grant = %q %q %v, want pending %q", state, got, ok, level)
			}
			// A pending grant authorises nothing.
			if lvl, err := e.acl.AddressBookLevel("alice", e.book, "bob"); err != nil || lvl != "" {
				t.Errorf("pending level = %q, %v", lvl, err)
			}
			e.drain()
			if e.sender.count() != 1 {
				t.Fatalf("invitations sent = %d, want 1", e.sender.count())
			}
			if raw := string(e.sender.sent[0].raw); !strings.Contains(raw, "Clients") {
				t.Errorf("invitation does not name the book")
			}
		})
	}
}

func TestShareBookGroupAccepted(t *testing.T) {
	e := newBookShareEnv(t)
	if rr := e.share("alice", e.book, "team@d.test", store.GrantLevelWrite); rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d %s", rr.Code, rr.Body.String())
	}
	state, level, ok := e.grantOn(t, store.GranteeGroup(e.teamID))
	if !ok || state != store.GrantStateAccepted || level != store.GrantLevelWrite {
		t.Fatalf("group grant = %q %q %v", state, level, ok)
	}
	e.drain()
	if n := e.sender.count(); n != 0 {
		t.Errorf("a group share sent %d mails", n)
	}
	// A member holds the level at once, a non-member does not.
	if lvl, _ := e.acl.AddressBookLevel("alice", e.book, "bob"); lvl != store.GrantLevelWrite {
		t.Errorf("member level = %q", lvl)
	}
	if lvl, _ := e.acl.AddressBookLevel("alice", e.book, "dave"); lvl != "" {
		t.Errorf("non-member level = %q", lvl)
	}
}

// Every negative resolution is one fixed answer, so the form is no oracle for
// which addresses are accounts, and none of them writes.
func TestShareBookRefusalsAreUniform(t *testing.T) {
	e := newBookShareEnv(t)
	targets := map[string]string{
		"unknown":          "nobody@d.test",
		"self":             "alice@d.test",
		"self alias":       "alice.alias@d.test",
		"disabled":         "carol@d.test",
		"external":         "someone@elsewhere.test",
		"other-domain grp": "crew@ca.test",
		"malformed":        "not an address",
		"display name":     "Bob <bob@d.test>",
	}
	want := i18n.T("en", "contacts.errShareRefused")
	for name, target := range targets {
		rr := e.share("alice", e.book, target, "read")
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d %s", name, rr.Code, rr.Body.String())
		}
		if got := strings.TrimSpace(rr.Body.String()); got != want {
			t.Errorf("%s: body %q, want %q", name, got, want)
		}
	}
	if n := e.anyGrants(t, "alice", e.book); n != 0 {
		t.Errorf("refused shares wrote %d grants", n)
	}
}

func TestShareBookOtherDomainGroupSameRefusal(t *testing.T) {
	e := newBookShareEnv(t)
	group := e.share("alice", e.book, "crew@ca.test", "read")
	unknown := e.share("alice", e.book, "nobody@d.test", "read")
	if group.Code != http.StatusBadRequest || group.Code != unknown.Code || group.Body.String() != unknown.Body.String() {
		t.Fatalf("other-domain group = %d %q, unknown = %d %q", group.Code, group.Body.String(), unknown.Code, unknown.Body.String())
	}
	if _, _, ok := e.grantOn(t, store.GranteeGroup(e.crewID)); ok {
		t.Error("a group of another domain was granted the book")
	}
}

// The limiter is the calendar handler's, so one hourly budget covers the
// calendar form, the folder form, IMAP and this form: exhausting it there
// refuses here before the address resolves.
func TestShareBookLimiterSharedWithCalendar(t *testing.T) {
	e := newBookShareEnv(t)
	for range 20 {
		e.h.ShareLimiter().Allow("alice")
	}
	rr := e.share("alice", e.book, "bob@d.test", "read")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d %s, want 429", rr.Code, rr.Body.String())
	}
	if secs, err := strconv.Atoi(rr.Header().Get("Retry-After")); err != nil || secs < 1 {
		t.Errorf("Retry-After = %q", rr.Header().Get("Retry-After"))
	}
	if e.resolver.calls != 0 {
		t.Errorf("ResolveAccount called %d times after the limit", e.resolver.calls)
	}
	if n := e.anyGrants(t, "alice", e.book); n != 0 {
		t.Errorf("a limited share wrote %d grants", n)
	}
	// A share through this form spends the calendar's budget too.
	e2 := newBookShareEnv(t)
	for range 19 {
		e2.h.ShareLimiter().Allow("alice")
	}
	if rr := e2.share("alice", e2.book, "bob@d.test", "read"); rr.Code != http.StatusSeeOther {
		t.Fatalf("19 spent, share = %d", rr.Code)
	}
	if rr := e2.share("alice", e2.book, "bob@d.test", "read"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("20 spent, share = %d, want 429", rr.Code)
	}
}

func TestShareBookInvalidLevelBeforeResolve(t *testing.T) {
	for _, level := range []string{"", "admin", "freebusy", "full", "READ", "read "} {
		t.Run("level="+level, func(t *testing.T) {
			e := newBookShareEnv(t)
			rr := e.share("alice", e.book, "bob@d.test", level)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d %s, want 400", rr.Code, rr.Body.String())
			}
			if want := i18n.T("en", "contacts.errShareInvalidLevel"); strings.TrimSpace(rr.Body.String()) != want {
				t.Errorf("body = %q, want %q", rr.Body.String(), want)
			}
			if e.resolver.calls != 0 {
				t.Errorf("ResolveAccount called %d times before the level was validated", e.resolver.calls)
			}
			if n := e.anyGrants(t, "alice", e.book); n != 0 {
				t.Errorf("wrote %d grants", n)
			}
		})
	}
}

func TestShareBookEmptyShareWith(t *testing.T) {
	e := newBookShareEnv(t)
	rr := e.share("alice", e.book, "  ", "read")
	if rr.Code != http.StatusBadRequest || strings.TrimSpace(rr.Body.String()) != i18n.T("en", "contacts.errShareWithRequired") {
		t.Fatalf("status = %d %q", rr.Code, rr.Body.String())
	}
	if e.resolver.calls != 0 {
		t.Error("resolved an empty address")
	}
}

// A full book answers the one refusal for a valid and an unknown address
// alike, so it is no oracle for which addresses are accounts.
func TestShareBookFullSameRefusal(t *testing.T) {
	e := newBookShareEnv(t)
	for i := range store.MaxGrantsPerResource {
		filler := "filler-" + strconv.Itoa(i)
		if err := e.acl.SetGrant(store.ACLKindAddressBook, "alice", e.book, store.GranteeUser(filler), store.GrantLevelRead, "", store.GrantStateAccepted, "alice", e.now); err != nil {
			t.Fatal(err)
		}
	}
	valid := e.share("alice", e.book, "bob@d.test", "read")
	unknown := e.share("alice", e.book, "nobody@d.test", "read")
	want := i18n.T("en", "contacts.errShareRefused")
	for name, rr := range map[string]*httptest.ResponseRecorder{"valid": valid, "unknown": unknown} {
		if rr.Code != http.StatusBadRequest || strings.TrimSpace(rr.Body.String()) != want {
			t.Errorf("%s: %d %q, want 400 %q", name, rr.Code, rr.Body.String(), want)
		}
	}
	if _, _, ok := e.grantOn(t, store.GranteeUser("bob")); ok {
		t.Error("a full book took another grantee")
	}
	if n := e.anyGrants(t, "alice", e.book); n != store.MaxGrantsPerResource {
		t.Errorf("grants = %d, want %d", n, store.MaxGrantsPerResource)
	}
}

// The default book, another user's book and a made-up book are all one 404,
// and nothing is written for any of them.
func TestShareBookDefaultAndForeign404(t *testing.T) {
	e := newBookShareEnv(t)
	want := i18n.T("en", "contacts.errBookNotFound")
	for name, book := range map[string]string{
		"default": store.DefaultAddressBookID,
		"foreign": e.bobBook,
		"unknown": store.NewAddressBookID(),
		"invalid": "../x",
		"empty":   "",
	} {
		rr := e.share("alice", book, "bob@d.test", "read")
		if rr.Code != http.StatusNotFound || strings.TrimSpace(rr.Body.String()) != want {
			t.Errorf("%s: %d %q, want 404 %q", name, rr.Code, rr.Body.String(), want)
		}
		rr = e.unshare("alice", book, "bob")
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s unshare: %d", name, rr.Code)
		}
		if rr := e.sharingPage("alice", book); rr.Code != http.StatusNotFound {
			t.Errorf("%s page: %d", name, rr.Code)
		}
	}
	if e.resolver.calls != 0 {
		t.Errorf("a refused book resolved %d addresses", e.resolver.calls)
	}
	for _, c := range []struct{ owner, book string }{
		{"alice", e.bobBook}, {"bob", e.bobBook}, {"alice", store.DefaultAddressBookID}, {"alice", e.book},
	} {
		if n := e.anyGrants(t, c.owner, c.book); n != 0 {
			t.Errorf("grants on %s/%s = %d, want 0", c.owner, c.book, n)
		}
	}
}

func TestUnshareBook(t *testing.T) {
	e := newBookShareEnv(t)
	for _, target := range []string{"bob@d.test", "team@d.test"} {
		if rr := e.share("alice", e.book, target, "read"); rr.Code != http.StatusSeeOther {
			t.Fatalf("share %s = %d", target, rr.Code)
		}
	}
	// A user who is not the owner cannot unshare: the book is not theirs.
	if rr := e.unshare("bob", e.book, "bob"); rr.Code != http.StatusNotFound {
		t.Errorf("grantee unshare = %d, want 404", rr.Code)
	}
	if _, _, ok := e.grantOn(t, store.GranteeUser("bob")); !ok {
		t.Fatal("a non-owner removed a grant")
	}
	// A grant that is not there writes nothing and goes back to the page.
	rr := e.unshare("alice", e.book, "dave")
	if rr.Code != http.StatusSeeOther {
		t.Errorf("absent grant = %d", rr.Code)
	}
	if n := e.anyGrants(t, "alice", e.book); n != 2 {
		t.Fatalf("grants = %d, want 2", n)
	}
	if rr := e.unshare("alice", e.book, ""); rr.Code != http.StatusBadRequest {
		t.Errorf("empty share_with = %d", rr.Code)
	}
	if rr := e.unshare("alice", e.book, "bob"); rr.Code != http.StatusSeeOther {
		t.Fatalf("unshare user = %d %s", rr.Code, rr.Body.String())
	}
	if _, _, ok := e.grantOn(t, store.GranteeUser("bob")); ok {
		t.Error("the user grant survives")
	}
	if rr := e.unshare("alice", e.book, "g:"+e.teamID); rr.Code != http.StatusSeeOther {
		t.Fatalf("unshare group = %d %s", rr.Code, rr.Body.String())
	}
	if n := e.anyGrants(t, "alice", e.book); n != 0 {
		t.Errorf("grants left = %d", n)
	}
	// Unsharing is not rate limited: it reveals nothing about accounts.
	for range 25 {
		e.h.ShareLimiter().Allow("alice")
	}
	if rr := e.unshare("alice", e.book, "bob"); rr.Code != http.StatusSeeOther {
		t.Errorf("unshare under an exhausted limiter = %d", rr.Code)
	}
}

func TestBookSharingPageListsGrants(t *testing.T) {
	e := newBookShareEnv(t)
	e.share("alice", e.book, "bob@d.test", "read")
	e.share("alice", e.book, "team@d.test", "write")
	rr := e.sharingPage("alice", e.book)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Clients", "bob@d.test", "team@d.test", `data-grant-state="pending"`, `data-grant-state="accepted"`,
		"/contacts/books/" + e.book + "/share", "/contacts/books/" + e.book + "/unshare"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Nobody else sees the page of a book that is not theirs.
	if rr := e.sharingPage("bob", e.book); rr.Code != http.StatusNotFound {
		t.Errorf("another user's sharing page = %d, want 404", rr.Code)
	}
}
