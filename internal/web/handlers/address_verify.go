package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/queue"
	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/web/middleware"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
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

	// maxPendingSendAsClaims caps a user's pending send-as claims (bare or
	// with a live challenge). It bounds Pebble growth from the claim form.
	maxPendingSendAsClaims = 20

	// verifyQueueSize is the buffer of the background verification-mail
	// sender. A full buffer drops the job; the handler never blocks on it.
	verifyQueueSize = 64

	// Code requests for the same (user, address) pair are rate-limited without a
	// separate interval constant: if a challenge exists and has not expired, a
	// new request is rejected. This prevents mail-bombing a claimed address.
)

// AddressVerifyHandler handles self-serve address ownership verification.
//
// Dependencies injected via setter methods so nil-guards allow deployment on
// edge/agent nodes without all stores present.
type AddressVerifyHandler struct {
	addresses   *store.AddressStore
	verifyStore *store.AddressVerifyStore
	blobs       verifyBlobPutter
	queueStore  verifyEnqueuer
	pebble      *store.PebbleStore
	metadata    *store.MetadataManager
	applier     store.RaftApplier
	limiter     interface{ Allow(string) bool } // nil = unlimited; per-user verification-mail cap
	logger      *slog.Logger

	// jobs feeds the background sender (Run). It is never closed, so a late
	// Submit cannot panic; pending counts jobs submitted but not yet finished.
	jobs    chan verifyJob
	pending atomic.Int64

	// running is true while Run is consuming jobs. RequestVerification fails
	// closed when it is false, so a code is never promised with no sender.
	running atomic.Bool
}

// verifyBlobPutter and verifyEnqueuer are the slices of the blob and queue
// stores the sender uses; tests substitute fakes.
type verifyBlobPutter interface {
	Put(data []byte) (string, error)
}

type verifyEnqueuer interface {
	Enqueue(msg *bongov1.OutboundMessage) error
}

// verifyJob is one verification mail to build and queue. rawCode is the
// plaintext one-time code: never log a job or put it in an error.
type verifyJob struct {
	userID       string
	address      string
	rawCode      []byte
	senderDomain string
}

// Limiter returns the per-user verification-mail limiter, or nil.
func (h *AddressVerifyHandler) Limiter() interface{ Allow(string) bool } { return h.limiter }

// SetLimiter sets the per-user cap on verification mails sent.
func (h *AddressVerifyHandler) SetLimiter(l interface{ Allow(string) bool }) { h.limiter = l }

// NewAddressVerifyHandler creates an AddressVerifyHandler.
func NewAddressVerifyHandler(logger *slog.Logger) *AddressVerifyHandler {
	return &AddressVerifyHandler{logger: logger, jobs: make(chan verifyJob, verifyQueueSize)}
}

// SetAddressStore injects the address store.
func (h *AddressVerifyHandler) SetAddressStore(as *store.AddressStore) {
	h.addresses = as
}

// SetOwnershipStores injects what the claim flow needs to tell whether an
// address is hosted or already allocated: Pebble (domain and alias keys), the
// metadata index (users, addresses and aliases tables) and the applier.
// Without them claiming and verifying fail closed.
func (h *AddressVerifyHandler) SetOwnershipStores(ps *store.PebbleStore, md *store.MetadataManager, applier store.RaftApplier) {
	h.pebble = ps
	h.metadata = md
	h.applier = applier
}

// SetVerifyStore injects the verification challenge store.
func (h *AddressVerifyHandler) SetVerifyStore(vs *store.AddressVerifyStore) {
	h.verifyStore = vs
}

// SetBlobStore injects the blob store for building verification emails.
func (h *AddressVerifyHandler) SetBlobStore(bs *store.BlobStore) {
	if bs == nil {
		h.blobs = nil
		return
	}
	h.blobs = bs
}

// SetQueueStore injects the outbound queue store.
func (h *AddressVerifyHandler) SetQueueStore(qs *queue.QueueStore) {
	if qs == nil {
		h.queueStore = nil
		return
	}
	h.queueStore = qs
}

// submit hands a job to the background sender without blocking. It reports
// false when the buffer is full.
func (h *AddressVerifyHandler) submit(j verifyJob) bool {
	h.pending.Add(1)
	select {
	case h.jobs <- j:
		return true
	default:
		h.pending.Add(-1)
		return false
	}
}

// Run sends queued verification mails until ctx is cancelled, then drains what
// is still buffered and returns. The caller must cancel ctx only after the HTTP
// server has shut down (and must not derive it from a context that is cancelled
// earlier, such as the signal context), so every in-flight submit is already in
// the buffer when the drain starts. While Run is not consuming, requests fail
// closed.
func (h *AddressVerifyHandler) Run(ctx context.Context) {
	h.running.Store(true)
	defer h.running.Store(false)
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case j := <-h.jobs:
					h.process(j)
				default:
					return
				}
			}
		case j := <-h.jobs:
			h.process(j)
		}
	}
}

func (h *AddressVerifyHandler) process(j verifyJob) {
	defer h.pending.Add(-1)
	h.sendVerification(j)
}

// idle reports whether every submitted job has finished.
func (h *AddressVerifyHandler) idle() bool { return h.pending.Load() == 0 }

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
		// Pending claims are not address records; merge them in. A failed
		// best-effort claim delete after verification leaves an address in
		// both lists, so deduplicate.
		if h.verifyStore != nil {
			claims, cErr := h.verifyStore.ListByUser(userID)
			if cErr != nil {
				h.logger.Warn("address-verify: failed to list claims", "user_id", userID, "error", cErr)
			}
			list = append(list, claims...)
		}
		slices.Sort(list)
		list = slices.Compact(list)
		for _, addr := range list {
			ai := templates.UserAddressInfo{Address: addr}
			// Only the caller's own record may colour the row: for a claim the
			// record may belong to someone else, and showing its state would
			// reveal who holds the address.
			if rec, ok, recErr := h.addresses.Get(addr); recErr == nil && ok && rec.UserId == userID {
				ai.CanSend = rec.CanSend
				ai.Verified = rec.Verified
			}
			// PendingVerification means a code was sent and has not expired. A
			// bare claim (no code yet) is not pending, so the row keeps its
			// Request button.
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

// AddSendAsAddress records that the user claims to own an address.
//
// POST /settings/addresses
// Form fields: address — the claimed email address.
//
// Only a per-user pending claim is written (/user/{id}/addrverify/{address},
// no code, no expiry). No /system/address/ record exists until the user proves
// ownership in SubmitCode, so the settings list and every reply are the same
// whether or not another account already holds the address: the decision here
// depends on the caller's own state only. A domain this server hosts
// (including catch-all domains) is refused with the same reply and no claim:
// hosting is public through DNS, and extra local addresses are the admin's
// job. Fails closed when the hosting check cannot be made.
func (h *AddressVerifyHandler) AddSendAsAddress(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserFromContext(r.Context())

	if h.addresses == nil || h.verifyStore == nil {
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

	hosted, hErr := domainHostedLocally(h.pebble, domain)
	if hErr != nil {
		h.logger.Error("address-verify: add: domain check failed", "address", address, "error", hErr)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if hosted {
		h.logger.Info("address-verify: add refused: domain hosted here", "user_id", userID, "address", address)
		h.sendAsAddRedirect(w, r)
		return
	}

	own, ownErr := h.ownRecord(userID, address)
	if ownErr != nil {
		h.logger.Error("address-verify: add: failed to look up address", "address", address, "error", ownErr)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if own != nil && own.Verified {
		h.sendAsAddRedirect(w, r)
		return
	}

	// Never overwrite an existing claim: it may carry a live challenge.
	_, hasClaim, err := h.verifyStore.Get(userID, address)
	if err != nil {
		h.logger.Error("address-verify: add: failed to look up claim", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if !hasClaim {
		// Cap the caller's own pending claims. The reply depends only on the
		// caller's own state, so it is not an ownership oracle.
		pending, err := h.pendingClaimCount(userID)
		if err != nil {
			h.logger.Error("address-verify: add: failed to count claims", "user_id", userID, "error", err)
			http.Error(w, "failed to look up address", http.StatusInternalServerError)
			return
		}
		if pending >= maxPendingSendAsClaims {
			h.logger.Info("address-verify: add refused: too many pending claims", "user_id", userID)
			http.Redirect(w, r, "/settings/addresses?error="+url.QueryEscape(fwdText(r, "settings.sendAsTooManyPending")), http.StatusSeeOther)
			return
		}
		if err := h.verifyStore.Put(userID, address, &bongov1.AddressVerification{}); err != nil {
			h.logger.Error("address-verify: add: failed to store claim", "user_id", userID, "address", address, "error", err)
			http.Error(w, "failed to add address", http.StatusInternalServerError)
			return
		}
		h.logger.Info("address-verify: address claimed", "user_id", userID, "address", address)
	}

	h.sendAsAddRedirect(w, r)
}

// pendingClaimCount counts the caller's own claims. A stale claim left behind
// by a failed best-effort delete after verification, whose address the caller
// already holds verified, is not pending and is not counted.
func (h *AddressVerifyHandler) pendingClaimCount(userID string) (int, error) {
	claims, err := h.verifyStore.ListByUser(userID)
	if err != nil {
		return 0, fmt.Errorf("listing claims: %w", err)
	}
	if len(claims) < maxPendingSendAsClaims {
		return len(claims), nil
	}
	n := 0
	for _, addr := range claims {
		own, err := h.ownRecord(userID, addr)
		if err != nil {
			return 0, err
		}
		if own != nil && own.Verified {
			continue
		}
		n++
	}
	return n, nil
}

// ownRecord returns the address record for address when it belongs to userID,
// and nil otherwise. A record held by anyone else is deliberately
// indistinguishable from no record: callers decide on the caller's own state.
func (h *AddressVerifyHandler) ownRecord(userID, address string) (*bongov1.AddressRecord, error) {
	rec, ok, err := h.addresses.Get(address)
	if err != nil {
		return nil, fmt.Errorf("looking up address record: %w", err)
	}
	if !ok || rec.UserId != userID {
		return nil, nil
	}
	return rec, nil
}

// sendAsAddRedirect is the single reply to AddSendAsAddress for every
// non-error outcome: claimed, already claimed or held, hosted domain. The reply
// must not depend on who owns the address, or the form becomes an oracle for
// which addresses exist on any tenant.
func (h *AddressVerifyHandler) sendAsAddRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/settings/addresses?flash="+url.QueryEscape(fwdText(r, "settings.sendAsRequested")), http.StatusSeeOther)
}

// RequestVerification issues a one-time verification code for a claimed address.
//
// POST /settings/addresses/{address}/verify-request
//
// Security properties:
//   - Claim: requires the caller's own pending claim, or an existing unverified
//     record of their own (an implicit claim, so addresses added before claims
//     existed can still be verified). Everything up to the allocation check
//     depends on the caller's own state only.
//   - Rate limiting: rejects if a valid (non-expired) challenge already exists,
//     and counts every request against the per-user limiter.
//   - Allocation: the request path does no work that depends on who holds the
//     address. It always stores a challenge and returns the normal success
//     redirect; the background sender (Run) then checks allocation and, for a
//     held address (or a hosted domain), sends nothing and leaves the decoy
//     challenge, whose code nobody holds. A different reply, or no stored
//     challenge, would let a second request tell a held address from a free one.
//     The send is best-effort and asynchronous: a failure is logged without the
//     code and resets the claim so the user can request again.
//   - Code security: generates via crypto/rand; stores only SHA-256 hash; NEVER logs the code.
//   - The code is emailed to the claimed address via the outbound queue.
//   - Fails closed (500) before any write if the ownership, blob or queue
//     stores are missing or the background sender is not running.
func (h *AddressVerifyHandler) RequestVerification(w http.ResponseWriter, r *http.Request) {
	userID, email := middleware.UserFromContext(r.Context())
	address := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "address")))

	if h.addresses == nil || h.verifyStore == nil {
		http.Error(w, "address store not available", http.StatusInternalServerError)
		return
	}
	// Fail closed, before any write, whatever the address: without the
	// ownership stores, the blob and queue stores, or a running sender the
	// reply would promise a code that is never sent.
	if h.pebble == nil || h.metadata == nil || h.applier == nil {
		http.Error(w, "address ownership check not available", http.StatusInternalServerError)
		return
	}
	if h.blobs == nil || h.queueStore == nil {
		http.Error(w, "outbound delivery not configured on this node", http.StatusInternalServerError)
		return
	}
	if !h.running.Load() {
		http.Error(w, "verification sender not running", http.StatusInternalServerError)
		return
	}

	own, err := h.ownRecord(userID, address)
	if err != nil {
		h.logger.Error("address-verify: request: failed to look up address", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if own != nil && own.CanSend && own.Verified {
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
	if !hasChallenge && own == nil {
		http.Redirect(w, r, "/settings/addresses?error=Address+not+found", http.StatusSeeOther)
		return
	}
	if hasChallenge && existing != nil && existing.ExpiresAt > time.Now().Unix() {
		http.Redirect(w, r, "/settings/addresses?error=A+verification+code+has+already+been+sent+to+this+address", http.StatusSeeOther)
		return
	}

	// Per-user cap on verification mails. Checked after the claim and
	// live-challenge checks and before any write, so a refusal strands no
	// challenge. The response is constant and never names the address.
	if h.limiter != nil && !h.limiter.Allow(userID) {
		h.logger.Warn("address-verify: request: per-user limit reached", "user_id", userID)
		http.Redirect(w, r, "/settings/addresses?error=Too+many+verification+requests,+try+again+later", http.StatusSeeOther)
		return
	}

	// The held-or-free decision is made on the background sender, not here, so
	// nothing on this path depends on who holds the address.

	// Generate one-time code.
	rawCode, err := generateVerificationCode()
	if err != nil {
		h.logger.Error("address-verify: request: failed to generate code", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to generate verification code", http.StatusInternalServerError)
		return
	}
	// Hash the code; only the hash is stored, the plaintext is only emailed.
	challenge := &bongov1.AddressVerification{
		CodeHash:  hashVerificationCode(rawCode),
		ExpiresAt: time.Now().Add(verifyCodeExpiry).Unix(),
		Attempts:  0,
	}
	if err := h.verifyStore.Put(userID, address, challenge); err != nil {
		h.logger.Error("address-verify: request: failed to store challenge", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to store verification challenge", http.StatusInternalServerError)
		return
	}

	// Hand the mail to the background sender. A full buffer drops the job and
	// resets the claim so the user can ask again; the reply is the same.
	if !h.submit(verifyJob{userID: userID, address: address, rawCode: rawCode, senderDomain: domainFromEmail(email)}) {
		h.logger.Warn("address-verify: request: sender queue full, verification not sent", "user_id", userID, "address", address)
		h.resetClaim(userID, address, "sender queue full")
	}

	http.Redirect(w, r, "/settings/addresses?flash=Verification+code+sent", http.StatusSeeOther)
}

// SubmitCode verifies a submitted code against the stored challenge.
//
// POST /settings/addresses/{address}/verify-submit
// Form fields: code — the one-time code the user received by email.
//
// It gates on the caller's own claim (or unverified record), never on who
// else holds the address. An expired or exhausted challenge is reset to a bare
// claim, not deleted, so the address stays listed and a new code can be
// requested. A correct code re-runs the allocation check before anything is
// written, because the address may have been taken since the request (another
// user verified first, an account or alias was created); in that case the
// claim is dropped and the generic "verification failed" reply is returned,
// leaving the holder's record untouched. The check and the write are separate
// steps (no Raft primitive makes them atomic), so two submits racing in the
// same instant can still both pass it and the later write wins.
//
// The success path is two non-atomic Raft writes in a deliberate order:
// AddressStore.Put(Verified=true, CanSend=true) first, then a best-effort delete
// of the claim. That order prevents a crash between the writes from locking
// the user out — if the Put succeeds and the Delete fails, the address is
// verified with a stale claim and a later submit correctly fails on
// mismatch/expiry (idempotent); the reverse, a consumed challenge with an
// unverified address, cannot happen.
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

	own, err := h.ownRecord(userID, address)
	if err != nil {
		h.logger.Error("address-verify: submit: failed to look up address", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}

	// Load challenge.
	challenge, hasChallenge, err := h.verifyStore.Get(userID, address)
	if err != nil {
		h.logger.Error("address-verify: submit: failed to load challenge", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to load verification challenge", http.StatusInternalServerError)
		return
	}
	if !hasChallenge && own == nil {
		http.Redirect(w, r, "/settings/addresses?error=Address+not+found", http.StatusSeeOther)
		return
	}
	// A bare claim has no code to check yet.
	if !hasChallenge || challenge == nil || len(challenge.CodeHash) == 0 {
		http.Redirect(w, r, "/settings/addresses?error=No+pending+verification+for+this+address", http.StatusSeeOther)
		return
	}

	// Check expiry.
	if time.Now().Unix() > challenge.ExpiresAt {
		h.resetClaim(userID, address, "expired")
		http.Redirect(w, r, "/settings/addresses?error=Verification+code+has+expired", http.StatusSeeOther)
		return
	}

	// Increment attempt count first (before compare) so that even a correct
	// code on the first post-limit submit is rejected.
	challenge.Attempts++
	if challenge.Attempts > maxVerifyAttempts {
		h.resetClaim(userID, address, "over-limit")
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

	// Correct code. The address may have been allocated since the request.
	unavailable, err := h.addressUnavailable(userID, address)
	if err != nil {
		h.logger.Error("address-verify: submit: ownership check failed", "address", address, "error", err)
		http.Error(w, "failed to look up address", http.StatusInternalServerError)
		return
	}
	if unavailable {
		h.logger.Info("address-verify: submit refused: address allocated since the request", "user_id", userID, "address", address)
		if delErr := h.verifyStore.Delete(userID, address); delErr != nil {
			h.logger.Warn("address-verify: submit: failed to delete refused claim (non-fatal)", "user_id", userID, "address", address, "error", delErr)
		}
		http.Redirect(w, r, "/settings/addresses?error="+url.QueryEscape(fwdText(r, "settings.sendAsVerificationFailed")), http.StatusSeeOther)
		return
	}

	// Set Verified+CanSend on the address (first write, must succeed). An
	// existing record of the caller's own keeps its kind.
	rec := own
	if rec == nil {
		rec = &bongov1.AddressRecord{
			UserId: userID,
			Domain: address[strings.LastIndex(address, "@")+1:],
			Kind:   "identity",
		}
	}
	rec.CanSend = true
	rec.Verified = true
	if err := h.addresses.Put(address, rec); err != nil {
		h.logger.Error("address-verify: submit: failed to set verified+can_send", "user_id", userID, "address", address, "error", err)
		http.Error(w, "failed to verify address", http.StatusInternalServerError)
		return
	}

	// Delete the claim (second write, best-effort — see partial-failure
	// semantics in the type-level comment on AddressVerifyStore).
	if delErr := h.verifyStore.Delete(userID, address); delErr != nil {
		h.logger.Warn("address-verify: submit: failed to delete consumed claim (non-fatal)", "user_id", userID, "address", address, "error", delErr)
	}

	h.logger.Info("address-verify: address verified", "user_id", userID, "address", address)
	http.Redirect(w, r, "/settings/addresses?flash=Address+verified", http.StatusSeeOther)
}

// resetClaim clears a spent challenge back to a bare claim, keeping the
// address listed for its user. A failed reset is logged; the stale challenge
// then fails the same check on the next submit.
func (h *AddressVerifyHandler) resetClaim(userID, address, reason string) {
	if err := h.verifyStore.Put(userID, address, &bongov1.AddressVerification{}); err != nil {
		h.logger.Warn("address-verify: failed to reset claim", "user_id", userID, "address", address, "reason", reason, "error", err)
	}
}

// addressUnavailable reports whether address can no longer be allocated to
// userID: its domain is hosted here, or an account, alias or another user's
// record holds it. The caller's own records do not count. Any other error is a
// failed read and callers must fail closed.
func (h *AddressVerifyHandler) addressUnavailable(userID, address string) (bool, error) {
	hosted, err := domainHostedLocally(h.pebble, address[strings.LastIndex(address, "@")+1:])
	if err != nil {
		return false, err
	}
	if hosted {
		return true, nil
	}
	if err := addressAllocationError(h.pebble, h.metadata, h.applier, address, userID); err != nil {
		if errors.Is(err, auth.ErrAccountExists) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// sendVerification runs on the background sender. It makes the allocation
// decision and, for a free address, builds and queues the mail. None of this is
// on the request path, so the reply cannot reveal whether the address is held.
// Failures are logged with context but never the code, and reset the claim to
// a bare one so the user can request again at once. A held address keeps its
// decoy challenge and is not reset: a reset would let a second request tell
// held from free.
func (h *AddressVerifyHandler) sendVerification(j verifyJob) {
	unavailable, err := h.addressUnavailable(j.userID, j.address)
	if err != nil {
		h.logger.Error("address-verify: verification not sent: ownership check failed", "user_id", j.userID, "address", j.address, "error", err)
		h.resetClaim(j.userID, j.address, "ownership check failed")
		return
	}
	if unavailable {
		h.logger.Info("address-verify: verification not sent: address already allocated", "user_id", j.userID, "address", j.address)
		return
	}

	rawMsg := buildVerificationEmail(j.address, j.rawCode, j.senderDomain)
	hash, err := h.blobs.Put(rawMsg)
	if err != nil {
		h.logger.Error("address-verify: verification not sent: failed to store email blob", "user_id", j.userID, "address", j.address, "error", err)
		h.resetClaim(j.userID, j.address, "blob write failed")
		return
	}

	// Unique per request (UnixNano) and panic-safe: never slice userID, which
	// could be shorter than 8 chars.
	now := time.Now().UnixNano()
	outMsg := &bongov1.OutboundMessage{
		Id:          fmt.Sprintf("verify-%d", now),
		SenderId:    j.userID,
		SenderEmail: fmt.Sprintf("postmaster@%s", j.senderDomain),
		Recipients:  []string{j.address},
		BlobHash:    hash,
		MessageGuid: fmt.Sprintf("<addrverify-%d@%s>", now, j.senderDomain),
		Origin:      bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL,
	}
	if err := h.queueStore.Enqueue(outMsg); err != nil {
		h.logger.Error("address-verify: verification not sent: failed to enqueue email", "user_id", j.userID, "address", j.address, "error", err)
		h.resetClaim(j.userID, j.address, "enqueue failed")
		return
	}
	h.logger.Info("address-verify: verification code sent", "user_id", j.userID, "address", j.address)
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
