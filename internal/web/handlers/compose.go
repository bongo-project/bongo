package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// ComposeHandler handles email composition.
type ComposeHandler struct {
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	mailboxes *store.MailboxStore
	search    *store.SearchManager
	applier   store.RaftApplier
}

// NewComposeHandler creates a ComposeHandler.
func NewComposeHandler(pebble *store.PebbleStore, blobs *store.BlobStore, mailboxes *store.MailboxStore, search *store.SearchManager, applier store.RaftApplier) *ComposeHandler {
	return &ComposeHandler{
		pebble:    pebble,
		blobs:     blobs,
		mailboxes: mailboxes,
		search:    search,
		applier:   applier,
	}
}

// NewMessage renders the compose form.
func (h *ComposeHandler) NewMessage(w http.ResponseWriter, r *http.Request) {
	templ.Handler(templates.ComposePage("new", "", "", "", "", "", 0)).ServeHTTP(w, r)
}

// Reply renders the reply form with pre-filled fields.
func (h *ComposeHandler) Reply(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := chi.URLParam(r, "mailbox")
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

	body := h.loadQuotedBody(userID, meta)

	templ.Handler(templates.ComposePage("reply", meta.From, "", subject, body, mailbox, uint32(uid))).ServeHTTP(w, r)
}

// Forward renders the forward form.
func (h *ComposeHandler) Forward(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	mailbox := chi.URLParam(r, "mailbox")
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

	body := h.loadQuotedBody(userID, meta)

	templ.Handler(templates.ComposePage("forward", "", "", subject, body, mailbox, uint32(uid))).ServeHTTP(w, r)
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

	// Build RFC 5322 message.
	rawMsg := buildRFC5322(email, to, cc, subject, body)

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
		ReceivedAt: time.Now().Unix(),
		RawMessage: &bongov1.BlobRef{
			Hash: hash,
			Size: int64(len(rawMsg)),
		},
	}

	metaBytes, _ := proto.Marshal(meta)

	// Store sender's MailMeta.
	mailMetaCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
		Value: metaBytes,
	}
	if err := h.applier.ApplyCommand(mailMetaCmd, 5*time.Second); err != nil {
		http.Error(w, "Failed to store metadata", http.StatusInternalServerError)
		return
	}

	// Copy to Sent folder.
	h.deliverToMailbox(userID, "Sent", guid, meta)

	// Deliver to local recipients.
	allRecipients := append(splitRecipients(to), splitRecipients(cc)...)
	for _, rcpt := range allRecipients {
		rcpt = strings.TrimSpace(rcpt)
		if rcpt == "" {
			continue
		}
		recipientID := h.resolveLocalUser(rcpt)
		if recipientID == "" {
			continue // skip non-local recipients
		}

		// Store MailMeta for recipient.
		recipientMetaCmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", recipientID, guid)),
			Value: metaBytes,
		}
		_ = h.applier.ApplyCommand(recipientMetaCmd, 5*time.Second)

		// Deliver to INBOX.
		h.deliverToMailbox(recipientID, "INBOX", guid, meta)

		// Index for search.
		if h.search != nil {
			h.search.IndexMail(recipientID, guid, subject, email, splitRecipients(to), body, time.Now().Unix(), false)
		}
	}

	// If reply, set Answered flag on original.
	if mode == "reply" && replyMailbox != "" && replyUIDStr != "" {
		replyUID, _ := strconv.ParseUint(replyUIDStr, 10, 32)
		if replyUID > 0 {
			_ = h.mailboxes.AddFlags(userID, replyMailbox, uint32(replyUID), store.FlagAnswered)
		}
	}

	// Index sender's copy.
	if h.search != nil {
		h.search.IndexMail(userID, guid, subject, email, splitRecipients(to), body, time.Now().Unix(), false)
	}

	http.Redirect(w, r, "/mail/Sent?flash=Message+sent", http.StatusSeeOther)
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

	rawMsg := buildRFC5322(email, to, "", subject, body)

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
	}
	metaBytes, _ := proto.Marshal(meta)

	mailMetaCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, guid)),
		Value: metaBytes,
	}
	_ = h.applier.ApplyCommand(mailMetaCmd, 5*time.Second)

	h.deliverToMailbox(userID, "Drafts", guid, meta)

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
	}
	result := make([]galJSON, 0, len(entries))
	for _, e := range entries {
		result = append(result, galJSON{Email: e.Email, DisplayName: e.DisplayName})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *ComposeHandler) deliverToMailbox(userID, mailbox, guid string, meta *bongov1.MailMeta) {
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

func buildRFC5322(from, to, cc, subject, body string) []byte {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("From: %s\r\n", from))
	buf.WriteString(fmt.Sprintf("To: %s\r\n", to))
	if cc != "" {
		buf.WriteString(fmt.Sprintf("Cc: %s\r\n", cc))
	}
	buf.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	buf.WriteString(fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123Z)))
	buf.WriteString(fmt.Sprintf("Message-ID: <%d@bongo>\r\n", time.Now().UnixNano()))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(body)
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
