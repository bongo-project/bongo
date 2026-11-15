package agent

import (
	"errors"
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

// TestContactsAddressBooksThroughRouter drives the address book routes through
// the production router of a server-role agent (the dev role does not enforce
// CSRF): registration, CSRF, the session user, and the setters on the handler.
func TestContactsAddressBooksThroughRouter(t *testing.T) {
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

	// CSRF: every book write is refused without a token, and nothing is written.
	for _, path := range []string{"/contacts/books", "/contacts/books/" + strings.Repeat("a", 32) + "/rename", "/contacts/books/" + strings.Repeat("a", 32) + "/delete"} {
		r := post(alice, "", path, url.Values{"name": {"x"}})
		if r.code != http.StatusForbidden || !strings.Contains(r.body, csrfBodyText) {
			t.Errorf("%s without a token: %d %q, want 403 %q", path, r.code, r.body, csrfBodyText)
		}
	}
	if books, _ := a.AddressBooks.List(aliceID); len(books) != 0 {
		t.Fatalf("a refused request created %d books", len(books))
	}

	// Create.
	want(post(alice, aliceTok, "/contacts/books", url.Values{"name": {"  "}}), http.StatusBadRequest, "blank name")
	r := post(alice, aliceTok, "/contacts/books", url.Values{"name": {"Work"}})
	want(r, http.StatusSeeOther, "create")
	books, _ := a.AddressBooks.List(aliceID)
	if len(books) != 1 || books[0].Name != "Work" {
		t.Fatalf("books = %v", books)
	}
	book := books[0].Id
	if !strings.Contains(r.loc, "book="+book) {
		t.Errorf("redirect %q does not name the new book", r.loc)
	}

	// A contact filed into the book, one in the default book.
	want(post(alice, aliceTok, "/contacts", url.Values{"display_name": {"Ada Work"}, "email": {"ada@work.example"}, "addressbook": {book}}), http.StatusSeeOther, "create in book")
	want(post(alice, aliceTok, "/contacts", url.Values{"display_name": {"Dee Default"}, "email": {"dee@home.example"}}), http.StatusSeeOther, "create default")

	// Refusals name the failure, not just "not success".
	r = post(alice, aliceTok, "/contacts", url.Values{"display_name": {"Eve"}, "addressbook": {strings.Repeat("e", 32)}})
	want(r, http.StatusBadRequest, "unknown book")
	if !strings.Contains(r.body, "That address book does not exist.") {
		t.Errorf("unknown book body = %q", r.body)
	}
	// Another user's real book is as unknown as a made-up one.
	bobBook := store.NewAddressBookID()
	if _, err := a.AddressBooks.Create(bobID, bobBook, "Bob's", ""); err != nil {
		t.Fatal(err)
	}
	want(post(alice, aliceTok, "/contacts", url.Values{"display_name": {"Eve"}, "addressbook": {bobBook}}), http.StatusBadRequest, "foreign book")
	want(post(alice, aliceTok, "/contacts", url.Values{"display_name": {"Eve"}, "addressbook": {"../x"}}), http.StatusBadRequest, "invalid book")

	filed, _, _ := a.Contacts.ListByBook(aliceID, book, 0, 0)
	if len(filed) != 1 || filed[0].DisplayName != "Ada Work" {
		t.Fatalf("book holds %v", filed)
	}
	if all, total, _ := a.Contacts.List(aliceID, 0, 0); total != 2 || len(all) != 2 {
		t.Fatalf("%d contacts stored, want 2 (a refused create must write nothing)", total)
	}

	// Filtering.
	list := get(alice, "/contacts?book="+book)
	want(list, http.StatusOK, "filter")
	if !strings.Contains(list.body, "Ada Work") || strings.Contains(list.body, "Dee Default") {
		t.Errorf("book filter shows the wrong contacts")
	}
	list = get(alice, "/contacts?book=default")
	want(list, http.StatusOK, "filter default")
	if strings.Contains(list.body, "Ada Work") || !strings.Contains(list.body, "Dee Default") {
		t.Errorf("default filter shows the wrong contacts")
	}
	list = get(alice, "/contacts")
	if !strings.Contains(list.body, "Ada Work") || !strings.Contains(list.body, "Dee Default") {
		t.Errorf("all contacts must show every book")
	}
	want(get(alice, "/contacts?book="+bobBook), http.StatusNotFound, "foreign book filter")
	want(get(alice, "/contacts?book=nonsense"), http.StatusNotFound, "invalid book filter")

	// An edit keeps the book.
	guid := filed[0].Guid
	want(post(alice, aliceTok, "/contacts/"+guid, url.Values{"display_name": {"Ada Renamed"}, "email": {"ada@work.example"}}), http.StatusSeeOther, "edit")
	if c, err := a.Contacts.Get(aliceID, guid); err != nil || c.DisplayName != "Ada Renamed" || c.AddressbookId != book {
		t.Fatalf("after edit: %v, %v", c, err)
	}

	// Rename: another user gets 404 and changes nothing.
	want(post(bob, bobTok, "/contacts/books/"+book+"/rename", url.Values{"name": {"Hijacked"}}), http.StatusNotFound, "foreign rename")
	want(post(alice, aliceTok, "/contacts/books/"+book+"/rename", url.Values{"name": {""}}), http.StatusBadRequest, "blank rename")
	want(post(alice, aliceTok, "/contacts/books/"+book+"/rename", url.Values{"name": {"Office"}}), http.StatusSeeOther, "rename")
	if b, _ := a.AddressBooks.Get(aliceID, book); b == nil || b.Name != "Office" {
		t.Fatalf("book after rename = %v", b)
	}

	// Delete: the default book is protected, another user cannot delete it.
	r = post(alice, aliceTok, "/contacts/books/default/delete", url.Values{})
	want(r, http.StatusForbidden, "delete default")
	if !strings.Contains(r.body, "The default address book cannot be deleted.") {
		t.Errorf("protected body = %q", r.body)
	}
	want(post(bob, bobTok, "/contacts/books/"+book+"/delete", url.Values{}), http.StatusNotFound, "foreign delete")
	if _, err := a.AddressBooks.Get(aliceID, book); err != nil {
		t.Fatalf("a foreign delete removed the book: %v", err)
	}
	want(post(alice, aliceTok, "/contacts/books/"+book+"/delete", url.Values{}), http.StatusSeeOther, "delete")
	if _, err := a.AddressBooks.Get(aliceID, book); !errors.Is(err, store.ErrAddressBookNotFound) {
		t.Fatalf("book survives: %v", err)
	}
	if _, err := a.Contacts.Get(aliceID, guid); err == nil {
		t.Fatal("the book's contact survives the delete")
	}
	if all, total, _ := a.Contacts.List(aliceID, 0, 0); total != 1 || all[0].DisplayName != "Dee Default" {
		t.Fatalf("remaining contacts = %v", all)
	}
	want(post(alice, aliceTok, "/contacts/books/"+book+"/delete", url.Values{}), http.StatusNotFound, "second delete")
}
