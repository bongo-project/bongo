package mail

import "strings"

// SanitizeHeaderValue strips control characters (U+0000–U+001F and U+007F),
// including CR (\r) and LF (\n), from a header field value to prevent
// header-injection attacks. Apply to every user-supplied value written into a
// raw RFC 5322 header line.
func SanitizeHeaderValue(v string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, v)
}

// SanitizeHeaderFilename sanitizes a filename for use inside a quoted-string
// MIME parameter (Content-Type name= / Content-Disposition filename=). In
// addition to stripping control characters, it removes double-quote characters
// so the value cannot break out of the surrounding quoted-string.
func SanitizeHeaderFilename(v string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F || r == '"' {
			return -1
		}
		return r
	}, v)
}
