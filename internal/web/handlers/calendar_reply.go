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
	replies := repliedPartstats(inv, sender)
	if len(replies) == 0 {
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
	ev, err := applyReplyEvents(userID, known, replies, sender, h.nowIn(userID))
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

// vevent answer is what the verified sender says in one VEVENT of a REPLY.
type veventAnswer struct {
	key      string // "" for the master, else the canonical RECURRENCE-ID
	seq      int    // the VEVENT's own SEQUENCE, not the master's
	partstat string
}

// repliedPartstats returns, per VEVENT of the REPLY, the PARTSTAT that sender
// gives as an ATTENDEE of it. A VEVENT without the sender, with a value that is
// not an answer, or with a RECURRENCE-ID that does not parse is skipped. When
// two VEVENTs carry the same key the first that answers wins.
func repliedPartstats(inv *imip.Invite, sender string) []veventAnswer {
	var out []veventAnswer
	seen := map[string]bool{}
	for _, ve := range inv.Events {
		key := ""
		if p := ve.Props.Get(ical.PropRecurrenceID); p != nil {
			var ok bool
			if key, ok = imip.RecurrenceKey(p); !ok {
				continue
			}
		}
		if seen[key] {
			continue
		}
		// A key is taken only by a VEVENT that answers, so an unusable one
		// does not hide a later answer for the same key.
	attendees:
		for _, p := range ve.Props.Values(ical.PropAttendee) {
			addr, ok := icalevent.AddressFromCalAddress(p.Value)
			if !ok || store.NormaliseAddress(addr) != sender {
				continue
			}
			switch ps := strings.ToUpper(strings.TrimSpace(p.Params.Get(ical.ParamParticipationStatus))); ps {
			case "ACCEPTED", "TENTATIVE", "DECLINED":
				out = append(out, veventAnswer{key: key, seq: componentSequence(ve), partstat: ps})
				seen[key] = true
				break attendees
			}
		}
	}
	return out
}

// componentSequence returns the SEQUENCE of a VEVENT, 0 when absent.
func componentSequence(c *ical.Component) int {
	seq := 0
	if p := c.Props.Get(ical.PropSequence); p != nil {
		seq, _ = strconv.Atoi(strings.TrimSpace(p.Value))
	}
	return seq
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

// applyReplyEvents applies every answer of a REPLY to one copy of known, so
// the caller makes a single store write. Each VEVENT stands alone: one that is
// refused (newer SEQUENCE, sender not an attendee of it, not an occurrence,
// override limit reached) is skipped and the rest still apply. The structured
// attendees change only when the master VEVENT applied; ical_raw is rewritten
// only when an override changed or the event already had one. A nil event means
// there is nothing to change.
func applyReplyEvents(userID string, known *bongov1.Event, replies []veventAnswer, sender string, now time.Time) (*bongov1.Event, error) {
	if isInstanceOnly(known) {
		return replyInstanceOnly(known, replies, sender)
	}
	s, err := seriesForEdit(known)
	if err != nil {
		return nil, err
	}
	var masterChanged, overridesChanged bool
	for _, r := range replies {
		if r.key == "" {
			if r.seq > int(known.Sequence) || !hasAttendee(known, sender) {
				continue
			}
			imip.SetPartstat(s.master, sender, r.partstat)
			masterChanged = true
			continue
		}
		_, slot, err := resolveSlot(known, r.key)
		if err != nil {
			slog.Info("calendar: a reply names no occurrence of the series, skipped", "user", userID, "guid", known.Guid, "error", err)
			continue
		}
		i, ov := s.overrideAt(slot)
		if ov != nil {
			if r.seq > componentSequence(ov) || !imip.SetPartstat(ov, sender, r.partstat) {
				continue
			}
			s.cal.Children[i] = ov
			overridesChanged = true
			continue
		}
		if r.seq > int(known.Sequence) || !hasAttendee(known, sender) {
			continue
		}
		if s.overrideCount() >= maxReplyOverrides {
			slog.Info("calendar: a reply would exceed the override limit, left as mail", "guid", known.Guid, "limit", maxReplyOverrides)
			continue
		}
		occ, found := store.FindOccurrence(known, r.key)
		if !found {
			continue
		}
		ov = newOverride(s, nil, occ, slot, int(known.Sequence), now)
		if !imip.SetPartstat(ov, sender, r.partstat) {
			continue
		}
		s.cal.Children = append(s.cal.Children, ov)
		overridesChanged = true
	}
	if !masterChanged && !overridesChanged {
		return nil, nil
	}
	ev := proto.Clone(known).(*bongov1.Event)
	if overridesChanged || len(known.IcalRaw) > 0 {
		if ev.IcalRaw, err = s.encode(); err != nil {
			return nil, err
		}
	}
	if masterChanged {
		for _, r := range replies {
			if r.key == "" {
				setAttendeePartstat(ev, map[string]bool{sender: true}, r.partstat)
				break
			}
		}
	}
	return ev, nil
}

// replyInstanceOnly applies the answer for the one occurrence an override-only
// object holds: its VEVENT is the series, so there are no overrides to make.
func replyInstanceOnly(known *bongov1.Event, replies []veventAnswer, sender string) (*bongov1.Event, error) {
	for _, r := range replies {
		if r.key != "" && !sameInstance(known, r.key) {
			continue
		}
		if r.seq > int(known.Sequence) || !hasAttendee(known, sender) {
			return nil, nil
		}
		cal, err := storedCalendar(known)
		if err != nil {
			return nil, err
		}
		if master, _ := icalevent.Master(cal); master != nil {
			imip.SetPartstat(master, sender, r.partstat)
		}
		ev := proto.Clone(known).(*bongov1.Event)
		if ev.IcalRaw, err = encodeCalendar(cal); err != nil {
			return nil, err
		}
		setAttendeePartstat(ev, map[string]bool{sender: true}, r.partstat)
		return ev, nil
	}
	return nil, nil
}
