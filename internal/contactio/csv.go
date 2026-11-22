package contactio

import (
	"encoding/csv"
	"errors"
	"io"
	"regexp"
	"strings"
)

// formulaStarts are the first characters a spreadsheet reads as a formula. The
// writer prefixes such a cell with an apostrophe and the reader removes that
// apostrophe again, so a Bongo export imports back unchanged.
const formulaStarts = "=+-@\t\r"

// neutralise prefixes a cell that a spreadsheet would run as a formula.
func neutralise(s string) string {
	if s != "" && strings.IndexByte(formulaStarts, s[0]) >= 0 {
		return "'" + s
	}
	return s
}

// unneutralise reverses neutralise. A leading apostrophe followed by anything
// else is the user's own and stays.
func unneutralise(s string) string {
	if len(s) >= 2 && s[0] == '\'' && strings.IndexByte(formulaStarts, s[1]) >= 0 {
		return s[1:]
	}
	return s
}

// normHeader lower-cases, trims and collapses spaces, and reads "e-mail" as
// "email" so the Google and Outlook spellings meet.
func normHeader(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.ReplaceAll(h, "e-mail", "email")
	return strings.Join(strings.Fields(h), " ")
}

var (
	reIndexed = regexp.MustCompile(`^(email|phone|address|website) (\d+) - (.+)$`)
	reOrgN    = regexp.MustCompile(`^organization (\d+) - (name|title|department)$`)
	reOutMail = regexp.MustCompile(`^email(?: (\d+))? address$`)
)

// A setter applies one cell to a person.
type setter func(p *person, v string)

// column is one classified header.
type column struct {
	set setter
	// ident is true for a header that names a person (a name, an organisation
	// or an email address), so a file with two harmless headers is not taken
	// for a contact list.
	ident bool
}

// outlookPhones maps an Outlook phone header to vCard types.
var outlookPhones = map[string][]string{
	"business phone": {"work"}, "business phone 2": {"work"},
	"home phone": {"home"}, "home phone 2": {"home"},
	"mobile phone": {"cell"},
	"business fax": {"fax", "work"}, "home fax": {"fax", "home"},
	"other phone": {"other"}, "pager": {"pager"}, "car phone": {"car"},
	"company main phone": {"work"},
}

// classify returns the column a header names, or ok=false for a header the
// importer does not use (a photo, a label, a phonetic name).
func classify(header string) (column, bool) {
	h := normHeader(header)
	text := func(dst func(p *person) *string, ident bool) (column, bool) {
		return column{set: func(p *person, v string) { *dst(p) = comp(v) }, ident: ident}, true
	}
	switch h {
	case "first name", "given name":
		return text(func(p *person) *string { return &p.given }, true)
	case "middle name", "additional name":
		return text(func(p *person) *string { return &p.middle }, false)
	case "last name", "family name":
		return text(func(p *person) *string { return &p.family }, true)
	case "name prefix", "title":
		// Outlook's Title is the honorific, not the job title.
		return text(func(p *person) *string { return &p.prefix }, false)
	case "name suffix", "suffix":
		return text(func(p *person) *string { return &p.suffix }, false)
	case "nickname":
		return text(func(p *person) *string { return &p.nickname }, false)
	case "file as", "name":
		return text(func(p *person) *string { return &p.fileAs }, true)
	case "organization name", "company":
		return text(func(p *person) *string { return &p.org }, true)
	case "organization title", "job title":
		return text(func(p *person) *string { return &p.title }, false)
	case "organization department", "department":
		return text(func(p *person) *string { return &p.dept }, false)
	case "notes":
		return column{set: func(p *person, v string) { p.notes = clean(v) }}, true
	case "birthday":
		return column{set: func(p *person, v string) { p.birthday = strings.TrimSpace(v) }}, true
	case "web page", "personal web page":
		return column{set: func(p *person, v string) {
			if v = line(v); v != "" {
				p.urls = append(p.urls, entry{value: v})
			}
		}}, true
	}
	if types, ok := outlookPhones[h]; ok {
		return column{set: func(p *person, v string) {
			if v = line(v); v != "" {
				p.phones = append(p.phones, entry{value: v, types: types})
			}
		}}, true
	}
	if h == "primary phone" {
		return column{set: func(p *person, v string) {
			if v = line(v); v != "" {
				p.phones = append(p.phones, entry{value: v, pref: true})
			}
		}}, true
	}
	if reOutMail.MatchString(h) {
		return column{set: func(p *person, v string) {
			if v = line(v); v != "" {
				p.emails = append(p.emails, entry{value: v})
			}
		}, ident: true}, true
	}
	if c, ok := outlookAddress(h); ok {
		return c, true
	}
	if m := reOrgN.FindStringSubmatch(h); m != nil {
		switch m[2] {
		case "name":
			return text(func(p *person) *string { return &p.org }, true)
		case "title":
			return text(func(p *person) *string { return &p.title }, false)
		default:
			return text(func(p *person) *string { return &p.dept }, false)
		}
	}
	if m := reIndexed.FindStringSubmatch(h); m != nil {
		return indexedColumn(m[1], m[2], m[3])
	}
	return column{}, false
}

// outlookAddress reads "business street 2", "home postal code" and the like.
func outlookAddress(h string) (column, bool) {
	kind, rest, ok := strings.Cut(h, " ")
	if !ok || (kind != "business" && kind != "home" && kind != "other") {
		return column{}, false
	}
	var field func(a *addr) *string
	switch rest {
	case "street":
		field = func(a *addr) *string { return &a.street }
	case "street 2", "street 3":
		// Extra street lines join the street, one per line.
		typ := kindType(kind)
		return column{set: func(p *person, v string) {
			a := p.addrOf("out:" + kind)
			a.types = []string{typ}
			if v = comp(v); v != "" {
				a.street += "\n" + v
			}
		}}, true
	case "city":
		field = func(a *addr) *string { return &a.city }
	case "state":
		field = func(a *addr) *string { return &a.region }
	case "postal code":
		field = func(a *addr) *string { return &a.postal }
	case "country/region", "country":
		field = func(a *addr) *string { return &a.country }
	default:
		return column{}, false
	}
	typ := kindType(kind)
	return column{set: func(p *person, v string) {
		a := p.addrOf("out:" + kind)
		a.types = []string{typ}
		*field(&a.addr) = comp(v)
	}}, true
}

// indexedColumn reads Google's "E-mail 1 - Value" family.
func indexedColumn(kind, n, part string) (column, bool) {
	key := kind + n
	switch kind {
	case "email", "phone", "website":
		switch part {
		case "label", "type":
			return column{set: func(p *person, v string) { p.pairOf(key, kind).label = v }}, true
		case "value":
			return column{set: func(p *person, v string) { p.pairOf(key, kind).value = v }, ident: kind == "email"}, true
		}
	case "address":
		var field func(a *addr) *string
		switch part {
		case "label", "type":
			return column{set: func(p *person, v string) { p.addrOf(key).label = v }}, true
		case "street":
			field = func(a *addr) *string { return &a.street }
		case "city":
			field = func(a *addr) *string { return &a.city }
		case "po box":
			field = func(a *addr) *string { return &a.pobox }
		case "region":
			field = func(a *addr) *string { return &a.region }
		case "postal code":
			field = func(a *addr) *string { return &a.postal }
		case "country":
			field = func(a *addr) *string { return &a.country }
		case "extended address":
			field = func(a *addr) *string { return &a.ext }
		case "formatted":
			return column{set: func(p *person, v string) { p.addrOf(key).formatted = clean(v) }}, true
		default:
			return column{}, false
		}
		return column{set: func(p *person, v string) { *field(&p.addrOf(key).addr) = comp(v) }}, true
	}
	return column{}, false
}

// pair is an indexed Google cell pair: a label and a value, possibly several
// values joined by " ::: ".
type pair struct{ kind, label, value string }

// gaddr is an address under construction from a CSV row.
type gaddr struct {
	addr
	label, formatted string
}

func (p *person) pairOf(key, kind string) *pair {
	if p.pairs == nil {
		p.pairs = map[string]*pair{}
	}
	if p.pairs[key] == nil {
		p.pairs[key] = &pair{kind: kind}
		p.pairKeys = append(p.pairKeys, key)
	}
	return p.pairs[key]
}

func (p *person) addrOf(key string) *gaddr {
	if p.gaddrs == nil {
		p.gaddrs = map[string]*gaddr{}
	}
	if p.gaddrs[key] == nil {
		p.gaddrs[key] = &gaddr{}
		p.addrKeys = append(p.addrKeys, key)
	}
	return p.gaddrs[key]
}

const multiSep = " ::: "

// finish turns the indexed cells of a row into entries and addresses, in
// column order, and settles the birthday.
func (p *person) finish() error {
	for _, key := range p.pairKeys {
		pr := p.pairs[key]
		labels := strings.Split(pr.label, multiSep)
		for i, v := range strings.Split(pr.value, multiSep) {
			if v = line(v); v == "" {
				continue
			}
			label := ""
			if i < len(labels) {
				label = labels[i]
			}
			types, pref := labelTypes(label)
			e := entry{value: v, types: types, pref: pref}
			switch pr.kind {
			case "email":
				p.emails = append(p.emails, e)
			case "phone":
				p.phones = append(p.phones, e)
			default:
				p.urls = append(p.urls, e)
			}
		}
	}
	for _, key := range p.addrKeys {
		g := p.gaddrs[key]
		a := g.addr
		a.street = strings.Trim(a.street, "\n")
		if a.empty() && g.formatted != "" {
			a.street = strings.ReplaceAll(g.formatted, ";", ",")
		}
		if len(a.types) == 0 {
			a.types, a.pref = labelTypes(g.label)
		}
		p.addrs = append(p.addrs, &a)
	}
	bd, ok := normaliseBirthday(p.birthday)
	if !ok {
		return errBirthday
	}
	p.birthday = bd
	return nil
}

var errBirthday = errors.New("birthday")

// csvDelimiter picks the delimiter whose header row names the most known
// columns (German Outlook writes semicolons). The file is a contact list only
// when at least two headers are known and one of them names a person.
func csvDelimiter(text string) (rune, bool) {
	best, bestScore := ',', 0
	for _, d := range []rune{',', ';', '\t'} {
		r := csv.NewReader(strings.NewReader(text))
		r.Comma = d
		r.FieldsPerRecord = -1
		head, err := r.Read()
		if err != nil {
			continue
		}
		known, ident := 0, false
		for _, h := range head {
			if c, ok := classify(h); ok {
				known++
				ident = ident || c.ident
			}
		}
		if known >= 2 && ident && known > bestScore {
			best, bestScore = d, known
		}
	}
	return best, bestScore > 0
}

func parseCSV(raw []byte, max int) (*Result, error) {
	text := string(raw)
	comma, ok := csvDelimiter(text)
	if !ok {
		return nil, ErrFormat
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = comma
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return nil, ErrFormat
	}
	cols := make([]column, len(head))
	known := make([]bool, len(head))
	for i, h := range head {
		cols[i], known[i] = classify(h)
	}
	// Trailing empty header cells are padding some tools add.
	width := len(head)
	for width > 0 && strings.TrimSpace(head[width-1]) == "" {
		width--
	}

	res := &Result{Format: FormatCSV}
	// row is the spreadsheet row: records are counted, and so are the blank
	// lines encoding/csv skips between them. prevEnd is the last line of the
	// record before.
	row := 1
	prevEnd := recordEnd(r, head)
	gap := func(startLine int) int {
		if startLine > prevEnd+1 {
			return startLine - prevEnd - 1
		}
		return 0
	}
	for {
		row++
		if max > 0 && row > max*skipFactor {
			return nil, ErrTooMany
		}
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				row += gap(pe.StartLine)
			}
			return nil, &ParseError{Format: FormatCSV, N: row, Reason: ReasonSyntax}
		}
		startLine, _ := r.FieldPos(0)
		row += gap(startLine)
		prevEnd = recordEnd(r, rec)
		for i := width; i < len(rec); i++ {
			if strings.TrimSpace(rec[i]) != "" {
				return nil, &ParseError{Format: FormatCSV, N: row, Reason: ReasonFields}
			}
		}
		p := &person{}
		for i := 0; i < len(rec) && i < len(cols); i++ {
			if !known[i] {
				continue
			}
			if v := unneutralise(rec[i]); strings.TrimSpace(v) != "" {
				cols[i].set(p, v)
			}
		}
		if err := p.finish(); err != nil {
			return nil, &ParseError{Format: FormatCSV, N: row, Reason: ReasonBirthday}
		}
		vc, ok := p.build()
		if !ok {
			res.Skipped++
			continue
		}
		res.Records = append(res.Records, Record{Ref: Ref{N: row}, Raw: vc})
		if max > 0 && len(res.Records) > max {
			return nil, ErrTooMany
		}
	}
	return res, nil
}

// kindType is the vCard type of an Outlook address kind.
func kindType(kind string) string {
	if kind == "business" {
		return "work"
	}
	return kind
}

// recordEnd is the last line of the record just read: its first line plus the
// line breaks inside its fields.
func recordEnd(r *csv.Reader, rec []string) int {
	line, _ := r.FieldPos(0)
	for _, f := range rec {
		line += strings.Count(f, "\n")
	}
	return line
}
