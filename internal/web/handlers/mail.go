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
const maxMessagePageLimit = int(^uint(0) >> 1)
const importMboxMultipartMaxBytes = 501 << 20

// trashMailbox is the folder deleted messages are moved to. Deleting a message
// that is already in Trash removes it permanently.
const trashMailbox = "Trash"

// ensureTrash makes sure the Trash mailbox exists before a message is moved
// into it (the move FSM does not auto-create the destination). It is cheap on
// the common path: CreateMailbox does an existence check and only writes when
// Trash is missing.
func (h *MailHandler) ensureTrash(userID string) error {
	if err := h.mailboxes.CreateMailbox(userID, trashMailbox); err != nil &&
		!strings.Contains(err.Error(), "already exists") {
		return err
	}
	return nil
}

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
	// Allow a curated set of *presentational* CSS properties on layout elements.
	// AllowStyles validates each value, so url() (tracking-pixel / remote-image
	// blocker bypass) and unknown functions are stripped. position, z-index, and
	// full width/height are deliberately excluded so a mail cannot break out of
	// the message pane to overlay the app chrome (UI-redressing / phishing).
	policy.AllowStyles(
		"color", "background-color",
		"font", "font-weight", "font-style", "font-size", "font-family",
		"text-align", "text-decoration", "line-height", "letter-spacing",
		"margin", "margin-top", "margin-bottom", "margin-left", "margin-right",
		"padding", "padding-top", "padding-bottom", "padding-left", "padding-right",
		"border", "border-color", "border-width", "border-style", "border-radius",
		"vertical-align",
	).OnElements("div", "span", "p", "td", "th", "table", "tr")
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

	msgs, err := h.listDisplayCandidates(userID, mailbox)
	if err != nil {
		http.Error(w, "Error loading messages", http.StatusInternalServerError)
		return
	}

	allSummaries := h.buildMessageSummaries(userID, msgs)
	total := len(allSummaries)
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := (page - 1) * perPage
	summaries := paginateMessageSummaries(allSummaries, offset, perPage)

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
	if len(guidSet) > 0 {
		var err error
		msgs, _, err = h.mailboxes.ListMessagesByGUIDs(userID, guidSet, 0, maxMessagePageLimit)
		if err != nil {
			http.Error(w, "Error loading messages", http.StatusInternalServerError)
			return
		}
	}

	allSummaries := h.buildMessageSummaries(userID, msgs)
	total := len(allSummaries)
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * perPage
	summaries := paginateMessageSummaries(allSummaries, offset, perPage)

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

func (h *MailHandler) listDisplayCandidates(userID, mailbox string) ([]*bongov1.MailboxMessage, error) {
	if store.VirtualMailboxes[mailbox] {
		msgs, _, err := h.mailboxes.ListVirtualMessages(userID, mailbox, 0, maxMessagePageLimit)
		return msgs, err
	}
	msgs, _, err := h.mailboxes.ListMessagesPaged(userID, mailbox, 0, maxMessagePageLimit)
	return msgs, err
}

func (h *MailHandler) buildMessageSummaries(userID string, msgs []*bongov1.MailboxMessage) []templates.MessageSummary {
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

				rootMeta := meta
				if rootGUID != "" && rootGUID != msg.MailGuid {
					if loadedRoot := h.loadMailMeta(userID, rootGUID); loadedRoot != nil {
						rootMeta = loadedRoot
					}
				}
				count := h.threads.GetThreadCount(userID, rootMeta.MessageId)
				if count > 1 {
					summary.ThreadCount = count
				}
			}
		}
		if summary.Subject == "" {
			summary.Subject = "(no subject)"
		}

		h.addTagDots(userID, msg, &summary)
		summaries = append(summaries, summary)
	}

	return summaries
}

func (h *MailHandler) addTagDots(userID string, msg *bongov1.MailboxMessage, summary *templates.MessageSummary) {
	if h.tags == nil || len(msg.Tags) == 0 {
		return
	}

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
			Name:        tagName,
			ColourIndex: colourIdx,
		})
	}
}

func paginateMessageSummaries(all []templates.MessageSummary, offset, limit int) []templates.MessageSummary {
	if offset >= len(all) {
		return nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end]
}

// ShowMessage renders a single message detail view.
func (h *MailHandler) ShowMessage(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, err := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	if err != nil {
		http.Error(w, "Invalid UID", http.StatusBadRequest)
		return
	}

	msg, realMailbox, err := h.resolveMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	// Auto-mark as seen.
	if msg.Flags&store.FlagSeen == 0 {
		if err := h.mailboxes.AddFlags(userID, realMailbox, msg.Uid, store.FlagSeen); err != nil {
			h.logger.Warn("web: failed to auto-mark message seen", "mailbox", realMailbox, "uid", msg.Uid, "user", userID, "error", err)
		}
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

	// Load raw message body and attachments (single MIME parse).
	h.applyEnvelopeBody(&detail, meta)

	detail.Thread = h.loadThreadMessageDetails(userID, msg.MailGuid, realMailbox)

	// Pane mode: return just the message content without layout wrapper.
	if r.URL.Query().Get("pane") == "1" {
		templ.Handler(templates.MessagePaneContent(detail)).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID, mailbox)

	// HTMX click: return just the content, swapped into the existing
	// #main-content. Direct navigation / refresh: return the full document
	// (layout + sidebar + <head>/CSS) so the page keeps its styling.
	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.MessagePage(detail, mailboxInfos)).ServeHTTP(w, r)
		return
	}

	sidebarTags := h.loadSidebarTags(userID, mailbox)
	templ.Handler(templates.MessageFullPage(detail, mailboxInfos, sidebarTags, email)).ServeHTTP(w, r)
}

func (h *MailHandler) loadThreadMessageDetails(userID, currentGUID, currentMailbox string) []templates.MessageDetail {
	if h.threads == nil {
		return nil
	}
	summary, err := h.threads.BuildThread(userID, currentGUID)
	if err != nil || len(summary.AllNodes) <= 1 {
		return nil
	}

	details := make([]templates.MessageDetail, 0, len(summary.AllNodes)-1)
	for _, node := range summary.AllNodes {
		if node.GUID == currentGUID {
			continue
		}
		mailbox, msg := h.findMailboxMessageByGUID(userID, currentMailbox, node.GUID)
		if msg == nil {
			continue
		}
		meta := h.loadMailMeta(userID, msg.MailGuid)
		if meta == nil {
			continue
		}
		details = append(details, h.messageDetailFromMeta(userID, mailbox, msg, meta))
	}
	return details
}

func (h *MailHandler) findMailboxMessageByGUID(userID, preferredMailbox, guid string) (string, *bongov1.MailboxMessage) {
	if preferredMailbox != "" && !store.VirtualMailboxes[preferredMailbox] {
		if msgs, err := h.mailboxes.ListMessages(userID, preferredMailbox); err == nil {
			for _, msg := range msgs {
				if msg.MailGuid == guid {
					return preferredMailbox, msg
				}
			}
		}
	}

	mbs, err := h.mailboxes.ListMailboxes(userID)
	if err != nil {
		return "", nil
	}
	for _, mb := range mbs {
		msgs, err := h.mailboxes.ListMessages(userID, mb.Name)
		if err != nil {
			continue
		}
		for _, msg := range msgs {
			if msg.MailGuid == guid {
				return mb.Name, msg
			}
		}
	}
	return "", nil
}

// applyEnvelopeBody loads the message's raw blob, parses its MIME envelope
// exactly once, and fills detail's body (HTML/text) and attachment list. Any
// proto-recorded attachments not present in the parsed envelope are then merged
// in. This is the single MIME-parse path shared by ShowMessage and the thread
// sibling rendering in messageDetailFromMeta, so each message's envelope is
// parsed at most once per render (previously this logic was duplicated at two
// call sites, each with its own enmime.ReadEnvelope).
func (h *MailHandler) applyEnvelopeBody(detail *templates.MessageDetail, meta *bongov1.MailMeta) {
	if meta.RawMessage != nil && meta.RawMessage.Hash != "" && h.blobs != nil {
		rawMsg, err := store.RawMessageBytes(h.blobs, meta)
		if err == nil {
			env, perr := enmime.ReadEnvelope(bytes.NewReader(rawMsg))
			if perr == nil {
				if env.HTML != "" {
					inlined := inlineStyleBlocks(env.HTML)
					sanitized := h.sanitizer.Sanitize(inlined)
					detail.HTMLBody, detail.HasBlockedImages = blockRemoteImages(sanitized)
				}
				detail.TextBody = env.Text

				for i, att := range env.Attachments {
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
					a.URL = h.attachmentURL(detail.Mailbox, detail.UID, i, a.Hash)
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
				URL:         h.attachmentURL(detail.Mailbox, detail.UID, -1, blobRef.Hash),
			})
		}
	}
}

// attachmentURL returns the download endpoint for an attachment. When the
// attachment has its own content-addressed blob (as sent mail does) it points at
// /mail/blob/{hash}. Inbound mail does not store its parts as separate blobs, so
// it points at the message-scoped route (served by ServeAttachment) which
// extracts the attachment from the raw message by index.
func (h *MailHandler) attachmentURL(mailbox string, uid uint32, index int, hash string) string {
	if hash != "" {
		return "/mail/blob/" + hash
	}
	if index < 0 {
		return ""
	}
	return fmt.Sprintf("/mail/%s/%d/attachment/%d", url.PathEscape(mailbox), uid, index)
}

func (h *MailHandler) messageDetailFromMeta(userID, mailbox string, msg *bongov1.MailboxMessage, meta *bongov1.MailMeta) templates.MessageDetail {
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

	if len(meta.Tags) > 0 && h.tags != nil {
		for _, name := range meta.Tags {
			ti := templates.TagInfo{Name: name}
			if tm, err := h.tags.GetTag(userID, name); err == nil {
				ti.ColourIndex = tm.ColourIndex
			}
			detail.Tags = append(detail.Tags, ti)
		}
	}

	h.applyEnvelopeBody(&detail, meta)
	return detail
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

	targets := h.bulkActionTargets(userID, mailbox, uidStrs, action)
	if len(targets) == 0 && len(uidStrs) > 0 {
		http.Error(w, "Bulk update failed", http.StatusBadRequest)
		return
	}

	for _, target := range targets {
		uid := target.uid
		targetMailbox := target.mailbox

		switch action {
		case "mark-read":
			if err := h.mailboxes.AddFlags(userID, targetMailbox, uid, store.FlagSeen); err != nil {
				http.Error(w, "Bulk update failed", http.StatusInternalServerError)
				return
			}
		case "mark-unread":
			if err := h.mailboxes.RemoveFlags(userID, targetMailbox, uid, store.FlagSeen); err != nil {
				http.Error(w, "Bulk update failed", http.StatusInternalServerError)
				return
			}
		case "flag":
			if err := h.mailboxes.AddFlags(userID, targetMailbox, uid, store.FlagFlagged); err != nil {
				http.Error(w, "Bulk update failed", http.StatusInternalServerError)
				return
			}
		case "unflag":
			if err := h.mailboxes.RemoveFlags(userID, targetMailbox, uid, store.FlagFlagged); err != nil {
				http.Error(w, "Bulk update failed", http.StatusInternalServerError)
				return
			}
		case "delete":
			if targetMailbox == trashMailbox {
				if err := h.mailboxes.AddFlags(userID, targetMailbox, uid, store.FlagDeleted); err != nil {
					http.Error(w, "Bulk delete failed", http.StatusInternalServerError)
					return
				}
				if _, err := h.mailboxes.Expunge(userID, targetMailbox); err != nil {
					http.Error(w, "Bulk delete failed", http.StatusInternalServerError)
					return
				}
			} else {
				if err := h.ensureTrash(userID); err != nil {
					http.Error(w, "Bulk delete failed", http.StatusInternalServerError)
					return
				}
				if err := h.mailboxes.MoveMessage(userID, targetMailbox, uid, trashMailbox); err != nil {
					http.Error(w, "Bulk delete failed", http.StatusInternalServerError)
					return
				}
			}
		case "move":
			dest := r.FormValue("destination")
			if dest != "" {
				if err := h.mailboxes.MoveMessage(userID, targetMailbox, uid, dest); err != nil {
					http.Error(w, "Bulk move failed", http.StatusInternalServerError)
					return
				}
			}
		case "spam":
			if targetMailbox != "Junk" {
				if err := h.mailboxes.MoveMessage(userID, targetMailbox, uid, "Junk"); err != nil {
					http.Error(w, "Bulk spam action failed", http.StatusInternalServerError)
					return
				}
			}
			if msg, err := h.mailboxes.GetMessage(userID, targetMailbox, uid); err == nil && h.applier != nil {
				if err := antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, 1.0); err != nil {
					http.Error(w, "Bulk spam action failed", http.StatusInternalServerError)
					return
				}
			}
		case "not-spam":
			if targetMailbox != "INBOX" {
				if err := h.mailboxes.MoveMessage(userID, targetMailbox, uid, "INBOX"); err != nil {
					http.Error(w, "Bulk not-spam action failed", http.StatusInternalServerError)
					return
				}
			}
			if msg, err := h.mailboxes.GetMessage(userID, targetMailbox, uid); err == nil && h.applier != nil {
				if err := antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, 0.0); err != nil {
					http.Error(w, "Bulk not-spam action failed", http.StatusInternalServerError)
					return
				}
			}
		}
	}

	h.logger.Info("web: bulk action", "action", action, "mailbox", mailbox, "user", userID, "count", len(uidStrs))
	w.WriteHeader(http.StatusOK)
}

type bulkMessageTarget struct {
	mailbox string
	uid     uint32
}

func (h *MailHandler) bulkActionTargets(userID, mailbox string, uidStrs []string, action string) []bulkMessageTarget {
	targets := make([]bulkMessageTarget, 0, len(uidStrs))
	seen := make(map[string]bool)
	addTarget := func(mailbox string, uid uint32) {
		key := fmt.Sprintf("%s:%d", mailbox, uid)
		if seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, bulkMessageTarget{mailbox: mailbox, uid: uid})
	}

	for _, uidStr := range uidStrs {
		uid64, err := strconv.ParseUint(uidStr, 10, 32)
		if err != nil {
			continue
		}
		uid := uint32(uid64)

		if action != "delete" || h.threads == nil {
			addTarget(mailbox, uid)
			continue
		}

		msg, realMailbox, err := h.messageForMailboxUID(userID, mailbox, uid)
		if err != nil || msg == nil {
			addTarget(mailbox, uid)
			continue
		}

		summary, err := h.threads.BuildThread(userID, msg.MailGuid)
		if err != nil || len(summary.AllNodes) <= 1 {
			addTarget(realMailbox, uid)
			continue
		}
		for _, node := range summary.AllNodes {
			nodeMailbox, nodeMsg := h.findMailboxMessageByGUID(userID, realMailbox, node.GUID)
			if nodeMsg == nil {
				continue
			}
			addTarget(nodeMailbox, nodeMsg.Uid)
		}
	}
	return targets
}

func (h *MailHandler) messageForMailboxUID(userID, mailbox string, uid uint32) (*bongov1.MailboxMessage, string, error) {
	if store.VirtualMailboxes[mailbox] {
		msg, realMailbox, err := h.mailboxes.GetMessageVirtual(userID, mailbox, uid)
		return msg, realMailbox, err
	}
	msg, err := h.mailboxes.GetMessage(userID, mailbox, uid)
	return msg, mailbox, err
}

// EmptyTrash permanently removes every message currently in Trash.
func (h *MailHandler) EmptyTrash(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if err := h.ensureTrash(userID); err != nil {
		http.Error(w, "Empty Trash failed", http.StatusInternalServerError)
		return
	}
	msgs, err := h.mailboxes.ListMessages(userID, trashMailbox)
	if err != nil {
		http.Error(w, "Empty Trash failed", http.StatusInternalServerError)
		return
	}
	for _, msg := range msgs {
		if err := h.mailboxes.AddFlags(userID, trashMailbox, msg.Uid, store.FlagDeleted); err != nil {
			http.Error(w, "Empty Trash failed", http.StatusInternalServerError)
			return
		}
	}
	if _, err := h.mailboxes.Expunge(userID, trashMailbox); err != nil {
		http.Error(w, "Empty Trash failed", http.StatusInternalServerError)
		return
	}

	h.logger.Info("web: emptied trash", "user", userID, "count", len(msgs))
	w.Header().Set("HX-Redirect", "/mail/Trash?flash=Trash+emptied")
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

	r.Body = http.MaxBytesReader(w, r.Body, importMboxMultipartMaxBytes)
	if err := r.ParseMultipartForm(500 << 20); err != nil { // #nosec G120 -- request body is capped by MaxBytesReader above
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
		if err := h.mailboxes.CreateMailbox(userID, targetMailbox); err != nil {
			http.Error(w, "Failed to create target mailbox", http.StatusInternalServerError)
			return
		}
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

		if _, err := h.mailboxes.DeliverMessage(userID, targetMailbox, meta, 0); err != nil {
			failed++
			continue
		}
		imported++
	}

	h.logger.Info("web: mbox imported", "mailbox", targetMailbox, "user", userID, "imported", imported, "failed", failed)

	templ.Handler(templates.ImportResult(imported, failed, targetMailbox)).ServeHTTP(w, r)
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

	msg, _, err := h.resolveMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil || meta.RawMessage == nil || meta.RawMessage.Hash == "" {
		http.Error(w, "Message data not found", http.StatusNotFound)
		return
	}

	rawMsg, err := store.RawMessageBytes(h.blobs, meta)
	if err != nil {
		http.Error(w, "Message blob not found", http.StatusNotFound)
		return
	}

	// Sanitize filename from subject.
	filename := sanitizeFilename(meta.Subject) + ".eml"

	w.Header().Set("Content-Type", "message/rfc822")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	// Length of the reconstructed message (skeleton size would be wrong for split records).
	w.Header().Set("Content-Length", strconv.FormatInt(int64(len(rawMsg)), 10))
	if _, err := w.Write(rawMsg); err != nil {
		return
	}
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

// safeInlineTypes are rendered directly by the browser (via <img>/<iframe>),
// so they may be served with Content-Disposition: inline. text/html is
// intentionally absent: rendering an attacker-supplied HTML attachment inline
// would execute its scripts in our own origin with the user's session (stored
// XSS). HTML attachments are always served as downloads.
var safeInlineTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/gif":       true,
	"image/webp":      true,
	"application/pdf": true,
}

// inlineTextTypes are shown as text previews that htmx swaps into the page DOM
// (hx-swap="innerHTML"). Their bytes are served HTML-escaped so untrusted
// markup can never execute in our origin. text/html is intentionally absent.
var inlineTextTypes = map[string]bool{
	"text/plain":       true,
	"text/csv":         true,
	"application/json": true,
	"application/xml":  true,
}

// ServeBlob streams a blob with the correct content type.
func (h *MailHandler) ServeBlob(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !store.IsValidHash(hash) {
		http.Error(w, "invalid blob hash", http.StatusBadRequest)
		return
	}

	// Blobs are content-addressable and shared across all users, so the hash
	// alone is not an authorization token: require that the requesting user
	// actually owns a message referencing this blob. Otherwise any
	// authenticated user could read another user's attachments by hash. A 404
	// (not 403) avoids confirming that the blob exists.
	userID, _ := middleware.UserFromContext(r.Context())
	if userID == "" || !h.userOwnsBlob(userID, hash) {
		http.Error(w, "Blob not found", http.StatusNotFound)
		return
	}

	rc, err := h.blobs.Get(hash)
	if err != nil {
		http.Error(w, "Blob not found", http.StatusNotFound)
		return
	}
	defer rc.Close()

	// Determine content type from query parameter or detect from content.
	contentType := r.URL.Query().Get("ct")
	inline := r.URL.Query().Get("inline") == "1"

	// Never let the browser MIME-sniff a blob into an executable type (e.g. a
	// text/plain attachment whose bytes look like HTML); serve exactly the
	// declared type.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")

	switch {
	case inline && inlineTextTypes[contentType]:
		// The text preview is swapped into the authenticated page via
		// hx-swap="innerHTML", so raw markup would execute in our origin.
		// Serve the content HTML-escaped inside a <pre>.
		data, err := io.ReadAll(rc)
		if err != nil {
			http.Error(w, "Blob not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<pre class=\"attachment-text\">%s</pre>", html.EscapeString(string(data)))
	case inline && contentType != "" && safeInlineTypes[contentType]:
		// Images and PDFs are rendered by <img>/<iframe>, not innerHTML-swapped.
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("Content-Type", contentType)
		if contentType == "application/pdf" {
			w.Header().Set("Content-Security-Policy", "sandbox")
		}
		if _, err := io.Copy(w, rc); err != nil {
			return
		}
	default:
		// Force a download with a non-renderable type so the browser never
		// interprets the bytes (and does not content-sniff them into HTML).
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := io.Copy(w, rc); err != nil {
			return
		}
	}
}

// ServeAttachment streams a single attachment extracted from a message's raw
// blob by index. Inbound mail does not store its attachments as separate
// content-addressed blobs, so they cannot be served via /mail/blob/{hash};
// this route parses the owning message's raw content and returns part {idx}.
// Ownership is enforced by resolving the message for the authenticated user, so
// a user can only reach attachments of their own messages. The bytes are always
// served as an octet-stream download so the browser never interprets them.
func (h *MailHandler) ServeAttachment(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, err := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)
	if err != nil {
		http.Error(w, "Invalid UID", http.StatusBadRequest)
		return
	}
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil || idx < 0 {
		http.Error(w, "Invalid attachment", http.StatusBadRequest)
		return
	}

	msg, _, err := h.resolveMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}
	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil || meta.RawMessage == nil || meta.RawMessage.Hash == "" || h.blobs == nil {
		http.Error(w, "Attachment not found", http.StatusNotFound)
		return
	}

	rawMsg, err := store.RawMessageBytes(h.blobs, meta)
	if err != nil {
		http.Error(w, "Attachment not found", http.StatusNotFound)
		return
	}

	env, err := enmime.ReadEnvelope(bytes.NewReader(rawMsg))
	if err != nil || idx >= len(env.Attachments) {
		http.Error(w, "Attachment not found", http.StatusNotFound)
		return
	}
	att := env.Attachments[idx]

	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Inline display is opt-in (?inline=1) and restricted to the safe raster
	// image and PDF allowlist — never SVG or arbitrary types — so an
	// attacker-supplied attachment is never interpreted as active content. The
	// content type comes from the parsed message (server-authoritative), not a
	// client-supplied query parameter. Everything else is an octet-stream
	// download; the filename is supplied by the link's download attribute, so no
	// user-controlled value reaches a response header.
	if r.URL.Query().Get("inline") == "1" && safeInlineTypes[att.ContentType] {
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("Content-Type", att.ContentType)
		if att.ContentType == "application/pdf" {
			w.Header().Set("Content-Security-Policy", "sandbox")
		}
		if _, err := w.Write(att.Content); err != nil {
			return
		}
		return
	}

	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := w.Write(att.Content); err != nil {
		return
	}
}

// ToggleFlags handles flag toggling via HTMX.
func (h *MailHandler) ToggleFlags(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	action := r.FormValue("action")
	flag := r.FormValue("flag")

	// Resolve virtual mailboxes to the real mailbox before writing flags.
	realMailbox := mailbox
	if store.VirtualMailboxes[mailbox] {
		_, rm, err := h.resolveMessage(userID, mailbox, uint32(uid))
		if err != nil {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		realMailbox = rm
	}

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
		if err := h.mailboxes.AddFlags(userID, realMailbox, uint32(uid), flagBit); err != nil {
			http.Error(w, "Failed to update flag", http.StatusInternalServerError)
			return
		}
	case "remove":
		if err := h.mailboxes.RemoveFlags(userID, realMailbox, uint32(uid), flagBit); err != nil {
			http.Error(w, "Failed to update flag", http.StatusInternalServerError)
			return
		}
	}

	h.logger.Info("web: flag toggled", "flag", flag, "action", action, "mailbox", mailbox, "uid", uid, "user", userID)

	// Return updated star icon.
	msg, _ := h.mailboxes.GetMessage(userID, realMailbox, uint32(uid))
	flagged := msg != nil && msg.Flags&store.FlagFlagged != 0
	postPath := fmt.Sprintf("/mail/%s/%d/flags", url.PathEscape(mailbox), uid)

	if flagged {
		fmt.Fprintf(w, `<span class="flag-star flagged" hx-post="%s" hx-vals='{"action":"remove","flag":"flagged"}' hx-swap="outerHTML">&#9733;</span>`, postPath)
	} else {
		fmt.Fprintf(w, `<span class="flag-star" hx-post="%s" hx-vals='{"action":"add","flag":"flagged"}' hx-swap="outerHTML">&#9734;</span>`, postPath)
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

	realMailbox := mailbox
	if store.VirtualMailboxes[mailbox] {
		_, rm, err := h.resolveMessage(userID, mailbox, uint32(uid))
		if err != nil {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		realMailbox = rm
	}

	if err := h.mailboxes.MoveMessage(userID, realMailbox, uint32(uid), dest); err != nil {
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
	msg, realMailbox, err := h.resolveMessage(userID, mailbox, uint32(uid))
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
			Key:   []byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/%010d", userID, realMailbox, uint32(uid))),
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

	realMailbox := mailbox
	if store.VirtualMailboxes[mailbox] {
		_, rm, err := h.resolveMessage(userID, mailbox, uint32(uid))
		if err != nil {
			http.Error(w, "Message not found", http.StatusNotFound)
			return
		}
		realMailbox = rm
	}

	flash := "Message+moved+to+Trash"
	if realMailbox == trashMailbox {
		// Already in Trash: remove permanently.
		if err := h.mailboxes.AddFlags(userID, realMailbox, uint32(uid), store.FlagDeleted); err != nil {
			http.Error(w, "failed to mark message deleted", http.StatusInternalServerError)
			return
		}
		if _, err := h.mailboxes.Expunge(userID, realMailbox); err != nil {
			http.Error(w, "failed to delete message", http.StatusInternalServerError)
			return
		}
		flash = "Message+permanently+deleted"
		h.logger.Info("web: message permanently deleted", "mailbox", realMailbox, "uid", uid, "user", userID)
	} else {
		// Move to Trash instead of permanently deleting.
		if err := h.ensureTrash(userID); err != nil {
			http.Error(w, "failed to prepare Trash", http.StatusInternalServerError)
			return
		}
		if err := h.mailboxes.MoveMessage(userID, realMailbox, uint32(uid), trashMailbox); err != nil {
			http.Error(w, "failed to move message to Trash", http.StatusInternalServerError)
			return
		}
		h.logger.Info("web: message moved to Trash", "mailbox", realMailbox, "uid", uid, "user", userID)
	}

	w.Header().Set("HX-Redirect", fmt.Sprintf("/mail/%s?flash=%s", mailbox, flash))
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

// resolveMessage returns the mailbox message and the real mailbox name.
// For virtual mailboxes (unread/starred/all-mail) it scans all real mailboxes.
func (h *MailHandler) resolveMessage(userID, mailbox string, uid uint32) (*bongov1.MailboxMessage, string, error) {
	if store.VirtualMailboxes[mailbox] {
		return h.mailboxes.GetMessageVirtual(userID, mailbox, uid)
	}
	msg, err := h.mailboxes.GetMessage(userID, mailbox, uid)
	return msg, mailbox, err
}

// userOwnsBlob reports whether the user has a message whose raw content or an
// attachment references the given blob hash. Used to authorize blob downloads:
// blobs are shared/deduplicated across users, so ownership must be proven per
// request rather than trusting the hash. Returns as soon as a match is found.
func (h *MailHandler) userOwnsBlob(userID, hash string) bool {
	iter, err := h.pebble.PrefixScan([]byte(fmt.Sprintf("/user/%s/mail/", userID)))
	if err != nil {
		return false
	}
	defer iter.Close()

	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			continue
		}
		if meta.RawMessage != nil && meta.RawMessage.Hash == hash {
			return true
		}
		for _, att := range meta.Attachments {
			if att.Hash == hash {
				return true
			}
		}
	}
	return false
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

	rawMsg, err := store.RawMessageBytes(h.blobs, meta)
	if err != nil {
		return
	}

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
	if err := h.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		h.logger.Warn("web: failed to backfill message references", "user", userID, "guid", guid, "error", err)
	}
}

// MarkSpam moves a message to Junk, updates spam score to 1.0, and trains Bayes.
func (h *MailHandler) MarkSpam(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	// Get the mail GUID from the mailbox message.
	msg, realMailbox, err := h.resolveMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	// Move to Junk.
	if realMailbox != "Junk" {
		if err := h.mailboxes.MoveMessage(userID, realMailbox, uint32(uid), "Junk"); err != nil {
			http.Error(w, "Move failed", http.StatusInternalServerError)
			return
		}
	}

	// Update spam score to 1.0.
	if h.applier != nil {
		if err := antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, 1.0); err != nil {
			h.logger.Warn("web: failed to update spam score", "mailbox", mailbox, "uid", uid, "user", userID, "error", err)
		}
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

	msg, realMailbox, err := h.resolveMessage(userID, mailbox, uint32(uid))
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	// Move to INBOX.
	if realMailbox != "INBOX" {
		if err := h.mailboxes.MoveMessage(userID, realMailbox, uint32(uid), "INBOX"); err != nil {
			http.Error(w, "Move failed", http.StatusInternalServerError)
			return
		}
	}

	// Update spam score to 0.0.
	if h.applier != nil {
		if err := antispam.UpdateSpamScore(h.pebble, h.applier, userID, msg.MailGuid, 0.0); err != nil {
			h.logger.Warn("web: failed to clear spam score", "mailbox", mailbox, "uid", uid, "user", userID, "error", err)
		}
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

	rawMsg, err := store.RawMessageBytes(h.blobs, meta)
	if err != nil {
		return
	}

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
	if err := h.bayes.Train(userID, tokens, isSpam); err != nil {
		h.logger.Warn("web: bayes training failed", "user", userID, "guid", guid, "spam", isSpam, "error", err)
		return
	}
	if err := h.bayes.IncrementTrained(userID); err != nil {
		h.logger.Warn("web: bayes trained counter update failed", "user", userID, "guid", guid, "spam", isSpam, "error", err)
	}
}
