// Package tagname folds user-supplied labels into the tag name form the store
// accepts: lowercase ASCII letters, digits and single hyphens.
package tagname

import (
	"strings"
	"unicode"
)

// maxLen matches store.MaxTagNameLen.
const maxLen = 50

// Normalise converts an arbitrary label string into a valid tag name:
//   - Lowercase
//   - Spaces and underscores replaced with hyphens
//   - Non-alphanumeric, non-hyphen characters stripped
//   - Leading/trailing hyphens trimmed
//   - Empty or invalid results return ""
func Normalise(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))

	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '_', r == '-':
			b.WriteByte('-')
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// Non-ASCII letters/digits: skip (tags must be ASCII per tagNameRe).
		default:
			// Punctuation, symbols: skip.
		}
	}

	result := strings.Trim(b.String(), "-")
	// Collapse consecutive hyphens.
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	if len(result) == 0 || len(result) > maxLen {
		return ""
	}

	return result
}
