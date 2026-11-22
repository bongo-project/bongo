package contactio

import (
	"encoding/base64"
	"strings"
	"unicode/utf8"
)

// attr is one attribute line of an LDIF entry, unfolded and decoded.
type attr struct{ name, value string }

// ldifEntry is a blank-line separated block.
type ldifEntry struct {
	line  int // first line of the entry
	attrs []attr
}

func (e *ldifEntry) all(name string) []string {
	var out []string
	for _, a := range e.attrs {
		if a.name == name {
			out = append(out, a.value)
		}
	}
	return out
}

func (e *ldifEntry) first(name string) string {
	for _, a := range e.attrs {
		if a.name == name {
			return a.value
		}
	}
	return ""
}

// readLDIF splits text into entries (RFC 2849). A line that starts with one
// space continues the line before it, a # line is a comment, and the optional
// "version: 1" line stands alone. Change records, URL values, bad base64 and a
// fold before any attribute are errors; nothing is ever fetched.
func readLDIF(text string, max int) ([]ldifEntry, error) {
	var (
		entries []ldifEntry
		cur     *ldifEntry
		comment bool
		n       int // entries read so far, for error numbers
	)
	fail := func(line int, r Reason) error {
		return &ParseError{Format: FormatLDIF, N: n + 1, Line: line, Reason: r}
	}
	flush := func() {
		if cur == nil {
			return
		}
		// A lone "version:" line is the file header, not an entry.
		if len(cur.attrs) == 1 && cur.attrs[0].name == "version" {
			cur = nil
			return
		}
		entries = append(entries, *cur)
		n++
		cur = nil
	}
	var sb strings.Builder // value of the open attribute, folds appended in O(1)
	var pending *attr      // last attribute, still open to continuation lines
	var pendingB64 bool
	var pendingLine int
	closePending := func() error {
		if pending == nil {
			return nil
		}
		defer func() { pending = nil }()
		pending.value = sb.String()
		if pendingB64 {
			dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pending.value))
			if err != nil || !utf8.Valid(dec) {
				return fail(pendingLine, ReasonBase64)
			}
			pending.value = string(dec)
		}
		cur.attrs = append(cur.attrs, *pending)
		return nil
	}

	ln := 0
	for rest := text; rest != ""; {
		var l string
		l, rest, _ = strings.Cut(rest, "\n")
		l = strings.TrimRight(l, "\r")
		ln++
		if strings.HasPrefix(l, " ") {
			if comment {
				continue
			}
			if pending == nil {
				return nil, fail(ln, ReasonSyntax)
			}
			sb.WriteString(l[1:])
			continue
		}
		comment = false
		if err := closePending(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(l) == "" {
			flush()
			if max > 0 && n > max*skipFactor {
				return nil, ErrTooMany
			}
			continue
		}
		if strings.HasPrefix(l, "#") {
			comment = true
			continue
		}
		if cur == nil {
			cur = &ldifEntry{line: ln}
		}
		name, val, ok := strings.Cut(l, ":")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fail(ln, ReasonSyntax)
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if j := strings.IndexByte(name, ';'); j >= 0 {
			name = name[:j] // language and binary options
		}
		pendingB64 = false
		switch {
		case strings.HasPrefix(val, ":"):
			pendingB64, val = true, val[1:]
		case strings.HasPrefix(val, "<"):
			return nil, fail(ln, ReasonURL)
		}
		if name == "changetype" || name == "control" {
			return nil, fail(ln, ReasonChange)
		}
		pending = &attr{name: name}
		sb.Reset()
		sb.WriteString(strings.TrimPrefix(val, " "))
		pendingLine = ln
	}
	if err := closePending(); err != nil {
		return nil, err
	}
	flush()
	if max > 0 && n > max*skipFactor {
		return nil, ErrTooMany
	}
	return entries, nil
}

// normDN makes two spellings of a distinguished name compare equal.
func normDN(dn string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(dn)), ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return strings.Join(parts, ",")
}

var ldifPersonClasses = map[string]bool{
	"person": true, "organizationalperson": true, "inetorgperson": true,
	"mozillaabpersonalpha": true, "mozillaabpersonobsolete": true, "mozillaabperson": true,
}

func parseLDIF(text []byte, max int) (*Result, error) {
	entries, err := readLDIF(string(text), max)
	if err != nil {
		return nil, err
	}
	res := &Result{Format: FormatLDIF}
	byDN := map[string]int{}
	type pendingGroup struct {
		idx  int
		dns  []string
		line int
	}
	var groups []pendingGroup

	for k, e := range entries {
		if max > 0 && len(res.Records) > max {
			return nil, ErrTooMany
		}
		classes := map[string]bool{}
		for _, c := range e.all("objectclass") {
			classes[strings.ToLower(strings.TrimSpace(c))] = true
		}
		ref := Ref{N: k + 1, Line: e.line}
		switch {
		case classes["groupofnames"] || classes["groupofuniquenames"]:
			g := &Group{Name: clean(e.first("cn"))}
			if g.Name == "" {
				res.Skipped++
				continue
			}
			var dns []string
			dns = append(dns, e.all("member")...)
			for _, u := range e.all("uniquemember") {
				if i := strings.Index(u, "#"); i >= 0 {
					u = u[:i]
				}
				dns = append(dns, u)
			}
			res.Records = append(res.Records, Record{Ref: ref, Group: g})
			groups = append(groups, pendingGroup{idx: len(res.Records) - 1, dns: dns, line: e.line})
		case len(classes) > 0 && !anyClass(classes, ldifPersonClasses):
			res.Skipped++
		default:
			p, err := ldifPerson(&e)
			if err != nil {
				return nil, &ParseError{Format: FormatLDIF, N: k + 1, Line: e.line, Reason: ReasonBirthday}
			}
			raw, ok := p.build()
			if !ok {
				res.Skipped++
				continue
			}
			res.Records = append(res.Records, Record{Ref: ref, Raw: raw})
			// Two people with one DN: group members resolve to the first.
			if k := normDN(e.first("dn")); !hasDN(byDN, k) {
				byDN[k] = len(res.Records) - 1
			}
		}
	}
	if max > 0 && len(res.Records) > max {
		return nil, ErrTooMany
	}
	for _, g := range groups {
		seen := map[int]bool{}
		for _, dn := range g.dns {
			if i, ok := byDN[normDN(dn)]; ok && !seen[i] {
				seen[i] = true
				res.Records[g.idx].Group.Members = append(res.Records[g.idx].Group.Members, i)
			}
		}
	}
	return res, nil
}

func anyClass(have, want map[string]bool) bool {
	for c := range have {
		if want[c] {
			return true
		}
	}
	return false
}

// ldifPerson maps Thunderbird's mozillaAbPersonAlpha attributes onto a person.
func ldifPerson(e *ldifEntry) (*person, error) {
	p := &person{
		given:    comp(e.first("givenname")),
		family:   comp(e.first("sn")),
		nickname: comp(e.first("mozillanickname")),
		org:      comp(e.first("o")),
		dept:     comp(firstNonEmpty(e.first("ou"), e.first("department"))),
		title:    comp(e.first("title")),
		notes:    clean(e.first("description")),
	}
	p.fileAs = comp(e.first("cn"))
	add := func(list *[]entry, names []string, types ...string) {
		for _, n := range names {
			for _, v := range e.all(strings.ToLower(n)) {
				if v = line(v); v != "" {
					*list = append(*list, entry{value: v, types: types})
				}
			}
		}
	}
	for i, v := range e.all("mail") {
		if v = line(v); v != "" {
			p.emails = append(p.emails, entry{value: v, pref: i == 0})
		}
	}
	add(&p.emails, []string{"mozillaSecondEmail"})
	add(&p.phones, []string{"telephoneNumber"}, "work")
	add(&p.phones, []string{"homePhone"}, "home")
	add(&p.phones, []string{"mobile"}, "cell")
	add(&p.phones, []string{"facsimileTelephoneNumber"}, "fax")
	add(&p.phones, []string{"pager"}, "pager")
	add(&p.urls, []string{"mozillaWorkUrl"}, "work")
	add(&p.urls, []string{"mozillaHomeUrl"}, "home")

	work := &addr{
		types: []string{"work"}, street: comp(e.first("street")),
		city: comp(e.first("l")), region: comp(e.first("st")),
		postal: comp(e.first("postalcode")), country: comp(e.first("c")),
	}
	joinStreet(work, e.first("mozillaworkstreet2"))
	home := &addr{
		types: []string{"home"}, street: comp(e.first("mozillahomestreet")),
		city: comp(e.first("mozillahomelocalityname")), region: comp(e.first("mozillahomestate")),
		postal: comp(e.first("mozillahomepostalcode")), country: comp(e.first("mozillahomecountryname")),
	}
	joinStreet(home, e.first("mozillahomestreet2"))
	p.addrs = []*addr{work, home}

	if y, m, d := e.first("birthyear"), e.first("birthmonth"), e.first("birthday"); m != "" && d != "" {
		v := "--" + pad2(atoi(m)) + "-" + pad2(atoi(d))
		if y != "" {
			v = pad4(atoi(y)) + v[1:]
		}
		p.birthday = v
	}
	bd, ok := normaliseBirthday(p.birthday)
	if !ok {
		return nil, errBirthday
	}
	p.birthday = bd
	return p, nil
}

func joinStreet(a *addr, second string) {
	if second = comp(second); second != "" {
		if a.street != "" {
			a.street += "\n"
		}
		a.street += second
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func hasDN(m map[string]int, k string) bool { _, ok := m[k]; return ok }
