package store

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Header-based mailbox views. They are not folders: each is a filter over the
// derived SQLite index whose results are shown with their real mailbox.
const (
	ViewToMe         = "to-me"
	ViewFromContacts = "from-contacts"
	ViewLists        = "lists"
	// ViewListPrefix starts the name of a single list's view, "lists/<id>".
	ViewListPrefix = "lists/"
)

const (
	maxListIDLen   = 255
	maxListNameLen = 200
)

// MailHeaderValues are the header-derived index columns of one message.
type MailHeaderValues struct {
	// ToMe is true when one of the user's addresses is in To or Cc.
	ToMe bool
	// SenderAddr is the normalised bare sender address ("" when none parses).
	SenderAddr string
	// ListID is the lower-cased list identifier, "" when absent or invalid.
	ListID string
	// ListName is the list's display name from the List-Id phrase.
	ListName string
	// Complete records that the values came from a MailMeta carrying every
	// header the views read (Cc and List-Id included), so the header backfill
	// need not revisit the row. A rebuild leaves it false.
	Complete bool
}

// MaxStoredHeaderBytes bounds the Reply-To, List-Id and List-Post values kept
// on MailMeta, which every replica carries in its Raft log and Pebble.
const MaxStoredHeaderBytes = 998

// StoredHeader prepares a reply or list header for MailMeta: trimmed, with
// CR and LF removed. A value over MaxStoredHeaderBytes is dropped rather
// than cut, because a truncated address list or URI could name a different
// recipient; readers fall back to the blob. The value is untrusted and is
// validated again where it is used.
func StoredHeader(v string) string {
	v = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(v))
	if len(v) > MaxStoredHeaderBytes {
		return ""
	}
	return v
}

// HeaderGetter is the part of a parsed message the header extraction needs.
type HeaderGetter interface {
	GetHeader(name string) string
}

// SplitAddressHeader splits a To or Cc header value on commas, trimming each
// entry and dropping empty ones. The split is naive: a quoted display name
// holding a comma yields fragments, which BareAddress copes with.
func SplitAddressHeader(v string) []string {
	var out []string
	for _, addr := range strings.Split(v, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}

// MailAddressHeaders extracts the To, Cc and List-Id values every writer of
// MailMeta stores (delivery, IMAP APPEND, web import and the header backfill),
// so they cannot disagree about what a message's headers say.
func MailAddressHeaders(h HeaderGetter) (to, cc []string, listID string) {
	return SplitAddressHeader(h.GetHeader("To")),
		SplitAddressHeader(h.GetHeader("Cc")),
		StoredHeader(h.GetHeader("List-Id"))
}

// MailHeaderFields derives the header columns from a message's metadata.
// mine holds the user's own normalised addresses.
//
// Aliases and identities count as the user's addresses when the caller puts
// them in mine; a catch-all delivery does not, because the address it was sent
// to is not one the user owns.
func MailHeaderFields(meta *bongov1.MailMeta, mine map[string]bool) MailHeaderValues {
	var v MailHeaderValues
	v.SenderAddr = BareAddress(meta.GetFrom())
	for _, list := range [][]string{meta.GetTo(), meta.GetCc()} {
		for _, entry := range list {
			if a := BareAddress(entry); a != "" && mine[a] {
				v.ToMe = true
			}
		}
	}
	v.ListID, v.ListName = ParseListID(meta.GetListId())
	return v
}

// BareAddress returns the normalised address inside a header value such as
// `Name <a@b.example>`. It prefers net/mail and falls back to the text between
// angle brackets, which copes with fragments of a naively comma-split header.
// A value with no "@" yields "".
func BareAddress(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if a, err := mail.ParseAddress(s); err == nil {
		return NormaliseAddress(a.Address)
	}
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 1 {
			s = s[i+1 : i+j]
		}
	}
	s = NormaliseAddress(s)
	if !strings.Contains(s, "@") || strings.ContainsAny(s, " \t\r\n<>\"") {
		return ""
	}
	return s
}

// ParseListID splits a List-Id header into a normalised id and a display name.
// The id is the lower-cased text in angle brackets, or the whole value when it
// is a single bare token. An id outside the safe character set is dropped:
// List-Id is attacker-controlled and the id reaches URLs.
func ParseListID(raw string) (id, name string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if i := strings.LastIndex(raw, "<"); i >= 0 {
		j := strings.Index(raw[i:], ">")
		if j < 0 {
			return "", ""
		}
		// Nothing but whitespace may follow the closing bracket.
		if strings.TrimSpace(raw[i+j+1:]) != "" {
			return "", ""
		}
		id = raw[i+1 : i+j]
		name = cleanListName(raw[:i])
	} else {
		id = raw
	}
	id = strings.ToLower(strings.TrimSpace(id))
	if !ValidListID(id) {
		return "", ""
	}
	return id, name
}

func cleanListName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > maxListNameLen {
		s = string([]rune(s)[:maxListNameLen])
	}
	return s
}

// ValidListID reports whether id is a normalised list identifier safe to store
// and to place in a URL path: lower-case letters, digits and . _ + -, with no
// leading or trailing dot.
func ValidListID(id string) bool {
	if id == "" || len(id) > maxListIDLen {
		return false
	}
	if id[0] == '.' || id[len(id)-1] == '.' {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '+', c == '-':
		default:
			return false
		}
	}
	return true
}

// HeaderView names a header-based view.
type HeaderView struct {
	Kind   string // ViewToMe, ViewFromContacts or ViewLists
	ListID string // set for a single list's view
}

// ParseHeaderView recognises the names of the header-based views, including
// "lists/<id>". An invalid list id is not a view.
func ParseHeaderView(name string) (HeaderView, bool) {
	switch name {
	case ViewToMe, ViewFromContacts, ViewLists:
		return HeaderView{Kind: name}, true
	}
	if id, ok := strings.CutPrefix(name, ViewListPrefix); ok && ValidListID(id) {
		return HeaderView{Kind: ViewLists, ListID: id}, true
	}
	return HeaderView{}, false
}

// IsHeaderView reports whether name is one of the header-based views.
func IsHeaderView(name string) bool {
	_, ok := ParseHeaderView(name)
	return ok
}
