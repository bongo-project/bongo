package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Resource calendars: a room or equipment account answers booking requests on
// its own. The account is a UserProfile with Kind "resource"; nobody can sign
// in to it, so everything here runs from the delivery stage or from the admin
// console.

const (
	// resourceHorizon is how far ahead a recurring booking is checked for
	// collisions. A series with an occurrence beyond resourceFarHorizon cannot
	// be checked and is declined.
	resourceHorizon    = 2 * 365 * 24 * time.Hour
	resourceFarHorizon = 10 * 365 * 24 * time.Hour

	// maxResourceBookings caps the bookings the admin page lists.
	maxResourceBookings = 500
)

// profileReader reads Pebble keys; *store.PebbleStore satisfies it.
type profileReader interface {
	Get(key []byte) ([]byte, error)
}

// SetProfiles wires the profile reads that tell resources from people. nil
// (the default) turns resource calendars off: nothing answers bookings and no
// free/busy is shared by resource.
func (h *CalendarHandler) SetProfiles(p profileReader) { h.profiles = p }

// HasProfiles reports whether the profile reader is wired.
func (h *CalendarHandler) HasProfiles() bool { return h.profiles != nil }

// profile returns the stored profile of userID, or nil when it cannot be read.
func (h *CalendarHandler) profile(userID string) *bongov1.UserProfile {
	if h.profiles == nil || userID == "" {
		return nil
	}
	data, err := h.profiles.Get([]byte("/user/" + userID + "/profile"))
	if err != nil || len(data) == 0 {
		return nil
	}
	var p bongov1.UserProfile
	if err := proto.Unmarshal(data, &p); err != nil {
		return nil
	}
	return &p
}

// isResource reports whether p is a room or equipment account.
func isResource(p *bongov1.UserProfile) bool { return p != nil && p.Kind == auth.ResourceKind }

// sharedResource reports whether targetUser is a resource on the caller's own
// domain, whose free/busy every user of the domain may read. Both domains come
// from the stored profiles, never from the request.
func (h *CalendarHandler) sharedResource(targetUser, callerID string) bool {
	target := h.profile(targetUser)
	if !isResource(target) {
		return false
	}
	caller := h.profile(callerID)
	if caller == nil || isResource(caller) || caller.Disabled {
		return false
	}
	d := strings.ToLower(domainFromEmail(target.Email))
	return d != "" && d == strings.ToLower(domainFromEmail(caller.Email))
}

// localPerson reports whether addr is the verified address of a person account
// (not a resource, not disabled) on this server.
func (h *CalendarHandler) localPerson(addr string) bool {
	if h.addresses == nil {
		return false
	}
	rec, ok, err := h.addresses.Get(addr)
	if err != nil || !ok || !rec.Verified || rec.UserId == "" {
		return false
	}
	p := h.profile(rec.UserId)
	return p != nil && p.Kind == "" && !p.Disabled
}

// AnswerBooking answers an iTIP REQUEST that was delivered to userID when
// userID is a resource, and releases a booking on a CANCEL. The delivery stage
// decides trust: verifiedSender is the From address it could vouch for, and ""
// means nothing happens.
//
// It returns true only when the message reached a final outcome: a REPLY
// (ACCEPTED or DECLINED) was delivered to the organiser, or a CANCEL from the
// booking's organiser was applied. The delivery stage then does not keep the
// mail in the resource's INBOX. Every refusal and failure returns false and the
// mail is stored as ordinary mail. If the booking write succeeds and the REPLY
// cannot be sent, the booking stands, the organiser is not told and the mail is
// stored; the write is not rolled back. A retry of the delivery stage after a
// later recipient failed repeats the answer (at-least-once, as everywhere in
// delivery); the booking is then re-accepted idempotently.
//
// Nothing is sent unless the verified sender is also the ORGANIZER. A reply to
// an unverified or merely claimed address would let anyone aim the resource at
// a third party (backscatter). A verified organiser on another domain, or one
// that is not a local person account, is answered DECLINED and nothing is
// written. The booking stored is the organiser's object copied through
// imip.Invite.Stored.
//
// A message about one occurrence (RECURRENCE-ID) is handled per occurrence: a
// CANCEL writes a cancelled override, which frees that slot and nothing else; a
// REQUEST is checked for its moved slot only and answered with the same
// RECURRENCE-ID. A series the resource does not hold is answered DECLINED.
//
// The check and the write of one resource run under a per-resource lock, so two
// requests for the same slot cannot both be accepted. The lock belongs to this
// process: in a cluster the delivery stage can run on any node, and two nodes
// handling requests for one resource at the same moment are not serialised.
// Production runs one node.
func (h *CalendarHandler) AnswerBooking(userID, verifiedSender string, part []byte, ctMethod string) bool {
	sender := store.NormaliseAddress(verifiedSender)
	if h == nil || h.events == nil || h.calendars == nil || sender == "" {
		return false
	}
	res := h.profile(userID)
	if !isResource(res) || res.Disabled {
		return false
	}
	inv, err := imip.Parse(part, ctMethod)
	if err != nil || (inv.Method != imip.MethodRequest && inv.Method != imip.MethodCancel) {
		return false
	}
	if sender != store.NormaliseAddress(inv.Organizer) {
		slog.Info("calendar: a booking request was not from its organiser, ignored", "resource", userID)
		return false
	}
	mine := h.myAddresses(userID, res.Email)
	if mine[sender] {
		return false
	}

	unlock := eventReplyLocks.lock("resource|" + userID)
	released := false
	release := func() {
		if !released {
			released = true
			unlock()
		}
	}
	defer release()

	known, err := h.findKnown(userID, inv.UID)
	if err != nil {
		slog.Warn("calendar: looking up a booking failed", "resource", userID, "error", err)
		return false
	}
	// Only the organiser of a booking changes or releases it.
	own := known != nil && store.NormaliseAddress(known.Organizer) == sender
	st := &inviteState{inv: inv, known: known, mine: mine, instanceOnly: known != nil && isInstanceOnly(known)}

	if inv.Method == imip.MethodCancel {
		return own && h.releaseBooking(userID, st)
	}

	me := resourceAttendee(inv.Event, mine)
	if me == nil || !icalevent.ValidAddress(me.Address) || !h.maySendAs(userID, res.Email, me.Address) {
		return false
	}
	me = &bongov1.Attendee{Address: me.Address, Name: res.DisplayName}
	st.me = me

	var partstat string
	var ok bool
	if inv.RecurrenceID != "" {
		partstat, ok = h.answerOccurrence(userID, res, sender, st, own)
	} else {
		partstat, ok = h.answerSeries(userID, res, sender, st, own)
	}
	if !ok {
		return false
	}
	release()

	reply, err := replyEvent(inv, me, partstat)
	if err != nil {
		slog.Warn("calendar: building a booking answer failed", "resource", userID, "error", err)
		return false
	}
	// The organiser's locale is not known here; the resource's is used. The
	// REPLY carries the RECURRENCE-ID of an occurrence the message was about.
	if !h.deliverReply(userID, res.Email, me.Address, h.storedLocale(userID), reply, inv.RecurrenceID) {
		slog.Warn("calendar: a booking answer was not sent", "resource", userID, "partstat", partstat)
		return false
	}
	return true
}

// releaseBooking applies a CANCEL from the booking's organiser: the whole
// series is deleted, or for one occurrence a cancelled override is written,
// which frees that slot and leaves the other occurrences booked. An occurrence
// that does not exist, or a booking that is a lone override, changes nothing.
// It reports whether the cancellation was applied. The caller holds the lock.
func (h *CalendarHandler) releaseBooking(userID string, st *inviteState) bool {
	known := st.known
	if st.inv.RecurrenceID == "" {
		if err := h.events.Delete(userID, known.Guid); err != nil {
			slog.Warn("calendar: releasing a booking failed", "resource", userID, "guid", known.Guid, "error", err)
			return false
		}
		return true
	}
	if st.instanceOnly {
		return false
	}
	if _, _, err := resolveSlot(known, st.inv.RecurrenceID); err != nil {
		return false
	}
	return h.writeOverride(userID, st, h.markCancelled)
}

// writeOverride builds the stored event with build and writes it over the
// booking. false means it could not be built or stored.
func (h *CalendarHandler) writeOverride(userID string, st *inviteState, build func(*inviteState) (*bongov1.Event, error)) bool {
	ev, err := build(st)
	if err != nil {
		slog.Warn("calendar: updating one occurrence of a booking failed", "resource", userID, "error", err)
		return false
	}
	if res := h.storeInvited(userID, st.known, ev); res.Status != 0 {
		slog.Warn("calendar: one occurrence of a booking could not be stored", "resource", userID, "status", res.Status)
		return false
	}
	return true
}

// answerOccurrence decides a REQUEST that moves one occurrence of a series and
// returns the PARTSTAT to answer with. ok is false when nothing should be
// answered (the write failed). Only the organiser of a series the resource
// holds, and only an occurrence of it, can be accepted; anything else is
// declined and nothing is written. The moved slot alone is checked, against
// every other booking and against the other occurrences of this series. When it
// clashes, the old occurrence is released with a cancelled override, as a
// declined series move deletes the booking: the old slot no longer describes
// the meeting.
func (h *CalendarHandler) answerOccurrence(userID string, res *bongov1.UserProfile, sender string, st *inviteState, own bool) (partstat string, ok bool) {
	inv, known := st.inv, st.known
	if !own || st.instanceOnly || !h.mayBook(res, sender) {
		return "DECLINED", true
	}
	canonical, _, err := resolveSlot(known, inv.RecurrenceID)
	if err != nil {
		return "DECLINED", true
	}
	cand := proto.Clone(inv.Event).(*bongov1.Event)
	cand.Rrule, cand.Exdates = "", nil
	conflict, err := h.bookingConflicts(userID, res.Email, cand, func(e *bongov1.Event) bool {
		return e.Guid == known.Guid && e.RecurrenceId == canonical
	})
	if err != nil {
		slog.Warn("calendar: checking a booking failed", "resource", userID, "error", err)
		return "", false
	}
	if !conflict {
		if !h.writeOverride(userID, st, func(st *inviteState) (*bongov1.Event, error) {
			return h.mergeInstance(st, func(ve *ical.Component) { imip.SetPartstat(ve, st.me.Address, "ACCEPTED") })
		}) {
			return "", false
		}
		return "ACCEPTED", true
	}
	// Best-effort: the decline is the answer the organiser needs whether or
	// not the old slot is released, and writeOverride logs a failure with the
	// resource. The booking then keeps the old occurrence until the next change.
	h.writeOverride(userID, st, h.markCancelled)
	return "DECLINED", true
}

// answerSeries decides a REQUEST for a whole booking and returns the PARTSTAT to
// answer with; ok is false when nothing should be answered.
func (h *CalendarHandler) answerSeries(userID string, res *bongov1.UserProfile, sender string, st *inviteState, own bool) (partstat string, ok bool) {
	inv, known, mine, me := st.inv, st.known, st.mine, st.me
	partstat = "DECLINED"
	if h.mayBook(res, sender) && (known == nil || own) {
		var exclude func(*bongov1.Event) bool
		if own {
			exclude = func(e *bongov1.Event) bool { return e.Guid == known.Guid }
		}
		// inv.Event carries neither EXDATEs nor overrides: the request's own
		// calendar does, and the series is checked as it will happen.
		cand := proto.Clone(inv.Event).(*bongov1.Event)
		var err error
		if cand.IcalRaw, err = inv.Stored("", ""); err != nil {
			slog.Warn("calendar: encoding a booking request failed", "resource", userID, "error", err)
			return "", false
		}
		conflict, err := h.bookingConflicts(userID, res.Email, cand, exclude)
		if err != nil {
			slog.Warn("calendar: checking a booking failed", "resource", userID, "error", err)
			return "", false
		}
		if !conflict {
			partstat = "ACCEPTED"
		}
	}

	switch {
	case partstat == "ACCEPTED":
		if !h.writeBooking(userID, known, inv, mine, me.Address) {
			return "", false
		}
	case own:
		// The organiser moved the meeting to a time the resource cannot
		// give: the old booking no longer describes anything.
		if err := h.events.Delete(userID, known.Guid); err != nil {
			slog.Warn("calendar: releasing a declined booking failed", "resource", userID, "guid", known.Guid, "error", err)
		}
	}
	return partstat, true
}

// resourceAttendee returns the ATTENDEE of e that is one of the resource's
// addresses, or nil.
func resourceAttendee(e *bongov1.Event, mine map[string]bool) *bongov1.Attendee {
	for _, a := range e.GetAttendees() {
		if mine[store.NormaliseAddress(a.Address)] {
			return a
		}
	}
	return nil
}

// mayBook reports whether sender may book the resource: a local person account
// on the resource's own domain.
func (h *CalendarHandler) mayBook(res *bongov1.UserProfile, sender string) bool {
	d := strings.ToLower(domainFromEmail(res.Email))
	if d == "" || d != strings.ToLower(domainFromEmail(sender)) {
		return false
	}
	return h.localPerson(sender)
}

// writeBooking stores the accepted booking in the resource's calendar, in place
// of known when the organiser is updating it. false means it could not be
// stored and nothing should be answered.
func (h *CalendarHandler) writeBooking(userID string, known *bongov1.Event, inv *imip.Invite, mine map[string]bool, meAddr string) bool {
	calID := ""
	guid := uidGuid(inv.UID)
	if known != nil {
		calID, guid = known.CalendarId, known.Guid
	} else {
		var err error
		if calID, err = h.calendars.EnsureDefault(userID); err != nil {
			slog.Warn("calendar: no calendar for a booking", "resource", userID, "error", err)
			return false
		}
	}
	raw, err := inv.Stored(meAddr, "ACCEPTED")
	if err != nil {
		slog.Warn("calendar: encoding a booking failed", "resource", userID, "error", err)
		return false
	}
	ev := proto.Clone(inv.Event).(*bongov1.Event)
	ev.Guid, ev.CalendarId, ev.IcalRaw = guid, calID, raw
	setAttendeePartstat(ev, mine, "ACCEPTED")
	if res := h.storeInvited(userID, known, ev); res.Status != 0 {
		slog.Warn("calendar: a booking could not be stored", "resource", userID, "status", res.Status)
		return false
	}
	return true
}

// bookingConflicts reports whether ev, with every occurrence of its series,
// overlaps a booking of the resource. A listed event for which exclude (may be
// nil) is true, such as the booking ev updates, does not count, and slots that only touch do not overlap. A
// series that cannot be checked, because it has no end or runs past the far
// horizon, counts as a conflict.
func (h *CalendarHandler) bookingConflicts(resID, resEmail string, ev *bongov1.Event, exclude func(*bongov1.Event) bool) (bool, error) {
	occ := []*bongov1.Event{ev}
	first, last := ev.StartTime, ev.EndTime
	if ev.Rrule != "" {
		up := strings.ToUpper(ev.Rrule)
		if !strings.Contains(up, "COUNT=") && !strings.Contains(up, "UNTIL=") {
			return true, nil
		}
		// An override can move an occurrence before the first slot of the
		// rule, so the expansion starts at the epoch.
		from := ev.StartTime
		to := from + int64(resourceHorizon/time.Second)
		far := from + int64(resourceFarHorizon/time.Second)
		var err error
		if occ, err = store.ExpandRecurrence(ev, 0, to); err != nil {
			return true, nil //nolint:nilerr // a rule that does not parse cannot be checked
		}
		if beyond, err := store.ExpandRecurrence(ev, to, far); err != nil || len(beyond) > 0 {
			return true, nil //nolint:nilerr // see above
		}
		if len(occ) > 0 {
			first, last = occ[0].StartTime, occ[0].EndTime
		}
		for _, o := range occ {
			first, last = min(first, o.StartTime), max(last, o.EndTime)
		}
	}
	if len(occ) == 0 {
		return false, nil
	}
	existing, err := h.events.ListByDateRange(resID, first, max(last, first+1), "")
	if err != nil {
		return false, fmt.Errorf("listing bookings: %w", err)
	}
	busy := make([]*bongov1.Event, 0, len(existing))
	for _, e := range existing {
		if (exclude == nil || !exclude(e)) && bookingHolds(e) {
			busy = append(busy, e)
		}
	}
	for _, o := range occ {
		for _, e := range busy {
			if o.StartTime < e.EndTime && o.EndTime > e.StartTime {
				return true, nil
			}
		}
	}
	return false, nil
}

// bookingHolds reports whether an event of the resource's calendar holds the
// resource: it is neither cancelled nor transparent.
func bookingHolds(e *bongov1.Event) bool {
	if strings.EqualFold(e.Status, "CANCELLED") {
		return false
	}
	if len(e.IcalRaw) > 0 {
		if cal, err := storedCalendar(e); err == nil {
			if master, _ := icalevent.Master(cal); master != nil {
				if p := master.Props.Get("TRANSP"); p != nil && strings.EqualFold(strings.TrimSpace(p.Value), "TRANSPARENT") {
					return false
				}
			}
		}
	}
	return true
}

// ResourceBookings is what the admin console needs from the calendar side of a
// resource. *CalendarHandler implements it.
type ResourceBookings interface {
	// EnsureResourceCalendar creates the resource's default calendar.
	EnsureResourceCalendar(userID string) error
	// Bookings lists the events of the resource, earliest first.
	Bookings(userID string) ([]*bongov1.Event, error)
	// CancelBooking deletes a booking and tells its organiser.
	CancelBooking(userID, guid string) error
}

// EnsureResourceCalendar creates the default calendar bookings are filed in.
func (h *CalendarHandler) EnsureResourceCalendar(userID string) error {
	if h == nil || h.calendars == nil {
		return errors.New("calendar store not available")
	}
	if _, err := h.calendars.EnsureDefault(userID); err != nil {
		return fmt.Errorf("creating the resource calendar: %w", err)
	}
	return nil
}

// Bookings lists the events of a resource, series not expanded.
func (h *CalendarHandler) Bookings(userID string) ([]*bongov1.Event, error) {
	if h == nil || h.events == nil {
		return nil, errors.New("event store not available")
	}
	events, _, err := h.events.ListAll(userID, 0, maxResourceBookings)
	if err != nil {
		return nil, fmt.Errorf("listing bookings: %w", err)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].StartTime < events[j].StartTime })
	return events, nil
}

// CancelBooking deletes the booking guid of the resource. A failed delete is
// returned and nothing is sent. The organiser is then told with a DECLINED
// answer from the resource; that part is best-effort and logged.
func (h *CalendarHandler) CancelBooking(userID, guid string) error {
	if h == nil || h.events == nil {
		return errors.New("event store not available")
	}
	res := h.profile(userID)
	if !isResource(res) {
		return errors.New("not a resource")
	}
	defer eventReplyLocks.lock("resource|" + userID)()

	ev, err := h.events.Get(userID, guid)
	if err != nil {
		return fmt.Errorf("reading booking: %w", err)
	}
	if err := h.events.Delete(userID, guid); err != nil {
		return fmt.Errorf("deleting booking: %w", err)
	}
	h.tellOrganiser(userID, res, ev)
	return nil
}

// tellOrganiser sends the organiser of a cancelled booking a REPLY in which the
// resource declines. It is best-effort.
func (h *CalendarHandler) tellOrganiser(userID string, res *bongov1.UserProfile, ev *bongov1.Event) {
	if h.invites == nil || !icalevent.ValidAddress(ev.Organizer) {
		return
	}
	mine := h.myAddresses(userID, res.Email)
	me := resourceAttendee(ev, mine)
	if me == nil || !h.maySendAs(userID, res.Email, me.Address) {
		return
	}
	reply := proto.Clone(ev).(*bongov1.Event)
	reply.Guid = ""
	reply.Attendees = []*bongov1.Attendee{{Address: me.Address, Name: res.DisplayName, Partstat: "DECLINED"}}
	if len(ev.IcalRaw) > 0 {
		cal, err := storedCalendar(ev)
		if err != nil {
			slog.Warn("calendar: telling the organiser of a cancelled booking failed", "resource", userID, "error", err)
			return
		}
		if master, _ := icalevent.Master(cal); master != nil {
			imip.SetPartstat(master, me.Address, "DECLINED")
		}
		if reply.IcalRaw, err = encodeCalendar(cal); err != nil {
			slog.Warn("calendar: telling the organiser of a cancelled booking failed", "resource", userID, "error", err)
			return
		}
	} else {
		// Without a stored object the UID is the guid, which the reply
		// needs to carry.
		reply.Guid = ev.Guid
	}
	if !h.deliverReply(userID, res.Email, me.Address, h.storedLocale(userID), reply, "") {
		slog.Warn("calendar: the organiser of a cancelled booking was not told", "resource", userID)
	}
}
