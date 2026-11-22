package contactio

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/emersion/go-vcard"
)

// Person is a contact to export. Ref is the identity contact groups use for it.
type Person struct {
	Card vcard.Card
	Ref  string
}

// GroupOut is a contact group to export; Refs name its members.
type GroupOut struct {
	Name string
	Refs []string
}

// ldifSafe reports whether v can be written as a plain SAFE-STRING (RFC 2849):
// ASCII with no NUL, CR or LF, and not starting with a space, colon or "<" nor
// ending with a space.
func ldifSafe(v string) bool {
	if v == "" {
		return true
	}
	if v[0] == ' ' || v[0] == ':' || v[0] == '<' || v[len(v)-1] == ' ' {
		return false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c == 0 || c == '\n' || c == '\r' || c > 127 {
			return false
		}
	}
	return true
}

// ldifLine writes "name: value" (or "name:: base64"), folded at 76 columns.
func ldifLine(w io.Writer, name, value string) error {
	l := name + ": " + value
	if !ldifSafe(value) {
		l = name + ":: " + base64.StdEncoding.EncodeToString([]byte(value))
	}
	for first := true; len(l) > 0; first = false {
		limit := 76
		if !first {
			limit = 75
		}
		chunk := l
		if len(chunk) > limit {
			chunk = chunk[:limit]
		}
		l = l[len(chunk):]
		prefix := ""
		if !first {
			prefix = " "
		}
		if _, err := io.WriteString(w, prefix+chunk+"\n"); err != nil {
			return err
		}
	}
	return nil
}

// escapeDN escapes a value for use in a distinguished name (RFC 4514).
func escapeDN(v string) string {
	v = strings.Join(strings.Fields(strings.NewReplacer("\n", " ", "\r", " ", "\x00", "").Replace(v)), " ")
	var b strings.Builder
	for i, r := range v {
		if strings.ContainsRune(`,+"\<>;=`, r) || (i == 0 && r == '#') {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// dnFor builds "cn=<name>,mail=<address>"; used makes it unique.
func dnFor(name, mail string, used map[string]bool, discriminator string) string {
	dn := "cn=" + escapeDN(name)
	if mail != "" {
		dn += ",mail=" + escapeDN(mail)
	}
	if used[normDN(dn)] {
		dn += "+uid=" + escapeDN(discriminator)
	}
	used[normDN(dn)] = true
	return dn
}

// WriteLDIF writes people as Thunderbird's mozillaAbPersonAlpha entries and
// groups as groupOfNames entries whose members are the people they name. A
// member that is not among people is left out.
func WriteLDIF(w io.Writer, people []Person, groups []GroupOut) error {
	if _, err := io.WriteString(w, "version: 1\n\n"); err != nil {
		return err
	}
	used := map[string]bool{}
	dnOf := map[string]string{}
	dns := make([]string, len(people))
	views := make([]*view, len(people))
	for i, p := range people {
		v := viewOf(p.Card)
		mail := ""
		if len(v.emails) > 0 {
			mail = v.emails[0].value
		}
		dns[i] = dnFor(firstNonEmpty(v.fn, mail), mail, used, fmt.Sprint(i+1))
		views[i] = v
		if _, dup := dnOf[p.Ref]; p.Ref != "" && !dup {
			dnOf[p.Ref] = dns[i]
		}
	}
	for i := range people {
		if err := writePerson(w, dns[i], views[i]); err != nil {
			return err
		}
	}
	for i, g := range groups {
		lines := [][2]string{
			{"dn", dnFor(g.Name, "", used, fmt.Sprintf("group-%d", i+1))},
			{"objectclass", "top"}, {"objectclass", "groupOfNames"}, {"cn", g.Name},
		}
		for _, ref := range g.Refs {
			if m, ok := dnOf[ref]; ok {
				lines = append(lines, [2]string{"member", m})
			}
		}
		if err := writeLines(w, lines); err != nil {
			return err
		}
	}
	return nil
}

func writeLines(w io.Writer, lines [][2]string) error {
	for _, l := range lines {
		if l[1] == "" {
			continue
		}
		if err := ldifLine(w, l[0], l[1]); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func writePerson(w io.Writer, dn string, v *view) error {
	lines := [][2]string{{"dn", dn}}
	add := func(name, value string) { lines = append(lines, [2]string{name, value}) }
	for _, c := range []string{"top", "person", "organizationalPerson", "inetOrgPerson", "mozillaAbPersonAlpha"} {
		add("objectclass", c)
	}
	add("cn", v.fn)
	add("givenName", v.given)
	add("sn", v.family)
	add("mozillaNickname", v.nickname)
	for i, e := range v.emails {
		if i == 1 {
			add("mozillaSecondEmail", e.value)
		} else {
			add("mail", e.value)
		}
	}
	for _, e := range v.phones {
		switch {
		case has(e.types, "fax"):
			add("facsimileTelephoneNumber", e.value)
		case has(e.types, "pager"):
			add("pager", e.value)
		case has(e.types, "cell"):
			add("mobile", e.value)
		case has(e.types, "home"):
			add("homePhone", e.value)
		default:
			add("telephoneNumber", e.value)
		}
	}
	var work, home *addr
	for _, a := range v.addrs {
		switch {
		case has(a.types, "home") && home == nil:
			home = a
		case !has(a.types, "home") && work == nil:
			work = a
		}
	}
	addAddr := func(a *addr, street, street2, city, state, postal, country string) {
		if a == nil {
			return
		}
		first, rest, _ := strings.Cut(a.street, "\n")
		add(street, first)
		add(street2, rest)
		add(city, a.city)
		add(state, a.region)
		add(postal, a.postal)
		add(country, a.country)
	}
	addAddr(work, "street", "mozillaWorkStreet2", "l", "st", "postalCode", "c")
	addAddr(home, "mozillaHomeStreet", "mozillaHomeStreet2", "mozillaHomeLocalityName", "mozillaHomeState", "mozillaHomePostalCode", "mozillaHomeCountryName")
	add("o", v.org)
	add("ou", v.dept)
	add("title", v.title)
	for i, u := range v.urls {
		if has(u.types, "home") || i == 1 {
			add("mozillaHomeUrl", u.value)
		} else {
			add("mozillaWorkUrl", u.value)
		}
	}
	if bd := v.birthday; strings.HasPrefix(bd, "--") {
		add("birthmonth", bd[2:4])
		add("birthday", bd[5:7])
	} else if bd != "" {
		add("birthyear", bd[0:4])
		add("birthmonth", bd[5:7])
		add("birthday", bd[8:10])
	}
	add("description", v.notes)
	return writeLines(w, lines)
}
