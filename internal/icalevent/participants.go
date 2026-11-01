package icalevent

import (
	"net/mail"
	"strconv"
	"strings"
	"unicode"

	"github.com/emersion/go-ical"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// MaxAttendees bounds the structured attendee list of an Event. The limit
// applies to that copy only: ical_raw keeps every ATTENDEE a CalDAV client
// sent, so a 150-person meeting is neither refused nor damaged.
const MaxAttendees = 100

// maxNameRunes bounds a stored attendee display name.
const maxNameRunes = 200

// Defaults for an attendee added from the web form.
const (
	RoleRequired        = "REQ-PARTICIPANT"
	PartStatNeedsAction = "NEEDS-ACTION"
)

// ValidAddress reports whether s is a single bare mail address. It is the
// check the admin forms use (net/mail, exact match) plus a refusal of control
// characters and double quotes, which would break the iCalendar line they are
// written into.
func ValidAddress(s string) bool {
	if s == "" || len(s) > 254 || !safeText(s) || strings.ContainsRune(s, '"') {
		return false
	}
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s && strings.Contains(s, "@")
}

// ValidName reports whether s can be written as a CN parameter.
func ValidName(s string) bool {
	return safeText(s) && !strings.ContainsRune(s, '"')
}

// safeText is false when s holds a control character (CR and LF included).
func safeText(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// cleanName drops what ValidName refuses and bounds the length.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxNameRunes {
		s = string(r[:maxNameRunes])
	}
	return s
}

// token upper-cases an iCalendar enumerated value and returns "" for anything
// that is not made of letters, digits and hyphens.
func token(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return ""
		}
	}
	return s
}

// AddressFromCalAddress returns the bare address of a CAL-ADDRESS value. Only
// mailto: URIs (any case) with a valid address qualify.
func AddressFromCalAddress(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if len(v) < 7 || !strings.EqualFold(v[:7], "mailto:") {
		return "", false
	}
	addr := v[7:]
	if !ValidAddress(addr) {
		return "", false
	}
	return addr, true
}

// attendeeFromProp reads one ATTENDEE property. ok is false when it is not a
// usable mailto address.
func attendeeFromProp(p *ical.Prop) (*bongov1.Attendee, bool) {
	addr, ok := AddressFromCalAddress(p.Value)
	if !ok {
		return nil, false
	}
	return &bongov1.Attendee{
		Address:  addr,
		Name:     cleanName(p.Params.Get(ical.ParamCommonName)),
		Role:     token(p.Params.Get(ical.ParamRole)),
		Partstat: token(p.Params.Get(ical.ParamParticipationStatus)),
		Rsvp:     strings.EqualFold(strings.TrimSpace(p.Params.Get(ical.ParamRSVP)), "TRUE"),
	}, true
}

var validStatus = map[string]bool{"TENTATIVE": true, "CONFIRMED": true, "CANCELLED": true}

// fillParticipants copies ORGANIZER, ATTENDEE, SEQUENCE and STATUS of a VEVENT
// into event. Attendees that are not mailto addresses or fail validation are
// skipped, duplicates (case-insensitive) keep the first, and the structured
// list stops at MaxAttendees.
func fillParticipants(event *bongov1.Event, props ical.Props) {
	if p := props.Get(ical.PropOrganizer); p != nil {
		if addr, ok := AddressFromCalAddress(p.Value); ok {
			event.Organizer = addr
		}
	}
	seen := map[string]bool{}
	for _, p := range props.Values(ical.PropAttendee) {
		if len(event.Attendees) >= MaxAttendees {
			break
		}
		a, ok := attendeeFromProp(&p)
		if !ok {
			continue
		}
		key := strings.ToLower(a.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		event.Attendees = append(event.Attendees, a)
	}
	if p := props.Get(ical.PropSequence); p != nil {
		if n, err := strconv.Atoi(strings.TrimSpace(p.Value)); err == nil && n > 0 {
			event.Sequence = int32(min(n, 1<<30))
		}
	}
	if p := props.Get(ical.PropStatus); p != nil {
		if s := token(p.Value); validStatus[s] {
			event.Status = s
		}
	}
}

// AttendeeProp builds the ATTENDEE property for a.
func AttendeeProp(a *bongov1.Attendee) *ical.Prop {
	p := ical.NewProp(ical.PropAttendee)
	p.Value = "mailto:" + a.Address
	if n := cleanName(a.Name); n != "" {
		p.Params.Set(ical.ParamCommonName, n)
	}
	if r := token(a.Role); r != "" {
		p.Params.Set(ical.ParamRole, r)
	}
	if s := token(a.Partstat); s != "" {
		p.Params.Set(ical.ParamParticipationStatus, s)
	}
	if a.Rsvp {
		p.Params.Set(ical.ParamRSVP, "TRUE")
	}
	return p
}

// OrganizerProp builds the ORGANIZER property for an address.
func OrganizerProp(addr string) *ical.Prop {
	p := ical.NewProp(ical.PropOrganizer)
	p.Value = "mailto:" + addr
	return p
}

// SetParticipants writes ORGANIZER, ATTENDEE, SEQUENCE and STATUS of e onto
// props, each only when it has a value. It is for a VEVENT synthesised from
// the structured fields; a stored ical_raw already carries these. Invalid
// addresses are skipped, so nothing unvalidated reaches the output.
func SetParticipants(props ical.Props, e *bongov1.Event) {
	if ValidAddress(e.Organizer) {
		props.Set(OrganizerProp(e.Organizer))
	}
	props.Del(ical.PropAttendee)
	for i, a := range e.Attendees {
		if i >= MaxAttendees {
			break
		}
		if ValidAddress(a.Address) {
			props.Add(AttendeeProp(a))
		}
	}
	if e.Sequence > 0 {
		sp := ical.NewProp(ical.PropSequence)
		sp.Value = strconv.Itoa(int(e.Sequence))
		props.Set(sp)
	}
	if s := token(e.Status); validStatus[s] {
		sp := ical.NewProp(ical.PropStatus)
		sp.Value = s
		props.Set(sp)
	}
}
