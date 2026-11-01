package templates

// InvitationCard is the model of the invitation card on the message view. Every
// string in it comes from an untrusted message: the handler strips control and
// bidirectional characters and bounds the lengths, and the template renders
// them as escaped text only.
type InvitationCard struct {
	// Mailbox and UID name the message; the buttons POST to it.
	Mailbox string
	UID     uint32
	// Method is REQUEST or CANCEL.
	Method      string
	Title       string
	When        string
	Where       string
	Description string
	Organiser   string
	// OrganiserDiffersFrom is the sender's address when it is not the organiser.
	OrganiserDiffersFrom string
	DMARCFailed          bool
	Attendees            []InvitationAttendee
	MoreAttendees        int
	Conflicts            []InvitationConflict
	ConflictsChecked     bool
	FirstInstanceOnly    bool
	Calendars            []InvitationCalendar
	// Status and Notes are message IDs. Status says what the user's calendar
	// holds for the event; Notes explain why a button is missing.
	Status string
	Notes  []string
	// Which buttons the server will honour.
	CanAnswer        bool
	CanUpdate        bool
	CanMarkCancelled bool
	CanRemove        bool
	ReplyFailed      bool
}

// InvitationAttendee is one invitee on the card.
type InvitationAttendee struct {
	Name, Address, Partstat string
}

// InvitationConflict is an event of the user's that overlaps the invitation.
type InvitationConflict struct {
	Title, When string
}

// InvitationCalendar is a calendar the user can add the event to.
type InvitationCalendar struct {
	ID, Name string
	Selected bool
}

// partstatKey maps a PARTSTAT to the message ID of its label.
func partstatKey(p string) string {
	switch p {
	case "ACCEPTED":
		return "invite.partAccepted"
	case "DECLINED":
		return "invite.partDeclined"
	case "TENTATIVE":
		return "invite.partTentative"
	default:
		return "invite.partNeedsAction"
	}
}
