package caldav

import (
	"bytes"
	"fmt"
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
	// Location is the user's time zone, which decides when an all-day event
	// is over.
	Location(userID string) *time.Location
	// Organise sends the REQUEST and CANCEL messages that follow a saved
	// change; before is nil for a create and after nil for a delete. Addresses
	// in skip are sent nothing. It returns SCHEDULE-STATUS by lower-cased
	// address for every address it attempted.
	Organise(userID string, before, after *bongov1.Event, skip map[string]bool) map[string]string
	// OrganiseOccurrence sends the message about one occurrence of a series
	// after a saved change to its exceptions: a REQUEST with RECURRENCE-ID rid
	// for instance, or a CANCEL when instance is nil. original is the
	// occurrence as attendees last saw it (nil when there was none), before
	// the series as stored earlier (nil for a create) and after as saved. It
	// returns SCHEDULE-STATUS by lower-cased address, as Organise does.
	OrganiseOccurrence(userID string, before, after *bongov1.Event, rid string, original, instance *bongov1.Event, skip map[string]bool) map[string]string
	// Reply sends the REPLY for attendee's answer; recurrenceID, when not
	// empty, makes it an answer for one instance. It returns the
	// SCHEDULE-STATUS of the answer: 1.1 when the organiser was told, 3.8
	// when attendee is an address the user cannot send as, 5.1 when the REPLY
	// could not be built or delivered.
	Reply(userID string, event *bongov1.Event, attendee, partstat, recurrenceID string) string
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
	scheduleStatusNoAuthority    = "3.8"
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

// eventsOf returns every VEVENT of cal: the master and the overrides.
func eventsOf(cal *ical.Calendar) []*ical.Component {
	if cal == nil {
		return nil
	}
	var out []*ical.Component
	for _, c := range cal.Children {
		if c.Name == ical.CompEvent {
			out = append(out, c)
		}
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
		for _, ve := range eventsOf(cal) {
			for _, a := range scheduleAttendees(ve) {
				if a.address != "" && a.agent != scheduleAgentServer {
					skip[a.address] = true
				}
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
	// An override lists its own attendees, so the series takes part in
	// scheduling when any VEVENT has one.
	var atts []scheduleAttendee
	perEvent := make([][]scheduleAttendee, 0, 1)
	for _, ve := range eventsOf(calendar) {
		list := scheduleAttendees(ve)
		perEvent = append(perEvent, list)
		atts = append(atts, list...)
	}
	if b.scheduler == nil || master == nil || event.Organizer == "" || len(atts) == 0 {
		return schedulePlan{}, nil
	}
	org := store.NormaliseAddress(event.Organizer)
	if slices.Contains(b.scheduler.Addresses(userID), org) {
		// The limit is per VEVENT (RFC 6638 max-attendees-per-instance).
		for _, list := range perEvent {
			if b.scheduledCount(userID, org, list) > icalevent.MaxAttendees {
				return schedulePlan{}, preconditionRefusal(preconditionMaxAttendees)
			}
		}
		return schedulePlan{mode: scheduleModeOrganiser}, nil
	}
	if me := b.answeringAddress(userID, atts); me != "" {
		return schedulePlan{mode: scheduleModeAttendee, me: me}, nil
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

// answeringAddress is the address the user answers an invitation as. The user
// may be invited at several of their addresses: answer as one they may send as,
// which the organiser can be sent a REPLY from; a plain alias is the fallback
// and its answer is reported as 3.8. It is "" when the user is not invited.
func (b *Backend) answeringAddress(userID string, atts []scheduleAttendee) string {
	me := ""
	for _, a := range atts {
		if a.address == "" || !b.scheduler.Owns(userID, a.address) {
			continue
		}
		if slices.Contains(b.scheduler.Addresses(userID), a.address) {
			return a.address
		}
		if me == "" {
			me = a.address
		}
	}
	return me
}

// scheduledCount is the number of distinct addresses the server would send an
// invitation to: usable, with SCHEDULE-AGENT SERVER, and neither the organiser
// nor an address the user sends as (the sender skips those). Apple Calendar and
// Thunderbird list the organiser among the attendees, and clients that
// schedule for themselves mark their attendees CLIENT; neither takes up the
// limit.
func (b *Backend) scheduledCount(userID, organiser string, atts []scheduleAttendee) int {
	own := b.scheduler.Addresses(userID)
	seen := map[string]bool{}
	for _, a := range atts {
		if a.address == "" || a.agent != scheduleAgentServer || a.address == organiser || slices.Contains(own, a.address) {
			continue
		}
		seen[a.address] = true
	}
	return len(seen)
}

// scheduleAfterSave runs the scheduling that follows a saved PUT and returns
// the event as it is stored in the end (SCHEDULE-STATUS may have been added).
func (b *Backend) scheduleAfterSave(userID string, plan schedulePlan, calendar *ical.Calendar, saved, existing *bongov1.Event) *bongov1.Event {
	switch plan.mode {
	case scheduleModeOrganiser:
		return b.organise(userID, calendar, saved, existing)
	case scheduleModeAttendee:
		return b.answer(userID, plan.me, calendar, saved, existing)
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
	occurrences := b.organiseOccurrences(userID, saved, existing, skip)

	cal := decodeRaw(saved.IcalRaw)
	master := masterOf(cal)
	if master == nil {
		return saved
	}
	// One rewrite carries the status of the series message and of each
	// occurrence message.
	changed := recordScheduleStatus(master, statuses)
	for _, ve := range eventsOf(cal) {
		if st, ok := occurrences[recurrenceKey(ve)]; ok && ve != master {
			changed = recordScheduleStatus(ve, st) || changed
		}
	}
	if !changed {
		return saved
	}
	return b.rewrite(userID, saved, cal)
}

// rewrite stores cal, an edited copy of saved's object, as the event and
// returns the event as stored. It is the second, non-atomic write of a
// scheduling PUT and best-effort: when it fails the invitations or replies are
// sent and the event is saved, only the status the client would show is
// missing, and saved is returned.
func (b *Backend) rewrite(userID string, saved *bongov1.Event, cal *ical.Calendar) *bongov1.Event {
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err != nil {
		b.logger.Warn("caldav: encoding the scheduling status failed", "user", userID, "guid", saved.Guid, "error", err)
		return saved
	}
	updated := proto.Clone(saved).(*bongov1.Event)
	updated.IcalRaw = buf.Bytes()
	updated.UpdatedAt = time.Now().Unix()
	if err := b.events.Update(userID, saved.Guid, updated); err != nil {
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
//
// The outcome of each answer is written as SCHEDULE-STATUS on the ORGANIZER of
// the VEVENT it came from (RFC 6638 section 3.2.9: in an attendee's copy the
// status belongs to the organiser, not to the ATTENDEE), so a client can show
// an answer that was not delivered, such as one given at an alias the user
// cannot send as (3.8). 1.1 is written on success too, which clears a stale
// failure. Like organise, the write is a second, non-atomic command after the
// save, best-effort, and a PUT landing between the two can be overwritten.
// answer returns the event as stored in the end.
func (b *Backend) answer(userID, me string, calendar *ical.Calendar, saved, existing *bongov1.Event) *bongov1.Event {
	master := masterOf(calendar)
	if master == nil || skipOrganiser(master) {
		return saved
	}
	var before *ical.Calendar
	if existing != nil {
		before = decodeRaw(existing.IcalRaw)
	}
	changes := partstatChanges(before, calendar, me)
	if len(changes) > maxReplyChangesPerPut {
		changes = changes[:maxReplyChangesPerPut]
	}
	if len(changes) == 0 {
		return saved
	}
	statuses := make(map[string]string, len(changes))
	for _, c := range changes {
		st := b.scheduler.Reply(userID, saved, me, c.partstat, c.recurrenceID)
		if st != scheduleStatusSent {
			b.logger.Warn("caldav: reply to the organiser not sent", "user", userID, "guid", saved.Guid, "status", st)
		}
		statuses[c.recurrenceID] = st
	}
	cal := decodeRaw(saved.IcalRaw)
	if cal == nil || !recordReplyStatus(cal, statuses) {
		return saved
	}
	return b.rewrite(userID, saved, cal)
}

// recordReplyStatus sets SCHEDULE-STATUS on the ORGANIZER of each VEVENT of cal
// whose recurrence key is in statuses. It reports whether anything changed.
func recordReplyStatus(cal *ical.Calendar, statuses map[string]string) bool {
	changed := false
	for _, ve := range cal.Children {
		if ve.Name != ical.CompEvent {
			continue
		}
		st, ok := statuses[recurrenceKey(ve)]
		if !ok {
			continue
		}
		if p := ve.Props.Get(ical.PropOrganizer); p != nil && p.Params.Get(paramScheduleStatus) != st {
			p.Params.Set(paramScheduleStatus, st)
			changed = true
		}
	}
	return changed
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

// organiserRevision keeps the organiser's SEQUENCE moving forward before the
// first write, so the stored object, GET and the REQUEST built from the saved
// event all carry one value. A client that edits a meeting without raising
// SEQUENCE would otherwise send attendees a REQUEST they may ignore as not
// newer. An invite-relevant change needs a value above the stored one; any
// other update never lowers it. A higher value from the client is kept. The
// master's SEQUENCE is handled here, and reviseOverrides raises the SEQUENCE
// of each RECURRENCE-ID override the PUT changes; when either moves, the
// object is re-encoded into event.
func organiserRevision(calendar *ical.Calendar, event, existing *bongov1.Event) error {
	master := masterOf(calendar)
	if master == nil || existing == nil {
		return nil
	}
	client := icalevent.Sequence(master.Props)
	want := min(int(existing.Sequence), 1<<30)
	if icalevent.InviteRelevantChange(existing, event) {
		want = min(want+1, 1<<30)
	}
	masterRaised := client < want
	if masterRaised {
		icalevent.SetSequence(master.Props, want)
	}
	// An override the PUT changes outranks the series and the override it
	// replaces; the master's SEQUENCE is left alone for an override-only change.
	overridesRaised := reviseOverrides(calendar, event, existing)
	if !masterRaised && !overridesRaised {
		return nil
	}
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(calendar); err != nil {
		return fmt.Errorf("encoding the revised sequence: %w", err)
	}
	event.IcalRaw = buf.Bytes()
	if masterRaised {
		event.Sequence = icalevent.ClampSequence(want)
	}
	return nil
}

// declineAfterDelete sends the REPLY PARTSTAT=DECLINED for a deleted event the
// user was invited to (RFC 6638 section 3.2.2.1: an attendee removing the
// object declines it). It is the counterpart of cancelAfterDelete and never
// overlaps it: an organiser that is any address of the user is not "someone
// else". Nothing is sent for an ORGANIZER with a SCHEDULE-AGENT other than
// SERVER, a cancelled event, an answer that already is DECLINED, a user who is
// not an attendee, or an event entirely past (store.EventEntirelyPast: a
// bounded series is past once its last occurrence has ended, an open-ended
// one never is).
//
// The send is best-effort and follows the delete, which cannot be undone: a
// failure is logged and DELETE still answers 204, and nothing retries it. The
// REPLY goes through Scheduler.Reply, so the send-as check (3.8 for an alias
// only invitation, which sends nothing), recipient budget and rate limits
// apply. SCHEDULE-STATUS is not written back: the object is gone.
func (b *Backend) declineAfterDelete(userID string, existing *bongov1.Event) {
	if b.scheduler == nil || existing == nil || existing.Organizer == "" {
		return
	}
	org := store.NormaliseAddress(existing.Organizer)
	if !strings.Contains(org, "@") {
		return
	}
	// Owns covers the calendar-user-address-set too: an organiser that is any
	// address of the user is cancelAfterDelete's case, not a decline.
	if b.scheduler.Owns(userID, org) {
		return
	}
	if strings.EqualFold(strings.TrimSpace(existing.Status), "CANCELLED") {
		return
	}
	master := masterOf(decodeRaw(existing.IcalRaw))
	if master == nil || skipOrganiser(master) {
		return
	}
	me := b.answeringAddress(userID, scheduleAttendees(master))
	if me == "" {
		return
	}
	if ps, _ := partstatOf(master, me); ps == "DECLINED" {
		return
	}
	if store.EventEntirelyPast(existing, b.clock(), b.scheduler.Location(userID)) {
		return
	}
	if st := b.scheduler.Reply(userID, existing, me, "DECLINED", ""); st != scheduleStatusSent {
		b.logger.Warn("caldav: the decline of a deleted invitation was not sent", "user", userID, "guid", existing.Guid, "status", st)
	}
}

// clock is the time source of the past check; tests replace it.
func (b *Backend) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}
