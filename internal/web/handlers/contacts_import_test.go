package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	ioUser  = "u-import"
	ioEmail = "import@example.test"
)

// failingContactPuts refuses the nth contact PUT (and, when failDeletes is set,
// every contact delete), as a Raft outage part way through an import would.
type failingContactPuts struct {
	inner       *directApplierTags
	mu          sync.Mutex
	puts        int
	nth         int
	failDeletes bool
}

func (f *failingContactPuts) ApplyCommand(cmd *bongov1.RaftCommand, d time.Duration) error {
	if strings.Contains(string(cmd.Key), "/contact/") {
		f.mu.Lock()
		defer f.mu.Unlock()
		if cmd.Op == bongov1.Op_OP_PUT {
			f.puts++
			if f.nth > 0 && f.puts == f.nth {
				return errors.New("injected raft failure")
			}
		}
		if cmd.Op == bongov1.Op_OP_DELETE && f.failDeletes {
			return errors.New("injected raft failure")
		}
	}
	return f.inner.ApplyCommand(cmd, d)
}

type sizeQuota struct {
	limit int64
	asked []int64
}

func (q *sizeQuota) RecipientWouldExceedQuota(_ string, add int64) (bool, error) {
	q.asked = append(q.asked, add)
	return add > q.limit, nil
}

type ioEnv struct {
	h        *ContactsHandler
	contacts *store.ContactStore
	books    *store.AddressBookStore
}

func newIOEnv(t *testing.T, wrap func(*directApplierTags) store.RaftApplier) *ioEnv {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ps.Close() })
	var applier store.RaftApplier = &directApplierTags{ps: ps}
	if wrap != nil {
		applier = wrap(&directApplierTags{ps: ps})
	}
	e := &ioEnv{
		contacts: store.NewContactStore(ps, applier),
		books:    store.NewAddressBookStore(ps, applier),
	}
	e.h = NewContactsHandler(e.contacts, nil)
	e.h.SetAddressBooks(e.books)
	return e
}

func importReq(t *testing.T, user string, content string, book string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("vcf", "upload.dat")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(content))
	if book != "" {
		_ = mw.WriteField("addressbook", book)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/contacts/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	ctx := middleware.WithUserContext(req.Context(), user, ioEmail)
	return req.WithContext(middleware.WithLocale(ctx, "en"))
}

func (e *ioEnv) doImport(t *testing.T, content, book string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	e.h.ImportContacts(rr, importReq(t, ioUser, content, book))
	return rr
}

func (e *ioEnv) count(t *testing.T, user string) int {
	t.Helper()
	_, total, err := e.contacts.List(user, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return total
}

// bodyMsg is the error message of a refusal page, without the page around it.
func bodyMsg(rr *httptest.ResponseRecorder) string {
	body := rr.Body.String()
	if _, after, ok := strings.Cut(body, `aria-live="assertive">`); ok {
		msg, _, _ := strings.Cut(after, "</div>")
		return html.UnescapeString(msg)
	}
	return body
}

func reason(r string) string { return i18n.T("en", "contacts.importReason."+r) }

func TestImportCSVWritesContacts(t *testing.T) {
	e := newIOEnv(t, nil)
	rr := e.doImport(t, "First Name,Last Name,E-mail 1 - Value\nAda,Lovelace,ada@example.test\nGrace,Hopper,grace@example.test\n", "")
	if rr.Code != http.StatusSeeOther || !strings.HasPrefix(rr.Header().Get("Location"), "/contacts") {
		t.Fatalf("status %d, Location %q", rr.Code, rr.Header().Get("Location"))
	}
	if got := e.count(t, ioUser); got != 2 {
		t.Fatalf("%d contacts, want 2", got)
	}
}

// A malformed row refuses the file; nothing is written while rows are read.
func TestImportMalformedRowWritesNothing(t *testing.T) {
	e := newIOEnv(t, nil)
	rr := e.doImport(t, "First Name,Last Name\nA,B\nC,D\nE,\"bad \" quote\n", "")
	want := fmt.Sprintf(i18n.T("en", "contacts.importErrRow"), 4, reason("syntax"))
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(bodyMsg(rr), want) {
		t.Fatalf("status %d, want 422 with %q; body %q", rr.Code, want, bodyMsg(rr))
	}
	if got := e.count(t, ioUser); got != 0 {
		t.Fatalf("%d contacts written, want 0", got)
	}
}

func TestImportVCardBadBlockWritesNothing(t *testing.T) {
	e := newIOEnv(t, nil)
	in := "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEMAIL:alice@example.test\nEND:VCARD\n" +
		"BEGIN:VCARD\nVERSION:3.0\nFN:Bob\nEMAIL:bob@example.test\n"
	rr := e.doImport(t, in, "")
	want := fmt.Sprintf(i18n.T("en", "contacts.importErrCard"), 2, reason("incomplete"))
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(bodyMsg(rr), want) {
		t.Fatalf("status %d, want 422 with %q; body %q", rr.Code, want, bodyMsg(rr))
	}
	if got := e.count(t, ioUser); got != 0 {
		t.Fatalf("%d contacts written, want 0 (the first card must not be kept)", got)
	}
}

func TestImportUnknownFormatWritesNothing(t *testing.T) {
	e := newIOEnv(t, nil)
	rr := e.doImport(t, `{"contacts":[]}`, "")
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(bodyMsg(rr), "English") {
		t.Fatalf("status %d body %q", rr.Code, bodyMsg(rr))
	}
}

func TestImportLDIFChangeRecordWritesNothing(t *testing.T) {
	e := newIOEnv(t, nil)
	in := "dn: cn=A\nobjectclass: person\ncn: A\nmail: a@example.test\n\ndn: cn=B\nchangetype: delete\n"
	rr := e.doImport(t, in, "")
	want := fmt.Sprintf(i18n.T("en", "contacts.importErrEntry"), 2, 7, reason("change"))
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(bodyMsg(rr), want) {
		t.Fatalf("status %d, want 422 with %q; body %q", rr.Code, want, bodyMsg(rr))
	}
	if got := e.count(t, ioUser); got != 0 {
		t.Fatalf("%d contacts written, want 0", got)
	}
}

// A write that fails part way deletes what was written.
func TestImportCompensatesOnWriteFailure(t *testing.T) {
	var fa *failingContactPuts
	e := newIOEnv(t, func(in *directApplierTags) store.RaftApplier {
		// The pre-existing contact is PUT 1, so PUT 4 is the third import write.
		fa = &failingContactPuts{inner: in, nth: 4}
		return fa
	})
	keep, err := e.contacts.Create(ioUser, &bongov1.Contact{DisplayName: "Existing", Email: "keep@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	rr := e.doImport(t, "First Name,Last Name\nA,A\nB,B\nC,C\nD,D\n", "")
	want := i18n.T("en", "contacts.importErrWrite")
	if rr.Code != http.StatusInternalServerError || !strings.Contains(bodyMsg(rr), want) {
		t.Fatalf("status %d, want 500 with %q; body %q", rr.Code, want, bodyMsg(rr))
	}
	if got := e.count(t, ioUser); got != 1 {
		t.Fatalf("%d contacts remain, want only the pre-existing one", got)
	}
	if _, err := e.contacts.Get(ioUser, keep); err != nil {
		t.Fatalf("the rollback removed a contact that was not part of the import: %v", err)
	}
}

// A 10 MiB file of tiny rows gets the too-many page without building them all.
func TestImportTinyRowsRefused(t *testing.T) {
	e := newIOEnv(t, nil)
	body := "First Name,Last Name\n" + strings.Repeat("a,\n", (10<<20-4096)/3)
	rr := e.doImport(t, body, "")
	want := fmt.Sprintf(i18n.T("en", "contacts.importErrTooMany"), contactsImportMaxRecords)
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(bodyMsg(rr), want) {
		t.Fatalf("status %d, want 422 with %q; body %q", rr.Code, want, bodyMsg(rr))
	}
	if got := e.count(t, ioUser); got != 0 {
		t.Fatalf("%d contacts written, want 0", got)
	}
}

// When the rollback fails too, the page says so with the count.
func TestImportReportsUncompensated(t *testing.T) {
	e := newIOEnv(t, func(in *directApplierTags) store.RaftApplier {
		return &failingContactPuts{inner: in, nth: 3, failDeletes: true}
	})
	rr := e.doImport(t, "First Name,Last Name\nA,A\nB,B\nC,C\nD,D\n", "")
	want := fmt.Sprintf(i18n.T("en", "contacts.importErrWritePartial"), 3)
	if rr.Code != http.StatusInternalServerError || !strings.Contains(bodyMsg(rr), want) {
		t.Fatalf("status %d, want 500 with %q; body %q", rr.Code, want, bodyMsg(rr))
	}
}

// The quota check counts the contacts as stored, not the upload.
func TestImportQuotaUsesBuiltSize(t *testing.T) {
	q := &sizeQuota{}
	e := newIOEnv(t, nil)
	e.h.SetQuotaChecker(q)
	csv := "First Name,Last Name\n" + strings.Repeat("A,B\n", 40)
	// Each stored contact is a vCard plus a protobuf record, far bigger than the
	// 4-byte CSV row it came from.
	q.limit = int64(len(csv))
	rr := e.doImport(t, csv, "")
	if rr.Code != http.StatusSeeOther || !strings.Contains(rr.Header().Get("Location"), "flash=") {
		t.Fatalf("status %d Location %q, want a quota flash redirect", rr.Code, rr.Header().Get("Location"))
	}
	if len(q.asked) != 1 || q.asked[0] <= int64(len(csv)) {
		t.Fatalf("quota asked %v, want one request larger than the %d byte upload", q.asked, len(csv))
	}
	if got := e.count(t, ioUser); got != 0 {
		t.Fatalf("%d contacts written, want 0", got)
	}
}

func TestImportRecordCap(t *testing.T) {
	e := newIOEnv(t, nil)
	csv := "First Name,Last Name\n" + strings.Repeat("A,B\n", contactsImportMaxRecords+1)
	rr := e.doImport(t, csv, "")
	want := fmt.Sprintf(i18n.T("en", "contacts.importErrTooMany"), contactsImportMaxRecords)
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(bodyMsg(rr), want) {
		t.Fatalf("status %d, want 422 with %q", rr.Code, want)
	}
	if got := e.count(t, ioUser); got != 0 {
		t.Fatalf("%d contacts written, want 0", got)
	}
}

func TestImportGroupTooLargeWritesNothing(t *testing.T) {
	e := newIOEnv(t, nil)
	var b strings.Builder
	var members strings.Builder
	n := store.MaxContactGroupMembers + 1
	for i := range n {
		fmt.Fprintf(&b, "dn: cn=P%d,mail=p%d@example.test\nobjectclass: person\ncn: P%d\nmail: p%d@example.test\n\n", i, i, i, i)
		fmt.Fprintf(&members, "member: cn=P%d,mail=p%d@example.test\n", i, i)
	}
	b.WriteString("dn: cn=Everyone\nobjectclass: groupOfNames\ncn: Everyone\n" + members.String())
	rr := e.doImport(t, b.String(), "")
	if rr.Code != http.StatusUnprocessableEntity || !strings.Contains(bodyMsg(rr), reason("groupTooLarge")) {
		t.Fatalf("status %d, want 422 naming the group", rr.Code)
	}
	if got := e.count(t, ioUser); got != 0 {
		t.Fatalf("%d contacts written, want 0", got)
	}
}

func TestImportRedirectsToBook(t *testing.T) {
	e := newIOEnv(t, nil)
	book := store.NewAddressBookID()
	if _, err := e.books.Create(ioUser, book, "Clients", ""); err != nil {
		t.Fatal(err)
	}
	rr := e.doImport(t, "First Name,Last Name\nA,B\n", book)
	if rr.Code != http.StatusSeeOther || !strings.HasPrefix(rr.Header().Get("Location"), contactsBookURL(book)) {
		t.Fatalf("status %d Location %q, want the book list", rr.Code, rr.Header().Get("Location"))
	}
	filed, _, _ := e.contacts.ListByBook(ioUser, book, 0, 0)
	if len(filed) != 1 {
		t.Fatalf("%d contacts in the book, want 1", len(filed))
	}
}

func TestImportIntoForeignBook(t *testing.T) {
	e := newIOEnv(t, nil)
	other := store.NewAddressBookID()
	if _, err := e.books.Create("u-someone-else", other, "Theirs", ""); err != nil {
		t.Fatal(err)
	}
	rr := e.doImport(t, "First Name,Last Name\nA,B\n", other)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
	if got := e.count(t, ioUser); got != 0 {
		t.Fatalf("%d contacts written, want 0", got)
	}
}

// Imported CSV contacts carry a UID, so a group can refer to them.
func TestImportGivesContactsUIDs(t *testing.T) {
	e := newIOEnv(t, nil)
	e.doImport(t, "First Name,Last Name\nA,B\n", "")
	list, _, _ := e.contacts.List(ioUser, 0, 0)
	if len(list) != 1 || !strings.Contains(string(list[0].VcardRaw), "UID:") {
		t.Fatalf("contact has no UID: %q", list[0].VcardRaw)
	}
}

func TestImportLDIFGroupMembers(t *testing.T) {
	e := newIOEnv(t, nil)
	in := "dn: cn=A,mail=a@example.test\nobjectclass: person\ncn: A\nmail: a@example.test\n\n" +
		"dn: cn=B,mail=b@example.test\nobjectclass: person\ncn: B\nmail: b@example.test\n\n" +
		"dn: cn=Team\nobjectclass: groupOfNames\ncn: Team\nmember: cn=B,mail=b@example.test\nmember: cn=Ghost,mail=g@example.test\n"
	if rr := e.doImport(t, in, ""); rr.Code != http.StatusSeeOther {
		t.Fatalf("status %d: %s", rr.Code, bodyMsg(rr))
	}
	groups, err := e.contacts.ListContactGroups(ioUser, "")
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups = %v, %v", groups, err)
	}
	res, err := e.contacts.ResolveContactGroup(ioUser, groups[0])
	if err != nil || len(res.Members) != 1 || res.Members[0].Contact.DisplayName != "B" {
		t.Fatalf("members = %+v, %v", res, err)
	}
}

// User-facing text in the import and export code comes from the catalogue, never
// from a literal passed to http.Error.
func TestContactIOHasNoLiteralHTTPErrors(t *testing.T) {
	src, err := os.ReadFile("contacts_io.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(src, []byte("http.Error(")) {
		t.Error("contacts_io.go passes literal text to http.Error; use bookFail or i18n.T")
	}
}
