package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	netmail "net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/queue"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/sanitize"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const composeMultipartMaxBytes = 33 << 20

// composeUploadMaxMemory is the maxMemory for compose forms. It stays large
// rather than using uploadMaxMemory because net/http allows non-file values
// only maxMemory plus 10 MiB in total, and the rich-text body field carries
// inline images as data URIs well past that. The body cap above still bounds the
// request; anything of the 33 MiB beyond this figure spools to disk.
const composeUploadMaxMemory = 32 << 20

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
	// recipientRL is the per-user external-recipient budget shared with SMTP
	// submission, keyed by user ID. nil = unlimited.
	recipientRL   *mail.RateLimiter
	maxRecipients int // per-message cap on To+Cc; <= 0 means defaultMaxRecipients
	prefs         *store.PreferenceStore
	resolver      localRecipientResolver // unified address→userID resolver
	addresses     *store.AddressStore    // for send-as From picker and enforcement
	logger        *slog.Logger
	quota         QuotaChecker // nil = unlimited
	spool         localSpooler // inbound pipeline spool for local recipients

	// dedupMinSize is the attachment-dedup threshold. > 0 stores sent mail as a
	// skeleton + deduplicated attachment blobs; <= 0 keeps whole-raw.
	dedupMinSize int
	// sendLocks serialises concurrent submissions of one send ID within this
	// process (a double click).
	sendLocks keyedMutex
}

// localSpooler is the slice of the inbound pipeline spool compose needs:
// writing one QueueEnvelope for asynchronous local delivery.
// *pipeline.SpoolStore satisfies it.
type localSpooler interface {
	Enqueue(env *bongov1.QueueEnvelope) error
}

// SetSpool sets the inbound pipeline spool that local recipients are delivered
// through, the same path SMTP submission uses.
func (h *ComposeHandler) SetSpool(s localSpooler) { h.spool = s }

// SetQuotaChecker enables the domain storage-quota check on Send and SaveDraft.
func (h *ComposeHandler) SetQuotaChecker(qc QuotaChecker) { h.quota = qc }

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

// SetRecipientLimiter sets the shared per-user external-recipient budget.
func (h *ComposeHandler) SetRecipientLimiter(rl *mail.RateLimiter) { h.recipientRL = rl }

// RecipientLimiter returns the shared per-user external-recipient budget, or nil.
func (h *ComposeHandler) RecipientLimiter() *mail.RateLimiter { return h.recipientRL }

// SetMaxRecipients sets the per-message cap on To and Cc recipients.
func (h *ComposeHandler) SetMaxRecipients(n int) { h.maxRecipients = n }

// defaultMaxRecipients matches the SMTP max_recipients default.
const defaultMaxRecipients = 100

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
		if ok, err := store.CanSendAs(h.addresses, userID, primaryEmail, addr); err != nil || !ok {
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

// maxComposeToBytes caps the ?to= prefill (the RFC 5321 path limit).
const maxComposeToBytes = 320

// composeToPrefill returns raw when it is exactly one bare address, with no
// line breaks and within the length cap, and "" otherwise. The value is shown
// in an input and later reused as a recipient, so anything else is dropped.
func composeToPrefill(raw string) string {
	// The CR/LF and display-name checks are redundant with addr.Address != raw below; kept as defence in depth.
	if raw == "" || len(raw) > maxComposeToBytes || strings.ContainsAny(raw, "\r\n") {
		return ""
	}
	addr, err := netmail.ParseAddress(raw)
	if err != nil || addr.Name != "" || addr.Address != raw {
		return ""
	}
	return addr.Address
}

// NewMessage renders the compose form. A single valid ?to= address prefills
// the recipient (used by the Email button on a contact).
func (h *ComposeHandler) NewMessage(w http.ResponseWriter, r *http.Request) {
	to := composeToPrefill(r.URL.Query().Get("to"))
	userID, email := middleware.UserFromContext(r.Context())
	body := h.signatureBody(userID)
	fromOpts, fromSel := h.buildFromPickerOptions(userID, email)

	if r.Header.Get("HX-Request") == "true" {
		templ.Handler(templates.ComposePage("new", to, "", "", body, "", 0, fromOpts, fromSel, "")).ServeHTTP(w, r)
		return
	}

	mailboxInfos := h.loadMailboxInfos(userID)
	templ.Handler(templates.ComposePageFull("new", to, "", "", body, "", 0, mailboxInfos, nil, email, fromOpts, fromSel, "")).ServeHTTP(w, r)
}

// maxQuickReplyRunes caps the quick reply text carried by the quick reply form.
const maxQuickReplyRunes = 2000

// quickReplyBodyMaxBytes bounds the quick reply POST body: the capped text at
// four bytes a rune, plus the CSRF field, with headroom.
const quickReplyBodyMaxBytes = 64 << 10

// truncateRunes returns s cut to at most n runes without splitting a rune.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
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

	// Reply-all is used when the request asks for it (?all=1, the message
	// view's Reply all button) or when the default_reply preference says so.
	replyTo := meta.From
	var replyCc string
	replyAll := r.URL.Query().Get("all") == "1"
	if !replyAll && h.prefs != nil {
		up := h.prefs.Get(userID)
		replyAll = up.DefaultReply == bongov1.DefaultReplyMode_REPLY_MODE_ALL
	}
	if replyAll {
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

	// Text typed into the message view's quick reply card seeds the body. It
	// arrives in a POST body, never the URL, so it stays out of history and
	// Referer. It is plain text; the composer template escapes it.
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, quickReplyBodyMaxBytes)
	}
	if quick := strings.TrimSpace(r.PostFormValue("quick")); quick != "" {
		body = truncateRunes(quick, maxQuickReplyRunes) + "\n" + body
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

// composeText translates a message ID into the request's locale.
func composeText(r *http.Request, id string) string {
	return i18n.T(middleware.LocaleFromContext(r.Context()), id)
}

// composeError writes a translated plain-text error response.
func composeError(w http.ResponseWriter, r *http.Request, id string, code int) {
	http.Error(w, composeText(r, id), code)
}

// Send processes the compose form and delivers the message.
func (h *ComposeHandler) Send(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, composeMultipartMaxBytes)
	defer removeUploadTemp(r)
	if err := r.ParseMultipartForm(composeUploadMaxMemory); err != nil { // #nosec G120 -- request body is capped by MaxBytesReader above
		if err2 := r.ParseForm(); err2 != nil {
			composeError(w, r, "compose.errInvalidForm", http.StatusBadRequest)
			return
		}
	}

	// The send ID names this submission so a retry is idempotent. It is
	// required: a form without one comes from a tab rendered before this field
	// existed or from a non-browser client, and generating one here would give
	// that request no idempotency while looking as if it had it. Refused before
	// any write or rate-limit spend.
	sendID := r.FormValue("send_id")
	if !sendIDPattern.MatchString(sendID) {
		composeError(w, r, "compose.errFormOutdated", http.StatusBadRequest)
		return
	}

	to := r.FormValue("to")
	cc := r.FormValue("cc")
	subject := r.FormValue("subject")
	body := r.FormValue("body")
	mode := r.FormValue("mode")
	replyMailbox := r.FormValue("reply_mailbox")
	replyUIDStr := r.FormValue("reply_uid")

	if to == "" || subject == "" {
		composeError(w, r, "compose.errToSubjectRequired", http.StatusBadRequest)
		return
	}

	// Quota policy: a send is refused, never sent without its Sent copy. The
	// sender's own Sent copy must fit, and so must every local recipient's
	// INBOX copy (SMTP RCPT rejects an over-quota local recipient with a
	// permanent 552; the web form has no per-recipient partial accept, so the
	// whole send is refused and nothing is delivered). Both checks weigh the
	// message size (msgSize, below) against the domain headroom, and both run
	// once the message is built but before any blob, queue or mailbox write and
	// before the rate limiters spend budget. Each local recipient and the sender
	// are checked one at a time, so copies landing in one domain are not added
	// together. The usage figure is recomputed periodically, so this is best
	// effort.

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
			composeError(w, r, "compose.errSendAsUnavailable", http.StatusForbidden)
			return
		}
		allowed, err := store.CanSendAs(h.addresses, userID, email, chosenFrom)
		switch {
		case err != nil:
			composeError(w, r, "compose.errAddressLookup", http.StatusInternalServerError)
			return
		case !allowed:
			composeError(w, r, "compose.errAddressForbidden", http.StatusForbidden)
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
				composeError(w, r, "compose.errAttachmentTooLarge", http.StatusBadRequest)
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
	//
	// It is derived from the send ID so every attempt of one send, including a
	// retry after a partial failure, gives all recipients the same Message-ID.
	messageID := fmt.Sprintf("<%s@%s>", strings.ReplaceAll(derivedSendID(userID, sendID, "msgid").String(), "-", ""), senderDomain(fromAddr))

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
			id := "compose.errBuildMessage"
			switch {
			case errors.Is(buildErr, errInlineImageTooLarge):
				id = "compose.errInlineTooLarge"
			case errors.Is(buildErr, errTotalInlineTooLarge):
				id = "compose.errTotalInlineTooLarge"
			}
			composeError(w, r, id, http.StatusBadRequest)
			return
		}
	} else {
		rawMsg = buildRFC5322WithAttachments(fromAddr, to, cc, subject, body, messageID, inReplyTo, references, attachmentFiles)
		searchBody = body
	}

	msgSize := int64(len(rawMsg))

	// Split recipients into local and external before any user-visible writes
	// so pre-delivery rejection does not leave a partial Sent copy behind.
	allRecipients := append(splitRecipients(to), splitRecipients(cc)...)
	var localRecipients, externalRecipients []string
	for _, rcpt := range allRecipients {
		// Reduce to the bare local@domain address: the To/Cc fields may carry a
		// display name or angle brackets ("Alice <a@b.com>"), which must not
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

	maxRcpts := h.maxRecipients
	if maxRcpts <= 0 {
		maxRcpts = defaultMaxRecipients
	}
	if len(localRecipients)+len(externalRecipients) > maxRcpts {
		composeError(w, r, "compose.errTooManyRecipients", http.StatusBadRequest)
		return
	}

	// Serialise submissions of this send ID in this process, then read which
	// steps are already recorded. A record made for different content (the
	// browser restored an old form, the user edited it and sent again) is
	// refused before anything is written: silently replaying the old send
	// would tell the user their edited message went out.
	fp := sendFingerprint([]string{
		fromAddr, to, cc, subject, body, r.FormValue("body_mode"),
		mode, replyMailbox, replyUIDStr,
	}, attachmentFiles)
	unlockSend := h.sendLocks.lock(userID + "/" + sendID)
	defer unlockSend()
	lg := &sendLog{h: h, userID: userID, sendID: sendID, fingerprint: fp}
	stepDone := make(map[string]bool)
	steps := []string{"sent"}
	if len(externalRecipients) > 0 {
		steps = append(steps, "external")
	}
	for _, rcpt := range localRecipients {
		steps = append(steps, store.SendLogLocalStep(rcpt))
	}
	// Any record of this send ID made for different content conflicts, even
	// for a step this submission does not have (all local recipients changed,
	// say), because the derived Message-ID is shared by every step.
	if err := lg.checkAll(); err != nil {
		if errors.Is(err, errSendConflict) {
			composeError(w, r, "compose.errSendConflict", http.StatusConflict)
			return
		}
		h.logger.Warn("web: reading send log failed", "user", userID, "error", err)
		composeError(w, r, "compose.errSendState", http.StatusInternalServerError)
		return
	}
	for _, step := range steps {
		done, err := lg.done(step)
		if err != nil {
			h.logger.Warn("web: reading send log failed", "user", userID, "error", err)
			composeError(w, r, "compose.errSendState", http.StatusInternalServerError)
			return
		}
		stepDone[step] = done
	}
	pendingLocal := false
	for _, rcpt := range localRecipients {
		if !stepDone[store.SendLogLocalStep(rcpt)] {
			pendingLocal = true
		}
	}

	// The size the usage recompute counts for a stored message
	// (MailMeta.RawMessage.Size), computed once. Every copy of this send, the
	// sender's and each local recipient's, is the same size.
	// Steps already recorded done are not re-checked: their copies now count
	// against quota, so a replay of a delivered send would be refused for a
	// message that went out.
	if !stepDone["sent"] && quotaExceeded(h.quota, h.logger, userID, msgSize) {
		refuseQuota(w, r)
		return
	}
	for _, rcpt := range localRecipients {
		if stepDone[store.SendLogLocalStep(rcpt)] {
			continue
		}
		if rid := h.resolveLocalUser(rcpt); rid != "" && quotaExceeded(h.quota, h.logger, rid, msgSize) {
			refuseQuotaText(w, r, quotaRecipientText(r, rcpt))
			return
		}
	}
	// Refuse before anything is stored or the rate limiters spend budget.
	if len(localRecipients) > 0 && h.spool == nil {
		h.logger.Error("web: cannot send to local recipients: inbound spool not configured", "user", userID)
		composeError(w, r, "compose.errLocalUnavailable", http.StatusServiceUnavailable)
		return
	}

	// Store the message: a skeleton + deduplicated attachment blobs when it
	// carries a large attachment, else the whole raw. StoreSplit self-checks
	// byte-exact reconstruction; on any split error we fall back to whole-raw so
	// a message is never stored in a form that cannot be reproduced.
	//
	// Blobs are content-addressed and the raw message carries a per-attempt
	// Date, so a retry may store a second blob. The first is reclaimed by the
	// blob GC when nothing references it; compose never deletes blobs.
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
			composeError(w, r, "compose.errStoreMessage", http.StatusInternalServerError)
			return
		}
		rawHash = wh
	}

	guid := derivedSendID(userID, sendID, "sent").String()

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
			Size: msgSize,
		},
		Attachments: metaAttachments,
		Layout:      layout,
		InReplyTo:   inReplyTo,
		References:  references,
	}

	// Check rate limits for external recipients before persisting sender-visible
	// state. The recipient budget is checked last so a message refused by the
	// message limiter spends nothing. A message refused by the recipient budget
	// has already used one message-limiter unit; that only makes the limits
	// slightly stricter. A refusal here leaves an unreferenced blob for the GC.
	// A retry whose external step is already recorded spends nothing: that
	// budget was paid by the attempt that did the work.
	if len(externalRecipients) > 0 && !stepDone["external"] {
		if h.rateLimiter != nil && !h.rateLimiter.Allow(email) {
			composeError(w, r, "compose.errRateLimit", http.StatusTooManyRequests)
			return
		}
		if h.recipientRL != nil && !h.recipientRL.AllowN(userID, len(externalRecipients)) {
			composeError(w, r, "compose.errRateLimit", http.StatusTooManyRequests)
			return
		}
	}

	// Hand local recipients to the inbound pipeline, the same path SMTP
	// submission uses, so each recipient's rules and forwarding run. The
	// envelope is Trusted with AuthUserId set to the author: compose is the
	// second source of Trusted besides smtp.Session.inboundMeta, and AuthUserId
	// comes only from the authenticated session, never from form input.
	//
	// Delivery is asynchronous: "sent" means accepted. Quota was checked above
	// with the size the recompute counts (msgSize) for the sender and each
	// local recipient, and the pipeline does not check it again, so it is
	// checked exactly once. The pipeline stores its stamped copy (msgSize plus
	// trace headers), the same small gap SMTP has with its declared SIZE.
	//
	// One envelope per recipient: the rules stage takes one TargetMailbox and
	// one delete verdict for a whole envelope, so a shared envelope would apply
	// one recipient's filing or delete rule to every other recipient.
	//
	// Partial failure. The spool writes, the external enqueue and the Sent copy
	// are separate writes and not atomic. Each is followed by a send log marker
	// (store.SendLogKey) and skipped on a retry of the same send ID, and every
	// object ID is derived from the send ID, so a retry after a 500 completes
	// the missing parts instead of repeating the finished ones. The guarantee
	// is at-least-once, not exactly-once: a crash between an object write and
	// its marker repeats that one write. The Sent copy and the external queue
	// entry close that gap by also checking for the object itself; a local
	// envelope cannot, because the spool is node-local and the envelope is
	// consumed within moments. The per-send lock above covers concurrent
	// submits on this node only; two nodes handling the same send ID at once
	// can both pass the checks.
	if pendingLocal {
		// The envelope must name the whole RFC 5322 message: the pipeline
		// reads BlobHash as the full message, but rawHash is a skeleton when
		// the Sent copy was split.
		envHash := rawHash
		if len(layout) > 0 {
			wh, err := h.blobs.Put(rawMsg)
			if err != nil {
				composeError(w, r, "compose.errStoreMessage", http.StatusInternalServerError)
				return
			}
			envHash = wh
		}
		for _, rcpt := range localRecipients {
			step := store.SendLogLocalStep(rcpt)
			if stepDone[step] {
				continue
			}
			env := &bongov1.QueueEnvelope{
				Id:         derivedSendID(userID, sendID, "local:"+strings.ToLower(rcpt)).String(),
				Sender:     fromAddr, // already checked against send-as
				Recipients: []string{rcpt},
				ReceivedAt: time.Now().Unix(),
				BlobHash:   envHash,
				Trusted:    true,
				AuthUserId: userID,
			}
			if err := h.spool.Enqueue(env); err != nil {
				h.logger.Warn("web: failed to spool local recipient copy", "recipient", rcpt, "guid", guid, "error", err)
				composeError(w, r, "compose.errLocalCopy", http.StatusInternalServerError)
				return
			}
			if err := lg.mark(step); err != nil {
				h.logger.Warn("web: failed to record local delivery", "recipient", rcpt, "guid", guid, "error", err)
				composeError(w, r, "compose.errLocalCopy", http.StatusInternalServerError)
				return
			}
			stepDone[step] = true
		}
	}

	// Enqueue external recipients for outbound delivery. A missing queue store
	// on a node that accepted external recipients means outbound mail would be
	// silently dropped while the user is told it was sent — fail loudly instead.
	if len(externalRecipients) > 0 && h.queueStore == nil {
		h.logger.Error("web: cannot send to external recipients: outbound queue not configured", "user", userID, "guid", guid, "recipients", externalRecipients)
		composeError(w, r, "compose.errOutboundUnavailable", http.StatusServiceUnavailable)
		return
	}
	if len(externalRecipients) > 0 && !stepDone["external"] {
		outID := derivedSendID(userID, sendID, "external").String()
		// The entry may already be queued by an attempt that died before
		// recording the step.
		if _, err := h.queueStore.Get(outID); err != nil {
			outMsg := &bongov1.OutboundMessage{
				Id:          outID,
				SenderId:    userID,
				SenderEmail: fromAddr, // envelope MAIL FROM / return-path follows the chosen From
				Recipients:  externalRecipients,
				BlobHash:    rawHash, // skeleton hash when split; the queue reconstructs before relaying
				Layout:      layout,
				Attachments: metaAttachments,
				MessageGuid: meta.MessageId,
				Origin:      bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL,
			}
			if err := h.queueStore.Enqueue(outMsg); err != nil {
				h.logger.Warn("web: failed to enqueue outbound delivery", "user", userID, "guid", guid, "error", err)
				composeError(w, r, "compose.errEnqueue", http.StatusInternalServerError)
				return
			}
		}
		if err := lg.mark("external"); err != nil {
			h.logger.Warn("web: failed to record outbound enqueue", "user", userID, "guid", guid, "error", err)
			composeError(w, r, "compose.errEnqueue", http.StatusInternalServerError)
			return
		}
	}

	// Store sender copy only after recipient delivery work has succeeded.
	// Mark it Seen: a message you sent is not "unread" and must not inflate
	// unread counts. A retry skips it when the step is recorded or the message
	// already exists under its derived GUID, and then also skips the search
	// indexing and Answered flag below, which the finished attempt did.
	sentSkipped := stepDone["sent"]
	if !sentSkipped {
		if _, err := h.pebble.Get([]byte("/user/" + userID + "/mail/" + guid)); err == nil {
			sentSkipped = true
		}
	}
	if !sentSkipped {
		if _, err := h.mailboxes.DeliverMessage(userID, "Sent", meta, store.FlagSeen); err != nil {
			composeError(w, r, "compose.errStoreSent", http.StatusInternalServerError)
			return
		}
	}
	if !stepDone["sent"] {
		if err := lg.mark("sent"); err != nil {
			h.logger.Warn("web: failed to record Sent copy", "user", userID, "guid", guid, "error", err)
			composeError(w, r, "compose.errStoreSent", http.StatusInternalServerError)
			return
		}
	}

	if !sentSkipped {
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
	}

	h.logger.Info("web: message sent", "from", fromAddr, "to_count", len(allRecipients), "local", len(localRecipients), "external", len(externalRecipients), "user", userID)

	// Build flash message based on delivery type.
	flashID := "compose.flashSent"
	if len(localRecipients) > 0 && len(externalRecipients) > 0 {
		flashID = "compose.flashSentLocalQueued"
	} else if len(externalRecipients) > 0 {
		flashID = "compose.flashQueued"
	}

	http.Redirect(w, r, "/mail/Sent?flash="+url.QueryEscape(composeText(r, flashID)), http.StatusSeeOther)
}

// SaveDraft saves a message as a draft.
func (h *ComposeHandler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, composeMultipartMaxBytes)
	defer removeUploadTemp(r)
	if err := r.ParseMultipartForm(composeUploadMaxMemory); err != nil { // #nosec G120 -- request body is capped by MaxBytesReader above
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
			id := "compose.errBuildDraft"
			switch {
			case errors.Is(buildErr, errInlineImageTooLarge):
				id = "compose.errInlineTooLarge"
			case errors.Is(buildErr, errTotalInlineTooLarge):
				id = "compose.errTotalInlineTooLarge"
			}
			composeError(w, r, id, http.StatusBadRequest)
			return
		}
		rawMsg = built
	} else {
		rawMsg = buildRFC5322WithAttachments(email, to, cc, subject, body, draftMessageID, "", "", attachmentFiles)
	}

	// A draft is always a new message, so it is a create: refuse it if it does
	// not fit, before any blob is written. The size is what the usage recompute
	// counts (MailMeta.RawMessage.Size); best effort, see Send.
	msgSize := int64(len(rawMsg))
	if quotaExceeded(h.quota, h.logger, userID, msgSize) {
		refuseQuota(w, r)
		return
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
			composeError(w, r, "compose.errStoreDraft", http.StatusInternalServerError)
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
			Size: msgSize,
		},
		Attachments: metaAttachments,
		Layout:      layout,
	}
	// A draft is authored by the user, not received mail: mark it Seen so it
	// does not count as unread.
	if _, err := h.mailboxes.DeliverMessage(userID, "Drafts", meta, store.FlagSeen); err != nil {
		composeError(w, r, "compose.errStoreDraft", http.StatusInternalServerError)
		return
	}

	h.logger.Info("web: draft saved", "user", userID)

	http.Redirect(w, r, "/mail/Drafts?flash="+url.QueryEscape(composeText(r, "compose.flashDraftSaved")), http.StatusSeeOther)
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
			if env, _, err := mimesafe.ReadEnvelope(raw); err == nil {
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
	// The GAL is a server-wide index. Scoping it to the caller's own address
	// domain is what stops one tenant enumerating another's users through
	// compose autocomplete.
	_, callerEmail := middleware.UserFromContext(r.Context())
	if q == "" || h.search == nil {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("[]")); err != nil {
			return
		}
		return
	}

	entries, err := h.search.SearchGAL(q, 10, callerEmail)
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
	env, _, err := mimesafe.ReadEnvelope(rawMsg)
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
// "Alice <a@b.com>" or "<a@b.com>"). It falls back to the trimmed input when
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

// splitRecipients splits a To/Cc field into one entry per recipient. A comma
// inside a quoted display name ("Doe, Jane" <j@x>) does not separate
// recipients. Lists that parse as RFC 5322 and contain no such comma are
// returned exactly as the plain comma split would return them, so existing
// input is unchanged; a list that does not parse falls back to the comma split.
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
	parsed, err := netmail.ParseAddressList(s)
	if err != nil || len(parsed) == len(result) {
		return result
	}
	quoted := make([]string, 0, len(parsed))
	for _, a := range parsed {
		if a.Name == "" {
			quoted = append(quoted, a.Address)
		} else {
			quoted = append(quoted, a.String())
		}
	}
	return quoted
}
