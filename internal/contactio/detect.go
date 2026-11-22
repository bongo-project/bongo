package contactio

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

// decodeText returns raw as UTF-8 with no byte order mark and normalised line
// ends. UTF-16 with a mark (Outlook's "Unicode CSV") is converted. Bytes that
// are not valid UTF-8 are read as Windows-1252, which is what Outlook writes for
// an ANSI file; a Latin-2 file therefore gets wrong accents, deterministically.
func decodeText(raw []byte) ([]byte, error) {
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}):
		out, err := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder().Bytes(raw[2:])
		if err != nil {
			return nil, ErrEncoding
		}
		raw = out
	case bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		out, err := unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder().Bytes(raw[2:])
		if err != nil {
			return nil, ErrEncoding
		}
		raw = out
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		raw = raw[3:]
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return nil, ErrEncoding
	}
	if !utf8.Valid(raw) {
		out, err := charmap.Windows1252.NewDecoder().Bytes(raw)
		if err != nil {
			return nil, ErrEncoding
		}
		raw = out
	}
	raw = bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF"))
	// A file with no LF at all but CRs has classic Mac line ends. A CR inside a
	// file that does have LFs is left to the parsers (clean).
	if bytes.IndexByte(raw, '\n') < 0 && bytes.IndexByte(raw, '\r') >= 0 {
		raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))
	}
	return raw, nil
}

// Detect decodes raw to UTF-8 and names its format from the content. A file name
// or content type plays no part. It refuses an empty file, JSON, an unreadable
// encoding and a CSV with headers it does not know.
func Detect(raw []byte) (Format, []byte, error) {
	text, err := decodeText(raw)
	if err != nil {
		return "", nil, err
	}
	// Look at the first non-blank, non-comment line only; never split the file.
	for rest := string(text); rest != ""; {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		t := strings.ToLower(strings.TrimSpace(line))
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		switch {
		case t == "begin:vcard":
			return FormatVCard, text, nil
		case t == "version: 1" || t == "version:1" || strings.HasPrefix(t, "dn:"):
			return FormatLDIF, text, nil
		}
		if _, ok := csvDelimiter(string(text)); ok {
			return FormatCSV, text, nil
		}
		return "", nil, ErrFormat
	}
	return "", nil, ErrFormat
}
