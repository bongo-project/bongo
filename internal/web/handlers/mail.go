package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"
	"github.com/microcosm-cc/bluemonday"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/antispam"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const defaultMessagesPerPage = 25

// MailHandler handles mail browsing endpoints.
type MailHandler struct {
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	mailboxes *store.MailboxStore
	threads   *store.ThreadStore
	applier   store.RaftApplier
	prefs     *store.PreferenceStore
	contacts  *store.ContactStore
	bayes     *antispam.BayesFilter
	sanitizer *bluemonday.Policy
	logger    *slog.Logger
	metadata  *store.MetadataManager
	search    *store.SearchManager
	tags      *store.TagStore
}

// SetThreadStore sets the thread store for conversation grouping.
func (h *MailHandler) SetThreadStore(ts *store.ThreadStore) {
	h.threads = ts
}

// NewMailHandler creates a MailHandler.
func NewMailHandler(pebble *store.PebbleStore, blobs *store.BlobStore, mailboxes *store.MailboxStore, logger *slog.Logger) *MailHandler {
	policy := bluemonday.UGCPolicy()
	policy.AllowAttrs("style").OnElements("div", "span", "p", "td", "th", "table", "tr")
	return &MailHandler{
		pebble:    pebble,
		blobs:     blobs,
		mailboxes: mailboxes,
		sanitizer: policy,
		logger:    logger,
	}
}

// SetApplier sets the Raft applier for lazy backfill operations.
func (h *MailHandler) SetApplier(applier store.RaftApplier) {
	h.applier = applier
}

// SetPreferences sets the preference store for per-user settings.
func (h *MailHandler) SetPreferences(prefs *store.PreferenceStore) {
	h.prefs = prefs
}

// SetContacts sets the contact store for sender lookup.
func (h *MailHandler) SetContacts(contacts *store.ContactStore) {
	h.contacts = contacts
}

// SetBayes sets the Bayesian filter for spam training.
func (h *MailHandler) SetBayes(bayes *antispam.BayesFilter) {
	h.bayes = bayes
}

// SetMetadata sets the metadata manager for SQLite tag index updates.
func (h *MailHandler) SetMetadata(metadata *store.MetadataManager) {
	h.metadata = metadata
}

// SetSearch sets the search manager for Bleve index updates.
func (h *MailHandler) SetSearch(search *store.SearchManager) {
	h.search = search
}

// SetTagStore sets the tag store for auto-creating tag definitions.
func (h *MailHandler) SetTagStore(tags *store.TagStore) {
	h.tags = tags
}

// ListMessages renders the message list for a mailbox.
func (h *MailHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	if mailbox == "" {
		mailbox = "INBOX"
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	perPage := defaultMessagesPerPage
	if h.prefs != nil {
		if up := h.prefs.Get(userID); up.MessagesPerPage > 0 {
			perPage = int(up.MessagesPerPage)
		}
	}

	offset := (page - 1) * perPage
	var msgs []*bongov1.MailboxMessage
	var total int
	var err error

	if store.VirtualMailboxes[mailbox] {
		msgs, total, err = h.mailboxes.ListVirtualMessages(userID, mailbox, offset, perPage)
	} else {
		msgs, total, err = h.mailboxes.ListMessagesPaged(userID, mailbox, offset, perPage)
	}
	if err != nil {
		http.Error(w, "Error loading messages", http.StatusInternalServerError)
		return
	}

	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	// Build summaries with thread info.
	summaries := make([]templates.MessageSummary, 0, len(msgs))
	seenRoots := make(map[string]bool)

	for _, msg := range msgs {
		summary := templates.MessageSummary{
			UID:      msg.Uid,
			MailGUID: msg.MailGuid,
			Date:     time.Unix(msg.InternalDate, 0),
			Seen:     msg.Flags&store.FlagSeen != 0,
			Flagged:  msg.Flags&store.FlagFlagged != 0,
		}

		// Load mail meta for subject/from.
		meta := h.loadMailMeta(userID, msg.MailGuid)
		if meta != nil {
			summary.From = meta.From
			summary.Subject = meta.Subject
			if meta.RawMessage != nil {
				summary.Size = meta.RawMessage.Size
			}
			summary.SpamScore = meta.SpamScore

			// Thread grouping: find root and count.
			if h.threads != nil && meta.MessageId != "" {
				rootGUID, _ := h.threads.GetThreadRoot(userID, msg.MailGuid)
				summary.ThreadRoot = rootGUID

				// Skip if we already showed this thread root.
				if seenRoots[rootGUID] {
					continue
				}
				seenRoots[rootGUID] = true

				count := h.threads.GetThreadCount(userID, meta.MessageId)
				if count > 1 {
					summary.ThreadCount = count
				}
			}
		}
		if summary.Subject == "" {
			summary.Subject = "(no subject)"
		}

		// Resolve tag dots from the denormalised tags on the message.
		// Show at most 3 dots; any excess is recorded as OverflowTags.
		if h.tags != nil && len(msg.Tags) > 0 {
			const maxDots = 3
			visible := msg.Tags
			if len(visible) > maxDots {
				summary.OverflowTags = len(visible) - maxDots
				visible = visible[:maxDots]
			}
			for _, tagName := range visible {
				var colourIdx int32
				if tm, err := h.tags.GetTag(userID, tagName); err == nil {
					colourIdx = tm.ColourIndex
				}
				summary.Tags = append(summary.Tags, templates.TagDot{
					Name:   tagName,
					Colour: templates.TagDotColour(colourIdx),
				})
			}
		}

		summaries = append(summaries, summary)
	}

	// Load user preferences for infinite scroll setting.
	infiniteScroll := false
	if h.prefs != nil {
		if up := h.prefs.Get(userID); up.InfiniteScroll {
			infiniteScroll = true
		}
	}

	// Infinite scroll partial: return only message rows (no layout, no sidebar, no pagination wrapper).
	if r.URL.Query().Get("partial") == "1" {
		templ.Handler(templates.MessageRowsPartial(summaries, mailbox, page, totalPages)).ServeHTTP(w, r)
		return
	}

	// Load mailbox list for sidebar.
	mailboxInfos := h.loadMailboxInfos(userID, mailbox)
	sidebarTags := h.loadSidebarTags(userID, mailbox)

	flash := r.URL.Query().Get("flash")

	// HTMX partial vs full page.
	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.MailboxContent(mailbox, summaries, page, totalPages, flash, infiniteScroll)).ServeHTTP(w, r)
		return
	}

	templ.Handler(templates.MailboxPage(mailbox, mailboxInfos, sidebarTags, summaries, page, totalPages, email, flash, infiniteScroll)).ServeHTTP(w, r)
}

// ListTagMessages renders the message list for a tag virtual view.
// GET /mail/tag/{name}
func (h *MailHandler) ListTagMessages(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	tagName := chi.URLParam(r, "name")

	if err := store.ValidateTagName(tagName); err != nil {
		http.Error(w, "Invalid tag name", http.StatusBadRequest)
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	perPage := defaultMessagesPerPage
	if h.prefs != nil {
		if up := h.prefs.Get(userID); up.MessagesPerPage > 0 {
			perPage = int(up.MessagesPerPage)
		}
	}

	offset := (page - 1) * perPage

	// Build virtual mailbox name used in template URLs (e.g. /mail/tag:work/uid).
	virtualMailbox := "tag:" + tagName

	// Query GUIDs from SQLite tag index.
	var guidSet map[string]bool
	if h.metadata != nil {
		guids, err := h.metadata.QueryByTag(userID, tagName)
		if err != nil {
			h.logger.Warn("web: querying tag index", "tag", tagName, "user", userID, "error", err)
		} else {
			guidSet = make(map[string]bool, len(guids))
			for _, g := range guids {
				guidSet[g] = true
			}
		}
	}

	var msgs []*bongov1.MailboxMessage
	var total int
	var err error

	if len(guidSet) > 0 {
		msgs, total, err = h.mailboxes.ListMessagesByGUIDs(userID, guidSet, offset, perPage)
		if err != nil {
			http.Error(w, "Error loading messages", http.StatusInternalServerError)
			return
		}
	}

	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	// Build summaries with thread info.
	summaries := make([]templates.MessageSummary, 0, len(msgs))
	seenRoots := make(map[string]bool)

	for _, msg := range msgs {
		summary := templates.MessageSummary{
			UID:      msg.Uid,
			MailGUID: msg.MailGuid,
			Date:     time.Unix(msg.InternalDate, 0),
			Seen:     msg.Flags&store.FlagSeen != 0,
			Flagged:  msg.Flags&store.FlagFlagged != 0,
		}

		meta := h.loadMailMeta(userID, msg.MailGuid)
		if meta != nil {
			summary.From = meta.From
			summary.Subject = meta.Subject
			if meta.RawMessage != nil {
				summary.Size = meta.RawMessage.Size
			}
			summary.SpamScore = meta.SpamScore

			if h.threads != nil && meta.MessageId != "" {
				rootGUID, _ := h.threads.GetThreadRoot(userID, msg.MailGuid)
				summary.ThreadRoot = rootGUID

				if seenRoots[rootGUID] {
					continue
				}
				seenRoots[rootGUID] = true

				count := h.threads.GetThreadCount(userID, meta.MessageId)
				if count > 1 {
					summary.ThreadCount = count
				}
			}
		}
		if summary.Subject == "" {
			summary.Subject = "(no subject)"
		}

		summaries = append(summaries, summary)
	}

	infiniteScroll := false
	if h.prefs != nil {
		if up := h.prefs.Get(userID); up.InfiniteScroll {
			infiniteScroll = true
		}
	}

	if r.URL.Query().Get("partial") == "1" {
		templ.Handler(templates.MessageRowsPartial(summaries, virtualMailbox, page, totalPages)).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID, virtualMailbox)
	sidebarTags := h.loadSidebarTags(userID, virtualMailbox)
	flash := r.URL.Query().Get("flash")

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.MailboxContent(virtualMailbox, summaries, page, totalPages, flash, infiniteScroll)).ServeHTTP(w, r)
		return
	}

	templ.Handler(templates.MailboxPage(virtualMailbox, mailboxInfos, sidebarTags, summaries, page, totalPages, email, flash, infiniteScroll)).ServeHTTP(w, r)
}

// ShowMessage renders a single message detail view.
func (h *MailHandler) ShowMessage(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
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

	// Lazy backfill: populate threading fields from raw blob for pre-sprint-08 messages.
	if meta.InReplyTo == "" && meta.RawMessage != nil && meta.RawMessage.Hash != "" {
		h.backfillThreadingFields(userID, msg.MailGuid, meta)
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

	// Populate current message tags with colour info.
	if len(meta.Tags) > 0 && h.tags != nil {
		for _, name := range meta.Tags {
			ti := templates.TagInfo{Name: name}
			if tm, err := h.tags.GetTag(userID, name); err == nil {
				ti.ColourIndex = tm.ColourIndex
			}
			detail.Tags = append(detail.Tags, ti)
		}
	}

	// Populate all user tags for the add-tag dropdown.
	if h.tags != nil {
		if allTags, err := h.tags.ListTags(userID); err == nil {
			for _, twc := range allTags {
				detail.AllTags = append(detail.AllTags, templates.TagInfo{
					Name:        twc.Tag.Name,
					ColourIndex: twc.Tag.ColourIndex,
				})
			}
		}
	}

	// Check if sender is already a contact.
	if h.contacts != nil && meta.From != "" {
		existing, _ := h.contacts.FindByEmail(userID, meta.From)
		detail.IsContact = existing != nil
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
					inlined := inlineStyleBlocks(env.HTML)
					sanitized := h.sanitizer.Sanitize(inlined)
					detail.HTMLBody, detail.HasBlockedImages = blockRemoteImages(sanitized)
				}
				detail.TextBody = env.Text

				for _, att := range env.Attachments {
					a := templates.Attachment{
						Filename:    att.FileName,
						ContentType: att.ContentType,
						Size:        int64(len(att.Content)),
					}
					// Try to match with proto BlobRef by filename for hash.
					for _, br := range meta.Attachments {
						if br.Filename == att.FileName {
							a.Hash = br.Hash
							if br.ContentType != "" {
								a.ContentType = br.ContentType
							}
							break
						}
					}
					detail.Attachments = append(detail.Attachments, a)
				}
			} else {
				// Fallback to raw text.
				detail.TextBody = string(rawMsg)
			}
		}
	}

	// Also add attachments from proto that weren't matched above.
	for _, blobRef := range meta.Attachments {
		found := false
		for _, existing := range detail.Attachments {
			if existing.Hash == blobRef.Hash || (existing.Filename != "" && existing.Filename == blobRef.Filename) {
				found = true
				break
			}
		}
		if !found {
			detail.Attachments = append(detail.Attachments, templates.Attachment{
				Filename:    blobRef.Filename,
				Hash:        blobRef.Hash,
				ContentType: blobRef.ContentType,
				Size:        blobRef.Size,
			})
		}
	}

	// Pane mode: return just the message content without layout wrapper.
	if r.URL.Query().Get("pane") == "1" {
		templ.Handler(templates.MessagePaneContent(detail)).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID, mailbox)
	templ.Handler(templates.MessagePage(detail, mailboxInfos)).ServeHTTP(w, r)
}

// BulkAction handles bulk operations on selected messages.
func (h *MailHandler) BulkAction(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	action := r.FormValue("action")
	uidStrs := r.Form["uids"]

	for _, uidStr := range uidStrs {
		uid, err := strconv.ParseUint(uidStr, 10, 32)
		if err != nil {
			continue
		}

		switch action {
		case "mark-read":
			_ = h.mailboxes.AddFlags(userID, mailbox, uint32(uid), store.FlagSeen)
		case "mark-unread":
			_ = h.mailboxes.RemoveFlags(userID, mailbox, uint32(uid), store.FlagSeen)
		case "flag":
			_ = h.mailboxes.AddFlags(userID, mailbox, uint32(uid), store.FlagFlagged)
		case "unflag":
			_ = h.mailboxes.RemoveFlags(userID, mailbox, uint32(uid), store.FlagFlagged)
		case "delete":
			_ = h.mailboxes.AddFlags(userID, mailbox, uint32(uid), store.FlagDeleted)
			_, _ = h.mailboxes.Expunge(userID, mailbox)
		case "move":
			dest := r.FormValue("destination")
			if dest != "" {
				_ = h.mailboxes.MoveMessage(userID, mailbox, uint32(uid), dest)
			}
		case "spam":
			if mailbox != "Junk" {
				_ = h.mailboxes.MoveMessage(userID, mailbox, uint32(uid), "Junk")
			}
			if msg, err := h.mailboxes.GetMessage(userID, mailbox, uint32(uid)); err == nil && h.applier != nil {
				_ = antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, 1.0)
			}
		case "not-spam":
			if mailbox != "INBOX" {
				_ = h.mailboxes.MoveMessage(userID, mailbox, uint32(uid), "INBOX")
			}
			if msg, err := h.mailboxes.GetMessage(userID, mailbox, uint32(uid)); err == nil && h.applier != nil {
				_ = antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, 0.0)
			}
		}
	}

	h.logger.Info("web: bulk action", "action", action, "mailbox", mailbox, "user", userID, "count", len(uidStrs))
	w.WriteHeader(http.StatusOK)
}

// ShowThread returns an HTMX partial with child messages for thread expansion.
func (h *MailHandler) ShowThread(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	rootGUID := chi.URLParam(r, "rootGUID")

	if h.threads == nil {
		http.Error(w, "Threading not available", http.StatusInternalServerError)
		return
	}

	summary, err := h.threads.BuildThread(userID, rootGUID)
	if err != nil {
		http.Error(w, "Thread not found", http.StatusNotFound)
		return
	}

	// Build child summaries (skip root).
	var children []templates.ThreadChild
	for _, node := range summary.AllNodes {
		if node.GUID == summary.Root.GUID {
			continue
		}
		children = append(children, templates.ThreadChild{
			GUID:    node.GUID,
			From:    node.From,
			Subject: node.Subject,
			Date:    time.Unix(node.ReceivedAt, 0),
			Depth:   node.Depth,
			Mailbox: mailbox,
		})
	}

	templ.Handler(templates.ThreadChildren(children, mailbox)).ServeHTTP(w, r)
}

// ImportPage renders the mbox import form.
func (h *MailHandler) ImportPage(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailboxInfos := h.loadMailboxInfos(userID, "")
	var names []string
	for _, mb := range mailboxInfos {
		names = append(names, mb.Name)
	}
	templ.Handler(templates.ImportPage(names)).ServeHTTP(w, r)
}

// ImportMbox processes an uploaded mbox file and imports messages.
func (h *MailHandler) ImportMbox(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	if err := r.ParseMultipartForm(500 << 20); err != nil {
		http.Error(w, "File too large or invalid form", http.StatusBadRequest)
		return
	}

	targetMailbox := r.FormValue("mailbox")
	if newMb := r.FormValue("new_mailbox"); newMb != "" {
		targetMailbox = newMb
	}
	if targetMailbox == "" {
		targetMailbox = "INBOX"
	}

	// Ensure target mailbox exists.
	if _, err := h.mailboxes.GetMailbox(userID, targetMailbox); err != nil {
		_ = h.mailboxes.CreateMailbox(userID, targetMailbox)
	}

	file, _, err := r.FormFile("mbox")
	if err != nil {
		http.Error(w, "No file uploaded", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	// Parse mbox: split on "\nFrom " lines.
	messages := splitMbox(data)

	imported := 0
	failed := 0
	for _, rawMsg := range messages {
		if len(rawMsg) == 0 {
			continue
		}

		// Store raw blob.
		hash, err := h.blobs.Put(rawMsg)
		if err != nil {
			failed++
			continue
		}

		// Parse headers for metadata.
		env, err := enmime.ReadEnvelope(bytes.NewReader(rawMsg))
		if err != nil {
			failed++
			continue
		}

		guid := fmt.Sprintf("import-%d-%s", time.Now().UnixNano(), hash[:8])

		// Parse date.
		receivedAt := time.Now().Unix()
		if dateStr := env.GetHeader("Date"); dateStr != "" {
			for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "Mon, 2 Jan 2006 15:04:05 -0700"} {
				if t, err := time.Parse(layout, dateStr); err == nil {
					receivedAt = t.Unix()
					break
				}
			}
		}

		var toAddrs []string
		if toHeader := env.GetHeader("To"); toHeader != "" {
			for _, addr := range strings.Split(toHeader, ",") {
				addr = strings.TrimSpace(addr)
				if addr != "" {
					toAddrs = append(toAddrs, addr)
				}
			}
		}

		meta := &bongov1.MailMeta{
			Guid:       guid,
			MessageId:  env.GetHeader("Message-ID"),
			Subject:    env.GetHeader("Subject"),
			From:       env.GetHeader("From"),
			To:         toAddrs,
			ReceivedAt: receivedAt,
			RawMessage: &bongov1.BlobRef{
				Hash: hash,
				Size: int64(len(rawMsg)),
			},
		}

		metaBytes, err := proto.Marshal(meta)
		if err != nil {
			failed++
			continue
		}

		cmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
			Value: metaBytes,
		}
		if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			failed++
			continue
		}

		h.deliverToMailbox(userID, targetMailbox, guid, meta)
		imported++
	}

	h.logger.Info("web: mbox imported", "mailbox", targetMailbox, "user", userID, "imported", imported, "failed", failed)

	templ.Handler(templates.ImportResult(imported, failed, targetMailbox)).ServeHTTP(w, r)
}

// deliverToMailbox is the same pattern used by ComposeHandler; duplicated here for MailHandler.
func (h *MailHandler) deliverToMailbox(userID, mailbox, guid string, meta *bongov1.MailMeta) {
	mbMeta, err := h.mailboxes.GetMailbox(userID, mailbox)
	if err != nil {
		return
	}
	uid := mbMeta.UidNext
	mbMeta.UidNext = uid + 1
	metaBytes, _ := proto.Marshal(mbMeta)
	metaCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mailbox/%s/meta", userID, mailbox)),
		Value: metaBytes,
	}
	_ = h.applier.ApplyCommand(metaCmd, 5*time.Second)

	msg := &bongov1.MailboxMessage{
		Uid:          uid,
		MailGuid:     guid,
		Flags:        0,
		InternalDate: meta.ReceivedAt,
	}
	msgBytes, _ := proto.Marshal(msg)
	msgCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/%010d", userID, mailbox, uid)),
		Value: msgBytes,
	}
	_ = h.applier.ApplyCommand(msgCmd, 5*time.Second)
}

// splitMbox splits an mbox byte slice into individual RFC 5322 messages.
// Each message starts with a "From " line at the beginning of a line.
func splitMbox(data []byte) [][]byte {
	var messages [][]byte
	content := string(data)

	// Ensure it starts with "From "
	if !strings.HasPrefix(content, "From ") {
		// Try to find the first "From " line.
		idx := strings.Index(content, "\nFrom ")
		if idx >= 0 {
			content = content[idx+1:]
		}
	}

	parts := strings.Split("\n"+content, "\nFrom ")
	for i, part := range parts {
		if i == 0 {
			continue // empty before first split
		}
		// Find end of "From " line.
		nlIdx := strings.Index(part, "\n")
		if nlIdx < 0 {
			continue
		}
		msgBody := part[nlIdx+1:]
		if len(strings.TrimSpace(msgBody)) == 0 {
			continue
		}
		messages = append(messages, []byte(msgBody))
	}
	return messages
}

// ExportEML downloads a message as a .eml file (raw RFC 5322).
func (h *MailHandler) ExportEML(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
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

	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil || meta.RawMessage == nil || meta.RawMessage.Hash == "" {
		http.Error(w, "Message data not found", http.StatusNotFound)
		return
	}

	rc, err := h.blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		http.Error(w, "Message blob not found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	// Sanitize filename from subject.
	filename := sanitizeFilename(meta.Subject) + ".eml"

	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if meta.RawMessage.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(meta.RawMessage.Size, 10))
	}
	io.Copy(w, rc)
}

// sanitizeFilename removes unsafe characters and limits length for use as a download filename.
func sanitizeFilename(s string) string {
	if s == "" {
		return "message"
	}
	// Replace unsafe characters.
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	s = replacer.Replace(s)
	if len(s) > 100 {
		s = s[:100]
	}
	return strings.TrimSpace(s)
}

// safeInlineTypes lists content types safe for inline display.
var safeInlineTypes = map[string]bool{
	"image/jpeg":       true,
	"image/png":        true,
	"image/gif":        true,
	"image/webp":       true,
	"application/pdf":  true,
	"text/plain":       true,
	"text/csv":         true,
	"text/html":        true,
	"application/json": true,
	"application/xml":  true,
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

	// Determine content type from query parameter or detect from content.
	contentType := r.URL.Query().Get("ct")
	inline := r.URL.Query().Get("inline") == "1"

	if inline && contentType != "" && safeInlineTypes[contentType] {
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("Content-Type", contentType)
		if contentType == "application/pdf" {
			w.Header().Set("Content-Security-Policy", "sandbox")
		}
	} else {
		w.Header().Set("Content-Disposition", "attachment")
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	io.Copy(w, rc)
}

// ToggleFlags handles flag toggling via HTMX.
func (h *MailHandler) ToggleFlags(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
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

	h.logger.Info("web: flag toggled", "flag", flag, "action", action, "mailbox", mailbox, "uid", uid, "user", userID)

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
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	dest := r.FormValue("destination")
	if dest == "" {
		http.Error(w, "Missing destination", http.StatusBadRequest)
		return
	}

	if err := h.mailboxes.MoveMessage(userID, mailbox, uint32(uid), dest); err != nil {
		h.logger.Warn("web: move failed", "mailbox", mailbox, "uid", uid, "dest", dest, "user", userID, "error", err)
		http.Error(w, "Move failed", http.StatusInternalServerError)
		return
	}

	h.logger.Info("web: message moved", "src", mailbox, "dest", dest, "uid", uid, "user", userID)

	// Redirect back to mailbox with flash.
	w.Header().Set("HX-Redirect", fmt.Sprintf("/mail/%s?flash=Moved+to+%s", mailbox, dest))
	w.WriteHeader(http.StatusOK)
}

// messageTagRequest is the JSON body for the UpdateMessageTags endpoint.
type messageTagRequest struct {
	Action string   `json:"action"`
	Tags   []string `json:"tags"`
}

// messageTagListResponse is the JSON body returned by the UpdateMessageTags endpoint.
type messageTagListResponse struct {
	Tags []string `json:"tags"`
}

// UpdateMessageTags handles POST /mail/{mailbox}/{uid}/tags.
// It adds or removes tags on a single message and updates Pebble, SQLite,
// and Bleve. New tag names are auto-created in the TagStore on "add".
func (h *MailHandler) UpdateMessageTags(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, err := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	if err != nil {
		http.Error(w, "Invalid UID", http.StatusBadRequest)
		return
	}

	var req messageTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Action != "add" && req.Action != "remove" {
		http.Error(w, `action must be "add" or "remove"`, http.StatusBadRequest)
		return
	}

	if len(req.Tags) == 0 {
		http.Error(w, "tags must not be empty", http.StatusBadRequest)
		return
	}

	// Validate all tag names up-front.
	for _, name := range req.Tags {
		if err := store.ValidateTagName(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Resolve mailbox message to get the mail GUID.
	msg, err := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}
	mailGUID := msg.MailGuid

	// Load the MailMeta from Pebble.
	meta := h.loadMailMeta(userID, mailGUID)
	if meta == nil {
		http.Error(w, "Message metadata not found", http.StatusInternalServerError)
		return
	}

	switch req.Action {
	case "add":
		// Enforce per-user distinct tag limit for new tag names.
		if h.mailboxes != nil {
			currentDistinct, err := h.mailboxes.CountUserDistinctTags(userID)
			if err != nil {
				h.logger.Warn("web: counting user tags failed", "user", userID, "error", err)
			} else {
				// Count how many of the requested tags are new (not yet used by this user).
				newTagCount := 0
				for _, name := range req.Tags {
					found := false
					for _, existing := range meta.Tags {
						if existing == name {
							found = true
							break
						}
					}
					if !found {
						newTagCount++
					}
				}
				if currentDistinct+newTagCount > store.MaxTagsPerUser {
					http.Error(w, fmt.Sprintf("adding these tags would exceed the per-user limit of %d distinct tags", store.MaxTagsPerUser), http.StatusUnprocessableEntity)
					return
				}
			}
		}

		if err := store.AddTags(meta, req.Tags); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		// Auto-create tag definitions for any new names.
		if h.tags != nil {
			for _, name := range req.Tags {
				if _, err := h.tags.GetTag(userID, name); err != nil {
					// Tag does not exist yet — create it. Ignore error if it was
					// created concurrently between the check and the create.
					if _, cerr := h.tags.CreateTag(userID, name); cerr != nil {
						h.logger.Warn("web: auto-creating tag", "tag", name, "user", userID, "error", cerr)
					}
				}
			}
		}

	case "remove":
		if err := store.RemoveTags(meta, req.Tags); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
	}

	// Persist updated MailMeta via Raft.
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		http.Error(w, "Failed to marshal metadata", http.StatusInternalServerError)
		return
	}
	metaCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, mailGUID)),
		Value: metaBytes,
	}
	if err := h.applier.ApplyCommand(metaCmd, 5*time.Second); err != nil {
		h.logger.Error("web: applying mail meta tag update", "error", err)
		http.Error(w, "Failed to update message", http.StatusInternalServerError)
		return
	}

	// Persist updated MailboxMessage tags via Raft.
	msg.Tags = meta.Tags
	msgBytes, err := proto.Marshal(msg)
	if err != nil {
		h.logger.Error("web: marshaling mailbox message", "error", err)
		// Non-fatal: MailMeta is the source of truth; MailboxMessage tags are a cache.
	} else {
		msgCmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/%010d", userID, mailbox, uint32(uid))),
			Value: msgBytes,
		}
		if err := h.applier.ApplyCommand(msgCmd, 5*time.Second); err != nil {
			h.logger.Warn("web: updating mailbox message tags", "error", err)
		}
	}

	// Update SQLite derived index.
	if h.metadata != nil {
		if err := h.metadata.IndexMailTags(userID, mailGUID, meta.Tags); err != nil {
			h.logger.Warn("web: indexing mail tags in SQLite", "error", err)
		}
	}

	// Update Bleve search index.
	if h.search != nil {
		if err := h.search.IndexMail(
			userID, mailGUID,
			meta.Subject, meta.From, meta.To,
			"", // body re-index not needed for tag-only update
			meta.ReceivedAt,
			len(meta.Attachments) > 0,
			meta.Tags,
			float64(meta.SpamScore),
		); err != nil {
			h.logger.Warn("web: re-indexing mail in bleve after tag update", "error", err)
		}
	}

	h.logger.Info("web: message tags updated", "action", req.Action, "mailbox", mailbox, "uid", uid, "user", userID, "tags", req.Tags)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(messageTagListResponse{Tags: meta.Tags}); err != nil {
		h.logger.Warn("web: encoding tag response", "error", err)
	}
}

// bulkTagMessageID identifies a single message in a bulk tag request.
type bulkTagMessageID struct {
	Mailbox string `json:"mailbox"`
	UID     uint32 `json:"uid"`
}

// bulkTagRequest is the JSON body for the BulkTagAction endpoint.
type bulkTagRequest struct {
	Action     string             `json:"action"`
	Tags       []string           `json:"tags"`
	MessageIDs []bulkTagMessageID `json:"message_ids"`
}

// bulkTagResponse is the JSON body returned by BulkTagAction.
type bulkTagResponse struct {
	Updated int      `json:"updated"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// BulkTagAction handles POST /mail/bulk/tags.
// It adds or removes tags on a set of messages identified by mailbox+uid pairs.
// New tag names are auto-created in the TagStore on "add".
func (h *MailHandler) BulkTagAction(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	var req bulkTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Action != "add" && req.Action != "remove" {
		http.Error(w, `action must be "add" or "remove"`, http.StatusBadRequest)
		return
	}

	if len(req.Tags) == 0 {
		http.Error(w, "tags must not be empty", http.StatusBadRequest)
		return
	}

	if len(req.MessageIDs) == 0 {
		http.Error(w, "message_ids must not be empty", http.StatusBadRequest)
		return
	}

	// Validate all tag names up-front.
	for _, name := range req.Tags {
		if err := store.ValidateTagName(name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Auto-create tag definitions once before the loop (add only).
	if req.Action == "add" && h.tags != nil {
		for _, name := range req.Tags {
			if _, err := h.tags.GetTag(userID, name); err != nil {
				if _, cerr := h.tags.CreateTag(userID, name); cerr != nil {
					h.logger.Warn("web: auto-creating tag in bulk", "tag", name, "user", userID, "error", cerr)
				}
			}
		}
	}

	var updated, failed int
	var errs []string

	for _, msgID := range req.MessageIDs {
		mailbox := msgID.Mailbox
		uid := msgID.UID

		msg, err := h.mailboxes.GetMessage(userID, mailbox, uid)
		if err != nil {
			failed++
			errs = append(errs, fmt.Sprintf("%s/%d: message not found", mailbox, uid))
			continue
		}
		mailGUID := msg.MailGuid

		meta := h.loadMailMeta(userID, mailGUID)
		if meta == nil {
			failed++
			errs = append(errs, fmt.Sprintf("%s/%d: metadata not found", mailbox, uid))
			continue
		}

		switch req.Action {
		case "add":
			if err := store.AddTags(meta, req.Tags); err != nil {
				failed++
				errs = append(errs, fmt.Sprintf("%s/%d: %s", mailbox, uid, err.Error()))
				continue
			}
		case "remove":
			if err := store.RemoveTags(meta, req.Tags); err != nil {
				failed++
				errs = append(errs, fmt.Sprintf("%s/%d: %s", mailbox, uid, err.Error()))
				continue
			}
		}

		// Persist updated MailMeta via Raft.
		metaBytes, err := proto.Marshal(meta)
		if err != nil {
			failed++
			errs = append(errs, fmt.Sprintf("%s/%d: marshal error", mailbox, uid))
			continue
		}
		metaCmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, mailGUID)),
			Value: metaBytes,
		}
		if err := h.applier.ApplyCommand(metaCmd, 5*time.Second); err != nil {
			h.logger.Error("web: applying bulk tag mail meta", "error", err, "mailbox", mailbox, "uid", uid)
			failed++
			errs = append(errs, fmt.Sprintf("%s/%d: store error", mailbox, uid))
			continue
		}

		// Persist updated MailboxMessage tags via Raft.
		msg.Tags = meta.Tags
		if msgBytes, merr := proto.Marshal(msg); merr == nil {
			msgCmd := &bongov1.RaftCommand{
				Op:    bongov1.Op_OP_PUT,
				Key:   []byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/%010d", userID, mailbox, uid)),
				Value: msgBytes,
			}
			if err := h.applier.ApplyCommand(msgCmd, 5*time.Second); err != nil {
				h.logger.Warn("web: bulk tag updating mailbox message", "error", err)
			}
		}

		// Update SQLite derived index.
		if h.metadata != nil {
			if err := h.metadata.IndexMailTags(userID, mailGUID, meta.Tags); err != nil {
				h.logger.Warn("web: bulk tag indexing mail tags in SQLite", "error", err)
			}
		}

		// Update Bleve search index.
		if h.search != nil {
			if err := h.search.IndexMail(
				userID, mailGUID,
				meta.Subject, meta.From, meta.To,
				"",
				meta.ReceivedAt,
				len(meta.Attachments) > 0,
				meta.Tags,
				float64(meta.SpamScore),
			); err != nil {
				h.logger.Warn("web: bulk tag re-indexing mail in bleve", "error", err)
			}
		}

		updated++
	}

	h.logger.Info("web: bulk tag action", "action", req.Action, "tags", req.Tags, "user", userID, "updated", updated, "failed", failed)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(bulkTagResponse{Updated: updated, Failed: failed, Errors: errs}); err != nil {
		h.logger.Warn("web: encoding bulk tag response", "error", err)
	}
}

// DeleteMessage marks a message as deleted and expunges.
func (h *MailHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	_ = h.mailboxes.AddFlags(userID, mailbox, uint32(uid), store.FlagDeleted)
	_, _ = h.mailboxes.Expunge(userID, mailbox)

	h.logger.Info("web: message deleted", "mailbox", mailbox, "uid", uid, "user", userID)

	w.Header().Set("HX-Redirect", fmt.Sprintf("/mail/%s?flash=Message+deleted", mailbox))
	w.WriteHeader(http.StatusOK)
}

// remoteImgRe matches <img ...> tags whose src attribute begins with http:// or https://.
// It deliberately does not match src="cid:..." or src="data:..." values.
var remoteImgRe = regexp.MustCompile(`(?i)<img([^>]*?)src\s*=\s*["'](https?://[^"'>\s]*)["']([^>]*)>`)

// blockRemoteImages replaces remote <img> tags with placeholder divs and
// returns the modified HTML together with a bool indicating whether any
// remote images were found. Images whose src begins with "cid:" or "data:"
// are left untouched.
func blockRemoteImages(html string) (string, bool) {
	blocked := false
	result := remoteImgRe.ReplaceAllStringFunc(html, func(match string) string {
		sub := remoteImgRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		originalURL := sub[2]
		blocked = true
		return fmt.Sprintf(
			`<div class="blocked-image" data-src="%s">[Remote image blocked]</div>`,
			strings.ReplaceAll(originalURL, `"`, `&quot;`),
		)
	})
	return result, blocked
}

// styleBlockRe matches <style>...</style> blocks in HTML.
var styleBlockRe = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)

// cssRuleRe matches simple CSS rules: selector { declarations }.
var cssRuleRe = regexp.MustCompile(`([^{]+)\{([^}]+)\}`)

// inlineStyleBlocks extracts <style> blocks, applies their rules as inline
// style attributes on matching elements, and removes the <style> blocks.
func inlineStyleBlocks(htmlStr string) string {
	// Extract all CSS rules from <style> blocks.
	type cssRule struct {
		selector     string
		declarations string
	}
	var rules []cssRule

	matches := styleBlockRe.FindAllStringSubmatch(htmlStr, -1)
	for _, m := range matches {
		cssContent := m[1]
		ruleMatches := cssRuleRe.FindAllStringSubmatch(cssContent, -1)
		for _, rm := range ruleMatches {
			sel := strings.TrimSpace(rm[1])
			decl := strings.TrimSpace(rm[2])
			// Only handle simple selectors: element, .class, #id.
			// Skip pseudo-classes, combinators, media queries, etc.
			if strings.ContainsAny(sel, ":@+~>[]") {
				continue
			}
			rules = append(rules, cssRule{selector: sel, declarations: decl})
		}
	}

	// Remove <style> blocks.
	htmlStr = styleBlockRe.ReplaceAllString(htmlStr, "")

	if len(rules) == 0 {
		return htmlStr
	}

	// Parse HTML and apply inline styles.
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return htmlStr
	}

	for _, rule := range rules {
		// Handle comma-separated selectors.
		for _, sel := range strings.Split(rule.selector, ",") {
			sel = strings.TrimSpace(sel)
			if sel == "" {
				continue
			}
			applyRuleToNodes(doc, sel, rule.declarations)
		}
	}

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return htmlStr
	}

	// html.Render wraps in <html><head><body>; extract the body content.
	rendered := buf.String()
	if idx := strings.Index(rendered, "<body>"); idx >= 0 {
		rendered = rendered[idx+6:]
		if end := strings.LastIndex(rendered, "</body>"); end >= 0 {
			rendered = rendered[:end]
		}
	}
	return rendered
}

// applyRuleToNodes applies CSS declarations to nodes matching a simple selector.
func applyRuleToNodes(n *html.Node, selector, declarations string) {
	if n.Type == html.ElementNode && matchesSelector(n, selector) {
		// Append to existing style attribute.
		existingStyle := ""
		for i, a := range n.Attr {
			if a.Key == "style" {
				existingStyle = a.Val
				n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
				break
			}
		}
		newStyle := declarations
		if existingStyle != "" {
			// Existing inline styles take precedence (appended last).
			if !strings.HasSuffix(newStyle, ";") {
				newStyle += ";"
			}
			newStyle += " " + existingStyle
		}
		n.Attr = append(n.Attr, html.Attribute{Key: "style", Val: newStyle})
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		applyRuleToNodes(c, selector, declarations)
	}
}

// matchesSelector checks if a node matches a simple CSS selector.
func matchesSelector(n *html.Node, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return false
	}

	switch {
	case strings.HasPrefix(selector, "."):
		// Class selector.
		className := selector[1:]
		for _, a := range n.Attr {
			if a.Key == "class" {
				for _, c := range strings.Fields(a.Val) {
					if c == className {
						return true
					}
				}
			}
		}
	case strings.HasPrefix(selector, "#"):
		// ID selector.
		id := selector[1:]
		for _, a := range n.Attr {
			if a.Key == "id" && a.Val == id {
				return true
			}
		}
	default:
		// Element selector.
		return strings.EqualFold(n.Data, selector)
	}
	return false
}

// decodeMailbox URL-decodes a mailbox name from a chi URL parameter.
// Sidebar links encode '/' as %2F so nested mailboxes like INBOX/Important
// are routed as a single path segment.
func decodeMailbox(encoded string) string {
	if decoded, err := url.PathUnescape(encoded); err == nil {
		return decoded
	}
	return encoded
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
	return LoadMailboxInfos(h.mailboxes, userID, selected)
}

func (h *MailHandler) loadSidebarTags(userID, selected string) []templates.SidebarTagInfo {
	return LoadSidebarTags(h.tags, userID, selected)
}

// backfillThreadingFields parses In-Reply-To, References, and Cc from the raw
// blob and updates the Pebble key via Raft. This handles pre-sprint-08 messages.
func (h *MailHandler) backfillThreadingFields(userID, guid string, meta *bongov1.MailMeta) {
	if h.applier == nil {
		return
	}

	rc, err := h.blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		return
	}
	rawMsg, _ := io.ReadAll(rc)
	rc.Close()

	env, err := enmime.ReadEnvelope(bytes.NewReader(rawMsg))
	if err != nil {
		return
	}

	irt := env.GetHeader("In-Reply-To")
	refs := env.GetHeader("References")
	if irt == "" && refs == "" {
		return
	}

	meta.InReplyTo = irt
	meta.References = refs

	// Also backfill Cc if missing.
	if len(meta.Cc) == 0 {
		if ccHeader := env.GetHeader("Cc"); ccHeader != "" {
			for _, addr := range strings.Split(ccHeader, ",") {
				addr = strings.TrimSpace(addr)
				if addr != "" {
					meta.Cc = append(meta.Cc, addr)
				}
			}
		}
	}

	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		return
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
		Value: metaBytes,
	}
	_ = h.applier.ApplyCommand(cmd, 5*time.Second)
}

// MarkSpam moves a message to Junk, updates spam score to 1.0, and trains Bayes.
func (h *MailHandler) MarkSpam(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	// Get the mail GUID from the mailbox message.
	msg, err := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	// Move to Junk.
	if mailbox != "Junk" {
		if err := h.mailboxes.MoveMessage(userID, mailbox, uint32(uid), "Junk"); err != nil {
			http.Error(w, "Move failed", http.StatusInternalServerError)
			return
		}
	}

	// Update spam score to 1.0.
	if h.applier != nil {
		_ = antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, 1.0)
	}

	// Train Bayes filter.
	if h.bayes != nil {
		h.trainBayes(userID, msg.MailGuid, true)
	}

	h.logger.Info("web: marked as spam", "mailbox", mailbox, "uid", uid, "user", userID)

	w.Header().Set("HX-Redirect", fmt.Sprintf("/mail/%s?flash=Marked+as+spam", mailbox))
	w.WriteHeader(http.StatusOK)
}

// MarkNotSpam moves a message to INBOX, updates spam score to 0.0, and trains Bayes.
func (h *MailHandler) MarkNotSpam(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	msg, err := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	// Move to INBOX.
	if mailbox != "INBOX" {
		if err := h.mailboxes.MoveMessage(userID, mailbox, uint32(uid), "INBOX"); err != nil {
			http.Error(w, "Move failed", http.StatusInternalServerError)
			return
		}
	}

	// Update spam score to 0.0.
	if h.applier != nil {
		_ = antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, 0.0)
	}

	// Train Bayes filter.
	if h.bayes != nil {
		h.trainBayes(userID, msg.MailGuid, false)
	}

	h.logger.Info("web: marked as not spam", "mailbox", mailbox, "uid", uid, "user", userID)

	w.Header().Set("HX-Redirect", fmt.Sprintf("/mail/%s?flash=Marked+as+not+spam", mailbox))
	w.WriteHeader(http.StatusOK)
}

// trainBayes extracts text from a message and trains the Bayes filter.
func (h *MailHandler) trainBayes(userID, guid string, isSpam bool) {
	meta := h.loadMailMeta(userID, guid)
	if meta == nil || meta.RawMessage == nil {
		return
	}

	rc, err := h.blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		return
	}
	rawMsg, _ := io.ReadAll(rc)
	rc.Close()

	env, err := enmime.ReadEnvelope(bytes.NewReader(rawMsg))
	if err != nil {
		return
	}

	subject := env.GetHeader("Subject")
	bodyText := env.Text
	if len(bodyText) > 4096 {
		bodyText = bodyText[:4096]
	}

	tokens := antispam.Tokenize(subject, bodyText)
	_ = h.bayes.Train(userID, tokens, isSpam)
	_ = h.bayes.IncrementTrained(userID)
}
