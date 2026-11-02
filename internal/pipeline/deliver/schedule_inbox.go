package deliver

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/imip"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ScheduleInboxPlacer puts an iTIP message in a user's CalDAV schedule inbox.
// id names the delivery, so placing the same id twice stores one item;
// *store.ScheduleInboxStore satisfies it.
type ScheduleInboxPlacer interface {
	Place(userID, id string, ics []byte) error
}

// SetScheduleInbox wires the schedule inbox. nil (the default) places nothing
// and leaves iTIP messages as ordinary mail.
func (p *DeliverProcessor) SetScheduleInbox(s ScheduleInboxPlacer) { p.schedInbox = s }

// ScheduleInbox returns the placer set by SetScheduleInbox, or nil.
func (p *DeliverProcessor) ScheduleInbox() ScheduleInboxPlacer { return p.schedInbox }

// placeScheduleInbox puts the iTIP message a delivered mail carries in the
// schedule inbox of each user whose copy was stored, so CalDAV clients show it
// (RFC 6638). The mail itself is already stored and nothing is added to a
// calendar: accepting an item is the user's action. Everything is best-effort,
// a failure is logged and never fails delivery.
//
// The trust rules are those of applyCalendarReplies, because a CalDAV client
// processes inbox items on its own and so a forged one would act without the
// DMARC rule: a REQUEST is placed for every copy (accepting it is a user
// action), a REPLY only when verifiedFrom vouches for the attendee that
// answers, and a CANCEL only when verifiedFrom is the ORGANIZER.
func (p *DeliverProcessor) placeScheduleInbox(env *bongov1.QueueEnvelope, parsed *enmime.Envelope, raw []byte, dmarcResult string, userIDs []string) {
	if p.schedInbox == nil || len(userIDs) == 0 {
		return
	}
	part, ctMethod, ok := imip.FindPart(parsed)
	if !ok {
		return
	}
	inv, err := imip.Parse(part, ctMethod)
	if err != nil {
		return
	}
	var onlyAttendee string
	switch inv.Method {
	case imip.MethodRequest:
	case imip.MethodReply:
		sender := p.verifiedFrom(env, raw, dmarcResult)
		if sender == "" || !hasAttendeeAddress(inv, sender) {
			return
		}
		onlyAttendee = sender
	case imip.MethodCancel:
		sender := p.verifiedFrom(env, raw, dmarcResult)
		if sender == "" || sender != store.NormaliseAddress(inv.Organizer) {
			return
		}
	default:
		return
	}
	ics, err := inv.Scheduled(onlyAttendee)
	if err != nil {
		p.logger.Warn("deliver: encoding a schedule inbox item failed", "id", env.Id, "error", err)
		return
	}
	for _, userID := range userIDs {
		itemID := scheduleItemID(env.Id, userID, inv)
		if err := p.schedInbox.Place(userID, itemID, ics); err != nil {
			p.logger.Warn("deliver: placing a schedule inbox item failed", "id", env.Id, "user", userID, "error", err)
		}
	}
}

// scheduleItemID names one delivery of one message to one user, so a stage
// that runs again after a crash places the item once.
func scheduleItemID(envID, userID string, inv *imip.Invite) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{envID, userID, inv.Method, inv.UID, inv.RecurrenceID}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// hasAttendeeAddress reports whether any VEVENT of inv has an ATTENDEE with
// this address.
func hasAttendeeAddress(inv *imip.Invite, addr string) bool {
	for _, ve := range inv.Events {
		for _, p := range ve.Props.Values("ATTENDEE") {
			if a, ok := icalevent.AddressFromCalAddress(p.Value); ok && store.NormaliseAddress(a) == addr {
				return true
			}
		}
	}
	return false
}
