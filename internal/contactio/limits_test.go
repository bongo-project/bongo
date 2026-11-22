package contactio

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-vcard"
)

// One attribute folded over many lines is read in linear time.
func TestLDIFFoldedValueIsLinear(t *testing.T) {
	in := "dn: cn=A\nobjectclass: person\ncn: A\nmail: a@example.test\ndescription: x\n" + strings.Repeat(" y\n", 200000)
	start := time.Now()
	res, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("parsing took %v, want linear time", d)
	}
	c, _ := vcard.NewDecoder(bytes.NewReader(res.Records[0].Raw)).Decode()
	if got := len(c.Value(vcard.FieldNote)); got != 1+200000 {
		t.Fatalf("note length %d, want %d", got, 1+200000)
	}
}

func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// A 10 MiB file of tiny rows is refused after max+1 records, not after building
// millions of cards.
func TestParseLimitStopsEarly(t *testing.T) {
	for name, row := range map[string]string{"records": "a,\n", "skipped rows": ",\n"} {
		in := []byte("First Name,Last Name\n" + strings.Repeat(row, (10<<20)/len(row)-8))
		var err error
		var d time.Duration
		mem := allocated(func() {
			start := time.Now()
			_, err = ParseLimit(in, 5000)
			d = time.Since(start)
		})
		if !errors.Is(err, ErrTooMany) {
			t.Fatalf("%s: err = %v, want ErrTooMany", name, err)
		}
		if d > 2*time.Second || mem > 100<<20 {
			t.Fatalf("%s: took %v and allocated %d MiB, want a quick refusal with bounded memory", name, d, mem>>20)
		}
	}
}

func TestParseLimitAllFormats(t *testing.T) {
	csvIn := "First Name,Last Name\nA,B\nC,D\nE,F\n"
	card := "BEGIN:VCARD\nVERSION:3.0\nFN:A\nEND:VCARD\n"
	person := func(i string) string {
		return "dn: cn=" + i + "\nobjectclass: person\ncn: " + i + "\n\n"
	}
	unit := "dn: ou=x\nobjectclass: organizationalUnit\nou: x\n\n"
	tests := []struct {
		name, in string
		max      int
		tooMany  bool
	}{
		{"csv over", csvIn, 2, true},
		{"csv at limit", csvIn, 3, false},
		{"vcard over", strings.Repeat(card, 3), 2, true},
		{"vcard at limit", strings.Repeat(card, 2), 2, false},
		{"ldif over", person("a") + person("b") + person("c"), 2, true},
		{"ldif at limit", person("a") + person("b"), 2, false},
		{"ldif skipped entries", strings.Repeat(unit, 20), 2, true},
	}
	for _, tc := range tests {
		_, err := ParseLimit([]byte(tc.in), tc.max)
		if errors.Is(err, ErrTooMany) != tc.tooMany || (!tc.tooMany && err != nil) {
			t.Errorf("%s: err = %v, tooMany want %v", tc.name, err, tc.tooMany)
		}
	}
}

// A bare CR in a value never reaches the card.
func TestBareCRIsNormalised(t *testing.T) {
	c := parseOne(t, "First Name,Last Name,Notes\nA,B,\"one\rtwo\"\n")
	if got := c.Value(vcard.FieldNote); got != "one\ntwo" {
		t.Errorf("CSV note = %q", got)
	}
	ldif := "dn: cn=A\nobjectclass: person\ncn: A\ndescription:: " + b64("one\rtwo") + "\n"
	c = parseOne(t, ldif)
	if got := c.Value(vcard.FieldNote); got != "one\ntwo" {
		t.Errorf("LDIF note = %q", got)
	}
}

func b64(s string) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out []byte
	b := []byte(s)
	for i := 0; i < len(b); i += 3 {
		var n uint
		chunk := b[i:min(i+3, len(b))]
		for j := range 3 {
			n <<= 8
			if j < len(chunk) {
				n |= uint(chunk[j])
			}
		}
		for j := range 4 {
			if j <= len(chunk) {
				out = append(out, tbl[(n>>(18-6*uint(j)))&63])
			} else {
				out = append(out, '=')
			}
		}
	}
	return string(out)
}

func TestLoneCRLineEnds(t *testing.T) {
	c := parseOne(t, "First Name,Last Name,E-mail 1 - Value\rAda,Lovelace,ada@example.test\r")
	if got := c.Value(vcard.FieldEmail); got != "ada@example.test" {
		t.Errorf("EMAIL = %q", got)
	}
	f, _, err := Detect([]byte("BEGIN:VCARD\rVERSION:3.0\rFN:A\rEND:VCARD\r"))
	if err != nil || f != FormatVCard {
		t.Errorf("vcard with CR ends: %v %v", f, err)
	}
	res, err := Parse([]byte("dn: cn=A\robjectclass: person\rcn: A\r"))
	if err != nil || len(res.Records) != 1 {
		t.Errorf("ldif with CR ends: %v %v", res, err)
	}
}

// Blank lines count as rows, as a spreadsheet numbers them.
func TestRowNumberCountsBlankLines(t *testing.T) {
	in := "First Name,Last Name\nA,B\n\n\nC,\"bad \" quote\n"
	_, err := Parse([]byte(in))
	var pe *ParseError
	if !errors.As(err, &pe) || pe.N != 5 {
		t.Fatalf("err = %v, want syntax error on row 5", err)
	}
	_, err = Parse([]byte("First Name,Last Name,Birthday\nA,B,\n\nC,D,nonsense\n"))
	if !errors.As(err, &pe) || pe.N != 4 || pe.Reason != ReasonBirthday {
		t.Fatalf("err = %v, want birthday error on row 4", err)
	}
}

func TestDuplicateDNResolvesToFirst(t *testing.T) {
	in := "dn: cn=A\nobjectclass: person\ncn: First\nmail: a1@x.test\n\n" +
		"dn: cn=A\nobjectclass: person\ncn: Second\nmail: a2@x.test\n\n" +
		"dn: cn=G\nobjectclass: groupOfNames\ncn: G\nmember: cn=A\n"
	res, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	g := res.Records[2].Group
	if len(g.Members) != 1 || g.Members[0] != 0 {
		t.Fatalf("members = %v, want the first person [0]", g.Members)
	}
}
