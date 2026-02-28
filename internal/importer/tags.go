package importer

import (
	"bufio"
	"bytes"
	"strings"
	"unicode"

	"github.com/emersion/go-imap/v2"
)

// gmailSystemLabels are Gmail-internal labels that should not become tags.
// These are the standard IMAP folder names Gmail maps to, plus Gmail-specific
// system category labels.
var gmailSystemLabels = map[string]bool{
	"inbox":    true,
	"sent":     true,
	"drafts":   true,
	"trash":    true,
	"spam":     true,
	"junk":     true,
	"all mail": true,
	"starred":  true, // becomes \Flagged, not a tag
	"chats":    true,
	"category:primary":   true,
	"category:social":    true,
	"category:promotions": true,
	"category:updates":   true,
	"category:forums":    true,
}

// imapStandardFlags are the standard IMAP system flags that should not become tags.
var imapStandardFlags = map[imap.Flag]bool{
	imap.FlagSeen:     true,
	imap.FlagAnswered: true,
	imap.FlagFlagged:  true,
	imap.FlagDeleted:  true,
	imap.FlagDraft:    true,
	"\\Recent":        true,
}

// ExtractGmailLabels reads the X-Gmail-Labels header from raw RFC 5322 message
// bytes and returns normalised tag names. Gmail system labels (Inbox, Sent,
// Starred, etc.) and the "Important" label are excluded. "Starred" is excluded
// because it is represented as the \Flagged IMAP flag rather than a tag.
func ExtractGmailLabels(raw []byte) []string {
	header := extractHeaderValue(raw, "x-gmail-labels")
	if header == "" {
		return nil
	}

	var tags []string
	for _, label := range strings.Split(header, ",") {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		lower := strings.ToLower(label)
		// Filter system labels and the Important label.
		if gmailSystemLabels[lower] || lower == "important" {
			continue
		}
		tag := normaliseTagName(label)
		if tag != "" {
			tags = append(tags, tag)
		}
	}

	return deduplicateStrings(tags)
}

// ExtractIMAPKeywords extracts user-defined IMAP keywords from a flags list and
// returns them as normalised tag names. Standard system flags (\Seen, \Answered,
// \Flagged, \Deleted, \Draft, \Recent) are excluded. Keywords with a leading "$"
// (e.g. "$label1") are included after stripping the prefix.
func ExtractIMAPKeywords(flags []imap.Flag) []string {
	var tags []string
	for _, flag := range flags {
		if imapStandardFlags[flag] {
			continue
		}
		// Strip leading $ (Thunderbird-style: $label1, $label2, etc.)
		keyword := string(flag)
		keyword = strings.TrimPrefix(keyword, "$")
		keyword = strings.TrimPrefix(keyword, "\\") // skip any remaining system flags
		if keyword == "" {
			continue
		}
		tag := normaliseTagName(keyword)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return deduplicateStrings(tags)
}

// ExtractThunderbirdTags reads the X-Mozilla-Keys header from raw RFC 5322
// message bytes and returns normalised tag names. Thunderbird stores user tags
// as space-separated keywords (e.g. "$label1 $label2"). The "$label1" through
// "$label5" names are Thunderbird's built-in tag identifiers; when imported they
// become tags like "label1".
func ExtractThunderbirdTags(raw []byte) []string {
	header := extractHeaderValue(raw, "x-mozilla-keys")
	if header == "" {
		return nil
	}

	var tags []string
	for _, keyword := range strings.Fields(header) {
		// Strip leading $ (e.g. $label1 -> label1).
		keyword = strings.TrimPrefix(keyword, "$")
		if keyword == "" {
			continue
		}
		tag := normaliseTagName(keyword)
		if tag != "" {
			tags = append(tags, tag)
		}
	}

	return deduplicateStrings(tags)
}

// extractHeaderValue scans raw RFC 5322 headers for a named header (case-
// insensitive) and returns its value. Handles folded headers (continuation
// lines starting with whitespace). Returns empty string if not found.
func extractHeaderValue(raw []byte, lowerName string) string {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// Ensure the scanner can handle long header values.
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	prefix := lowerName + ":"
	var found bool
	var value strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// End of headers.
		if line == "" || line == "\r" {
			break
		}

		// Folded continuation of a found header.
		if found && len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			value.WriteString(" ")
			value.WriteString(strings.TrimSpace(line))
			continue
		}

		// New header line: stop accumulating the previous header.
		if found {
			break
		}

		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, prefix) {
			found = true
			value.WriteString(strings.TrimSpace(line[len(prefix):]))
		}
	}

	return value.String()
}

// normaliseTagName converts an arbitrary label string into a valid tag name:
//   - Lowercase
//   - Spaces and underscores replaced with hyphens
//   - Non-alphanumeric, non-hyphen characters stripped
//   - Leading/trailing hyphens trimmed
//   - Empty or invalid results return ""
func normaliseTagName(s string) string {
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

	if len(result) == 0 || len(result) > 50 {
		return ""
	}

	return result
}

// deduplicateStrings returns a new slice with duplicates removed, preserving
// order of first occurrence.
func deduplicateStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
