package contactio

import (
	"regexp"
	"strings"

	"github.com/emersion/go-vcard"
)

// shareFields are the only properties a card sent as "my card" carries. Anything
// else (NOTE, UID, REV, CATEGORIES, PHOTO, MEMBER, every X- extension and so on)
// stays private: the user's own record holds things they never meant to send.
var shareFields = map[string]bool{
	vcard.FieldFormattedName: true,
	vcard.FieldName:          true,
	vcard.FieldNickname:      true,
	vcard.FieldOrganization:  true,
	vcard.FieldTitle:         true,
	vcard.FieldRole:          true,
	vcard.FieldEmail:         true,
	vcard.FieldTelephone:     true,
	vcard.FieldAddress:       true,
	vcard.FieldURL:           true,
	vcard.FieldBirthday:      true,
}

// shareParams are the only parameters kept on an allowed property. GEO, TZ,
// LABEL, PID, ALTID, SORT-AS, LANGUAGE and every X- parameter are dropped: a
// parameter on an ADR can carry home coordinates and an X- one anything at all.
var shareParams = map[string]bool{"TYPE": true, "PREF": true, "VALUE": true, "CHARSET": true}

// ShareCard builds the vCard attached to an outgoing message from card, by
// allowlist. The stored VERSION is kept (3.0 when absent); a 3.0 card with no N
// gets an empty one, as 3.0 requires it. Line breaks and control characters are
// stripped from every value, so a stored value cannot start another property.
func ShareCard(card vcard.Card) vcard.Card {
	out := make(vcard.Card)
	version := "3.0"
	if f := card.Get(vcard.FieldVersion); f != nil {
		if v := shareClean(f.Value); v != "" {
			version = v
		}
	}
	out.SetValue(vcard.FieldVersion, version)
	for key, fields := range card {
		name := strings.ToUpper(key)
		if !shareFields[name] {
			continue
		}
		for _, f := range fields {
			nf := &vcard.Field{Value: shareClean(f.Value), Group: shareClean(f.Group)}
			if len(f.Params) > 0 {
				nf.Params = make(vcard.Params, len(f.Params))
				for pk, pvs := range f.Params {
					pname := strings.ToUpper(shareClean(pk))
					if !shareParams[pname] {
						continue
					}
					for _, pv := range pvs {
						nf.Params[pname] = append(nf.Params[pname], shareClean(pv))
					}
				}
				if len(nf.Params) == 0 {
					nf.Params = nil
				}
			}
			out[name] = append(out[name], nf)
		}
	}
	if version == "3.0" && out.Get(vcard.FieldName) == nil {
		out.Set(vcard.FieldName, &vcard.Field{Value: ";;;;"})
	}
	return out
}

// shareClean drops every control character, line breaks included.
func shareClean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

var filenameSafe = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,60}$`)

// CardFilename is the attachment name for a card: FN plus ".vcf" when FN is
// plain ASCII of up to 60 characters, else "contact.vcf", so no user text that
// needs quoting or encoding reaches a header.
func CardFilename(fn string) string {
	if filenameSafe.MatchString(fn) {
		return fn + ".vcf"
	}
	return "contact.vcf"
}
