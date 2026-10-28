package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
)

// unreadCountsResponse is what the sidebar polls after a mail action: the
// unread count per mailbox exactly as the sidebar tree shows it (a parent
// includes its subfolders), and the INBOX-only figure the Unread view badge
// uses, matching the SSE badge update.
type unreadCountsResponse struct {
	Mailboxes map[string]int `json:"mailboxes"`
	Inbox     int            `json:"inbox"`
}

// UnreadCounts serves GET /api/mail/unread-counts.
func (h *MailHandler) UnreadCounts(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	resp := unreadCountsResponse{Mailboxes: map[string]int{}}
	flattenUnread(LoadMailboxInfos(h.mailboxes, userID, ""), resp.Mailboxes)
	resp.Inbox = h.mailboxes.UnreadCount(userID, "INBOX")

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func flattenUnread(nodes []templates.MailboxInfo, out map[string]int) {
	for _, n := range nodes {
		out[n.Name] = n.Unread
		flattenUnread(n.Children, out)
	}
}
