package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

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
// means nothing happens. Everything is best-effort: the mail is already in the
// INBOX, so every refusal and failure just returns, and a failed write is
// logged.
//
// Nothing is sent unless the verified sender is also the ORGANIZER. A reply to
// an unverified or merely claimed address would let anyone aim the resource at
// a third party (backscatter). A verified organiser on another domain, or one
// that is not a local person account, is answered DECLINED and nothing is
// written. The booking stored is the organiser's object copied through
// imip.Invite.Stored.
//
// The check and the write of one resource run under a per-resource lock, so two
// requests for the same slot cannot both be accepted. The lock belongs to this
// process: in a cluster the delivery stage can run on any node, and two nodes
// handling requests for one resource at the same moment are not serialised.
// Production runs one node.
func (h *CalendarHandler) AnswerBooking(userID, verifiedSender string, part []byte, ctMethod string) {
	sender := store.NormaliseAddress(verifiedSender)
	if h == nil || h.events == nil || h.calendars == nil || sender == "" {
		return
	}
	res := h.profile(userID)
	if !isResource(res) || res.Disabled {
		return
	}
	inv, err := imip.Parse(part, ctMethod)
	if err != nil || (inv.Method != imip.MethodRequest && inv.Method != imip.MethodCancel) {
		return
	}
	if sender != store.NormaliseAddress(inv.Organizer) {
		slog.Info("calendar: a booking request was not from its organiser, ignored", "resource", userID)
		return
	}
	mine := h.myAddresses(userID, res.Email)
	if mine[sender] {
		return
	}
	if inv.RecurrenceID != "" {
		// A change to one occurrence of a series is not supported; the
		// booking of the series stays as it is.
		slog.Info("calendar: a booking request for one occurrence was ignored", "resource", userID)
		return
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
		return
	}
	// Only the organiser of a booking changes or releases it.
	own := known != nil && store.NormaliseAddress(known.Organizer) == sender

	if inv.Method == imip.MethodCancel {
		if own {
			if err := h.events.Delete(userID, known.Guid); err != nil {
				slog.Warn("calendar: releasing a booking failed", "resource", userID, "guid", known.Guid, "error", err)
			}
		}
		return
	}

	me := resourceAttendee(inv.Event, mine)
	if me == nil || !icalevent.ValidAddress(me.Address) || !h.maySendAs(userID, res.Email, me.Address) {
		return
	}
	me = &bongov1.Attendee{Address: me.Address, Name: res.DisplayName}

	partstat := "DECLINED"
	if h.mayBook(res, sender) && (known == nil || own) {
		exclude := ""
		if own {
			exclude = known.Guid
		}
		conflict, err := h.bookingConflicts(userID, res.Email, inv.Event, exclude)
		if err != nil {
			slog.Warn("calendar: checking a booking failed", "resource", userID, "error", err)
			return
		}
		if !conflict {
			partstat = "ACCEPTED"
		}
	}

	switch {
	case partstat == "ACCEPTED":
		if !h.writeBooking(userID, known, inv, mine, me.Address) {
			return
		}
	case own:
		// The organiser moved the meeting to a time the resource cannot
		// give: the old booking no longer describes anything.
		if err := h.events.Delete(userID, known.Guid); err != nil {
			slog.Warn("calendar: releasing a declined booking failed", "resource", userID, "guid", known.Guid, "error", err)
		}
	}
	release()

	reply, err := replyEvent(inv, me, partstat)
	if err != nil {
		slog.Warn("calendar: building a booking answer failed", "resource", userID, "error", err)
		return
	}
	// The organiser's locale is not known here; the resource's is used.
	if !h.deliverReply(userID, res.Email, me.Address, h.storedLocale(userID), reply, "") {
		slog.Warn("calendar: a booking answer was not sent", "resource", userID, "partstat", partstat)
	}
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
// overlaps a booking of the resource. The booking with guid exclude (the one
// ev updates) does not count, and slots that only touch do not overlap. A
// series that cannot be checked, because it has no end or runs past the far
// horizon, counts as a conflict.
func (h *CalendarHandler) bookingConflicts(resID, resEmail string, ev *bongov1.Event, exclude string) (bool, error) {
	occ := []*bongov1.Event{ev}
	last := ev.EndTime
	if ev.Rrule != "" {
		up := strings.ToUpper(ev.Rrule)
		if !strings.Contains(up, "COUNT=") && !strings.Contains(up, "UNTIL=") {
			return true, nil
		}
		from := ev.StartTime
		to := from + int64(resourceHorizon/time.Second)
		far := from + int64(resourceFarHorizon/time.Second)
		var err error
		if occ, err = store.ExpandRecurrence(ev, from, to); err != nil {
			return true, nil //nolint:nilerr // a rule that does not parse cannot be checked
		}
		if beyond, err := store.ExpandRecurrence(ev, to, far); err != nil || len(beyond) > 0 {
			return true, nil //nolint:nilerr // see above
		}
		for _, o := range occ {
			last = max(last, o.EndTime)
		}
	}
	if len(occ) == 0 {
		return false, nil
	}
	existing, err := h.events.ListByDateRange(resID, ev.StartTime, max(last, ev.StartTime+1), "")
	if err != nil {
		return false, fmt.Errorf("listing bookings: %w", err)
	}
	busy := make([]*bongov1.Event, 0, len(existing))
	for _, e := range existing {
		if e.Guid != exclude && bookingHolds(e) {
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
