package templates

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func groupFixture() *bongov1.Contact {
	return &bongov1.Contact{
		Guid: "g1", DisplayName: "Team", Kind: "group",
		Members:  []string{"m1", "m2"},
		VcardRaw: []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nX-ADDRESSBOOKSERVER-KIND:group\r\nFN:Team\r\nEND:VCARD\r\n"),
	}
}

func TestContactRowShowsGroupMemberCountNotEmail(t *testing.T) {
	g := groupFixture()
	out := renderToString(t, ContactRow(g))
	if !strings.Contains(out, "2 member(s)") || !strings.Contains(out, "contact-avatar-group") {
		t.Errorf("group row:\n%s", out)
	}
	person := renderToString(t, ContactRow(&bongov1.Contact{Guid: "p", DisplayName: "Ann", Email: "ann@x.example"}))
	if !strings.Contains(person, "ann@x.example") || strings.Contains(person, "member(s)") {
		t.Errorf("person row:\n%s", person)
	}
}

// Every contact group form posts a CSRF token and its action is a same-origin
// path; the group card has no person editor.
func TestContactGroupCardForms(t *testing.T) {
	ctx, token := csrfRenderCtx(t)
	initI18nForTest()
	var buf bytes.Buffer
	view := ContactCardView{Group: &ContactGroupView{Members: []ContactGroupMemberView{
		{Ref: "m1", Guid: "p1", Name: "Ann", Email: "ann@x.example"},
		{Ref: "mailto:z@x.example", Email: "z@x.example"},
	}, Nested: 1}}
	if err := ContactDetailPartial(groupFixture(), view).Render(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`action="/contacts/groups/g1/rename"`, `action="/contacts/groups/g1/delete"`,
		`action="/contacts/groups/g1/members/remove"`, `href="/compose?contact_group=g1"`,
		"ann@x.example", "z@x.example", "1 nested group(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("group card lacks %q", want)
		}
	}
	if strings.Contains(out, "contact-editor") {
		t.Error("a group card must not offer the person editor")
	}
	forms := regexp.MustCompile(`(?s)<form.*?</form>`).FindAllString(out, -1)
	if len(forms) != 4 { // rename, remove x2, delete
		t.Fatalf("forms = %d, want 4", len(forms))
	}
	for _, f := range forms {
		if !strings.Contains(f, `name="_csrf" value="`+token+`"`) {
			t.Errorf("form without the CSRF token: %.120s", f)
		}
	}
}

func TestContactCardOffersAddToGroupOnlyWithGroups(t *testing.T) {
	person := &bongov1.Contact{Guid: "p1", DisplayName: "Ann", Email: "ann@x.example"}
	none := renderToString(t, ContactDetailPartial(person, ContactCardView{}))
	if strings.Contains(none, "add-to-group") {
		t.Error("add-to-group offered with no groups")
	}
	with := renderToString(t, ContactDetailPartial(person, ContactCardView{Groups: []*bongov1.Contact{groupFixture()}}))
	if !strings.Contains(with, `action="/contacts/p1/add-to-group"`) || !strings.Contains(with, `<option value="g1">Team</option>`) {
		t.Errorf("add-to-group form missing:\n%s", with)
	}
}

func TestContactsSidebarListsGroups(t *testing.T) {
	out := renderToString(t, ContactsSidebar(ContactBooksView{Groups: []*bongov1.Contact{groupFixture()}}))
	if !strings.Contains(out, `href="/contacts/g1"`) || !strings.Contains(out, `action="/contacts/groups"`) {
		t.Errorf("sidebar:\n%s", out)
	}
}
