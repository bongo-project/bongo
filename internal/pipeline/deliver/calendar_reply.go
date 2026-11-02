package deliver

import (
	"strings"

	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/imip"
	bongomail "github.com/bongo-project/bongo/internal/mail"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// CalendarReplyApplier applies an iTIP REPLY carried by a delivered message to
// the recipient's own events. verifiedSender is the lower-cased From address
// when the delivery stage could vouch for it (DMARC pass, or an authenticated
// local author who may send as it), and "" otherwise; the applier must change
// nothing for "". It returns nothing: the mail is already stored and a failure
// to apply must never fail delivery.
type CalendarReplyApplier interface {
	ApplyReply(userID, verifiedSender string, part []byte, ctMethod string)
}

// SetCalendarReplies wires the calendar side of iMIP replies. nil (the
// default) leaves replies as ordinary mail.
func (p *DeliverProcessor) SetCalendarReplies(a CalendarReplyApplier) { p.calReplies = a }

// CalendarReplies returns the applier set by SetCalendarReplies, or nil.
func (p *DeliverProcessor) CalendarReplies() CalendarReplyApplier { return p.calReplies }

// verifiedFrom returns the lower-cased single header From address when this
// delivery can vouch for it, else "". The two proofs are a DMARC pass of the
// builtin check, and an authenticated local author who may send as the
// address. An empty verdict (checks bypassed for a trusted network, or
// antispam disabled) is not a pass, and a missing authorizer or a lookup error
// fails closed. The From is parsed from the unstamped bytes with the signer's
// own strict rules, as forwardOrigin does.
func (p *DeliverProcessor) verifiedFrom(env *bongov1.QueueEnvelope, raw []byte, dmarcResult string) string {
	addr, err := bongomail.StrictFromAddress(raw)
	if err != nil {
		return ""
	}
	addr = strings.ToLower(addr)
	if dmarcResult == "pass" {
		return addr
	}
	userID := env.GetAuthUserId()
	if userID == "" || p.authorizer == nil {
		return ""
	}
	ok, err := p.authorizer.CanSendAs(userID, addr)
	if err != nil {
		p.logger.Warn("deliver: send-as lookup failed for a calendar reply",
			"id", env.Id, "user", userID, "error", err)
		return ""
	}
	if !ok {
		return ""
	}
	return addr
}

// applyCalendarReplies hands the calendar part of the message to the applier
// once for each user whose copy was stored. Ordinary mail costs one walk of
// the part tree.
func (p *DeliverProcessor) applyCalendarReplies(env *bongov1.QueueEnvelope, parsed *enmime.Envelope, raw []byte, dmarcResult string, userIDs []string) {
	if p.calReplies == nil || len(userIDs) == 0 {
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
		p.calReplies.ApplyReply(userID, sender, part, ctMethod)
	}
}
