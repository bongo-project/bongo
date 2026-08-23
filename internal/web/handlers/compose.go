package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/mail"
	"github.com/bongo-go/bongo-go/internal/queue"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/sanitize"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const composeMultipartMaxBytes = 33 << 20

// localRecipientResolver resolves an email address to a local user ID.
// Defined at the consumer (ComposeHandler) to avoid an import cycle with
// internal/smtp. Satisfied by *store.RecipientResolver.
type localRecipientResolver interface {
	ResolveRecipient(email string) (string, error)
}

// ComposeHandler handles email composition.
type ComposeHandler struct {
	pebble      *store.PebbleStore
	blobs       *store.BlobStore
	mailboxes   *store.MailboxStore
	search      *store.SearchManager
	applier     store.RaftApplier
	queueStore  *queue.QueueStore
	rateLimiter *mail.RateLimiter
	prefs       *store.PreferenceStore
	resolver    localRecipientResolver // unified address→userID resolver
	addresses   *store.AddressStore    // for send-as From picker and enforcement
	logger      *slog.Logger

	// dedupMinSize is the attachment-dedup threshold. > 0 stores sent mail as a
	// skeleton + deduplicated attachment blobs; <= 0 keeps whole-raw.
	dedupMinSize int
}

// SetAttachmentDedup sets the minimum encoded-attachment size for splitting sent
// mail into deduplicated blobs. A non-positive value disables it.
func (h *ComposeHandler) SetAttachmentDedup(minSize int) { h.dedupMinSize = minSize }

// NewComposeHandler creates a ComposeHandler.
func NewComposeHandler(pebble *store.PebbleStore, blobs *store.BlobStore, mailboxes *store.MailboxStore, search *store.SearchManager, applier store.RaftApplier, logger *slog.Logger) *ComposeHandler {
	return &ComposeHandler{
		pebble:    pebble,
		blobs:     blobs,
		mailboxes: mailboxes,
		search:    search,
		applier:   applier,
		logger:    logger,
	}
}

// SetQueueStore sets the queue store for outbound delivery.
func (h *ComposeHandler) SetQueueStore(qs *queue.QueueStore) {
	h.queueStore = qs
}

// SetRateLimiter sets the rate limiter for outbound messages.
func (h *ComposeHandler) SetRateLimiter(rl *mail.RateLimiter) {
	h.rateLimiter = rl
}

// SetPreferences sets the preference store for per-user settings.
func (h *ComposeHandler) SetPreferences(prefs *store.PreferenceStore) {
	h.prefs = prefs
}

// SetLocalResolver injects the shared recipient resolver used by resolveLocalUser.
// When nil, resolveLocalUser treats every address as non-local, so it must not be
// left nil in production.
func (h *ComposeHandler) SetLocalResolver(r localRecipientResolver) {
	h.resolver = r
}

// SetAddressStore injects the address store for the send-as From picker and
// server-side enforcement. When nil, the picker shows only the session email and
// send-as enforcement is bypassed (for edge/pre-migration nodes).
func (h *ComposeHandler) SetAddressStore(as *store.AddressStore) {
	h.addresses = as
}

// buildFromPickerOptions returns the list of addresses the authenticated user
// may send from (those owned by userID with CanSend=true AND Verified=true),
// plus the currently selected address (defaults to the session primary email).
//
// When the address store is nil or ListByUser returns no results, a single-entry
// slice containing primaryEmail is returned so the template hides the picker and
// the send path falls back to the session email as before.
func (h *ComposeHandler) buildFromPickerOptions(userID, primaryEmail string) (options []string, selected string) {
	primaryLower := strings.ToLower(primaryEmail)
	selected = primaryLower

	if h.addresses == nil {
		return []string{primaryEmail}, selected
	}

	addrs, err := h.addresses.ListByUser(userID)
	if err != nil || len(addrs) == 0 {
		return []string{primaryEmail}, selected
	}

	// Filter: keep only addresses owned by this user with CanSend && Verified.
	// Always check via Pebble (Get) — SQLite's can_send/verified columns may be stale.
	for _, addr := range addrs {
		rec, ok, err := h.addresses.Get(addr)
		if err != nil || !ok {
			continue
		}
		if rec.UserId != userID || !rec.CanSend || !rec.Verified {
			continue
		}
		options = append(options, strings.ToLower(addr))
	}

	// Ensure the primary email is always in the list (safety net for pre-migration
	// nodes where the primary might not yet be indexed in SQLite).
	hasPrimary := false
	for _, opt := range options {
		if strings.EqualFold(opt, primaryEmail) {
			hasPrimary = true
			break
		}
	}
	if !hasPrimary {
		options = append([]string{primaryLower}, options...)
	}

	if len(options) == 0 {
		return []string{primaryEmail}, selected
	}
	return options, selected
}

// NewMessage renders the compose form.
func (h *ComposeHandler) NewMessage(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	body := h.signatureBody(userID)
	fromOpts, fromSel := h.buildFromPickerOptions(userID, email)

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("new", "", "", "", body, "", 0, fromOpts, fromSel, "")).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("new", "", "", "", body, "", 0, mailboxInfos, nil, email, fromOpts, fromSel, "")).ServeHTTP(w, r)
}

// Reply renders the reply form with pre-filled fields.
func (h *ComposeHandler) Reply(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	msg, _ := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	if msg == nil {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}

	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}

	subject := meta.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	body := h.signatureBody(userID) + h.loadQuotedBody(userID, meta)

	// Determine reply recipients based on default_reply preference.
	replyTo := meta.From
	var replyCc string
	if h.prefs != nil {
		up := h.prefs.Get(userID)
		if up.DefaultReply == bongov1.DefaultReplyMode_REPLY_MODE_ALL {
			// Cc = original To + original Cc minus the current user.
			_, currentEmail := middleware.UserFromContext(r.Context())
			var ccAddrs []string
			for _, addr := range meta.To {
				if !strings.EqualFold(strings.TrimSpace(addr), currentEmail) {
					ccAddrs = append(ccAddrs, strings.TrimSpace(addr))
				}
			}
			for _, addr := range meta.Cc {
				if !strings.EqualFold(strings.TrimSpace(addr), currentEmail) {
					ccAddrs = append(ccAddrs, strings.TrimSpace(addr))
				}
			}
			replyCc = strings.Join(ccAddrs, ", ")
		}
	}

	_, replyEmail := middleware.UserFromContext(r.Context())
	fromOpts, fromSel := h.buildFromPickerOptions(userID, replyEmail)

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("reply", replyTo, replyCc, subject, body, mailbox, uint32(uid), fromOpts, fromSel, "")).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("reply", replyTo, replyCc, subject, body, mailbox, uint32(uid), mailboxInfos, nil, replyEmail, fromOpts, fromSel, "")).ServeHTTP(w, r)
}

// Forward renders the forward form.
func (h *ComposeHandler) Forward(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	msg, _ := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	if msg == nil {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}

	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}

	subject := meta.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "fwd:") {
		subject = "Fwd: " + subject
	}

	body := h.signatureBody(userID) + h.loadQuotedBody(userID, meta)

	_, fwdEmail := middleware.UserFromContext(r.Context())
	fromOpts, fromSel := h.buildFromPickerOptions(userID, fwdEmail)

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("forward", "", "", subject, body, mailbox, uint32(uid), fromOpts, fromSel, "")).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("forward", "", "", subject, body, mailbox, uint32(uid), mailboxInfos, nil, fwdEmail, fromOpts, fromSel, "")).ServeHTTP(w, r)
}

// Send processes the compose form and delivers the message.
func (h *ComposeHandler) Send(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, composeMultipartMaxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- request body is capped by MaxBytesReader above
		if err2 := r.ParseForm(); err2 != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
	}

	to := r.FormValue("to")
	cc := r.FormValue("cc")
	subject := r.FormValue("subject")
	body := r.FormValue("body")
	mode := r.FormValue("mode")
	replyMailbox := r.FormValue("reply_mailbox")
	replyUIDStr := r.FormValue("reply_uid")

	if to == "" || subject == "" {
		http.Error(w, "To and Subject are required", http.StatusBadRequest)
		return
	}

	// Resolve the effective From address.
	//
	// Back-compat gate: if the submitted "from" is empty or equals the session
	// email (case-insensitive), use the session email directly and skip
	// AddressStore enforcement. This preserves behaviour on edge/agent nodes and
	// pre-migration clusters where the primary may not yet be indexed.
	//
	// Enforcement runs ONLY when "from" differs from the session email, which is
	// the tamper/send-as case. This must happen before any blob/Sent/queue writes.
	fromAddr := email
	chosenFrom := strings.ToLower(strings.TrimSpace(r.FormValue("from")))
	if chosenFrom != "" && !strings.EqualFold(chosenFrom, email) {
		if h.addresses == nil {
			http.Error(w, "Send-as is not available on this node", http.StatusForbidden)
			return
		}
		rec, ok, err := h.addresses.Get(chosenFrom)
		switch {
		case err != nil:
			http.Error(w, "Address lookup failed", http.StatusInternalServerError)
			return
		case !ok:
			http.Error(w, "Forbidden: address not found", http.StatusForbidden)
			return
		case rec.UserId != userID:
			http.Error(w, "Forbidden: address not owned by your account", http.StatusForbidden)
			return
		case !rec.CanSend || !rec.Verified:
			http.Error(w, "Forbidden: address is not authorised for sending", http.StatusForbidden)
			return
		}
		fromAddr = chosenFrom
	}

	// Build threading headers for replies.
	var inReplyTo, references string
	if mode == "reply" && replyMailbox != "" && replyUIDStr != "" {
		replyUID, _ := strconv.ParseUint(replyUIDStr, 10, 32)
		if replyUID > 0 {
			if replyMsg, _ := h.mailboxes.GetMessage(userID, replyMailbox, uint32(replyUID)); replyMsg != nil {
				origMeta := h.loadMailMeta(userID, replyMsg.MailGuid)
				if origMeta != nil && origMeta.MessageId != "" {
					inReplyTo = origMeta.MessageId
					if origMeta.References != "" {
						references = origMeta.References + " " + origMeta.MessageId
					} else {
						references = origMeta.MessageId
					}
				}
			}
		}
	}

	// Process file attachments. The payloads are assembled into the RFC 5322
	// message below; they are NOT stored as standalone decoded blobs. When the
	// message is split, the encoded spans become the deduplicated attachment
	// blobs; otherwise the whole raw is stored. attachmentMeta carries display
	// metadata (filename/size/type, no hash) used only in the whole-raw case,
	// where the attachment is served by reconstructing from the raw.
	var attachmentMeta []*bongov1.BlobRef
	var attachmentFiles []attachmentData
	if r.MultipartForm != nil && r.MultipartForm.File["attachments"] != nil {
		for _, fh := range r.MultipartForm.File["attachments"] {
			if fh.Size > 25<<20 {
				http.Error(w, "Individual attachment must be under 25MB", http.StatusBadRequest)
				return
			}
			f, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				continue
			}
			contentType := fh.Header.Get("Content-Type")
			if contentType == "" || contentType == "application/octet-stream" {
				contentType = http.DetectContentType(data)
			}
			attachmentMeta = append(attachmentMeta, &bongov1.BlobRef{
				Size:        fh.Size,
				ContentType: contentType,
				Filename:    fh.Filename,
			})
			attachmentFiles = append(attachmentFiles, attachmentData{
				Filename:    fh.Filename,
				ContentType: contentType,
				Data:        data,
			})
		}
	}

	// Generate a single Message-ID scoped to the sender's domain, used both in
	// the outgoing message header and the stored copy's metadata so threading
	// stays consistent. A non-FQDN Message-ID (the old "@bongo") is a negative
	// deliverability signal. The host is derived from fromAddr (the chosen From)
	// so that Message-ID host aligns with the From domain.
	messageID := fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), randomToken(), senderDomain(fromAddr))

	// Build RFC 5322 message. body_mode='rich' → multipart/alternative with
	// sanitized HTML and derived plain text. Any other value (or absent) falls
	// through to the plain-text path.
	// fromAddr (not email) is used as the From header so DKIM signing via the
	// registry (which keys on the From-header domain) automatically uses the
	// correct d= value without any additional signing code.
	bodyMode := r.FormValue("body_mode")
	var rawMsg []byte
	var searchBody string
	if bodyMode == "rich" {
		var buildErr error
		rawMsg, searchBody, buildErr = h.buildRichMessage(fromAddr, to, cc, subject, body, userID, messageID, inReplyTo, references, attachmentFiles, true)
		if buildErr != nil {
			// Reject before any user-visible writes (blob store, Sent copy, enqueue),
			// mirroring the attachment-size rejection above.
			// Map the lowercase sentinel errors to friendly, capitalized 400 messages.
			msg := "Message could not be built"
			switch {
			case errors.Is(buildErr, errInlineImageTooLarge):
				msg = "Inline image too large (max 1MB each)"
			case errors.Is(buildErr, errTotalInlineTooLarge):
				msg = "Total inline images too large (max 10MB)"
			}
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
	} else {
		rawMsg = buildRFC5322WithAttachments(fromAddr, to, cc, subject, body, messageID, inReplyTo, references, attachmentFiles)
		searchBody = body
	}

	// Store the message: a skeleton + deduplicated attachment blobs when it
	// carries a large attachment, else the whole raw. StoreSplit self-checks
	// byte-exact reconstruction; on any split error we fall back to whole-raw so
	// a message is never stored in a form that cannot be reproduced.
	rawHash := ""
	metaAttachments := attachmentMeta
	var layout []*bongov1.MessageSegment
	if split, serr := store.StoreSplit(h.blobs, rawMsg, h.dedupMinSize); split != nil {
		rawHash = split.SkeletonHash
		metaAttachments = split.Attachments
		layout = split.Layout
		h.logger.Info("compose: attachment dedup",
			"user", userID,
			"attachments", len(split.Attachments),
			"blobs_written", split.BlobsWritten,
			"blobs_deduped", split.BlobsDeduped,
			"bytes_saved", split.BytesSaved)
	} else {
		if serr != nil {
			h.logger.Warn("compose: attachment split failed, storing whole raw", "user", userID, "error", serr)
		}
		wh, err := h.blobs.Put(rawMsg)
		if err != nil {
			http.Error(w, "Failed to store message", http.StatusInternalServerError)
			return
		}
		rawHash = wh
	}

	guid := fmt.Sprintf("%d-%s", time.Now().UnixNano(), rawHash[:8])

	// Create MailMeta. meta.From is set to fromAddr (the chosen From address)
	// so the stored copy reflects the actual sender identity.
	meta := &bongov1.MailMeta{
		Guid:       guid,
		MessageId:  messageID,
		Subject:    subject,
		From:       fromAddr,
		To:         splitRecipients(to),
		Cc:         splitRecipients(cc),
		ReceivedAt: time.Now().Unix(),
		RawMessage: &bongov1.BlobRef{
			Hash: rawHash,
			Size: int64(len(rawMsg)),
		},
		Attachments: metaAttachments,
		Layout:      layout,
		InReplyTo:   inReplyTo,
		References:  references,
	}

	// Split recipients into local and external before any user-visible writes
	// so pre-delivery rejection does not leave a partial Sent copy behind.
	allRecipients := append(splitRecipients(to), splitRecipients(cc)...)
	var localRecipients, externalRecipients []string
	for _, rcpt := range allRecipients {
		// Reduce to the bare local@domain address: the To/Cc fields may carry a
		// display name or angle brackets ("Lance <a@b.com>"), which must not
		// leak into the SMTP envelope or the MX-lookup domain ("b.com>").
		rcpt = normalizeRecipient(rcpt)
		if rcpt == "" {
			continue
		}
		if h.resolveLocalUser(rcpt) != "" {
			localRecipients = append(localRecipients, rcpt)
		} else {
			externalRecipients = append(externalRecipients, rcpt)
		}
	}

	// Check rate limit for external recipients before persisting sender-visible state.
	if len(externalRecipients) > 0 && h.rateLimiter != nil && !h.rateLimiter.Allow(email) {
		http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
		return
	}

	// Deliver to local recipients.
	for _, rcpt := range localRecipients {
		recipientID := h.resolveLocalUser(rcpt)
		if recipientID == "" {
			continue
		}

		recipientMeta := proto.Clone(meta).(*bongov1.MailMeta)
		if _, err := h.mailboxes.DeliverMessage(recipientID, "INBOX", recipientMeta, 0); err != nil {
			h.logger.Warn("web: failed to deliver local recipient copy", "recipient", rcpt, "user_id", recipientID, "guid", guid, "error", err)
			http.Error(w, "Failed to deliver local recipient copy", http.StatusInternalServerError)
			return
		}

		// Index for search. In rich mode index the derived plain text; in plain
		// mode index the submitted body directly — both are captured in searchBody.
		if h.search != nil {
			if err := h.search.IndexMail(recipientID, guid, subject, fromAddr, splitRecipients(to), searchBody, time.Now().Unix(), false, nil); err != nil {
				h.logger.Warn("web: failed to index recipient mail", "guid", guid, "error", err)
			}
		}
	}

	// Enqueue external recipients for outbound delivery. A missing queue store
	// on a node that accepted external recipients means outbound mail would be
	// silently dropped while the user is told it was sent — fail loudly instead.
	if len(externalRecipients) > 0 && h.queueStore == nil {
		h.logger.Error("web: cannot send to external recipients: outbound queue not configured", "user", userID, "guid", guid, "recipients", externalRecipients)
		http.Error(w, "Outbound delivery is not available on this node", http.StatusServiceUnavailable)
		return
	}
	if len(externalRecipients) > 0 && h.queueStore != nil {
		outMsg := &bongov1.OutboundMessage{
			Id:          guid,
			SenderId:    userID,
			SenderEmail: fromAddr, // envelope MAIL FROM / return-path follows the chosen From
			Recipients:  externalRecipients,
			BlobHash:    rawHash, // skeleton hash when split; the queue reconstructs before relaying
			Layout:      layout,
			Attachments: metaAttachments,
			MessageGuid: meta.MessageId,
		}
		if err := h.queueStore.Enqueue(outMsg); err != nil {
			h.logger.Warn("web: failed to enqueue outbound delivery", "user", userID, "guid", guid, "error", err)
			http.Error(w, "Failed to enqueue outbound delivery", http.StatusInternalServerError)
			return
		}
	}

	// Store sender copy only after recipient delivery work has succeeded.
	// Mark it Seen: a message you sent is not "unread" and must not inflate
	// unread counts.
	if _, err := h.mailboxes.DeliverMessage(userID, "Sent", meta, store.FlagSeen); err != nil {
		http.Error(w, "Failed to store message in Sent", http.StatusInternalServerError)
		return
	}

	// If reply, set Answered flag on original.
	if mode == "reply" && replyMailbox != "" && replyUIDStr != "" {
		replyUID, _ := strconv.ParseUint(replyUIDStr, 10, 32)
		if replyUID > 0 {
			if err := h.mailboxes.AddFlags(userID, replyMailbox, uint32(replyUID), store.FlagAnswered); err != nil {
				h.logger.Warn("web: failed to set answered flag", "mailbox", replyMailbox, "uid", replyUID, "user", userID, "error", err)
			}
		}
	}

	// Index sender's copy. Use searchBody (derived plain text in rich mode).
	if h.search != nil {
		if err := h.search.IndexMail(userID, guid, subject, fromAddr, splitRecipients(to), searchBody, time.Now().Unix(), false, nil); err != nil {
			h.logger.Warn("web: failed to index sender copy", "guid", guid, "error", err)
		}
	}

	h.logger.Info("web: message sent", "from", fromAddr, "to_count", len(allRecipients), "subject", subject, "local", len(localRecipients), "external", len(externalRecipients), "user", userID)

	// Build flash message based on delivery type.
	flash := "Message+sent"
	if len(localRecipients) > 0 && len(externalRecipients) > 0 {
		flash = "Message+sent+to+local+recipients%3B+external+delivery+queued"
	} else if len(externalRecipients) > 0 {
		flash = "Message+queued+for+delivery"
	}

	http.Redirect(w, r, "/mail/Sent?flash="+flash, http.StatusSeeOther)
}

// SaveDraft saves a message as a draft.
func (h *ComposeHandler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, composeMultipartMaxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- request body is capped by MaxBytesReader above
		_ = r.ParseForm()
	}

	to := r.FormValue("to")
	cc := r.FormValue("cc")
	subject := r.FormValue("subject")
	body := r.FormValue("body")
	bodyMode := r.FormValue("body_mode")

	// Process file attachments for draft. As with Send, payloads are assembled
	// into the message rather than stored as standalone decoded blobs; the split
	// (or whole-raw) store below handles persistence.
	var attachmentMeta []*bongov1.BlobRef
	var attachmentFiles []attachmentData
	if r.MultipartForm != nil && r.MultipartForm.File["attachments"] != nil {
		for _, fh := range r.MultipartForm.File["attachments"] {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				continue
			}
			contentType := fh.Header.Get("Content-Type")
			if contentType == "" || contentType == "application/octet-stream" {
				contentType = http.DetectContentType(data)
			}
			attachmentMeta = append(attachmentMeta, &bongov1.BlobRef{
				Size:        fh.Size,
				ContentType: contentType,
				Filename:    fh.Filename,
			})
			attachmentFiles = append(attachmentFiles, attachmentData{
				Filename:    fh.Filename,
				ContentType: contentType,
				Data:        data,
			})
		}
	}

	draftMessageID := fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), randomToken(), senderDomain(email))

	// body_mode='rich' stores the draft as multipart/alternative with the
	// sanitized HTML (no signature — that is appended only on send), so the
	// draft reopens in the rich editor rather than showing raw tags. Any other
	// value stores plain text.
	var rawMsg []byte
	if bodyMode == "rich" {
		built, _, buildErr := h.buildRichMessage(email, to, cc, subject, body, userID, draftMessageID, "", "", attachmentFiles, false)
		if buildErr != nil {
			msg := "Draft could not be built"
			switch {
			case errors.Is(buildErr, errInlineImageTooLarge):
				msg = "Inline image too large (max 1MB each)"
			case errors.Is(buildErr, errTotalInlineTooLarge):
				msg = "Total inline images too large (max 10MB)"
			}
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		rawMsg = built
	} else {
		rawMsg = buildRFC5322WithAttachments(email, to, cc, subject, body, draftMessageID, "", "", attachmentFiles)
	}

	rawHash := ""
	metaAttachments := attachmentMeta
	var layout []*bongov1.MessageSegment
	if split, serr := store.StoreSplit(h.blobs, rawMsg, h.dedupMinSize); split != nil {
		rawHash = split.SkeletonHash
		metaAttachments = split.Attachments
		layout = split.Layout
		h.logger.Info("compose: attachment dedup",
			"user", userID,
			"attachments", len(split.Attachments),
			"blobs_written", split.BlobsWritten,
			"blobs_deduped", split.BlobsDeduped,
			"bytes_saved", split.BytesSaved)
	} else {
		if serr != nil {
			h.logger.Warn("compose: draft split failed, storing whole raw", "user", userID, "error", serr)
		}
		wh, err := h.blobs.Put(rawMsg)
		if err != nil {
			http.Error(w, "Failed to store draft", http.StatusInternalServerError)
			return
		}
		rawHash = wh
	}

	guid := fmt.Sprintf("draft-%d-%s", time.Now().UnixNano(), rawHash[:8])

	meta := &bongov1.MailMeta{
		Guid:       guid,
		Subject:    subject,
		From:       email,
		To:         splitRecipients(to),
		Cc:         splitRecipients(cc),
		ReceivedAt: time.Now().Unix(),
		RawMessage: &bongov1.BlobRef{
			Hash: rawHash,
			Size: int64(len(rawMsg)),
		},
		Attachments: metaAttachments,
		Layout:      layout,
	}
	// A draft is authored by the user, not received mail: mark it Seen so it
	// does not count as unread.
	if _, err := h.mailboxes.DeliverMessage(userID, "Drafts", meta, store.FlagSeen); err != nil {
		http.Error(w, "Failed to store draft", http.StatusInternalServerError)
		return
	}

	h.logger.Info("web: draft saved", "user", userID, "subject", subject)

	http.Redirect(w, r, "/mail/Drafts?flash=Draft+saved", http.StatusSeeOther)
}

// EditDraft reopens a saved draft in the compose form. A rich draft
// (multipart/alternative with an HTML part) reopens in the rich editor seeded
// with its sanitized HTML; a plain draft reopens in the plain textarea.
// GET /compose/draft/{mailbox}/{uid}.
func (h *ComposeHandler) EditDraft(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	mailbox := decodeMailbox(chi.URLParam(r, "mailbox"))
	uid, _ := strconv.ParseUint(chi.URLParam(r, "uid"), 10, 32)

	msg, _ := h.mailboxes.GetMessage(userID, mailbox, uint32(uid))
	if msg == nil {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}
	meta := h.loadMailMeta(userID, msg.MailGuid)
	if meta == nil {
		http.Redirect(w, r, "/compose", http.StatusSeeOther)
		return
	}

	to := strings.Join(meta.To, ", ")
	cc := strings.Join(meta.Cc, ", ")
	subject := meta.Subject

	// Extract the body: prefer the HTML part (rich draft), else plain text.
	// htmlBody drives the rich editor; plainBody seeds the plain textarea.
	var htmlBody, plainBody string
	if meta.RawMessage != nil && h.blobs != nil {
		if raw, err := store.RawMessageBytes(h.blobs, meta); err == nil {
			if env, err := enmime.ReadEnvelope(bytes.NewReader(raw)); err == nil {
				if env.HTML != "" {
					htmlBody = sanitize.Compose(env.HTML)
				}
				plainBody = env.Text
			}
		}
	}

	fromOpts, fromSel := h.buildFromPickerOptions(userID, email)

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("draft", to, cc, subject, plainBody, mailbox, uint32(uid), fromOpts, fromSel, htmlBody)).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("draft", to, cc, subject, plainBody, mailbox, uint32(uid), mailboxInfos, nil, email, fromOpts, fromSel, htmlBody)).ServeHTTP(w, r)
}

// GALSearch returns GAL autocomplete results as JSON.
func (h *ComposeHandler) GALSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" || h.search == nil {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("[]")); err != nil {
			return
		}
		return
	}

	entries, err := h.search.SearchGAL(q, 10)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("[]")); err != nil {
			return
		}
		return
	}

	type galJSON struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Source      string `json:"source"`
	}

	// Merge personal contacts (first) with GAL entries.
	seen := make(map[string]bool)
	var result []galJSON

	userID, _ := middleware.UserFromContext(r.Context())
	if userID != "" && h.search != nil {
		contactResults, _ := h.search.SearchContacts(userID, q, 10)
		for _, c := range contactResults {
			lower := strings.ToLower(c.Email)
			if seen[lower] {
				continue
			}
			seen[lower] = true
			result = append(result, galJSON{Email: c.Email, DisplayName: c.DisplayName, Source: "contacts"})
		}
	}

	for _, e := range entries {
		lower := strings.ToLower(e.Email)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		result = append(result, galJSON{Email: e.Email, DisplayName: e.DisplayName, Source: "gal"})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		return
	}
}

// resolveLocalUser maps a bare email address to a local user ID using the shared
// recipient resolver, which checks the AddressStore Pebble key (source of truth,
// covering migrated primaries and aliases) then the legacy /system/alias key plus
// the users table (post-boot accounts/aliases). Returns "" when the address is
// not local.
//
// Compose and inbound delivery must share this resolver: they previously
// disagreed, with compose alias-aware but returning a username rather than a
// userID from its alias branch, and delivery.go users-table only and alias-blind.
func (h *ComposeHandler) resolveLocalUser(email string) string {
	if h.resolver == nil {
		return ""
	}
	userID, err := h.resolver.ResolveRecipient(email)
	if err != nil {
		return ""
	}
	return userID
}

func (h *ComposeHandler) loadMailboxInfos(userID string) []templates.MailboxInfo {
	return LoadMailboxInfos(h.mailboxes, userID, "")
}

func (h *ComposeHandler) loadMailMeta(userID, guid string) *bongov1.MailMeta {
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

func (h *ComposeHandler) loadQuotedBody(userID string, meta *bongov1.MailMeta) string {
	if meta.RawMessage == nil || meta.RawMessage.Hash == "" {
		return ""
	}

	rawMsg, err := store.RawMessageBytes(h.blobs, meta)
	if err != nil {
		return ""
	}
	env, err := enmime.ReadEnvelope(bytes.NewReader(rawMsg))
	if err != nil {
		return ""
	}

	body := env.Text
	if body == "" {
		body = env.HTML
	}

	// Add quote prefix.
	lines := strings.Split(body, "\n")
	var quoted strings.Builder
	fmt.Fprintf(&quoted, "\n\nOn %s, %s wrote:\n", time.Unix(meta.ReceivedAt, 0).Format("Jan 2, 2006"), meta.From)
	for _, line := range lines {
		quoted.WriteString("> " + line + "\n")
	}
	return quoted.String()
}

func (h *ComposeHandler) signatureBody(userID string) string {
	if h.prefs == nil {
		return ""
	}
	up := h.prefs.Get(userID)
	if up.Signature == "" {
		return ""
	}
	return "\n\n-- \n" + up.Signature
}

// signatureHTML returns the user's HTML signature wrapped in a <div>, or empty
// string when the user has no HTML signature. It is appended to the HTML part
// in rich-mode composition so that the signature appears exactly once — the
// composer does not pre-fill the editor with the signature in rich mode.
func (h *ComposeHandler) signatureHTML(userID string) string {
	if h.prefs == nil {
		return ""
	}
	up := h.prefs.Get(userID)
	if up.SignatureHtml == "" {
		return ""
	}
	return "<div>" + up.SignatureHtml + "</div>"
}

// buildRichMessage constructs a multipart/alternative (or multipart/related /
// multipart/mixed) RFC 5322 message from an HTML body submitted in rich mode.
// It returns the raw message bytes and the derived plain-text string (used for
// FTS indexing so the search index never stores raw HTML). The numbered steps in
// the body are order-sensitive; see the comment on each.
//
// includeSignature controls whether the user's HTML+plain signature is appended.
// Sends append it (so it appears once in outgoing mail); drafts do NOT, so a
// draft round-trips verbatim and re-saving never duplicates the signature.
func (h *ComposeHandler) buildRichMessage(
	from, to, cc, subject, rawHTML, userID, messageID, inReplyTo, references string,
	attachments []attachmentData, includeSignature bool,
) (rawMsg []byte, plainText string, err error) {
	// 1a. Fast pre-check: estimate inline-image sizes from base64 string lengths
	//     before running the expensive HTML sanitiser. This makes the rejection
	//     path DoS-safe — we avoid running bluemonday on clearly oversized input.
	//     The definitive check (step 4) still runs after extractDataURIImages
	//     to cover the appended signature HTML as well.
	if err := preCheckInlineImageSizes(rawHTML); err != nil {
		return nil, "", err
	}

	// 1b. Sanitize submitted HTML.
	sanitizedBody := sanitize.Compose(rawHTML)

	// 2. Append HTML signature before extracting inline images so that data:
	//    images embedded in the signature are also converted to cid: parts.
	//    Drafts skip this (includeSignature=false) so re-saving never duplicates it.
	combined := sanitizedBody
	if includeSignature {
		combined += h.signatureHTML(userID)
	}

	// 3. Extract inline images (data: → cid:).
	rewrittenHTML, inlineImgs := extractDataURIImages(combined)

	// 4. Definitive inline-image size check on decoded bytes. This is the actual
	//    DoS control — the client-side cap in richeditor.js is bypassable.
	//    Catches any images that arrived via the signature (not covered by step 1a).
	if err := checkInlineImageCaps(inlineImgs); err != nil {
		return nil, "", err
	}

	// 5. Derive plain text from the sanitized body (not the combined HTML — the
	//    signature HTML is handled separately as plain text below).
	plainText = plainTextFromHTML(sanitizedBody)
	if includeSignature && h.prefs != nil {
		up := h.prefs.Get(userID)
		if up.Signature != "" {
			plainText += "\n\n-- \n" + up.Signature
		}
	}

	// 6. Build the MIME message.
	rawMsg = buildMIME(mimeParts{
		from:        from,
		to:          to,
		cc:          cc,
		subject:     subject,
		plain:       plainText,
		html:        rewrittenHTML,
		messageID:   messageID,
		inReplyTo:   inReplyTo,
		references:  references,
		inline:      inlineImgs,
		attachments: attachments,
	})
	return rawMsg, plainText, nil
}

// attachmentData holds file data for building MIME messages.
type attachmentData struct {
	Filename    string
	ContentType string
	Data        []byte
}

// buildRFC5322WithAttachments is a thin wrapper around buildMIME that constructs
// a plain-text message, optionally with file attachments (no HTML, no inline
// images).
func buildRFC5322WithAttachments(from, to, cc, subject, body, messageID, inReplyTo, references string, attachments []attachmentData) []byte {
	return buildMIME(mimeParts{
		from:        from,
		to:          to,
		cc:          cc,
		subject:     subject,
		plain:       body,
		messageID:   messageID,
		inReplyTo:   inReplyTo,
		references:  references,
		attachments: attachments,
	})
}

// senderDomain returns the domain part of an email address, falling back to
// "localhost" when the address has no parseable domain.
func senderDomain(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 && i < len(email)-1 {
		return email[i+1:]
	}
	return "localhost"
}

// randomToken returns a short random hex string for Message-ID uniqueness.
func randomToken() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0"
	}
	return hex.EncodeToString(b[:])
}

// normalizeRecipient extracts the bare local@domain address from a recipient
// field that may include a display name or angle brackets (e.g.
// "Lance <a@b.com>" or "<a@b.com>"). It falls back to the trimmed input when
// the value cannot be parsed as an address.
func normalizeRecipient(rcpt string) string {
	rcpt = strings.TrimSpace(rcpt)
	if rcpt == "" {
		return ""
	}
	if addr, err := netmail.ParseAddress(rcpt); err == nil {
		return addr.Address
	}
	return rcpt
}

func splitRecipients(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	for _, r := range strings.Split(s, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			result = append(result, r)
		}
	}
	return result
}
