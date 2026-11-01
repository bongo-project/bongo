package imip

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/emersion/go-ical"

	"github.com/bongo-project/bongo/internal/icalevent"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Limits on a received calendar part. The part is untrusted input read on every
// view of the message.
const (
	// MaxPartBytes is the largest text/calendar part Parse reads.
	MaxPartBytes = 256 << 10
	// MaxEvents bounds the VEVENTs of one part: a master and its overrides.
	MaxEvents = 50
	// maxUIDLen bounds a UID.
	maxUIDLen = 255
)

// Why Parse refused a part. The caller treats every one of them the same way:
// the part is shown as an ordinary attachment.
var (
	ErrTooLarge       = errors.New("imip: calendar part too large")
	ErrMalformed      = errors.New("imip: malformed calendar part")
	ErrMethodMismatch = errors.New("imip: METHOD missing or different from the Content-Type")
	ErrNoEvent        = errors.New("imip: calendar part has no VEVENT")
	ErrMultipleUIDs   = errors.New("imip: calendar part describes more than one event")
	ErrOrganiser      = errors.New("imip: calendar part has no valid organiser")
)

// Invite is a parsed iTIP message. It is read-only input from the network: no
// field has been checked against any account.
type Invite struct {
	// Method is the upper-case METHOD of the VCALENDAR.
	Method string
	// UID, Sequence and Organizer are those of the master VEVENT, or of the
	// override when the part holds only overrides.
	UID       string
	Sequence  int
	Organizer string
	// RecurrenceID is the RECURRENCE-ID of the single instance the message is
	// about, as the expander writes it ("20300513T090000Z" or a date). It is
	// empty when the part carries a master VEVENT, which is the whole series.
	RecurrenceID string
	// Events are the VEVENTs of the part, in document order, and Zones its
	// VTIMEZONEs.
	Events []*ical.Component
	Zones  []*ical.Component
	// Event is the summary of the master VEVENT built with icalevent.ToEvent.
	// It has no identity or calendar; the caller sets those.
	Event *bongov1.Event
}

// Parse reads one text/calendar part. ctMethod is the method parameter of the
// part's Content-Type; RFC 6047 requires it to equal the calendar's METHOD, and
// "" skips the comparison (an .ics attachment has none).
//
// A part is refused when it is over MaxPartBytes (checked before decoding),
// does not decode, has no METHOD or one that differs from ctMethod, has no
// VEVENT or more than MaxEvents, has a VEVENT without UID or DTSTART, describes
// more than one UID, or has an ORGANIZER that is not a valid mailto address.
// Attendees beyond icalevent.MaxAttendees are cut, as they are everywhere.
func Parse(part []byte, ctMethod string) (*Invite, error) {
	if len(part) > MaxPartBytes {
		return nil, ErrTooLarge
	}
	cal, err := ical.NewDecoder(bytes.NewReader(part)).Decode()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}

	mp := cal.Props.Get(ical.PropMethod)
	if mp == nil {
		return nil, ErrMethodMismatch
	}
	method := strings.ToUpper(strings.TrimSpace(mp.Value))
	if method == "" || (ctMethod != "" && !strings.EqualFold(strings.TrimSpace(ctMethod), method)) {
		return nil, ErrMethodMismatch
	}

	inv := &Invite{Method: method}
	for _, c := range cal.Children {
		switch c.Name {
		case ical.CompEvent:
			if len(inv.Events) >= MaxEvents {
				return nil, fmt.Errorf("%w: more than %d events", ErrMalformed, MaxEvents)
			}
			inv.Events = append(inv.Events, c)
		case ical.CompTimezone:
			inv.Zones = append(inv.Zones, c)
		}
	}
	if len(inv.Events) == 0 {
		return nil, ErrNoEvent
	}
	for _, c := range inv.Events {
		uid, ok := cleanUID(c)
		if !ok {
			return nil, fmt.Errorf("%w: bad UID", ErrMalformed)
		}
		if c.Props.Get(ical.PropDateTimeStart) == nil {
			return nil, fmt.Errorf("%w: no DTSTART", ErrMalformed)
		}
		switch {
		case inv.UID == "":
			inv.UID = uid
		case inv.UID != uid:
			return nil, ErrMultipleUIDs
		}
	}

	event, fallback := icalevent.ToEvent(cal)
	if !icalevent.ValidAddress(event.Organizer) {
		return nil, ErrOrganiser
	}
	inv.Event = event
	inv.Organizer = event.Organizer
	inv.Sequence = int(event.Sequence)
	if fallback {
		master, _ := icalevent.Master(cal)
		if p := master.Props.Get(ical.PropRecurrenceID); p != nil {
			if key, ok := RecurrenceKey(p); ok {
				inv.RecurrenceID = key
			}
		}
	}
	return inv, nil
}

// cleanUID returns the UID of a VEVENT when it is present, bounded and free of
// control characters.
func cleanUID(c *ical.Component) (string, bool) {
	p := c.Props.Get(ical.PropUID)
	if p == nil {
		return "", false
	}
	uid := strings.TrimSpace(p.Value)
	if uid == "" || len(uid) > maxUIDLen {
		return "", false
	}
	for _, r := range uid {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return uid, true
}

// RecurrenceKey returns a RECURRENCE-ID property in the form the expander and
// BuildCalendar use: a UTC basic date-time or a date.
func RecurrenceKey(p *ical.Prop) (string, bool) {
	t, dateOnly, ok := icalevent.PropInstant(p, time.UTC)
	if !ok {
		return "", false
	}
	if dateOnly {
		return t.Format("20060102"), true
	}
	return t.UTC().Format("20060102T150405Z"), true
}

// SetPartstat sets PARTSTAT on the ATTENDEE of vevent whose address is me
// (case-insensitive) and reports whether there was one.
func SetPartstat(vevent *ical.Component, me, partstat string) bool {
	found := false
	props := vevent.Props[ical.PropAttendee]
	for i := range props {
		addr, ok := icalevent.AddressFromCalAddress(props[i].Value)
		if !ok || !strings.EqualFold(addr, me) {
			continue
		}
		props[i].Params.Set(ical.ParamParticipationStatus, partstat)
		// An answered invitation no longer asks for one.
		props[i].Params.Del(ical.ParamRSVP)
		found = true
	}
	return found
}

// Stored returns the iCalendar object to keep for the invitation: the received
// VEVENTs and VTIMEZONEs without METHOD (RFC 4791 forbids it on a stored
// object) and without the sender's VALARMs, with PARTSTAT of the attendee me
// set to partstat when that is not empty. It changes the components of inv.
func (inv *Invite) Stored(me, partstat string) ([]byte, error) {
	out := ical.NewCalendar()
	out.Props.SetText(ical.PropVersion, "2.0")
	out.Props.SetText(ical.PropProductID, prodID)
	out.Children = append(out.Children, inv.Zones...)
	for _, ve := range inv.Events {
		StripAlarms(ve)
		if partstat != "" {
			SetPartstat(ve, me, partstat)
		}
		out.Children = append(out.Children, ve)
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(out); err != nil {
		return nil, fmt.Errorf("imip: encoding stored invitation: %w", err)
	}
	return buf.Bytes(), nil
}

// StripAlarms removes the VALARMs of a VEVENT: an alarm in a received invitation
// is the sender's, not the recipient's.
func StripAlarms(vevent *ical.Component) {
	kept := vevent.Children[:0]
	for _, c := range vevent.Children {
		if c.Name != ical.CompAlarm {
			kept = append(kept, c)
		}
	}
	vevent.Children = kept
}

// DMARCResult returns the DMARC verdict ("pass", "fail", ...) recorded by the
// Authentication-Results header whose authserv-id equals authservID, or "" when
// there is no such header or it carries no dmarc result. Only the first header
// with that authserv-id is read, and headers from other servers are ignored:
// the delivery stage strips inbound ones and stamps its own, so a header with
// another authserv-id is foreign (an imported or sent message) and says nothing
// about this server's check. An empty authservID (no hostname configured, so no
// header was stamped) never matches.
func DMARCResult(headerValues []string, authservID string) string {
	authservID = strings.TrimSpace(authservID)
	if authservID == "" {
		return ""
	}
	for _, v := range headerValues {
		fields := strings.Split(v, ";")
		id := strings.Fields(fields[0])
		if len(id) == 0 || !strings.EqualFold(id[0], authservID) {
			continue
		}
		for _, f := range fields[1:] {
			f = strings.TrimSpace(f)
			if rest, ok := strings.CutPrefix(strings.ToLower(f), "dmarc="); ok {
				if verdict := strings.Fields(rest); len(verdict) > 0 {
					return verdict[0]
				}
			}
		}
		return ""
	}
	return ""
}
