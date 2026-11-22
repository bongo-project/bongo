package contactio

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
)

func parseOne(t *testing.T, in string) vcard.Card {
	t.Helper()
	res, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(res.Records) != 1 {
		t.Fatalf("%d records, want 1", len(res.Records))
	}
	card, err := vcard.NewDecoder(bytes.NewReader(res.Records[0].Raw)).Decode()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return card
}

func typesOfValue(card vcard.Card, field, value string) []string {
	for _, f := range card[field] {
		if f.Value == value {
			return f.Params[vcard.ParamType]
		}
	}
	return nil
}

func TestGoogleNewHeaders(t *testing.T) {
	in := "First Name,Last Name,Organization Name,Organization Title,Organization Department,Birthday,E-mail 1 - Label,E-mail 1 - Value,Phone 1 - Label,Phone 1 - Value,Address 1 - Label,Address 1 - Street,Address 1 - City,Address 1 - Postal Code,Address 1 - Country,Website 1 - Value\n" +
		"Ada,Lovelace,Analytical,Programmer,Engines,1815-12-10,* Home ::: Work,ada@home.test ::: ada@work.test,Mobile ::: Work Fax,+44 1 ::: +44 2,Home,1 Street,London,N1,UK,https://ada.example.test\n"
	c := parseOne(t, in)
	if got := c.Value(vcard.FieldFormattedName); got != "Ada Lovelace" {
		t.Errorf("FN = %q", got)
	}
	if got := c.Value(vcard.FieldOrganization); got != "Analytical;Engines" {
		t.Errorf("ORG = %q", got)
	}
	if got := c.Value(vcard.FieldTitle); got != "Programmer" {
		t.Errorf("TITLE = %q", got)
	}
	if got := c.Value(vcard.FieldBirthday); got != "1815-12-10" {
		t.Errorf("BDAY = %q", got)
	}
	if got := strings.Join(typesOfValue(c, vcard.FieldEmail, "ada@home.test"), ","); got != "home,pref" {
		t.Errorf("home email types = %q, want home,pref", got)
	}
	if got := strings.Join(typesOfValue(c, vcard.FieldEmail, "ada@work.test"), ","); got != "work" {
		t.Errorf("work email types = %q", got)
	}
	if got := strings.Join(typesOfValue(c, vcard.FieldTelephone, "+44 1"), ","); got != "cell" {
		t.Errorf("mobile types = %q", got)
	}
	if got := strings.Join(typesOfValue(c, vcard.FieldTelephone, "+44 2"), ","); got != "fax,work" {
		t.Errorf("work fax types = %q", got)
	}
	a := c.Address()
	if a == nil || a.StreetAddress != "1 Street" || a.Locality != "London" || a.Country != "UK" {
		t.Errorf("address = %+v", a)
	}
	if got := strings.Join(a.Params[vcard.ParamType], ","); got != "home" {
		t.Errorf("address types = %q", got)
	}
}

func TestGoogleLegacyHeaders(t *testing.T) {
	in := "Name,Given Name,Additional Name,Family Name,Group Membership,E-mail 1 - Type,E-mail 1 - Value,Organization 1 - Name,Organization 1 - Title\n" +
		"Ada L,Ada,Augusta,Lovelace,* myContacts,* Work,ada@example.test,Engines,Countess\n"
	c := parseOne(t, in)
	if got := c.Value(vcard.FieldFormattedName); got != "Ada L" {
		t.Errorf("FN = %q", got)
	}
	if n := c.Name(); n == nil || n.GivenName != "Ada" || n.AdditionalName != "Augusta" || n.FamilyName != "Lovelace" {
		t.Errorf("N = %+v", n)
	}
	if got := c.Value(vcard.FieldOrganization); got != "Engines" {
		t.Errorf("ORG = %q", got)
	}
	if got := strings.Join(typesOfValue(c, vcard.FieldEmail, "ada@example.test"), ","); got != "work,pref" {
		t.Errorf("types = %q", got)
	}
}

// Outlook's Title is the honorific; the job title is Job Title.
func TestOutlookTitleIsPrefix(t *testing.T) {
	in := "Title,First Name,Last Name,Job Title,Company,E-mail Address\nDr,Grace,Hopper,Rear Admiral,Navy,grace@example.test\n"
	c := parseOne(t, in)
	if got := c.Value(vcard.FieldTitle); got != "Rear Admiral" {
		t.Errorf("TITLE = %q, want the job title", got)
	}
	if n := c.Name(); n == nil || n.HonorificPrefix != "Dr" {
		t.Errorf("N = %+v, want prefix Dr", n)
	}
	if got := c.Value(vcard.FieldFormattedName); got != "Dr Grace Hopper" {
		t.Errorf("FN = %q", got)
	}
}

func TestOutlookPhonesAndAddresses(t *testing.T) {
	in := "First Name,Last Name,E-mail Address,E-mail 2 Address,Business Phone,Home Phone,Mobile Phone,Business Fax,Business Street,Business Street 2,Business City,Business State,Business Postal Code,Business Country/Region,Home Street,Home City\n" +
		"A,B,a@x.test,b@x.test,111,222,333,444,Main St,Suite 4,Town,ST,12345,DE,Home Rd,Village\n"
	c := parseOne(t, in)
	for value, want := range map[string]string{"111": "work", "222": "home", "333": "cell", "444": "fax,work"} {
		if got := strings.Join(typesOfValue(c, vcard.FieldTelephone, value), ","); got != want {
			t.Errorf("phone %s types = %q, want %q", value, got, want)
		}
	}
	if len(c[vcard.FieldEmail]) != 2 {
		t.Errorf("%d emails, want 2", len(c[vcard.FieldEmail]))
	}
	var work, home *vcard.Address
	for _, a := range c.Addresses() {
		switch strings.Join(a.Params[vcard.ParamType], ",") {
		case "work":
			work = a
		case "home":
			home = a
		}
	}
	if work == nil || work.StreetAddress != "Main St\nSuite 4" || work.Locality != "Town" || work.Region != "ST" || work.PostalCode != "12345" || work.Country != "DE" {
		t.Errorf("work address = %+v", work)
	}
	if home == nil || home.StreetAddress != "Home Rd" {
		t.Errorf("home address = %+v", home)
	}
}

func TestBirthdayForms(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"0/0/00", "", true},
		{"1/1/1601", "", true},
		{"1980-02-29", "1980-02-29", true},
		{"--03-04", "--03-04", true},
		{"--0304", "--03-04", true},
		{"19800304", "1980-03-04", true},
		{"3/4/1980", "1980-03-04", true},
		{"25/12/1980", "1980-12-25", true},
		{"4.3.1980", "1980-03-04", true},
		{"1980-01-01T00:00:00Z", "1980-01-01", true},
		{"1981-02-29", "", false},
		{"yesterday", "", false},
		{"13/13/1980", "", false},
	}
	for _, tc := range tests {
		got, ok := normaliseBirthday(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("normaliseBirthday(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	_, err := Parse([]byte("First Name,Last Name,Birthday\nA,B,1980-01-01\nC,D,not a date\n"))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.N != 3 || pe.Reason != ReasonBirthday {
		t.Fatalf("err = %v, want birthday error on row 3", err)
	}
}

func TestHeaderCaseInsensitive(t *testing.T) {
	c := parseOne(t, "FIRST NAME,last name,E-MAIL 1 - value\nAda,Lovelace,ada@example.test\n")
	if got := c.Value(vcard.FieldEmail); got != "ada@example.test" {
		t.Errorf("EMAIL = %q", got)
	}
}

// A multi-line note counts as one row, as a spreadsheet numbers it.
func TestRowNumberIsSpreadsheetRow(t *testing.T) {
	in := "First Name,Last Name,Notes\nA,B,\"line one\nline two\"\nC,D,\"bad \" quote\n"
	_, err := Parse([]byte(in))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Format != FormatCSV || pe.N != 3 || pe.Reason != ReasonSyntax {
		t.Fatalf("err = %v, want syntax error on row 3", err)
	}
}

func TestTooManyFieldsIsAnError(t *testing.T) {
	_, err := Parse([]byte("First Name,Last Name\nA,B\nC,D,extra\n"))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.N != 3 || pe.Reason != ReasonFields {
		t.Fatalf("err = %v, want fields error on row 3", err)
	}
}

func TestEmptyRowsAreSkipped(t *testing.T) {
	res, err := Parse([]byte("First Name,Last Name,E-mail 1 - Value\nA,B,a@x.test\n,,\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 1 || res.Skipped != 1 {
		t.Fatalf("records %d skipped %d, want 1 and 1", len(res.Records), res.Skipped)
	}
}

func TestSemicolonInStructuredComponent(t *testing.T) {
	c := parseOne(t, "First Name,Last Name,Organization Name,Address 1 - Street\nA,Smith;Jones,Acme;Corp,1 Main;Street\n")
	if n := c.Name(); n.FamilyName != "Smith,Jones" || n.GivenName != "A" {
		t.Errorf("N = %+v", n)
	}
	if got := c.Value(vcard.FieldOrganization); got != "Acme,Corp" {
		t.Errorf("ORG = %q", got)
	}
	if a := c.Address(); a == nil || a.StreetAddress != "1 Main,Street" {
		t.Errorf("ADR = %+v", a)
	}
}

func TestNeutralise(t *testing.T) {
	for _, first := range []string{"=", "+", "-", "@", "\t", "\r"} {
		in := first + "cmd|' /c calc'!A1"
		if got := neutralise(in); got != "'"+in {
			t.Errorf("neutralise(%q) = %q, want an apostrophe prefix", in, got)
		}
	}
	for _, safe := range []string{"", "plain", "a=b", "x+y", "'quoted", " =lead"} {
		if got := neutralise(safe); got != safe {
			t.Errorf("neutralise(%q) = %q, want it unchanged", safe, got)
		}
	}
}

func TestWriteCSVNeutralises(t *testing.T) {
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "3.0")
	card.SetValue(vcard.FieldFormattedName, "=HYPERLINK(\"http://evil.test\")")
	card.Add(vcard.FieldTelephone, &vcard.Field{Value: "+49 30 1234"})
	card.Add(vcard.FieldEmail, &vcard.Field{Value: "@evil.test"})
	card.SetValue(vcard.FieldNote, "-2+3 and a=b mid-cell")
	var buf bytes.Buffer
	if err := WriteCSV(&buf, []vcard.Card{card}); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	if !bytes.HasPrefix(out, []byte("\xEF\xBB\xBF")) {
		t.Error("no UTF-8 byte order mark")
	}
	recs, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(out, []byte("\xEF\xBB\xBF")))).ReadAll()
	if err != nil {
		t.Fatalf("output does not re-parse: %v", err)
	}
	cells := strings.Join(recs[1], "\x00")
	for _, want := range []string{"'=HYPERLINK(", "'+49 30 1234", "'@evil.test", "'-2+3 and a=b mid-cell"} {
		if !strings.Contains(cells, want) {
			t.Errorf("row lacks %q: %q", want, recs[1])
		}
	}
	// Nothing in the data row starts with a formula character.
	for _, c := range recs[1] {
		if c != "" && strings.ContainsRune("=+-@\t\r", rune(c[0])) {
			t.Errorf("cell %q starts with a formula character", c)
		}
	}
}

func TestImportStripsOwnNeutralisation(t *testing.T) {
	c := parseOne(t, "First Name,Last Name,Phone 1 - Value,Notes\nA,B,'+49 30 1,'hello\n")
	if got := c.Value(vcard.FieldTelephone); got != "+49 30 1" {
		t.Errorf("TEL = %q, want the apostrophe removed", got)
	}
	if got := c.Value(vcard.FieldNote); got != "'hello" {
		t.Errorf("NOTE = %q, want the user's apostrophe kept", got)
	}
}
