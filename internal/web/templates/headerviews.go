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

// spansMailboxes reports whether a list shows messages from more than one
// mailbox: the To me, From my contacts and Mailing lists views (view non-nil),
// the virtual mailboxes (All Mail, Unread, Starred) and the tag views. In such
// a list the list mailbox is only a navigation key, and a UID alone does not
// name a message, because UIDs are only unique within one mailbox.
func spansMailboxes(listMailbox string, view *store.HeaderView) bool {
	return view != nil || store.VirtualMailboxes[listMailbox] || isTagView(listMailbox)
}

// tagViewPrefix starts the list mailbox of a tag view, "tag:<name>".
const tagViewPrefix = "tag:"

// isTagView reports whether listMailbox is a tag view's list mailbox. A folder
// may really be called "tag:x" (IMAP allows it); its rows carry no real
// mailbox, so rowRealMailbox falls back to the folder itself, and the tag bulk
// route acts on its uid:tag:x targets, so treating it as a tag view is safe.
func isTagView(listMailbox string) bool {
	return strings.HasPrefix(listMailbox, tagViewPrefix)
}

// rowActionMailbox is the mailbox an action on a row must target: the
// message's real mailbox in a list that spans mailboxes, and otherwise the
// list mailbox, exactly as before.
func rowActionMailbox(msg MessageSummary, listMailbox string, view *store.HeaderView) string {
	if spansMailboxes(listMailbox, view) {
		return rowRealMailbox(msg, listMailbox)
	}
	return listMailbox
}

// rowSelKey is the bulk-selection key of a row in a list that spans mailboxes:
// it pairs the UID with the real mailbox. It is empty in a folder, which keeps
// keying selections by UID.
func rowSelKey(msg MessageSummary, listMailbox string, view *store.HeaderView) string {
	if !spansMailboxes(listMailbox, view) {
		return ""
	}
	return fmt.Sprintf("%d:%s", msg.UID, rowRealMailbox(msg, listMailbox))
}

// viewBulkURL is the URL bulk actions are posted to in a header view or a tag
// view, or empty in a folder or a virtual mailbox (the client then posts to
// /mail/<list mailbox>/bulk). It is built from constants, the validated list
// id and the path-escaped tag name only.
func viewBulkURL(listMailbox string, view *store.HeaderView) string {
	switch {
	case view != nil:
		return view.BulkURL()
	case isTagView(listMailbox):
		return "/mail/tag/" + url.PathEscape(strings.TrimPrefix(listMailbox, tagViewPrefix)) + "/bulk"
	}
	return ""
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
