package contactio

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-vcard"
)

// entry is one typed value: an email address, phone number or URL.
type entry struct {
	value string
	types []string
	pref  bool
}

// addr is one postal address.
type addr struct {
	types                                             []string
	pref                                              bool
	pobox, ext, street, city, region, postal, country string
}

func (a *addr) empty() bool {
	return a.pobox == "" && a.ext == "" && a.street == "" && a.city == "" &&
		a.region == "" && a.postal == "" && a.country == ""
}

// person collects what a CSV row or LDIF entry says about one contact before it
// becomes a vCard.
type person struct {
	prefix, given, middle, family, suffix string
	fileAs, nickname                      string
	org, dept, title                      string
	birthday, notes                       string
	emails, phones, urls                  []entry
	addrs                                 []*addr

	// Filled while a CSV row is read; finish settles them.
	pairs    map[string]*pair
	pairKeys []string
	gaddrs   map[string]*gaddr
	addrKeys []string
}

// clean turns every line end into LF and trims the ends of a value.
func clean(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

// line is clean for a value that is one line: an email address, a phone number.
func line(s string) string { return strings.ReplaceAll(clean(s), "\n", " ") }

// comp is clean for a component of a structured value (N, ADR, ORG). go-vcard
// escapes backslash, LF and comma but not the semicolon that separates
// components, so a semicolon inside one would corrupt the structure; it becomes
// a comma instead.
func comp(s string) string { return strings.ReplaceAll(clean(s), ";", ",") }

// labelTypes maps a Google label ("Home", "* Work Fax", "Mobile") to vCard TYPE
// values and the preferred flag. An unknown label is kept as a lower-case token.
func labelTypes(label string) (types []string, pref bool) {
	label = strings.TrimSpace(label)
	if strings.HasPrefix(label, "*") {
		pref = true
		label = strings.TrimSpace(strings.TrimPrefix(label, "*"))
	}
	switch l := strings.ToLower(label); l {
	case "":
		return nil, pref
	case "home", "work", "other":
		return []string{l}, pref
	case "mobile":
		return []string{"cell"}, pref
	case "home fax":
		return []string{"fax", "home"}, pref
	case "work fax":
		return []string{"fax", "work"}, pref
	default:
		return []string{typeToken(l)}, pref
	}
}

var nonToken = regexp.MustCompile(`[^a-z0-9-]+`)

func typeToken(s string) string {
	return strings.Trim(nonToken.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func typeParams(types []string, pref bool) vcard.Params {
	var ts []string
	for _, t := range types {
		if t = typeToken(t); t != "" {
			ts = append(ts, t)
		}
	}
	if pref {
		ts = append(ts, "pref")
	}
	if len(ts) == 0 {
		return nil
	}
	return vcard.Params{vcard.ParamType: ts}
}

// build returns the person as a 3.0 vCard, or ok=false when there is nothing
// worth keeping (no name, email address, phone number or organisation).
func (p *person) build() (raw []byte, ok bool) {
	composed := strings.Join(nonEmpty(p.prefix, p.given, p.middle, p.family, p.suffix), " ")
	var firstEmail, firstPhone string
	if len(p.emails) > 0 {
		firstEmail = p.emails[0].value
	}
	if len(p.phones) > 0 {
		firstPhone = p.phones[0].value
	}
	fn := firstNonEmpty(p.fileAs, composed, firstEmail, p.org, firstPhone)
	if fn == "" {
		return nil, false
	}

	card := make(vcard.Card)
	card.SetValue(vcard.FieldVersion, "3.0")
	card.SetValue(vcard.FieldFormattedName, fn)
	if composed != "" {
		card.SetName(&vcard.Name{
			FamilyName: p.family, GivenName: p.given, AdditionalName: p.middle,
			HonorificPrefix: p.prefix, HonorificSuffix: p.suffix,
		})
	}
	if p.nickname != "" {
		card.SetValue(vcard.FieldNickname, p.nickname)
	}
	if p.org != "" || p.dept != "" {
		org := p.org
		if p.dept != "" {
			org += ";" + p.dept
		}
		card.SetValue(vcard.FieldOrganization, org)
	}
	if p.title != "" {
		card.SetValue(vcard.FieldTitle, p.title)
	}
	if p.birthday != "" {
		card.SetValue(vcard.FieldBirthday, p.birthday)
	}
	if p.notes != "" {
		card.SetValue(vcard.FieldNote, p.notes)
	}
	for _, e := range p.emails {
		card.Add(vcard.FieldEmail, &vcard.Field{Value: e.value, Params: typeParams(e.types, e.pref)})
	}
	for _, e := range p.phones {
		card.Add(vcard.FieldTelephone, &vcard.Field{Value: e.value, Params: typeParams(e.types, e.pref)})
	}
	for _, e := range p.urls {
		card.Add(vcard.FieldURL, &vcard.Field{Value: e.value, Params: typeParams(e.types, e.pref)})
	}
	for _, a := range p.addrs {
		if a.empty() {
			continue
		}
		card.AddAddress(&vcard.Address{
			Field:           &vcard.Field{Params: typeParams(a.types, a.pref)},
			PostOfficeBox:   a.pobox,
			ExtendedAddress: a.ext,
			StreetAddress:   a.street,
			Locality:        a.city,
			Region:          a.region,
			PostalCode:      a.postal,
			Country:         a.country,
		})
	}
	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

var (
	reISODate   = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	reCompact   = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})$`)
	reNoYear    = regexp.MustCompile(`^--(\d{2})-?(\d{2})$`)
	reSlashDate = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)
	reDotDate   = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})\.(\d{4})$`)
)

// normaliseBirthday returns s as YYYY-MM-DD or --MM-DD. An empty value and the
// "no date" placeholders Outlook writes (0/0/00, 1/1/1601) give "" with ok. Any
// other text that is not a real date is not ok.
func normaliseBirthday(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "Tt "); i > 0 {
		s = s[:i]
	}
	switch s {
	case "", "0/0/00", "0/0/0000", "1/1/1601", "1601-01-01", "0000-00-00":
		return "", true
	}
	num := func(v string) int { n, _ := strconv.Atoi(v); return n }
	if m := reNoYear.FindStringSubmatch(s); m != nil {
		if !validDate(2000, num(m[1]), num(m[2])) {
			return "", false
		}
		return "--" + m[1] + "-" + m[2], true
	}
	var y, mo, d int
	switch {
	case reISODate.MatchString(s):
		m := reISODate.FindStringSubmatch(s)
		y, mo, d = num(m[1]), num(m[2]), num(m[3])
	case reCompact.MatchString(s):
		m := reCompact.FindStringSubmatch(s)
		y, mo, d = num(m[1]), num(m[2]), num(m[3])
	case reSlashDate.MatchString(s):
		m := reSlashDate.FindStringSubmatch(s)
		a, b := num(m[1]), num(m[2])
		mo, d, y = a, b, num(m[3])
		if a > 12 {
			mo, d = b, a
		}
	case reDotDate.MatchString(s):
		m := reDotDate.FindStringSubmatch(s)
		d, mo, y = num(m[1]), num(m[2]), num(m[3])
	default:
		return "", false
	}
	if y == 1601 && mo == 1 && d == 1 {
		return "", true
	}
	if !validDate(y, mo, d) {
		return "", false
	}
	return pad4(y) + "-" + pad2(mo) + "-" + pad2(d), true
}

func validDate(y, m, d int) bool {
	if y < 1 || m < 1 || m > 12 || d < 1 {
		return false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Year() == y && int(t.Month()) == m && t.Day() == d
}

func pad2(n int) string { return strings.Repeat("0", 2-len(strconv.Itoa(n))) + strconv.Itoa(n) }
func pad4(n int) string {
	s := strconv.Itoa(n)
	if len(s) >= 4 {
		return s
	}
	return strings.Repeat("0", 4-len(s)) + s
}
