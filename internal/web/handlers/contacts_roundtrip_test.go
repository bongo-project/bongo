package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/emersion/go-vcard"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	rtAlice = "u-rt-alice"
	rtBob   = "u-rt-bob"
)

// fixtureCard builds contact i: non-ASCII and awkward names, typed values, two
// addresses with a line break in the street, an organisation with a department,
// a birthday, a URL and a multi-line note. Contact 50 has no N.
func fixtureCard(i int) vcard.Card {
	names := []string{"Zoë Müller", "李雷", "Smith, Jo", "=SUM(A1) Evil", "Ann Lee", "+Plus Sign", "@handle Person", "O'Brien"}
	given := names[i%len(names)]
	family := fmt.Sprintf("Fixture%d", i)
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "3.0")
	card.SetValue(vcard.FieldFormattedName, given+" "+family)
	if i != 50 {
		card.SetName(&vcard.Name{FamilyName: family, GivenName: given})
	}
	emailTypes := []string{"home", "work", "home"}
	for k := 0; k <= i%3; k++ {
		card.Add(vcard.FieldEmail, &vcard.Field{
			Value:  fmt.Sprintf("c%d.%d@example.test", i, k),
			Params: vcard.Params{vcard.ParamType: []string{emailTypes[k]}},
		})
	}
	phoneTypes := []string{"cell", "home", "work", "fax", "pager"}
	phones := []string{fmt.Sprintf("+49 30 12345%02d", i), fmt.Sprintf("-12%02d", i), fmt.Sprintf("030 555 %02d", i)}
	for k := 0; k <= i%3; k++ {
		card.Add(vcard.FieldTelephone, &vcard.Field{
			Value:  phones[k],
			Params: vcard.Params{vcard.ParamType: []string{phoneTypes[(i+k)%len(phoneTypes)]}},
		})
	}
	card.AddAddress(&vcard.Address{
		Field:         &vcard.Field{Params: vcard.Params{vcard.ParamType: []string{"home"}}},
		StreetAddress: fmt.Sprintf("%d Home Street\nApt 4", i), Locality: "Berlin", Region: "BE", PostalCode: "10115", Country: "Germany",
	})
	card.AddAddress(&vcard.Address{
		Field:         &vcard.Field{Params: vcard.Params{vcard.ParamType: []string{"work"}}},
		StreetAddress: fmt.Sprintf("%d Work Road", i), Locality: "Hamburg", PostalCode: "20095", Country: "Germany",
	})
	card.SetValue(vcard.FieldOrganization, fmt.Sprintf("Acme %d;Dept %d", i, i))
	card.SetValue(vcard.FieldTitle, fmt.Sprintf("Engineer %d", i))
	card.SetValue(vcard.FieldBirthday, "1980-03-04")
	card.SetValue(vcard.FieldURL, fmt.Sprintf("https://example.test/%d", i))
	card.SetValue(vcard.FieldNote, fmt.Sprintf("first line %d\nsecond line", i))
	return card
}

// projection reduces a stored contact to what must survive a round trip. ldif
// drops what Thunderbird cannot hold: email types and URL types.
func projection(t *testing.T, raw []byte, ldif bool) string {
	t.Helper()
	card, err := vcard.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	types := func(f *vcard.Field) string {
		var ts []string
		for _, v := range f.Params[vcard.ParamType] {
			if v = strings.ToLower(v); v != "pref" && v != "internet" && v != "voice" {
				ts = append(ts, v)
			}
		}
		sort.Strings(ts)
		return strings.Join(ts, ",")
	}
	var parts []string
	parts = append(parts, "fn="+card.Value(vcard.FieldFormattedName))
	for _, f := range card[vcard.FieldEmail] {
		ty := types(f)
		if ldif {
			ty = ""
		}
		parts = append(parts, "email="+f.Value+"|"+ty)
	}
	for _, f := range card[vcard.FieldTelephone] {
		parts = append(parts, "tel="+f.Value+"|"+types(f))
	}
	for _, a := range card.Addresses() {
		parts = append(parts, fmt.Sprintf("adr=%s|%s|%s|%s|%s|%s", types(a.Field), a.StreetAddress, a.Locality, a.Region, a.PostalCode, a.Country))
	}
	parts = append(parts, "org="+card.Value(vcard.FieldOrganization), "title="+card.Value(vcard.FieldTitle),
		"bday="+card.Value(vcard.FieldBirthday), "url="+card.Value(vcard.FieldURL), "note="+card.Value(vcard.FieldNote))
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// seedFixture stores 100 contacts and two groups for rtAlice.
func seedFixture(t *testing.T, e *ioEnv) (groups map[string][]string) {
	t.Helper()
	var guids []string
	for i := range 100 {
		var buf bytes.Buffer
		if err := vcard.NewEncoder(&buf).Encode(fixtureCard(i)); err != nil {
			t.Fatal(err)
		}
		c, err := parseVCardBlock(buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		giveNewContactUID(c)
		if _, err := e.contacts.Create(rtAlice, c); err != nil {
			t.Fatal(err)
		}
		guids = append(guids, c.Guid)
	}
	groups = map[string][]string{}
	for name, idx := range map[string][]int{"Team A": {0, 1, 2, 3, 4}, "Team B": {10, 11, 12, 13}} {
		guid := store.NewContactGUID()
		uid, _ := store.UUIDFromGUID(guid)
		raw, err := store.NewContactGroupCard(name, uid)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range idx {
			m, err := e.contacts.Get(rtAlice, guids[i])
			if err != nil {
				t.Fatal(err)
			}
			raw, err = store.AddContactGroupMember(raw, store.ContactMemberValue(store.ContactIdentity(m)))
			if err != nil {
				t.Fatal(err)
			}
			groups[name] = append(groups[name], projectionFN(t, m.VcardRaw))
		}
		sort.Strings(groups[name])
		if _, err := e.contacts.Create(rtAlice, newGroupContact(guid, name, raw)); err != nil {
			t.Fatal(err)
		}
	}
	return groups
}

func projectionFN(t *testing.T, raw []byte) string {
	t.Helper()
	card, err := vcard.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		t.Fatal(err)
	}
	return card.Value(vcard.FieldFormattedName)
}

// roundTrip exports rtAlice's contacts in format, imports them as rtBob into a
// fresh store and returns the two environments.
func roundTrip(t *testing.T, format string) (src, dst *ioEnv, groups map[string][]string) {
	t.Helper()
	src = newIOEnv(t, nil)
	groups = seedFixture(t, src)

	req := httptest.NewRequest(http.MethodGet, "/contacts/export?format="+format, nil)
	ctx := middleware.WithUserContext(req.Context(), rtAlice, ioEmail)
	rr := httptest.NewRecorder()
	src.h.ExportContacts(rr, req.WithContext(middleware.WithLocale(ctx, "en")))
	if rr.Code != http.StatusOK {
		t.Fatalf("export: status %d", rr.Code)
	}

	dst = newIOEnv(t, nil)
	imp := httptest.NewRecorder()
	dst.h.ImportContacts(imp, importReq(t, rtBob, rr.Body.String(), ""))
	if imp.Code != http.StatusSeeOther {
		t.Fatalf("import: status %d: %s", imp.Code, bodyMsg(imp))
	}
	return src, dst, groups
}

func people(t *testing.T, e *ioEnv, user string, ldif bool) map[string]string {
	t.Helper()
	list, _, err := e.contacts.List(user, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, c := range list {
		if store.IsContactGroup(c) {
			continue
		}
		p := projection(t, c.VcardRaw, ldif)
		out[strings.SplitN(p, "\n", 2)[0]+"#"+fmt.Sprint(len(out))] = p
	}
	return out
}

func sortedValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func compareProjections(t *testing.T, want, got map[string]string) {
	t.Helper()
	w, g := sortedValues(want), sortedValues(got)
	if len(w) != 100 || len(g) != 100 {
		t.Fatalf("%d contacts exported, %d imported, want 100 each", len(w), len(g))
	}
	for i := range w {
		if w[i] != g[i] {
			t.Fatalf("contact differs after the round trip\nwant:\n%s\n\ngot:\n%s", w[i], g[i])
		}
	}
}

func groupMembers(t *testing.T, e *ioEnv, user string) map[string][]string {
	t.Helper()
	gs, err := e.contacts.ListContactGroups(user, "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, g := range gs {
		res, err := e.contacts.ResolveContactGroup(user, g)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range res.Members {
			out[g.DisplayName] = append(out[g.DisplayName], m.Contact.DisplayName)
		}
		sort.Strings(out[g.DisplayName])
	}
	return out
}

func TestRoundTripVCard(t *testing.T) {
	src, dst, groups := roundTrip(t, "vcard")
	compareProjections(t, people(t, src, rtAlice, false), people(t, dst, rtBob, false))
	if got := groupMembers(t, dst, rtBob); fmt.Sprint(got) != fmt.Sprint(groups) {
		t.Fatalf("groups = %v, want %v", got, groups)
	}
}

func TestRoundTripCSV(t *testing.T) {
	src, dst, _ := roundTrip(t, "csv")
	compareProjections(t, people(t, src, rtAlice, false), people(t, dst, rtBob, false))
	if gs, _ := dst.contacts.ListContactGroups(rtBob, ""); len(gs) != 0 {
		t.Fatalf("CSV import created %d groups, want none", len(gs))
	}
}

func TestRoundTripLDIF(t *testing.T) {
	src, dst, groups := roundTrip(t, "ldif")
	compareProjections(t, people(t, src, rtAlice, true), people(t, dst, rtBob, true))
	if got := groupMembers(t, dst, rtBob); fmt.Sprint(got) != fmt.Sprint(groups) {
		t.Fatalf("groups = %v, want %v", got, groups)
	}
}

func newGroupContact(guid, name string, raw []byte) *bongov1.Contact {
	return &bongov1.Contact{Guid: guid, DisplayName: name, VcardRaw: raw}
}
