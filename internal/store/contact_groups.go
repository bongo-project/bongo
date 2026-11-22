package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/emersion/go-vcard"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A contact group is a contact card that lists other cards: vCard 4 KIND:group
// with MEMBER, or Apple's X-ADDRESSBOOKSERVER-KIND:group with
// X-ADDRESSBOOKSERVER-MEMBER. It is stored as an ordinary contact record at
// /user/{id}/contact/{guid}: no new key or Raft command. "Group" already means
// a distribution group elsewhere in Bongo, so everything here says "contact
// group".
//
// vcard_raw is the source of truth. Contact.kind and Contact.members are
// derived from it on every write through ContactStore.Create and Update, so
// CardDAV, the web, import and quick-add all agree. A member is a reference to
// the member card's UID property, canonicalised (ContactMemberRef); for
// Bongo-made cards and for clients that name the resource <UID>.vcf the UID
// equals the GUID. References are resolved at read time, within the group's own
// address book, and are never stored as GUIDs.

// ContactKindGroup is Contact.kind for a contact group.
const ContactKindGroup = "group"

// MaxContactGroupMembers bounds the distinct member references of one group.
const MaxContactGroupMembers = 500

const (
	fieldAppleKind   = "X-ADDRESSBOOKSERVER-KIND"
	fieldAppleMember = "X-ADDRESSBOOKSERVER-MEMBER"
)

var (
	// ErrContactGroupTooLarge refuses a group with more than
	// MaxContactGroupMembers distinct members. Nothing is written.
	ErrContactGroupTooLarge = errors.New("contact group has too many members")
	// ErrNotContactGroup is returned by a group mutator given a card that is not
	// a contact group.
	ErrNotContactGroup = errors.New("not a contact group")
)

var uuidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var hex32Shape = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// NewContactGUID returns a fresh contact GUID, for callers that need to know
// it before the record is written (a new card's UID is derived from it).
func NewContactGUID() string { return generateGUID() }

// UUIDFromGUID formats a 32-hex GUID as an 8-4-4-4-12 UUID, and reports false
// for anything else.
func UUIDFromGUID(guid string) (string, bool) {
	if !hex32Shape.MatchString(guid) {
		return "", false
	}
	g := strings.ToLower(guid)
	return g[0:8] + "-" + g[8:12] + "-" + g[12:16] + "-" + g[16:20] + "-" + g[20:], true
}

// ContactMemberRef canonicalises a member reference or a UID so the two compare
// equal. A "urn:uuid:" prefix is dropped; a 36-character UUID or 32 hex in any
// case becomes lower-case 32 hex; "mailto:x" becomes "mailto:" plus the bare
// address; anything else is lower-cased and kept (it never resolves to a card).
// The empty string means no usable reference.
func ContactMemberRef(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 9 && strings.EqualFold(v[:9], "urn:uuid:") {
		v = strings.TrimSpace(v[9:])
	}
	switch {
	case v == "":
		return ""
	case hex32Shape.MatchString(v):
		return strings.ToLower(v)
	case uuidShape.MatchString(v):
		return strings.ToLower(strings.ReplaceAll(v, "-", ""))
	case len(v) >= 7 && strings.EqualFold(v[:7], "mailto:"):
		a := BareAddress(v[7:])
		if a == "" {
			return ""
		}
		return "mailto:" + a
	}
	return strings.ToLower(v)
}

// ContactMemberValue is the MEMBER value that names a card with the given UID:
// "urn:uuid:" plus the UID when it is a UUID or 32 hex (kept verbatim, so Apple's
// upper-case UUIDs survive), otherwise the UID itself.
//
// A UID with a line break in it is refused with "": a MEMBER value must never be
// able to start another property.
func ContactMemberValue(uid string) string {
	uid = strings.TrimSpace(uid)
	if !safeUID(uid) {
		return ""
	}
	if hex32Shape.MatchString(uid) || uuidShape.MatchString(uid) {
		return "urn:uuid:" + uid
	}
	return uid
}

// parseContactGroup reads the first card of raw: whether it is a group, and its
// distinct canonical member references in order (both forms, union).
func parseContactGroup(raw []byte) (bool, []string) {
	var kinds, members []string
	card, err := vcard.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		kinds, members = scanGroupLines(raw)
	} else {
		kinds = append(card.Values(vcard.FieldKind), card.Values(fieldAppleKind)...)
		members = append(card.Values(vcard.FieldMember), card.Values(fieldAppleMember)...)
	}
	isGroup := false
	for _, k := range kinds {
		if strings.EqualFold(strings.TrimSpace(k), ContactKindGroup) {
			isGroup = true
			break
		}
	}
	if !isGroup {
		return false, nil
	}
	seen := make(map[string]bool, len(members))
	var out []string
	for _, m := range members {
		ref := ContactMemberRef(m)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return true, out
}

// scanGroupLines is the fallback for a card go-vcard rejects: KIND and MEMBER
// lines read directly, ignoring a "group." prefix. Folded lines are not joined.
// The vCard is untrusted and is never logged.
func scanGroupLines(raw []byte) (kinds, members []string) {
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if dot := strings.IndexByte(line, '.'); dot > 0 && !strings.ContainsAny(line[:dot], ":;") {
			line = line[dot+1:]
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if i := strings.IndexByte(name, ';'); i >= 0 {
			name = name[:i]
		}
		switch strings.ToUpper(strings.TrimSpace(name)) {
		case vcard.FieldKind, fieldAppleKind:
			kinds = append(kinds, value)
		case vcard.FieldMember, fieldAppleMember:
			members = append(members, value)
		}
	}
	return kinds, members
}

// mayBeContactGroup is a cheap pre-check that spares decoding every card.
func mayBeContactGroup(raw []byte) bool {
	return len(raw) > 0 && bytes.Contains(bytes.ToUpper(raw), []byte("KIND"))
}

// deriveContactGroup refreshes Kind and Members from VcardRaw. A group with more
// than MaxContactGroupMembers distinct members is refused.
func deriveContactGroup(c *bongov1.Contact) error {
	c.Kind, c.Members = "", nil
	if !mayBeContactGroup(c.VcardRaw) {
		return nil
	}
	isGroup, members := parseContactGroup(c.VcardRaw)
	if !isGroup {
		return nil
	}
	if len(members) > MaxContactGroupMembers {
		return ErrContactGroupTooLarge
	}
	c.Kind, c.Members = ContactKindGroup, members
	return nil
}

// ValidateContact reports the error Create would return for c's group, without
// writing anything or touching c: ErrContactGroupTooLarge for a group over
// MaxContactGroupMembers. An import uses it to refuse a whole file before the
// first write.
func ValidateContact(c *bongov1.Contact) error {
	return deriveContactGroup(&bongov1.Contact{VcardRaw: c.VcardRaw})
}

// ContactGroupInfo reports whether c is a contact group and its member
// references. It trusts the stored Kind and Members; a record with no Kind whose
// card says KIND is read from vcard_raw, which covers a group stored before the
// fields existed and a CardDAV PUT handled by a node that does not set them.
func ContactGroupInfo(c *bongov1.Contact) (bool, []string) {
	if c.Kind == ContactKindGroup {
		return true, c.Members
	}
	if c.Kind == "" && mayBeContactGroup(c.VcardRaw) {
		return parseContactGroup(c.VcardRaw)
	}
	return false, nil
}

// IsContactGroup reports whether c is a contact group.
func IsContactGroup(c *bongov1.Contact) bool {
	ok, _ := ContactGroupInfo(c)
	return ok
}

var foldedLine = regexp.MustCompile(`\r?\n[ \t]`)

// safeUID reports whether a UID can stand as a MEMBER value: non-empty, with no
// line break (real or escaped) that could start another property, and no backslash.
func safeUID(uid string) bool {
	return uid != "" && !strings.ContainsAny(uid, "\r\n\\")
}

// vcardUID returns the usable UID property of the first card in raw by a line
// scan, "" when there is none or it is unsafe (see safeUID). Folded lines are
// joined first.
func vcardUID(raw []byte) string {
	for _, line := range strings.Split(foldedLine.ReplaceAllString(string(raw), ""), "\n") {
		line = strings.TrimRight(line, "\r")
		if dot := strings.IndexByte(line, '.'); dot > 0 && !strings.ContainsAny(line[:dot], ":;") {
			line = line[dot+1:]
		}
		if len(line) < 4 || !strings.EqualFold(line[:3], "UID") || (line[3] != ':' && line[3] != ';') {
			continue
		}
		if _, v, ok := strings.Cut(line, ":"); ok {
			if v = strings.TrimSpace(v); safeUID(v) {
				return v
			}
			return ""
		}
	}
	return ""
}

// ContactIdentity is the canonical reference other cards use for c: its UID
// property, or its GUID when the card has none.
func ContactIdentity(c *bongov1.Contact) string {
	if uid := ContactMemberRef(vcardUID(c.VcardRaw)); uid != "" {
		return uid
	}
	return ContactMemberRef(c.Guid)
}

// contactPrimaryAddress is the address a contact is mailed at: the quick Email
// field, else the first EMAIL property of the card.
func contactPrimaryAddress(c *bongov1.Contact) string {
	if a := BareAddress(c.Email); a != "" {
		return a
	}
	if all := vcardEmails(c.VcardRaw); len(all) > 0 {
		return all[0]
	}
	return ""
}

// ContactGroupMember is one resolved member of a group: a card in the group's
// book, or a bare address for a mailto: reference.
type ContactGroupMember struct {
	Ref     string
	Contact *bongov1.Contact // nil for a mailto: reference
	Address string           // set for a mailto: reference
}

// ResolvedContactGroup is a group's members as they exist now.
type ResolvedContactGroup struct {
	Members []ContactGroupMember
	// Nested counts members that are themselves groups, or the group itself.
	// They are not expanded: contact groups do not nest.
	Nested int
}

// scanContacts calls fn for every contact of the user, in key order.
func (s *ContactStore) scanContacts(userID string, fn func(*bongov1.Contact)) error {
	iter, err := s.pebble.PrefixScan(contactPrefix(userID))
	if err != nil {
		return fmt.Errorf("scanning contacts: %w", err)
	}
	defer iter.Close()
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var c bongov1.Contact
		if err := proto.Unmarshal(val, &c); err != nil {
			continue
		}
		fn(&c)
	}
	return nil
}

// ListContactGroups returns the user's contact groups sorted by name. book ""
// means every book; otherwise only that book ("default" for the default one).
// It is a prefix scan of the user's contacts.
func (s *ContactStore) ListContactGroups(userID, book string) ([]*bongov1.Contact, error) {
	var out []*bongov1.Contact
	err := s.scanContacts(userID, func(c *bongov1.Contact) {
		if (book == "" || ContactBook(c) == book) && IsContactGroup(c) {
			out = append(out, c)
		}
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName)
	})
	return out, nil
}

// ResolveContactGroup maps a group's member references to the cards that exist
// in the group's own address book: a reference to a card in another book, to a
// deleted card or to one not yet synced is dropped silently, so a group can
// arrive before its members. A member that is a group, or the group itself, is
// counted in Nested and skipped. One prefix scan of the owner's contacts.
func (s *ContactStore) ResolveContactGroup(userID string, g *bongov1.Contact) (*ResolvedContactGroup, error) {
	_, refs := ContactGroupInfo(g)
	book := ContactBook(g)
	byRef := make(map[string]*bongov1.Contact, len(refs))
	err := s.scanContacts(userID, func(c *bongov1.Contact) {
		if ContactBook(c) != book {
			return
		}
		ref := ContactIdentity(c)
		if _, dup := byRef[ref]; !dup {
			byRef[ref] = c
		}
	})
	if err != nil {
		return nil, err
	}
	out := &ResolvedContactGroup{}
	for _, ref := range refs {
		if strings.HasPrefix(ref, "mailto:") {
			out.Members = append(out.Members, ContactGroupMember{Ref: ref, Address: strings.TrimPrefix(ref, "mailto:")})
			continue
		}
		c := byRef[ref]
		if c == nil {
			continue
		}
		if c.Guid == g.Guid || IsContactGroup(c) {
			out.Nested++
			continue
		}
		out.Members = append(out.Members, ContactGroupMember{Ref: ref, Contact: c})
	}
	return out, nil
}

// ExpandedContactGroup is a group's addresses for a mail message.
type ExpandedContactGroup struct {
	// Addresses are lower-case and de-duplicated, in member order.
	Addresses []string
	// Skipped counts members that give no address (no email, or a nested group).
	Skipped int
}

// ExpandContactGroup returns the addresses of a group's members. A resolved
// member with no email, and a nested group, count in Skipped; a reference that
// resolves to nothing does not.
func (s *ContactStore) ExpandContactGroup(userID string, g *bongov1.Contact) (*ExpandedContactGroup, error) {
	res, err := s.ResolveContactGroup(userID, g)
	if err != nil {
		return nil, err
	}
	out := &ExpandedContactGroup{Skipped: res.Nested}
	seen := make(map[string]bool, len(res.Members))
	for _, m := range res.Members {
		addr := m.Address
		if m.Contact != nil {
			addr = contactPrimaryAddress(m.Contact)
		}
		if addr == "" {
			out.Skipped++
			continue
		}
		if seen[addr] {
			continue
		}
		seen[addr] = true
		out.Addresses = append(out.Addresses, addr)
	}
	return out, nil
}

// GroupsReferencing returns the contact groups of c's address book that list c.
func (s *ContactStore) GroupsReferencing(userID string, c *bongov1.Contact) ([]*bongov1.Contact, error) {
	groups, err := s.ListContactGroups(userID, ContactBook(c))
	if err != nil {
		return nil, err
	}
	ref := ContactIdentity(c)
	var out []*bongov1.Contact
	for _, g := range groups {
		_, members := ContactGroupInfo(g)
		for _, m := range members {
			if m == ref {
				out = append(out, g)
				break
			}
		}
	}
	return out, nil
}

func decodeCard(raw []byte) (vcard.Card, error) {
	card, err := vcard.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil, fmt.Errorf("decoding vcard: %w", err)
	}
	if card.Get(vcard.FieldVersion) == nil {
		card.SetValue(vcard.FieldVersion, "3.0")
	}
	return card, nil
}

func encodeCard(card vcard.Card) ([]byte, error) {
	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return nil, fmt.Errorf("encoding vcard: %w", err)
	}
	return buf.Bytes(), nil
}

func setGroupName(card vcard.Card, name string) {
	card.SetValue(vcard.FieldFormattedName, name)
	card.SetName(&vcard.Name{FamilyName: name})
}

// NewContactGroupCard returns the vCard of a new contact group in Apple's form:
// VERSION 3.0, X-ADDRESSBOOKSERVER-KIND:group, N, FN and UID. The Apple form is
// the one Apple Contacts, DAVx5 and Thunderbird read for a 3.0 card.
func NewContactGroupCard(name, uid string) ([]byte, error) {
	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "3.0")
	card.SetValue(fieldAppleKind, ContactKindGroup)
	card.SetValue(vcard.FieldUID, uid)
	setGroupName(card, name)
	return encodeCard(card)
}

// groupForms says which forms the group card is written in.
func groupForms(card vcard.Card) (v4, apple bool) {
	for _, k := range card.Values(vcard.FieldKind) {
		if strings.EqualFold(strings.TrimSpace(k), ContactKindGroup) {
			v4 = true
		}
	}
	for _, k := range card.Values(fieldAppleKind) {
		if strings.EqualFold(strings.TrimSpace(k), ContactKindGroup) {
			apple = true
		}
	}
	return v4, apple
}

// AddContactGroupMember adds a member to a group card, in whichever form or
// forms the card uses, and returns the new card. A reference already present in
// a form is not added twice. value is a MEMBER value (see ContactMemberValue).
func AddContactGroupMember(raw []byte, value string) ([]byte, error) {
	card, err := decodeCard(raw)
	if err != nil {
		return nil, err
	}
	v4, apple := groupForms(card)
	if !v4 && !apple {
		return nil, ErrNotContactGroup
	}
	ref := ContactMemberRef(value)
	if ref == "" {
		return nil, fmt.Errorf("empty member reference")
	}
	add := func(field string) {
		for _, v := range card.Values(field) {
			if ContactMemberRef(v) == ref {
				return
			}
		}
		card.Add(field, &vcard.Field{Value: value})
	}
	if v4 {
		add(vcard.FieldMember)
	}
	if apple {
		add(fieldAppleMember)
	}
	return encodeCard(card)
}

// RemoveContactGroupMember removes every member of the card whose canonical
// reference is ref, in both forms. It reports whether the card changed.
func RemoveContactGroupMember(raw []byte, ref string) ([]byte, bool, error) {
	card, err := decodeCard(raw)
	if err != nil {
		return nil, false, err
	}
	if v4, apple := groupForms(card); !v4 && !apple {
		return nil, false, ErrNotContactGroup
	}
	changed := false
	for _, field := range []string{vcard.FieldMember, fieldAppleMember} {
		fields := card[field]
		kept := fields[:0:0]
		for _, f := range fields {
			if ContactMemberRef(f.Value) == ref {
				changed = true
				continue
			}
			kept = append(kept, f)
		}
		if len(kept) == 0 {
			delete(card, field)
		} else {
			card[field] = kept
		}
	}
	if !changed {
		return raw, false, nil
	}
	out, err := encodeCard(card)
	return out, true, err
}

// RenameContactGroup sets the group's name: FN, and N, which Apple shows.
func RenameContactGroup(raw []byte, name string) ([]byte, error) {
	card, err := decodeCard(raw)
	if err != nil {
		return nil, err
	}
	if v4, apple := groupForms(card); !v4 && !apple {
		return nil, ErrNotContactGroup
	}
	card.SetValue(vcard.FieldFormattedName, name)
	if _, has := card[vcard.FieldName]; has || card.Get(fieldAppleKind) != nil {
		card.SetName(&vcard.Name{FamilyName: name})
	}
	return encodeCard(card)
}

// EnsureContactUID returns the UID of the card in raw, adding one first when the
// card has none or only an unsafe one (see safeUID): uid is then written over it.
// changed reports that raw was rewritten.
func EnsureContactUID(raw []byte, uid string) (out []byte, got string, changed bool, err error) {
	if existing := vcardUID(raw); existing != "" {
		return raw, existing, false, nil
	}
	card, err := decodeCard(raw)
	if err != nil {
		return nil, "", false, err
	}
	if existing := strings.TrimSpace(card.Value(vcard.FieldUID)); safeUID(existing) {
		return raw, existing, false, nil
	}
	card.SetValue(vcard.FieldUID, uid)
	out, err = encodeCard(card)
	return out, uid, true, err
}

// NewContactUID returns a random UUID string for a card that has none and whose
// GUID is not 32 hex.
func NewContactUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	u, _ := UUIDFromGUID(hex.EncodeToString(b[:]))
	return u
}
