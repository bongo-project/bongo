package store

import (
	"slices"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestContactAddressesFromVCard(t *testing.T) {
	tests := []struct {
		name  string
		email string
		vcard string
		want  []string
	}{
		{
			name:  "grouped property from Apple Contacts",
			vcard: "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nitem1.EMAIL;type=INTERNET:Grouped@Example.com\r\nitem1.X-ABLabel:other\r\nEND:VCARD\r\n",
			want:  []string{"grouped@example.com"},
		},
		{
			name:  "folded line",
			vcard: "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nEMAIL;TYPE=work:folded.long\r\n @example.com\r\nEND:VCARD\r\n",
			want:  []string{"folded.long@example.com"},
		},
		{
			name:  "mailto value",
			vcard: "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:A\r\nEMAIL:MAILTO:X@Y.example\r\nEND:VCARD\r\n",
			want:  []string{"x@y.example"},
		},
		{
			name:  "two emails in one card plus the Email field",
			email: "Field@Example.com",
			vcard: "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nEMAIL:one@example.com\r\nitem2.EMAIL:two@example.com\r\nEND:VCARD\r\n",
			want:  []string{"field@example.com", "one@example.com", "two@example.com"},
		},
		{
			name: "two cards in one blob",
			vcard: "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nEMAIL:first@example.com\r\nEND:VCARD\r\n" +
				"BEGIN:VCARD\r\nVERSION:3.0\r\nFN:B\r\nEMAIL:second@example.com\r\nEND:VCARD\r\n",
			want: []string{"first@example.com", "second@example.com"},
		},
		{
			name:  "lookalike properties are ignored",
			vcard: "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nEMAILX:no@example.com\r\nNOTE:email:fake@example.com\r\nEND:VCARD\r\n",
			want:  nil,
		},
		{
			name:  "malformed card keeps its grouped address and does not panic",
			vcard: "BEGIN:VCARD\r\nitem1.EMAIL:rescued@example.com\r\nthis line has no colon\r\n",
			want:  []string{"rescued@example.com"},
		},
		{
			name:  "garbage yields nothing",
			vcard: "\x00\x01not a vcard at all",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps, err := NewPebbleStore(t.TempDir() + "/pebble")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { ps.Close() })
			cs := NewContactStore(ps, &directApplier{store: ps})
			if _, err := cs.Create("u1", &bongov1.Contact{DisplayName: "C", Email: tt.email, VcardRaw: []byte(tt.vcard)}); err != nil {
				t.Fatal(err)
			}
			got, err := cs.Addresses("u1")
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			for k := range got {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if !slices.Equal(keys, tt.want) {
				t.Errorf("addresses = %v, want %v", keys, tt.want)
			}
		})
	}
}
