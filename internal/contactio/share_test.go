package contactio

import (
	"bytes"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"
)

const privateCard = "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:urn:uuid:secret-uid\r\nFN:Jane Doe\r\nN:Doe;Jane;;;\r\n" +
	"NICKNAME:JD\r\nORG:Acme;Sales\r\nTITLE:Engineer\r\nROLE:Lead\r\n" +
	"EMAIL;TYPE=work,pref:jane@example.com\r\nitem1.EMAIL;TYPE=home:jane@home.example\r\nitem1.X-ABLabel:Private label\r\n" +
	"TEL;TYPE=cell:+31 6 1234\r\nADR;TYPE=home;GEO=\"geo:52.37,4.89\";X-SECRET=hunter2:;;1 High St;Town;;1234;NL\r\nEMAIL;TYPE=other;X-NOTE=privatestuff:o@example.com\r\nURL:https://jane.example\r\nBDAY:1990-02-03\r\n" +
	"NOTE:SECRET-NOTE-TEXT\r\nREV:20240101T000000Z\r\nPRODID:-//x//\r\nCATEGORIES:family,vip\r\n" +
	"X-SOCIALPROFILE:twitter:janesecret\r\nPHOTO;ENCODING=b;TYPE=JPEG:AAAA\r\nMEMBER:urn:uuid:other\r\nKEY:abc\r\nGEO:1;2\r\n" +
	"X-ADDRESSBOOKSERVER-KIND:group\r\nEND:VCARD\r\n"

func decodeCard(t *testing.T, raw string) vcard.Card {
	t.Helper()
	c, err := vcard.NewDecoder(strings.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func encodeCard(t *testing.T, c vcard.Card) string {
	t.Helper()
	var b bytes.Buffer
	if err := vcard.NewEncoder(&b).Encode(c); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestShareCardDropsPrivateFields(t *testing.T) {
	out := encodeCard(t, ShareCard(decodeCard(t, privateCard)))
	for _, bad := range []string{"SECRET-NOTE-TEXT", "NOTE", "secret-uid", "UID", "REV", "PRODID", "CATEGORIES", "family",
		"X-ABLabel", "Private label", "X-SOCIALPROFILE", "janesecret", "PHOTO", "AAAA", "MEMBER", "KEY", "GEO", "X-ADDRESSBOOKSERVER", "GEO", "X-SECRET", "X-NOTE", "hunter2", "privatestuff", "52.37"} {
		if strings.Contains(out, bad) {
			t.Errorf("shared card contains %q:\n%s", bad, out)
		}
	}
	for _, good := range []string{"FN:Jane Doe", "N:Doe;Jane", "NICKNAME:JD", "ORG:Acme", "TITLE:Engineer", "ROLE:Lead",
		"jane@example.com", "TYPE=work", "jane@home.example", "item1.EMAIL", "+31 6 1234", "TYPE=cell", "ADR;TYPE=home", "1 High St",
		"URL:https://jane.example", "BDAY:1990-02-03", "VERSION:3.0"} {
		if !strings.Contains(out, good) {
			t.Errorf("shared card misses %q:\n%s", good, out)
		}
	}
}

func TestShareCardKeepsStoredVersion(t *testing.T) {
	out := encodeCard(t, ShareCard(decodeCard(t, "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:A\r\nEND:VCARD\r\n")))
	if !strings.Contains(out, "VERSION:4.0") {
		t.Errorf("version not kept:\n%s", out)
	}
	if strings.Contains(out, "\r\nN:") {
		t.Errorf("4.0 card must not gain an N:\n%s", out)
	}
}

func TestShareCardDeterministic(t *testing.T) {
	a := encodeCard(t, ShareCard(decodeCard(t, privateCard)))
	for range 20 {
		if b := encodeCard(t, ShareCard(decodeCard(t, privateCard))); a != b {
			t.Fatalf("output differs between runs:\n%s\n---\n%s", a, b)
		}
	}
}

func TestShareCardAddsNFor30(t *testing.T) {
	out := encodeCard(t, ShareCard(decodeCard(t, "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nEND:VCARD\r\n")))
	if !strings.Contains(out, "\r\nN:") {
		t.Errorf("3.0 card needs N:\n%s", out)
	}
	if out2 := encodeCard(t, ShareCard(vcard.Card{"FN": {{Value: "A"}}})); !strings.Contains(out2, "VERSION:3.0") {
		t.Errorf("missing VERSION must become 3.0:\n%s", out2)
	}
}

func TestShareCardStripsLineBreaks(t *testing.T) {
	c := vcard.Card{}
	c.SetValue(vcard.FieldVersion, "3.0")
	c.SetValue(vcard.FieldFormattedName, "Jane\r\nNOTE:injected\x00\x07")
	c.Add(vcard.FieldEmail, &vcard.Field{Value: "a@example.com\nTEL:1", Params: vcard.Params{"TYPE": {"work\r\nX-EVIL:1"}}})
	c.Add(vcard.FieldTelephone, &vcard.Field{Value: "1", Group: "g\rNOTE", Params: vcard.Params{"TYPE\r": {"cell\rX-EVIL:1"}}})
	out := encodeCard(t, ShareCard(c))
	if strings.Count(out, "\r") != strings.Count(out, "\r\n") {
		t.Errorf("a bare CR survives:\n%q", out)
	}
	for _, line := range strings.Split(out, "\r\n") {
		if strings.HasPrefix(line, "NOTE") || strings.HasPrefix(line, "TEL") || strings.HasPrefix(line, "X-EVIL") {
			t.Errorf("injected line %q in:\n%s", line, out)
		}
	}
	if strings.ContainsAny(out, "\x00\x07") {
		t.Error("control characters survive")
	}
}

func TestCardFilename(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Jane Doe", "Jane Doe.vcf"},
		{"a.b_c-d 1", "a.b_c-d 1.vcf"},
		{"Zoë", "contact.vcf"},
		{`Jane "JD" Doe`, "contact.vcf"},
		{"a/b", "contact.vcf"},
		{"a\r\nb", "contact.vcf"},
		{"", "contact.vcf"},
		{strings.Repeat("a", 60), strings.Repeat("a", 60) + ".vcf"},
		{strings.Repeat("a", 61), "contact.vcf"},
	}
	for _, tc := range tests {
		if got := CardFilename(tc.in); got != tc.want {
			t.Errorf("CardFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
