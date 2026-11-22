package carddav

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	webdav "github.com/emersion/go-webdav/carddav"
	"google.golang.org/protobuf/proto"

	bongocaldav "github.com/bongo-project/bongo/internal/caldav"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// User ids are UUIDs: the shared segment parser rejects anything else.
const (
	shOwner    = "aaaaaaaa-1111-4111-8111-bbbbbbbbbbbb"
	shReader   = "22222222-2222-4222-8222-222222222222"
	shWriter   = "33333333-3333-4333-8333-333333333333"
	shStranger = "44444444-4444-4444-8444-444444444444"

	shBook    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // shared with reader and writer
	shPrivate = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" // never shared
	shOther   = "cccccccccccccccccccccccccccccccc"
)

type shareEnv struct {
	*davEnv
	acl *store.ACLStore
}

// idQuota records the user each quota question names.
type idQuota struct {
	mu   sync.Mutex
	over bool
	ids  []string
}

func (q *idQuota) RecipientWouldExceedQuota(id string, _ int64) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, id)
	return q.over, nil
}

func newShareEnv(t *testing.T) *shareEnv {
	t.Helper()
	e := &shareEnv{davEnv: newDAVEnv(t)}
	e.acl = store.NewACLStore(e.ps, e.applier)
	e.backend.SetGrants(e.acl)
	for id, email := range map[string]string{shOwner: "owner@example.com", shReader: "reader@example.com"} {
		data, err := proto.Marshal(&bongov1.UserProfile{Email: email})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.ps.Set([]byte("/user/"+id+"/profile"), data); err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range [][2]string{{shBook, "Work"}, {shPrivate, "Private"}, {shOther, "Other"}} {
		if _, err := e.books.Create(shOwner, b[0], b[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	e.contact(t, "c1", shBook)
	e.contact(t, "priv", shPrivate)
	return e
}

func (e *shareEnv) contact(t *testing.T, guid, book string) {
	t.Helper()
	c := &bongov1.Contact{
		Guid: guid, DisplayName: "Ada " + guid, Email: guid + "@example.com", AddressbookId: book,
		VcardRaw: []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ada " + guid + "\r\nEMAIL:" + guid + "@example.com\r\nEND:VCARD\r\n"),
	}
	if _, err := e.contacts.Create(shOwner, c); err != nil {
		t.Fatal(err)
	}
}

func (e *shareEnv) grant(t *testing.T, book, grantee, level, state string, at time.Time) {
	t.Helper()
	if err := e.acl.SetGrant(store.ACLKindAddressBook, shOwner, book, grantee, level, "", state, shOwner, at); err != nil {
		t.Fatal(err)
	}
}

func sharedSeg(owner, book string) string { return "shared-" + owner + "-" + book }

func (e *shareEnv) sharedPut(user, seg, guid string) *httptest.ResponseRecorder {
	return e.put(user, seg, guid)
}

func recordBytes(t *testing.T, e *shareEnv, guid string) string {
	t.Helper()
	c, err := e.contacts.Get(shOwner, guid)
	if err != nil {
		t.Fatalf("contact %s: %v", guid, err)
	}
	b, err := proto.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCardDAVSharedBookInHomeSet(t *testing.T) {
	e := newShareEnv(t)
	now := time.Now()
	e.grant(t, shBook, store.GranteeUser(shReader), store.GrantLevelRead, store.GrantStateAccepted, now)
	// Group grant on another book, reached through membership.
	if err := e.ps.Set(store.GroupMemberKey(shReader, "team"), nil); err != nil {
		t.Fatal(err)
	}
	e.grant(t, shOther, store.GranteeGroup("team"), store.GrantLevelWrite, store.GrantStateAccepted, now)
	// Pending and expired pending authorise nothing and are not listed.
	e.grant(t, shPrivate, store.GranteeUser(shReader), store.GrantLevelRead, store.GrantStatePending, now)

	w := e.do(shReader, "PROPFIND", "/carddav/"+shReader+"/addressbooks/", "", "Depth", "1")
	wantStatus(t, w, http.StatusMultiStatus, "propfind")
	body := w.Body.String()
	for _, want := range []string{
		"/carddav/" + shReader + "/addressbooks/" + sharedSeg(shOwner, shBook) + "/",
		"/carddav/" + shReader + "/addressbooks/" + sharedSeg(shOwner, shOther) + "/",
		"Work (owner@example.com)", "Other (owner@example.com)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home set lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, shPrivate) {
		t.Errorf("pending share listed:\n%s", body)
	}

	// The expired pending grant stays out too.
	e.grant(t, shPrivate, store.GranteeUser(shWriter), store.GrantLevelRead, store.GrantStatePending, now.Add(-(store.PendingGrantTTL+10)*time.Second))
	if got := e.do(shWriter, "PROPFIND", "/carddav/"+shWriter+"/addressbooks/", "", "Depth", "1").Body.String(); strings.Contains(got, shPrivate) {
		t.Errorf("expired pending share listed:\n%s", got)
	}
}

func TestCardDAVSharedBookNoOracle(t *testing.T) {
	e := newShareEnv(t)
	now := time.Now()
	e.grant(t, shBook, store.GranteeUser(shWriter), store.GrantLevelWrite, store.GrantStateAccepted, now)

	// Grant states for the reader.
	e.grant(t, shOther, store.GranteeUser(shReader), store.GrantLevelRead, store.GrantStatePending, now)
	e.grant(t, shPrivate, store.GranteeUser(shReader), store.GrantLevelRead, store.GrantStatePending, now.Add(-(store.PendingGrantTTL+10)*time.Second))
	e.grant(t, shBook, store.GranteeUser(shReader), store.GrantLevelRead, store.GrantStateAccepted, now)
	if err := e.acl.RevokeGrant(store.ACLKindAddressBook, shOwner, shBook, store.GranteeUser(shReader)); err != nil {
		t.Fatal(err)
	}
	// A group the reader has left.
	e.grant(t, shBook, store.GranteeGroup("gone"), store.GrantLevelWrite, store.GrantStateAccepted, now)
	// The owner is a member of a group that is granted their own book: the
	// grant resolves, so only the owner check keeps the shared- path closed.
	if err := e.ps.Set(store.GroupMemberKey(shOwner, "ownergrp"), nil); err != nil {
		t.Fatal(err)
	}
	e.grant(t, shBook, store.GranteeGroup("ownergrp"), store.GrantLevelWrite, store.GrantStateAccepted, now)
	// A live grant on a book that was deleted.
	const deleted = "dddddddddddddddddddddddddddddddd"
	if _, err := e.books.Create(shOwner, deleted, "Deleted", ""); err != nil {
		t.Fatal(err)
	}
	e.grant(t, deleted, store.GranteeUser(shReader), store.GrantLevelWrite, store.GrantStateAccepted, now)
	if err := store.PurgeAddressBook(e.books, e.contacts, shOwner, deleted); err != nil {
		t.Fatal(err)
	}
	// PurgeAddressBook revokes the grants; put the grant back to model a failed revoke.
	e.grant(t, deleted, store.GranteeUser(shReader), store.GrantLevelWrite, store.GrantStateAccepted, now)

	type row struct {
		name, user, seg string
	}
	rows := []row{
		{"baseline unknown book", shReader, sharedSeg(shOwner, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")},
		{"no grant", shStranger, sharedSeg(shOwner, shBook)},
		{"pending", shReader, sharedSeg(shOwner, shOther)},
		{"expired pending", shReader, sharedSeg(shOwner, shPrivate)},
		{"revoked", shReader, sharedSeg(shOwner, shBook)},
		{"left group", shReader, sharedSeg(shOwner, shBook)},
		{"owner via shared path", shOwner, sharedSeg(shOwner, shBook)},
		{"owner via group grant", shOwner, sharedSeg(shOwner, shBook)},
		{"upper-case owner", shWriter, sharedSeg(strings.ToUpper(shOwner), shBook)},
		{"hyphenated book", shWriter, "shared-" + shOwner + "-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		{"default book", shWriter, sharedSeg(shOwner, "default")},
		{"deleted book, live grant", shReader, sharedSeg(shOwner, deleted)},
		{"short owner", shWriter, "shared-" + shOwner[:35] + "-" + shBook},
		{"empty book", shWriter, "shared-" + shOwner + "-"},
	}
	type call struct {
		name   string
		method string
		path   func(user, seg string) string
		body   string
		hdr    []string
	}
	calls := []call{
		{"GET object", "GET", func(u, s string) string { return "/carddav/" + u + "/addressbooks/" + s + "/c1.vcf" }, "", nil},
		{"PROPFIND collection", "PROPFIND", func(u, s string) string { return "/carddav/" + u + "/addressbooks/" + s + "/" }, "", []string{"Depth", "1"}},
		{"PUT", "PUT", func(u, s string) string { return "/carddav/" + u + "/addressbooks/" + s + "/new.vcf" }, vcardBody, []string{"Content-Type", "text/vcard"}},
		{"DELETE object", "DELETE", func(u, s string) string { return "/carddav/" + u + "/addressbooks/" + s + "/c1.vcf" }, "", nil},
		{"DELETE collection", "DELETE", func(u, s string) string { return "/carddav/" + u + "/addressbooks/" + s + "/" }, "", nil},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			var wantBody string
			for i, r := range rows {
				w := e.do(r.user, c.method, c.path(r.user, r.seg), c.body, c.hdr...)
				if w.Code != http.StatusNotFound {
					t.Errorf("%s: status = %d, want 404; body %q", r.name, w.Code, w.Body.String())
				}
				if i == 0 {
					wantBody = w.Body.String()
				} else if w.Body.String() != wantBody {
					t.Errorf("%s: body %q differs from the baseline %q", r.name, w.Body.String(), wantBody)
				}
			}
		})
	}
	// Another user's home is the same refusal.
	w := e.do(shWriter, "GET", "/carddav/"+shReader+"/addressbooks/"+sharedSeg(shOwner, shBook)+"/c1.vcf", "")
	wantStatus(t, w, http.StatusNotFound, "another user's home")
	if recordBytes(t, e, "c1") == "" {
		t.Fatal("contact vanished")
	}
}

func TestCardDAVReadShareRefusesWrites(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shReader), store.GrantLevelRead, store.GrantStateAccepted, time.Now())
	seg := sharedSeg(shOwner, shBook)
	before := recordBytes(t, e, "c1")

	// The read holder can read.
	wantStatus(t, e.do(shReader, "GET", "/carddav/"+shReader+"/addressbooks/"+seg+"/c1.vcf", ""), http.StatusOK, "read")

	wantStatus(t, e.put(shReader, seg, "fresh"), http.StatusForbidden, "PUT new")
	wantStatus(t, e.put(shReader, seg, "c1"), http.StatusForbidden, "PUT existing")
	wantStatus(t, e.do(shReader, "DELETE", "/carddav/"+shReader+"/addressbooks/"+seg+"/c1.vcf", ""), http.StatusForbidden, "DELETE object")
	wantStatus(t, e.do(shReader, "DELETE", "/carddav/"+shReader+"/addressbooks/"+seg+"/", ""), http.StatusForbidden, "DELETE collection")

	if _, err := e.contacts.Get(shOwner, "fresh"); err == nil {
		t.Error("read holder created a contact")
	}
	if got := recordBytes(t, e, "c1"); got != before {
		t.Error("owner's record changed")
	}
	if _, err := e.books.Get(shOwner, shBook); err != nil {
		t.Errorf("book gone: %v", err)
	}
}

func TestCardDAVWriteShareDeleteCollectionIs403(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shWriter), store.GrantLevelWrite, store.GrantStateAccepted, time.Now())
	w := e.do(shWriter, "DELETE", "/carddav/"+shWriter+"/addressbooks/"+sharedSeg(shOwner, shBook)+"/", "")
	wantStatus(t, w, http.StatusForbidden, "write holder deletes collection")
	if _, err := e.books.Get(shOwner, shBook); err != nil {
		t.Errorf("book gone: %v", err)
	}
	if _, err := e.contacts.Get(shOwner, "c1"); err != nil {
		t.Errorf("contact purged: %v", err)
	}
}

func TestCardDAVWriteShareWritesOwnerKeyspace(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shWriter), store.GrantLevelWrite, store.GrantStateAccepted, time.Now())
	seg := sharedSeg(shOwner, shBook)

	w := e.put(shWriter, seg, "newc")
	wantStatus(t, w, http.StatusCreated, "PUT new")
	got, err := e.contacts.Get(shOwner, "newc")
	if err != nil {
		t.Fatalf("contact not in the owner's keyspace: %v", err)
	}
	if got.AddressbookId != shBook {
		t.Errorf("addressbook_id = %q, want %q", got.AddressbookId, shBook)
	}
	if _, err := e.contacts.Get(shWriter, "newc"); err == nil {
		t.Error("contact landed in the writer's keyspace")
	}
	if all, _, _ := e.contacts.ListByBook(shWriter, store.DefaultAddressBookID, 0, 0); len(all) != 0 {
		t.Errorf("writer's own keyspace holds %d contacts", len(all))
	}

	// Update keeps the book; the owner sees the same ETag the writer was given.
	w = e.put(shWriter, seg, "c1")
	wantStatus(t, w, http.StatusCreated, "PUT existing")
	if c, _ := e.contacts.Get(shOwner, "c1"); c == nil || c.AddressbookId != shBook {
		t.Errorf("updated contact = %+v", c)
	}
	own := e.do(shOwner, "GET", "/carddav/"+shOwner+"/addressbooks/"+shBook+"/c1.vcf", "")
	wantStatus(t, own, http.StatusOK, "owner GET")
	if etag := w.Header().Get("ETag"); etag == "" || etag != own.Header().Get("ETag") {
		t.Errorf("writer ETag %q, owner ETag %q", etag, own.Header().Get("ETag"))
	}

	// hrefs sit under the grantee's shared segment.
	list := e.do(shWriter, "PROPFIND", "/carddav/"+shWriter+"/addressbooks/"+seg+"/", "", "Depth", "1")
	wantStatus(t, list, http.StatusMultiStatus, "list")
	for _, g := range []string{"newc", "c1"} {
		want := "/carddav/" + shWriter + "/addressbooks/" + seg + "/" + g + ".vcf"
		if !strings.Contains(list.Body.String(), want) {
			t.Errorf("listing lacks %q:\n%s", want, list.Body.String())
		}
	}
	if strings.Contains(list.Body.String(), "priv.vcf") {
		t.Error("listing shows a contact of another book")
	}

	// Delete removes the owner's record.
	wantStatus(t, e.do(shWriter, "DELETE", "/carddav/"+shWriter+"/addressbooks/"+seg+"/newc.vcf", ""), http.StatusNoContent, "DELETE")
	if _, err := e.contacts.Get(shOwner, "newc"); err == nil {
		t.Error("contact survived the delete")
	}
}

func TestCardDAVSharedPutGuidInOtherBookIs403(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shWriter), store.GrantLevelWrite, store.GrantStateAccepted, time.Now())
	before := recordBytes(t, e, "priv")
	wantStatus(t, e.put(shWriter, sharedSeg(shOwner, shBook), "priv"), http.StatusForbidden, "PUT guid of another book")
	if recordBytes(t, e, "priv") != before {
		t.Error("private contact changed")
	}
}

func TestCardDAVSharedDeleteMissingEqualsOtherBook(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shWriter), store.GrantLevelWrite, store.GrantStateAccepted, time.Now())
	seg := sharedSeg(shOwner, shBook)
	before := recordBytes(t, e, "priv")
	missing := e.do(shWriter, "DELETE", "/carddav/"+shWriter+"/addressbooks/"+seg+"/nothere.vcf", "")
	other := e.do(shWriter, "DELETE", "/carddav/"+shWriter+"/addressbooks/"+seg+"/priv.vcf", "")
	wantStatus(t, missing, http.StatusNotFound, "missing")
	wantStatus(t, other, http.StatusNotFound, "other book")
	if missing.Body.String() != other.Body.String() {
		t.Errorf("bodies differ: %q vs %q", missing.Body.String(), other.Body.String())
	}
	if recordBytes(t, e, "priv") != before {
		t.Error("private contact changed")
	}
}

func TestCardDAVSharedPutChargesOwnerQuota(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shWriter), store.GrantLevelWrite, store.GrantStateAccepted, time.Now())
	q := &idQuota{}
	e.backend.SetQuotaChecker(q)
	seg := sharedSeg(shOwner, shBook)

	wantStatus(t, e.put(shWriter, seg, "q1"), http.StatusCreated, "within quota")
	q.over = true
	wantStatus(t, e.put(shWriter, seg, "q2"), http.StatusInsufficientStorage, "over the owner's quota")
	if _, err := e.contacts.Get(shOwner, "q2"); err == nil {
		t.Error("contact stored despite the refusal")
	}
	if len(q.ids) == 0 {
		t.Fatal("quota never consulted")
	}
	for _, id := range q.ids {
		if id != shOwner {
			t.Errorf("quota charged %q, want the owner %q", id, shOwner)
		}
	}
}

func TestCardDAVSharedRevokeNextRequest(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shReader), store.GrantLevelRead, store.GrantStateAccepted, time.Now())
	path := "/carddav/" + shReader + "/addressbooks/" + sharedSeg(shOwner, shBook) + "/c1.vcf"
	wantStatus(t, e.do(shReader, "GET", path, ""), http.StatusOK, "before revoke")
	if err := e.acl.RevokeGrant(store.ACLKindAddressBook, shOwner, shBook, store.GranteeUser(shReader)); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.do(shReader, "GET", path, ""), http.StatusNotFound, "after revoke")
}

func TestCardDAVMkcolSharedSegmentConstant403(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shWriter), store.GrantLevelWrite, store.GrantStateAccepted, time.Now())
	seg := sharedSeg(shOwner, shBook)
	with := e.mkcol(shWriter, seg, "X")
	without := e.mkcol(shStranger, seg, "X")
	wantStatus(t, with, http.StatusForbidden, "holder")
	wantStatus(t, without, http.StatusForbidden, "stranger")
	if with.Body.String() != without.Body.String() {
		t.Errorf("bodies differ: %q vs %q", with.Body.String(), without.Body.String())
	}
	if books, _ := e.books.List(shWriter); len(books) != 0 {
		t.Errorf("a book was created: %v", books)
	}
	// MKCOL beneath a shared book is refused.
	w := e.do(shWriter, "MKCOL", "/carddav/"+shWriter+"/addressbooks/"+seg+"/sub/", mkcolBody("Sub"), "Content-Type", "application/xml")
	wantStatus(t, w, http.StatusForbidden, "beneath")
}

var writePriv = regexp.MustCompile(`<([A-Za-z0-9]+:)?write[ />]`)

func TestCardDAVReadOnlySharePrivileges(t *testing.T) {
	e := newShareEnv(t)
	e.grant(t, shBook, store.GranteeUser(shReader), store.GrantLevelRead, store.GrantStateAccepted, time.Now())
	e.grant(t, shOther, store.GranteeUser(shReader), store.GrantLevelWrite, store.GrantStateAccepted, time.Now())
	dav := &webdav.Handler{Backend: e.backend, Prefix: "/carddav"}
	h := bongocaldav.PrivilegeFilter(dav, e.backend.ReadOnlyShare, e.backend.logger)
	propfind := func(user, seg string) string {
		r := httptest.NewRequest("PROPFIND", "/carddav/"+user+"/addressbooks/"+seg+"/", strings.NewReader(""))
		r.Header.Set("Depth", "0")
		r = r.WithContext(WithUserID(r.Context(), user))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusMultiStatus {
			t.Fatalf("PROPFIND %s: %d %s", seg, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if body := propfind(shReader, sharedSeg(shOwner, shBook)); writePriv.MatchString(body) || !strings.Contains(body, "read") {
		t.Errorf("read share reports write or no read:\n%s", body)
	}
	if body := propfind(shReader, sharedSeg(shOwner, shOther)); !writePriv.MatchString(body) {
		t.Errorf("write share lost write:\n%s", body)
	}
	if body := propfind(shReader, "default"); !writePriv.MatchString(body) {
		t.Errorf("own book lost write:\n%s", body)
	}
}

func TestCardDAVMultigetSharedNoGrant404(t *testing.T) {
	e := newShareEnv(t)
	body := func(seg string) string {
		return `<?xml version="1.0"?><C:addressbook-multiget xmlns:D="DAV:" xmlns:C="urn:ietf:params:xml:ns:carddav">` +
			`<D:prop><D:getetag/><C:address-data/></D:prop><D:href>/carddav/` + shStranger + `/addressbooks/` + seg + `/c1.vcf</D:href></C:addressbook-multiget>`
	}
	hdr := []string{"Depth", "1", "Content-Type", "application/xml"}
	real := e.do(shStranger, "REPORT", "/carddav/"+shStranger+"/addressbooks/"+sharedSeg(shOwner, shBook)+"/", body(sharedSeg(shOwner, shBook)), hdr...)
	none := e.do(shStranger, "REPORT", "/carddav/"+shStranger+"/addressbooks/"+sharedSeg(shOwner, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")+"/", body(sharedSeg(shOwner, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")), hdr...)
	if strings.Contains(real.Body.String(), "example.com") || strings.Contains(real.Body.String(), "VCARD") {
		t.Errorf("multiget leaked data:\n%s", real.Body.String())
	}
	noneBody := strings.ReplaceAll(none.Body.String(), "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", shBook)
	if real.Code != none.Code || real.Body.String() != noneBody {
		t.Errorf("answers differ: %d %q vs %d %q", real.Code, real.Body.String(), none.Code, none.Body.String())
	}
	if real.Code == http.StatusOK || real.Code == http.StatusMultiStatus && strings.Contains(real.Body.String(), "200 OK") {
		t.Errorf("multiget succeeded: %d %s", real.Code, real.Body.String())
	}
}

func TestParseSharedBookSegment(t *testing.T) {
	book := shBook
	tests := []struct {
		seg   string
		owner string
		book  string
		ok    bool
	}{
		{sharedSeg(shOwner, book), shOwner, book, true},
		{sharedSeg(strings.ToUpper(shOwner), book), "", "", false},
		{sharedSeg(shOwner, "default"), "", "", false},
		{sharedSeg(shOwner, strings.ToUpper(book)), "", "", false},
		{sharedSeg(shOwner, book[:31]), "", "", false},
		{sharedSeg(shOwner, book+"a"), "", "", false},
		{"shared-" + shOwner[:35] + "-" + book, "", "", false},
		{"shared-" + shOwner + book, "", "", false},
		{"shared-not-a-uuid-at-all-not-a-uuid-xxxx-" + book, "", "", false},
		{"other-" + shOwner + "-" + book, "", "", false},
		{sharedSeg(shOwner, book+"/x"), "", "", false},
	}
	for _, tt := range tests {
		o, b, ok := parseSharedBookSegment(tt.seg)
		if ok != tt.ok || o != tt.owner || b != tt.book {
			t.Errorf("parseSharedBookSegment(%q) = %q, %q, %v", tt.seg, o, b, ok)
		}
	}
}
