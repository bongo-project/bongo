package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The To me, From my contacts and Mailing lists views. They are filters over
// the user's derived SQLite index (to_me, sender_addr and list_id), listed from
// INBOX and the user's folders, never Junk, Trash, Sent or Drafts. Each row is
// shown with its real mailbox, and actions on it target that mailbox.
//
// Folders are not reserved names: a folder called "lists" or "to-me" is an
// ordinary folder at /mail/lists and /mail/to-me. The views live under
// /mail/view/ and are identified by a store.HeaderView, never by a mailbox name.

var (
	toMeView         = store.HeaderView{Kind: store.ViewToMe}
	fromContactsView = store.HeaderView{Kind: store.ViewFromContacts}
	listsOverview    = store.HeaderView{Kind: store.ViewLists}
)

// ListToMe renders the To me view. GET /mail/view/to-me
func (h *MailHandler) ListToMe(w http.ResponseWriter, r *http.Request) {
	h.serveList(w, r, toMeView.URL(), &toMeView)
}

// ListFromContacts renders the From my contacts view. GET /mail/view/from-contacts
func (h *MailHandler) ListFromContacts(w http.ResponseWriter, r *http.Request) {
	h.serveList(w, r, fromContactsView.URL(), &fromContactsView)
}

// listViewFromRequest is the view of one mailing list named by the {listID}
// path parameter. List-Id is attacker-controlled: only an id from the stored
// character set is a view, anything else is not found.
func listViewFromRequest(r *http.Request) (store.HeaderView, bool) {
	id := decodeMailbox(chi.URLParam(r, "listID"))
	if !store.ValidListID(id) {
		return store.HeaderView{}, false
	}
	return store.HeaderView{Kind: store.ViewLists, ListID: id}, true
}

// ListMailingList renders one mailing list's messages.
// GET /mail/view/lists/{listID}
func (h *MailHandler) ListMailingList(w http.ResponseWriter, r *http.Request) {
	view, ok := listViewFromRequest(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveList(w, r, view.URL(), &view)
}

// BulkToMe applies a bulk action to messages of the To me view, which are named
// as uid:mailbox targets. POST /mail/view/to-me/bulk
func (h *MailHandler) BulkToMe(w http.ResponseWriter, r *http.Request) {
	h.bulkAction(w, r, toMeView.URL(), true)
}

// BulkFromContacts is BulkToMe for the From my contacts view.
// POST /mail/view/from-contacts/bulk
func (h *MailHandler) BulkFromContacts(w http.ResponseWriter, r *http.Request) {
	h.bulkAction(w, r, fromContactsView.URL(), true)
}

// BulkMailingList is BulkToMe for one mailing list's view.
// POST /mail/view/lists/{listID}/bulk
func (h *MailHandler) BulkMailingList(w http.ResponseWriter, r *http.Request) {
	view, ok := listViewFromRequest(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.bulkAction(w, r, view.URL(), true)
}

// ListMailingLists renders the Mailing lists overview: each list with its name
// and message count, linking to the list's view. GET /mail/view/lists
func (h *MailHandler) ListMailingLists(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	lists, err := h.mailingLists(userID)
	if err != nil {
		h.logger.Warn("web: listing mailing lists", "user", userID, "error", err)
		http.Error(w, "Error loading messages", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ListsContent(lists)).ServeHTTP(w, r)
		return
	}
	mailboxInfos := h.loadMailboxInfos(userID, listsOverview.URL())
	sidebarTags := h.loadSidebarTags(userID, listsOverview.URL())
	templ.Handler(templates.ListsPage(mailboxInfos, sidebarTags, lists, email)).ServeHTTP(w, r)
}

// mailingLists groups the user's list mail by List-Id. The index has no
// mailbox column, so counts are taken from the messages the list views would
// show (after the Junk, Trash, Sent and Drafts exclusion), and a list with
// nothing to show is left out.
func (h *MailHandler) mailingLists(userID string) ([]templates.MailingList, error) {
	if h.metadata == nil {
		return nil, nil
	}
	groups, err := h.metadata.ListGroups(userID)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, nil
	}
	groupOf := make(map[string]int)
	guidSet := make(map[string]bool)
	for i, g := range groups {
		for _, guid := range g.GUIDs {
			groupOf[guid] = i
			guidSet[guid] = true
		}
	}
	msgs, _, _, err := h.mailboxes.ListViewMessages(userID, guidSet, 0, maxMessagePageLimit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(msgs))
	counts := make([]int, len(groups))
	for _, m := range msgs {
		if seen[m.MailGuid] {
			continue
		}
		seen[m.MailGuid] = true
		counts[groupOf[m.MailGuid]]++
	}
	out := make([]templates.MailingList, 0, len(groups))
	for i, g := range groups {
		if counts[i] == 0 {
			continue
		}
		out = append(out, templates.MailingList{ID: g.ID, Name: g.Name, Count: counts[i]})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := listSortKey(out[i]), listSortKey(out[j])
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func listSortKey(l templates.MailingList) string {
	if strings.TrimSpace(l.Name) != "" {
		return strings.ToLower(l.Name)
	}
	return strings.ToLower(l.ID)
}

// headerViewMessages returns the messages of a header view and, index for
// index, the real mailbox each one lives in, newest first across folders.
// unreadOnly keeps the messages not yet seen.
func (h *MailHandler) headerViewMessages(userID string, view store.HeaderView, unreadOnly bool) ([]*bongov1.MailboxMessage, []string, error) {
	guids, err := h.headerViewGUIDs(userID, view)
	if err != nil {
		return nil, nil, err
	}
	if len(guids) == 0 {
		return nil, nil, nil
	}
	guidSet := make(map[string]bool, len(guids))
	for _, g := range guids {
		guidSet[g] = true
	}
	var keep func(*bongov1.MailboxMessage) bool
	if unreadOnly {
		keep = func(m *bongov1.MailboxMessage) bool { return m.Flags&store.FlagSeen == 0 }
	}
	msgs, names, _, err := h.mailboxes.ListViewMessagesFiltered(userID, guidSet, keep, 0, maxMessagePageLimit)
	return msgs, names, err
}

// headerViewGUIDs asks the user's own index for the guids of a view. userID is
// always the session's, which is what keeps one user's list mail out of
// another's view even when both belong to the same list.
func (h *MailHandler) headerViewGUIDs(userID string, view store.HeaderView) ([]string, error) {
	if h.metadata == nil {
		return nil, nil
	}
	switch {
	case view.ListID != "":
		return h.metadata.QueryByListID(userID, view.ListID)
	case view.Kind == store.ViewToMe:
		return h.metadata.QueryToMe(userID)
	case view.Kind == store.ViewFromContacts:
		// Read per request, so a contact added a moment ago changes the view
		// without any re-index.
		if h.contacts == nil {
			return nil, nil
		}
		senders, err := h.contacts.Addresses(userID)
		if err != nil {
			return nil, fmt.Errorf("reading contact addresses: %w", err)
		}
		return h.metadata.QueryBySenders(userID, senders)
	}
	return nil, nil
}
