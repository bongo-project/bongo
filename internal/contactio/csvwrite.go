package contactio

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"

	"github.com/emersion/go-vcard"
)

// labelOf is the Google label for a set of vCard types.
func labelOf(types []string, pref bool) string {
	label := ""
	switch {
	case has(types, "fax") && has(types, "home"):
		label = "Home Fax"
	case has(types, "fax") && has(types, "work"):
		label = "Work Fax"
	case has(types, "cell"):
		label = "Mobile"
	case has(types, "home"):
		label = "Home"
	case has(types, "work"):
		label = "Work"
	case has(types, "other"):
		label = "Other"
	case len(types) > 0:
		label = strings.ToUpper(types[0][:1]) + types[0][1:]
	}
	if pref && label != "" {
		return "* " + label
	}
	return label
}

// WriteCSV writes cards as a Google Contacts CSV: UTF-8 with a byte order mark
// (Excel needs it), CRLF line ends, the E-mail, Phone, Address and Website
// column groups repeated as often as the most populated contact needs, and every
// data cell that a spreadsheet would run as a formula prefixed with an
// apostrophe. Bongo's own import removes that apostrophe again; Google and
// Outlook keep it, so move contacts between services as vCard.
func WriteCSV(w io.Writer, cards []vcard.Card) error {
	views := make([]*view, len(cards))
	nE, nP, nA, nW := 1, 1, 1, 1
	for i, c := range cards {
		v := viewOf(c)
		views[i] = v
		nE, nP = max(nE, len(v.emails)), max(nP, len(v.phones))
		nA, nW = max(nA, len(v.addrs)), max(nW, len(v.urls))
	}

	head := []string{
		"First Name", "Middle Name", "Last Name", "Name Prefix", "Name Suffix", "Nickname", "File As",
		"Organization Name", "Organization Title", "Organization Department", "Birthday", "Notes",
	}
	for i := 1; i <= nE; i++ {
		n := strconv.Itoa(i)
		head = append(head, "E-mail "+n+" - Label", "E-mail "+n+" - Value")
	}
	for i := 1; i <= nP; i++ {
		n := strconv.Itoa(i)
		head = append(head, "Phone "+n+" - Label", "Phone "+n+" - Value")
	}
	for i := 1; i <= nA; i++ {
		n := strconv.Itoa(i)
		head = append(head, "Address "+n+" - Label", "Address "+n+" - Formatted", "Address "+n+" - Street",
			"Address "+n+" - City", "Address "+n+" - PO Box", "Address "+n+" - Region",
			"Address "+n+" - Postal Code", "Address "+n+" - Country", "Address "+n+" - Extended Address")
	}
	for i := 1; i <= nW; i++ {
		n := strconv.Itoa(i)
		head = append(head, "Website "+n+" - Label", "Website "+n+" - Value")
	}

	if _, err := io.WriteString(w, "\xEF\xBB\xBF"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	if err := cw.Write(head); err != nil {
		return err
	}
	for _, v := range views {
		row := writeRow(v, nE, nP, nA, nW)
		for i, c := range row {
			row[i] = neutralise(c)
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeRow(v *view, nE, nP, nA, nW int) []string {
	given, middle, family := v.given, v.middle, v.family
	if v.composed() == "" {
		given = v.fn
	}
	fileAs := ""
	if v.fn != "" && v.fn != strings.Join(nonEmpty(v.prefix, given, middle, family, v.suffix), " ") {
		fileAs = v.fn
	}
	row := []string{given, middle, family, v.prefix, v.suffix, v.nickname, fileAs, v.org, v.title, v.dept, v.birthday, v.notes}
	typed := func(list []entry, n int) {
		for i := range n {
			if i < len(list) {
				row = append(row, labelOf(list[i].types, list[i].pref), list[i].value)
			} else {
				row = append(row, "", "")
			}
		}
	}
	typed(v.emails, nE)
	typed(v.phones, nP)
	for i := range nA {
		if i >= len(v.addrs) {
			row = append(row, make([]string, 9)...)
			continue
		}
		a := v.addrs[i]
		formatted := strings.Join(nonEmpty(strings.ReplaceAll(a.street, "\n", " "), a.city, strings.TrimSpace(a.region+" "+a.postal), a.country), ", ")
		row = append(row, labelOf(a.types, a.pref), strings.TrimSpace(formatted), a.street, a.city, a.pobox, a.region, a.postal, a.country, a.ext)
	}
	typed(v.urls, nW)
	return row
}
