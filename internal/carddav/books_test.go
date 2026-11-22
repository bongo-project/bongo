package carddav

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	webdav "github.com/emersion/go-webdav/carddav"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// bookApplier applies commands to Pebble as the FSM would, including the grant
// commands a book delete issues, and can fail the nth contact delete.
type bookApplier struct {
	ps *store.PebbleStore

	mu             sync.Mutex
	aclMu          sync.Mutex // grant commands are serialised as the FSM applies them
	failDeleteAt   int
	contactDeletes int
}

var errBookInjected = errors.New("injected apply failure")

func (a *bookApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && store.IsACLSyntheticCommand(cmd.Key) {
		a.aclMu.Lock()
		defer a.aclMu.Unlock()
		return store.ApplyACLCommand(a.ps, cmd)
	}
	if cmd.Op == bongov1.Op_OP_DELETE && strings.Contains(string(cmd.Key), "/contact/") {
		a.mu.Lock()
		a.contactDeletes++
		fail := a.failDeleteAt > 0 && a.contactDeletes >= a.failDeleteAt
		a.mu.Unlock()
		if fail {
			return errBookInjected
		}
	}
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.ps.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.ps.Delete(cmd.Key)
	}
	return nil
}

func (a *bookApplier) failAt(n int) {
	a.mu.Lock()
	a.contactDeletes, a.failDeleteAt = 0, n
	a.mu.Unlock()
}

type davEnv struct {
	backend  *Backend
	handler  http.Handler
	applier  *bookApplier
	books    *store.AddressBookStore
	contacts *store.ContactStore
	ps       *store.PebbleStore
}

func newDAVEnv(t *testing.T) *davEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	app := &bookApplier{ps: ps}
	e := &davEnv{
		applier:  app,
		ps:       ps,
		books:    store.NewAddressBookStore(ps, app),
		contacts: store.NewContactStore(ps, app),
	}
	e.backend = NewBackend(e.contacts, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.backend.SetAddressBooks(e.books)
	dav := &webdav.Handler{Backend: e.backend, Prefix: "/carddav"}
	e.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithUserID(r.Context(), r.Header.Get("X-Test-User"))
		dav.ServeHTTP(w, r.WithContext(ctx))
	})
	return e
}

func (e *davEnv) do(user, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-Test-User", user)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.handler.ServeHTTP(w, r)
	return w
}

func mkcolBody(name string) string {
	return `<?xml version="1.0" encoding="utf-8"?>
<mkcol xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><set><prop>
<resourcetype><collection/><C:addressbook/></resourcetype>
<displayname>` + name + `</displayname></prop></set></mkcol>`
}

const vcardBody = "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ada\r\nEMAIL:ada@example.com\r\nEND:VCARD\r\n"

func (e *davEnv) mkcol(user, seg, name string) *httptest.ResponseRecorder {
	return e.do(user, "MKCOL", "/carddav/"+user+"/addressbooks/"+seg+"/", mkcolBody(name),
		"Content-Type", "application/xml")
}

func (e *davEnv) put(user, seg, guid string) *httptest.ResponseRecorder {
	return e.do(user, "PUT", "/carddav/"+user+"/addressbooks/"+seg+"/"+guid+".vcf", vcardBody,
		"Content-Type", "text/vcard")
}

func wantStatus(t *testing.T, w *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("%s: status = %d, want %d; body %q", what, w.Code, want, w.Body.String())
	}
}

const (
	bookHex  = "0123456789abcdef0123456789abcdef"
	bookUUID = "01234567-89AB-CDEF-0123-456789ABCDEF"
)

func TestCardDAVPropfindHomeListsBooks(t *testing.T) {
	e := newDAVEnv(t)
	wantStatus(t, e.mkcol("alice", bookHex, "Work"), http.StatusCreated, "mkcol")

	w := e.do("alice", "PROPFIND", "/carddav/alice/addressbooks/", "", "Depth", "1")
	wantStatus(t, w, http.StatusMultiStatus, "propfind")
	body := w.Body.String()
	for _, want := range []string{"/carddav/alice/addressbooks/default/", "/carddav/alice/addressbooks/" + bookHex + "/", "Work"} {
		if !strings.Contains(body, want) {
			t.Errorf("home set listing lacks %q:\n%s", want, body)
		}
	}
	if other := e.do("bob", "PROPFIND", "/carddav/bob/addressbooks/", "", "Depth", "1").Body.String(); strings.Contains(other, bookHex) {
		t.Errorf("another user's listing shows alice's book:\n%s", other)
	}
}

func TestCardDAVMkcol(t *testing.T) {
	e := newDAVEnv(t)

	wantStatus(t, e.mkcol("alice", bookHex, "Work &amp; Home"), http.StatusCreated, "create")
	book, err := e.books.Get("alice", bookHex)
	if err != nil || book.Name != "Work & Home" {
		t.Fatalf("stored book = %v, %v", book, err)
	}

	wantStatus(t, e.mkcol("alice", bookHex, "Again"), http.StatusMethodNotAllowed, "existing")
	wantStatus(t, e.mkcol("alice", "default", "Mine"), http.StatusMethodNotAllowed, "default")
	wantStatus(t, e.mkcol("alice", "../x", "Mine"), http.StatusForbidden, "traversal segment")
	wantStatus(t, e.mkcol("alice", "plain-name", "Mine"), http.StatusForbidden, "invalid segment")

	// A client that creates a collection under an upper-case hyphenated UUID
	// gets the canonical id and can keep using its own URL.
	wantStatus(t, e.mkcol("bob", bookUUID, "Bob"), http.StatusCreated, "uuid")
	if _, err := e.books.Get("bob", bookHex); err != nil {
		t.Fatalf("canonical book missing: %v", err)
	}
	wantStatus(t, e.put("bob", bookUUID, "c1"), http.StatusCreated, "put via the client's own URL")

	// The other user's id in the path is refused.
	w := e.do("alice", "MKCOL", "/carddav/bob/addressbooks/"+strings.Repeat("a", 32)+"/", mkcolBody("Sneaky"),
		"Content-Type", "application/xml")
	wantStatus(t, w, http.StatusForbidden, "other user's path")
	if books, _ := e.books.List("bob"); len(books) != 1 {
		t.Fatalf("bob has %d books, want only his own", len(books))
	}

	// Name is cleaned and defaulted.
	wantStatus(t, e.mkcol("alice", strings.Repeat("b", 32), ""), http.StatusCreated, "no name")
	if b, _ := e.books.Get("alice", strings.Repeat("b", 32)); b == nil || b.Name != "Contacts" {
		t.Fatalf("default name = %v", b)
	}
}

func TestCardDAVMkcolCap(t *testing.T) {
	e := newDAVEnv(t)
	for i := range store.MaxAddressBooksPerUser {
		id := strings.Repeat(string(rune('a'+i%6)), 31) + string(rune('0'+i%10))
		if i >= 10 {
			id = strings.Repeat("c", 30) + string(rune('0'+i/10)) + string(rune('0'+i%10))
		}
		if w := e.mkcol("alice", id, "b"); w.Code != http.StatusCreated {
			t.Fatalf("book %d (%s): status %d %s", i, id, w.Code, w.Body.String())
		}
	}
	wantStatus(t, e.mkcol("alice", strings.Repeat("e", 32), "over"), http.StatusInsufficientStorage, "over the cap")
}

func TestCardDAVPutIntoBook(t *testing.T) {
	e := newDAVEnv(t)
	wantStatus(t, e.mkcol("alice", bookHex, "Work"), http.StatusCreated, "mkcol")
	other := strings.Repeat("f", 32)
	wantStatus(t, e.mkcol("alice", other, "Other"), http.StatusCreated, "mkcol other")

	wantStatus(t, e.put("alice", bookHex, "c1"), http.StatusCreated, "put")
	c, err := e.contacts.Get("alice", "c1")
	if err != nil || c.AddressbookId != bookHex {
		t.Fatalf("contact = %v, %v; want addressbook_id %s", c, err, bookHex)
	}

	wantStatus(t, e.do("alice", "GET", "/carddav/alice/addressbooks/default/c1.vcf", ""), http.StatusNotFound, "get through the default path")
	wantStatus(t, e.do("alice", "GET", "/carddav/alice/addressbooks/"+bookHex+"/c1.vcf", ""), http.StatusOK, "get through its own book")
	wantStatus(t, e.put("alice", other, "c1"), http.StatusConflict, "same guid into another book")
	wantStatus(t, e.put("alice", "default", "c1"), http.StatusConflict, "same guid into the default book")
	wantStatus(t, e.do("alice", "DELETE", "/carddav/alice/addressbooks/default/c1.vcf", ""), http.StatusNotFound, "delete through the default path")
	if _, err := e.contacts.Get("alice", "c1"); err != nil {
		t.Fatalf("contact was deleted through the wrong book: %v", err)
	}

	// An update keeps the book.
	wantStatus(t, e.put("alice", bookHex, "c1"), http.StatusCreated, "update")
	if c, _ := e.contacts.Get("alice", "c1"); c.AddressbookId != bookHex {
		t.Fatalf("update changed the book to %q", c.AddressbookId)
	}

	// Default book contacts carry no id.
	wantStatus(t, e.put("alice", "default", "d1"), http.StatusCreated, "put default")
	if c, _ := e.contacts.Get("alice", "d1"); c.AddressbookId != "" {
		t.Fatalf("default contact addressbook_id = %q, want empty", c.AddressbookId)
	}

	// A PUT into a book that does not exist writes nothing.
	missing := strings.Repeat("9", 32)
	wantStatus(t, e.put("alice", missing, "m1"), http.StatusConflict, "put into a missing book")
	if _, err := e.contacts.Get("alice", "m1"); err == nil {
		t.Fatal("a contact was written into a missing book")
	}
}

func TestCardDAVListIsPerBook(t *testing.T) {
	e := newDAVEnv(t)
	wantStatus(t, e.mkcol("alice", bookHex, "Work"), http.StatusCreated, "mkcol")
	wantStatus(t, e.put("alice", bookHex, "w1"), http.StatusCreated, "put")
	wantStatus(t, e.put("alice", "default", "d1"), http.StatusCreated, "put")

	list := func(seg string) string {
		w := e.do("alice", "PROPFIND", "/carddav/alice/addressbooks/"+seg+"/", "", "Depth", "1")
		wantStatus(t, w, http.StatusMultiStatus, "propfind "+seg)
		return w.Body.String()
	}
	work, def := list(bookHex), list("default")
	if !strings.Contains(work, "w1.vcf") || strings.Contains(work, "d1.vcf") {
		t.Errorf("work book listing wrong:\n%s", work)
	}
	if !strings.Contains(def, "d1.vcf") || strings.Contains(def, "w1.vcf") {
		t.Errorf("default book listing wrong:\n%s", def)
	}

	report := `<?xml version="1.0"?><C:addressbook-query xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav"><D:prop><D:getetag/></D:prop><C:filter><C:prop-filter name="FN"/></C:filter></C:addressbook-query>`
	w := e.do("alice", "REPORT", "/carddav/alice/addressbooks/"+bookHex+"/", report, "Depth", "1", "Content-Type", "application/xml")
	wantStatus(t, w, http.StatusMultiStatus, "report")
	if !strings.Contains(w.Body.String(), "w1.vcf") || strings.Contains(w.Body.String(), "d1.vcf") {
		t.Errorf("report leaks across books:\n%s", w.Body.String())
	}
}

func TestCardDAVDeleteBook(t *testing.T) {
	e := newDAVEnv(t)
	wantStatus(t, e.do("alice", "DELETE", "/carddav/alice/addressbooks/default/", ""), http.StatusForbidden, "default book")

	wantStatus(t, e.mkcol("alice", bookHex, "Work"), http.StatusCreated, "mkcol")
	wantStatus(t, e.put("alice", bookHex, "w1"), http.StatusCreated, "put")
	wantStatus(t, e.put("alice", bookHex, "w2"), http.StatusCreated, "put")
	wantStatus(t, e.put("alice", "default", "d1"), http.StatusCreated, "put")

	grant, err := store.BuildACLCommand(store.ACLRequest{Op: store.ACLOpSet, Kind: store.ACLKindAddressBook,
		Owner: "alice", Resource: bookHex, Grantee: store.GranteeUser("bob"),
		Level: store.GrantLevelRead, State: store.GrantStateAccepted, GrantedBy: "alice", At: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.applier.ApplyCommand(grant, time.Second); err != nil {
		t.Fatal(err)
	}
	acl := store.NewACLStore(e.ps, e.applier)
	if g, _ := acl.GetGrant(store.ACLKindAddressBook, "alice", bookHex, store.GranteeUser("bob")); g == nil {
		t.Fatal("grant was not written")
	}

	// Bob cannot delete Alice's book, under either user segment.
	wantStatus(t, e.do("bob", "DELETE", "/carddav/alice/addressbooks/"+bookHex+"/", ""), http.StatusNotFound, "bob via alice's path")
	wantStatus(t, e.do("bob", "DELETE", "/carddav/bob/addressbooks/"+bookHex+"/", ""), http.StatusNotFound, "bob via his own path")
	if _, err := e.books.Get("alice", bookHex); err != nil {
		t.Fatalf("alice's book was deleted: %v", err)
	}

	wantStatus(t, e.do("alice", "DELETE", "/carddav/alice/addressbooks/"+bookHex+"/", ""), http.StatusNoContent, "delete")
	if _, err := e.books.Get("alice", bookHex); !errors.Is(err, store.ErrAddressBookNotFound) {
		t.Fatalf("book survives: %v", err)
	}
	for _, g := range []string{"w1", "w2"} {
		if _, err := e.contacts.Get("alice", g); err == nil {
			t.Errorf("contact %s survives the book", g)
		}
	}
	if _, err := e.contacts.Get("alice", "d1"); err != nil {
		t.Errorf("default contact was removed: %v", err)
	}
	if g, _ := acl.GetGrant(store.ACLKindAddressBook, "alice", bookHex, store.GranteeUser("bob")); g != nil {
		t.Error("grant survives the book")
	}
	wantStatus(t, e.do("alice", "DELETE", "/carddav/alice/addressbooks/"+bookHex+"/", ""), http.StatusNotFound, "second delete")
}

func TestCardDAVDeleteBookResumesAfterFailure(t *testing.T) {
	e := newDAVEnv(t)
	wantStatus(t, e.mkcol("alice", bookHex, "Work"), http.StatusCreated, "mkcol")
	for _, g := range []string{"w1", "w2", "w3", "w4"} {
		wantStatus(t, e.put("alice", bookHex, g), http.StatusCreated, "put")
	}

	e.applier.failAt(3)
	w := e.do("alice", "DELETE", "/carddav/alice/addressbooks/"+bookHex+"/", "")
	if w.Code < 500 {
		t.Fatalf("failed delete answered %d, want a server error", w.Code)
	}
	if _, err := e.books.Get("alice", bookHex); err != nil {
		t.Fatalf("the book must survive a partial delete: %v", err)
	}
	left, _, _ := e.contacts.ListByBook("alice", bookHex, 0, 0)
	if len(left) == 0 || len(left) == 4 {
		t.Fatalf("%d contacts remain, want some but not all", len(left))
	}

	e.applier.failAt(0)
	wantStatus(t, e.do("alice", "DELETE", "/carddav/alice/addressbooks/"+bookHex+"/", ""), http.StatusNoContent, "retry")
	if left, _, _ := e.contacts.ListByBook("alice", bookHex, 0, 0); len(left) != 0 {
		t.Fatalf("%d contacts remain after the retry", len(left))
	}
	if _, err := e.books.Get("alice", bookHex); !errors.Is(err, store.ErrAddressBookNotFound) {
		t.Fatalf("book survives the retry: %v", err)
	}
}

func TestCardDAVForeignBookIsNotFound(t *testing.T) {
	e := newDAVEnv(t)
	wantStatus(t, e.mkcol("bob", bookHex, "Bob's"), http.StatusCreated, "mkcol")
	wantStatus(t, e.put("bob", bookHex, "secret"), http.StatusCreated, "put")

	for _, user := range []string{"alice"} {
		for _, owner := range []string{"alice", "bob"} {
			base := "/carddav/" + owner + "/addressbooks/" + bookHex + "/"
			for _, tc := range []struct{ method, path, body string }{
				{"PROPFIND", base, ""},
				{"GET", base + "secret.vcf", ""},
				{"DELETE", base + "secret.vcf", ""},
				{"PUT", base + "x.vcf", vcardBody},
			} {
				w := e.do(user, tc.method, tc.path, tc.body, "Depth", "1", "Content-Type", "text/vcard")
				want := http.StatusNotFound
				if tc.method == "PUT" && owner == "alice" {
					want = http.StatusConflict
				}
				if w.Code != want || strings.Contains(w.Body.String(), "secret") {
					t.Errorf("%s %s as %s: status %d (want %d), body %q", tc.method, tc.path, user, w.Code, want, w.Body.String())
				}
			}
		}
	}
	if _, err := e.contacts.Get("bob", "secret"); err != nil {
		t.Fatalf("bob's contact was touched: %v", err)
	}
	if _, err := e.contacts.Get("alice", "x"); err == nil {
		t.Fatal("alice wrote a contact through bob's book id")
	}
}

func TestCardDAVBookPathParsing(t *testing.T) {
	tests := []struct {
		path           string
		user, seg, obj string
		ok             bool
	}{
		{"/carddav/u/addressbooks/default/", "u", "default", "", true},
		{"/carddav/u/addressbooks/default", "u", "default", "", true},
		{"/carddav/u/addressbooks/b/c.vcf", "u", "b", "c.vcf", true},
		{"/carddav/u/addressbooks/", "", "", "", false},
		{"/carddav/u/other/b/", "", "", "", false},
		{"/carddav/u/addressbooks/b/c/d", "", "", "", false},
		{"/carddav//addressbooks/b/", "", "", "", false},
		{"/other/u/addressbooks/b/", "", "", "", false},
	}
	for _, tt := range tests {
		user, seg, obj, ok := parseBookPath(tt.path)
		if user != tt.user || seg != tt.seg || obj != tt.obj || ok != tt.ok {
			t.Errorf("parseBookPath(%q) = %q %q %q %v", tt.path, user, seg, obj, ok)
		}
	}
}

// TestCardDAVBooklessBackendStillServesDefault covers a backend wired without
// the book store.
func TestCardDAVBooklessBackendStillServesDefault(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	b := NewBackend(store.NewContactStore(ps, &bookApplier{ps: ps}), nil, slog.Default())
	ctx := WithUserID(context.Background(), "u")
	books, err := b.ListAddressBooks(ctx)
	if err != nil || len(books) != 1 {
		t.Fatalf("books = %v, %v", books, err)
	}
}
