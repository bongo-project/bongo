package templates

import (
	"context"
	"fmt"
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
// me, From my contacts and Mailing lists views the list mailbox is only a
// navigation key, so the message's real mailbox is used; in a folder it is the
// list mailbox, exactly as before. view is nil for everything but those views.
func rowActionMailbox(msg MessageSummary, listMailbox string, view *store.HeaderView) string {
	if view != nil {
		return rowRealMailbox(msg, listMailbox)
	}
	return listMailbox
}

// rowSelKey is the bulk-selection key of a row in a header view: UIDs are only
// unique within one mailbox, so it pairs the UID with the real mailbox. It is
// empty in every other view, which keeps keying selections by UID.
func rowSelKey(msg MessageSummary, listMailbox string, view *store.HeaderView) string {
	if view == nil {
		return ""
	}
	return fmt.Sprintf("%d:%s", msg.UID, rowRealMailbox(msg, listMailbox))
}

// viewBulkURL is the URL bulk actions are posted to in a header view, or empty
// in a folder (the client then posts to the folder's own bulk URL). It is built
// from constants and the validated list id only.
func viewBulkURL(view *store.HeaderView) string {
	if view == nil {
		return ""
	}
	return view.BulkURL()
}

// mailboxHeading is the page heading of a list view: the translated name of a
// header view, the list id for one list, and otherwise the mailbox title.
func mailboxHeading(ctx context.Context, mailbox string, view *store.HeaderView) string {
	if view != nil {
		switch {
		case view.ListID != "":
			return view.ListID
		case view.Kind == store.ViewToMe:
			return t(ctx, "nav.toMe")
		case view.Kind == store.ViewFromContacts:
			return t(ctx, "nav.fromContacts")
		default:
			return t(ctx, "nav.mailingLists")
		}
	}
	return mailboxTitle(mailbox)
}

// emptyListText is the empty-state title and hint of a list view.
func emptyListText(ctx context.Context, view *store.HeaderView) (title, hint string) {
	if view != nil {
		switch {
		case view.ListID != "":
			return t(ctx, "view.listEmpty"), t(ctx, "view.listHint")
		case view.Kind == store.ViewToMe:
			return t(ctx, "view.toMeEmpty"), t(ctx, "view.toMeHint")
		case view.Kind == store.ViewFromContacts:
			return t(ctx, "view.fromContactsEmpty"), t(ctx, "view.fromContactsHint")
		}
	}
	return t(ctx, "mail.noMessages"), t(ctx, "mail.emptyMailbox")
}

// listsNavKey is the navigation key of the Mailing lists overview. A view's
// navigation key is its URL, which no folder-name comparison can match by
// accident the way the bare words "to-me" and "lists" could.
var listsNavKey = store.HeaderView{Kind: store.ViewLists}.URL()

// isListsSelected reports whether the Mailing lists sidebar entry is current:
// on the overview or on one list.
func isListsSelected(selected string) bool {
	return selected == listsNavKey || strings.HasPrefix(selected, listsNavKey+"/")
}

// listViewURL is the path of one mailing list's view.
func listViewURL(id string) string {
	return store.HeaderView{Kind: store.ViewLists, ListID: id}.URL()
}

// listLabel is the text a list is shown by: its name, or its id without one.
func listLabel(l MailingList) string {
	if strings.TrimSpace(l.Name) != "" {
		return l.Name
	}
	return l.ID
}
