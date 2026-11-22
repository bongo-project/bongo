package store

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const upperUUID = "A1B2C3D4-E5F6-4A5B-8C7D-0123456789AB"
const upperUUIDRef = "a1b2c3d4e5f64a5b8c7d0123456789ab"

func vcard4Group(members ...string) []byte {
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:group\r\nFN:Team\r\n")
	for _, m := range members {
		b.WriteString("MEMBER:" + m + "\r\n")
	}
	b.WriteString("END:VCARD\r\n")
	return []byte(b.String())
}

func appleGroup(members ...string) []byte {
	var b strings.Builder
	b.WriteString("BEGIN:VCARD\r\nVERSION:3.0\r\nX-ADDRESSBOOKSERVER-KIND:group\r\nN:Team;;;;\r\nFN:Team\r\n")
	for _, m := range members {
		b.WriteString("X-ADDRESSBOOKSERVER-MEMBER:" + m + "\r\n")
	}
	b.WriteString("END:VCARD\r\n")
	return []byte(b.String())
}

func personCard(uid, name, email string) []byte {
	s := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:" + name + "\r\n"
	if uid != "" {
		s += "UID:" + uid + "\r\n"
	}
	if email != "" {
		s += "EMAIL:" + email + "\r\n"
	}
	return []byte(s + "END:VCARD\r\n")
}

func mustCreate(t *testing.T, cs *ContactStore, user string, c *bongov1.Contact) string {
	t.Helper()
	guid, err := cs.Create(user, c)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return guid
}

func TestContactGroupDerivedFromVCard4(t *testing.T) {
	cs := newContactTestEnv(t)
	guid := mustCreate(t, cs, "u1", &bongov1.Contact{
		DisplayName: "Team",
		VcardRaw: vcard4Group(
			"urn:uuid:"+upperUUID,
			"urn:uuid:"+upperUUID, // duplicate
			"mailto:Bob@Example.com",
			"0123456789abcdef0123456789abcdef",
		),
	})
	got, err := cs.Get("u1", guid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != ContactKindGroup {
		t.Fatalf("Kind = %q, want group", got.Kind)
	}
	want := []string{upperUUIDRef, "mailto:bob@example.com", "0123456789abcdef0123456789abcdef"}
	if fmt.Sprint(got.Members) != fmt.Sprint(want) {
		t.Fatalf("Members = %v, want %v", got.Members, want)
	}
}

func TestContactGroupDerivedFromAppleForm(t *testing.T) {
	cs := newContactTestEnv(t)
	guid := mustCreate(t, cs, "u1", &bongov1.Contact{
		DisplayName: "Team",
		VcardRaw:    appleGroup("urn:uuid:"+upperUUID, "urn:uuid:0123456789abcdef0123456789abcdef"),
	})
	got, _ := cs.Get("u1", guid)
	if got.Kind != ContactKindGroup || len(got.Members) != 2 || got.Members[0] != upperUUIDRef {
		t.Fatalf("Kind=%q Members=%v", got.Kind, got.Members)
	}
}

func TestContactGroupBothFormsUnion(t *testing.T) {
	raw := "BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:group\r\nX-ADDRESSBOOKSERVER-KIND:group\r\nFN:T\r\n" +
		"MEMBER:urn:uuid:" + upperUUID + "\r\nX-ADDRESSBOOKSERVER-MEMBER:urn:uuid:0123456789abcdef0123456789abcdef\r\nEND:VCARD\r\n"
	c := &bongov1.Contact{VcardRaw: []byte(raw)}
	if err := deriveContactGroup(c); err != nil {
		t.Fatal(err)
	}
	if len(c.Members) != 2 {
		t.Fatalf("Members = %v, want the union of both forms", c.Members)
	}
}

func TestContactGroupNotDerivedForPersonOrKindIndividual(t *testing.T) {
	for name, raw := range map[string][]byte{
		"person":     personCard("x", "Ann", "ann@example.com"),
		"individual": []byte("BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:individual\r\nFN:Ann\r\nEND:VCARD\r\n"),
		"org":        []byte("BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:org\r\nFN:Acme\r\nMEMBER:urn:uuid:" + upperUUID + "\r\nEND:VCARD\r\n"),
	} {
		c := &bongov1.Contact{Kind: "group", Members: []string{"stale"}, VcardRaw: raw}
		if err := deriveContactGroup(c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.Kind != "" || c.Members != nil {
			t.Errorf("%s: Kind=%q Members=%v, want a person", name, c.Kind, c.Members)
		}
	}
}

func distinctMembers(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("urn:uuid:%032x", i+1)
	}
	return out
}

func TestContactGroupCapRefused(t *testing.T) {
	cs := newContactTestEnv(t)

	// Exactly 500 is accepted.
	okGUID := mustCreate(t, cs, "u1", &bongov1.Contact{VcardRaw: vcard4Group(distinctMembers(MaxContactGroupMembers)...)})

	// 600 lines that are 500 distinct references are accepted.
	dup := append(distinctMembers(MaxContactGroupMembers), distinctMembers(100)...)
	mustCreate(t, cs, "u1", &bongov1.Contact{VcardRaw: vcard4Group(dup...)})

	// 501 distinct references: Create refuses and writes nothing.
	over := &bongov1.Contact{Guid: "toolarge", VcardRaw: vcard4Group(distinctMembers(MaxContactGroupMembers + 1)...)}
	if _, err := cs.Create("u1", over); err != ErrContactGroupTooLarge {
		t.Fatalf("Create err = %v, want ErrContactGroupTooLarge", err)
	}
	if _, err := cs.Get("u1", "toolarge"); err == nil {
		t.Fatal("a refused Create left a record behind")
	}

	// Update refuses too, and the stored record is unchanged.
	before, _ := cs.Get("u1", okGUID)
	upd := &bongov1.Contact{VcardRaw: vcard4Group(distinctMembers(MaxContactGroupMembers + 1)...)}
	if err := cs.Update("u1", okGUID, upd); err != ErrContactGroupTooLarge {
		t.Fatalf("Update err = %v, want ErrContactGroupTooLarge", err)
	}
	after, _ := cs.Get("u1", okGUID)
	if len(after.Members) != len(before.Members) || after.UpdatedAt != before.UpdatedAt {
		t.Fatal("a refused Update changed the stored record")
	}
}

func TestContactGroupMutatorsKeepForm(t *testing.T) {
	apple, err := AddContactGroupMember(appleGroup(), "urn:uuid:"+upperUUID)
	if err != nil {
		t.Fatal(err)
	}
	apple, err = RenameContactGroup(apple, "Renamed")
	if err != nil {
		t.Fatal(err)
	}
	s := string(apple)
	if strings.Contains(s, "\r\nKIND:") || strings.Contains(s, "\r\nMEMBER:") {
		t.Errorf("Apple card gained vCard 4 properties:\n%s", s)
	}
	if !strings.Contains(s, "X-ADDRESSBOOKSERVER-MEMBER:urn:uuid:"+upperUUID) {
		t.Errorf("member missing or its case changed:\n%s", s)
	}
	if !strings.Contains(s, "FN:Renamed") || !strings.Contains(s, "N:Renamed;;;;") {
		t.Errorf("rename must update FN and N:\n%s", s)
	}

	v4, err := AddContactGroupMember(vcard4Group(), "urn:uuid:"+upperUUID)
	if err != nil {
		t.Fatal(err)
	}
	v4, _ = RenameContactGroup(v4, "Renamed")
	s = string(v4)
	if strings.Contains(s, "X-ADDRESSBOOKSERVER") {
		t.Errorf("vCard 4 card gained Apple properties:\n%s", s)
	}
	if !strings.Contains(s, "MEMBER:urn:uuid:"+upperUUID) || strings.Contains(s, "\r\nN:") {
		t.Errorf("v4 card wrong:\n%s", s)
	}

	// Adding the same member again is a no-op; removing works across spellings.
	v4, _ = AddContactGroupMember(v4, "urn:uuid:"+strings.ToLower(upperUUID))
	if strings.Count(string(v4), "MEMBER:") != 1 {
		t.Errorf("duplicate member added:\n%s", v4)
	}
	out, changed, err := RemoveContactGroupMember(v4, upperUUIDRef)
	if err != nil || !changed || strings.Contains(string(out), "MEMBER:") {
		t.Errorf("remove: changed=%v err=%v\n%s", changed, err, out)
	}
	if _, err := AddContactGroupMember(personCard("x", "Ann", ""), "urn:uuid:"+upperUUID); err != ErrNotContactGroup {
		t.Errorf("a person card must not take members: %v", err)
	}
}

func TestResolveContactGroupSameBookOnly(t *testing.T) {
	cs := newContactTestEnv(t)
	const user = "u1"
	book := strings.Repeat("b", 32)
	other := strings.Repeat("c", 32)

	// UID differs from GUID: the member resolves by UID.
	byUID := mustCreate(t, cs, user, &bongov1.Contact{Guid: strings.Repeat("1", 32), DisplayName: "ByUID", Email: "uid@example.com", AddressbookId: book,
		VcardRaw: personCard("11111111-2222-4333-8444-555555555555", "ByUID", "uid@example.com")})
	// No UID: resolves by GUID.
	byGUID := mustCreate(t, cs, user, &bongov1.Contact{Guid: strings.Repeat("2", 32), DisplayName: "ByGUID", Email: "guid@example.com", AddressbookId: book,
		VcardRaw: personCard("", "ByGUID", "guid@example.com")})
	// Other book.
	foreign := mustCreate(t, cs, user, &bongov1.Contact{Guid: strings.Repeat("3", 32), DisplayName: "Elsewhere", Email: "else@example.com", AddressbookId: other,
		VcardRaw: personCard("", "Elsewhere", "else@example.com")})
	// Another group (nested).
	nested := mustCreate(t, cs, user, &bongov1.Contact{Guid: strings.Repeat("4", 32), AddressbookId: book, VcardRaw: vcard4Group()})

	gid := strings.Repeat("5", 32)
	g := &bongov1.Contact{Guid: gid, AddressbookId: book, VcardRaw: vcard4Group(
		"urn:uuid:11111111-2222-4333-8444-555555555555",
		"urn:uuid:"+byGUID,
		"urn:uuid:"+foreign,
		"urn:uuid:"+nested,
		"urn:uuid:"+gid,                     // itself
		"urn:uuid:"+strings.Repeat("9", 32), // never existed
	)}
	mustCreate(t, cs, user, g)
	_ = byUID

	got, _ := cs.Get(user, gid)
	res, err := cs.ResolveContactGroup(user, got)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range res.Members {
		names = append(names, m.Contact.DisplayName)
	}
	if fmt.Sprint(names) != "[ByUID ByGUID]" {
		t.Fatalf("members = %v, want [ByUID ByGUID]", names)
	}
	if res.Nested != 2 {
		t.Errorf("Nested = %d, want 2 (the other group and itself)", res.Nested)
	}
}

func TestContactGroupInfoLegacyRecord(t *testing.T) {
	cs := newContactTestEnv(t)
	// A record as an earlier version stored it: the card says group, the
	// derived fields are empty. Written through the applier so Create does not
	// derive them.
	legacy := &bongov1.Contact{Guid: "legacy", DisplayName: "Old", VcardRaw: vcard4Group("urn:uuid:" + upperUUID)}
	putRaw(t, cs, "u1", legacy)

	groups, err := cs.ListContactGroups("u1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Guid != "legacy" {
		t.Fatalf("legacy group not listed: %v", groups)
	}
	if ok, m := ContactGroupInfo(legacy); !ok || len(m) != 1 || m[0] != upperUUIDRef {
		t.Fatalf("ContactGroupInfo = %v %v", ok, m)
	}
}

func TestExpandContactGroup(t *testing.T) {
	cs := newContactTestEnv(t)
	const user = "u1"
	mk := func(guid, uid, name, email string) {
		mustCreate(t, cs, user, &bongov1.Contact{Guid: guid, DisplayName: name, Email: email, VcardRaw: personCard(uid, name, email)})
	}
	g1, g2, g3, g4 := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32)
	mk(g1, "", "Ann", "Ann@Example.com")
	mk(g2, "", "AnnDup", "ann@example.com")
	mk(g3, "", "NoMail", "")
	mustCreate(t, cs, user, &bongov1.Contact{Guid: g4, VcardRaw: vcard4Group()})
	gid := strings.Repeat("e", 32)
	mustCreate(t, cs, user, &bongov1.Contact{Guid: gid, VcardRaw: vcard4Group(
		"urn:uuid:"+g1, "urn:uuid:"+g2, "urn:uuid:"+g3, "urn:uuid:"+g4,
		"mailto:Zed@Example.com", "urn:uuid:"+strings.Repeat("0", 32))})

	grp, _ := cs.Get(user, gid)
	exp, err := cs.ExpandContactGroup(user, grp)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(exp.Addresses) != "[ann@example.com zed@example.com]" {
		t.Errorf("Addresses = %v", exp.Addresses)
	}
	if exp.Skipped != 2 { // NoMail and the nested group; the dangling reference is not counted
		t.Errorf("Skipped = %d, want 2", exp.Skipped)
	}
}

func TestAddressesIgnoresGroupMembers(t *testing.T) {
	cs := newContactTestEnv(t)
	mustCreate(t, cs, "u1", &bongov1.Contact{Guid: strings.Repeat("a", 32), Email: "ann@example.com", VcardRaw: personCard("", "Ann", "ann@example.com")})
	mustCreate(t, cs, "u1", &bongov1.Contact{VcardRaw: vcard4Group("mailto:zed@example.com", "urn:uuid:"+strings.Repeat("a", 32))})
	got, err := cs.Addresses("u1")
	if err != nil {
		t.Fatal(err)
	}
	if !got["ann@example.com"] || got["zed@example.com"] || len(got) != 1 {
		t.Errorf("Addresses = %v: a group's members must not count as contacts' addresses", got)
	}
}

func TestEnsureContactUID(t *testing.T) {
	out, uid, changed, err := EnsureContactUID(personCard("", "Ann", ""), "11111111-2222-4333-8444-555555555555")
	if err != nil || !changed || uid != "11111111-2222-4333-8444-555555555555" || vcardUID(out) != uid {
		t.Fatalf("add: %v %v %v %q", err, changed, uid, out)
	}
	_, uid, changed, _ = EnsureContactUID(personCard("keep-me", "Ann", ""), "other")
	if changed || uid != "keep-me" {
		t.Fatalf("an existing UID must be kept: %v %q", changed, uid)
	}
}

func TestContactMemberRef(t *testing.T) {
	for in, want := range map[string]string{
		"urn:uuid:" + upperUUID:    upperUUIDRef,
		"URN:UUID:" + upperUUIDRef: upperUUIDRef,
		upperUUID:                  upperUUIDRef,
		"mailto:Bob <b@x.com>":     "mailto:b@x.com",
		"  Some-Other-Id ":         "some-other-id",
		"urn:uuid:":                "",
		"":                         "",
	} {
		if got := ContactMemberRef(in); got != want {
			t.Errorf("ContactMemberRef(%q) = %q, want %q", in, got, want)
		}
	}
}

// putRaw stores a contact exactly as given, bypassing the derivation Create and
// Update do, to build a record an earlier version wrote.
func putRaw(t *testing.T, cs *ContactStore, user string, c *bongov1.Contact) {
	t.Helper()
	data, err := proto.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.pebble.Set(contactKey(user, c.Guid), data); err != nil {
		t.Fatal(err)
	}
}

func TestVCardUIDFoldedAndUnsafe(t *testing.T) {
	folded := []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ann\r\nUID:11111111-2222-4333-\r\n 8444-555555555555\r\nEND:VCARD\r\n")
	if got := vcardUID(folded); got != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("folded UID = %q", got)
	}
	// An escaped newline decodes to a real one and could start another property.
	unsafe := []byte("BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Ann\r\nUID:abc\\nMEMBER:urn:uuid:" + upperUUID + "\r\nEND:VCARD\r\n")
	if got := vcardUID(unsafe); got != "" {
		t.Errorf("an unsafe UID was used: %q", got)
	}
	out, uid, changed, err := EnsureContactUID(unsafe, "99999999-2222-4333-8444-555555555555")
	if err != nil || !changed || uid != "99999999-2222-4333-8444-555555555555" {
		t.Fatalf("unsafe UID not replaced: %v %v %q", err, changed, uid)
	}
	if strings.Contains(string(out), "abc") || vcardUID(out) != uid {
		t.Errorf("the unsafe UID survived:\n%s", out)
	}
	for _, v := range []string{"a\nb", "a\rb", "a\\nb", ""} {
		if got := ContactMemberValue(v); got != "" {
			t.Errorf("ContactMemberValue(%q) = %q, want a refusal", v, got)
		}
	}
}
