package deliver

// ResourceBookingAnswerer answers an iTIP REQUEST (or releases a booking on a
// CANCEL) delivered to a room or equipment account. verifiedSender is the
// lower-cased From address when the delivery stage could vouch for it, and ""
// otherwise; the answerer must do nothing for "". It is called before the
// message is stored and returns true only when the message reached a final
// outcome (a REPLY was delivered, or a CANCEL was applied); the mail is then
// not kept in the recipient's INBOX. On false the mail is stored as usual, so a
// failure never loses mail.
type ResourceBookingAnswerer interface {
	AnswerBooking(userID, verifiedSender string, part []byte, ctMethod string) (handled bool)
}

// SetResourceBookings wires the automatic answers of resource calendars. nil
// (the default) leaves booking requests as ordinary mail.
func (p *DeliverProcessor) SetResourceBookings(a ResourceBookingAnswerer) { p.resourceBookings = a }

// ResourceBookings returns the answerer set by SetResourceBookings, or nil.
func (p *DeliverProcessor) ResourceBookings() ResourceBookingAnswerer { return p.resourceBookings }
