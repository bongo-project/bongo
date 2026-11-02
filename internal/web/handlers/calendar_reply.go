package handlers

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// replyLocks serialises the read-modify-write of one event by replies that
// arrive together (two attendees answering at once). EventStore has no
// compare-and-swap, so on one node this is what stops a lost update; a reply
// racing a web or CalDAV edit stays last-writer-wins, as everywhere else.
type replyLocks struct {
	mu sync.Mutex
	m  map[string]*replyLock
}

type replyLock struct {
	mu   sync.Mutex
	refs int
}

var eventReplyLocks = &replyLocks{m: map[string]*replyLock{}}

// lock takes the lock of key and returns the function that releases it.
func (l *replyLocks) lock(key string) func() {
	l.mu.Lock()
	e := l.m[key]
	if e == nil {
		e = &replyLock{}
		l.m[key] = e
	}
	e.refs++
	l.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		l.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(l.m, key)
		}
		l.mu.Unlock()
	}
}

// ApplyReply applies an iTIP REPLY that was delivered to userID. The delivery
// stage decides trust: verifiedSender is the From address it could vouch for
// (DMARC pass, or an authenticated local author), and "" means nothing is
// applied. Everything here is best-effort: the mail is already in the INBOX, so
// every refusal and failure just returns, and a failed write is logged.
//
// Only the one ATTENDEE whose address is verifiedSender is read from the
// message, and only its PARTSTAT, from a whitelist. The organiser's event is
// found in the user's own keys and must be one they organise; nothing else of
// the received object is copied.
func (h *CalendarHandler) ApplyReply(userID, verifiedSender string, part []byte, ctMethod string) {
	sender := store.NormaliseAddress(verifiedSender)
	if h == nil || h.events == nil || sender == "" {
		return
	}
	inv, err := imip.Parse(part, ctMethod)
	if err != nil || inv.Method != imip.MethodReply {
		return
	}
	partstat, ok := repliedPartstat(inv, sender)
	if !ok {
		return
	}

	defer eventReplyLocks.lock(userID + "|" + inv.UID)()

	known, err := h.findKnown(userID, inv.UID)
	if err != nil {
		slog.Warn("calendar: looking up the event of a reply failed", "user", userID, "error", err)
		return
	}
	if known == nil || !strings.EqualFold(known.Organizer, inv.Organizer) ||
		!h.myAddresses(userID, "")[store.NormaliseAddress(known.Organizer)] {
		return
	}
	seriesLevel := inv.RecurrenceID == "" || isInstanceOnly(known) && sameInstance(known, inv.RecurrenceID)
	var ev *bongov1.Event
	if seriesLevel {
		if inv.Sequence > int(known.Sequence) || !hasAttendee(known, sender) {
			return
		}
		ev, err = replySeries(known, sender, partstat)
	} else {
		ev, err = replyInstance(known, inv, sender, partstat, h.nowIn(userID))
	}
	if err != nil {
		slog.Warn("calendar: applying a reply failed", "user", userID, "guid", known.Guid, "error", err)
		return
	}
	if ev == nil {
		return
	}
	if res := h.storeInvited(userID, known, ev); res.Status != 0 {
		slog.Warn("calendar: a reply could not be stored", "user", userID, "guid", known.Guid, "status", res.Status)
	}
}

// repliedPartstat returns the PARTSTAT that sender, an ATTENDEE of the VEVENT
// the REPLY is about, gives; false when there is no such attendee or the value
// is not an answer.
func repliedPartstat(inv *imip.Invite, sender string) (string, bool) {
	for _, ve := range inv.Events {
		key := ""
		if p := ve.Props.Get(ical.PropRecurrenceID); p != nil {
			key, _ = imip.RecurrenceKey(p)
		}
		if key != inv.RecurrenceID {
			continue
		}
		for _, p := range ve.Props.Values(ical.PropAttendee) {
			addr, ok := icalevent.AddressFromCalAddress(p.Value)
			if !ok || store.NormaliseAddress(addr) != sender {
				continue
			}
			switch ps := strings.ToUpper(strings.TrimSpace(p.Params.Get(ical.ParamParticipationStatus))); ps {
			case "ACCEPTED", "TENTATIVE", "DECLINED":
				return ps, true
			}
		}
	}
	return "", false
}

// hasAttendee reports whether sender is an attendee of e.
func hasAttendee(e *bongov1.Event, sender string) bool {
	for _, a := range e.GetAttendees() {
		if store.NormaliseAddress(a.Address) == sender {
			return true
		}
	}
	return false
}

// replySeries sets the attendee's answer on the series: the master VEVENT in
// ical_raw, when the event has one, and the structured attendees.
func replySeries(known *bongov1.Event, sender, partstat string) (*bongov1.Event, error) {
	ev := proto.Clone(known).(*bongov1.Event)
	if len(known.IcalRaw) > 0 {
		cal, err := storedCalendar(known)
		if err != nil {
			return nil, err
		}
		if master, _ := icalevent.Master(cal); master != nil {
			imip.SetPartstat(master, sender, partstat)
		}
		if ev.IcalRaw, err = encodeCalendar(cal); err != nil {
			return nil, err
		}
	}
	setAttendeePartstat(ev, map[string]bool{sender: true}, partstat)
	return ev, nil
}

// maxReplyOverrides caps the override components (VEVENTs with a
// RECURRENCE-ID) of one event for which a REPLY may create a new one. A REPLY
// is unauthenticated apart from its sender, so without a cap one verified
// attendee could grow the organiser's ical_raw by one override per message on
// an unbounded series. Beyond it the mail stays ordinary mail; a REPLY to an
// override that already exists still applies.
const maxReplyOverrides = 100

// overrideCount returns the number of override VEVENTs in the series.
func (s *seriesCal) overrideCount() int {
	n := 0
	for _, c := range s.cal.Children {
		if c.Name == ical.CompEvent && c.Props.Get(ical.PropRecurrenceID) != nil {
			n++
		}
	}
	return n
}

// replyInstance sets the attendee's answer on one occurrence: the PARTSTAT of
// the existing override, or of a new override cloned from the occurrence when
// there is none. The structured attendees describe the series and stay. The
// SEQUENCE of a new override is the series', so later replies still match.
// A nil event means there is nothing to change.
func replyInstance(known *bongov1.Event, inv *imip.Invite, sender, partstat string, now time.Time) (*bongov1.Event, error) {
	_, slot, err := resolveSlot(known, inv.RecurrenceID)
	if err != nil {
		return nil, err
	}
	s, err := seriesForEdit(known)
	if err != nil {
		return nil, err
	}
	i, ov := s.overrideAt(slot)
	if ov != nil {
		seq := 0
		if p := ov.Props.Get(ical.PropSequence); p != nil {
			seq, _ = strconv.Atoi(strings.TrimSpace(p.Value))
		}
		if inv.Sequence > seq || !imip.SetPartstat(ov, sender, partstat) {
			return nil, nil
		}
		s.cal.Children[i] = ov
	} else {
		if inv.Sequence > int(known.Sequence) || !hasAttendee(known, sender) {
			return nil, nil
		}
		if s.overrideCount() >= maxReplyOverrides {
			slog.Info("calendar: a reply would exceed the override limit, left as mail", "guid", known.Guid, "limit", maxReplyOverrides)
			return nil, nil
		}
		occ, found := store.FindOccurrence(known, inv.RecurrenceID)
		if !found {
			return nil, nil
		}
		ov = newOverride(s, nil, occ, slot, int(known.Sequence), now)
		if !imip.SetPartstat(ov, sender, partstat) {
			return nil, nil
		}
		s.cal.Children = append(s.cal.Children, ov)
	}
	ev := proto.Clone(known).(*bongov1.Event)
	if ev.IcalRaw, err = s.encode(); err != nil {
		return nil, err
	}
	return ev, nil
}
