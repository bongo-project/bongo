package handlers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-go/bongo-go/internal/queue"
	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	"github.com/bongo-go/bongo-go/internal/web/templates"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	// verifyCodeLen is the number of random bytes to generate for a one-time code.
	// 16 bytes → 32 hex chars, adequate entropy for a time-limited code.
	verifyCodeLen = 16

	// verifyCodeExpiry is how long a verification code remains valid.
	verifyCodeExpiry = 25 * time.Minute

	// maxVerifyAttempts is the maximum number of failed verify attempts before
	// the challenge is invalidated (brute-force guard).
	maxVerifyAttempts = 5

	// verifyRateLimit is the minimum interval between code requests for the
	// same (user, address) pair — prevents spam generation.
	// NOTE: enforced at the store level via the existing challenge check: if a
	// challenge exists and has not expired, a new request is rejected.
)

// AddressVerifyHandler handles self-serve address ownership verification.
//
// Dependencies injected via setter methods so nil-guards allow deployment on
// edge/agent nodes without all stores present.
type AddressVerifyHandler struct {
	addresses   *store.AddressStore
	verifyStore *store.AddressVerifyStore
	blobs       *store.BlobStore
	queueStore  *queue.QueueStore
	logger      *slog.Logger
}

// NewAddressVerifyHandler creates an AddressVerifyHandler.
func NewAddressVerifyHandler(logger *slog.Logger) *AddressVerifyHandler {
	return &AddressVerifyHandler{logger: logger}
}

// SetAddressStore injects the address store.
func (h *AddressVerifyHandler) SetAddressStore(as *store.AddressStore) {
	h.addresses = as
}

// SetVerifyStore injects the verification challenge store.
func (h *AddressVerifyHandler) SetVerifyStore(vs *store.AddressVerifyStore) {
	h.verifyStore = vs
}

// SetBlobStore injects the blob store for building verification emails.
func (h *AddressVerifyHandler) SetBlobStore(bs *store.BlobStore) {
	h.blobs = bs
}

// SetQueueStore injects the outbound queue store.
func (h *AddressVerifyHandler) SetQueueStore(qs *queue.QueueStore) {
	h.queueStore = qs
}

// ShowAddresses renders the self-serve address management page.
//
// GET /settings/addresses
func (h *AddressVerifyHandler) ShowAddresses(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	flash := r.URL.Query().Get("flash")
	errMsg := r.URL.Query().Get("error")

	var addrs []templates.UserAddressInfo
	if h.addresses != nil {
		list, err := h.addresses.ListByUser(userID)
		if err != nil {
			h.logger.Warn("address-verify: failed to list addresses", "user_id", userID, "error", err)
		}
		for _, addr := range list {
			ai := templates.UserAddressInfo{Address: addr}
			if rec, ok, recErr := h.addresses.Get(addr); recErr == nil && ok {
				ai.CanSend = rec.CanSend
				ai.Verified = rec.Verified
			}
			// Check for pending challenge.
			if h.verifyStore != nil {
				if ch, ok, _ := h.verifyStore.Get(userID, addr); ok && ch != nil {
					ai.PendingVerification = ch.ExpiresAt > time.Now().Unix()
				}
			}
			addrs = append(addrs, ai)
		}
	}

	templ.Handler(templates.AddressVerifyPage(addrs, email, flash, errMsg)).ServeHTTP(w, r)
}

// AddSendAsAddress adds an address that the user claims to own.
//
// POST /settings/addresses
// Form fields: address — the claimed email address.
//
// The address is stored with Kind=identity, CanSend=false, Verified=false.
// The user can subsequently request verification.
// Ownership: rejects if the address already belongs to a different user.
func (h *AddressVerifyHandler) AddSendAsAddress(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	if h.addresses == nil {
		http.Error(w, "address store not available", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	address := strings.ToLower(strings.TrimSpace(r.FormValue("address")))
	if address == "" {
		http.Redirect(w, r, "/settings/addresses?error=Address+is+required", http.StatusSeeOther)
		return
	}
	atIdx := strings.LastIndex(address, "@")
	if atIdx <= 0 || atIdx == len(address)-1 {
		http.Redirect(w, r, "/settings/addresses?error=Invalid+email+address", http.StatusSeeOther)
		return
	}
	domain := address[atIdx+1:]

	// Ownership check: if it already exists, it must belong to this user.
	existing, ok, err := h.addresses.Get(address)
	if err != nil {
		h.logger.Error("address-verify: add: failed to look up address", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if ok && existing.UserId != userID {
		http.Redirect(w, r, "/settings/addresses?error=Address+is+already+owned+by+another+account", http.StatusSeeOther)
		return
	}

	if !ok {
		// New address — create it (unverified).
		rec := &bongov1.AddressRecord{
			UserId:   userID,
			Domain:   domain,
			Kind:     "identity",
			CanSend:  false,
			Verified: false,
		}
		if err := h.addresses.Put(address, rec); err != nil {
			h.logger.Error("address-verify: add: failed to store address", "user_id", userID, "address", address, "error", err)
			http.Error(w, "failed to add address", http.StatusInternalServerError)
			return
		}
		h.logger.Info("address-verify: address added", "user_id", userID, "address", address)
	}

	http.Redirect(w, r, "/settings/addresses?flash=Address+added", http.StatusSeeOther)
}

// RequestVerification issues a one-time verification code for an owned address.
//
// POST /settings/addresses/{address}/verify-request
//
// Security properties:
//   - Ownership: rejects if address belongs to a different user.
//   - Rate limiting: rejects if a valid (non-expired) challenge already exists.
//   - Code security: generates via crypto/rand; stores only SHA-256 hash; NEVER logs the code.
//   - The code is emailed to the claimed address via the outbound queue.
func (h *AddressVerifyHandler) RequestVerification(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	address := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "address")))

	if h.addresses == nil || h.verifyStore == nil {
		http.Error(w, "address store not available", http.StatusInternalServerError)
		return
	}
	if h.blobs == nil || h.queueStore == nil {
		http.Error(w, "outbound delivery not configured on this node", http.StatusServiceUnavailable)
		return
	}

	// Ownership check.
	rec, ok, err := h.addresses.Get(address)
	if err != nil {
		h.logger.Error("address-verify: request: failed to look up address", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Redirect(w, r, "/settings/addresses?error=Address+not+found", http.StatusSeeOther)
		return
	}
	if rec.UserId != userID {
		http.Redirect(w, r, "/settings/addresses?error=Address+is+owned+by+another+account", http.StatusSeeOther)
		return
	}
	if rec.CanSend && rec.Verified {
		http.Redirect(w, r, "/settings/addresses?flash=Address+is+already+verified", http.StatusSeeOther)
		return
	}

	// Rate-limit: if a valid (non-expired) challenge already exists, reject.
	existing, hasChallenge, err := h.verifyStore.Get(userID, address)
	if err != nil {
		h.logger.Error("address-verify: request: failed to check existing challenge", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to check verification state", http.StatusInternalServerError)
		return
	}
	if hasChallenge && existing != nil && existing.ExpiresAt > time.Now().Unix() {
		http.Redirect(w, r, "/settings/addresses?error=A+verification+code+has+already+been+sent+to+this+address", http.StatusSeeOther)
		return
	}

	// Generate one-time code.
	rawCode, err := generateVerificationCode()
	if err != nil {
		h.logger.Error("address-verify: request: failed to generate code", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to generate verification code", http.StatusInternalServerError)
		return
	}
	// Hash the code — only the hash is stored; the plaintext is only emailed.
	codeHash := hashVerificationCode(rawCode)

	challenge := &bongov1.AddressVerification{
		CodeHash:  codeHash,
		ExpiresAt: time.Now().Add(verifyCodeExpiry).Unix(),
		Attempts:  0,
	}
	if err := h.verifyStore.Put(userID, address, challenge); err != nil {
		h.logger.Error("address-verify: request: failed to store challenge", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to store verification challenge", http.StatusInternalServerError)
		return
	}

	// Build and enqueue the verification email.
	// The plaintext code (rawCode) is embedded in the email body and sent to
	// the claimed address. It is NEVER logged — only stored as a hash above.
	domain := domainFromEmail(email) // use sender domain for postmaster From
	rawMsg := buildVerificationEmail(address, rawCode, domain)

	hash, err := h.blobs.Put(rawMsg)
	if err != nil {
		h.logger.Error("address-verify: request: failed to store email blob", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to store verification email", http.StatusInternalServerError)
		return
	}

	// Unique per request (UnixNano) and panic-safe — never slice userID, which
	// could be shorter than 8 chars, and avoid colliding IDs on repeat requests.
	msgID := fmt.Sprintf("verify-%d", time.Now().UnixNano())
	outMsg := &bongov1.OutboundMessage{
		Id:          msgID,
		SenderId:    userID,
		SenderEmail: fmt.Sprintf("postmaster@%s", domain),
		Recipients:  []string{address},
		BlobHash:    hash,
		MessageGuid: fmt.Sprintf("<addrverify-%d@%s>", time.Now().UnixNano(), domain),
	}
	if err := h.queueStore.Enqueue(outMsg); err != nil {
		h.logger.Error("address-verify: request: failed to enqueue verification email", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to send verification email", http.StatusInternalServerError)
		return
	}

	// Log success WITHOUT the code. The address is logged so operators can
	// identify issues, but the code itself is never included.
	h.logger.Info("address-verify: verification code sent", "user_id", userID, "address", address)

	http.Redirect(w, r, "/settings/addresses?flash=Verification+code+sent", http.StatusSeeOther)
}

// SubmitCode verifies a submitted code against the stored challenge.
//
// POST /settings/addresses/{address}/verify-submit
// Form fields: code — the one-time code the user received by email.
//
// Success path (two Raft writes, non-atomic, intentional ordering):
//  1. AddressStore.Put(Verified=true, CanSend=true) — performed first.
//  2. Delete challenge — performed second (best-effort).
//
// Ordering prevents a crash between writes from locking the user out:
// - If Put succeeds but Delete fails → address is verified, challenge is stale.
//   A subsequent submit finds mismatch/expired and correctly fails (idempotent).
// - If Delete succeeds but Put had not yet run → impossible (Put is first).
func (h *AddressVerifyHandler) SubmitCode(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())
	address := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "address")))

	if h.addresses == nil || h.verifyStore == nil {
		http.Error(w, "address store not available", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	// Normalize the submitted code (trim whitespace).
	submittedCode := strings.TrimSpace(r.FormValue("code"))
	if submittedCode == "" {
		http.Redirect(w, r, "/settings/addresses?error=Verification+code+is+required", http.StatusSeeOther)
		return
	}

	// Ownership check.
	rec, ok, err := h.addresses.Get(address)
	if err != nil {
		h.logger.Error("address-verify: submit: failed to look up address", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Redirect(w, r, "/settings/addresses?error=Address+not+found", http.StatusSeeOther)
		return
	}
	if rec.UserId != userID {
		http.Redirect(w, r, "/settings/addresses?error=Address+is+owned+by+another+account", http.StatusSeeOther)
		return
	}

	// Load challenge.
	challenge, hasChallenge, err := h.verifyStore.Get(userID, address)
	if err != nil {
		h.logger.Error("address-verify: submit: failed to load challenge", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to load verification challenge", http.StatusInternalServerError)
		return
	}
	if !hasChallenge || challenge == nil {
		http.Redirect(w, r, "/settings/addresses?error=No+pending+verification+for+this+address", http.StatusSeeOther)
		return
	}

	// Check expiry.
	if time.Now().Unix() > challenge.ExpiresAt {
		// Expired: delete the stale challenge and inform the user.
		if delErr := h.verifyStore.Delete(userID, address); delErr != nil {
			h.logger.Warn("address-verify: submit: failed to delete expired challenge", "user_id", userID, "address", address, "error", delErr)
		}
		http.Redirect(w, r, "/settings/addresses?error=Verification+code+has+expired", http.StatusSeeOther)
		return
	}

	// Increment attempt count first (before compare) so that even a correct
	// code on the first post-limit submit is rejected.
	challenge.Attempts++
	if challenge.Attempts > maxVerifyAttempts {
		// Too many attempts: invalidate the challenge entirely.
		if delErr := h.verifyStore.Delete(userID, address); delErr != nil {
			h.logger.Warn("address-verify: submit: failed to delete over-limit challenge", "user_id", userID, "address", address, "error", delErr)
		}
		h.logger.Warn("address-verify: submit: max attempts exceeded", "user_id", userID, "address", address)
		http.Redirect(w, r, "/settings/addresses?error=Too+many+failed+attempts.+Please+request+a+new+code.", http.StatusSeeOther)
		return
	}

	// Constant-time compare of hashes.
	submittedHash := hashVerificationCode([]byte(submittedCode))
	if subtle.ConstantTimeCompare(submittedHash, challenge.CodeHash) != 1 {
		// Wrong code: persist incremented attempt count.
		if putErr := h.verifyStore.Put(userID, address, challenge); putErr != nil {
			h.logger.Warn("address-verify: submit: failed to update attempt count", "user_id", userID, "address", address, "error", putErr)
		}
		h.logger.Warn("address-verify: submit: wrong code", "user_id", userID, "address", address, "attempts", challenge.Attempts)
		http.Redirect(w, r, "/settings/addresses?error=Incorrect+verification+code", http.StatusSeeOther)
		return
	}

	// Correct code: set Verified+CanSend on the address (step 1, must succeed).
	rec.CanSend = true
	rec.Verified = true
	if err := h.addresses.Put(address, rec); err != nil {
		h.logger.Error("address-verify: submit: failed to set verified+can_send", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to verify address", http.StatusInternalServerError)
		return
	}

	// Delete the challenge (step 2, best-effort — see partial-failure semantics
	// in the type-level comment on AddressVerifyStore).
	if delErr := h.verifyStore.Delete(userID, address); delErr != nil {
		h.logger.Warn("address-verify: submit: failed to delete consumed challenge (non-fatal)", "user_id", userID, "address", address, "error", delErr)
	}

	h.logger.Info("address-verify: address verified", "user_id", userID, "address", address)
	http.Redirect(w, r, "/settings/addresses?flash=Address+verified", http.StatusSeeOther)
}

// generateVerificationCode generates a cryptographically random code using
// crypto/rand. Returns the raw bytes (hex-encoded for human readability).
func generateVerificationCode() ([]byte, error) {
	raw := make([]byte, verifyCodeLen)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generating verification code: %w", err)
	}
	// Hex-encode for readability in the email body.
	encoded := make([]byte, verifyCodeLen*2)
	const hexChars = "0123456789abcdef"
	for i, b := range raw {
		encoded[i*2] = hexChars[b>>4]
		encoded[i*2+1] = hexChars[b&0xf]
	}
	return encoded, nil
}

// hashVerificationCode returns the SHA-256 hash of the code bytes.
// This is the only form stored in Pebble — the plaintext is never persisted.
func hashVerificationCode(code []byte) []byte {
	h := sha256.Sum256(code)
	return h[:]
}

// buildVerificationEmail constructs the RFC 5322 verification email.
// toAddress is the claimed address we're sending to; domain is used for
// the From/postmaster and Message-ID; code is the plaintext code bytes.
// The plaintext code is embedded in the body — the caller must never log
// the returned bytes or pass them to any logging function.
func buildVerificationEmail(toAddress string, code []byte, domain string) []byte {
	var buf bytes.Buffer
	now := time.Now()
	msgID := fmt.Sprintf("<addrverify-%d@%s>", now.UnixNano(), domain)

	fmt.Fprintf(&buf, "From: Bongo Mail <postmaster@%s>\r\n", domain)
	fmt.Fprintf(&buf, "To: %s\r\n", toAddress)
	buf.WriteString("Subject: Your Bongo Mail address verification code\r\n")
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: %s\r\n", msgID)
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString("Your Bongo Mail address verification code is:\r\n\r\n")
	buf.Write(code)
	buf.WriteString("\r\n\r\nThis code expires in 25 minutes and can only be used once.\r\n")
	buf.WriteString("If you did not request this, please ignore this email.\r\n")
	buf.WriteString("\r\n-- Bongo Mail\r\n")

	return buf.Bytes()
}

