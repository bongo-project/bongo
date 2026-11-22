package agent

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/store"
)

// TestShareAddressBookThroughRouter drives address-book sharing through the
// production router of a server-role agent (the dev role does not enforce
// CSRF): registration, CSRF on the writes, the session user, the 404 for
// another user's book, the shared limiter, the read-only shared pages and
// autocomplete.
func TestShareAddressBookThroughRouter(t *testing.T) {
	cfg := peerRunConfig(t, "127.0.0.1", "127.0.0.1")
	a, stop := runAgentUntilReady(t, cfg)
	defer stop()

	const password = "pass-1234-xyz"
	aliceID, err := auth.CreateUser(a, a.Metadata, "alice@localhost", "Alice", password, true)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	bobID, err := auth.CreateUser(a, a.Metadata, "bob@localhost", "Bob", password, false)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.Web.Port)

	session := func(email string) (*http.Client, string) {
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar, Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.PostForm(base+"/login", url.Values{"email": {email}, "password": {password}})
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		page, err := c.Get(base + "/settings/vacation")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(page.Body)
		_ = page.Body.Close()
		m := csrfHeaderRe.FindSubmatch(body)
		if page.StatusCode != http.StatusOK || m == nil {
			t.Fatalf("page status %d, token found: %v", page.StatusCode, m != nil)
		}
		return c, string(m[1])
	}
	alice, aliceTok := session("alice@localhost")
	bob, bobTok := session("bob@localhost")

	type result struct {
		code int
		body string
		loc  string
	}
	post := func(c *http.Client, tok, path string, form url.Values) result {
		req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if tok != "" {
			req.Header.Set("X-CSRF-Token", tok)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return result{resp.StatusCode, string(b), resp.Header.Get("Location")}
	}
	get := func(c *http.Client, path string) result {
		resp, err := c.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return result{resp.StatusCode, string(b), resp.Header.Get("Location")}
	}
	want := func(r result, code int, what string) {
		t.Helper()
		if r.code != code {
			t.Fatalf("%s: status %d, want %d; body %.200q", what, r.code, code, r.body)
		}
	}

	book := store.NewAddressBookID()
	if _, err := a.AddressBooks.Create(aliceID, book, "Clients", ""); err != nil {
		t.Fatal(err)
	}
	bobBook := store.NewAddressBookID()
	if _, err := a.AddressBooks.Create(bobID, bobBook, "Bobs", ""); err != nil {
		t.Fatal(err)
	}
	want(post(alice, aliceTok, "/contacts", url.Values{"display_name": {"Zelda Client"}, "email": {"zelda@client.example"}, "addressbook": {book}}), http.StatusSeeOther, "create contact")
	filed, _, _ := a.Contacts.ListByBook(aliceID, book, 0, 0)
	if len(filed) != 1 {
		t.Fatalf("book holds %d contacts", len(filed))
	}
	guid := filed[0].Guid

	// CSRF: every write is refused without a token, and nothing is written.
	for _, p := range []string{"/contacts/books/" + book + "/share", "/contacts/books/" + book + "/unshare"} {
		r := post(alice, "", p, url.Values{"share_with": {"bob@localhost"}, "level": {"read"}})
		if r.code != http.StatusForbidden || !strings.Contains(r.body, csrfBodyText) {
			t.Errorf("%s without a token: %d %q, want 403 %q", p, r.code, r.body, csrfBodyText)
		}
	}
	if gs, _ := a.ACL.ListResourceGrants(store.ACLKindAddressBook, aliceID, book); len(gs) != 0 {
		t.Fatalf("a refused request wrote %d grants", len(gs))
	}

	// Another user's book, and the default book, are 404 and write nothing.
	want(post(alice, aliceTok, "/contacts/books/"+bobBook+"/share", url.Values{"share_with": {"bob@localhost"}, "level": {"read"}}), http.StatusNotFound, "foreign book")
	want(post(alice, aliceTok, "/contacts/books/default/share", url.Values{"share_with": {"bob@localhost"}, "level": {"read"}}), http.StatusNotFound, "default book")
	want(get(bob, "/contacts/books/"+book+"/sharing"), http.StatusNotFound, "foreign sharing page")
	want(get(alice, "/contacts/books/default/sharing"), http.StatusNotFound, "default sharing page")
	want(get(alice, "/contacts/books/"+book+"/sharing"), http.StatusOK, "own sharing page")
	if list := get(alice, "/contacts?book="+book); !strings.Contains(list.body, "/contacts/books/"+book+"/sharing") {
		t.Error("the book page has no Sharing link")
	}

	// Before the share: the shared pages are 404 for bob and for the owner.
	sharedPath := "/shared/contacts/" + aliceID + "/" + book
	want(get(bob, sharedPath), http.StatusNotFound, "shared list without a grant")
	want(get(alice, sharedPath), http.StatusNotFound, "shared list as the owner")

	// Share: pending, so still nothing for bob until he accepts.
	r := post(alice, aliceTok, "/contacts/books/"+book+"/share", url.Values{"share_with": {"bob@localhost"}, "level": {"read"}})
	want(r, http.StatusSeeOther, "share")
	if !strings.HasPrefix(r.loc, "/contacts/books/"+book+"/sharing") {
		t.Errorf("redirect = %q", r.loc)
	}
	want(get(bob, sharedPath), http.StatusNotFound, "shared list while pending")
	if body := get(bob, "/shares").body; !strings.Contains(body, "Clients") {
		t.Errorf("the /shares page does not name the book")
	}
	if got := get(bob, "/api/gal?q=zelda"); strings.Contains(got.body, "zelda@client.example") {
		t.Errorf("autocomplete offers a pending share: %s", got.body)
	}

	want(post(bob, bobTok, "/shares/accept", url.Values{"kind": {"addressbook"}, "owner": {aliceID}, "resource": {book}}), http.StatusOK, "accept")
	list := get(bob, sharedPath)
	want(list, http.StatusOK, "shared list")
	if !strings.Contains(list.body, "Zelda Client") || strings.Contains(list.body, "/contacts/"+guid+"/edit") {
		t.Errorf("shared list body is wrong")
	}
	want(get(bob, sharedPath+"/"+guid), http.StatusOK, "shared contact")
	want(get(bob, sharedPath+"/"+strings.Repeat("f", 32)), http.StatusNotFound, "missing shared contact")
	// The owner's own routes stay the owner's: bob cannot read the contact there.
	want(get(bob, "/contacts/"+guid), http.StatusNotFound, "owner's contact page as the grantee")
	if side := get(bob, "/contacts"); !strings.Contains(side.body, sharedPath) {
		t.Error("the contacts sidebar does not list the share")
	}
	if got := get(bob, "/api/gal?q=zelda"); !strings.Contains(got.body, "zelda@client.example") {
		t.Errorf("autocomplete misses the accepted share: %s", got.body)
	}

	// Unshare is the owner's alone and takes effect at once.
	want(post(bob, bobTok, "/contacts/books/"+book+"/unshare", url.Values{"share_with": {bobID}}), http.StatusNotFound, "grantee unshare")
	want(get(bob, sharedPath), http.StatusOK, "still shared")
	want(post(alice, aliceTok, "/contacts/books/"+book+"/unshare", url.Values{"share_with": {bobID}}), http.StatusSeeOther, "unshare")
	want(get(bob, sharedPath), http.StatusNotFound, "shared list after unshare")
	if got := get(bob, "/api/gal?q=zelda"); strings.Contains(got.body, "zelda@client.example") {
		t.Errorf("autocomplete offers a revoked share: %s", got.body)
	}

	// The share limiter is the calendar handler's: spend the budget there and
	// the book form is refused, with nothing written.
	if a.calendarHandler == nil || a.calendarHandler.ShareLimiter() == nil {
		t.Fatal("no calendar share limiter wired")
	}
	for range 20 {
		a.calendarHandler.ShareLimiter().Allow(aliceID)
	}
	r = post(alice, aliceTok, "/contacts/books/"+book+"/share", url.Values{"share_with": {"bob@localhost"}, "level": {"read"}})
	want(r, http.StatusTooManyRequests, "share over the shared limit")
	if gs, _ := a.ACL.ListResourceGrants(store.ACLKindAddressBook, aliceID, book); len(gs) != 0 {
		t.Fatalf("a limited share wrote %d grants", len(gs))
	}
}
