package handlers

import (
	"log/slog"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SCHEDULE-STATUS values the CalDAV wrapper writes (RFC 6638 section 3.2.9).
const (
	scheduleSent       = "1.1"
	scheduleNotReached = "5.1"
	// scheduleNoAuthority (RFC 5546 3.8) reports an answer given at an address
	// the user may not send as.
	scheduleNoAuthority = "3.8"
)

// CalDAVScheduler lets the CalDAV backend send invitations and replies for a
// client's PUT and DELETE (RFC 6638 implicit scheduling). It adds no sending
// rules of its own: it plans and delivers through the same CalendarHandler
// paths the web UI uses, so send-as checks, SEQUENCE rules, rate limits and
// the recipient budget are those of the web invitation path. There is no request, so
// iMIP text is written in the acting user's preference locale. *CalDAVScheduler
// satisfies the Scheduler interface of internal/caldav.
type CalDAVScheduler struct {
	h      *CalendarHandler
	prefs  localeSource
	emails userEmailSource
}

// userEmailSource names a user's primary address; *store.ACLStore satisfies it.
type userEmailSource interface {
	UserEmail(userID string) string
}

// NewCalDAVScheduler returns a scheduler over h. prefs supplies the user's
// locale and may be nil (English); emails supplies the primary address.
func NewCalDAVScheduler(h *CalendarHandler, prefs localeSource, emails userEmailSource) *CalDAVScheduler {
	return &CalDAVScheduler{h: h, prefs: prefs, emails: emails}
}

func (s *CalDAVScheduler) email(userID string) string {
	if s.emails == nil {
		return ""
	}
	return s.emails.UserEmail(userID)
}

func (s *CalDAVScheduler) locale(userID string) string {
	if s.prefs != nil {
		if p := s.prefs.Get(userID); p != nil && p.Locale != "" {
			return p.Locale
		}
	}
	return "en"
}

// Addresses returns the calendar-user-address-set of the user: the primary
// address and the send-as identities that are verified. Plain aliases and
// lists are left out, because the user cannot send as them.
func (s *CalDAVScheduler) Addresses(userID string) []string {
	var out []string
	add := func(a string) {
		a = store.NormaliseAddress(a)
		if a != "" && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	add(s.email(userID))
	if s.h.addresses == nil {
		return out
	}
	list, err := s.h.addresses.ListByUser(userID)
	if err != nil {
		slog.Warn("caldav: listing the user's addresses failed", "user", userID, "error", err)
	}
	for _, a := range list {
		if rec, ok, err := s.h.addresses.Get(a); err == nil && ok && rec.UserId == userID && rec.CanSend && rec.Verified {
			add(a)
		}
	}
	return out
}

// Owns reports whether addr is any address of the user, aliases included.
func (s *CalDAVScheduler) Owns(userID, addr string) bool {
	return s.h.myAddresses(userID, s.email(userID))[store.NormaliseAddress(addr)]
}

// Organise sends the REQUEST and CANCEL messages that follow a saved change to
// an event the user organises: before is the event as stored earlier (nil for
// a create), after the event as saved (nil for a delete). Addresses in skip
// are sent nothing. The result maps each address a message was attempted for
// to its SCHEDULE-STATUS; a change that does not affect attendees sends
// nothing and returns an empty map.
func (s *CalDAVScheduler) Organise(userID string, before, after *bongov1.Event, skip map[string]bool) map[string]string {
	email := s.email(userID)
	plan := s.h.planAttendees(userID, email, before, after, skip)
	return s.deliver(userID, email, plan)
}

// OrganiseOccurrence sends the iTIP message about one occurrence of a series
// the user organises, after a saved change to its exceptions: a REQUEST with
// that RECURRENCE-ID and the series UID when instance is not nil, a CANCEL
// when it is nil. before is the series as stored earlier (nil for a create)
// and after as saved; original is the occurrence as attendees last saw it (nil
// when there was none) and instance as it is now. The recipients are those of
// the occurrence, an override's own ATTENDEEs included. The rules, results and
// skip are those of Organise.
func (s *CalDAVScheduler) OrganiseOccurrence(userID string, before, after *bongov1.Event, rid string, original, instance *bongov1.Event, skip map[string]bool) map[string]string {
	email := s.email(userID)
	plan := s.h.planInstance(userID, email, &InstanceChange{
		Scope: scopeThis, Guid: after.Guid, RecurrenceID: rid, Deleted: instance == nil,
		Before: before, After: after, Original: original, Instance: instance,
	}, skip)
	return s.deliver(userID, email, plan)
}

// deliver sends plan in the user's locale and maps each address a message was
// attempted for to its SCHEDULE-STATUS.
func (s *CalDAVScheduler) deliver(userID, email string, plan []invitation) map[string]string {
	sent, missed := s.h.deliverPlanIn(s.locale(userID), userID, email, plan)
	out := make(map[string]string, len(sent)+len(missed))
	for _, a := range sent {
		out[store.NormaliseAddress(a)] = scheduleSent
	}
	for _, a := range missed {
		out[store.NormaliseAddress(a)] = scheduleNotReached
	}
	return out
}

// Reply sends the iTIP REPLY for attendee's answer to an event the user was
// invited to. recurrenceID, when set, makes it an answer for one instance. It
// returns the SCHEDULE-STATUS of the answer: 1.1 when the organiser was told,
// 3.8 when the user may not send as attendee (a plain alias; no REPLY is sent
// in another name), 5.1 when that could not be looked up or the REPLY could not
// be built or delivered. A
// failure is logged by the sender.
func (s *CalDAVScheduler) Reply(userID string, ev *bongov1.Event, attendee, partstat, recurrenceID string) string {
	if ev == nil || !icalevent.ValidAddress(ev.Organizer) {
		return scheduleNotReached
	}
	// A failed lookup is not a refusal: 3.8 would be stored as the answer.
	may, err := s.h.sendAsCheck(userID, s.email(userID), attendee)
	if err != nil {
		return scheduleNotReached
	}
	if !may {
		return scheduleNoAuthority
	}
	reply := proto.Clone(ev).(*bongov1.Event)
	reply.Guid = ""
	name := ""
	for _, a := range ev.Attendees {
		if store.NormaliseAddress(a.Address) == store.NormaliseAddress(attendee) {
			name = a.Name
		}
	}
	reply.Attendees = []*bongov1.Attendee{{Address: attendee, Name: name, Partstat: partstat}}
	if len(ev.IcalRaw) > 0 {
		cal, err := storedCalendar(ev)
		if err != nil {
			return scheduleNotReached
		}
		master, _ := icalevent.Master(cal)
		if master == nil {
			return scheduleNotReached
		}
		imip.SetPartstat(master, attendee, partstat)
		if reply.IcalRaw, err = encodeCalendar(cal); err != nil {
			return scheduleNotReached
		}
	}
	if !s.h.sendReplyIn(s.locale(userID), userID, s.email(userID), reply, recurrenceID) {
		return scheduleNotReached
	}
	return scheduleSent
}

// FreeBusy answers the busy periods of the local user who owns address, as
// [start, end] unix seconds in [from, to], for requester. ok is false when the
// address is not a local user's or requester may not see that user's
// free/busy: the caller must answer both the same way. A user may see their
// own, another's when that user shared a calendar with them at free/busy
// level or above, and a resource's on their own domain, as on the web.
func (s *CalDAVScheduler) FreeBusy(requester, address string, from, to int64) (busy [][2]int64, ok bool) {
	h := s.h
	if h.addresses == nil || h.events == nil {
		return nil, false
	}
	rec, found, err := h.addresses.Get(store.NormaliseAddress(address))
	if err != nil || !found || rec.UserId == "" {
		return nil, false
	}
	target := rec.UserId
	if !h.mayReadFreeBusy(target, requester) {
		return nil, false
	}
	events, err := h.events.ListByDateRange(target, from, to, "")
	if err != nil {
		slog.Warn("caldav: loading events for a free/busy answer failed", "user", target, "error", err)
		return nil, false
	}
	targetEmail := ""
	if target == requester {
		targetEmail = s.email(requester)
	}
	return h.busyIntervals(target, targetEmail, events), true
}
