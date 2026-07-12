package handlers

import (
	"bytes"
	"testing"

	"github.com/emersion/go-vcard"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

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
