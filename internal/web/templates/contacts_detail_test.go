package templates

import (
	"context"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// contactsTr returns the translated text for key; the test parameter t hides
// the package-level t() inside test functions.
func contactsTr(key string) string { return t(context.Background(), key) }

func TestContactDetailFields(t *testing.T) {
	multi := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Jane\r\nEMAIL:a@x.example\r\nEMAIL:b@x.example\r\n" +
		"ADR:;;1 High St;Metropolis;;99999;Country\r\nEND:VCARD\r\n"
	tests := []struct {
		name string
		c    *bongov1.Contact
		want []contactExtraField
	}{
		{"empty contact", &bongov1.Contact{}, nil},
		{"whitespace only values dropped", &bongov1.Contact{Email: "  ", Phone: "\t", Company: " ", Notes: "\n"}, nil},
		{"all primary fields", &bongov1.Contact{Email: "a@x.example", Phone: "555", Company: "Acme", Notes: "hi"}, []contactExtraField{
			{"contacts.email", "a@x.example"}, {"contacts.phone", "555"}, {"contacts.company", "Acme"}, {"contacts.notes", "hi"},
		}},
		{"empty phone company notes omitted", &bongov1.Contact{Email: "a@x.example"}, []contactExtraField{
			{"contacts.email", "a@x.example"},
		}},
		{"vcard extras follow primaries", &bongov1.Contact{Email: "a@x.example", VcardRaw: []byte(multi)}, []contactExtraField{
			{"contacts.email", "a@x.example"}, {"contacts.email", "b@x.example"}, {"contacts.address", "1 High St, Metropolis, 99999, Country"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contactDetailFields(tt.c)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("field %d = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestContactDetailPartialEmailLink(t *testing.T) {
	c := &bongov1.Contact{Guid: "g1", DisplayName: "Ada", Email: "a+b@x.example"}
	out := renderToString(t, ContactDetailPartial(c))
	if !strings.Contains(out, `href="/compose?to=a%2Bb%40x.example"`) {
		t.Errorf("missing escaped compose link in %s", out)
	}
	if !strings.Contains(out, ">"+contactsTr("contacts.emailAction")+"<") {
		t.Errorf("missing Email button label")
	}
}

func TestContactDetailPartialNoEmailNoLink(t *testing.T) {
	out := renderToString(t, ContactDetailPartial(&bongov1.Contact{Guid: "g1", DisplayName: "Ada", Phone: "555"}))
	if strings.Contains(out, "/compose") {
		t.Errorf("compose link rendered for a contact without email")
	}
}

func TestContactDetailGridOnlyForValues(t *testing.T) {
	c := &bongov1.Contact{Guid: "g1", DisplayName: "Ada", Email: "a@x.example"}
	out := renderToString(t, ContactDetailPartial(c))
	if !strings.Contains(out, "<dt>"+contactsTr("contacts.email")+"</dt>") {
		t.Errorf("email dt missing")
	}
	for _, k := range []string{"contacts.phone", "contacts.company", "contacts.notes"} {
		if strings.Contains(out, "<dt>"+contactsTr(k)+"</dt>") {
			t.Errorf("dt for empty field %s rendered", k)
		}
	}
	none := renderToString(t, ContactDetailPartial(&bongov1.Contact{Guid: "g2", DisplayName: "Bob"}))
	if strings.Contains(none, "<dl") {
		t.Errorf("empty dl rendered for contact without fields")
	}
}

func TestContactDetailWebsiteIsPlainText(t *testing.T) {
	raw := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:J\r\nURL:javascript:alert(1)\r\nEND:VCARD\r\n"
	out := renderToString(t, ContactDetailPartial(&bongov1.Contact{Guid: "g", DisplayName: "J", VcardRaw: []byte(raw)}))
	if !strings.Contains(out, "<dd>javascript:alert(1)</dd>") {
		t.Errorf("website not rendered as plain text dd")
	}
	if strings.Contains(out, `href="javascript`) {
		t.Errorf("website rendered as href")
	}
}

func TestContactEditorHiddenState(t *testing.T) {
	c := &bongov1.Contact{Guid: "g1", DisplayName: "Ada", Email: "a@x.example"}
	partial := renderToString(t, ContactDetailPartial(c))
	page := renderToString(t, ContactDetailPage(c, "me@x.example"))
	idx := func(s string) string {
		i := strings.Index(s, `id="contact-editor-g1"`)
		if i < 0 {
			t.Fatalf("no editor wrapper in output")
		}
		j := strings.Index(s[i:], ">")
		return s[i : i+j]
	}
	if !strings.Contains(idx(partial), "hidden") {
		t.Errorf("partial editor should be hidden: %s", idx(partial))
	}
	if strings.Contains(idx(page), "hidden") {
		t.Errorf("full page editor should be visible: %s", idx(page))
	}
	for _, s := range []string{partial, page} {
		if !strings.Contains(s, "data-contact-edit-toggle") {
			t.Errorf("edit toggle missing")
		}
		if !strings.Contains(s, `name="display_name"`) {
			t.Errorf("edit form missing")
		}
	}
}

func TestContactRowKeepsHooks(t *testing.T) {
	out := renderToString(t, ContactRow(&bongov1.Contact{Guid: "g1", DisplayName: "Ada Lovelace", Email: "a@x.example"}))
	for _, want := range []string{
		`hx-get="/contacts/g1/detail"`, `hx-target="#contact-detail"`, `data-contact-select="true"`,
		`id="contact-g1"`, `class="contact-card"`, `AL`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("row missing %q", want)
		}
	}
}

func TestContactsPageHeader(t *testing.T) {
	out := renderToString(t, ContactsPage(nil, 1, 1, "u@x.example", ""))
	for _, want := range []string{
		`class="page-head contacts-head"`, `href="/contacts/export"`,
		`data-contact-form-toggle="toggle"`, contactsTr("contacts.importVcard"), `name="vcf"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q", want)
		}
	}
}
