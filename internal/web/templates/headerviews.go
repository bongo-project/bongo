package templates

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/bongo-project/bongo/internal/store"
)

// MailingList is one row of the Mailing lists overview.
type MailingList struct {
	// ID is the normalised List-Id; it is validated before it gets here.
	ID string
	// Name is the list's display name, empty when no message carried one.
	Name  string
	Count int
}

// rowActionMailbox is the mailbox an action on a row must target. In the To
// me, From my contacts and Mailing lists views the list mailbox is only a view
// name, so the message's real mailbox is used; elsewhere it is the list
// mailbox, exactly as before.
func rowActionMailbox(msg MessageSummary, listMailbox string) string {
	if store.IsHeaderView(listMailbox) {
		return rowRealMailbox(msg, listMailbox)
	}
	return listMailbox
}

// rowSelKey is the bulk-selection key of a row in a header view: UIDs are only
// unique within one mailbox, so it pairs the UID with the real mailbox. It is
// empty in every other view, which keeps keying selections by UID.
func rowSelKey(msg MessageSummary, listMailbox string) string {
	if !store.IsHeaderView(listMailbox) {
		return ""
	}
	return fmt.Sprintf("%d:%s", msg.UID, rowRealMailbox(msg, listMailbox))
}

// mailboxHeading is the page heading of a list view: the translated name of a
// header view, the list id for one list, and otherwise the mailbox title.
func mailboxHeading(ctx context.Context, mailbox string) string {
	if v, ok := store.ParseHeaderView(mailbox); ok {
		switch {
		case v.ListID != "":
			return v.ListID
		case v.Kind == store.ViewToMe:
			return t(ctx, "nav.toMe")
		case v.Kind == store.ViewFromContacts:
			return t(ctx, "nav.fromContacts")
		default:
			return t(ctx, "nav.mailingLists")
		}
	}
	return mailboxTitle(mailbox)
}

// emptyListText is the empty-state title and hint of a list view.
func emptyListText(ctx context.Context, mailbox string) (title, hint string) {
	if v, ok := store.ParseHeaderView(mailbox); ok {
		switch {
		case v.ListID != "":
			return t(ctx, "view.listEmpty"), t(ctx, "view.listHint")
		case v.Kind == store.ViewToMe:
			return t(ctx, "view.toMeEmpty"), t(ctx, "view.toMeHint")
		case v.Kind == store.ViewFromContacts:
			return t(ctx, "view.fromContactsEmpty"), t(ctx, "view.fromContactsHint")
		}
	}
	return t(ctx, "mail.noMessages"), t(ctx, "mail.emptyMailbox")
}

// isListsSelected reports whether the Mailing lists sidebar entry is current:
// on the overview or on one list.
func isListsSelected(selected string) bool {
	return selected == store.ViewLists || strings.HasPrefix(selected, store.ViewListPrefix)
}

// listViewURL is the path of one mailing list's view.
func listViewURL(id string) string {
	return "/mail/" + store.ViewListPrefix + url.PathEscape(id)
}

// listLabel is the text a list is shown by: its name, or its id without one.
func listLabel(l MailingList) string {
	if strings.TrimSpace(l.Name) != "" {
		return l.Name
	}
	return l.ID
}
