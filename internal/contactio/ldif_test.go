package contactio

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
)

const thunderbirdLDIF = `# Thunderbird address book
dn:: Y249WmXDqyBNw7xsbGVyLG1haWw9emVAZXhhbXBsZS50ZXN0
objectclass: top
objectclass: person
objectclass: inetOrgPerson
objectclass: mozillaAbPersonAlpha
cn:: WmXDqyBNw7xsbGVy
givenName: Zoe
sn: Mueller
mail: ze@example.test
telephoneNumber: +49 30 1234567
o: Mueller 
 GmbH
description: A very long note that
 continues on the next line

dn: cn=Org,dc=example
objectclass: organizationalUnit
ou: Org
`

func TestThunderbirdPerson(t *testing.T) {
	res, err := Parse([]byte(thunderbirdLDIF))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 1 || res.Skipped != 1 {
		t.Fatalf("records %d skipped %d, want 1 and 1", len(res.Records), res.Skipped)
	}
	c, _ := vcard.NewDecoder(bytes.NewReader(res.Records[0].Raw)).Decode()
	if got := c.Value(vcard.FieldFormattedName); got != "Zeë Müller" {
		t.Errorf("FN = %q", got)
	}
	if got := c.Value(vcard.FieldOrganization); got != "Mueller GmbH" {
		t.Errorf("ORG = %q (folded line)", got)
	}
	if got := c.Value(vcard.FieldNote); got != "A very long note thatcontinues on the next line" {
		t.Errorf("NOTE = %q", got)
	}
	if got := strings.Join(typesOfValue(c, vcard.FieldTelephone, "+49 30 1234567"), ","); got != "work" {
		t.Errorf("phone types = %q", got)
	}
	if ref := res.Records[0].Ref; ref.N != 1 || ref.Line != 2 {
		t.Errorf("ref = %+v", ref)
	}
}

func TestGroupOfNamesMembers(t *testing.T) {
	in := "dn: cn=A,mail=a@x.test\nobjectclass: person\ncn: A\nmail: a@x.test\n\n" +
		"dn: cn=B,mail=b@x.test\nobjectclass: person\ncn: B\nmail: b@x.test\n\n" +
		"dn: cn=Team\nobjectclass: groupOfNames\ncn: Team\nmember: cn=B, mail=b@x.test\nmember: cn=Gone,mail=gone@x.test\nmember: cn=A,mail=a@x.test\n"
	res, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Records) != 3 || res.Records[2].Group == nil {
		t.Fatalf("records = %+v", res.Records)
	}
	g := res.Records[2].Group
	if g.Name != "Team" || len(g.Members) != 2 || g.Members[0] != 1 || g.Members[1] != 0 {
		t.Errorf("group = %+v, want members [1 0] with the unmatched one dropped", g)
	}
}

func TestRefusesChangetype(t *testing.T) {
	_, err := Parse([]byte("dn: cn=A\nchangetype: delete\n"))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Reason != ReasonChange || pe.N != 1 || pe.Line != 2 {
		t.Fatalf("err = %v, want change-record error at entry 1 line 2", err)
	}
	// A later entry is named by its own number.
	_, err = Parse([]byte("dn: cn=A\ncn: A\nobjectclass: person\n\ndn: cn=B\nchangetype: modify\n"))
	if !errors.As(err, &pe) || pe.N != 2 {
		t.Fatalf("err = %v, want entry 2", err)
	}
}

func TestRefusesURLValue(t *testing.T) {
	_, err := Parse([]byte("dn: cn=A\ncn: A\njpegPhoto:< http://127.0.0.1:1/secret\n"))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Reason != ReasonURL {
		t.Fatalf("err = %v, want a URL refusal", err)
	}
}

func TestLDIFBadBase64AndFold(t *testing.T) {
	var pe *ParseError
	if _, err := Parse([]byte("dn: cn=A\ncn:: !!!notbase64\n")); !errors.As(err, &pe) || pe.Reason != ReasonBase64 {
		t.Errorf("bad base64: err = %v", err)
	}
	if _, err := Parse([]byte("dn: cn=A\ncn:: " + base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe}) + "\n")); !errors.As(err, &pe) || pe.Reason != ReasonBase64 {
		t.Errorf("invalid UTF-8: err = %v", err)
	}
	if _, err := Parse([]byte("version: 1\n\n dn: cn=A\n")); !errors.As(err, &pe) {
		t.Errorf("fold before any attribute: err = %v", err)
	}
}

func TestWriteLDIFSafeString(t *testing.T) {
	var buf bytes.Buffer
	for _, v := range []string{" lead", ":colon", "<lt", "trail ", "Zoë", "two\nlines", "plain"} {
		buf.Reset()
		if err := ldifLine(&buf, "description", v); err != nil {
			t.Fatal(err)
		}
		wantB64 := v != "plain"
		if got := strings.HasPrefix(buf.String(), "description:: "); got != wantB64 {
			t.Errorf("%q: base64 = %v, want %v (%q)", v, got, wantB64, buf.String())
		}
	}
	buf.Reset()
	long := strings.Repeat("a", 200)
	if err := ldifLine(&buf, "description", long); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("not folded: %q", lines)
	}
	for i, l := range lines {
		if len(l) > 76 || (i > 0 && !strings.HasPrefix(l, " ")) {
			t.Errorf("line %d = %q: too long or not a continuation", i, l)
		}
	}
	if got := escapeDN("Smith, John+Jr"); got != `Smith\, John\+Jr` {
		t.Errorf("escapeDN = %q", got)
	}
	if got := dnFor("A", "", map[string]bool{"cn=a": true}, "7"); got != "cn=A+uid=7" {
		t.Errorf("duplicate dn = %q", got)
	}
}
