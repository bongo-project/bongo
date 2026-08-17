# PRD: Outbound Deliverability and View Original

## 1. Introduction/Overview

Three related defects, discovered while investigating why bounce messages land in Junk. They
form a single failure chain, which is why they are fixed together.

**Problem 1 — we send `EHLO localhost` on every outbound connection.** `Relay.sendToHost` calls
`netsmtp.NewClient(conn, host)` and never calls `client.Hello(...)`. Go's stdlib hardcodes the
HELO name to `"localhost"`, so every message we relay announces itself with a non-qualified
hostname. Receivers enforcing RFC 5321 §4.1.1.1 (Postfix `reject_non_fqdn_helo_hostname`, and
most large providers) permanently reject the mail. This affects all outbound mail from all local
users to a large share of the internet.

**Problem 2 — the resulting bounces are junked by our own spam pipeline.** Those permanent
rejections trigger `BounceGenerator.GenerateAndDeliver`, which builds an RFC 3464 DSN and injects
it into the inbound spool. The injected message has no SPF result and no DKIM signature, while
its `From:` domain is our own, so our DMARC evaluator finds it unaligned against our own
published policy (`example.org` publishes `p=quarantine`) and routes it to Junk. The DSN is also
missing headers RFC 3464/3834 require.

**Problem 3 — users cannot see message headers in the web UI.** The only route to the raw message
is the Export button, which downloads a `.eml` the user must open in a text editor. Worse, we
never stamp authentication or spam-decision headers onto stored messages, so even the exported
file cannot explain why a message was junked — those reasons exist only in server logs.

Net user-visible effect today: **mail is silently lost.** The send fails (problem 1) and the
notification of failure is hidden in Junk (problem 2), with no way for the user to investigate
(problem 3).

**Implementation order matters.** US-006 is the root cause and should ship first; it is small,
independently testable, and every hour it remains unfixed is more rejected mail.

## 2. Goals

- Outbound mail is accepted by receivers that require a fully-qualified HELO.
- A permanent-failure bounce delivers to the sender's INBOX, not Junk, on a domain publishing a
  restrictive DMARC policy.
- Generated DSNs conform to RFC 3464 and RFC 3834.
- The full antispam pipeline continues to run on bounces. No message bypasses spam scoring.
- A user can read any message's full headers and raw source in the browser.
- A user can tell from the raw source why a message was classified as spam.

## 3. User Stories

### US-006: Outbound SMTP uses a fully-qualified HELO name (do first)

**Description:** As a local user sending mail to an external recipient, I want my message
accepted by their server so that it is actually delivered.

**Acceptance Criteria:**
- [ ] Given `SMTP.Hostname` is `mail.example.test`, when the relay opens an outbound connection,
      then the command sent is `EHLO mail.example.test` and the string `localhost` never appears
      in the HELO/EHLO command.
- [ ] The HELO name is sent before the STARTTLS negotiation, so the pre-TLS EHLO is also
      qualified (not just a post-TLS re-issue).
- [ ] Given a test SMTP server that rejects any HELO without a dot in it, when a message is
      relayed through it, then delivery succeeds and `permanentFails` is empty.
- [ ] Given `SMTP.Hostname` is unset and `os.Hostname()` returns a qualified name containing a
      dot, then that name is used as the HELO name.
- [ ] Given `SMTP.Hostname` is unset and `os.Hostname()` returns an unqualified name (no dot),
      then the resolved server domain from `ResolveServerDomain` is used instead, and the
      literal `localhost` is never sent.
- [ ] Given no qualified name can be derived by any means, a warning is logged at startup naming
      the config key to set.

### US-001: Bounces are DKIM-signed so they pass DMARC

**Description:** As a user whose mail failed to send, I want the bounce notification in my inbox
so that I find out my message was not delivered.

**Acceptance Criteria:**
- [ ] Given a configured DKIM signer for domain `example.test`, when `GenerateAndDeliver` builds
      a bounce whose `From:` is `postmaster@example.test`, then the bytes handed to
      `Deliverer.Deliver` contain a `DKIM-Signature:` header with `d=example.test`.
- [ ] Given that bounce, when `antispam.VerifyDKIM` is run over the delivered bytes, then the
      result is `"pass"` and the returned domain is `example.test`.
- [ ] Given a `DMARCEvaluator` whose record lookup for `example.test` returns
      `v=DMARC1; p=quarantine`, when the signed bounce is scored by `BuiltinProcessor.Process`,
      then the returned `StageResult.Action` is `"pass"` and `env.TargetMailbox` is `""`.
- [ ] Given a `BounceGenerator` with a nil DKIM registry, when a bounce is generated, then
      delivery still succeeds and the message is returned unsigned (no panic, no dropped mail).
- [ ] Given a domain with no registered DKIM key, when a bounce is generated for it, then a
      warning is logged naming the domain and the bounce is still delivered.

### US-002: DSNs carry the headers RFC 3464 and RFC 3834 require

**Description:** As a receiving mail system, I want bounces marked as auto-generated and
structurally complete so that I can classify and thread them correctly.

**Acceptance Criteria:**
- [ ] `BuildBounceMessage` output contains the header `Auto-Submitted: auto-replied`.
- [ ] `BuildBounceMessage` output contains exactly three MIME parts: `text/plain`,
      `message/delivery-status`, and a third part of type `text/rfc822-headers` carrying the
      original message's header block.
- [ ] Each of the first two MIME parts declares an explicit `Content-Transfer-Encoding`.
- [ ] Given a `reason` string containing a non-ASCII character, when the bounce is built, then
      the human-readable part encodes it consistently with its declared charset and
      transfer-encoding (round-trips through `enmime.ReadEnvelope` to the original text).
- [ ] Given an original message whose `MessageGuid` is `<abc@example.test>`, the bounce contains
      both `In-Reply-To: <abc@example.test>` and `References: <abc@example.test>`.
- [ ] Given an original message with an empty `MessageGuid`, the bounce contains neither an
      `In-Reply-To` nor a `References` header (no empty-value header is emitted).

### US-003: DSNs use the null envelope sender

**Description:** As a mail administrator, I want DSNs sent with an empty return path so that a
bounce of a bounce cannot loop.

**Acceptance Criteria:**
- [ ] When `GenerateAndDeliver` calls `Deliverer.Deliver`, the `from` argument is the empty string
      `""`, not `MAILER-DAEMON@<domain>`.
- [ ] The bounce's `From:` header remains `MAILER-DAEMON <postmaster@<domain>>`.
- [ ] Given a `QueueEnvelope` with `Sender: ""` and a message carrying a `From:` header, when the
      deliver stage runs, then the stored `MailMeta.SenderEmail` is taken from the `From:` header
      and is non-empty.
- [ ] Given a `ScoringInput` with `EnvelopeSender: ""`, the scorer does not add the
      "envelope sender domain mismatch" reason.

### US-004: Delivered messages carry authentication and spam-decision headers

**Description:** As a user looking at a message that was filed as spam, I want the message itself
to record why so that I can see the reason without server access.

**Acceptance Criteria:**
- [ ] Given an envelope with `SpfResult: "pass"` and a message with a valid DKIM signature from
      `example.test`, when the deliver stage stores the message, then the stored raw message
      begins with an `Authentication-Results:` header naming the server's hostname and containing
      `spf=pass`, `dkim=pass header.d=example.test`, and a `dmarc=` result.
- [ ] Given an antispam `StageResult` with `Score: 0.62` and
      `Reasons: ["SPF fail", "DKIM missing"]`, the stored raw message contains
      `X-Spam-Score: 0.62` and `X-Spam-Reasons: SPF fail, DKIM missing`.
- [ ] Given a message that scored at or above the spam threshold, the stored raw message contains
      `X-Spam-Status: Yes`; below the threshold it contains `X-Spam-Status: No`.
- [ ] Trace headers are prepended above existing headers and do not invalidate a pre-existing
      DKIM signature (`antispam.VerifyDKIM` still returns `"pass"` for a message that verified
      before the headers were added).
- [ ] Any `X-Spam-*` or `Authentication-Results` headers already present on an inbound message are
      removed before ours are added, so a remote sender cannot forge a passing result.

### US-005: View Original in the web UI

**Description:** As a user, I want to read a message's full raw source in the browser so that I
can inspect headers without downloading a file.

**Acceptance Criteria:**
- [ ] A "View Original" control appears in the message toolbar alongside Export.
- [ ] `GET /mail/{mailbox}/{uid}/source` for a message owned by the authenticated user returns
      HTTP 200 and an HTML page containing the message's full raw RFC 5322 text — headers and
      body — inside a `<pre>` element.
- [ ] The raw text is HTML-escaped: a message whose subject contains `<script>alert(1)</script>`
      renders that text visibly and does not execute it.
- [ ] `GET /mail/{mailbox}/{uid}/source` for a UID that does not belong to the authenticated user
      returns HTTP 404 and no message content.
- [ ] `GET /mail/{mailbox}/{uid}/source` without a session cookie redirects to login and returns
      no message content.
- [ ] The view has a Copy button that copies the full raw source to the clipboard.
- [ ] All user-facing strings in the view are wrapped in `t(ctx, ...)` and have entries in every
      locale file.

## 4. Functional Requirements

**Outbound HELO (US-006)**

- FR-19: `Relay.sendToHost` must call `client.Hello(name)` immediately after `netsmtp.NewClient`
  and before the `client.Extension("STARTTLS")` check, so the initial EHLO carries a qualified
  name.
- FR-20: The HELO name must resolve in this order: `SMTP.Hostname`; else `os.Hostname()` when it
  contains a dot; else `ResolveServerDomain(SMTP.DKIM.Domain, SMTP.Hostname)`.
- FR-21: The system must never send `localhost` as a HELO name. When no qualified name can be
  derived, it must log a warning naming the config key to set.
- FR-22: The resolved HELO name must be plumbed to `Relay`. `SMTPRelay` has no hostname field and
  `NewRelay` currently receives only `&a.Config.SMTP.Relay`, so this requires either a new
  parameter or a field on the `Relay` struct. Prefer passing the resolved name, not the whole
  `SMTPConfig`, to keep the relay's dependencies narrow.

**Bounce generation (US-001 to US-003)**

- FR-1: `BounceGenerator` must accept a `*mail.DKIMRegistry` and sign the generated DSN via
  `DKIMRegistry.Sign` before passing it to `Deliverer.Deliver`.
- FR-2: When no signer is registered for the bounce domain, the system must log a warning
  identifying the domain and deliver the bounce unsigned rather than failing.
- FR-3: `GenerateAndDeliver` must use the null envelope sender `""` when calling `Deliver`.
- FR-4: `buildBounce` must emit `Auto-Submitted: auto-replied`.
- FR-5: `buildBounce` must emit a third MIME part of type `text/rfc822-headers` containing the
  original message's header block, retrieved from the blob store via the generator's existing
  `blobs` field. If the original blob is unavailable, the part must be omitted and the omission
  logged; bounce delivery must still succeed.
- FR-6: `buildBounce` must emit an explicit `Content-Transfer-Encoding` on each MIME part.
- FR-7: `buildBounce` must emit `References:` alongside `In-Reply-To:` when the original
  `MessageGuid` is present, and omit both when it is absent.
- FR-8: `buildBounce` must populate `Status:` from the enhanced status code in the remote SMTP
  reply when one is present, falling back to `5.0.0` when it is not.
- FR-9: The system must NOT introduce any mechanism that causes a message to skip antispam
  scoring. Bounces must pass the pipeline on their merits.

**Trace headers (US-004)**

- FR-10: The deliver stage must prepend an `Authentication-Results:` header recording the SPF
  result from the envelope, the DKIM result and signing domain, and the DMARC result.
- FR-11: The deliver stage must prepend `X-Spam-Score`, `X-Spam-Status`, and `X-Spam-Reasons`
  headers derived from the antispam `StageResult`.
- FR-12: The deliver stage must strip any inbound `Authentication-Results` and `X-Spam-*` headers
  before adding its own.
- FR-13: Added headers must be prepended above existing headers so that DKIM signatures over the
  original header set remain valid.

> **AMENDED 2026-08-17 after adversarial verification — FR-12 takes precedence over FR-13.**
> As originally written these two requirements are in direct tension and cannot both hold. A
> sender who *oversigns* — listing `x-spam-status` (or `authentication-results`) in their DKIM
> `h=` tag — binds a header FR-12 requires us to strip. Stripping it means the stored copy no
> longer verifies; not stripping it would let a remote sender pin our own trace headers by
> covering them with their signature, defeating the anti-forgery purpose of FR-12 entirely.
>
> **Resolution: strip unconditionally.** FR-13 holds for the ordinary case — stamping only
> prepends and never alters signed headers — and is proven for that case by a test added in
> task 11.2. For oversigned senders the stored copy will not re-verify; the arrival-time verdict
> is already durable on `MailMeta.DkimResult`/`DkimDomain`, and relayed and forwarded copies use
> the untouched original blob, so nothing that depends on stored-message DKIM verification is
> affected today. The consequence to accept knowingly: IMAP, POP3 and Export all serve the
> stamped copy, so a mail client re-verifying an oversigned message will report a DKIM failure
> for mail that authenticated correctly on arrival.

**View Original (US-005)**

- FR-14: The system must expose `GET /mail/{mailbox}/{uid}/source`, authenticated as the existing
  mail routes are, resolving the message through the same ownership check `ExportEML` uses.
- FR-15: The route must render the full raw message inside a `<pre>`, HTML-escaped.
- FR-16: The message toolbar must link to the route.
- FR-17: The view must provide a Copy-to-clipboard control.
- FR-18: All styling must use classes in `bongo.css`. No inline `style=""` attributes — the
  Content-Security-Policy is `style-src 'self'` and inline styles are blocked by the browser.

## 5. Non-Goals (Out of Scope)

- No "local origin" or trusted-sender flag that bypasses antispam. Explicitly rejected: inbound
  internet mail enters through the same `EnqueueLocal` / `DeliverWithSPF` methods the bounce
  generator uses, so such a flag would be one careless caller away from a full spam bypass.
- No per-recipient-domain or per-sending-IP HELO selection. One HELO name for the server.
- No changes to DMARC enforcement configuration or default policy.
- No outbound DMARC aggregate (`rua`) report generation.
- No parsed/prettified headers table. Raw source only.
- No admin-facing view of other users' message sources.
- No changes to IMAP, POP3, or the Export route.
- No localisation of bounce message body text (bounces stay English).

## 6. Technical Considerations

Key integration points, verified against the current tree:

**Outbound HELO**

- `internal/smtp/relay.go:190` — `netsmtp.NewClient(conn, host)`. The `host` argument is the
  *server* name used for TLS `ServerName` and AUTH; it is not the HELO name. This is the exact
  point of the bug.
- `$GOROOT/src/net/smtp/smtp.go:71` — stdlib sets `localName: "localhost"` at construction.
  `Hello()` at `smtp.go:98` is the only way to change it. Confirmed on go1.26.2.
- The implicit handshake fires inside `client.Extension("STARTTLS")` at `relay.go:213`, so
  `Hello()` must be called before that line or the bad EHLO is already on the wire.
- `internal/config/config.go:128` — `SMTP.Hostname`, already documented in
  `deploy/bongo.prod.hcl:57` as "EHLO/HELO banner (align with rDNS/SPF)". The inbound server
  already consumes it correctly at `internal/smtp/server.go:546-551`; this work makes the
  outbound path consistent with it.
- `internal/agent/agent.go:1176` — `NewRelay` call site, where the resolved name gets plumbed in.
- Rejections surface at RCPT, not EHLO: Postfix's `reject_non_fqdn_helo_hostname` accepts the
  EHLO and defers rejection to RCPT TO, which is why this appears as a per-recipient permanent
  failure at `relay.go:245-248` rather than a connection error.

**Bounce generation**

- `internal/mail/bounce.go:69` — `GenerateAndDeliver`, where signing and the null sender go. The
  struct already holds a `blobs *store.BlobStore` field (currently unused) that FR-5 needs.
- `internal/mail/bounce.go:85` — `buildBounce`, all header work. Keep the existing
  `SanitizeHeaderValue` calls on every user-controlled value.
- `internal/mail/dkim_registry.go:66` — `DKIMRegistry.Sign(rawMsg, logger)`, keyed by `From`
  header domain. Nil registry is a documented passthrough. Already used on the outbound path at
  `internal/queue/worker.go:198`; reuse it rather than writing new signing code.
- **Why signing achieves alignment:** the bounce `From:` is `postmaster@<bounceDomain>`, and
  `ResolveServerDomain` (`internal/mail/bounce.go:52`) prefers `SMTP.DKIM.Domain` verbatim. So
  `d=` equals the `From` domain exactly and DMARC alignment passes.
- `internal/mail/dkim.go:116` — signed `HeaderKeys` are `from, to, subject, date, message-id,
  content-type`. `Received:` and the FR-10/FR-11 trace headers are not covered, so prepending them
  after signing is safe. This is what makes FR-13 work.
- `internal/agent/agent.go:1189` — `NewBounceGenerator` call site; `a.DKIMRegistry` is already in
  scope here (passed to the queue worker on the next line).
- `internal/pipeline/deliver/deliver.go:152` — `headerFrom` falls back to `env.Sender` only when
  the `From:` header is absent. Our DSN always sets `From:`, so the null envelope sender of FR-3
  does not blank out sender metadata. This is the main regression risk of US-003 and is where
  tests should focus.
- `internal/pipeline/antispam/builtin.go:250` — `applyDMARC`, the code currently junking bounces.
  No change needed; it should simply start passing.

**View Original**

- `internal/web/handlers/mail.go:1043` — `ExportEML`, the model for the new source handler:
  `resolveMessage` for ownership, `loadMailMeta`, then `store.RawMessageBytes`.
- `internal/agent/agent.go:754` — route registration, next to the existing `/export` route.
- `internal/web/templates/message.templ:121` — toolbar, where the new control goes.

**Storage consequence of FR-10..FR-13 (added 2026-08-17 after adversarial verification)**

Stamping trace headers changes a message's bytes, so the stored copy must be written as a NEW
blob and the one written at SMTP DATA time is left unreferenced. This was not called out when the
PRD was written and is a genuine cross-phase interaction with Phase 9:

- Before this change, an orphaned DATA-time blob occurred only for messages large enough to
  trigger attachment splitting. It now occurs for **100% of inbound mail**.
- The orphan is reclaimed by the unreferenced-blob GC — but `BlobGCMode()` defaults to `off`
  (`internal/config/config.go:84`), because a data-deleting sweep must not start on an existing
  deployment without the operator choosing it.
- So on a default-configured deployment, inbound mail consumes roughly **twice** the space it
  should, indefinitely.

Resolved in task 11.3 by documenting it rather than changing the compiled default: `blob_gc` is
now set in both shipped example configs, and the operator troubleshooting guide gains a
"storage growing faster than expected" entry. The compiled default stays `off` deliberately —
flipping it would start deleting data on upgrade for operators who never opted in.

**Constraints**

- Follow the repo guardrail on multi-step Raft mutations: nothing here should claim atomicity it
  does not have.
- Never log the raw message or DKIM key material.
- Table-driven tests, `t.TempDir()` for storage, `-race` on all runs.

## 7. Success Metrics

- Mail from a local user to an external recipient on a Postfix host enforcing
  `reject_non_fqdn_helo_hostname` is accepted rather than permanently rejected.
- The string `EHLO localhost` no longer appears on any outbound connection.
- A permanent failure on `example.org` produces a bounce that arrives in INBOX with `dmarc=pass`
  visible in its `Authentication-Results` header.
- `go test -race ./...` passes; `golangci-lint run` and `gosec ./...` clean of new HIGH/MEDIUM
  findings.
- A generated DSN parses as a conformant three-part `multipart/report` under `enmime`.
- A user can open View Original on a junked message and read the score and reasons that caused it,
  with no server access.

## 8. Open Questions

1. **HELO name in multi-domain deployments.** One server may host several domains, but HELO is
   per-connection, not per-message. Confirm `SMTP.Hostname` is the right single value and that it
   matches the sending IP's rDNS on the live host.
2. **Queued mail already marked failed.** Messages permanently failed by the HELO bug are already
   out of the queue. Is a manual resend acceptable, or is a requeue path needed?
3. ~~**DMARC-publishing domain with no DKIM key.**~~ **RESOLVED (2026-08-16) — no code needed.**
   The concern was that FR-2 delivers the bounce unsigned, so it still gets junked, silently.
   But this is already surfaced: `buildDNSChecks` (`internal/web/handlers/admin.go:839`, built in
   task 7.2) compares ACTUAL published DNS against EXPECTED-for-this-server and marks DKIM ok only
   when the published `p=` matches ours exactly, neutral when no key has been generated. It is
   shown per-domain on `/admin/domains/{domain}` alongside MX/SPF/DMARC. A startup warning would
   duplicate a weaker version of that check (local key exists) rather than the stronger one
   (published key matches). Note the operator-visible symptom is indirect — bounces landing in
   Junk does not obviously point at a DKIM DNS record — so a line in the operator docs tying the
   two together is the only remaining gap. Tracked as task 10.7 (docs only, no code).
4. **Existing stored messages.** Trace headers apply only to newly delivered mail. Confirmed as
   acceptable — no backfill — but worth stating before implementation.
5. **`Authentication-Results` hostname.** Should the `authserv-id` be `SMTP.Hostname` or the
   resolved `ResolveServerDomain` value? They differ in multi-domain deployments.
6. **Copy button and clipboard API.** `navigator.clipboard` requires a secure context. Is a
   plaintext-HTTP dev-mode fallback needed, or is degrading silently on `http://` acceptable?
