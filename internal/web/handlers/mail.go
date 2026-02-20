package handlers

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"
	"github.com/microcosm-cc/bluemonday"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const messagesPerPage = 25

// MailHandler handles mail browsing endpoints.
type MailHandler struct {
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	mailboxes *store.MailboxStore
	sanitizer *bluemonday.Policy
}

// NewMailHandler creates a MailHandler.
func NewMailHandler(pebble *store.PebbleStore, blobs *store.BlobStore, mailboxes *store.MailboxStore) *MailHandler {
	policy := bluemonday.UGCPolicy()
	policy.AllowAttrs("style").OnElements("div", "span", "p", "td", "th", "table", "tr")
	return &MailHandler{
		pebble:    pebble,
		blobs:     blobs,
		mailboxes: mailboxes,
		sanitizer: policy,
	}
}

// ListMessages renders the message list for a mailbox.
func (h *MailHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	mailbox := chi.URLParam(r, "mailbox")
	if mailbox == "" {
		mailbox = "INBOX"
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	offset := (page - 1) * messagesPerPage
	msgs, total, err := h.mailboxes.ListMessagesPaged(userID, mailbox, offset, messagesPerPage)
	if err != nil {
		http.Error(w, "Error loading messages", http.StatusInternalServerError)
		return
	}

	totalPages := (total + messagesPerPage - 1) / messagesPerPage
	if totalPages < 1 {
		totalPages = 1
	}

	// Build summaries.
	summaries := make([]templates.MessageSummary, 0, len(msgs))
	for _, msg := range msgs {
		summary := templates.MessageSummary{
			UID:     msg.Uid,
			Date:    time.Unix(msg.InternalDate, 0),
			Seen:    msg.Flags&store.FlagSeen != 0,
			Flagged: msg.Flags&store.FlagFlagged != 0,
		}

		// Load mail meta for subject/from.
		meta := h.loadMailMeta(userID, msg.MailGuid)
		if meta != nil {
			summary.From = meta.From
			summary.Subject = meta.Subject
			if meta.RawMessage != nil {
				summary.Size = meta.RawMessage.Size
			}
		}
		if summary.Subject == "" {
			summary.Subject = "(no subject)"
		}

		summaries = append(summaries, summary)
	}

	// Load mailbox list for sidebar.
	mailboxInfos := h.loadMailboxInfos(userID, mailbox)

	flash := r.URL.Query().Get("flash")

	// HTMX partial vs full page.
	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.MailboxContent(mailbox, summaries, page, totalPages, flash)).ServeHTTP(w, r)
		return
	}

	templ.Handler(templates.MailboxPage(mailbox, mailboxInfos, summaries, page, totalPages, email, flash)).ServeHTTP(w, r)
}

// ShowMessage renders a single message detail view.
func (h *MailHandler) ShowMessage(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := chi.URLParam(r, "mailbox")
	uid, err := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	if err != nil {
		http.Error(w, "Invalid UID", http.StatusBadRequest)
		return
	}

	msg, err := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	// Auto-mark as seen.
	if msg.Flags&store.FlagSeen == 0 {
		_ = h.mailboxes.AddFlags(userID, mailbox, msg.Uid, store.FlagSeen)
	}

	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil {
		http.Error(w, "Message metadata not found", http.StatusInternalServerError)
		return
	}

	detail := templates.MessageDetail{
		UID:     msg.Uid,
		Mailbox: mailbox,
		Subject: meta.Subject,
		From:    meta.From,
		To:      meta.To,
		Date:    time.Unix(meta.ReceivedAt, 0),
		Seen:    msg.Flags&store.FlagSeen != 0,
		Flagged: msg.Flags&store.FlagFlagged != 0,
	}

	// Load raw message for body and attachments.
	if meta.RawMessage != nil && meta.RawMessage.Hash != "" {
		rc, err := h.blobs.Get(meta.RawMessage.Hash)
		if err == nil {
			rawMsg, _ := io.ReadAll(rc)
			rc.Close()

			env, err := enmime.ReadEnvelope(bytes.NewReader(rawMsg))
			if err == nil {
				if env.HTML != "" {
					detail.HTMLBody = h.sanitizer.Sanitize(env.HTML)
				}
				detail.TextBody = env.Text

				for _, att := range env.Attachments {
					detail.Attachments = append(detail.Attachments, templates.Attachment{
						Filename:    att.FileName,
						ContentType: att.ContentType,
						Size:        int64(len(att.Content)),
					})
				}
			} else {
				// Fallback to raw text.
				detail.TextBody = string(rawMsg)
			}
		}
	}

	// Also add attachments from proto.
	for _, blobRef := range meta.Attachments {
		found := false
		for _, existing := range detail.Attachments {
			if existing.Hash == blobRef.Hash {
				found = true
				break
			}
		}
		if !found {
			detail.Attachments = append(detail.Attachments, templates.Attachment{
				Hash:        blobRef.Hash,
				ContentType: blobRef.ContentType,
				Size:        int64(blobRef.Size),
			})
		}
	}

	mailboxInfos := h.loadMailboxInfos(userID, mailbox)
	templ.Handler(templates.MessagePage(detail, mailboxInfos)).ServeHTTP(w, r)
}

// ServeBlob streams a blob with the correct content type.
func (h *MailHandler) ServeBlob(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	rc, err := h.blobs.Get(hash)
	if err != nil {
		http.Error(w, "Blob not found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	io.Copy(w, rc)
}

// ToggleFlags handles flag toggling via HTMX.
func (h *MailHandler) ToggleFlags(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := chi.URLParam(r, "mailbox")
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	action := r.FormValue("action")
	flag := r.FormValue("flag")

	var flagBit uint32
	switch flag {
	case "seen":
		flagBit = store.FlagSeen
	case "flagged":
		flagBit = store.FlagFlagged
	case "deleted":
		flagBit = store.FlagDeleted
	case "answered":
		flagBit = store.FlagAnswered
	default:
		http.Error(w, "Unknown flag", http.StatusBadRequest)
		return
	}

	switch action {
	case "add":
		_ = h.mailboxes.AddFlags(userID, mailbox, uint32(uid), flagBit)
	case "remove":
		_ = h.mailboxes.RemoveFlags(userID, mailbox, uint32(uid), flagBit)
	}

	// Return updated star icon.
	msg, _ := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	flagged := msg != nil && msg.Flags&store.FlagFlagged != 0

	if flagged {
		fmt.Fprintf(w, `<span class="flag-star flagged" hx-post="/mail/%s/%d/flags" hx-vals='{"action":"remove","flag":"flagged"}' hx-swap="outerHTML">&#9733;</span>`, mailbox, uid)
	} else {
		fmt.Fprintf(w, `<span class="flag-star" hx-post="/mail/%s/%d/flags" hx-vals='{"action":"add","flag":"flagged"}' hx-swap="outerHTML">&#9734;</span>`, mailbox, uid)
	}
}

// MoveMessage moves a message to another mailbox.
func (h *MailHandler) MoveMessage(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := chi.URLParam(r, "mailbox")
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	dest := r.FormValue("destination")
	if dest == "" {
		http.Error(w, "Missing destination", http.StatusBadRequest)
		return
	}

	if err := h.mailboxes.MoveMessage(userID, mailbox, uint32(uid), dest); err != nil {
		http.Error(w, "Move failed", http.StatusInternalServerError)
		return
	}

	// Redirect back to mailbox with flash.
	w.Header().Set("HX-Redirect", fmt.Sprintf("/mail/%s?flash=Moved+to+%s", mailbox, dest))
	w.WriteHeader(http.StatusOK)
}

// DeleteMessage marks a message as deleted and expunges.
func (h *MailHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := chi.URLParam(r, "mailbox")
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	_ = h.mailboxes.AddFlags(userID, mailbox, uint32(uid), store.FlagDeleted)
	_, _ = h.mailboxes.Expunge(userID, mailbox)

	w.Header().Set("HX-Redirect", fmt.Sprintf("/mail/%s?flash=Message+deleted", mailbox))
	w.WriteHeader(http.StatusOK)
}

func (h *MailHandler) loadMailMeta(userID, guid string) *bongov1.MailMeta {
	key := fmt.Sprintf("/user/%s/mail/%s", userID, guid)
	data, err := h.pebble.Get([]byte(key))
	if err != nil {
		return nil
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

func (h *MailHandler) loadMailboxInfos(userID, selected string) []templates.MailboxInfo {
	mbs, _ := h.mailboxes.ListMailboxes(userID)
	infos := make([]templates.MailboxInfo, 0, len(mbs))
	for _, mb := range mbs {
		_, _, unseen, _ := h.mailboxes.CountMessages(userID, mb.Name)
		infos = append(infos, templates.MailboxInfo{
			Name:     mb.Name,
			Unread:   int(unseen),
			Selected: mb.Name == selected,
		})
	}
	// Sort: put standard mailboxes first in order.
	orderedInfos := make([]templates.MailboxInfo, 0, len(infos))
	for _, name := range store.DefaultMailboxes {
		for _, info := range infos {
			if info.Name == name {
				orderedInfos = append(orderedInfos, info)
				break
			}
		}
	}
	for _, info := range infos {
		isDefault := false
		for _, name := range store.DefaultMailboxes {
			if info.Name == name {
				isDefault = true
				break
			}
		}
		if !isDefault {
			orderedInfos = append(orderedInfos, info)
		}
	}
	return orderedInfos
}
