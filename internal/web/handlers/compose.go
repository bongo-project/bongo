package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

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
	logger      *slog.Logger
}

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

// NewMessage renders the compose form.
func (h *ComposeHandler) NewMessage(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	body := h.signatureBody(userID)

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("new", "", "", "", body, "", 0)).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("new", "", "", "", body, "", 0, mailboxInfos, nil, email)).ServeHTTP(w, r)
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

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("reply", replyTo, replyCc, subject, body, mailbox, uint32(uid))).ServeHTTP(w, r)
		return
	}

	_, replyEmail := middleware.UserFromContext(r.Context())
	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("reply", replyTo, replyCc, subject, body, mailbox, uint32(uid), mailboxInfos, nil, replyEmail)).ServeHTTP(w, r)
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

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("forward", "", "", subject, body, mailbox, uint32(uid))).ServeHTTP(w, r)
		return
	}

	_, fwdEmail := middleware.UserFromContext(r.Context())
	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("forward", "", "", subject, body, mailbox, uint32(uid), mailboxInfos, nil, fwdEmail)).ServeHTTP(w, r)
}

// Send processes the compose form and delivers the message.
func (h *ComposeHandler) Send(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	if err := r.ParseMultipartForm(32 << 20); err != nil {
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

	// Process file attachments.
	var attachments []*bongov1.BlobRef
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
			f.Close()
			if err != nil {
				continue
			}
			contentType := fh.Header.Get("Content-Type")
			if contentType == "" || contentType == "application/octet-stream" {
				contentType = http.DetectContentType(data)
			}
			hash, err := h.blobs.Put(data)
			if err != nil {
				continue
			}
			attachments = append(attachments, &bongov1.BlobRef{
				Hash:        hash,
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

	// Build RFC 5322 message.
	rawMsg := buildRFC5322WithAttachments(email, to, cc, subject, body, inReplyTo, references, attachmentFiles)

	// Store blob.
	hash, err := h.blobs.Put(rawMsg)
	if err != nil {
		http.Error(w, "Failed to store message", http.StatusInternalServerError)
		return
	}

	guid := fmt.Sprintf("%d-%s", time.Now().UnixNano(), hash[:8])

	// Create MailMeta.
	meta := &bongov1.MailMeta{
		Guid:       guid,
		MessageId:  fmt.Sprintf("<%s@bongo>", guid),
		Subject:    subject,
		From:       email,
		To:         splitRecipients(to),
		Cc:         splitRecipients(cc),
		ReceivedAt: time.Now().Unix(),
		RawMessage: &bongov1.BlobRef{
			Hash: hash,
			Size: int64(len(rawMsg)),
		},
		Attachments: attachments,
		InReplyTo:   inReplyTo,
		References:  references,
	}

	// Split recipients into local and external before any user-visible writes
	// so pre-delivery rejection does not leave a partial Sent copy behind.
	allRecipients := append(splitRecipients(to), splitRecipients(cc)...)
	var localRecipients, externalRecipients []string
	for _, rcpt := range allRecipients {
		rcpt = strings.TrimSpace(rcpt)
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

		// Index for search.
		if h.search != nil {
			h.search.IndexMail(recipientID, guid, subject, email, splitRecipients(to), body, time.Now().Unix(), false, nil)
		}
	}

	// Enqueue external recipients for outbound delivery.
	if len(externalRecipients) > 0 && h.queueStore != nil {
		outMsg := &bongov1.OutboundMessage{
			Id:          guid,
			SenderId:    userID,
			SenderEmail: email,
			Recipients:  externalRecipients,
			BlobHash:    hash,
			MessageGuid: meta.MessageId,
		}
		if err := h.queueStore.Enqueue(outMsg); err != nil {
			h.logger.Warn("web: failed to enqueue outbound delivery", "user", userID, "guid", guid, "error", err)
			http.Error(w, "Failed to enqueue outbound delivery", http.StatusInternalServerError)
			return
		}
	}

	// Store sender copy only after recipient delivery work has succeeded.
	if _, err := h.mailboxes.DeliverMessage(userID, "Sent", meta, 0); err != nil {
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

	// Index sender's copy.
	if h.search != nil {
		h.search.IndexMail(userID, guid, subject, email, splitRecipients(to), body, time.Now().Unix(), false, nil)
	}

	h.logger.Info("web: message sent", "from", email, "to_count", len(allRecipients), "subject", subject, "local", len(localRecipients), "external", len(externalRecipients), "user", userID)

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

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		_ = r.ParseForm()
	}

	to := r.FormValue("to")
	subject := r.FormValue("subject")
	body := r.FormValue("body")

	// Process file attachments for draft.
	var attachments []*bongov1.BlobRef
	var attachmentFiles []attachmentData
	if r.MultipartForm != nil && r.MultipartForm.File["attachments"] != nil {
		for _, fh := range r.MultipartForm.File["attachments"] {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				continue
			}
			contentType := fh.Header.Get("Content-Type")
			if contentType == "" || contentType == "application/octet-stream" {
				contentType = http.DetectContentType(data)
			}
			hash, err := h.blobs.Put(data)
			if err != nil {
				continue
			}
			attachments = append(attachments, &bongov1.BlobRef{
				Hash:        hash,
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

	rawMsg := buildRFC5322WithAttachments(email, to, "", subject, body, "", "", attachmentFiles)

	hash, err := h.blobs.Put(rawMsg)
	if err != nil {
		http.Error(w, "Failed to store draft", http.StatusInternalServerError)
		return
	}

	guid := fmt.Sprintf("draft-%d-%s", time.Now().UnixNano(), hash[:8])

	meta := &bongov1.MailMeta{
		Guid:       guid,
		Subject:    subject,
		From:       email,
		To:         splitRecipients(to),
		ReceivedAt: time.Now().Unix(),
		RawMessage: &bongov1.BlobRef{
			Hash: hash,
			Size: int64(len(rawMsg)),
		},
		Attachments: attachments,
	}
	if _, err := h.mailboxes.DeliverMessage(userID, "Drafts", meta, 0); err != nil {
		http.Error(w, "Failed to store draft", http.StatusInternalServerError)
		return
	}

	h.logger.Info("web: draft saved", "user", userID, "subject", subject)

	http.Redirect(w, r, "/mail/Drafts?flash=Draft+saved", http.StatusSeeOther)
}

// GALSearch returns GAL autocomplete results as JSON.
func (h *ComposeHandler) GALSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" || h.search == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	entries, err := h.search.SearchGAL(q, 10)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
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
	json.NewEncoder(w).Encode(result)
}

func (h *ComposeHandler) resolveLocalUser(email string) string {
	// Look up by email in system metadata.
	key := fmt.Sprintf("/system/alias/%s", strings.ToLower(email))
	data, err := h.pebble.Get([]byte(key))
	if err == nil && len(data) > 0 {
		return string(data)
	}

	// Try looking up in user profiles.
	// Scan all user profiles for matching email.
	iter, err := h.pebble.PrefixScan([]byte("/user/"))
	if err != nil {
		return ""
	}
	defer iter.Close()

	for iter.Next() {
		keyStr := string(iter.Key())
		if !strings.HasSuffix(keyStr, "/profile") {
			continue
		}
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var profile bongov1.UserProfile
		if err := proto.Unmarshal(val, &profile); err != nil {
			continue
		}
		if strings.EqualFold(profile.Email, email) {
			// Extract userID from key: /user/{userID}/profile
			parts := strings.Split(keyStr, "/")
			if len(parts) >= 3 {
				return parts[2]
			}
		}
	}
	return ""
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

	rc, err := h.blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		return ""
	}
	defer rc.Close()

	rawMsg, _ := io.ReadAll(rc)
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
	quoted.WriteString(fmt.Sprintf("\n\nOn %s, %s wrote:\n", time.Unix(meta.ReceivedAt, 0).Format("Jan 2, 2006"), meta.From))
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

// attachmentData holds file data for building MIME messages.
type attachmentData struct {
	Filename    string
	ContentType string
	Data        []byte
}

func buildRFC5322WithAttachments(from, to, cc, subject, body, inReplyTo, references string, attachments []attachmentData) []byte {
	// Sanitize user-controlled values to prevent header-injection attacks.
	from = mail.SanitizeHeaderValue(from)
	to = mail.SanitizeHeaderValue(to)
	cc = mail.SanitizeHeaderValue(cc)
	subject = mail.SanitizeHeaderValue(subject)

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if cc != "" {
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buf.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	buf.WriteString(fmt.Sprintf("Message-ID: <%d@bongo>\r\n", time.Now().UnixNano()))
	if inReplyTo != "" {
		buf.WriteString(fmt.Sprintf("In-Reply-To: %s\r\n", inReplyTo))
	}
	if references != "" {
		buf.WriteString(fmt.Sprintf("References: %s\r\n", references))
	}
	buf.WriteString("MIME-Version: 1.0\r\n")

	if len(attachments) == 0 {
		buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		buf.WriteString("\r\n")
		buf.WriteString(body)
		return buf.Bytes()
	}

	// Build multipart/mixed message.
	var randBytes [16]byte
	_, _ = rand.Read(randBytes[:])
	boundary := "bongo-" + hex.EncodeToString(randBytes[:])
	buf.WriteString(fmt.Sprintf("Content-Type: multipart/mixed; boundary=\"%s\"\r\n", boundary))
	buf.WriteString("\r\n")

	// Text body part.
	buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(body)
	buf.WriteString("\r\n")

	// Attachment parts.
	for _, att := range attachments {
		safeName := mail.SanitizeHeaderFilename(att.Filename)
		buf.WriteString(fmt.Sprintf("--%s\r\n", boundary))
		buf.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", att.ContentType, safeName))
		buf.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n", safeName))
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString("\r\n")

		encoded := base64.StdEncoding.EncodeToString(att.Data)
		// Wrap at 76 characters per RFC 2045.
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			buf.WriteString(encoded[i:end])
			buf.WriteString("\r\n")
		}
	}

	buf.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
	return buf.Bytes()
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
