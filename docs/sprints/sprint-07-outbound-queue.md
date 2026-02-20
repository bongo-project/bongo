# Sprint 07: Outbound SMTP Relay & Message Queue

**Phase:** 1 (Foundation)
**Goal:** Deliver mail to external recipients via SMTP relay with persistent queue, DKIM signing, SPF verification, rate limiting, and bounce generation — so bongo-go becomes a fully functional send+receive mail server.

---

## Context

Sprints 01–06 built the complete inbound pipeline: SMTP receives mail, Raft replicates
it, Pebble stores it, IMAP and the web UI let users read it. But the compose handler
silently drops external recipients — users can only send to local addresses. This sprint
closes that gap.

**Design decisions:**
- **Queue persistence:** Raft-replicated Pebble keys. The queue survives leader failover
  and any node can inspect it via CLI.
- **Worker model:** Leader-only processing. Followers skip the poll loop. Raft already
  provides election — no extra distributed lock needed.
- **DKIM library:** `github.com/emersion/go-msgauth` — same author as go-smtp/go-imap,
  supports ed25519 + RSA.
- **MX lookup:** `net.LookupMX` (stdlib) for MX records, `github.com/miekg/dns`
  (already indirect dep) for SPF TXT queries.
- **Bounce handling:** Generate RFC 3464 DSN bounce and deliver to sender's INBOX via
  the existing local delivery path.

**Existing config (already defined, just needs usage):**
```go
type SMTPRelay struct {
    Enabled bool   // smtp.relay.enabled
    Host    string // smtp.relay.host (e.g. "smtp.example.com:587")
}
type SMTPDKIMConfig struct {
    Selector       string // smtp.dkim.selector (e.g. "mail")
    PrivateKeyFile string // smtp.dkim.private_key_file
}
```

## No cleanup items (sprint 06 passed clean)

---

## Tasks

### 1. Proto definitions for queue messages

Define protobuf messages for the outbound queue.

**Create `proto/bongo/v1/queue.proto`:**
- `enum MessageStatus { MESSAGE_STATUS_UNSPECIFIED = 0; MESSAGE_STATUS_PENDING = 1; MESSAGE_STATUS_FAILED = 2; MESSAGE_STATUS_DELIVERED = 3; MESSAGE_STATUS_BOUNCED = 4; }`
- `message OutboundMessage`:
  - `string id = 1` — UUID for deduplication
  - `string sender_id = 2` — user ID of sender (for bounce delivery)
  - `string sender_email = 3` — MAIL FROM address
  - `repeated string recipients = 4` — RCPT TO list (external only)
  - `string blob_hash = 5` — SHA-256 ref to raw RFC 5322 message in blob store
  - `int32 attempt_count = 6` — delivery attempts so far (0–10)
  - `int64 created_at = 7` — Unix timestamp when queued
  - `int64 next_retry_at = 8` — Unix timestamp for next attempt
  - `MessageStatus status = 9`
  - `string status_reason = 10` — last SMTP error string
  - `repeated string failed_recipients = 11` — permanently bounced recipients
  - `string message_guid = 12` — original MailMeta GUID (for bounce In-Reply-To)
- `message QueueStats`:
  - `int32 pending_count = 1`
  - `int32 failed_count = 2`
  - `int64 oldest_pending_seconds = 3`

**Done when:** `protoc` generates Go code. `OutboundMessage` marshals/unmarshals correctly.

### 2. Queue store (Pebble + Raft)

Persistent queue storage using the existing Raft-replicated Pebble store.

**Create `internal/queue/store.go`:**
- `type QueueStore struct` — wraps `*store.PebbleStore` and `store.RaftApplier`
- Key layout: `/queue/pending/{next_retry_at_padded}-{uuid}` → OutboundMessage proto.
  Zero-padded 20-digit timestamp prefix gives natural oldest-first ordering on prefix scan.
- Methods:
  - `Enqueue(msg *bongov1.OutboundMessage) error` — set `next_retry_at = now`,
    generate key, marshal proto, apply via Raft (OP_PUT).
  - `GetReady(now time.Time, batchSize int) ([]*bongov1.OutboundMessage, error)` —
    prefix scan `/queue/pending/`, collect messages where `next_retry_at <= now`,
    return up to `batchSize` (default 100).
  - `MarkDelivered(id string) error` — delete `/queue/pending/*-{id}` via Raft.
  - `MarkFailed(msg *bongov1.OutboundMessage) error` — increment `attempt_count`,
    compute exponential backoff: `nextRetry = now + min(2^attempt × 60s, 24h)`.
    Delete old key, write new key with updated timestamp prefix.
  - `MarkBounced(msg *bongov1.OutboundMessage) error` — delete pending key,
    write to `/queue/bounced/{id}` for audit trail.
  - `Stats() (*bongov1.QueueStats, error)` — prefix scan pending + bounced, count.
  - `Get(id string) (*bongov1.OutboundMessage, error)` — scan for key containing id.
  - `Delete(id string) error` — remove from pending (for `queue drop` CLI).
  - `RetryNow(id string) error` — reset `next_retry_at` to now (for `queue retry` CLI).

**Create `internal/queue/store_test.go`:**
- Test enqueue + get ready (message appears after enqueue)
- Test mark failed (attempt count increments, backoff grows)
- Test mark delivered (message gone from pending)
- Test mark bounced (message in bounced namespace)
- Test backoff math: attempt 1 = 2min, attempt 5 = ~32min, attempt 10 = capped at 24h
- Test stats counting

**Done when:** `go test -race ./internal/queue/...` passes. All writes are Raft-consistent.

### 3. SMTP relay client

Deliver messages to external SMTP servers via MX lookup or smart relay.

**Create `internal/smtp/relay.go`:**
- `type Relay struct` — config, logger
- `func NewRelay(cfg *config.SMTPRelay, logger *slog.Logger) *Relay`
- `func (r *Relay) Deliver(ctx context.Context, from string, recipients []string, rawMsg []byte) (permanentFails []string, err error)`:
  - If `config.SMTPRelay.Enabled && config.SMTPRelay.Host != ""`:
    - Smart relay mode: connect directly to configured host.
  - Else:
    - Group recipients by domain.
    - For each domain: `net.LookupMX(domain)` → sort by priority → try each MX host.
  - For each target host:
    - `net.DialTimeout("tcp", host+":25", 30*time.Second)`
    - `smtp.NewClient(conn, host)` (stdlib `net/smtp`)
    - `client.StartTLS(&tls.Config{ServerName: host})` (opportunistic, don't fail if unavailable)
    - `client.Mail(from)` → `client.Rcpt(rcpt)` for each recipient → `client.Data()` write rawMsg
    - Handle per-recipient errors:
      - 4xx → temp error (caller retries entire message)
      - 5xx → add recipient to `permanentFails`, continue to next recipient
      - Network/timeout → temp error
    - `client.Quit()`
  - Return `permanentFails` and any temp error.

**Create `internal/smtp/relay_test.go`:**
- Start a mock SMTP server (use `emersion/go-smtp` in test) that accepts all mail.
- Test: deliver single recipient → verify mock received the message.
- Test: deliver multi-domain → verify both domains contacted.
- Test: mock returns 550 for one recipient → verify it's in permanentFails.
- Test: mock returns 421 → verify temp error returned.
- Test: smart relay mode → verify connects to relay host, not MX.

**Done when:** `go test -race ./internal/smtp/...` passes with relay tests.

### 4. DKIM signing

Sign outbound messages with DKIM before delivery.

**Create `internal/mail/dkim.go`:**
- `type DKIMSigner struct` — selector, domain, private key
- `func NewDKIMSigner(cfg *config.SMTPDKIMConfig, domain string) (*DKIMSigner, error)`:
  - If `cfg.PrivateKeyFile == ""`, return nil (DKIM disabled, no-op).
  - Read PEM file, try parse as ed25519 first, then RSA.
  - Store parsed key + selector + domain.
- `func (ds *DKIMSigner) Sign(rawMsg []byte) ([]byte, error)`:
  - If signer is nil, return rawMsg unchanged (passthrough).
  - Use `github.com/emersion/go-msgauth/dkim`:
    - `options.Selector = ds.selector`
    - `options.Domain = ds.domain`
    - `options.HeaderCanonicalization = dkim.CanonicalizationRelaxed`
    - `options.BodyCanonicalization = dkim.CanonicalizationRelaxed`
    - `options.HeaderKeys = []string{"from", "to", "subject", "date", "message-id", "content-type"}`
  - Sign → prepend `DKIM-Signature` header → return signed message.

**Add to `go.mod`:** `github.com/emersion/go-msgauth`

**Create `internal/mail/dkim_test.go`:**
- Generate test ed25519 key pair in test.
- Sign a sample RFC 5322 message.
- Verify `DKIM-Signature` header is present and parseable.
- Test nil signer returns message unchanged.

**Done when:** `go test -race ./internal/mail/...` passes. Signed messages have valid DKIM-Signature.

### 5. SPF checking on inbound

Verify sender SPF records when receiving mail.

**Create `internal/mail/spf.go`:**
- `type SPFChecker struct` — DNS resolver (mockable)
- `func (sc *SPFChecker) Check(ctx context.Context, senderIP net.IP, senderDomain string) (result string, err error)`:
  - Look up TXT record for `senderDomain` via DNS.
  - Find record starting with `v=spf1`.
  - Parse mechanisms: `ip4:`, `ip6:`, `a`, `mx`, `include:`, `all`.
  - Evaluate `senderIP` against each mechanism in order.
  - Return result: `pass`, `fail`, `softfail`, `neutral`, `none`, `temperror`, `permerror`.
- Interface `DNSResolver` for testability: `LookupTXT(ctx, domain) ([]string, error)`.
  Production uses `net.DefaultResolver`. Tests use mock.

**Modify `internal/smtp/server.go`:**
- In the `Session`, after `Mail()` captures the sender:
  - Extract domain from sender email.
  - Call `SPFChecker.Check(ctx, conn.RemoteAddr, domain)`.
  - Store result string on session.
- In `Data()`, before passing to delivery pipeline:
  - Prepend `Received-SPF: {result} (bongo-go; domain={domain})` header to raw message.
  - Log SPF result at Info level.

**Create `internal/mail/spf_test.go`:**
- Mock DNS returning various SPF policies.
- Test: `v=spf1 ip4:192.168.1.0/24 -all` with matching IP → `pass`.
- Test: same policy with non-matching IP → `fail`.
- Test: `v=spf1 mx -all` with IP matching MX → `pass`.
- Test: no TXT record → `none`.

**Done when:** `go test -race ./internal/mail/...` passes. Inbound messages get `Received-SPF` header.

### 6. Per-sender rate limiting

Prevent abuse by limiting outbound message rate per sender.

**Create `internal/mail/ratelimit.go`:**
- `type RateLimiter struct` — `sync.Mutex`, map of sender → `[]time.Time` (sliding window)
- `func NewRateLimiter(msgsPerHour int) *RateLimiter`:
  - If `msgsPerHour <= 0`, limiter is disabled (Allow always returns true).
- `func (rl *RateLimiter) Allow(sender string) bool`:
  - Lock, get or create entry for sender.
  - Discard timestamps older than 1 hour.
  - If `len(remaining) >= rl.limit`, return false.
  - Append `time.Now()`, return true.
- Uses `config.SMTP.Limits.FloodThreshold` (already exists, default 0 = unlimited).

**Create `internal/mail/ratelimit_test.go`:**
- Test: limit=5, send 5 → all allowed, 6th rejected.
- Test: limit=5, send 5, wait for window to expire, send again → allowed.
- Test: limit=0 (disabled) → always allowed.

**Done when:** `go test -race ./internal/mail/...` passes. Rate limit enforced in compose handler.

### 7. Queue worker (leader-only background loop)

Background goroutine that processes the outbound queue.

**Create `internal/queue/worker.go`:**
- `type Worker struct` — store, relay, signer, raft, bouncer, logger
- `func NewWorker(store *QueueStore, relay *smtp.Relay, signer *mail.DKIMSigner, raft *cluster.RaftNode, bouncer *mail.BounceGenerator, blobs *store.BlobStore, logger *slog.Logger) *Worker`
- `func (w *Worker) Run(ctx context.Context)`:
  - Ticker: 5 seconds.
  - Each tick:
    - If `!w.raft.IsLeader()` → skip (followers idle).
    - `msgs := w.store.GetReady(time.Now(), 100)`
    - Process up to 5 concurrently (semaphore channel):
      - Read raw message blob from BlobStore via `msg.blob_hash`.
      - If signer configured: `signed = w.signer.Sign(rawMsg)`.
      - `permanentFails, err := w.relay.Deliver(ctx, msg.sender_email, msg.recipients, signed)`
      - If `err != nil` (temp failure):
        - If `msg.attempt_count >= 10`: mark bounced, generate bounce for all recipients.
        - Else: `w.store.MarkFailed(msg)` (increments attempt, sets backoff).
      - If `err == nil`:
        - If `len(permanentFails) > 0`: generate bounce for failed recipients.
        - Mark remaining recipients as delivered: `w.store.MarkDelivered(msg.id)`.
- `func (w *Worker) Stop()` — cancel context, wait up to 5s for in-flight.

**Create `internal/queue/worker_test.go`:**
- Test: enqueue message → worker picks it up and calls relay (mock relay).
- Test: relay returns temp error → message retried with incremented attempt.
- Test: relay returns permanent fail → bounce generated.
- Test: max attempts exceeded → bounce generated for all recipients.
- Test: non-leader → worker idles.

**Done when:** `go test -race ./internal/queue/...` passes. Worker correctly processes, retries, and bounces.

### 8. Bounce message generation

Create RFC 3464 DSN bounce messages and deliver to sender's INBOX.

**Create `internal/mail/bounce.go`:**
- `type BounceGenerator struct` — delivery pipeline (for local delivery), blobs, logger
- `func NewBounceGenerator(deliverer smtp.Deliverer, blobs *store.BlobStore, logger *slog.Logger) *BounceGenerator`
- `func (bg *BounceGenerator) GenerateAndDeliver(original *bongov1.OutboundMessage, failedRecipients []string, reason string) error`:
  - Build RFC 5322 bounce message:
    ```
    From: MAILER-DAEMON <postmaster@localhost>
    To: {original.sender_email}
    Subject: Undelivered Mail Returned to Sender
    Date: {now RFC 2822}
    Message-ID: <bounce-{uuid}@localhost>
    In-Reply-To: {original.message_guid}
    Content-Type: multipart/report; report-type=delivery-status
    ```
  - Body part 1 (human-readable): "The following message could not be delivered to one or
    more recipients:" + list of failed recipients + reason.
  - Body part 2 (machine-readable DSN): `Reporting-MTA`, `Final-Recipient`, `Action: failed`,
    `Status: 5.x.x`, `Diagnostic-Code`.
  - Deliver via `deliverer.Deliver("MAILER-DAEMON@localhost", []string{original.sender_email}, bounceBytes)`.
    This uses the existing inbound delivery pipeline, so the bounce goes through Raft,
    Pebble, SQLite, and Bleve — appearing in the sender's INBOX.

**Create `internal/mail/bounce_test.go`:**
- Test: generate bounce → verify RFC 5322 valid (parseable by enmime).
- Test: verify Subject, From, To, In-Reply-To headers.
- Test: verify failed recipients listed in body.

**Done when:** `go test -race ./internal/mail/...` passes. Bounces parse as valid RFC 5322.

### 9. Wire compose handler to enqueue external recipients

Update the compose flow to queue external recipients instead of dropping them.

**Modify `internal/web/handlers/compose.go`:**
- Add fields to `ComposeHandler`:
  - `queueStore *queue.QueueStore`
  - `rateLimiter *mail.RateLimiter`
- In `Send()`:
  - After collecting all recipients, split into two lists:
    - `localRecipients`: `resolveLocalUser(rcpt)` succeeds → deliver immediately (existing code).
    - `externalRecipients`: `resolveLocalUser(rcpt)` returns "" → queue for relay.
  - For external recipients:
    - Check `rateLimiter.Allow(senderEmail)` → if false, return 429 with flash
      "Rate limit exceeded. Please try again later."
    - Store the raw RFC 5322 message blob (already done for local delivery).
    - Build `OutboundMessage` proto: sender_id, sender_email, external recipients,
      blob_hash, message_guid, created_at = now.
    - Call `queueStore.Enqueue(msg)`.
  - Update flash message:
    - Local only: "Message sent"
    - External only: "Message queued for delivery"
    - Mixed: "Message sent to local recipients; external delivery queued"

**Done when:** Composing to `user@external.com` enqueues the message. Composing to
`admin@localhost` delivers immediately. Mixed works. Rate limit triggers 429.

### 10. Queue CLI commands + HTTP endpoint

CLI and HTTP API for queue inspection and management.

**Create `cmd/bongo-go/queue.go`:**
- `bongo-go queue status` — GET `/internal/queue/stats` → print table:
  ```
  Pending:  5
  Bounced:  2
  Oldest:   12m ago
  ```
- `bongo-go queue retry {id}` — POST `/internal/queue/{id}/retry` → reset message.
- `bongo-go queue drop {id}` — DELETE `/internal/queue/{id}` → remove from queue.
- Flags: `--http-addr` (default `127.0.0.1:8080`), `--json` for machine output.

**Create `internal/web/handlers/queue.go`:**
- `type QueueHandler struct` — queueStore
- `GET /internal/queue/stats` → JSON `QueueStats`.
- `POST /internal/queue/{id}/retry` → call `queueStore.RetryNow(id)`.
- `DELETE /internal/queue/{id}` → call `queueStore.Delete(id)`.

**Done when:** `bongo-go queue status` works against a running dev agent. Retry and drop
return success. JSON output parseable.

### 11. Agent lifecycle wiring

Wire all new subsystems into agent startup and shutdown.

**Modify `internal/agent/agent.go`:**
- Add fields:
  - `QueueStore *queue.QueueStore`
  - `QueueWorker *queue.Worker`
  - `DKIMSigner *mail.DKIMSigner`
  - `Relay *smtp.Relay`
  - `RateLimiter *mail.RateLimiter`
  - `SPFChecker *mail.SPFChecker`
  - `BounceGen *mail.BounceGenerator`
  - `queueWorkerCancel context.CancelFunc`
- In `Run()`, after SMTP/IMAP startup:
  - Init `QueueStore` (wraps existing Pebble + Raft).
  - Init `DKIMSigner` from config (nil if no key file).
  - Init `Relay` from config.
  - Init `RateLimiter` from `config.SMTP.Limits.FloodThreshold`.
  - Init `SPFChecker`.
  - Init `BounceGenerator` (pass delivery pipeline + blobs).
  - Create `Worker` with all deps.
  - `queueCtx, queueCancel := context.WithCancel(ctx)`
  - `go w.QueueWorker.Run(queueCtx)`
- Wire `QueueHandler` into chi router at `/internal/queue/*`.
- Pass `QueueStore` + `RateLimiter` to `ComposeHandler`.
- Pass `SPFChecker` to SMTP `Backend`.
- Shutdown order: HTTP → SMTP → IMAP → **Queue worker** → blob sync → gossip → raft → …

**Done when:** `bongo-go serve --dev` starts the queue worker. Worker logs "queue worker started".
Shutdown stops worker cleanly before Raft closes.

### 12. Integration test

End-to-end tests for outbound delivery, bounce, and rate limiting.

**Create `test/integration/outbound_test.go` (build tag `integration`):**

**TestOutboundRelay:**
1. Start mock external SMTP server (go-smtp, accepts all mail, captures to channel).
2. Start dev agent with `config.SMTP.Relay.Enabled = true, Host = mockAddr`.
3. Create sender user.
4. Enqueue an outbound message via `QueueStore.Enqueue()`.
5. Wait up to 30s for mock server to receive message.
6. Verify: message received, From/To correct, DKIM-Signature header present (if key configured).

**TestBounceGeneration:**
1. Start dev agent with relay pointing to a mock that rejects all mail (always 550).
2. Enqueue message.
3. Wait for queue worker to exhaust retries (use low max attempts in test: 2).
4. Verify bounce message appears in sender's INBOX (prefix scan `/user/{id}/mail/`).
5. Verify bounce subject contains "Undelivered".

**TestRateLimiting:**
1. Start dev agent with `FloodThreshold = 3`.
2. Enqueue 3 messages → all accepted.
3. Enqueue 4th → verify rate limit error.
4. (Optional: wait for window reset, verify next enqueue succeeds.)

**TestSPFInbound:**
1. Start dev agent.
2. Send inbound email via `net/smtp`.
3. Read delivered message from Pebble.
4. Parse raw blob, verify `Received-SPF` header present.

**Helper: `mockExternalSMTP(t *testing.T) (addr string, received chan []byte)`:**
- Start go-smtp server on a free port.
- Backend captures raw messages to buffered channel.
- Returns address and channel for assertions.
- `t.Cleanup()` stops the server.

**Done when:** `go test -race -tags=integration ./test/integration/outbound_test.go` passes.

---

## Exit Criteria

- [ ] `go test -race ./...` passes (all unit tests)
- [ ] `go test -race -tags=integration ./test/integration/...` passes
- [ ] Queue persists in Pebble via Raft (survives leader failover)
- [ ] Leader-only queue processing (followers idle)
- [ ] External recipients queued from compose handler (not silently dropped)
- [ ] MX lookup delivers to correct mail exchange (priority order)
- [ ] Smart relay mode works when `smtp.relay.host` is configured
- [ ] DKIM-Signature header present on outbound mail (when key configured)
- [ ] DKIM no-op passthrough when no key configured
- [ ] SPF checked on inbound, result in `Received-SPF` header
- [ ] Rate limiting enforced per sender, surfaces as error in web UI
- [ ] Exponential backoff: retry intervals grow from 2min → capped at 24h
- [ ] Bounce DSN delivered to sender's INBOX after max retries exhausted
- [ ] Bounce message is RFC 3464 compliant (parseable by enmime)
- [ ] `bongo-go queue status` shows pending/bounced counts
- [ ] `bongo-go queue retry {id}` reschedules a failed message
- [ ] `bongo-go queue drop {id}` removes a message from the queue
- [ ] Queue worker starts and stops with agent lifecycle

## What NOT to do in this sprint

- No Sieve/mail filter rules (Sprint 12)
- No POP3 server (Sprint 12)
- No CalDAV/CardDAV (Sprints 9–10)
- No web UI for queue management (CLI only; web UI in a later sprint)
- No SMTP AUTH on relay connection (just STARTTLS; AUTH can be added later)
- No DANE/TLSA/DNSSEC validation
- No ARC (Authenticated Received Chain) signing
- No per-recipient delivery status tracking in web UI
- No configuration hot-reload
- No DMARC policy checking (later sprint, builds on SPF + DKIM)
