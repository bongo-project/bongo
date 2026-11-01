package imip

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/emersion/go-ical"
	gomessage "github.com/emersion/go-message/mail"

	"github.com/bongo-project/bongo/internal/icalevent"
)

// attachmentName is the file name of the .ics attachment.
const attachmentName = "invite.ics"

// Params describes one iMIP message.
type Params struct {
	// Method must equal the METHOD of Cal; empty takes it from Cal.
	Method string
	// From is the bare sender address: the organiser.
	From string
	// To lists the bare recipient addresses.
	To []string
	// Subject and TextBody are the human-readable parts, already translated.
	Subject  string
	TextBody string
	Cal      *ical.Calendar
	// MessageID may carry angle brackets or not; empty generates one in the
	// From domain.
	MessageID string
	// Date is the Date header; zero means now.
	Date time.Time
}

// BuildMessage returns the RFC 5322 message for p: multipart/mixed holding a
// multipart/alternative (a text part and a text/calendar part whose method
// parameter equals the calendar's METHOD) and the same calendar as an .ics
// attachment, which is what clients that ignore the inline part offer.
//
// Header values are encoded by go-message, and control characters are removed
// from the subject first, so a title cannot add a header.
func BuildMessage(p Params) ([]byte, error) {
	if p.Cal == nil {
		return nil, errors.New("imip: no calendar")
	}
	calMethod := ""
	if prop := p.Cal.Props.Get(ical.PropMethod); prop != nil {
		calMethod = prop.Value
	}
	method := p.Method
	if method == "" {
		method = calMethod
	}
	if method == "" || method != calMethod {
		return nil, fmt.Errorf("imip: message method %q does not match calendar method %q", method, calMethod)
	}
	if !icalevent.ValidAddress(p.From) {
		return nil, errors.New("imip: invalid sender address")
	}
	if len(p.To) == 0 {
		return nil, errors.New("imip: no recipients")
	}
	to := make([]*gomessage.Address, 0, len(p.To))
	for _, a := range p.To {
		if !icalevent.ValidAddress(a) {
			return nil, errors.New("imip: invalid recipient address")
		}
		to = append(to, &gomessage.Address{Address: a})
	}

	var ics bytes.Buffer
	if err := ical.NewEncoder(&ics).Encode(p.Cal); err != nil {
		return nil, fmt.Errorf("imip: encoding calendar: %w", err)
	}

	id := strings.Trim(strings.TrimSpace(p.MessageID), "<>")
	if id == "" {
		var b [12]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("imip: message id: %w", err)
		}
		id = hex.EncodeToString(b[:]) + "@" + addressDomain(p.From)
	}
	if _, err := mail.ParseAddress("<" + id + ">"); err != nil {
		return nil, errors.New("imip: invalid message id")
	}
	date := p.Date
	if date.IsZero() {
		date = time.Now()
	}

	var h gomessage.Header
	h.Set("MIME-Version", "1.0")
	h.SetDate(date)
	h.SetMessageID(id)
	h.SetSubject(cleanHeader(p.Subject))
	h.SetAddressList("From", []*gomessage.Address{{Address: p.From}})
	h.SetAddressList("To", to)

	var out bytes.Buffer
	mw, err := gomessage.CreateWriter(&out, h)
	if err != nil {
		return nil, fmt.Errorf("imip: writing message: %w", err)
	}
	alt, err := mw.CreateInline()
	if err != nil {
		return nil, fmt.Errorf("imip: writing message: %w", err)
	}
	if err := writeInline(alt, "text/plain", map[string]string{"charset": "UTF-8"}, []byte(p.TextBody)); err != nil {
		return nil, err
	}
	if err := writeInline(alt, "text/calendar", map[string]string{"charset": "UTF-8", "method": method}, ics.Bytes()); err != nil {
		return nil, err
	}
	if err := alt.Close(); err != nil {
		return nil, fmt.Errorf("imip: writing message: %w", err)
	}

	var ah gomessage.AttachmentHeader
	ah.SetContentType("application/ics", map[string]string{"name": attachmentName})
	ah.SetFilename(attachmentName)
	att, err := mw.CreateAttachment(ah)
	if err != nil {
		return nil, fmt.Errorf("imip: writing message: %w", err)
	}
	if _, err := att.Write(ics.Bytes()); err != nil {
		return nil, fmt.Errorf("imip: writing message: %w", err)
	}
	if err := att.Close(); err != nil {
		return nil, fmt.Errorf("imip: writing message: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("imip: writing message: %w", err)
	}
	return out.Bytes(), nil
}

func writeInline(alt *gomessage.InlineWriter, mediaType string, params map[string]string, body []byte) error {
	var ih gomessage.InlineHeader
	ih.SetContentType(mediaType, params)
	w, err := alt.CreatePart(ih)
	if err != nil {
		return fmt.Errorf("imip: writing message: %w", err)
	}
	if err := writeAll(w, body); err != nil {
		return fmt.Errorf("imip: writing message: %w", err)
	}
	return nil
}

func writeAll(w io.WriteCloser, body []byte) error {
	if _, err := w.Write(body); err != nil {
		return err
	}
	return w.Close()
}

// cleanHeader replaces control characters, CR and LF included, with a space.
func cleanHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func addressDomain(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return "localhost"
}
