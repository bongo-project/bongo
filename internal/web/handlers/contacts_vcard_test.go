package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

func TestBuildVCardFromForm(t *testing.T) {
	// Start from an existing vCard carrying an unmodelled property (X-CUSTOM) to
	// verify it is preserved across a full-form save.
	existing := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Old\r\nX-CUSTOM:keepme\r\nEND:VCARD\r\n"

	form := url.Values{
		"display_name": {"Jane Doe"},
		"email":        {"jane@home.com", "jane@work.com"},
		"email_type":   {"home", "work"},
		"phone":        {"+1000", ""}, // blank phone dropped
		"phone_type":   {"cell", "home"},
		"company":      {"Acme"},
		"title":        {"CEO"},
		"adr_street":   {"1 High St"},
		"adr_city":     {"Metropolis"},
		"adr_region":   {"NY"},
		"adr_postal":   {"12345"},
		"adr_country":  {"USA"},
		"birthday":     {"1985-05-05"},
		"website":      {"https://example.com"},
		"nickname":     {"JD"},
		"timezone":     {"America/New_York"},
		"notes":        {"hello"},
	}
	r := httptest.NewRequest(http.MethodPost, "/contacts", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	c := &bongov1.Contact{VcardRaw: []byte(existing)}
	buildVCardFromForm(c, r)

	card, err := vcard.NewDecoder(bytes.NewReader(c.VcardRaw)).Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got := card.PreferredValue(vcard.FieldFormattedName); got != "Jane Doe" {
		t.Errorf("FN = %q", got)
	}
	if emails := card.Values(vcard.FieldEmail); len(emails) != 2 || emails[0] != "jane@home.com" || emails[1] != "jane@work.com" {
		t.Errorf("emails = %v", emails)
	}
	if phones := card.Values(vcard.FieldTelephone); len(phones) != 1 || phones[0] != "+1000" {
		t.Errorf("phones = %v (blank should be dropped)", phones)
	}
	for field, want := range map[string]string{
		vcard.FieldOrganization: "Acme",
		vcard.FieldTitle:        "CEO",
		vcard.FieldBirthday:     "1985-05-05",
		vcard.FieldURL:          "https://example.com",
		vcard.FieldNickname:     "JD",
		vcard.FieldTimezone:     "America/New_York",
		vcard.FieldNote:         "hello",
	} {
		if got := card.Value(field); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
	if addrs := card.Addresses(); len(addrs) != 1 || addrs[0].Locality != "Metropolis" || addrs[0].Country != "USA" {
		t.Errorf("address = %v", card.Addresses())
	}
	// Unmodelled property preserved.
	if got := card.Value("X-CUSTOM"); got != "keepme" {
		t.Errorf("X-CUSTOM = %q, want keepme (must be preserved)", got)
	}
	// Derived quick fields.
	if c.DisplayName != "Jane Doe" || c.Email != "jane@home.com" || c.Phone != "+1000" || c.Company != "Acme" || c.Notes != "hello" {
		t.Errorf("quick fields not synced: %+v", c)
	}
}

const richVCard = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"FN:Old Name\r\n" +
	"EMAIL;TYPE=home:home@example.com\r\n" +
	"EMAIL;TYPE=work:work@example.com\r\n" +
	"TEL:+1000\r\n" +
	"ADR:;;123 Main St;Springfield;;12345;Country\r\n" +
	"BDAY:1990-01-01\r\n" +
	"TITLE:Engineer\r\n" +
	"END:VCARD\r\n"

// Editing the quick fields must update the primary values in the stored vCard
// while preserving every other property (extra email, address, birthday, title).
func TestSyncQuickFieldsToVCardPreservesExtras(t *testing.T) {
	c := &bongov1.Contact{
		Guid:        "g1",
		DisplayName: "New Name",
		Email:       "newhome@example.com", // edited primary email
		Phone:       "+1000",
		Company:     "Acme",
		VcardRaw:    []byte(richVCard),
	}

	syncQuickFieldsToVCard(c)

	card, err := vcard.NewDecoder(bytes.NewReader(c.VcardRaw)).Decode()
	if err != nil {
		t.Fatalf("decode result vcard: %v", err)
	}

	if got := card.PreferredValue(vcard.FieldFormattedName); got != "New Name" {
		t.Errorf("FN = %q, want New Name", got)
	}
	emails := card.Values(vcard.FieldEmail)
	if len(emails) != 2 {
		t.Fatalf("emails = %v, want 2 (primary updated, other preserved)", emails)
	}
	if emails[0] != "newhome@example.com" {
		t.Errorf("primary email = %q, want newhome@example.com", emails[0])
	}
	if emails[1] != "work@example.com" {
		t.Errorf("second email = %q, want it preserved", emails[1])
	}
	if got := card.PreferredValue(vcard.FieldOrganization); got != "Acme" {
		t.Errorf("ORG = %q, want Acme", got)
	}
	// Untouched extras must survive.
	if len(card.Addresses()) == 0 || card.Addresses()[0].Locality != "Springfield" {
		t.Error("postal address was lost")
	}
	if got := card.Value(vcard.FieldBirthday); got != "1990-01-01" {
		t.Errorf("BDAY = %q, want it preserved", got)
	}
	if got := card.Value(vcard.FieldTitle); got != "Engineer" {
		t.Errorf("TITLE = %q, want it preserved", got)
	}
}

// A web-created contact (no prior vCard) still produces a valid vCard.
func TestSyncQuickFieldsToVCardFromEmpty(t *testing.T) {
	c := &bongov1.Contact{DisplayName: "Fresh", Email: "fresh@example.com"}
	syncQuickFieldsToVCard(c)
	if len(c.VcardRaw) == 0 {
		t.Fatal("expected a generated vCard")
	}
	card, err := vcard.NewDecoder(bytes.NewReader(c.VcardRaw)).Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if card.PreferredValue(vcard.FieldFormattedName) != "Fresh" ||
		card.PreferredValue(vcard.FieldEmail) != "fresh@example.com" {
		t.Errorf("generated vCard missing fields: %s", c.VcardRaw)
	}
}
