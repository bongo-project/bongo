package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func (e *ioEnv) export(user, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/contacts/export"+query, nil)
	ctx := middleware.WithUserContext(req.Context(), user, ioEmail)
	req = req.WithContext(middleware.WithLocale(ctx, "en"))
	rr := httptest.NewRecorder()
	e.h.ExportContacts(rr, req)
	return rr
}

func (e *ioEnv) add(t *testing.T, user, book, name, email string) {
	t.Helper()
	raw := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:" + name + "\r\nEMAIL:" + email + "\r\nEND:VCARD\r\n"
	c, err := parseVCardBlock([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	c.AddressbookId = book
	giveNewContactUID(c)
	if _, err := e.contacts.Create(user, c); err != nil {
		t.Fatal(err)
	}
}

func TestExportFormats(t *testing.T) {
	e := newIOEnv(t, nil)
	e.add(t, ioUser, "", "Ada Lovelace", "ada@example.test")
	tests := []struct {
		query, contentType, disposition, contains string
	}{
		{"", "text/vcard", `attachment; filename="contacts.vcf"`, "BEGIN:VCARD"},
		{"?format=vcard", "text/vcard", `attachment; filename="contacts.vcf"`, "BEGIN:VCARD"},
		{"?format=csv", "text/csv; charset=utf-8", `attachment; filename="contacts.csv"`, "ada@example.test"},
		{"?format=ldif", "text/x-ldif; charset=utf-8", `attachment; filename="contacts.ldif"`, "mail: ada@example.test"},
	}
	for _, tc := range tests {
		rr := e.export(ioUser, tc.query)
		if rr.Code != http.StatusOK {
			t.Fatalf("%q: status %d", tc.query, rr.Code)
		}
		if got := rr.Header().Get("Content-Type"); got != tc.contentType {
			t.Errorf("%q: Content-Type = %q, want %q", tc.query, got, tc.contentType)
		}
		if got := rr.Header().Get("Content-Disposition"); got != tc.disposition {
			t.Errorf("%q: Content-Disposition = %q, want %q", tc.query, got, tc.disposition)
		}
		if !strings.Contains(rr.Body.String(), tc.contains) {
			t.Errorf("%q: body lacks %q", tc.query, tc.contains)
		}
	}
}

func TestExportUnknownFormat(t *testing.T) {
	e := newIOEnv(t, nil)
	if rr := e.export(ioUser, "?format=xlsx"); rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
}

func TestExportByBook(t *testing.T) {
	e := newIOEnv(t, nil)
	book := store.NewAddressBookID()
	if _, err := e.books.Create(ioUser, book, "Clients", ""); err != nil {
		t.Fatal(err)
	}
	theirs := store.NewAddressBookID()
	if _, err := e.books.Create("u-other", theirs, "Theirs", ""); err != nil {
		t.Fatal(err)
	}
	e.add(t, ioUser, "", "Default Dora", "dora@example.test")
	e.add(t, ioUser, book, "Booked Bob", "bob@example.test")

	body := e.export(ioUser, "?format=csv&book="+book).Body.String()
	if !strings.Contains(body, "bob@example.test") || strings.Contains(body, "dora@example.test") {
		t.Errorf("book export = %q, want only the book's contact", body)
	}
	body = e.export(ioUser, "?format=csv&book=default").Body.String()
	if !strings.Contains(body, "dora@example.test") || strings.Contains(body, "bob@example.test") {
		t.Errorf("default book export = %q", body)
	}
	body = e.export(ioUser, "?format=csv").Body.String()
	if !strings.Contains(body, "dora@example.test") || !strings.Contains(body, "bob@example.test") {
		t.Errorf("all-books export = %q", body)
	}
	for _, bad := range []string{theirs, "nonsense", strings.Repeat("0", 32)} {
		if rr := e.export(ioUser, "?book="+bad); rr.Code != http.StatusNotFound {
			t.Errorf("book %q: status %d, want 404", bad, rr.Code)
		}
	}
}

func TestCSVExportSkipsGroups(t *testing.T) {
	e := newIOEnv(t, nil)
	e.add(t, ioUser, "", "Ada Lovelace", "ada@example.test")
	guid := store.NewContactGUID()
	uid, _ := store.UUIDFromGUID(guid)
	card, _ := store.NewContactGroupCard("Mathematicians", uid)
	if _, err := e.contacts.Create(ioUser, &bongov1.Contact{Guid: guid, DisplayName: "Mathematicians", VcardRaw: card}); err != nil {
		t.Fatal(err)
	}
	body := e.export(ioUser, "?format=csv").Body.String()
	if strings.Contains(body, "Mathematicians") {
		t.Errorf("CSV export contains the group: %q", body)
	}
	if body = e.export(ioUser, "?format=ldif").Body.String(); !strings.Contains(body, "groupOfNames") {
		t.Errorf("LDIF export lacks the group: %q", body)
	}
}

// A contact whose name is a formula is exported with the apostrophe.
func TestCSVExportNeutralisesFormulaName(t *testing.T) {
	e := newIOEnv(t, nil)
	e.add(t, ioUser, "", "=1+1", "x@example.test")
	body := e.export(ioUser, "?format=csv").Body.String()
	if !strings.Contains(body, "'=1+1") || strings.Contains(body, ",=1+1") {
		t.Errorf("body = %q, want the formula neutralised", body)
	}
}
