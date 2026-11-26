package templates

import "fmt"

// UnsubscribeView is what the message view and the Mailing lists view show for
// the unsubscribe methods a message offers. It carries no URI except LinkURL,
// which the server has parsed and re-serialised as a plain https URL.
type UnsubscribeView struct {
	// Host is the host of the https URI, for the confirmation text.
	Host string
	// LinkURL is the validated https URL the user opens in their own browser.
	// Empty when the message offers none.
	LinkURL string
	// OneClick offers the server-side POST (RFC 8058). The DKIM check happens
	// when the user clicks.
	OneClick bool
	// Mailto offers the compose route.
	Mailto bool
	// SpamBlocked is true for a message in Junk or tagged as spam: no action
	// is offered, only the hint to report it as spam.
	SpamBlocked bool
}

// Unsubscribe result kinds rendered by UnsubscribeResult.
const (
	UnsubscribeSuccess = "success"
	UnsubscribeFailure = "failure"
	UnsubscribeLink    = "link"
)

// unsubscribeMailbox is the real mailbox of msg: the unsubscribe routes refuse
// a virtual mailbox, which spans folders and names no single message.
func unsubscribeMailbox(msg MessageDetail) string {
	if msg.RealMailbox != "" {
		return msg.RealMailbox
	}
	return msg.Mailbox
}

// UnsubscribeMailtoURL is the compose route that prepares the message's
// List-Unsubscribe mailto: request.
func UnsubscribeMailtoURL(mailbox string, uid uint32) string {
	return fmt.Sprintf("/compose/unsubscribe/%s/%d", encMailbox(mailbox), uid)
}
