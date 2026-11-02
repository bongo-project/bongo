package caldav

import (
	"bytes"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav/caldav"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Implicit scheduling (RFC 6638): when the backend stores or deletes an event,
// it sends the iMIP messages the change calls for, through the same paths as
// the web UI, so a CalDAV client that sees "calendar-auto-schedule" in the DAV
// header does not send them itself.
//
// Order and failure semantics: the object is saved first, then the messages go
// out, then SCHEDULE-STATUS is written into the stored object with a second
// write. Those are separate Raft commands and not atomic. A failed send is
// reported in SCHEDULE-STATUS (5.1) and never undoes the save; a failed status
// write is logged, leaves the object as the client sent it and is not retried
// (the next change to the event writes it). A PUT that changes nothing an
// invitation tells sends nothing, so a client's habit of uploading the same
// event again, with the status the server wrote, spends no recipient budget.

// Scheduler sends and answers invitations on behalf of a user. The
// implementation lives with the web handlers, whose send-as, SEQUENCE and rate
// limit rules it reuses; the backend only decides when to call it.
type Scheduler interface {
	// Addresses is the user's calendar-user-address-set: the primary address
	// and verified send-as identities, lower-cased.
	Addresses(userID string) []string
	// Owns reports whether addr is any address of the user, aliases included.
	Owns(userID, addr string) bool
	// Organise sends the REQUEST and CANCEL messages that follow a saved
	// change; before is nil for a create and after nil for a delete. Addresses
	// in skip are sent nothing. It returns SCHEDULE-STATUS by lower-cased
	// address for every address it attempted.
	Organise(userID string, before, after *bongov1.Event, skip map[string]bool) map[string]string
	// Reply sends the REPLY for attendee's answer; recurrenceID, when not
	// empty, makes it an answer for one instance. It reports whether the
	// organiser was told.
	Reply(userID string, event *bongov1.Event, attendee, partstat, recurrenceID string) bool
	// FreeBusy returns the busy periods ([start, end], unix seconds) of the
	// local user who owns address, for requester. ok is false for a
	// non-local address and for one requester may not see, alike.
	FreeBusy(requester, address string, from, to int64) (busy [][2]int64, ok bool)
}

// ScheduleInbox is the part of the schedule inbox store the CalDAV side reads
// and trims; *store.ScheduleInboxStore satisfies it.
type ScheduleInbox interface {
	List(userID string) ([]store.ScheduleItem, error)
	Get(userID, name string) (*store.ScheduleItem, error)
	Delete(userID, name string) error
}

// SetScheduler enables implicit scheduling and the scheduling collections.
// Without it the server advertises none of RFC 6638 and clients keep sending
// iMIP themselves.
func (b *Backend) SetScheduler(s Scheduler) { b.scheduler = s }

// HasScheduler reports whether implicit scheduling is enabled.
func (b *Backend) HasScheduler() bool { return b.scheduler != nil }

// SetScheduleInbox wires the schedule inbox collection.
func (b *Backend) SetScheduleInbox(i ScheduleInbox) { b.inbox = i }

// Preconditions of RFC 6638 section 3.2, and the names the scheduling code
// uses for iCalendar values.
const (
	preconditionValidOrganizer caldav.PreconditionType = "valid-organizer"
	preconditionMaxAttendees   caldav.PreconditionType = "max-attendees-per-instance"

	// maxReplyChangesPerPut bounds the REPLYs one PUT can send: an attendee
	// answering a long series occurrence by occurrence changes many overrides
	// at once, and the recipient budget is the organiser's to spend, not theirs.
	maxReplyChangesPerPut = 20

	scheduleAgentServer          = "SERVER"
	scheduleStatusSent           = "1.1"
	scheduleStatusInvalidAddress = "3.7"
	paramScheduleAgent           = "SCHEDULE-AGENT"
	paramScheduleStatus          = "SCHEDULE-STATUS"
	partstatNeedsAction          = "NEEDS-ACTION"
)

// How a PUT takes part in scheduling; the zero value is not at all.
const (
	scheduleModeOrganiser = iota + 1
	scheduleModeAttendee
)

// scheduleAttendee is one ATTENDEE property of the master VEVENT.
type scheduleAttendee struct {
	prop    int    // index into the ATTENDEE property list
	address string // lower-cased bare address; "" when not a valid mailto
	agent   string // SCHEDULE-AGENT, upper case, SERVER when absent
}

func scheduleAgent(p *ical.Prop) string {
	a := strings.ToUpper(strings.TrimSpace(p.Params.Get(paramScheduleAgent)))
	if a == "" {
		return scheduleAgentServer
	}
	return a
}

func scheduleAttendees(ve *ical.Component) []scheduleAttendee {
	if ve == nil {
		return nil
	}
	props := ve.Props.Values(ical.PropAttendee)
	out := make([]scheduleAttendee, 0, len(props))
	for i := range props {
		a := scheduleAttendee{prop: i, agent: scheduleAgent(&props[i])}
		if addr, ok := icalevent.AddressFromCalAddress(props[i].Value); ok {
			a.address = store.NormaliseAddress(addr)
		}
		out = append(out, a)
	}
	return out
}

// masterOf returns the master VEVENT of cal, or nil.
func masterOf(cal *ical.Calendar) *ical.Component {
	if cal == nil {
		return nil
	}
	m, _ := icalevent.Master(cal)
	return m
}

func decodeRaw(raw []byte) *ical.Calendar {
	if len(raw) == 0 {
		return nil
	}
	cal, err := ical.NewDecoder(bytes.NewReader(raw)).Decode()
	if err != nil {
		return nil
	}
	return cal
}

// skipAddresses are the attendees of the VEVENTs a server must not message:
// those whose SCHEDULE-AGENT is CLIENT or NONE (RFC 6638 section 7.1).
func skipAddresses(cals ...*ical.Calendar) map[string]bool {
	skip := map[string]bool{}
	for _, cal := range cals {
		for _, a := range scheduleAttendees(masterOf(cal)) {
			if a.address != "" && a.agent != scheduleAgentServer {
				skip[a.address] = true
			}
		}
	}
	return skip
}

// schedulePlan is what a PUT needs done after it is saved.
type schedulePlan struct {
	mode int
	// me is the address the user answers as, in attendee mode.
	me string
}

// planSchedule decides how a PUT of calendar takes part in scheduling. It
// returns a 403 only when the PUT would schedule as organiser without the
// user being allowed to: a create, or a change of ORGANIZER, naming an
// organiser outside the user's calendar-user-address-set, with attendees to
// message and the user not among them. Everything else is stored; at most
// nothing is sent.
func (b *Backend) planSchedule(userID string, calendar *ical.Calendar, event, existing *bongov1.Event) (schedulePlan, error) {
	master := masterOf(calendar)
	atts := scheduleAttendees(master)
	if b.scheduler == nil || master == nil || event.Organizer == "" || len(atts) == 0 {
		return schedulePlan{}, nil
	}
	org := store.NormaliseAddress(event.Organizer)
	if slices.Contains(b.scheduler.Addresses(userID), org) {
		if len(atts) > icalevent.MaxAttendees {
			return schedulePlan{}, preconditionRefusal(preconditionMaxAttendees)
		}
		return schedulePlan{mode: scheduleModeOrganiser}, nil
	}
	for _, a := range atts {
		if a.address != "" && b.scheduler.Owns(userID, a.address) {
			return schedulePlan{mode: scheduleModeAttendee, me: a.address}, nil
		}
	}
	// A foreign organiser and the user is not invited: the event is only
	// stored. The one thing refused is a PUT that would make the server
	// schedule in someone else's name, which is an organiser the user may not
	// send as appearing on a create or replacing the stored one.
	changed := existing == nil || store.NormaliseAddress(existing.Organizer) != org
	if changed {
		for _, a := range atts {
			if a.agent == scheduleAgentServer {
				return schedulePlan{}, preconditionRefusal(preconditionValidOrganizer)
			}
		}
	}
	return schedulePlan{}, nil
}

// scheduleAfterSave runs the scheduling that follows a saved PUT and returns
// the event as it is stored in the end (SCHEDULE-STATUS may have been added).
func (b *Backend) scheduleAfterSave(userID string, plan schedulePlan, calendar *ical.Calendar, saved, existing *bongov1.Event) *bongov1.Event {
	switch plan.mode {
	case scheduleModeOrganiser:
		return b.organise(userID, calendar, saved, existing)
	case scheduleModeAttendee:
		b.answer(userID, plan.me, calendar, saved, existing)
	}
	return saved
}

// organise messages the attendees and records the outcome per ATTENDEE.
func (b *Backend) organise(userID string, calendar *ical.Calendar, saved, existing *bongov1.Event) *bongov1.Event {
	skip := skipAddresses(calendar)
	if existing != nil {
		skip = mergeSets(skip, skipAddresses(decodeRaw(existing.IcalRaw)))
	}
	statuses := b.scheduler.Organise(userID, existing, saved, skip)

	cal := decodeRaw(saved.IcalRaw)
	master := masterOf(cal)
	if master == nil || !recordScheduleStatus(master, statuses) {
		return saved
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		b.logger.Warn("caldav: encoding the scheduling status failed", "user", userID, "guid", saved.Guid, "error", err)
		return saved
	}
	updated := proto.Clone(saved).(*bongov1.Event)
	updated.IcalRaw = buf.Bytes()
	updated.UpdatedAt = time.Now().Unix()
	if err := b.events.Update(userID, saved.Guid, updated); err != nil {
		// Best-effort: the invitations are sent and the event is saved; only
		// the status the client would show is missing.
		b.logger.Warn("caldav: writing the scheduling status failed", "user", userID, "guid", saved.Guid, "error", err)
		return saved
	}
	return updated
}

func mergeSets(a, b map[string]bool) map[string]bool {
	for k := range b {
		a[k] = true
	}
	return a
}

// recordScheduleStatus sets SCHEDULE-STATUS on the ATTENDEEs of master that the
// server handled: the status in statuses for an address it messaged, 3.7 for
// an ATTENDEE that is not a usable address. An ATTENDEE with another
// SCHEDULE-AGENT than SERVER is left alone. It reports whether anything
// changed.
func recordScheduleStatus(master *ical.Component, statuses map[string]string) bool {
	props := master.Props[ical.PropAttendee]
	changed := false
	for i, a := range scheduleAttendees(master) {
		if a.agent != scheduleAgentServer {
			continue
		}
		want := scheduleStatusInvalidAddress
		if a.address != "" {
			s, ok := statuses[a.address]
			if !ok {
				continue
			}
			want = s
		}
		if props[i].Params.Get(paramScheduleStatus) != want {
			props[i].Params.Set(paramScheduleStatus, want)
			changed = true
		}
	}
	return changed
}

// partstatChange is an answer the user gave in a PUT.
type partstatChange struct {
	recurrenceID string
	partstat     string
}

// answer sends the REPLY for each answer the PUT gives for the user's own
// ATTENDEE. A create counts too: a client that accepts an invitation from mail
// creates the event with PARTSTAT already set, and since the server advertises
// calendar-auto-schedule the client sends no iMIP itself. The send-as and
// recipient-budget checks are the sender's, as for a re-PUT.
func (b *Backend) answer(userID, me string, calendar *ical.Calendar, saved, existing *bongov1.Event) {
	master := masterOf(calendar)
	if master == nil || skipOrganiser(master) {
		return
	}
	var before *ical.Calendar
	if existing != nil {
		before = decodeRaw(existing.IcalRaw)
	}
	changes := partstatChanges(before, calendar, me)
	if len(changes) > maxReplyChangesPerPut {
		changes = changes[:maxReplyChangesPerPut]
	}
	for _, c := range changes {
		if !b.scheduler.Reply(userID, saved, me, c.partstat, c.recurrenceID) {
			b.logger.Warn("caldav: reply to the organiser not sent", "user", userID, "guid", saved.Guid)
		}
	}
}

// skipOrganiser reports whether the ORGANIZER asks to be handled by the client.
func skipOrganiser(master *ical.Component) bool {
	p := master.Props.Get(ical.PropOrganizer)
	return p != nil && scheduleAgent(p) != scheduleAgentServer
}

func uidOf(ve *ical.Component) string {
	if p := ve.Props.Get(ical.PropUID); p != nil {
		return strings.TrimSpace(p.Value)
	}
	return ""
}

// partstatChanges lists the answers after gives for the attendee me that
// before (nil for a create) does not: the master's and each override's. An
// override without a counterpart before is compared with the master, which it
// inherits from. Only ACCEPTED, TENTATIVE and DECLINED are answers.
func partstatChanges(before, after *ical.Calendar, me string) []partstatChange {
	prior := map[string]string{}
	if before != nil {
		for _, ve := range before.Children {
			if ve.Name == ical.CompEvent {
				if ps, ok := partstatOf(ve, me); ok {
					prior[recurrenceKey(ve)] = ps
				}
			}
		}
	}
	var out []partstatChange
	for _, ve := range after.Children {
		if ve.Name != ical.CompEvent {
			continue
		}
		ps, ok := partstatOf(ve, me)
		if !ok || (ps != "ACCEPTED" && ps != "TENTATIVE" && ps != "DECLINED") {
			continue
		}
		key := recurrenceKey(ve)
		was, had := prior[key]
		if !had {
			was, had = prior[""]
		}
		if !had {
			was = partstatNeedsAction
		}
		if was != ps {
			out = append(out, partstatChange{recurrenceID: key, partstat: ps})
		}
	}
	return out
}

// recurrenceKey is the RECURRENCE-ID of a VEVENT in the form BuildCalendar
// takes, or "" for a master.
func recurrenceKey(ve *ical.Component) string {
	p := ve.Props.Get(ical.PropRecurrenceID)
	if p == nil {
		return ""
	}
	key, _ := imip.RecurrenceKey(p)
	return key
}

// partstatOf returns the PARTSTAT of the ATTENDEE me in ve.
func partstatOf(ve *ical.Component, me string) (string, bool) {
	for _, p := range ve.Props.Values(ical.PropAttendee) {
		addr, ok := icalevent.AddressFromCalAddress(p.Value)
		if !ok || store.NormaliseAddress(addr) != me {
			continue
		}
		ps := strings.ToUpper(strings.TrimSpace(p.Params.Get(ical.ParamParticipationStatus)))
		if ps == "" {
			ps = partstatNeedsAction
		}
		return ps, true
	}
	return "", false
}

// cancelAfterDelete sends the CANCEL for a deleted event the user organised.
func (b *Backend) cancelAfterDelete(userID string, existing *bongov1.Event) {
	if b.scheduler == nil || existing == nil || existing.Organizer == "" || len(existing.Attendees) == 0 {
		return
	}
	if !slices.Contains(b.scheduler.Addresses(userID), store.NormaliseAddress(existing.Organizer)) {
		return
	}
	statuses := b.scheduler.Organise(userID, existing, nil, skipAddresses(decodeRaw(existing.IcalRaw)))
	notDelivered := 0
	for _, st := range statuses {
		if st != scheduleStatusSent {
			notDelivered++
		}
	}
	if notDelivered > 0 {
		b.logger.Warn("caldav: a cancellation was not delivered to every attendee",
			"user", userID, "guid", existing.Guid, "not_delivered", notDelivered)
	}
}
