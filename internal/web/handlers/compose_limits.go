package handlers

import (
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// SetMessageLimiter sets the shared per-user messages-per-minute limiter. It is
// the instance SMTP submission uses, so both paths draw on one allowance. Its
// own limit is the global value.
func (h *ComposeHandler) SetMessageLimiter(rl *mail.RateLimiter) { h.messageRL = rl }

// MessageLimiter returns the shared per-user messages-per-minute limiter, or nil.
func (h *ComposeHandler) MessageLimiter() *mail.RateLimiter { return h.messageRL }

// SetSendLimitsReader sets how the account's per-user send limits are read.
// Without a reader every user has the global limits. A read error refuses the
// send (fail closed).
func (h *ComposeHandler) SetSendLimitsReader(fn func(userID string) (*bongov1.SendLimits, error)) {
	h.sendLimits = fn
}

// composeLimits are the limits that apply to one user's send: the per-user
// value where the admin set one, otherwise the global one. A value <= 0 means
// unlimited for the counters (the recipient cap always has a floor).
type composeLimits struct {
	messagesPerMinute int
	recipientsPerHour int
	maxRecipients     int
}

// limitsFor resolves the effective limits for userID. Web compose and
// DeliverGenerated both go through it so the two paths cannot disagree.
func (h *ComposeHandler) limitsFor(userID string) (composeLimits, error) {
	var perUser *bongov1.SendLimits
	if h.sendLimits != nil {
		var err error
		if perUser, err = h.sendLimits(userID); err != nil {
			return composeLimits{}, err
		}
	}
	globalMsgs, globalRcpts := 0, 0
	if h.messageRL != nil {
		globalMsgs = h.messageRL.Limit()
	}
	if h.recipientRL != nil {
		globalRcpts = h.recipientRL.Limit()
	}
	return composeLimits{
		messagesPerMinute: store.EffectiveLimit(perUser.GetMessagesPerMinute(), globalMsgs),
		recipientsPerHour: store.EffectiveLimit(perUser.GetRecipientsPerHour(), globalRcpts),
		maxRecipients:     store.EffectiveLimit(perUser.GetMaxRecipients(), h.globalMaxRecipients()),
	}, nil
}

// globalMaxRecipients is the global per-message recipient cap.
func (h *ComposeHandler) globalMaxRecipients() int {
	if h.maxRecipients > 0 {
		return h.maxRecipients
	}
	return defaultMaxRecipients
}

// recipientLimitFor is the per-message recipient cap shown to userID. A read
// error falls back to the global cap: the send itself enforces the real limit
// and refuses when the limits cannot be read.
func (h *ComposeHandler) recipientLimitFor(userID string) int {
	l, err := h.limitsFor(userID)
	if err != nil {
		return h.globalMaxRecipients()
	}
	return l.maxRecipients
}

// chargeExternal spends the messages-per-minute and recipients-per-hour budgets
// for a send with external recipients, in that order so a message refused by
// the message limiter spends no recipient budget. It reports whether the send
// may go ahead. A message slot already spent is not refunded when the
// recipient budget then refuses, the same strictness as the SMTP DATA path.
func (h *ComposeHandler) chargeExternal(userID string, l composeLimits, external int) bool {
	if h.messageRL != nil && !h.messageRL.AllowNLimit(userID, 1, l.messagesPerMinute) {
		return false
	}
	if h.recipientRL != nil && !h.recipientRL.AllowNLimit(userID, external, l.recipientsPerHour) {
		return false
	}
	return true
}
