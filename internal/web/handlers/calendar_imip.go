package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/emersion/go-ical"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Receiving and answering iMIP invitations (RFC 5546, RFC 6047).
//
// A received calendar part is untrusted input. Rendering the card reads and
// never writes: every change to a calendar is a POST by the user, which parses
// the message again, so nothing in the request but the chosen action, the
// answer and the target calendar is believed. Concurrent edits of one event
// (the card and a CalDAV client) are last-writer-wins, as everywhere else.
//
// "Declined and hidden" is the attendee PARTSTAT=DECLINED on the stored event,
// kept in ical_raw and in the structured attendee list, with a filter in the
// web views; no extra field exists. A cancellation is the existing
// Event.status CANCELLED.

// inviteMessage is the calendar part of one message and what the mail handler
// knows about its envelope.
type inviteMessage struct {
	UserID, Email string
	// Mailbox and UID name the message in the user's mailbox.
	Mailbox string
	UID     uint32
	// Part is the decoded text/calendar part and CTMethod the method parameter
	// of its Content-Type.
	Part     []byte
	CTMethod string
	// From is the bare address of the header From.
	From string
	// AuthResults are the Authentication-Results header values of the stored
	// message.
	AuthResults []string
}

// inviteAction is the user's choice, as the form carried it.
type inviteAction struct {
	Action     string // answer, update, cancel or remove
	Partstat   string // ACCEPTED, TENTATIVE or DECLINED, for answer
	CalendarID string // optional target calendar, for answer
}

// inviteResult says how an action ended. A non-zero Status is an HTTP error.
type inviteResult struct {
	Status      int
	Message     string
	ReplyFailed bool
}

// invitationService is what the mail handler needs of the calendar side.
type invitationService interface {
	InvitationCard(r *http.Request, msg inviteMessage) *templates.InvitationCard
	ApplyInvitation(r *http.Request, msg inviteMessage, act inviteAction) inviteResult
}

// SetAuthservID sets the authserv-id of the Authentication-Results header the
// delivery stage stamps (the configured SMTP hostname). Empty finds no verdict,
// so no DMARC warning is shown.
func (h *CalendarHandler) SetAuthservID(id string) { h.authservID = id }

// AuthservID returns the configured authserv-id.
func (h *CalendarHandler) AuthservID() string { return h.authservID }

// errNotInvitation covers every reason a part is not a card: it did not parse
// or it carries another method.
var errNotInvitation = errors.New("not an invitation")

// Limits on what the card shows.
const (
	maxCardTitle     = 200
	maxCardWhere     = 200
	maxCardName      = 100
	maxCardDesc      = 2000
	maxCardAttendees = 20
	maxCardConflicts = 5
)

var safeUIDKey = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,128}$`)

// uidGuid is the identity of an event received by iMIP: the hex SHA-256 of its
// UID. A UID is chosen by the sender and may hold anything, so it is never used
// in a key itself; the fixed derivation also makes a double submit one PUT.
func uidGuid(uid string) string {
	sum := sha256.Sum256([]byte(uid))
	return hex.EncodeToString(sum[:])
}

// cleanDisplay makes an untrusted string safe to show: control characters (and
// newlines unless keepLines), bidirectional controls and zero-width characters
// are dropped, and the result is cut to max runes.
func cleanDisplay(s string, max int, keepLines bool) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepLines, r == '\t' && keepLines:
			return r
		case r == '\r':
			return -1
		case unicode.IsControl(r), r == 0x061C, r == 0xFEFF,
			r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E,
			r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069:
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if rs := []rune(s); len(rs) > max {
		s = string(rs[:max-1]) + "…"
	}
	return s
}

// addressLister is the part of the address store the declined filters read.
type addressLister interface {
	ListByUser(userID string) ([]string, error)
}

// myAddresses returns the lower-cased addresses the user owns: the session
// address and every address of theirs in the address index (aliases and
// send-as identities included).
func (h *CalendarHandler) myAddresses(userID, email string) map[string]bool {
	mine := map[string]bool{}
	if e := store.NormaliseAddress(email); e != "" {
		mine[e] = true
	}
	if h.owned != nil {
		list, err := h.owned.ListByUser(userID)
		if err != nil {
			slog.Warn("calendar: listing the user's addresses failed", "user", userID, "error", err)
		}
		for _, a := range list {
			mine[store.NormaliseAddress(a)] = true
		}
	}
	return mine
}

// myPartstat returns the PARTSTAT of the first attendee of e that is one of
// mine, or "" when none is.
func myPartstat(e *bongov1.Event, mine map[string]bool) string {
	for _, a := range e.GetAttendees() {
		if mine[store.NormaliseAddress(a.Address)] {
			return a.Partstat
		}
	}
	return ""
}

// hasDeclined reports whether any attendee of e declined, which is the cheap
// test that decides whether the user's addresses are needed at all.
func hasDeclined(e *bongov1.Event) bool {
	for _, a := range e.GetAttendees() {
		if a.Partstat == "DECLINED" {
			return true
		}
	}
	return false
}

// hideDeclined drops the events the user declined from a list read from their
// own key space, for the day, week, month and agenda views.
func (h *CalendarHandler) hideDeclined(userID, email string, events []*bongov1.Event) []*bongov1.Event {
	need := false
	for _, e := range events {
		if hasDeclined(e) {
			need = true
			break
		}
	}
	if !need {
		return events
	}
	mine := h.myAddresses(userID, email)
	out := make([]*bongov1.Event, 0, len(events))
	for _, e := range events {
		if myPartstat(e, mine) != "DECLINED" {
			out = append(out, e)
		}
	}
	return out
}

// busyEvents drops what does not make a user busy: cancelled events, and
// events that user declined. email is the user's session address when known.
func (h *CalendarHandler) busyEvents(userID, email string, events []*bongov1.Event) []*bongov1.Event {
	// The user's addresses are only needed to recognise a declined answer, so
	// skip the address lookup when nobody declined.
	var mine map[string]bool
	for _, e := range events {
		if hasDeclined(e) {
			mine = h.myAddresses(userID, email)
			break
		}
	}
	out := make([]*bongov1.Event, 0, len(events))
	for _, e := range events {
		if e.Status == "CANCELLED" || myPartstat(e, mine) == "DECLINED" {
			continue
		}
		out = append(out, e)
	}
	return out
}

// inviteState is what the user's calendar and addresses say about one received
// message, and so which actions the card offers and the POST honours.
type inviteState struct {
	inv  *imip.Invite
	mine map[string]bool
	// me is the attendee entry that is one of the user's addresses.
	me *bongov1.Attendee
	// self is true when the user is the organiser, who is served from their
	// own calendar (a forged invitation naming the user as organiser must not
	// make later edits mail attacker-chosen attendees as the user).
	self bool
	// known is the event with this UID in the user's own calendars.
	known            *bongov1.Event
	organiserMatches bool
	// instanceOnly: known is itself a single override stored without a master.
	instanceOnly bool

	canAnswer, canUpdate, canCancel, canRemove bool
	status                                     string
	notes                                      []string
}

// assess parses the message and decides what may be done with it.
func (h *CalendarHandler) assess(msg inviteMessage) (*inviteState, error) {
	inv, err := imip.Parse(msg.Part, msg.CTMethod)
	if err != nil || (inv.Method != imip.MethodRequest && inv.Method != imip.MethodCancel) {
		return nil, errNotInvitation
	}
	st := &inviteState{inv: inv, mine: h.myAddresses(msg.UserID, msg.Email)}
	for _, a := range inv.Event.Attendees {
		if st.mine[store.NormaliseAddress(a.Address)] {
			st.me = a
			break
		}
	}
	st.self = st.mine[store.NormaliseAddress(inv.Organizer)]

	if st.known, err = h.findKnown(msg.UserID, inv.UID); err != nil {
		return nil, err
	}
	if st.known != nil {
		st.organiserMatches = strings.EqualFold(st.known.Organizer, inv.Organizer)
		st.instanceOnly = isInstanceOnly(st.known)
	}
	st.decide()
	return st, nil
}

// decide fills the capabilities, the status line and the notes.
func (st *inviteState) decide() {
	inv, known := st.inv, st.known
	if known != nil {
		switch myPartstat(known, st.mine) {
		case "ACCEPTED":
			st.status = "invite.statusAccepted"
		case "TENTATIVE":
			st.status = "invite.statusTentative"
		case "DECLINED":
			st.status = "invite.statusDeclined"
		}
		if known.Status == "CANCELLED" {
			st.status = "invite.statusCancelled"
		}
	}
	if st.self {
		st.status = ""
		st.notes = append(st.notes, "invite.noteSelfOrganised")
		return
	}
	if known != nil && !st.organiserMatches {
		st.status = ""
		st.notes = append(st.notes, "invite.noteOrganiserMismatch")
		return
	}
	if known != nil && st.instanceOnly && inv.RecurrenceID != "" && !sameInstance(known, inv.RecurrenceID) {
		st.notes = append(st.notes, "invite.noteUnsupported")
		return
	}
	cancelled := known != nil && known.Status == "CANCELLED"
	seriesLevel := inv.RecurrenceID == "" || st.instanceOnly

	switch inv.Method {
	case imip.MethodRequest:
		switch {
		case st.me == nil:
			st.notes = append(st.notes, "invite.noteNotInvited")
		case known == nil:
			st.canAnswer = true
		case inv.Sequence < int(known.Sequence):
			st.notes = append(st.notes, "invite.noteOutdated")
		case cancelled && inv.Sequence == int(known.Sequence):
			// The original request, resent after a cancellation.
		default:
			st.canAnswer = true
			if inv.Sequence > int(known.Sequence) && seriesLevel {
				st.canUpdate = true
				st.status = "invite.statusUpdate"
			}
		}
	case imip.MethodCancel:
		switch {
		case known == nil:
			st.notes = append(st.notes, "invite.noteNotInCalendar")
		case inv.Sequence < int(known.Sequence):
			st.notes = append(st.notes, "invite.noteOutdated")
		case seriesLevel && cancelled:
		case !seriesLevel && overrideCancelled(known, inv.RecurrenceID):
			st.notes = append(st.notes, "invite.noteAlreadyCancelled")
		default:
			st.canCancel = true
			st.notes = append(st.notes, "invite.noteCancelPending")
		}
	}
	// A cancelled event is removable through the normal delete as well; the
	// card offers the shortcut where the message is about the whole event.
	if cancelled && seriesLevel {
		st.canRemove = true
	}
}

// findKnown returns the event of the user with this UID, or nil. The derived
// guid is tried first; then the UID itself when it is a safe key, because
// Bongo-created events are named by their UID and CalDAV clients often name an
// object after it. Only the user's own key space is read, and a hit counts only
// when the event's own UID is the one asked for.
func (h *CalendarHandler) findKnown(userID, uid string) (*bongov1.Event, error) {
	guids := []string{uidGuid(uid)}
	if safeUIDKey.MatchString(uid) {
		guids = append(guids, uid)
	}
	for _, g := range guids {
		ev, err := h.events.Get(userID, g)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, fmt.Errorf("looking up event: %w", err)
		}
		if seriesUID(ev) == uid {
			return ev, nil
		}
	}
	return nil, nil
}

// isInstanceOnly reports whether the stored object has no master VEVENT: it is
// a single override received on its own.
func isInstanceOnly(e *bongov1.Event) bool {
	if len(e.IcalRaw) == 0 {
		return false
	}
	cal, err := storedCalendar(e)
	if err != nil {
		return false
	}
	m, fallback := icalevent.Master(cal)
	return m != nil && fallback
}

// sameInstance reports whether the override-only object e is the instance rid.
func sameInstance(e *bongov1.Event, rid string) bool {
	cal, err := storedCalendar(e)
	if err != nil {
		return false
	}
	m, _ := icalevent.Master(cal)
	if m == nil {
		return false
	}
	p := m.Props.Get(ical.PropRecurrenceID)
	if p == nil {
		return false
	}
	key, ok := imip.RecurrenceKey(p)
	return ok && key == rid
}

// overrideCancelled reports whether the series e already has a cancelled
// override at rid.
func overrideCancelled(e *bongov1.Event, rid string) bool {
	cal, err := storedCalendar(e)
	if err != nil {
		return false
	}
	for _, c := range cal.Children {
		if c.Name != ical.CompEvent {
			continue
		}
		p := c.Props.Get(ical.PropRecurrenceID)
		if p == nil {
			continue
		}
		if key, ok := imip.RecurrenceKey(p); ok && key == rid {
			st := c.Props.Get(ical.PropStatus)
			return st != nil && strings.EqualFold(strings.TrimSpace(st.Value), "CANCELLED")
		}
	}
	return false
}

// storedCalendar opens the stored object of e, or builds the one CalDAV would serve
// for an event without one.
func storedCalendar(e *bongov1.Event) (*ical.Calendar, error) {
	if len(e.IcalRaw) == 0 {
		return icalevent.Synthesise(e), nil
	}
	return ical.NewDecoder(bytes.NewReader(e.IcalRaw)).Decode()
}

// encodeCalendar serialises cal for ical_raw.
func encodeCalendar(cal *ical.Calendar) ([]byte, error) {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		return nil, fmt.Errorf("encoding ical: %w", err)
	}
	return buf.Bytes(), nil
}

// InvitationCard builds the card for a message, or nil when the part is not an
// invitation the user can be shown. It reads and never writes.
func (h *CalendarHandler) InvitationCard(r *http.Request, msg inviteMessage) *templates.InvitationCard {
	st, err := h.assess(msg)
	if err != nil {
		if !errors.Is(err, errNotInvitation) {
			slog.Warn("calendar: reading an invitation failed", "user", msg.UserID, "error", err)
		}
		return nil
	}
	inv, ev := st.inv, st.inv.Event
	loc := h.userLocation(msg.UserID)

	c := &templates.InvitationCard{
		Mailbox:              msg.Mailbox,
		UID:                  msg.UID,
		Method:               inv.Method,
		Title:                cleanDisplay(ev.Title, maxCardTitle, false),
		When:                 imip.When(ev, loc),
		Where:                cleanDisplay(ev.Location, maxCardWhere, false),
		Description:          cleanDisplay(ev.Description, maxCardDesc, true),
		Organiser:            cleanDisplay(inv.Organizer, maxCardWhere, false),
		DMARCFailed:          imip.DMARCResult(msg.AuthResults, h.authservID) == "fail",
		Status:               st.status,
		Notes:                st.notes,
		CanAnswer:            st.canAnswer,
		CanUpdate:            st.canUpdate,
		CanMarkCancelled:     st.canCancel,
		CanRemove:            st.canRemove,
		ReplyFailed:          r != nil && r.URL.Query().Get("ir") == "failed",
		OrganiserDiffersFrom: "",
	}
	if from := cleanDisplay(msg.From, maxCardWhere, false); from != "" && !strings.EqualFold(from, inv.Organizer) {
		c.OrganiserDiffersFrom = from
	}
	for i, a := range ev.Attendees {
		if i >= maxCardAttendees {
			c.MoreAttendees = len(ev.Attendees) - maxCardAttendees
			break
		}
		c.Attendees = append(c.Attendees, templates.InvitationAttendee{
			Name:     cleanDisplay(a.Name, maxCardName, false),
			Address:  cleanDisplay(a.Address, maxCardWhere, false),
			Partstat: a.Partstat,
		})
	}

	if inv.Method == imip.MethodRequest {
		c.ConflictsChecked = true
		c.Conflicts = h.inviteConflicts(msg.UserID, st, loc)
		c.FirstInstanceOnly = ev.Rrule != "" && inv.RecurrenceID == ""
	}
	if st.canAnswer && st.known == nil {
		cals, err := h.calendars.List(msg.UserID)
		if err != nil {
			slog.Warn("calendar: listing calendars for an invitation failed", "user", msg.UserID, "error", err)
		}
		for i, cal := range cals {
			c.Calendars = append(c.Calendars, templates.InvitationCalendar{
				ID: cal.Guid, Name: cleanDisplay(cal.Name, maxCardName, false), Selected: i == 0,
			})
		}
	}
	return c
}

// inviteConflicts lists the user's events that overlap the first instance of
// the invitation (or the instance it is about). The event itself, cancelled
// events and events the user declined do not count.
func (h *CalendarHandler) inviteConflicts(userID string, st *inviteState, loc *time.Location) []templates.InvitationConflict {
	ev := st.inv.Event
	start, end := ev.StartTime, ev.EndTime
	if end <= start {
		end = start + 3600
	}
	events, err := h.events.ListByDateRange(userID, start, end, "")
	if err != nil {
		slog.Warn("calendar: conflict check failed", "user", userID, "error", err)
		return nil
	}
	own := uidGuid(st.inv.UID)
	var out []templates.InvitationConflict
	for _, e := range events {
		if e.Guid == own || e.Status == "CANCELLED" || myPartstat(e, st.mine) == "DECLINED" || seriesUID(e) == st.inv.UID {
			continue
		}
		out = append(out, templates.InvitationConflict{
			Title: cleanDisplay(e.Title, maxCardTitle, false),
			When:  imip.When(e, loc),
		})
		if len(out) >= maxCardConflicts {
			break
		}
	}
	return out
}

// ApplyInvitation carries out one action of the card. It trusts nothing of the
// request but the action: the invitation is parsed again from the message and
// the action must be one the card would have offered.
func (h *CalendarHandler) ApplyInvitation(r *http.Request, msg inviteMessage, act inviteAction) inviteResult {
	st, err := h.assess(msg)
	if err != nil {
		if errors.Is(err, errNotInvitation) {
			return inviteResult{Status: http.StatusUnprocessableEntity, Message: "not an invitation"}
		}
		slog.Warn("calendar: reading an invitation failed", "user", msg.UserID, "error", err)
		return inviteResult{Status: http.StatusInternalServerError, Message: "failed to read the invitation"}
	}
	refuse := inviteResult{Status: http.StatusConflict, Message: "this action is not available for this invitation"}

	switch act.Action {
	case "answer":
		switch act.Partstat {
		case "ACCEPTED", "TENTATIVE", "DECLINED":
		default:
			return inviteResult{Status: http.StatusBadRequest, Message: "invalid answer"}
		}
		if !st.canAnswer {
			return refuse
		}
		return h.answerInvitation(r, msg, st, act)
	case "update":
		if !st.canUpdate {
			return refuse
		}
		return h.updateInvitation(msg, st)
	case "cancel":
		if !st.canCancel {
			return refuse
		}
		return h.cancelInvitation(msg, st)
	case "remove":
		if !st.canRemove {
			return refuse
		}
		// Nothing is sent: the organiser is someone else, and no attendee of
		// theirs is ours to notify.
		if err := h.events.Delete(msg.UserID, st.known.Guid); err != nil {
			slog.Warn("calendar: removing a cancelled event failed", "user", msg.UserID, "error", err)
			return inviteResult{Status: http.StatusInternalServerError, Message: "failed to remove event"}
		}
		return inviteResult{}
	}
	return inviteResult{Status: http.StatusBadRequest, Message: "unknown action"}
}

// ownCalendar resolves the calendar an answer is filed in: a calendar of the
// user's own, or their default when none is named.
func (h *CalendarHandler) ownCalendar(userID, id string) (string, inviteResult) {
	if id == "" {
		guid, err := h.calendars.EnsureDefault(userID)
		if err != nil {
			slog.Warn("calendar: default calendar failed", "user", userID, "error", err)
			return "", inviteResult{Status: http.StatusInternalServerError, Message: "failed to find a calendar"}
		}
		return guid, inviteResult{}
	}
	if _, err := h.calendars.Get(userID, id); err != nil {
		return "", inviteResult{Status: http.StatusNotFound, Message: "calendar not found"}
	}
	return id, inviteResult{}
}

// answerInvitation files the event with the user's PARTSTAT and then replies to
// the organiser. The event is written first: a failed write fails the request
// and sends nothing, while a failed reply is reported and keeps the event.
func (h *CalendarHandler) answerInvitation(r *http.Request, msg inviteMessage, st *inviteState, act inviteAction) inviteResult {
	userID := msg.UserID
	// A known event stays in its calendar, so the calendar_id of the form is
	// only looked up (and validated) when the event is new.
	calID := ""
	if st.known == nil {
		var res inviteResult
		if calID, res = h.ownCalendar(userID, act.CalendarID); res.Status != 0 {
			return res
		}
	}

	var ev *bongov1.Event
	var err error
	if st.known == nil {
		ev, err = h.newInviteEvent(st, calID, act.Partstat)
	} else {
		ev, err = h.mergeInvite(st, act.Partstat)
	}
	if err != nil {
		var ie *instanceError
		if errors.As(err, &ie) {
			return inviteResult{Status: http.StatusConflict, Message: ie.msg}
		}
		slog.Warn("calendar: building the event from an invitation failed", "user", userID, "error", err)
		return inviteResult{Status: http.StatusInternalServerError, Message: "failed to apply the invitation"}
	}
	if res := h.storeInvited(userID, st.known, ev); res.Status != 0 {
		return res
	}
	return inviteResult{ReplyFailed: !h.sendReply(r, msg, st, act.Partstat)}
}

// updateInvitation applies a higher-SEQUENCE REQUEST: the user's PARTSTAT is
// kept where the time is unchanged and goes back to NEEDS-ACTION where it
// changed.
func (h *CalendarHandler) updateInvitation(msg inviteMessage, st *inviteState) inviteResult {
	ps := myPartstat(st.known, st.mine)
	if ps == "" || timeChanged(st.known, st.inv.Event) {
		ps = icalevent.PartStatNeedsAction
	}
	ev, err := h.replaceSeries(st, ps)
	if err != nil {
		slog.Warn("calendar: applying an updated invitation failed", "user", msg.UserID, "error", err)
		return inviteResult{Status: http.StatusInternalServerError, Message: "failed to apply the invitation"}
	}
	return h.storeInvited(msg.UserID, st.known, ev)
}

// timeChanged reports whether the update moves the event or changes how it
// repeats.
func timeChanged(old, updated *bongov1.Event) bool {
	return old.StartTime != updated.StartTime || old.EndTime != updated.EndTime ||
		old.AllDay != updated.AllDay || old.Rrule != updated.Rrule
}

// cancelInvitation marks the event, or one instance of it, cancelled.
func (h *CalendarHandler) cancelInvitation(msg inviteMessage, st *inviteState) inviteResult {
	ev, err := h.markCancelled(st)
	if err != nil {
		var ie *instanceError
		if errors.As(err, &ie) {
			return inviteResult{Status: http.StatusConflict, Message: ie.msg}
		}
		slog.Warn("calendar: applying a cancellation failed", "user", msg.UserID, "error", err)
		return inviteResult{Status: http.StatusInternalServerError, Message: "failed to apply the cancellation"}
	}
	return h.storeInvited(msg.UserID, st.known, ev)
}

// storeInvited writes ev: a create when old is nil, else an update. The domain
// quota is charged for new events and for growth, as event create is.
func (h *CalendarHandler) storeInvited(userID string, old, ev *bongov1.Event) inviteResult {
	grow := store.EventRecordSize(ev)
	if old != nil {
		grow -= store.EventRecordSize(old)
	}
	if grow > 0 && quotaExceeded(h.quota, nil, userID, grow) {
		return inviteResult{Status: http.StatusInsufficientStorage, Message: "storage quota exceeded"}
	}
	var err error
	if old == nil {
		_, err = h.events.Create(userID, ev)
	} else {
		err = h.events.Update(userID, ev.Guid, ev)
	}
	if err != nil {
		slog.Warn("calendar: saving an invited event failed", "user", userID, "error", err)
		return inviteResult{Status: http.StatusInternalServerError, Message: "failed to save the event"}
	}
	return inviteResult{}
}

// setAttendeePartstat records the user's answer on the structured attendees.
func setAttendeePartstat(ev *bongov1.Event, mine map[string]bool, partstat string) {
	for _, a := range ev.Attendees {
		if mine[store.NormaliseAddress(a.Address)] {
			a.Partstat = partstat
			a.Rsvp = false
		}
	}
}

// newInviteEvent builds the event for an invitation the calendar does not hold.
// A lone instance is stored as its own event.
func (h *CalendarHandler) newInviteEvent(st *inviteState, calID, partstat string) (*bongov1.Event, error) {
	inv := st.inv
	raw, err := inv.Stored(st.me.Address, partstat)
	if err != nil {
		return nil, err
	}
	ev := proto.Clone(inv.Event).(*bongov1.Event)
	ev.Guid = uidGuid(inv.UID)
	ev.CalendarId = calID
	ev.IcalRaw = raw
	setAttendeePartstat(ev, st.mine, partstat)
	return ev, nil
}

// mergeInvite applies an answer to an event the calendar holds.
func (h *CalendarHandler) mergeInvite(st *inviteState, partstat string) (*bongov1.Event, error) {
	inv, known := st.inv, st.known
	seriesLevel := inv.RecurrenceID == "" || st.instanceOnly
	switch {
	case seriesLevel && inv.Sequence > int(known.Sequence):
		return h.replaceSeries(st, partstat)
	case seriesLevel:
		// The request already in the calendar, answered again: only the answer
		// changes.
		cal, err := storedCalendar(known)
		if err != nil {
			return nil, err
		}
		for _, c := range cal.Children {
			if c.Name == ical.CompEvent {
				imip.SetPartstat(c, st.me.Address, partstat)
			}
		}
		ev := proto.Clone(known).(*bongov1.Event)
		if ev.IcalRaw, err = encodeCalendar(cal); err != nil {
			return nil, err
		}
		setAttendeePartstat(ev, st.mine, partstat)
		return ev, nil
	}
	return h.mergeInstance(st, func(ve *ical.Component) {
		imip.SetPartstat(ve, st.me.Address, partstat)
	})
}

// replaceSeries swaps the stored event for the received one, with the user's
// PARTSTAT set to partstat. The identity, calendar and creation time stay.
func (h *CalendarHandler) replaceSeries(st *inviteState, partstat string) (*bongov1.Event, error) {
	inv, known := st.inv, st.known
	raw, err := inv.Stored(st.me.Address, partstat)
	if err != nil {
		return nil, err
	}
	ev := proto.Clone(known).(*bongov1.Event)
	n := proto.Clone(inv.Event).(*bongov1.Event)
	ev.Title, ev.Description, ev.Location = n.Title, n.Description, n.Location
	ev.StartTime, ev.EndTime, ev.AllDay = n.StartTime, n.EndTime, n.AllDay
	ev.Rrule, ev.Timezone, ev.Exdates = n.Rrule, n.Timezone, nil
	ev.Organizer, ev.Attendees, ev.Sequence, ev.Status = n.Organizer, n.Attendees, n.Sequence, n.Status
	ev.IcalRaw = raw
	setAttendeePartstat(ev, st.mine, partstat)
	return ev, nil
}

// mergeInstance puts the received instance into the stored series as an
// override, after edit has adjusted the VEVENT, and returns the updated event.
func (h *CalendarHandler) mergeInstance(st *inviteState, edit func(*ical.Component)) (*bongov1.Event, error) {
	inv, known := st.inv, st.known
	_, slot, err := resolveSlot(known, inv.RecurrenceID)
	if err != nil {
		return nil, err
	}
	s, err := seriesForEdit(known)
	if err != nil {
		return nil, err
	}
	var override *ical.Component
	for _, c := range inv.Events {
		if p := c.Props.Get(ical.PropRecurrenceID); p != nil {
			if key, ok := imip.RecurrenceKey(p); ok && key == inv.RecurrenceID {
				override = c
				break
			}
		}
	}
	if override == nil {
		return nil, newInstanceError(http.StatusConflict, "the message has no such instance")
	}
	imip.StripAlarms(override)
	edit(override)

	if i, _ := s.overrideAt(slot); i >= 0 {
		s.cal.Children[i] = override
	} else {
		s.cal.Children = append(s.cal.Children, override)
	}
	addMissingZones(s.cal, inv.Zones)

	ev := proto.Clone(known).(*bongov1.Event)
	if ev.IcalRaw, err = s.encode(); err != nil {
		return nil, err
	}
	return ev, nil
}

// addMissingZones appends the VTIMEZONEs of zones that cal does not have.
func addMissingZones(cal *ical.Calendar, zones []*ical.Component) {
	have := map[string]bool{}
	for _, c := range cal.Children {
		if c.Name == ical.CompTimezone {
			if p := c.Props.Get(ical.PropTimezoneID); p != nil {
				have[p.Value] = true
			}
		}
	}
	for _, z := range zones {
		if p := z.Props.Get(ical.PropTimezoneID); p != nil && !have[p.Value] {
			have[p.Value] = true
			cal.Children = append(cal.Children, z)
		}
	}
}

// markCancelled returns the event with the cancellation applied.
func (h *CalendarHandler) markCancelled(st *inviteState) (*bongov1.Event, error) {
	inv, known := st.inv, st.known
	if inv.RecurrenceID != "" && !st.instanceOnly {
		return h.mergeInstance(st, func(ve *ical.Component) {
			ve.Props.SetText(ical.PropStatus, "CANCELLED")
		})
	}
	cal, err := storedCalendar(known)
	if err != nil {
		return nil, err
	}
	master, _ := icalevent.Master(cal)
	if master == nil {
		return nil, errors.New("stored event has no VEVENT")
	}
	master.Props.SetText(ical.PropStatus, "CANCELLED")
	seq := ical.NewProp(ical.PropSequence)
	seq.Value = fmt.Sprint(inv.Sequence)
	master.Props.Set(seq)

	ev := proto.Clone(known).(*bongov1.Event)
	if ev.IcalRaw, err = encodeCalendar(cal); err != nil {
		return nil, err
	}
	ev.Status = "CANCELLED"
	ev.Sequence = inv.Event.Sequence
	return ev, nil
}

// sendReply sends the iTIP REPLY to the organiser, as the attendee address the
// invitation named. It is best-effort: false means the organiser was not told.
func (h *CalendarHandler) sendReply(r *http.Request, msg inviteMessage, st *inviteState, partstat string) bool {
	if h.invites == nil || st.me == nil {
		return false
	}
	inv := st.inv
	me := st.me.Address
	if !icalevent.ValidAddress(inv.Organizer) || !h.maySendAs(msg.UserID, msg.Email, me) {
		slog.Warn("calendar: not replying to an invitation, the address cannot be sent as", "user", msg.UserID)
		return false
	}
	raw, err := inv.Stored(me, partstat)
	if err != nil {
		return false
	}
	reply := proto.Clone(inv.Event).(*bongov1.Event)
	reply.Guid = ""
	reply.IcalRaw = raw
	reply.Organizer = inv.Organizer
	reply.Sequence = inv.Event.Sequence
	reply.Attendees = []*bongov1.Attendee{{Address: me, Name: st.me.Name, Partstat: partstat}}

	now := h.nowIn(msg.UserID)
	locale := middleware.LocaleFromContext(r.Context())
	cal, err := imip.BuildCalendar(imip.MethodReply, reply, inv.RecurrenceID, now)
	if err != nil {
		slog.Warn("calendar: building a reply failed", "user", msg.UserID, "error", err)
		return false
	}
	subject, body := imip.Describe(imip.MethodReply, false, reply, h.userLocation(msg.UserID), func(id string) string { return i18n.T(locale, id) })
	mime, err := imip.BuildMessage(imip.Params{
		Method: imip.MethodReply, From: me, To: []string{inv.Organizer}, Subject: subject, TextBody: body, Cal: cal, Date: now,
	})
	if err != nil {
		slog.Warn("calendar: building a reply message failed", "user", msg.UserID, "error", err)
		return false
	}
	missed, err := h.invites.DeliverGenerated(msg.UserID, msg.Email, me, []string{inv.Organizer}, mime)
	if err != nil || len(missed) > 0 {
		slog.Warn("calendar: sending a reply failed", "user", msg.UserID, "not_reached", len(missed), "error", err)
		return false
	}
	return true
}
