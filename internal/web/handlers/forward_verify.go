package handlers

import (
	"bytes"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/queue"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// maxPendingForwardTargets caps the unverified targets one user may hold.
// Each new external address writes a persistent record, so without a cap a user
// could grow the keyspace without bound even while the mail limiter refuses.
const maxPendingForwardTargets = 10

var (
	// ErrForwardVerifyRateLimited means the per-user verification-mail limit was
	// reached. The target is recorded as pending; no mail was sent.
	ErrForwardVerifyRateLimited = errors.New("too many verification mails requested")

	// ErrForwardVerifyUnavailable means this node cannot send mail, so no
	// challenge was issued. The target is recorded as pending.
	ErrForwardVerifyUnavailable = errors.New("outbound delivery not configured on this node")

	// ErrForwardTooManyPending means the user already holds the maximum number
	// of unverified targets; no record was created.
	ErrForwardTooManyPending = errors.New("too many pending forwarding targets")

	// ErrForwardInvalidAddress means the target is not a plain email address.
	ErrForwardInvalidAddress = errors.New("invalid forwarding address")
)

// ForwardVerifier runs the code-based verification of external forwarding
// targets. It reuses the send-as challenge primitives (code generation,
// hashing, expiry, attempt limit, AddressVerification) but stores targets and
// challenges in ForwardTargetStore, so confirming a target never grants
// send-as rights or claims the address globally.
//
// Hosted targets need no verification. Verification mail is rate limited per
// user, and at most one live challenge is created per target by Ensure.
type ForwardVerifier struct {
	targets *store.ForwardTargetStore
	blobs   *store.BlobStore
	queue   *queue.QueueStore
	hosted  func(address string) bool
	limiter interface{ Allow(key string) bool }
	logger  *slog.Logger
}

// NewForwardVerifier creates a ForwardVerifier. hosted reports whether an
// address is delivered locally (such targets are exempt); limiter may be nil.
func NewForwardVerifier(targets *store.ForwardTargetStore, hosted func(string) bool, logger *slog.Logger) *ForwardVerifier {
	return &ForwardVerifier{targets: targets, hosted: hosted, logger: logger}
}

// SetMailer injects what is needed to send the challenge mail.
func (v *ForwardVerifier) SetMailer(blobs *store.BlobStore, qs *queue.QueueStore) {
	v.blobs = blobs
	v.queue = qs
}

// SetLimiter sets the per-user verification-mail limiter.
func (v *ForwardVerifier) SetLimiter(l interface{ Allow(key string) bool }) { v.limiter = l }

// parseForwardTarget normalises and validates a forwarding address.
func parseForwardTarget(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return "", false
	}
	a, err := mail.ParseAddress(raw)
	if err != nil || a.Address != raw || a.Name != "" {
		return "", false
	}
	return strings.ToLower(a.Address), true
}

// Ensure makes sure a target record exists for userID and, for a new or
// expired pending target, starts the challenge. A target that is already
// verified, grandfathered or hosted is left alone. Hard failures are returned
// as errors; ErrForwardVerifyRateLimited and ErrForwardVerifyUnavailable mean
// the target is recorded as pending but no code was sent.
func (v *ForwardVerifier) Ensure(userID, fromEmail, target string) error {
	addr, ok := parseForwardTarget(target)
	if !ok {
		return ErrForwardInvalidAddress
	}
	if v.hosted != nil && v.hosted(addr) {
		return nil
	}
	rec, exists, err := v.targets.Get(userID, addr)
	if err != nil {
		return fmt.Errorf("forward verify: %w", err)
	}
	if exists && (rec.State == store.ForwardStateVerified || rec.State == store.ForwardStateGrandfathered) {
		return nil
	}
	if !exists {
		n, err := v.pendingCount(userID)
		if err != nil {
			return fmt.Errorf("forward verify: %w", err)
		}
		if n >= maxPendingForwardTargets {
			return ErrForwardTooManyPending
		}
		if err := v.targets.SetState(userID, addr, store.ForwardStatePending); err != nil {
			return fmt.Errorf("forward verify: %w", err)
		}
	} else if ch, has, gErr := v.targets.GetChallenge(userID, addr); gErr == nil && has && ch.ExpiresAt > time.Now().Unix() {
		// A live challenge is outstanding: do not mail the address again.
		return nil
	}
	return v.sendChallenge(userID, fromEmail, addr)
}

// pendingCount returns how many unverified targets userID holds.
func (v *ForwardVerifier) pendingCount(userID string) (int, error) {
	list, err := v.targets.ListByUser(userID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range list {
		if e.State == store.ForwardStatePending {
			n++
		}
	}
	return n, nil
}

// sendChallenge issues a fresh code for an existing pending target and mails it.
func (v *ForwardVerifier) sendChallenge(userID, fromEmail, addr string) error {
	if v.blobs == nil || v.queue == nil {
		return ErrForwardVerifyUnavailable
	}
	if v.limiter != nil && !v.limiter.Allow(userID) {
		return ErrForwardVerifyRateLimited
	}
	rawCode, err := generateVerificationCode()
	if err != nil {
		return fmt.Errorf("forward verify: %w", err)
	}
	ch := &bongov1.AddressVerification{
		CodeHash:  hashVerificationCode(rawCode),
		ExpiresAt: time.Now().Add(verifyCodeExpiry).Unix(),
	}
	if err := v.targets.PutChallenge(userID, addr, ch); err != nil {
		return fmt.Errorf("forward verify: %w", err)
	}

	domain := domainFromEmail(fromEmail)
	hash, err := v.blobs.Put(buildForwardVerificationEmail(addr, fromEmail, rawCode, domain))
	if err != nil {
		return fmt.Errorf("forward verify: store email: %w", err)
	}
	now := time.Now().UnixNano()
	out := &bongov1.OutboundMessage{
		Id:          fmt.Sprintf("fwdverify-%d", now),
		SenderId:    userID,
		SenderEmail: "postmaster@" + domain,
		Recipients:  []string{addr},
		BlobHash:    hash,
		MessageGuid: fmt.Sprintf("<fwdverify-%d@%s>", now, domain),
		Origin:      bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL,
	}
	if err := v.queue.Enqueue(out); err != nil {
		return fmt.Errorf("forward verify: enqueue: %w", err)
	}
	// The code is never logged.
	v.logger.Info("forward-verify: verification code sent", "user_id", userID, "address", addr)
	return nil
}

// Pending reports whether the target is awaiting verification.
func (v *ForwardVerifier) Pending(userID, target string) bool {
	rec, ok, err := v.targets.Get(userID, strings.ToLower(strings.TrimSpace(target)))
	return err == nil && ok && rec.State == store.ForwardStatePending
}

// View lists the user's targets for the settings page.
func (v *ForwardVerifier) View(userID string) []templates.ForwardTargetInfo {
	list, err := v.targets.ListByUser(userID)
	if err != nil {
		v.logger.Warn("forward-verify: failed to list targets", "user_id", userID, "error", err)
		return nil
	}
	out := make([]templates.ForwardTargetInfo, 0, len(list))
	for _, e := range list {
		out = append(out, templates.ForwardTargetInfo{Address: e.Address, State: e.State})
	}
	return out
}

// fwdText translates a message ID into the request's locale.
func fwdText(r *http.Request, id string) string {
	return i18n.T(middleware.LocaleFromContext(r.Context()), id)
}

// fwdError writes a translated plain-text error response.
func fwdError(w http.ResponseWriter, r *http.Request, id string, code int) {
	http.Error(w, fwdText(r, id), code)
}

// forwardRedirect redirects to the settings page with a translated message.
func forwardRedirect(w http.ResponseWriter, r *http.Request, key, id string) {
	http.Redirect(w, r, "/settings?"+key+"="+url.QueryEscape(fwdText(r, id)), http.StatusSeeOther)
}

// outcomeMessage maps an Ensure error to a user-facing message. ok is true for
// the soft outcomes where the target was recorded but no code went out.
func forwardOutcomeMessage(r *http.Request, err error) (msg string, soft bool) {
	switch {
	case errors.Is(err, ErrForwardVerifyRateLimited):
		return fwdText(r, "settings.fwdRateLimited"), true
	case errors.Is(err, ErrForwardVerifyUnavailable):
		return fwdText(r, "settings.fwdUnavailable"), true
	case errors.Is(err, ErrForwardTooManyPending):
		return fwdText(r, "settings.fwdTooManyPending"), true
	}
	return "", false
}

// Request sends a new verification code for a pending target.
//
// POST /settings/forwarding/request  (form: address)
func (v *ForwardVerifier) Request(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		fwdError(w, r, "settings.fwdErrForm", http.StatusBadRequest)
		return
	}
	addr, ok := parseForwardTarget(r.FormValue("address"))
	if !ok {
		forwardRedirect(w, r, "error", "settings.fwdInvalidAddress")
		return
	}
	rec, exists, err := v.targets.Get(userID, addr)
	if err != nil {
		v.logger.Error("forward-verify: request: lookup failed", "user_id", userID, "error", err)
		fwdError(w, r, "settings.fwdErrLookup", http.StatusInternalServerError)
		return
	}
	if !exists || rec.State != store.ForwardStatePending {
		forwardRedirect(w, r, "error", "settings.fwdNoPending")
		return
	}
	if err := v.sendChallenge(userID, email, addr); err != nil {
		if msg, soft := forwardOutcomeMessage(r, err); soft {
			http.Redirect(w, r, "/settings?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		v.logger.Error("forward-verify: request failed", "user_id", userID, "error", err)
		fwdError(w, r, "settings.fwdErrSend", http.StatusInternalServerError)
		return
	}
	forwardRedirect(w, r, "flash", "settings.fwdCodeSent")
}

// Submit checks a code and, when correct, marks the target verified.
//
// POST /settings/forwarding/verify  (form: address, code)
//
// The success path is two non-atomic Raft writes in a deliberate order: the
// verified record first, then a best-effort delete of the challenge. A crash
// between them leaves a verified target with a stale challenge, never a
// consumed code and an unverified target.
func (v *ForwardVerifier) Submit(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		fwdError(w, r, "settings.fwdErrForm", http.StatusBadRequest)
		return
	}
	addr, ok := parseForwardTarget(r.FormValue("address"))
	code := strings.TrimSpace(r.FormValue("code"))
	if !ok || code == "" {
		forwardRedirect(w, r, "error", "settings.fwdCodeRequired")
		return
	}

	rec, exists, err := v.targets.Get(userID, addr)
	if err != nil {
		v.logger.Error("forward-verify: submit: lookup failed", "user_id", userID, "error", err)
		fwdError(w, r, "settings.fwdErrLookup", http.StatusInternalServerError)
		return
	}
	if !exists || rec.State != store.ForwardStatePending {
		forwardRedirect(w, r, "error", "settings.fwdNoPending")
		return
	}
	ch, has, err := v.targets.GetChallenge(userID, addr)
	if err != nil {
		v.logger.Error("forward-verify: submit: challenge lookup failed", "user_id", userID, "error", err)
		fwdError(w, r, "settings.fwdErrChallenge", http.StatusInternalServerError)
		return
	}
	if !has {
		forwardRedirect(w, r, "error", "settings.fwdNoPending")
		return
	}
	if time.Now().Unix() > ch.ExpiresAt {
		if dErr := v.targets.DeleteChallenge(userID, addr); dErr != nil {
			v.logger.Warn("forward-verify: failed to delete expired challenge", "user_id", userID, "error", dErr)
		}
		forwardRedirect(w, r, "error", "settings.fwdCodeExpired")
		return
	}
	// Count the attempt before comparing so a correct code after the limit fails.
	ch.Attempts++
	if ch.Attempts > maxVerifyAttempts {
		if dErr := v.targets.DeleteChallenge(userID, addr); dErr != nil {
			v.logger.Warn("forward-verify: failed to delete over-limit challenge", "user_id", userID, "error", dErr)
		}
		forwardRedirect(w, r, "error", "settings.fwdTooManyAttempts")
		return
	}
	if subtle.ConstantTimeCompare(hashVerificationCode([]byte(code)), ch.CodeHash) != 1 {
		if pErr := v.targets.PutChallenge(userID, addr, ch); pErr != nil {
			v.logger.Warn("forward-verify: failed to record attempt", "user_id", userID, "error", pErr)
		}
		forwardRedirect(w, r, "error", "settings.fwdBadCode")
		return
	}

	if err := v.targets.SetState(userID, addr, store.ForwardStateVerified); err != nil {
		v.logger.Error("forward-verify: submit: failed to mark verified", "user_id", userID, "error", err)
		fwdError(w, r, "settings.fwdErrVerify", http.StatusInternalServerError)
		return
	}
	if dErr := v.targets.DeleteChallenge(userID, addr); dErr != nil {
		v.logger.Warn("forward-verify: failed to delete consumed challenge (non-fatal)", "user_id", userID, "error", dErr)
	}
	v.logger.Info("forward-verify: forwarding target verified", "user_id", userID, "address", addr)
	forwardRedirect(w, r, "flash", "settings.fwdVerified")
}

// buildForwardVerificationEmail builds the challenge mail. It names the account
// that asked for forwarding so the recipient can tell an unwanted request.
func buildForwardVerificationEmail(toAddress, forUser string, code []byte, domain string) []byte {
	var buf bytes.Buffer
	now := time.Now()
	fmt.Fprintf(&buf, "From: Bongo Mail <postmaster@%s>\r\n", domain)
	fmt.Fprintf(&buf, "To: %s\r\n", toAddress)
	buf.WriteString("Subject: Confirm mail forwarding to this address\r\n")
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <fwdverify-%d@%s>\r\n", now.UnixNano(), domain)
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	fmt.Fprintf(&buf, "The account %s asked to forward its mail to this address.\r\n\r\n", sanitizeHeaderText(forUser))
	buf.WriteString("To allow it, enter this code in the forwarding settings:\r\n\r\n")
	buf.Write(code)
	buf.WriteString("\r\n\r\nThis code expires in 25 minutes and can only be used once.\r\n")
	buf.WriteString("If you did not expect this, ignore this email and no mail will be forwarded.\r\n")
	buf.WriteString("\r\n-- Bongo Mail\r\n")
	return buf.Bytes()
}

// sanitizeHeaderText drops line breaks from text interpolated into a message.
func sanitizeHeaderText(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}
