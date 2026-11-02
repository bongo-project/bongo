package deliver

import (
	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/imip"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ResourceBookingAnswerer answers an iTIP REQUEST (or releases a booking on a
// CANCEL) delivered to a room or equipment account. verifiedSender is the
// lower-cased From address when the delivery stage could vouch for it, and ""
// otherwise; the answerer must do nothing for "". It returns nothing: the mail
// is already stored and a failure must never fail delivery.
type ResourceBookingAnswerer interface {
	AnswerBooking(userID, verifiedSender string, part []byte, ctMethod string)
}

// SetResourceBookings wires the automatic answers of resource calendars. nil
// (the default) leaves booking requests as ordinary mail.
func (p *DeliverProcessor) SetResourceBookings(a ResourceBookingAnswerer) { p.resourceBookings = a }

// ResourceBookings returns the answerer set by SetResourceBookings, or nil.
func (p *DeliverProcessor) ResourceBookings() ResourceBookingAnswerer { return p.resourceBookings }

// answerResourceBookings hands the calendar part of the message to the
// answerer once for each user whose copy was stored outside Junk. Ordinary mail
// costs one walk of the part tree, and a message without a verified sender
// costs nothing more.
func (p *DeliverProcessor) answerResourceBookings(env *bongov1.QueueEnvelope, parsed *enmime.Envelope, raw []byte, dmarcResult string, userIDs []string) {
	if p.resourceBookings == nil || len(userIDs) == 0 {
		return
	}
	part, ctMethod, ok := imip.FindPart(parsed)
	if !ok {
		return
	}
	sender := p.verifiedFrom(env, raw, dmarcResult)
	if sender == "" {
		return
	}
	for _, userID := range userIDs {
		p.resourceBookings.AnswerBooking(userID, sender, part, ctMethod)
	}
}
