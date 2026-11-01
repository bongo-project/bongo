package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// inviteSender delivers a message the server built on a user's behalf, with
// the user's send-as, rate-limit and quota rules applied. *ComposeHandler
// satisfies it. notReached names the recipients the message did not reach.
type inviteSender interface {
	DeliverGenerated(userID, sessionEmail, from string, rcpts []string, raw []byte) (notReached []string, err error)
}

// SetInviteSender wires the delivery of iMIP invitations, updates and
// cancellations from the web UI. nil (the default) sends nothing.
func (h *CalendarHandler) SetInviteSender(s inviteSender) { h.invites = s }

// SetAddressStore wires the send-as check that decides whether the acting user
// may send as an event's organiser. nil permits only the session address.
func (h *CalendarHandler) SetAddressStore(as *store.AddressStore) { h.addresses = as }

// InviteSender returns the wired invitation sender, or nil.
func (h *CalendarHandler) InviteSender() inviteSender { return h.invites }

// HasAddressStore reports whether the send-as store is wired.
func (h *CalendarHandler) HasAddressStore() bool { return h.addresses != nil }

// invitation is one iMIP message to send: a REQUEST or CANCEL about event to a
// set of recipients.
type invitation struct {
	method  string
	updated bool
	event   *bongov1.Event
	to      []string
	// recurrenceID makes the message about one instance of a series; uid, when
	// set, replaces the UID of the built event (an instance is built from its
	// structured fields, whose UID is the guid, while a CalDAV series keeps the
	// UID its client chose).
	recurrenceID string
	uid          string
}

// notifyAttendees sends the iMIP messages that follow a saved change to an
// event: before is the event as stored earlier (nil for a create) and after
// the event as saved (nil for a delete). It returns the addresses the messages
// did not reach.
//
// Sending is best-effort by design: the event is already saved, and a failure
// is reported to the user by name rather than undoing the save. Nothing is
// sent unless the acting user may send as the event's organiser, so an event
// organised by someone else (imported over CalDAV, say) is never answered in
// the organiser's name. Rate limits and quota apply to the acting user.
//
// A save that changes neither time, place, repeat rule nor attendees sends
// nothing, so it does not spend the recipient budget.
func (h *CalendarHandler) notifyAttendees(r *http.Request, userID, email string, before, after *bongov1.Event) (notReached []string) {
	if h.invites == nil || (before == nil && after == nil) {
		return nil
	}
	ev := after
	if ev == nil {
		ev = before
	}
	organiser := ev.Organizer
	if !icalevent.ValidAddress(organiser) || !h.maySendAs(userID, email, organiser) {
		return nil
	}

	var plan []invitation
	switch {
	case before == nil:
		plan = append(plan, invitation{method: imip.MethodRequest, event: after, to: h.inviteRecipients(userID, email, organiser, after.Attendees)})
	case after == nil:
		gone := proto.Clone(before).(*bongov1.Event)
		gone.Sequence = before.Sequence + 1
		plan = append(plan, invitation{method: imip.MethodCancel, event: gone, to: h.inviteRecipients(userID, email, organiser, before.Attendees)})
	case inviteRelevantChange(before, after):
		plan = append(plan, invitation{method: imip.MethodRequest, updated: true, event: after, to: h.inviteRecipients(userID, email, organiser, after.Attendees)})
		if removed := removedAttendees(before, after); len(removed) > 0 {
			gone := proto.Clone(before).(*bongov1.Event)
			gone.Attendees = removed
			// At least one above the REQUEST the attendee last saw.
			gone.Sequence = max(after.Sequence, before.Sequence+1)
			plan = append(plan, invitation{method: imip.MethodCancel, event: gone, to: h.inviteRecipients(userID, email, organiser, removed)})
		}
	}

	return h.deliverPlan(r, userID, email, plan)
}

// deliverPlan sends each invitation of plan that has recipients and returns
// the addresses that were not reached.
func (h *CalendarHandler) deliverPlan(r *http.Request, userID, email string, plan []invitation) (notReached []string) {
	locale := middleware.LocaleFromContext(r.Context())
	loc := h.userLocation(userID)
	now := h.nowIn(userID)
	for _, inv := range plan {
		if len(inv.to) == 0 {
			continue
		}
		missed := h.sendInvitation(userID, email, locale, loc, now, inv)
		notReached = append(notReached, missed...)
	}
	return notReached
}

// notifyInstance sends the iTIP messages that follow an instance edit or
// delete, with the guards of notifyAttendees. A "this event" change is about
// that instance alone (RECURRENCE-ID set); "this and following" changes the
// series: the original with its new UNTIL and, for an edit, the new series. A
// series with no attendees sends nothing.
func (h *CalendarHandler) notifyInstance(r *http.Request, userID, email string, c *InstanceChange) (notReached []string) {
	if h.invites == nil || c == nil || c.Before == nil {
		return nil
	}
	organiser := c.Before.Organizer
	if !icalevent.ValidAddress(organiser) || !h.maySendAs(userID, email, organiser) {
		return nil
	}
	to := func(e *bongov1.Event) []string { return h.inviteRecipients(userID, email, organiser, e.Attendees) }

	var plan []invitation
	switch {
	case c.Scope == scopeThis && !c.Deleted:
		if c.Instance != nil {
			plan = append(plan, invitation{method: imip.MethodRequest, updated: true, event: c.Instance, to: to(c.Instance),
				recurrenceID: c.RecurrenceID, uid: seriesUID(c.After)})
		}
	case c.Scope == scopeThis:
		if inst, ok := store.FindOccurrence(c.Before, c.RecurrenceID); ok {
			// At least one above the SEQUENCE the attendee last saw.
			inst.Sequence = max(c.After.Sequence, c.Before.Sequence+1)
			plan = append(plan, invitation{method: imip.MethodCancel, event: inst, to: to(inst),
				recurrenceID: c.RecurrenceID, uid: seriesUID(c.Before)})
		}
	case c.After == nil:
		// The first instance was chosen: the whole series is gone.
		gone := proto.Clone(c.Before).(*bongov1.Event)
		gone.Sequence = c.Before.Sequence + 1
		plan = append(plan, invitation{method: imip.MethodCancel, event: gone, to: to(gone)})
	default:
		plan = append(plan, invitation{method: imip.MethodRequest, updated: true, event: c.After, to: to(c.After)})
		if c.NewSeries != nil {
			plan = append(plan, invitation{method: imip.MethodRequest, event: c.NewSeries, to: to(c.NewSeries)})
		}
	}
	return h.deliverPlan(r, userID, email, plan)
}

// seriesUID is the UID a series is known by outside: the one in its stored
// iCalendar object, or its guid.
func seriesUID(e *bongov1.Event) string {
	if e == nil {
		return ""
	}
	if len(e.IcalRaw) > 0 {
		if s, err := decodeSeries(e); err == nil {
			if p := s.master.Props.Get(ical.PropUID); p != nil && p.Value != "" {
				return p.Value
			}
		}
	}
	return e.Guid
}

// sendInvitation builds and delivers one message. A message that cannot be
// built or delivered at all reports every recipient as not reached.
func (h *CalendarHandler) sendInvitation(userID, email, locale string, loc *time.Location, now time.Time, inv invitation) []string {
	organiser := inv.event.Organizer
	cal, err := imip.BuildCalendar(inv.method, inv.event, inv.recurrenceID, now)
	if err != nil {
		slog.Warn("calendar: building invitation failed", "user", userID, "guid", inv.event.Guid, "method", inv.method, "error", err)
		return inv.to
	}
	if inv.uid != "" {
		if ve, _ := icalevent.Master(cal); ve != nil {
			ve.Props.SetText(ical.PropUID, inv.uid)
		}
	}
	subject, body := imip.Describe(inv.method, inv.updated, inv.event, loc, func(id string) string { return i18n.T(locale, id) })
	raw, err := imip.BuildMessage(imip.Params{
		Method: inv.method, From: organiser, To: inv.to, Subject: subject, TextBody: body, Cal: cal, Date: now,
	})
	if err != nil {
		slog.Warn("calendar: building invitation message failed", "user", userID, "guid", inv.event.Guid, "method", inv.method, "error", err)
		return inv.to
	}
	missed, err := h.invites.DeliverGenerated(userID, email, organiser, inv.to, raw)
	if err != nil {
		slog.Warn("calendar: sending invitations failed", "user", userID, "guid", inv.event.Guid, "method", inv.method,
			"recipients", len(inv.to), "not_reached", len(missed), "error", err)
		if len(missed) == 0 {
			// A failure that names nobody must not look like success.
			return inv.to
		}
	}
	return missed
}

// maySendAs reports whether the acting user may send as addr: the session
// address or a verified send-as address of theirs.
func (h *CalendarHandler) maySendAs(userID, email, addr string) bool {
	ok, err := store.CanSendAs(h.addresses, userID, email, addr)
	if err != nil {
		slog.Warn("calendar: send-as lookup failed, sending no invitations", "user", userID, "error", err)
		return false
	}
	return ok
}

// inviteRecipients returns the addresses to write to: valid, once each, and
// not the organiser or any other address the user sends as. A stored attendee
// list can come from a CalDAV client, so each address is validated again.
func (h *CalendarHandler) inviteRecipients(userID, email, organiser string, attendees []*bongov1.Attendee) []string {
	seen := map[string]bool{strings.ToLower(organiser): true, strings.ToLower(email): true}
	var out []string
	for i, a := range attendees {
		if i >= icalevent.MaxAttendees {
			break
		}
		key := strings.ToLower(a.Address)
		if seen[key] || !icalevent.ValidAddress(a.Address) {
			continue
		}
		seen[key] = true
		if own, err := store.CanSendAs(h.addresses, userID, email, a.Address); err == nil && own {
			continue
		}
		out = append(out, a.Address)
	}
	return out
}

// inviteRelevantChange reports whether an edit changes what an invitation
// tells: when, where, how often or who.
func inviteRelevantChange(before, after *bongov1.Event) bool {
	if before.StartTime != after.StartTime || before.EndTime != after.EndTime || before.AllDay != after.AllDay ||
		before.Location != after.Location || before.Rrule != after.Rrule {
		return true
	}
	return len(removedAttendees(before, after)) > 0 || len(removedAttendees(after, before)) > 0
}

// removedAttendees returns the attendees of before that after no longer has.
func removedAttendees(before, after *bongov1.Event) []*bongov1.Attendee {
	still := make(map[string]bool, len(after.Attendees))
	for _, a := range after.Attendees {
		still[strings.ToLower(a.Address)] = true
	}
	var out []*bongov1.Attendee
	for _, a := range before.Attendees {
		if !still[strings.ToLower(a.Address)] {
			out = append(out, a)
		}
	}
	return out
}

// The not-reached warning travels in the redirect as the first few addresses
// plus a count, so a 100-attendee failure does not make a 3 kB URL. Only
// addresses that pass icalevent.ValidAddress are shown, which limits what a
// crafted link can make the page say.
const (
	maxWarnAddresses = 5
	maxWarnMore      = 1000
	warnAddrParam    = "nr"
	warnMoreParam    = "nrmore"
)

// inviteWarningParams encodes the not-reached list as query parameters, or ""
// when everyone was reached.
func inviteWarningParams(notReached []string) string {
	if len(notReached) == 0 {
		return ""
	}
	shown := notReached
	if len(shown) > maxWarnAddresses {
		shown = shown[:maxWarnAddresses]
	}
	return encodeInviteWarning(shown, len(notReached)-len(shown))
}

func encodeInviteWarning(shown []string, more int) string {
	v := url.Values{warnAddrParam: {strings.Join(shown, ",")}}
	if more > 0 {
		v.Set(warnMoreParam, strconv.Itoa(more))
	}
	return v.Encode()
}

// withInviteWarning appends the warning parameters for notReached to path.
func withInviteWarning(path string, notReached []string) string {
	return appendParams(path, inviteWarningParams(notReached))
}

func appendParams(path, params string) string {
	switch {
	case params == "":
		return path
	case strings.Contains(path, "?"):
		return path + "&" + params
	default:
		return path + "?" + params
	}
}

// parseInviteWarning reads the warning parameters of a request, keeping only
// valid addresses.
func parseInviteWarning(q url.Values) (shown []string, more int) {
	for _, a := range strings.Split(q.Get(warnAddrParam), ",") {
		if icalevent.ValidAddress(a) && len(shown) < maxWarnAddresses {
			shown = append(shown, a)
		}
	}
	if len(shown) == 0 {
		return nil, 0
	}
	if n, err := strconv.Atoi(q.Get(warnMoreParam)); err == nil && n > 0 {
		more = min(n, maxWarnMore)
	}
	return shown, more
}

// inviteWarningText is the translated warning for a request that carries one,
// or "".
func inviteWarningText(r *http.Request) string {
	shown, more := parseInviteWarning(r.URL.Query())
	if len(shown) == 0 {
		return ""
	}
	locale := middleware.LocaleFromContext(r.Context())
	names := strings.Join(shown, ", ")
	if more > 0 {
		names = fmt.Sprintf(i18n.T(locale, "calendar.invitesNotReachedMore"), names, more)
	}
	return fmt.Sprintf(i18n.T(locale, "calendar.invitesNotReached"), names)
}
