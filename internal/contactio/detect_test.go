package contactio

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16le(s string, bom bool) []byte {
	var out []byte
	if bom {
		out = append(out, 0xFF, 0xFE)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func TestDetect(t *testing.T) {
	googleCSV := "First Name,Last Name,E-mail 1 - Value\nAda,Lovelace,ada@example.test\n"
	tests := []struct {
		name    string
		in      []byte
		want    Format
		wantErr error
		contain string
	}{
		{"vcard with BOM", []byte("\xEF\xBB\xBFBEGIN:VCARD\nVERSION:3.0\nFN:A\nEND:VCARD\n"), FormatVCard, nil, "BEGIN:VCARD"},
		{"utf-16le outlook csv", utf16le("First Name,Last Name,E-mail Address\r\nZoe,Mueller,z@example.test\r\n", true), FormatCSV, nil, "Zoe,Mueller"},
		{"windows-1252 csv", []byte("First Name,Last Name,E-mail Address\nA,M\xFCller,a@example.test\n"), FormatCSV, nil, "Müller"},
		{"ldif with comment first", []byte("# exported\ndn: cn=A\ncn: A\n"), FormatLDIF, nil, ""},
		{"ldif with version", []byte("version: 1\n\ndn: cn=A\ncn: A\n"), FormatLDIF, nil, ""},
		{"semicolon csv", []byte("Vorname;First Name;Last Name;E-mail Address\n;A;B;a@example.test\n"), FormatCSV, nil, ""},
		{"tab csv", []byte("First Name\tLast Name\tE-mail Address\nA\tB\ta@example.test\n"), FormatCSV, nil, ""},
		{"csv content, any name", []byte(googleCSV), FormatCSV, nil, ""},
		{"empty", nil, "", ErrFormat, ""},
		{"blank lines only", []byte("\n\n  \n"), "", ErrFormat, ""},
		{"json", []byte(`{"contacts": []}`), "", ErrFormat, ""},
		{"unknown headers", []byte("Foo,Bar,Baz\n1,2,3\n"), "", ErrFormat, ""},
		{"german outlook headers", []byte("Vorname,Nachname,E-Mail-Adresse,Firma\nA,B,a@example.test,X\n"), "", ErrFormat, ""},
		{"only one known header", []byte("First Name,Foo\nA,B\n"), "", ErrFormat, ""},
		{"nul without bom", []byte("First\x00Name,Last Name\n"), "", ErrEncoding, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, text, err := Detect(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("format = %q, want %q", got, tc.want)
			}
			if tc.contain != "" && !strings.Contains(string(text), tc.contain) {
				t.Fatalf("decoded text %q lacks %q", text, tc.contain)
			}
		})
	}
}
