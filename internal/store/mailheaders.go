package store

import (
	"net/mail"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jhillyerd/enmime"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Header-based mailbox views. They are not folders: each is a filter over the
// derived SQLite index whose results are shown with their real mailbox.
//
// A view is identified by a HeaderView value, never by a mailbox name: folder
// names are not reserved, so no string can be told apart from a folder.

// HeaderViewKind says which header-based view a HeaderView is.
type HeaderViewKind int

const (
	ViewToMe HeaderViewKind = iota + 1
	ViewFromContacts
	ViewLists
	// ViewParticipant lists the conversations with one or more addresses.
	ViewParticipant
)

// ViewPathPrefix is the URL prefix of the header views.
const ViewPathPrefix = "/mail/view/"

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
	// Participants are the distinct From, To and Cc addresses of the message,
	// at most MaxParticipantsPerMessage, in the order From, To, Cc.
	Participants []Participant
}

// Participant roles, OR-ed together when one address appears in several headers.
const (
	RoleFrom = 1 << iota
	RoleTo
	RoleCc
)

// Participant is one address of a message with the headers it appeared in.
type Participant struct {
	Addr  string
	Roles int
}

const (
	// MaxParticipantsPerMessage bounds the participant rows of one message.
	// It is above the 100-recipient send cap, so a user's own mail is never cut.
	MaxParticipantsPerMessage = 200
	// MaxParticipantQuery bounds the addresses of one participant search.
	MaxParticipantQuery = 20
	// maxParticipantAddrLen drops an address too long to be real.
	maxParticipantAddrLen = 254
	// HeadersRevCurrent is the headers_rev a complete derivation writes.
	// Revision 2 added the participants table.
	HeadersRevCurrent = 2
)

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

// SplitAddressHeader splits an address header value (To, Cc and the like) into
// one entry per address. A comma inside a quoted display name ("Doe, Jane"
// <j@x>) does not separate addresses. Lists that parse as RFC 5322 and hold no
// such comma, lists that do not parse, and groups (undisclosed-recipients:;)
// come back exactly as the plain comma split returns them, trimmed and without
// empty entries.
func SplitAddressHeader(v string) []string {
	parsed, err := mail.ParseAddressList(v)
	return splitParsed(v, parsed, err)
}

// EnvelopeAddresses splits the named address header of a parsed message. The
// list is parsed from the raw header, so an RFC 2047 word whose decoded name
// holds a comma (=?UTF-8?Q?M=C3=BCller=2C_J=C3=B6rg?= <j@x>) is one address,
// not two. The names are decoded once, here.
func EnvelopeAddresses(env *enmime.Envelope, name string) []string {
	if env == nil {
		return nil
	}
	parsed, err := env.AddressList(name)
	// A decoded word can carry CR or LF (=0D=0A); stored entries are later
	// copied into outgoing headers, so no line break may survive.
	v := strings.NewReplacer("\r", " ", "\n", " ").Replace(env.GetHeader(name))
	return splitParsed(v, parsed, err)
}

// splitParsed splits the decoded header value v. parsed and err are the result
// of parsing the same list; when the parse found more addresses than there are
// comma pieces, a name holds a comma and the list is rebuilt from the parse.
func splitParsed(v string, parsed []*mail.Address, err error) []string {
	var out []string
	for _, addr := range strings.Split(v, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			out = append(out, addr)
		}
	}
	if err != nil || len(parsed) == 0 || len(parsed) == len(out) {
		return out
	}
	rebuilt := make([]string, 0, len(parsed))
	for _, a := range parsed {
		rebuilt = append(rebuilt, formatAddress(a))
	}
	return rebuilt
}

// formatAddress renders one parsed address. A non-ASCII name is quoted as is:
// mail.Address.String would turn it into an RFC 2047 word, and the header value
// reaching here has already been decoded.
func formatAddress(a *mail.Address) string {
	switch {
	case a.Name == "":
		return a.Address
	case isASCII(a.Name):
		return a.String()
	}
	// Control characters (an encoded CR or LF) must not reach stored headers
	// that a reply later copies into outgoing mail.
	name := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "", "\x00", "").Replace(a.Name)
	return `"` + name + `" <` + a.Address + `>`
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// MailAddressHeaders extracts the To, Cc and List-Id values every writer of
// MailMeta stores (delivery, IMAP APPEND, web import and the header backfill),
// so they cannot disagree about what a message's headers say.
func MailAddressHeaders(env *enmime.Envelope) (to, cc []string, listID string) {
	return EnvelopeAddresses(env, "To"),
		EnvelopeAddresses(env, "Cc"),
		StoredHeader(env.GetHeader("List-Id"))
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
	v.Participants = messageParticipants(meta)
	return v
}

// messageParticipants lists the distinct addresses of From, To and Cc, in that
// order, one entry per address with its roles OR-ed. Reply-To is left out: it
// is as forgeable as From and the participant search is not about replies.
// Entries that hold no address (a group such as undisclosed-recipients:;) and
// addresses too long to be real are dropped, and the list stops at
// MaxParticipantsPerMessage so a message with a huge header cannot flood the table.
func messageParticipants(meta *bongov1.MailMeta) []Participant {
	var out []Participant
	at := make(map[string]int)
	add := func(entry string, role int) {
		a := BareAddress(entry)
		if a == "" || len(a) > maxParticipantAddrLen {
			return
		}
		if i, ok := at[a]; ok {
			out[i].Roles |= role
			return
		}
		if len(out) >= MaxParticipantsPerMessage {
			return
		}
		at[a] = len(out)
		out = append(out, Participant{Addr: a, Roles: role})
	}
	add(meta.GetFrom(), RoleFrom)
	for _, e := range meta.GetTo() {
		add(e, RoleTo)
	}
	for _, e := range meta.GetCc() {
		add(e, RoleCc)
	}
	return out
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
	Kind   HeaderViewKind
	ListID string // set for a single list's view; must satisfy ValidListID
	// Participants are the normalised, de-duplicated, sorted addresses of a
	// participant view.
	Participants []string
}

// URL is the path of the view. The list id is escaped. A participant view is
// /search with one escaped participant parameter per address ("a+b@x" is
// "a%2Bb%40x"), so the URL is also what pagination and refresh come back to.
func (v HeaderView) URL() string {
	switch {
	case v.Kind == ViewParticipant:
		return "/search?" + v.participantQuery()
	case v.Kind == ViewToMe:
		return ViewPathPrefix + "to-me"
	case v.Kind == ViewFromContacts:
		return ViewPathPrefix + "from-contacts"
	case v.ListID != "":
		return ViewPathPrefix + "lists/" + url.PathEscape(v.ListID)
	}
	return ViewPathPrefix + "lists"
}

// BulkURL is the path bulk actions on the view's messages are posted to.
func (v HeaderView) BulkURL() string {
	if v.Kind == ViewParticipant {
		return "/search/bulk?" + v.participantQuery()
	}
	return v.URL() + "/bulk"
}

func (v HeaderView) participantQuery() string {
	return url.Values{"participant": v.Participants}.Encode()
}

// Excludes reports whether the view skips a mailbox. The To me, From my
// contacts and Mailing lists views skip Junk, Trash, Sent and Drafts; a
// participant view skips only Junk and Trash (and what is below them), since
// the mail a user sent to someone is part of the conversation with them.
func (v HeaderView) Excludes(mailbox string) bool {
	if v.Kind == ViewParticipant {
		return VirtualExcludes(mailbox)
	}
	return HeaderViewExcludes(mailbox)
}
