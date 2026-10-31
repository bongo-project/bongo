package deliver

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	bongomail "github.com/bongo-project/bongo/internal/mail"
)

// maxOriginalSubjectRunes bounds how much of the original subject is echoed.
const maxOriginalSubjectRunes = 100

// maxMsgIDLen bounds a Message-ID accepted for In-Reply-To and References.
const maxMsgIDLen = 255

// subjectChunkRunes is how many runes go into one encoded word, which keeps
// every header line well inside RFC 5322's 998 octets.
const subjectChunkRunes = 10

// validMsgID reports whether s is a single, plain msg-id: <id-left@id-right>
// in printable ASCII with no whitespace, nested angle brackets, quotes,
// comments or domain literal. It is stricter than RFC 5322 on purpose: the
// value is copied from an attacker-controlled header into ours.
func validMsgID(s string) bool {
	if len(s) < 5 || len(s) > maxMsgIDLen || s[0] != '<' || s[len(s)-1] != '>' {
		return false
	}
	inner := s[1 : len(s)-1]
	if strings.Count(inner, "@") != 1 {
		return false
	}
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c <= 0x20 || c >= 0x7F || strings.IndexByte("<>()[],;:\\\"", c) >= 0 {
			return false
		}
	}
	left, right, _ := strings.Cut(inner, "@")
	return left != "" && right != ""
}

// vacationOriginalSubject decodes a received Subject for echoing: RFC 2047
// words decoded, control characters removed and the length cut by runes.
// The raw header bytes are never passed through.
func vacationOriginalSubject(raw string) string {
	dec, err := new(mime.WordDecoder).DecodeHeader(raw)
	if err != nil {
		dec = raw
	}
	if !utf8.ValidString(dec) {
		dec = strings.ToValidUTF8(dec, "")
	}
	dec = strings.TrimSpace(stripControl(dec))
	if utf8.RuneCountInString(dec) > maxOriginalSubjectRunes {
		dec = string([]rune(dec)[:maxOriginalSubjectRunes])
	}
	return dec
}

// stripControl removes C0 and C1 controls, DEL and the Unicode line and
// paragraph separators.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) || r == 0x2028 || r == 0x2029 {
			return -1
		}
		return r
	}, s)
}

// encodeSubject renders s as a Subject header value. Plain printable ASCII is
// kept as is; anything else becomes folded RFC 2047 B-encoded words, so the
// result contains no raw non-ASCII and no line over the limit.
func encodeSubject(s string) string {
	plain := true
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] >= 0x7F {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	runes := []rune(s)
	var words []string
	for len(runes) > 0 {
		n := min(subjectChunkRunes, len(runes))
		// Always an encoded word, even for an ASCII chunk: adjacent encoded
		// words join without the folding space, a plain word would not.
		words = append(words, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(string(runes[:n])))+"?=")
		runes = runes[n:]
	}
	return strings.Join(words, "\r\n ")
}

// vacationReplySubject is the configured subject, or "Auto: " plus the
// decoded, sanitised original, or "Auto-reply" when there is neither.
func vacationReplySubject(configured, original string) string {
	if s := strings.TrimSpace(bongomail.SanitizeHeaderValue(configured)); s != "" {
		return encodeSubject(s)
	}
	if o := vacationOriginalSubject(original); o != "" {
		return encodeSubject("Auto: " + o)
	}
	return "Auto-reply"
}

// crlf normalises line endings to CRLF.
func crlf(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// vacationFromAddress picks the reply's From: the address the message was sent
// to when the user may send as it, else their primary address.
func (p *DeliverProcessor) vacationFromAddress(c *vacationCandidate) (string, error) {
	v := p.vacation
	if a, err := mail.ParseAddress(strings.TrimSpace(c.rcpt)); err == nil && p.authorizer != nil {
		if ok, cerr := p.authorizer.CanSendAs(c.userID, a.Address); cerr == nil && ok {
			return a.Address, nil
		}
	}
	primary, err := v.Profiles.PrimaryEmail(c.userID)
	if err != nil {
		return "", fmt.Errorf("primary address: %w", err)
	}
	a, err := mail.ParseAddress(primary)
	if err != nil {
		return "", fmt.Errorf("primary address %q: %w", primary, err)
	}
	return a.Address, nil
}

// buildVacationReply renders the reply message. Every header is either fixed,
// generated here or validated; nothing is copied raw from the original except
// a Message-ID that passed validMsgID.
func (p *DeliverProcessor) buildVacationReply(c *vacationCandidate, now time.Time) ([]byte, error) {
	from, err := p.vacationFromAddress(c)
	if err != nil {
		return nil, err
	}
	at := strings.LastIndexByte(from, '@')
	if at <= 0 || at == len(from)-1 {
		return nil, errors.New("from address has no domain")
	}
	hdr := func(name, value string) string {
		return name + ": " + bongomail.SanitizeHeaderValue(value) + "\r\n"
	}

	var b bytes.Buffer
	b.WriteString(hdr("From", "<"+from+">"))
	b.WriteString(hdr("To", "<"+c.sender+">"))
	// The subject is already encoded and may hold folding CRLFs of its own.
	b.WriteString("Subject: " + vacationReplySubject(c.prefs.GetVacationSubject(), c.origSubject) + "\r\n")
	b.WriteString(hdr("Date", now.UTC().Format(time.RFC1123Z)))
	b.WriteString(hdr("Message-ID", "<"+uuid.New().String()+"@"+from[at+1:]+">"))
	if validMsgID(c.origMsgID) {
		b.WriteString(hdr("In-Reply-To", c.origMsgID))
		b.WriteString(hdr("References", c.origMsgID))
	}
	b.WriteString("Auto-Submitted: auto-replied\r\n")
	b.WriteString("Precedence: bulk\r\n")
	b.WriteString("X-Auto-Response-Suppress: All\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&b)
	body := crlf(c.prefs.GetVacationBody())
	if !strings.HasSuffix(body, "\r\n") {
		body += "\r\n"
	}
	if _, err := qp.Write([]byte(body)); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, fmt.Errorf("encode body: %w", err)
	}
	return b.Bytes(), nil
}
