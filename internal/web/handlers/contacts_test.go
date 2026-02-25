package handlers

import (
	"testing"

	"github.com/emersion/go-vcard"
)

func TestSplitVCardBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			name:  "empty input",
			input: "",
			want:  0,
		},
		{
			name:  "single vcard",
			input: "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEND:VCARD\n",
			want:  1,
		},
		{
			name: "two vcards",
			input: "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEND:VCARD\n\n" +
				"BEGIN:VCARD\nVERSION:3.0\nFN:Bob\nEND:VCARD\n",
			want: 2,
		},
		{
			name:  "garbage before and after",
			input: "junk\nBEGIN:VCARD\nVERSION:3.0\nFN:Alice\nEND:VCARD\nmore junk\n",
			want:  1,
		},
		{
			name:  "case insensitive begin/end",
			input: "begin:vcard\nVERSION:3.0\nFN:Alice\nend:vcard\n",
			want:  1,
		},
		{
			name:  "missing END:VCARD",
			input: "BEGIN:VCARD\nVERSION:3.0\nFN:Alice\n",
			want:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocks := splitVCardBlocks([]byte(tc.input))
			if len(blocks) != tc.want {
				t.Errorf("got %d blocks, want %d", len(blocks), tc.want)
			}
		})
	}
}

func TestParseVCardBlock_V30(t *testing.T) {
	input := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Alice Smith\r\n" +
		"EMAIL;TYPE=WORK:alice@example.com\r\n" +
		"TEL;TYPE=CELL:+1-555-0101\r\n" +
		"ORG:Acme Corp\r\n" +
		"NOTE:Friend from work\r\n" +
		"END:VCARD\r\n"

	contact, err := parseVCardBlock([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contact.DisplayName != "Alice Smith" {
		t.Errorf("DisplayName = %q, want %q", contact.DisplayName, "Alice Smith")
	}
	if contact.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", contact.Email, "alice@example.com")
	}
	if contact.Phone != "+1-555-0101" {
		t.Errorf("Phone = %q, want %q", contact.Phone, "+1-555-0101")
	}
	if contact.Company != "Acme Corp" {
		t.Errorf("Company = %q, want %q", contact.Company, "Acme Corp")
	}
	if contact.Notes != "Friend from work" {
		t.Errorf("Notes = %q, want %q", contact.Notes, "Friend from work")
	}
}

func TestParseVCardBlock_MultipleEmails(t *testing.T) {
	input := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Bob Jones\r\n" +
		"EMAIL;TYPE=HOME:bob@home.example.com\r\n" +
		"EMAIL;TYPE=WORK:bob@work.example.com\r\n" +
		"TEL;TYPE=WORK;TYPE=VOICE:+1-555-0202\r\n" +
		"TEL;TYPE=CELL:+1-555-0203\r\n" +
		"END:VCARD\r\n"

	contact, err := parseVCardBlock([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contact.DisplayName != "Bob Jones" {
		t.Errorf("DisplayName = %q, want %q", contact.DisplayName, "Bob Jones")
	}
	// Should get the first email.
	if contact.Email == "" {
		t.Error("Email is empty, want non-empty (first of multiple)")
	}
	if contact.Phone == "" {
		t.Error("Phone is empty, want non-empty (first of multiple)")
	}
}

func TestParseVCardBlock_PlainEmail(t *testing.T) {
	// EMAIL without TYPE parameter.
	input := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Diana\r\n" +
		"EMAIL:diana@plain.example.com\r\n" +
		"END:VCARD\r\n"

	contact, err := parseVCardBlock([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contact.Email != "diana@plain.example.com" {
		t.Errorf("Email = %q, want %q", contact.Email, "diana@plain.example.com")
	}
}

func TestParseVCardBlock_NoNameFallbackToN(t *testing.T) {
	// FN is missing, should fall back to building name from N field.
	input := "BEGIN:VCARD\r\nVERSION:3.0\r\n" +
		"N:Doe;John;;;\r\n" +
		"EMAIL:john@example.com\r\n" +
		"END:VCARD\r\n"

	contact, err := parseVCardBlock([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contact.DisplayName != "John Doe" {
		t.Errorf("DisplayName = %q, want %q", contact.DisplayName, "John Doe")
	}
}

func TestParseVCardBlock_SkipNoNameNoEmail(t *testing.T) {
	// Contact with only a structured name with empty parts should be skipped.
	input := "BEGIN:VCARD\r\nVERSION:3.0\r\n" +
		"N:;;;;\r\n" +
		"END:VCARD\r\n"

	contact, err := parseVCardBlock([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both DisplayName and Email are empty, so the import loop would skip this.
	if contact.DisplayName != "" {
		t.Errorf("DisplayName = %q, want empty", contact.DisplayName)
	}
	if contact.Email != "" {
		t.Errorf("Email = %q, want empty", contact.Email)
	}
}

func TestDecodeQPIfNeeded(t *testing.T) {
	tests := []struct {
		name   string
		val    string
		fields []*vcard.Field
		want   string
	}{
		{
			name:   "plain text no encoding",
			val:    "Hello World",
			fields: nil,
			want:   "Hello World",
		},
		{
			name: "QP encoding via ENCODING param",
			val:  "=48=65=69=6B=6F=20=4B=6F=70=70",
			fields: []*vcard.Field{
				{
					Value:  "=48=65=69=6B=6F=20=4B=6F=70=70",
					Params: vcard.Params{"ENCODING": {"QUOTED-PRINTABLE"}},
				},
			},
			want: "Heiko Kopp",
		},
		{
			name: "QP encoding via bare QUOTED-PRINTABLE param key",
			val:  "=48=65=69=6B=6F=20=4B=6F=70=70",
			fields: []*vcard.Field{
				{
					Value:  "=48=65=69=6B=6F=20=4B=6F=70=70",
					Params: vcard.Params{"QUOTED-PRINTABLE": {""}},
				},
			},
			want: "Heiko Kopp",
		},
		{
			name:   "QP detected by heuristic (no field params)",
			val:    "=48=65=69=6B=6F=20=4B=6F=70=70",
			fields: nil,
			want:   "Heiko Kopp",
		},
		{
			name:   "UTF-8 QP with umlaut",
			val:    "=4D=C3=BC=6C=6C=65=72",
			fields: nil,
			want:   "M\u00fcller",
		},
		{
			name:   "empty value",
			val:    "",
			fields: nil,
			want:   "",
		},
		{
			name:   "short value with equals not QP",
			val:    "a=b",
			fields: nil,
			want:   "a=b",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeQPIfNeeded(tc.val, tc.fields)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFieldHasQPEncoding(t *testing.T) {
	tests := []struct {
		name   string
		fields []*vcard.Field
		want   bool
	}{
		{
			name:   "nil fields",
			fields: nil,
			want:   false,
		},
		{
			name: "ENCODING=QUOTED-PRINTABLE",
			fields: []*vcard.Field{
				{Params: vcard.Params{"ENCODING": {"QUOTED-PRINTABLE"}}},
			},
			want: true,
		},
		{
			name: "bare QUOTED-PRINTABLE key",
			fields: []*vcard.Field{
				{Params: vcard.Params{"QUOTED-PRINTABLE": {""}}},
			},
			want: true,
		},
		{
			name: "case insensitive encoding param",
			fields: []*vcard.Field{
				{Params: vcard.Params{"ENCODING": {"quoted-printable"}}},
			},
			want: true,
		},
		{
			name: "no QP encoding",
			fields: []*vcard.Field{
				{Params: vcard.Params{"TYPE": {"WORK"}}},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fieldHasQPEncoding(tc.fields)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractFirstEmail(t *testing.T) {
	tests := []struct {
		name string
		card vcard.Card
		want string
	}{
		{
			name: "standard EMAIL key",
			card: vcard.Card{
				"EMAIL": []*vcard.Field{{Value: "alice@example.com"}},
			},
			want: "alice@example.com",
		},
		{
			name: "multiple EMAIL values takes first non-empty",
			card: vcard.Card{
				"EMAIL": []*vcard.Field{
					{Value: "first@example.com"},
					{Value: "second@example.com"},
				},
			},
			want: "first@example.com",
		},
		{
			name: "no EMAIL field",
			card: vcard.Card{
				"FN": []*vcard.Field{{Value: "Alice"}},
			},
			want: "",
		},
		{
			name: "empty card",
			card: vcard.Card{},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFirstEmail(tc.card)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractFirstPhone(t *testing.T) {
	tests := []struct {
		name string
		card vcard.Card
		want string
	}{
		{
			name: "standard TEL key",
			card: vcard.Card{
				"TEL": []*vcard.Field{{Value: "+1-555-0101"}},
			},
			want: "+1-555-0101",
		},
		{
			name: "multiple TEL values takes first non-empty",
			card: vcard.Card{
				"TEL": []*vcard.Field{
					{Value: "+1-555-0101"},
					{Value: "+1-555-0202"},
				},
			},
			want: "+1-555-0101",
		},
		{
			name: "no TEL field",
			card: vcard.Card{
				"FN": []*vcard.Field{{Value: "Alice"}},
			},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFirstPhone(tc.card)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildNameFromN(t *testing.T) {
	tests := []struct {
		name string
		card vcard.Card
		want string
	}{
		{
			name: "family and given",
			card: vcard.Card{
				"N": []*vcard.Field{{Value: "Doe;John;;;"}},
			},
			want: "John Doe",
		},
		{
			name: "family only",
			card: vcard.Card{
				"N": []*vcard.Field{{Value: "Doe;;;;"}},
			},
			want: "Doe",
		},
		{
			name: "given only",
			card: vcard.Card{
				"N": []*vcard.Field{{Value: ";John;;;"}},
			},
			want: "John",
		},
		{
			name: "empty N",
			card: vcard.Card{
				"N": []*vcard.Field{{Value: ";;;;"}},
			},
			want: "",
		},
		{
			name: "no N field",
			card: vcard.Card{},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildNameFromN(tc.card)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseVCardBlock_EvolutionFormat(t *testing.T) {
	// Test with the exact format from the demo VCF file (Evolution export).
	input := "BEGIN:VCARD\r\nVERSION:3.0\r\n" +
		"FN:Dave Camp\r\n" +
		"N:Camp;Dave;;;\r\n" +
		"EMAIL;TYPE=WORK;X-EVOLUTION-UI-SLOT=1:dave@novell.com\r\n" +
		"EMAIL;TYPE=HOME;X-EVOLUTION-UI-SLOT=3:dave.camp@gmail.com\r\n" +
		"TEL;TYPE=WORK;TYPE=VOICE;X-EVOLUTION-UI-SLOT=1:617-555-1234\r\n" +
		"TEL;TYPE=CELL;X-EVOLUTION-UI-SLOT=3:617-555-2345\r\n" +
		"END:VCARD\r\n"

	contact, err := parseVCardBlock([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contact.DisplayName != "Dave Camp" {
		t.Errorf("DisplayName = %q, want %q", contact.DisplayName, "Dave Camp")
	}
	if contact.Email == "" {
		t.Error("Email is empty, want non-empty")
	}
	if contact.Phone == "" {
		t.Error("Phone is empty, want non-empty")
	}
}

func TestParseVCardBlock_V40(t *testing.T) {
	input := "BEGIN:VCARD\r\nVERSION:4.0\r\nFN:Eve Modern\r\n" +
		"EMAIL;TYPE=work:eve@modern.example.com\r\n" +
		"TEL;TYPE=cell:+1-555-0505\r\n" +
		"END:VCARD\r\n"

	contact, err := parseVCardBlock([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contact.DisplayName != "Eve Modern" {
		t.Errorf("DisplayName = %q, want %q", contact.DisplayName, "Eve Modern")
	}
	if contact.Email != "eve@modern.example.com" {
		t.Errorf("Email = %q, want %q", contact.Email, "eve@modern.example.com")
	}
	if contact.Phone != "+1-555-0505" {
		t.Errorf("Phone = %q, want %q", contact.Phone, "+1-555-0505")
	}
}

func TestParseVCardBlock_MalformedInput(t *testing.T) {
	// Completely garbled input should return an error, not panic.
	input := "BEGIN:VCARD\r\nthis is not valid\r\nEND:VCARD\r\n"

	_, err := parseVCardBlock([]byte(input))
	// We just want no panic. Error or success is fine depending on library behavior.
	_ = err
}

func TestDecodeVCardField_PrefixMatch(t *testing.T) {
	// Simulate vCard 2.1 where the library stores the field under a key
	// that includes bare parameters.
	card := vcard.Card{
		"FN;ENCODING=QUOTED-PRINTABLE": []*vcard.Field{
			{
				Value:  "=48=65=69=6B=6F",
				Params: vcard.Params{"ENCODING": {"QUOTED-PRINTABLE"}},
			},
		},
	}

	got := decodeVCardField(card, "FN")
	if got != "Heiko" {
		t.Errorf("got %q, want %q", got, "Heiko")
	}
}
