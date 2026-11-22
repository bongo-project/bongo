package contactio

import (
	"strings"

	"github.com/emersion/go-vcard"
)

// view is what the writers read from a card.
type view struct {
	fn                                    string
	prefix, given, middle, family, suffix string
	nickname, org, dept, title            string
	birthday, notes                       string
	emails, phones, urls                  []entry
	addrs                                 []*addr
}

// typesOf lower-cases the TYPE parameters of a field.
func typesOf(f *vcard.Field) (types []string, pref bool) {
	for _, t := range f.Params[vcard.ParamType] {
		for _, part := range strings.Split(t, ",") {
			switch t := strings.ToLower(strings.TrimSpace(part)); t {
			case "":
			case "pref":
				pref = true
			case "internet", "voice":
			default:
				types = append(types, t)
			}
		}
	}
	return types, pref
}

func entriesOf(card vcard.Card, field string) []entry {
	var out []entry
	for _, f := range card[field] {
		if v := line(f.Value); v != "" {
			types, pref := typesOf(f)
			out = append(out, entry{value: v, types: types, pref: pref})
		}
	}
	return out
}

func viewOf(card vcard.Card) *view {
	v := &view{
		fn:       clean(card.Value(vcard.FieldFormattedName)),
		nickname: clean(card.Value(vcard.FieldNickname)),
		title:    clean(card.Value(vcard.FieldTitle)),
		notes:    clean(card.Value(vcard.FieldNote)),
		emails:   entriesOf(card, vcard.FieldEmail),
		phones:   entriesOf(card, vcard.FieldTelephone),
		urls:     entriesOf(card, vcard.FieldURL),
	}
	if n := card.Name(); n != nil {
		v.prefix, v.given, v.middle = n.HonorificPrefix, n.GivenName, n.AdditionalName
		v.family, v.suffix = n.FamilyName, n.HonorificSuffix
	}
	org, dept, _ := strings.Cut(card.Value(vcard.FieldOrganization), ";")
	v.org, v.dept = clean(org), clean(strings.Split(dept, ";")[0])
	// A birthday the importer could not read again is left out rather than
	// written into a file that would then be refused.
	if bd, ok := normaliseBirthday(card.Value(vcard.FieldBirthday)); ok {
		v.birthday = bd
	}
	for _, a := range card.Addresses() {
		types, pref := typesOf(a.Field)
		out := &addr{
			types: types, pref: pref, pobox: a.PostOfficeBox, ext: a.ExtendedAddress,
			street: a.StreetAddress, city: a.Locality, region: a.Region,
			postal: a.PostalCode, country: a.Country,
		}
		if !out.empty() {
			v.addrs = append(v.addrs, out)
		}
	}
	if v.fn == "" {
		v.fn = strings.Join(nonEmpty(v.prefix, v.given, v.middle, v.family, v.suffix), " ")
	}
	return v
}

// composed is the name the name parts make, what File As is compared with.
func (v *view) composed() string {
	return strings.Join(nonEmpty(v.prefix, v.given, v.middle, v.family, v.suffix), " ")
}

// has reports whether types contains t.
func has(types []string, t string) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}
