package templates

import (
	"context"
	"strings"
	"unicode"
)

// accountInitials returns up to two upper-case initials taken from the local
// part of an email address, split on '.', '_' and '-'. It returns "?" when no
// letter or digit is available, so the account chip always has a label.
func accountInitials(email string) string {
	local := email
	if i := strings.IndexByte(email, '@'); i >= 0 {
		local = email[:i]
	}
	parts := strings.FieldsFunc(local, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	var out []rune
	for _, p := range parts {
		for _, r := range p {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// accountMenuLabel is the accessible name of the account menu button. When the
// page has no email (2FA setup pages) it falls back to the generic label.
func accountMenuLabel(ctx context.Context, email string) string {
	label := t(ctx, "nav.accountMenu")
	if email == "" {
		return label
	}
	return label + ": " + email
}
