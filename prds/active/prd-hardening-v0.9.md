# PRD: Hardening releases v0.9.0 to v0.11.0

## 1. Introduction / Overview

Phase 18 (the security review of 2026-10-20) closed with v0.8.2. Every batch
in it ended with a review, and every review added new rows to
`prds/TASKS.md`. The result is a backlog that grows faster than it shrinks and
has no clear end. This PRD sets a fixed scope for three releases, one per
theme, and stops that pattern.

The scope is every open follow-up and backlog item that was still open on
2026-10-23, apart from the research PRDs and the features listed under
Non-Goals. Each candidate was checked against the code at `origin/main`
(`0a76063`) before it was included. Items that turned out to be fixed already
are listed in section 6.1 with the commit that fixed them, and are not in
scope.

**The scope rule for this release.** The in-scope list is section 3. Anything
a review finds during this release goes into section 8.2 ("Candidates for the
next release"), and does **not** become a new task row, unless the change
under review introduced the problem itself. A gap opened by the change is
fixed before that change merges (this is the rule recorded in
`prds/progress.md` on 2026-10-23: "Defer a review finding to a follow-up only
when it is pre-existing").

The stories are grouped into four phases in priority order. Decided
2026-10-23: each release ships when its phases are done and is tagged on its
own, so every release has a scope written before work starts.

| Release | Phases | Content |
|---------|--------|---------|
| v0.9.0 | A, D | Abuse and authentication gaps; CI, supply chain and test reliability. |
| v0.10.0 | C | Data correctness. |
| v0.11.0 | B | Agent role and cluster transport security (deployment-affecting). |

| Phase | Theme | Stories | Release note |
|-------|-------|---------|--------------|
| A | Abuse and authentication gaps | US-001 to US-013 | Closes address and account oracles, binds every protocol login to the credentials it checked, and makes quota and rate limits count what is actually written or sent. |
| B | Agent role and cluster transport security | US-014 to US-021 | Agent-role nodes and cluster peers now authenticate each other with mutual TLS and fail closed where they used to trust the network. |
| C | Data correctness | US-022 to US-031 | Mail written in the web composer obeys the recipient's rules, calendar data survives edits and imports intact, and stale routing and sharing entries are reported or removed. |
| D | CI, supply chain and test reliability | US-032 to US-039 | Container images and actions are pinned and kept current, the deploy hook is tested in CI, and the known flaky tests are fixed at the cause. |

A few terms used throughout:

- **Oracle**: a response that differs depending on a secret fact (for example
  "this address belongs to a live account"), letting an attacker learn that
  fact by probing.
- **Epoch**: the value derived from a user's password hash, 2FA state and
  `SessionGeneration` (`security.SessionEpoch`). A session remembers the epoch
  it started with; when the live epoch changes (password reset, 2FA change),
  the session ends.
- **SRS** (Sender Rewriting Scheme): when we forward mail, we replace the
  envelope sender with an address on our own domain (`SRS0=...@ourdomain`) so
  SPF passes at the next hop. A bounce sent to that address is decoded and
  relayed to the original sender. `SRS1` is the form used when the sender was
  already an SRS address.
- **DSN** (Delivery Status Notification): a bounce message.
- **Backscatter**: bounces sent to an address that never sent the original
  mail, because the sender address was forged.
- **mTLS** (mutual TLS): both ends of a connection present certificates signed
  by a shared certificate authority (CA), and each verifies the other.
- **LOCAL / FORWARD origin**: `OutboundMessage.origin`. LOCAL mail is
  DKIM-signed by the queue worker as our own; FORWARD mail is relayed byte for
  byte and never re-signed.

## 2. Goals

- Close every abuse and authentication gap that was open on 2026-10-23
  (Phase A) before any other work in this release.
- Make the agent role (remote stateless protocol processors over gRPC) safe to
  deploy: no plaintext or unauthenticated channel between agents and servers,
  and no agent-side path that trusts more than the server would.
- Fix the known data-loss and data-drift bugs in calendar handling, web
  compose delivery and routing records.
- Make CI trustworthy: pinned images, automatic update PRs, the deploy hook
  under test, and no known flaky test left unexplained.
- Finish with `prds/TASKS.md` rows 18.39 to 18.42 and backlog rows B7, B20 to
  B27 closed, and with no new task rows created during the release except
  for gaps a change opened itself.

## 3. User Stories

Every story is written test first: the first commit adds a test that fails
against `origin/main` for the reason stated, then the fix turns it green. For
each negative assertion (something is refused), break the fix once and confirm
the test goes red.

Line references are to `origin/main` at `0a76063`.

---

### Phase A: Abuse and authentication gaps

Release note: closes address and account oracles, binds every protocol login
to the credentials it checked, and makes quota and rate limits count what is
actually written or sent.

### US-001: Rate-limit calendar sharing
**Source:** 18.41(e)

**Description:** As an operator, I want the calendar share endpoint to be rate
limited so that a logged-in user cannot use it to test which addresses are
live accounts. Today `POST /calendar/{calendarID}/share`
(`internal/agent/agent.go:1332`, handler `internal/web/handlers/calendar.go:289`)
answers 200 for a live local account and 400 for anything else, with no limit
on attempts.

**Acceptance Criteria:**
- [ ] A user may make 20 share requests per rolling hour. The 21st request in
      that hour returns 429 with a `Retry-After` header, and the calendar's
      `SharedWith` is unchanged, even when `share_with` is a valid live account.
- [ ] Every request counts, whatever its outcome: 200, 400 (malformed,
      unknown, self, cap reached) and repeat shares of an existing entry.
- [ ] The limit is keyed by the authenticated user ID, not the client IP: user
      B's first request returns its normal status while user A is limited.
- [ ] After the window passes (test with an injectable clock), user A's next
      request is processed normally.
- [ ] The limiter is the shared per-user limiter type already used elsewhere
      (`mail.RateLimiter` or `authlimit`), not a new implementation.

### US-002: Create send-as address records only after verification
**Source:** 18.39(a)

**Description:** As a user, I want adding a send-as address to reveal nothing
about whether another account already holds it. Today
`AddSendAsAddress` (`internal/web/handlers/address_verify.go:146`) answers
every outcome with the same redirect, but writes a `/system/address/` record
(`address_verify.go:208-219`) only when nobody holds the address, so the
settings list shows the address for a free address and omits it for a taken
one. That difference is the residual oracle.

**Acceptance Criteria:**
- [ ] `POST /settings/addresses` with `address=claim@ext.example` by user A
      writes no `/system/address/claim@ext.example` key. It writes a per-user
      pending claim under `/user/A/addrverify/claim@ext.example` (the existing
      verification key space).
- [ ] `GET /settings/addresses` for A then lists `claim@ext.example` as
      pending verification.
- [ ] The same POST by user B, when A has a verified record for that address,
      returns the same status and `Location` as A's request, creates a pending
      claim for B, and B's list shows it as pending. Nothing in B's responses
      differs from the case where nobody holds the address.
- [ ] `RequestVerification` for B's claim on an address another account has
      verified returns the same redirect as a successful request and queues no
      verification mail.
- [ ] A correct code from A writes `/system/address/claim@ext.example` with
      `UserId=A`, `Verified=true`, `CanSend=true` and deletes A's pending claim.
- [ ] A correct code from B after A's verification succeeded leaves A's record
      unchanged and returns the generic "verification failed" redirect.
- [ ] An address in a domain this server hosts is still refused with the same
      redirect and no claim (hosting is public through DNS, so this is not an
      oracle).

### US-003: Account creation replaces another user's unverified claim
**Source:** B26(b)

**Description:** As an operator, I want an unverified send-as claim to never
block a real account. Today `checkAddressFreeFor`
(`internal/auth/create.go:78-93`) treats any `/system/address/` record as
taken, so a user who claims `new@future.example` before that domain is hosted
here blocks the account `new@future.example` forever. US-002 stops new records
of this kind; this story handles the ones that already exist.

**Acceptance Criteria:**
- [ ] Given `/system/address/new@future.example` with `Kind=identity`,
      `Verified=false`, `UserId=A`, `auth.CreateUser` for `new@future.example`
      succeeds and the record afterwards has `Kind=primary` and the new
      account's user ID.
- [ ] The replacement is logged at Warn with the address and the previous
      owner's user ID.
- [ ] A's settings page no longer lists the address.
- [ ] If A's record has `Verified=true`, `CreateUser` still returns an error
      wrapping `auth.ErrAccountExists` and nothing is written.
- [ ] The admin create-user form shows the same outcomes (303 on success, the
      existing 409 page when refused).

### US-004: Revoke verified send-as identities on another account's address
**Source:** B26(c)

**Description:** As an operator, I want a verified send-as identity that
collides with another account's local address to be revoked, not just listed.
Today `reportShadowedIdentities` (`internal/agent/agent.go:2970`) only logs
these at startup.

**Acceptance Criteria:**
- [ ] On leader startup, every record that `store.FindShadowedIdentities`
      reports with a non-empty `ConflictOwner` (another account, alias or
      extra address holds the address) is deleted through Raft.
- [ ] Each deletion is logged at Warn with `address`, `user_id` and
      `conflict_owner`.
- [ ] After the pass, `CanSendAs(A, address)` returns false, and mail to the
      address resolves to the conflicting owner.
- [ ] Records reported without a conflict (unverified, or verified but
      shadowed only by resolver order) are left untouched and still reported.
- [ ] A second startup deletes nothing and logs no revocations.
- [ ] Followers do not run the pass.

### US-005: Deleting an alias cannot remove an admin-added address
**Source:** B26(a)

**Description:** As an operator, I want deleting an alias to affect aliases
only. Admin-added extra addresses are stored with `Kind="alias"`
(`internal/web/handlers/admin.go:2349`), and `DeleteAlias`
(`admin.go:2015-2023`) deletes any `alias`-kind record, so deleting an
"alias" with the address of a user's extra address removes that user's mail
routing.

**Acceptance Criteria:**
- [ ] Given an extra address `x@hosted.example` added through
      `POST /admin/users/{id}/addresses` (no `/system/alias/` key),
      `POST /admin/aliases/x@hosted.example/delete` returns 404 and leaves the
      address record and the user's routing unchanged.
- [ ] Given an alias created through the alias form (it has a
      `/system/alias/` key), the delete still removes the alias key, the
      `alias`-kind record and the SQLite row, and returns 303 as today.
- [ ] A legacy mixed-case alias key is still deletable (existing
      `admin_alias_case_test.go` cases stay green).

### US-006: IMAP, POP3 and SMTP take the session epoch from the profile they verified
**Source:** 2026-10-23 review finding (progress.md, 18.43 entry)

**Description:** As a user whose password was just reset, I want an old
password that was being checked at the same moment to not open a long-lived
session. IMAP (`internal/imap/backend.go:288-306`), POP3
(`internal/pop3/session.go:319`) and SMTP (`internal/smtp/server.go:335`)
check the password with `Authenticate` and then read the epoch with a second
`AccountState` call. A reset between the two gives a session authenticated
with the old password but holding the new epoch, so it survives the reset.
The web login had the same bug and was fixed in `b9273ec` by using
`LocalAuth.AuthenticateProfile` (`internal/auth/local.go:59`).

**Acceptance Criteria:**
- [ ] For each of IMAP LOGIN, POP3 USER/PASS and SMTP AUTH PLAIN: with a test
      authenticator whose `AuthenticateProfile` returns profile P1 and an
      account reader that returns P2 (the post-reset profile), the session's
      epoch equals `SessionEpoch(P1)`.
- [ ] In that state, the next command that re-checks the account (IMAP
      SELECT, POP3 STAT, SMTP MAIL FROM) is refused (IMAP `NO`, POP3 `-ERR`,
      SMTP 535 5.7.8, as each protocol does today for a changed epoch).
- [ ] Login makes no separate `AccountState` call (assert with a counting
      fake).
- [ ] A disabled or missing account is still refused at login.

### US-007: Change password checks the current password against the locked profile
**Source:** 2026-10-23 review finding (progress.md, 18.43 entry)

**Description:** As a user, I want "change password" to check my current
password against the same profile it then updates. `reauthenticate`
(`internal/web/handlers/security.go:324` then `:330`) calls `Authenticate`,
which reads one profile, then loads the profile again. `lockUser` is a
per-process lock, so across nodes an admin reset can land between the two
reads.

**Acceptance Criteria:**
- [ ] The current password is compared with bcrypt against
      `profile.PasswordHash` of the profile loaded inside `lockUser`.
      `Authenticate` is not called on this path (a fake authenticator that
      accepts every password is never consulted).
- [ ] Given a loaded profile whose hash is for `new-pass`, a POST with
      `current_password=old-pass` redirects to
      `/settings/security?flash=Invalid+password`, writes nothing, and records
      one limiter failure.
- [ ] The same applies to the other caller of `reauthenticate`,
      `DisableTOTP` (`security.go:206`).
- [ ] Existing ChangePassword tests, including the `SessionGeneration` bump,
      stay green.

### US-008: Session checks fail closed for admin rights when the profile cannot be read
**Source:** 18.41(f)

**Description:** As an operator, I want a store error during the per-request
session check to never grant admin rights from a stale cookie. Today
`sessionStateFromProfile` (`internal/agent/agent.go:279-290`) returns a zero
state on any read error, and `RequireAuth`
(`internal/web/middleware/auth.go:178-197`) then trusts the cookie's
`IsAdmin` snapshot. This keeps users reading mail during a store hiccup, which
is the intended trade-off, but also keeps a demoted or deleted admin's rights.

**Acceptance Criteria:**
- [ ] When `stateFn` returns `Found=false, Missing=false`, `RequireAuth` puts
      `isAdmin=false` into the request context regardless of the cookie.
- [ ] In that state, a request to any route behind `RequireAdmin` with a
      cookie carrying `IsAdmin=true` returns 503 with the text "account state
      unavailable" and the handler is not called.
- [ ] In that state, a GET to a non-admin route (for example `/mail`) still
      reaches the handler (200).
- [ ] When `Found=true`, behaviour is unchanged (live `IsAdmin` wins).
- [ ] The doc comments on `RequireAuth` and `userSessionState` describe the
      new rule.

### US-009: LOCAL forwards count against the author's recipient budget
**Source:** 18.42(b)

**Description:** As an operator, I want a forward that we sign as the author
(LOCAL origin, `internal/pipeline/deliver/deliver.go:976-996`) to use up the
author's outbound recipient budget, as if the author had sent it. Today only
the forwarding user's forward limiter is charged
(`deliver.go:756`), so an author can reach more external recipients under
their own DKIM signature than `RecipientLimiter` allows, by sending to local
users who forward.

**Acceptance Criteria:**
- [ ] The deliver stage receives the shared `RecipientLimiter` (the one
      compose and SMTP submission use, `internal/agent/agent.go:158`).
- [ ] Given author A with 3 recipients left in the budget, and one submission
      from A to local users B and C who each forward to 2 verified external
      targets: 3 forward targets are enqueued with origin LOCAL, and the
      remaining target is enqueued with origin FORWARD (unsigned and
      SRS-rewritten, exactly as a forward of external mail is today).
- [ ] Immediately afterwards a `POST /compose/send` from A to one external
      recipient returns 429.
- [ ] A forward of mail with no `AuthUserId` charges no author budget.

### US-010: Forwarding a forged sender produces no backscatter
**Source:** B27(b)

**Description:** As the owner of an address that someone forged as a sender,
I want our server to never send me bounces for mail I did not send. Today, if
a forward to a verified target fails, the queue worker sends a DSN to the
forward's sender (`internal/queue/worker.go:351-362`); with SRS that DSN, or
any remote bounce, comes back to our SRS address and `relaySRSBounce` passes it
on to the original, possibly forged, sender.

**Acceptance Criteria:**
- [ ] A message is "authenticated" for this purpose when the envelope's
      `spf_result` is `pass` or its DMARC result is `pass`.
- [ ] Given an inbound message from `mallory@forged.example` with
      `spf_result=fail` and DMARC `fail`, delivered to B who forwards to a
      verified external target, and the forward fails permanently in the
      queue worker: no DSN is generated (bounce generator fake records zero
      calls) and a log line "DSN suppressed for unauthenticated forwarded
      sender" names the forward ID and the forwarding user.
- [ ] A remote bounce addressed to the envelope sender of that same forward
      and arriving on port 25 is not relayed: the outbound queue receives
      nothing addressed to `mallory@forged.example`.
- [ ] The same two cases with `spf_result=pass` behave as today: one DSN, and
      one relayed bounce to the original sender.
- [ ] LOCAL-origin forwards are unaffected.

### US-011: Cap the request body on CalDAV and CardDAV
**Source:** 2026-10-23 review finding (progress.md, 18.43 entry)

**Description:** As an operator, I want an authenticated DAV client to be
unable to make the server buffer an arbitrarily large body. The DAV handlers
(`internal/agent/agent.go:3852-3890`) pass the request straight to
go-webdav, which parses the whole body before our quota check runs
(`internal/caldav/server.go:323`).

**Acceptance Criteria:**
- [ ] A constant `davMaxBodyBytes = 10 << 20` (10 MiB) is applied with
      `http.MaxBytesReader` after authentication succeeds, in both DAV
      handlers.
- [ ] An authenticated `PUT` to a calendar object with a body of
      `davMaxBodyBytes + 1` bytes returns 413, stores nothing, and leaves the
      user's usage unchanged. The same with `Transfer-Encoding: chunked` and
      no `Content-Length`.
- [ ] The same for a CardDAV `PUT`.
- [ ] A 100 KiB `PUT` succeeds as today (201 for create and for update: go-webdav
      v0.7.0 answers every PUT with 201, see its TODO; amended 2026-10-23).
- [ ] An unauthenticated oversized `PUT` still returns 401 without reading
      the body.

### US-012: IMAP APPEND and SMTP RCPT weigh the message size against quota
**Source:** 2026-10-23 review finding (progress.md, 18.43 entry)

**Description:** As an operator, I want the domain quota to refuse a message
that would not fit, not only when the domain is already full. IMAP APPEND
(`internal/imap/backend.go:618-630`) and SMTP RCPT
(`internal/smtp/server.go:509-517`) call `RecipientOverQuota`, which is
`RecipientWouldExceedQuota(userID, 0)` (`internal/store/domain_quota_store.go:185`).

**Acceptance Criteria:**
- [ ] With a domain quota of 10,000,000 bytes and recorded usage of
      9,950,000: IMAP `APPEND INBOX {100000}` returns `NO [OVERQUOTA]` and no
      blob is written; `APPEND INBOX {10000}` succeeds.
- [ ] Same quota and usage: `MAIL FROM:<s@remote.example> SIZE=100000` then
      `RCPT TO:<user@hosted.example>` returns 552 5.2.2; with `SIZE=10000` the
      RCPT returns 250.
- [ ] `MAIL FROM` without a `SIZE` parameter keeps today's behaviour (refuse
      only when already at or over quota). This remaining gap is documented
      in the code comment.
- [ ] A quota checker error still fails open (logged), as today.

### US-013: Web compose weighs the message size against quota
**Source:** 18.41(b)

**Description:** As an operator, I want compose to refuse a Sent copy or a
local delivery that would not fit. Today compose checks quota with
`addBytes=0` for the sender (`internal/web/handlers/compose.go:320`), for each
local recipient (`compose.go:530`) and for drafts (`compose.go:655`), so one
byte of headroom admits a message up to the compose size cap.

**Acceptance Criteria:**
- [ ] The size used is the quantity the usage recompute counts for a stored
      message (see the `[quota]` pattern in `prds/progress.md`: size a
      pre-check with exactly what the recompute counts), computed once per
      send.
- [ ] With quota 10,000,000 and sender usage 9,990,000, sending a 50 KB
      message returns 507, nothing is queued and no Sent copy is written; a
      5 KB message is sent.
- [ ] With the recipient's domain at 9,990,000 of 10,000,000, a 50 KB message
      to that local recipient returns 507 with the translated recipient-full
      text and nothing is delivered or queued for any recipient.
- [ ] Saving a 50 KB draft at 9,990,000 of 10,000,000 returns 507.

---

### Phase B: Agent role and cluster transport security

Release note: agent-role nodes and cluster peers now authenticate each other
with mutual TLS and fail closed where they used to trust the network.

### US-014: Sign internal requests with the effective session secret
**Source:** 18.42(f)

**Description:** As an operator, I want internal control-plane requests to be
signed with the same secret the receiving side verifies with. `autoJoinRaft`
signs with `a.Config.Web.SessionSecret` (`internal/agent/agent.go:3198`),
which is empty in dev mode, where an auto-generated secret is used everywhere
else (`agent.go:836-843`).

**Acceptance Criteria:**
- [ ] The effective secret chosen at `agent.go:836-843` is stored on the
      agent and used by every caller of `security.SignInternalRequest` and
      `BlobSync.SetSharedSecret`.
- [ ] In a test with an empty configured secret, a join request built by the
      auto-join code verifies against `RequireInternalAuth` constructed with
      the effective secret (handler reached), and does not verify against an
      empty secret (401).
- [ ] No code path signs with `a.Config.Web.SessionSecret` directly (grep
      check in the test or review).

### US-015: Agent-role SMTP refuses invalid SRS recipients at RCPT
**Source:** 18.42(d)

**Description:** As an operator, I want an agent-role node to refuse an
invalid SRS address at RCPT with 550, as a server-role node does, instead of
accepting every recipient (`internal/smtp/server.go:486-491`) and letting the
server drop it later. The agent backend has no SRS verifier
(`internal/agent/agent.go:752-770`).

**Acceptance Criteria:**
- [ ] The agent-role backend gets an SRS verifier derived the same way as the
      server role (`agent.go:598`), from the agent's configured session
      secret.
- [ ] On an agent node: `RCPT TO:<SRS0=xxxx=TT=example.org=bob@hosted.example>`
      with a wrong hash returns 550 5.1.1 with the same text the server role
      uses; a valid SRS address with a null sender returns 250; a valid SRS
      address with a non-null sender returns 550.
- [ ] Because the agent has no store, the hosted-domain test is replaced by
      the HMAC check: any recipient whose local part is SRS-shaped must pass
      `Reverse`, whatever its domain.
- [ ] With no session secret configured on the agent, every SRS-shaped
      recipient is refused with 550 (fail closed) and the agent logs this once
      at startup.
- [ ] Ordinary recipients are still accepted at RCPT on the agent, as today.

### US-016: Agent-role antispam does not bypass DMARC for submitted mail
**Source:** B27(d)

**Description:** As an operator, I want an agent-role antispam processor to
never skip DMARC on the strength of envelope flags it cannot verify.
`buildAgentAntispamProcessor` (`internal/agent/antispam.go:165-189`) has no
send-as authorizer, so an envelope with `Trusted=true` and `AuthUserId` set
keeps the unconditional DMARC bypass. `DispatchOrLocal` has no production
caller yet, so this must be closed before it gets one.

**Acceptance Criteria:**
- [ ] An envelope with `Trusted=true`, `AuthUserId="u1"` and a From domain
      whose DMARC evaluation fails is processed by the agent processor with
      DMARC run and the result recorded as `fail` (no bypass).
- [ ] The same envelope through the server-role processor, with an
      authorizer that grants `u1` the From address, still bypasses DMARC (no
      regression).
- [ ] The doc comment's "Known gap" paragraph is removed.

### US-017: Cluster listeners get their own bind address
**Source:** B7 (L6 in `docs/security-review-2026-07-06.md`)

**Description:** As an operator, I want Raft, gossip and the agent gRPC
listeners to bind to a private address, not to the public `server.bind_addr`
(`internal/config/config.go:43-48`), once a node advertises a routable
address.

**Acceptance Criteria:**
- [ ] New key `cluster.bind_addr`. When unset, cluster listeners bind to the
      advertise address (loopback for a single node, as today).
- [ ] With `server.bind_addr="0.0.0.0"`, `advertise_addr="10.0.0.5"` and no
      `cluster.bind_addr`, Raft and gossip listen on `10.0.0.5`, not
      `0.0.0.0`.
- [ ] With `cluster.bind_addr="10.0.0.6"`, they listen on `10.0.0.6`.
- [ ] Protocol listeners (SMTP, IMAP, POP3, web) are unaffected.
- [ ] `config.Diff` classifies the key as restart-required.
- [ ] `deploy/bongo.example.hcl` documents the key.

### US-018: Agent gRPC servers require mutual TLS
**Source:** B20, B21 (`agent_server.tls.*` keys)

**Description:** As an operator, I want the agent gRPC servers to accept only
clients holding a certificate from the cluster CA. Today
`AgentServer.Start` (`internal/agent/grpc/server.go:220-225`) and
`AgentProcessorServer.Start` (`internal/agent/grpc/agent_processor.go:47-53`)
call `grpc.NewServer()` with no credentials on every interface, so anyone who
can reach the port can register as an agent, deposit mail and fetch blobs.

**Acceptance Criteria:**
- [ ] `agent_server.tls.{cert_file,key_file,ca_file}` are read and no longer
      classified as no-effect by `config.Diff`.
- [ ] Both `Start` methods return an error naming the missing key, and do not
      listen (the port stays free), when any of the three files is not
      configured.
- [ ] `Config.Validate` refuses role `agent` without the three keys.
- [ ] With TLS configured: a client presenting a certificate signed by the
      CA completes a `Register` call; a client with no certificate, a client
      with a certificate from another CA, and a plaintext client all fail
      before any handler runs.
- [ ] The server uses `tls.RequireAndVerifyClientCert` and a minimum of
      TLS 1.2.

### US-019: Agent gRPC clients dial with mutual TLS and bind privately
**Source:** B20

**Description:** As an operator, I want every gRPC client in the agent path to
verify the server and present its own certificate. Clients dial with
`insecure.NewCredentials()` (`internal/agent/grpc/client.go:84`,
`internal/agent/grpc/server.go:395`).

**Acceptance Criteria:**
- [ ] Both dial sites use transport credentials built from the same three
      files; no call to `insecure.NewCredentials` remains outside tests.
- [ ] A client dialling a server whose certificate is from another CA fails
      with a TLS verification error.
- [ ] The gRPC listeners bind to the cluster bind address from US-017 plus
      the configured port, not `:port`.
- [ ] `docs/multi-node-cluster-checklist.md` step 4 is updated to describe
      the shipped behaviour.

### US-020: Raft transport has no plaintext fallback and verifies peers
**Source:** B7 (H4 in `docs/security-review-2026-07-06.md`)

**Description:** As an operator, I want a clustered Raft transport to refuse
to start without TLS and to verify every peer against the cluster CA. Today
it falls back to plaintext when the certificate is missing
(`internal/cluster/raft.go:101-126`) and the dialer sets
`InsecureSkipVerify` when no CA pool is loaded (`raft.go:369`).

**Acceptance Criteria:**
- [ ] A server with peers (non-empty `cluster.join`, or a routable advertise
      address) and missing or unreadable `cluster.tls` files fails to start
      with an error naming the file. It never opens a plaintext Raft
      listener.
- [ ] `InsecureSkipVerify` is never set on the Raft dialer; with TLS enabled
      and no CA pool the node fails to start.
- [ ] The Raft listener uses `tls.RequireAndVerifyClientCert` with the
      cluster CA.
- [ ] A peer presenting a certificate from another CA cannot complete a
      handshake (test with two in-process transports).
- [ ] A single node with loopback advertise starts exactly as today (the
      existing gate tests in `Config.Validate` stay green).

### US-021: Blob sync and cluster join travel over TLS
**Source:** B7 (M8 in `docs/security-review-2026-07-06.md`)

**Description:** As an operator, I want mail bodies and join requests between
nodes to be encrypted and the peer verified. Blob pulls build `http://` URLs
(`internal/cluster/blobsync.go:113,135`) and auto-join posts to `http://`
(`internal/agent/agent.go:3192`).

**Acceptance Criteria:**
- [ ] Before code, a short design note in `docs/multi-node-cluster-checklist.md`
      states which listener serves `/internal/blob/*` and
      `/internal/cluster/join` to peers and how a peer is authenticated (the
      repository guardrail requires a private listener, explicit auth, or
      both).
- [ ] When cluster TLS is configured, blob HEAD/GET and join requests use
      `https://` and verify the peer against the cluster CA; a peer with a
      certificate from another CA makes the pull fail and the failure is
      logged with the peer name.
- [ ] Without cluster TLS (single node, dev), behaviour is unchanged.
- [ ] Blob pulls keep re-hashing the content (existing integrity check).

---

### Phase C: Data correctness

Release note: mail written in the web composer obeys the recipient's rules,
calendar data survives edits and imports intact, and stale routing and
sharing entries are reported or removed.

### US-022: Web-composed local mail runs the recipient's rules and forwarding
**Source:** 18.42(a)

**Description:** As a user with mail rules or forwarding, I want mail from a
colleague on the same server to be filtered and forwarded like any other
mail. Compose delivers local copies straight into INBOX with
`DeliverMessage` (`internal/web/handlers/compose.go:551-558`), so the
recipient's rules and `ForwardAddress` never run.

**Acceptance Criteria:**
- [ ] Local recipients of a web-composed message are delivered through the
      same pipeline as SMTP submission to a local recipient: a
      `QueueEnvelope` with `Trusted=true` and `AuthUserId` set to the author.
- [ ] Given B's rule "subject contains `invoice` -> move to `Invoices`", a
      message from A via `POST /compose/send` with subject `invoice 42`
      arrives in B's `Invoices` mailbox and not in INBOX.
- [ ] Given B's `ForwardAddress=ext@remote.example` (verified) and
      `ForwardKeepCopy=false`, the message is not stored in B's INBOX and one
      `OutboundMessage` to `ext@remote.example` is enqueued.
- [ ] A's Sent copy and external recipients behave as today.
- [ ] If the envelope cannot be written to the spool, the request fails
      with 500 and the "sent" flash is not shown.

### US-023: Remove calendar shares, including legacy entries
**Source:** 18.41(d)

**Description:** As a user, I want to stop sharing a calendar, and I want old
share entries that never worked to stop filling the 50-entry cap
(`internal/web/handlers/calendar.go:38,314`). Entries saved before shares were
stored as user IDs are raw strings that never match `IsSharedWith`, and
there is no route to remove any entry.

**Acceptance Criteria:**
- [ ] `POST /calendar/{calendarID}/unshare` with `share_with=<entry>` removes
      that exact entry (a user ID or a legacy string) and returns 200 with
      `HX-Trigger: calendarRefresh`.
- [ ] Removing an entry that is not present returns 200 and writes nothing.
- [ ] Another user's calendar ID returns 404.
- [ ] On leader startup, every `SharedWith` entry that is not the ID of an
      existing profile is removed through Raft, logged with the calendar ID
      and the number removed. A second startup removes nothing.
- [ ] A calendar holding 50 entries of which 10 are legacy accepts a new
      share after the cleanup.

### US-024: Quota refusals on full-page forms render inside the app
**Source:** 18.41(c)

**Description:** As a user, I want a quota refusal on a normal form post to
show the app's page with the message, not a bare text page. `refuseQuota`
(`internal/web/handlers/quota.go:58-60`) writes plain text, which is fine for
htmx swaps but not for full-page posts such as `/compose/send`
(`internal/web/templates/compose.templ:34`).

**Acceptance Criteria:**
- [ ] A request without `HX-Request: true` refused for quota returns 507,
      `Content-Type: text/html`, and a page rendered with the app layout
      (the same layout element the other app pages use) containing the
      translated `quota.exceeded` text.
- [ ] The same request with `HX-Request: true` returns 507 with the short
      text response as today.
- [ ] This applies to every `refuseQuota` caller and to the recipient refusal
      at `compose.go:531`.
- [ ] The page text comes from the i18n catalogue in every shipped locale (a
      grep pass finds no new hard-coded English).

### US-025: All-day events keep their date in any server time zone
**Source:** 2026-10-23 review finding (progress.md, 18.43 entry)

**Description:** As a user, I want editing an all-day event on the web to
leave its date alone. CalDAV stores all-day events at UTC midnight, but the
edit form formats the date in the server's zone
(`internal/web/templates/calendar.templ:740`) and parses it back in the
server's zone (`internal/web/handlers/calendar.go:798-803`). On a server
behind UTC, a CalDAV event on 5 November shows as 4 November, and saving the
form moves it.

**Acceptance Criteria:**
- [ ] With the process zone set to `America/New_York` in the test: a CalDAV
      `PUT` with `DTSTART;VALUE=DATE:20261105`, then `GET` of the edit form,
      shows `start_date` value `2026-11-05`.
- [ ] Posting that form with only the title changed leaves the stored
      `DTSTART;VALUE=DATE:20261105` and `DTEND;VALUE=DATE:20261106`.
- [ ] A web-created all-day event for `2026-11-05` in that zone is written
      with `DTSTART;VALUE=DATE:20261105` and appears on 5 November in the
      month view.
- [ ] The same three cases pass with the zone set to `Asia/Tokyo` and `UTC`.

### US-026: CalDAV reads the master event, not the first VEVENT
**Source:** 2026-10-23 review finding (progress.md, 18.43 entry)

**Description:** As a user of a recurring event with exceptions, I want the
server's summary of the event to describe the series. `icalToEvent`
(`internal/caldav/server.go:374`) takes `events[0]`, which a client may send
as an override (a VEVENT with `RECURRENCE-ID`).

**Acceptance Criteria:**
- [ ] A `PUT` whose calendar holds an override first (with `RECURRENCE-ID`,
      summary `Moved`) and the master second (no `RECURRENCE-ID`, `RRULE`,
      summary `Weekly`) stores an Event with title `Weekly`, the master's
      start time and the master's recurrence rule.
- [ ] The stored `ical_raw` still contains both VEVENTs.
- [ ] A calendar with only overrides (no master) uses the earliest override
      and logs at Debug.

### US-027: Calendar import keeps the raw iCalendar data and groups overrides
**Source:** 2026-10-23 review finding (progress.md, 18.43 entry)

**Description:** As a user importing an `.ics` file, I want the imported
events to keep their full data and recurring series to stay as one event.
`ImportCalendar` builds a calendar with no `PRODID`, so encoding fails and
the error is ignored (`internal/web/handlers/calendar.go:684-690`), and it
stores every VEVENT as its own event (`calendar.go:661`).

**Acceptance Criteria:**
- [ ] Importing a file with one simple event stores an Event whose
      `ical_raw` is non-empty and contains `PRODID:-//Bongo-Go//Calendar//EN`
      and `VERSION:2.0`.
- [ ] Importing a master plus two overrides that share one `UID` stores one
      Event whose `ical_raw` holds all three VEVENTs, with fields taken from
      the master (US-026 rule).
- [ ] VTIMEZONE components referenced by the events are carried into
      `ical_raw`.
- [ ] All-day events are imported with `AllDay=true`.
- [ ] An event whose raw data cannot be encoded is counted as an error and
      not stored; the flash reports `Imported N events, M failed`.

### US-028: The orphan report lists stale legacy aliases and catch-alls
**Source:** 18.39(c)

**Description:** As an operator, I want the address orphan report to show
every routing entry that points at a deleted account. `FindOrphanedAddresses`
(`internal/store/address_orphan.go:40`) scans only `/system/address/`. Legacy
`/system/alias/{addr}` keys (value: the target's email) and
`/system/catchall/{domain}` keys (value: a user ID) are not checked, and
`internal/store/user_routing_purge.go` says so.

**Acceptance Criteria:**
- [ ] `GET /internal/addresses/orphaned` includes a legacy alias whose target
      email has no account, with `kind: "legacy-alias"`, and a catch-all
      whose user ID has no profile, with `kind: "catchall"`.
- [ ] Entries pointing at live accounts are not listed.
- [ ] `POST /internal/addresses/repair` is unchanged: it does not delete the
      new kinds (report only in this release).
- [ ] The comment in `user_routing_purge.go` is updated.

### US-029: Followers remove orphan users rows at startup
**Source:** B22

**Description:** As an operator of a multi-node cluster, I want users rows
whose profile no longer exists to be removed on every node. `rebuildProfiles`
(`internal/cluster/fsm_rebuild.go:79`) never prunes, and `sweepOrphanedUsers`
(`internal/agent/agent.go:2492-2494`) runs on the leader only, so rows
orphaned before 16.21 stay on followers and block the address there.

**Acceptance Criteria:**
- [ ] A startup reconcile runs on every node before the Raft node starts
      (like `ReconcileUserEnabled`), deleting each `users` row and GAL entry
      whose `/user/{id}/profile` is absent.
- [ ] Rows with a profile are untouched.
- [ ] The count removed is logged; zero removals log nothing.
- [ ] After the reconcile, `auth.CheckAddressFree` for the orphan's address
      on that node returns nil.

### US-030: The reload log shows which config file version was read
**Source:** B23

**Description:** As an operator, I want the reload log to show the config
file's modification time and size, so I can see when a replaced bind mount
left the container reading the old file. `config reload complete`
(`internal/agent/reload.go:208-214`) logs neither.

**Acceptance Criteria:**
- [ ] The `config reload complete` line carries `file_mtime` (RFC 3339) and
      `file_size` (bytes) of the file that was read.
- [ ] The startup config log line carries the same two fields.
- [ ] A test changes the file, reloads, and asserts both fields changed.

### US-031: SMTP listeners honour server.bind_addr
**Source:** B25

**Description:** As an operator, I want SMTP to listen where I told the
server to listen. `internal/smtp/server.go:962,975` use `:port`, while IMAP,
POP3 and web use `server.bind_addr`.

**Acceptance Criteria:**
- [ ] With `bind_addr="127.0.0.1"`, the MX and submission servers' `Addr`
      are `127.0.0.1:<port>`.
- [ ] With `bind_addr` empty or `"0.0.0.0"`, `Addr` stays `:<port>`, so
      SMTP keeps listening on IPv6 as well as IPv4 (see section 6).
- [ ] With `bind_addr="::1"`, `Addr` is `[::1]:<port>`.
- [ ] `config.Diff` classifies the key as restart-required for SMTP as it
      already is for the other listeners.

---

### Phase D: CI, supply chain and test reliability

Release note: container images and actions are pinned and kept current, the
deploy hook is tested in CI, and the known flaky tests are fixed at the cause.

### US-032: Pin the nginx image by digest
**Source:** 18.39(d)

**Description:** As an operator, I want every third-party image in the
deploy template pinned by digest, so a re-pushed tag cannot change what runs.
`deploy/docker-compose.yml:210` uses `nginx:1.29-alpine`.

**Acceptance Criteria:**
- [ ] The nginx line reads `nginx:1.29-alpine@sha256:<64 hex>` with a digest
      that `docker pull` resolves.
- [ ] A CI step fails when any `image:` line in `deploy/docker-compose.yml`
      for a third-party image lacks `@sha256:`. Bongo's own image is exempt
      and the exemption is a comment in the check.

### US-033: Dependabot keeps actions and base images current
**Source:** 18.39(d)

**Description:** As a maintainer, I want update pull requests for pinned
actions and images, so pinning does not mean falling behind. There is no
`.github/dependabot.yml`.

**Acceptance Criteria:**
- [ ] `.github/dependabot.yml` exists with three entries, each weekly:
      `github-actions` (directory `/`), `docker` (directory `/deploy`, for
      `deploy/Dockerfile`) and `docker-compose` (directory `/deploy`).
- [ ] `deploy/test-certs-hook.sh` reads the certs-dumper image from
      `deploy/docker-compose.yml` instead of hard-coding the digest
      (line 15), so a Dependabot bump cannot leave the two out of step.
- [ ] The file passes GitHub's schema (checked by opening a test PR or with a
      schema-validating lint step).

### US-034: Run the certs hook test in CI
**Source:** 18.39(d)

**Description:** As a maintainer, I want `deploy/test-certs-hook.sh` to run
on every pull request that touches `deploy/`, since it guards the compose
wiring as well as the script.

**Acceptance Criteria:**
- [ ] A CI job runs `sh deploy/test-certs-hook.sh new` and expects exit 0,
      then `sh deploy/test-certs-hook.sh old` and expects exit 1.
- [ ] The job runs on pull requests that change `deploy/**` and on pushes
      to `main`.
- [ ] The job uses the same pinned `actions/checkout` as the other jobs.

### US-035: Ready means the web listener is bound
**Source:** 18.40 (new flake: `TestUserManagement`)

**Description:** As a maintainer, I want `Agent.Ready()` to become true only
after the web listener is bound. Today `a.ready.Store(true)`
(`internal/agent/agent.go:1849`) runs before `ListenAndServe` starts in a
goroutine (`agent.go:1888,1896`), so the integration helper
(`test/integration/helpers_test.go:119`) can return while the port is not yet
listening. `TestUserManagement` (`test/integration/admin_test.go:139`)
failed on main CI with "connection refused" at its first login. A bind
failure is also reported only after the helper has returned success, so
the helper's retry on "address already in use" (`helpers_test.go:171`) never
sees it.

**Acceptance Criteria:**
- [ ] The web listener is created with `net.Listen` before `ready` is set,
      and served with `Serve`/`ServeTLS` on that listener. The same change is
      made in the agent-role path (`agent.go:3293,3305`).
- [ ] With the web port already taken by the test, `Run` returns an error
      containing "address already in use" and `Ready()` is never observed
      true.
- [ ] With a free port, a TCP dial to the web address made immediately after
      `Ready()` first returns true succeeds (repeat 50 times in the test).
- [ ] `go test -race -tags=integration -run TestUserManagement -count=20
      ./test/integration/` passes 20 of 20.

### US-036: Make TestSMTPFullMailFlow deterministic
**Source:** 18.40

**Description:** As a maintainer, I want `TestSMTPFullMailFlow`
(`test/integration/smtp_test.go:20`) to wait for delivery instead of
sleeping one second (`smtp_test.go:76`), and to know why `WaitIdle` could
report idle before the per-user SQLite rows existed (`smtp_test.go:136-146`).

**Acceptance Criteria:**
- [ ] First, reproduce with `-count=16` and record the failure rate and the
      failing line in the task notes.
- [ ] The fixed sleep is replaced by polling for the mail key with a 10 s
      deadline.
- [ ] The cause of the SQLite failure is found and stated in the commit
      message. If it is in `FSM.WaitIdle` or the indexer
      (`internal/cluster/fsm.go:168-177,473`), a unit test reproduces it
      first.
- [ ] `-count=30` passes 30 of 30 under `-race`.

### US-037: SIGHUP tests fail instead of hanging
**Source:** B24

**Description:** As a maintainer, I want a teardown bug in signal handling to
fail a test within seconds, not hang until the 10-minute `go test` timeout.
`cmd/bongo-go/serve_signal_test.go:33,58` call `defer stop()` with no bound.

**Acceptance Criteria:**
- [ ] Every test in `serve_signal_test.go` calls `stop` through one helper
      that fails the test if `stop` has not returned within 2 seconds.
- [ ] Mutation check: making `stop` block forever turns each test red within
      5 seconds.

### US-038: End-to-end test of a bounce through SRS
**Source:** 18.42(e), 18.43(f)

**Description:** As a maintainer, I want one test that drives a real DSN from
`BounceGenerator` through the SRS address back to the original sender, and
a deliver-level test that relays a bounce for an SRS1 address. Today the
deliver tests (`internal/pipeline/deliver/srs_test.go:199`) only cover SRS0
with hand-built bounces.

**Acceptance Criteria:**
- [ ] Integration test: an inbound message from `bob@example.org` with
      `spf_result=pass` to local user B, who forwards to a remote target; the
      fake relay answers 550; the queue worker's `BounceGenerator` produces a
      DSN to the SRS address; that DSN enters through the MX; exactly one
      `OutboundMessage` is enqueued to `bob@example.org` with a null sender,
      and B's mailboxes receive nothing.
- [ ] Deliver-level test: a null-sender bounce to an SRS1 address issued
      here is relayed to the decoded SRS0 address; a tampered SRS1 hash is not
      relayed.
- [ ] Both tests go red if `relaySRSBounce` is short-circuited.
- [ ] Depends on US-010 (the sender is authenticated, so the bounce is
      allowed).

### US-039: Middleware-level test for ReissueSessionCookie
**Source:** 2026-10-23 review finding (progress.md, 18.43 entry)

**Description:** As a maintainer, I want the cookie that ChangePassword
reissues (`internal/web/middleware/auth.go:118`) to be tested against
`RequireAuth`, not only through the handler.

**Acceptance Criteria:**
- [ ] A cookie from `ReissueSessionCookie(secret, data, secure, epochB)`
      passes `RequireAuth` (handler reached) when `stateFn` returns
      `Found=true, Epoch=epochB`.
- [ ] The same cookie is refused (redirect to `/login`, session cookie
      cleared) when `stateFn` returns `Epoch=epochC`.
- [ ] The cookie keeps `data.Exp` (`MaxAge` within 1 second of
      `data.Exp - now`), and sets `HttpOnly`, `SameSite=Lax` and `Secure`
      as passed.
- [ ] A cookie with one byte of the signature changed is refused.

## 4. Functional Requirements

Phase A
- FR-1: The calendar share endpoint must allow at most 20 requests per user
  per rolling hour and return 429 beyond that (US-001).
- FR-2: A self-service send-as claim must not create a `/system/address/`
  record until the code is verified, and its responses and listings must not
  depend on whether another account holds the address (US-002).
- FR-3: Account creation must replace an unverified identity record held by
  another user (US-003).
- FR-4: The leader must delete verified send-as identities that collide with
  another account's address at startup (US-004).
- FR-5: Alias deletion must only remove address records that belong to an
  alias with a `/system/alias/` key (US-005).
- FR-6: IMAP, POP3 and SMTP login must derive the session epoch from the
  profile the password was verified against (US-006).
- FR-7: Re-authentication on the security settings page must verify the
  password against the profile loaded under the user lock (US-007).
- FR-8: When live account state cannot be read, the request must not carry
  admin rights (US-008).
- FR-9: A LOCAL-origin forward must be charged to the author's recipient
  budget, and must fall back to FORWARD origin when the budget is spent
  (US-009).
- FR-10: No DSN or relayed bounce may be sent to the original sender of a
  forwarded message that was not authenticated by SPF or DMARC (US-010).
- FR-11: CalDAV and CardDAV request bodies must be capped at 10 MiB (US-011).
- FR-12: IMAP APPEND, SMTP RCPT (when SIZE is declared) and web compose must
  refuse a write that would take the domain over quota (US-012, US-013).

Phase B
- FR-13: Internal request signing must use the effective session secret
  (US-014).
- FR-14: Agent-role SMTP must refuse SRS-shaped recipients that fail HMAC
  verification with 550 (US-015).
- FR-15: Agent-role antispam must not skip DMARC on envelope flags alone
  (US-016).
- FR-16: Cluster listeners must bind to `cluster.bind_addr`, defaulting to
  the advertise address (US-017).
- FR-17: Agent gRPC servers and clients must use mutual TLS from the
  `agent_server.tls` files and refuse to start without them (US-018,
  US-019).
- FR-18: A clustered Raft transport must refuse plaintext and verify peers
  against the cluster CA (US-020).
- FR-19: Blob sync and cluster join must use TLS with peer verification when
  cluster TLS is configured (US-021).

Phase C
- FR-20: Web-composed mail to local recipients must pass through the delivery
  pipeline (US-022).
- FR-21: Users must be able to remove any calendar share entry, and legacy
  entries must be removed at startup (US-023).
- FR-22: Quota refusals on full-page posts must render in the app layout
  (US-024).
- FR-23: All-day event dates must be independent of the server time zone
  (US-025).
- FR-24: CalDAV and import must take event fields from the master VEVENT and
  keep the full raw iCalendar data (US-026, US-027).
- FR-25: The orphan report must include legacy aliases and catch-alls
  (US-028).
- FR-26: Every node must remove orphan users rows at startup (US-029).
- FR-27: The reload log must include the config file's mtime and size
  (US-030).
- FR-28: SMTP must honour `server.bind_addr` (US-031).

Phase D
- FR-29: Third-party images in the deploy template must be pinned by digest,
  checked in CI (US-032).
- FR-30: Dependabot must cover actions, the Dockerfile and the compose file
  (US-033).
- FR-31: CI must run the certs hook test on deploy changes (US-034).
- FR-32: `Ready()` must imply the web listener is bound (US-035).
- FR-33: The two named flaky tests must pass repeated runs (US-035, US-036).
- FR-34: Signal tests must be bounded (US-037).
- FR-35: The SRS bounce path must have an end-to-end test (US-038).
- FR-36: `ReissueSessionCookie` must have a middleware-level test (US-039).

## 5. Non-Goals (Out of Scope)

- **B10**: groups and mailing lists.
- **B11**: UI reactivity and keyboard-driven triage.
- **B13**: backup that scales past a few GB.
- The existing research documents in `prds/research/`: app passwords
  (`prd-app-passwords.md`), the zero-gap inbound SMTP front end
  (`prd-zero-gap-inbound-smtp-front-end.md`), UI reactivity and keyboard
  triage (`prd-ui-reactivity-and-keyboard-triage.md`), and the
  share-for-testing assessment (`assessment-share-for-testing.md`).
- New features in general. In particular: wiring `AgentServer` or
  `DispatchOrLocal` into the server role (no production caller exists today;
  this release only makes that code safe to wire), forming a multi-node
  cluster in production, and enforcing `UserProfile.QuotaBytes`.
- The remaining reserved config keys in B21 (`server.datacenter`,
  `smtp.tls.ca_file`, `web.tls.ca_file`, `storage.pebble.wal_dir`). The
  operator decided on 2026-10-11 to keep them until the code that needs them
  is built. Only `agent_server.tls.*` is wired, in US-018.
- Automatic repair of the new orphan kinds in US-028 (report only).
- Dependabot for Go modules (see Open Questions).

## 6. Technical Considerations

### 6.1 Candidate triage

Every candidate given for this release was checked at `origin/main`
(`0a76063`).

**Already fixed, not in scope:**

| Item | Fixed by |
|------|----------|
| B8 `Config.Validate()` dead code | 16.5, merged in `78b6477` (#34). `cmd/bongo-go/agent.go:265` now calls it. |
| B9 config reload on SIGHUP | 16.6 to 16.14, merged in `78b6477` (#34). `internal/agent/reload.go`. |
| B12 first delivery after restart stalls | `ca632fd` (#20), measured and closed in `ed4674e` (#27). |
| B15 blob GC deleted referenced blobs | Phase 14, `84ac4a6` to `a7b9894` (#9 to #15) and `f3f16bc` (#17); `blob_gc` back on in `d42d979` (#19). |
| B16 three flaky tests | 16.1, merged in `78b6477` (#34); see also `4800a14` (#31). |
| B17 `CreateUser` does not normalise | 16.2, merged in `78b6477` (#34). `internal/auth/create.go:140`. |
| B18 pipeline outcomes logged without identity | 16.3, merged in `78b6477` (#34). `internal/pipeline/runner.go:267,286`. |

**Not applicable:** B21 apart from `agent_server.tls.*` (operator decision,
see Non-Goals).

**In scope:** every other candidate, mapped to a story:

| Source | Story |
|--------|-------|
| 18.39(a) | US-002 |
| 18.39(c) | US-028 |
| 18.39(d) | US-032, US-033, US-034 |
| 18.40, TestUserManagement flake | US-035, US-036 |
| 18.41(b) | US-013 |
| 18.41(c) | US-024 |
| 18.41(d) | US-023 |
| 18.41(e) | US-001 |
| 18.41(f) | US-008 |
| 18.42(a) | US-022 |
| 18.42(b) | US-009 |
| 18.42(d) | US-015 |
| 18.42(e), 18.43(f) | US-038 |
| 18.42(f) | US-014 |
| DAV PUT body cap | US-011 |
| IMAP APPEND / SMTP RCPT size-blind quota | US-012 |
| All-day date shift | US-025 |
| icalToEvent first VEVENT | US-026 |
| Calendar import loses ical_raw, splits overrides | US-027 |
| ChangePassword separate profile read | US-007 |
| ReissueSessionCookie untested | US-039 |
| IMAP/POP3/SMTP authenticate-then-AccountState race | US-006 |
| B7 (H4, M8, L6) | US-020, US-021, US-017 |
| B20 | US-018, US-019 |
| B21 `agent_server.tls.*` | US-018 |
| B22 | US-029 |
| B23 | US-030 |
| B24 | US-037 |
| B25 | US-031 |
| B26(a), (b), (c) | US-005, US-003, US-004 |
| B27(b), (d) | US-010, US-016 |

On import, TASKS.md rows 18.39 to 18.42 and backlog rows B7, B8, B9, B12,
B15 to B18 and B20 to B27 can be closed or replaced by the imported stories.

### 6.2 Story notes

- **US-002 and US-003 order.** Do US-002 first. After it, no new unverified
  `/system/address/` identity records are written, and US-003 only has to
  handle old ones.
- **US-006.** `LocalAuth.AuthenticateProfile` already exists for the web
  login (`b9273ec`). The protocol packages define their own small
  authenticator interfaces; extend those at the consumer, as the conventions
  require.
- **US-008** is a policy change to a deliberate trade-off. The proposed rule
  keeps mail reading available during a store error and removes only admin
  rights. Confirm before implementing (Open Question 2).
- **US-010.** The deliver stage knows the inbound authentication results
  (`QueueEnvelope.spf_result` and the antispam stage's DMARC result). Two ways
  to stop the relayed bounce: (a) for an unauthenticated original, rewrite the
  forward's envelope sender to an address on the forwarding domain that
  accepts bounces but never relays them, or (b) carry an "authenticated" flag
  inside the SRS address under the MAC. (a) needs no codec change. The queue
  worker side needs the same fact on `OutboundMessage`, which means a new
  proto field; old queued messages without it must default to "suppress".
- **US-012.** SMTP cannot know the message size at RCPT unless the client
  declares `SIZE` (RFC 1870). Most MTAs do, since we advertise it.
- **US-015.** An agent has no Pebble store, so it cannot check hosted
  domains. The HMAC check is the stronger test anyway; only we can mint a
  valid SRS hash.
- **US-018 to US-021** are deployment-affecting. US-018 makes role `agent`
  refuse to start without certificates; US-020 makes a clustered server
  refuse to start without readable cluster TLS files. Production runs
  `role: server` single-node with loopback advertise, so it is unaffected,
  but the release notes must say this. `docs/multi-node-cluster-checklist.md`
  is updated in the same change.
- **US-021** may need splitting once the design note is written. If the
  note shows it needs a new private listener, that listener is part of this
  story; do not route peer traffic through the public web listener.
- **US-022** changes compose from synchronous local delivery to spooled
  delivery. The success response then means "accepted for delivery", the
  same as SMTP submission. The envelope must carry `Trusted=true` and
  `AuthUserId` so antispam treats it as an authenticated submission (and the
  send-as authorizer still applies). A web user who sends to a local
  recipient and immediately opens that mailbox may see the message a moment
  later than today.
- **US-023.** Legacy entries never granted access (they never matched
  `IsSharedWith`), so deleting them changes no one's access. Converting them
  to user IDs would grant access the owner may have forgotten about; see Open
  Question 4.
- **US-025.** Web-created all-day events in a non-UTC zone were stored at
  local midnight. The fix should read all-day dates from `ical_raw` where
  present and treat the proto times as a UTC date otherwise. Existing
  records need no migration if every reader compares dates, not instants.
- **US-031.** `deploy/bongo.prod.hcl:27` and `deploy/bongo.example.hcl:55`
  set `bind_addr = "0.0.0.0"`. Passing that through literally would make the
  SMTP listeners IPv4-only and drop IPv6 MX traffic, so `0.0.0.0` and empty
  must keep `:port`. The live production config is in the infra repository;
  check it before release.
- **US-033.** The `docker-compose` Dependabot ecosystem must be confirmed as
  available for this repository; if it is not, the compose digests are
  bumped through the `docker` entry or by a scheduled job, and the story
  notes which.
- **US-035** also fixes the integration helper's retry: once bind errors
  come from `Run` before `Ready`, the existing retry on "address already in
  use" sees them.

### 6.3 Process for this release

- One PR per phase, or smaller; Phase A merges first.
- Each story's first test reproduces the gap (red) before the fix.
- Each negative assertion is mutation-checked once by the implementer and
  once by the reviewer.
- A review finding outside the story goes into section 8.2 of this PRD,
  not into TASKS.md, unless the story's change created it.
- `make test`, `make test-race`, `make lint`, `make sec` and the integration
  suite pass before each merge.

## 7. Success Metrics

- All 39 stories are done, and each has a test that fails on `0a76063`.
- TASKS.md rows 18.39 to 18.42 and backlog rows B7, B20 to B27 are closed.
- No new TASKS.md rows were added during the release apart from gaps opened
  by a change in it (count them at release time; the target is zero).
- `TestUserManagement` and `TestSMTPFullMailFlow` pass 20 and 30 repeated
  runs respectively under `-race`, and CI on `main` has no failure from a
  known flaky test during the release.
- `gosec -severity=medium` findings do not increase.
- Every third-party image in the deploy template is pinned by digest, and at
  least one Dependabot PR has been opened by the end of the release.

## 8. Open Questions

### 8.1 Decisions (operator, 2026-10-23)

1. **Releases:** one per phase group: v0.9.0 = Phases A and D, v0.10.0 =
   Phase C, v0.11.0 = Phase B.
2. **B7 cluster transport (US-017, US-020, US-021): in scope**, reversing the
   2026-07-21 deferral. They ship in v0.11.0 with the agent mTLS stories.
3. **Defaults accepted as written:** US-001 20 share requests per user per
   hour (the 200/400 split stays; the rate limit is the fix); US-008 a store
   error keeps the session readable but drops admin rights; US-009 a LOCAL
   forward over the author's budget is sent as FORWARD, not dropped; US-011
   DAV body cap 10 MiB as a constant; US-023 legacy share entries are deleted,
   not converted; Dependabot also covers Go modules (`gomod`).
4. **US-010 mechanism: option (a)**, an envelope sender on the forwarding
   domain that accepts bounces but never relays them. No SRS codec change.
5. **US-031:** `0.0.0.0` and an empty `bind_addr` keep binding every address
   (IPv4 and IPv6); only a specific address is honoured literally.
6. **US-033 reversed after release (2026-10-23):** Dependabot is removed. Its
   first run opened twelve PRs at once. Vulnerable dependencies are caught by
   `govulncheck` in CI and in the release gate, and go through the normal PRD
   flow (section 8.2, then a release scope). Pinned actions and images are
   bumped by hand when a release scope includes it. The rest of US-033 stays:
   the certs hook test reads the dumper image from the compose file.

**Not confirmed during scoping:**

- The cause of the 18.40 SQLite failure (`WaitIdle` returning true before
  the per-user rows exist). The counter in `internal/cluster/fsm.go:473-481`
  is incremented before the queue send and decremented after indexing, so
  the obvious race is not there. US-036 starts by reproducing it. The fixed
  one-second sleep at `smtp_test.go:76` is confirmed.
- The `TestUserManagement` CI failure log itself was not available. The
  mechanism (US-035) is confirmed in code: `ready` is set at
  `agent.go:1849` before the listener is bound at `agent.go:1888/1896`.

### 8.2 Candidates for the next release

Findings from reviews during this release go here, with the story and review
that raised them, unless the story's own change introduced them (those are
fixed before merge). This list is the starting point for the v0.10 scope.
Nothing goes from here into TASKS.md during v0.9.

From the Phase 19 batch 1 reviews (2026-10-23):

- `internal/web/handlers/auth.go:121-126`: web Login still falls back to
  `Authenticate` for authenticators without `AuthenticateProfile`
  (production uses `LocalAuth`, which has it). (19.6)
- `internal/web/handlers/security.go` ChangePassword and the admin profile
  writes have no compare-and-swap; `lockUser` is per process, so a write on
  another node between read and write can be overwritten. (19.7)
- `internal/pop3/session.go` handlePass calls `authLimiter.Succeed` before the
  nil/disabled profile guard (unreachable with `LocalAuth`). (19.6 review)
- DAV PUT on update answers 201, not 204 (go-webdav v0.7.0). (19.11)
- No global budget for DAV bodies in flight: each authenticated request can
  hold up to 10 MiB. (19.11)
- `internal/smtp/server.go`: a client can declare a small `SIZE` and send up
  to the message cap; DATA does not check actual size against the declared
  one. (19.12)
- Compose checks each local recipient's domain separately, so several copies
  landing in one domain are each admitted against the same headroom. (19.13)
- `internal/store/domain_quota_store.go`: `RecipientOverQuota` has no
  production caller left. (19.12)
- The per-node share limiter resets on restart and multiplies by the number of
  web-serving nodes. (19.1)
- `internal/agent` tests bind fixed ports 8301 and 1143 and collide when two
  runs overlap. (19.1, 19.11 reviews)

From the Phase 19 batch 2 reviews (2026-10-23):

- `internal/web/handlers/address_verify.go` RequestVerification: the taken
  branch skips the blob write and the enqueue, so it answers measurably
  faster than the free branch (timing oracle). (19.2)
- Admin AddAddress (`admin.go`, early 409 on another user's record) and alias
  creation (`CheckAddressFreeFor`) are still blocked by another user's legacy
  unverified identity, the same class as US-003. Self-serve also treats such
  a record as taken, so B's correct code is a decoy for good. (19.2, 19.3)
- AddSendAsAddress has no per-user cap on pending claims; a cap on the
  caller's own count would leak nothing. (19.2)
- `/user/{id}/addrverify/` keys are deleted only by SubmitCode and CreateUser,
  so a claim can outlive an admin removal, domain cascade or orphan cleanup
  and still show on the settings page. (19.2)
- SubmitCode's allocation check and record write are not atomic: two
  simultaneous correct submits can both pass and the later write wins. (19.2)
- Behaviour change for the release notes: self-serve claims no longer appear
  on the admin user page, and GrantSend answers "address not found" for an
  address a user has only claimed; the admin path is AddAddress then
  GrantSend. (19.2)
- `internal/store/address_shadowed.go` alias-table branch checks only that the
  Pebble alias key exists, not that its target differs from the identity's
  owner. The US-004 "extra address" conflict cannot be detected: the identity
  overwrote the only record at that key. (19.4)
- `admin.go` DeleteAlias: a /system/alias/ read error other than not-found is
  meant to answer 500; no test, because the handler holds a concrete
  PebbleStore and has no seam to inject the error. (19.5)
- `internal/imap` session revocation has the same fail-open-on-store-error
  shape US-008 removed from the web; it grants no admin rights. (19.8)
- `senderAuthenticated` accepts DMARC pass, but DMARC aligns the header From
  and bounces go to the envelope sender: SPF fail plus an aligned DKIM From
  still gets an SRS sender, so bounces reach a forged MAIL FROM. SPF pass (or
  AuthUserId) alone would close it. US-010 defines authenticated this way. (19.10)
- SRS addresses issued before the upgrade for unauthenticated forwards keep
  relaying bounces until SRSMaxAgeDays expires. (19.10)
- Differences from the US-009/010 text as written: AuthUserId counts as
  authenticated; trusted-network mail without SPF pass is now unauthenticated;
  the worker DSN to the sink is discarded anyway, but the proto field is kept
  so pre-upgrade FORWARDs default to suppress. (19.9, 19.10)

From the Phase 20 reviews (2026-10-23):

- `scripts/check-image-pins.sh` is line-based: a quoted `"image":` key or a
  flow-style `{image: x}` slips past it. Running it over
  `docker compose config --format json` would close that, and a fixture
  self-test in CI would pin its exemption and empty-file guards. (20.1)
- certs-dumper is pinned as `:latest@sha256:...`; a real vN tag would say
  which release runs. (20.2)
- With Dependabot removed (8.1 item 6), nothing reports stale pinned actions,
  base images or Go modules that have no advisory; a release scope has to
  include the bumps. Raised by its one run: actions/checkout 4 to 7,
  setup-go 5 to 7, docker build actions, golangci-lint action 8 to 9, Go 1.27
  base image, the deploy template's own image (0.6.20), and
  `hashicorp/raft`, whose newer release needs a `memberlist` and
  `go-metrics` upgrade together. (20.2)
- The infra repo's production compose probably needs the same nginx digest
  (operator, via the infra repo). (20.1)
- The certs hook CI job pulls from Docker Hub and can fail on its rate
  limit. (20.3)
- `internal/agent/agent.go` HTTPS redirect server still uses ListenAndServe
  in a goroutine, so a bind failure is only logged; and a Serve error after
  ready returns without Shutdown, leaving ready true. (20.4)
- `test/integration/smtp_test.go:38` still sleeps 500 ms after CreateUser;
  `updateDerivedIndexes` called outside Apply (tests only) has no in-flight
  hold. (20.5)
- `test/integration/srs_bounce_test.go`: one run with relaySRSBounce
  short-circuited reported a race, probably teardown after the 60 s failure;
  not reproduced. (20.7)

From the Phase 21 batch 1 reviews (2026-10-23):

- The rules stage sets one TargetMailbox and one delete verdict for a whole
  envelope, so on the SMTP multi-RCPT path one recipient's rule files or drops
  every other recipient's copy. Compose avoids it with one envelope per local
  recipient. (21.1)
- Compose spool writes, the external enqueue and the Sent copy are separate
  writes: a failure after some envelopes were written returns 500 with those
  recipients already queued, so a retry duplicates their copies. (21.1)
- The compose flash strings ("Message sent", "queued") and its http.Error
  texts, including "Local delivery is not available on this node", are
  hard-coded English. (21.1)
- Release note: web-composed local mail now runs antispam scoring, gets a new
  pipeline GUID and Authentication-Results header, and the recipient's
  forwards are charged to the author's budget as LOCAL forwards. (21.1)
- `ExportCalendar` copies only VEVENT children out of `ical_raw`, so an
  exported event with a TZID loses its VTIMEZONE; imports now carry TZIDs, so
  this affects every zoned import. (21.6)
- Import gives every event a fresh GUID, so re-importing a file duplicates its
  events; events imported before this release have an empty `ical_raw` and are
  not regrouped. (21.6)
- The import quota pre-check uses the upload size; grouped `ical_raw` with
  VTIMEZONEs can differ slightly. (21.6)
- `PebbleStore.Get` has no fault-injection seam, so a profile read error
  counting as an orphan in `ReconcileOrphanUsers` is checked by reading the
  code only. (21.8)
- The orphan report lower-cases a legacy alias value before checking it, while
  the resolver looks up `users.username` with the case as stored; an alias
  differing only in case from its username is reported live but does not
  resolve. (21.7)
- The agent gRPC listener (`internal/agent/grpc/server.go`) still listens on
  `:port` and ignores `server.bind_addr`. (21.10)
- Release note and operator check: a specific `server.bind_addr` now limits
  SMTP to that address; check the live config in the infra repo before
  deploying. (21.10)

From the Phase 21 batch 2 reviews and CI (2026-10-24):

- Reminders for all-day events fire relative to 00:00 UTC
  (`internal/agent/reminders.go:94`); in America/New_York that is the
  previous evening. Web-created events used local midnight before. (21.4)
- `MonthView` queries with local month bounds, so a UTC-midnight all-day event
  on the first or last day of a neighbouring month can be missing from the
  grid's leading or trailing cells in some zones. (21.4)
- Legacy web all-day records in zones at UTC+12 to +14 or UTC-12 can still be
  a day out until re-saved; there is no migration. (21.4)
- Drag-and-drop reschedule (`bongo.js`) sends local instants; if all-day chips
  become draggable, `RescheduleEvent` must read them by UTC date. (21.4)
- There is no UI listing a calendar's shares, so unshare is reachable only by
  a direct POST. (21.2)
- CSRF on the calendar share and unshare routes is only structural (route
  group); a route-table test asserting every mutating calendar route sits
  behind RequireCSRF would catch a route moving out. (21.2)
- `TestReadyImpliesWebBound` picks a free port, releases it and binds it
  again; on a busy CI runner another test can take it in between ("address
  already in use", PR #61, passed on rerun). (20.4)


From the Phase 22 batch 1 reviews (2026-10-24):

- `Reload()` in `internal/agent/agent.go` calls `validateSessionSecret` with
  role "agent" and an empty secret, so an agent without
  `web.session_secret` can never hot-reload; `runAgent` also skips the
  32-byte minimum. (22.1)
- The `cmd/bongo-go` CLIs sign with their own `--session-secret`, so they
  cannot authenticate to a dev node that auto-generated its secret. (22.1)
- On an agent, a Trusted envelope with no AuthUserId still bypasses DMARC.
  Once DispatchOrLocal has a caller, the deny-all authorizer makes every
  server-dispatched authenticated envelope run DMARC on the agent, including
  users sending from their own p=reject domains; the server should pass a
  verified send-as decision instead. (22.3)
- `TestNothingSignsWithConfiguredSessionSecret` and
  `TestRaftSourceNeverSkipsVerify` read source from disk, so `-overlay`
  mutations cannot reach them; both rules are also held by behavioural
  tests. (22.1, 22.7)
- `Config.Validate` accepts a server with `server.bind_addr` 0.0.0.0 and no
  `advertise_addr`; it now fails only at startup, in NewRaftNode. (22.4)
- Release notes for v0.11.0: a server with a non-loopback `advertise_addr`
  and no join now needs cert, key and ca_file; one with `server.bind_addr`
  0.0.0.0 and no `advertise_addr` no longer starts; role agent needs the
  three `agent_server.tls` files and, until US-019, cannot reach its servers;
  an agent with no or a different `web.session_secret` refuses SRS bounces
  with 550. A single node with a cluster.tls certificate and no ca_file now
  also requires peers to present that certificate (it never dials itself).
  Production (role server, single node, loopback advertise) is unaffected.
  (22.1-22.7)

From the Phase 22 batch 2 reviews (2026-10-24):

- Role server does not run `AgentServer`, so an agent-role node has nothing
  to register with and `DispatchOrLocal` has no caller: agent mode does not
  work end to end, before or after v0.11.0. Wiring it is a feature, not a
  hardening fix. (22.6)
- `loadTLSFiles` (`internal/agent/grpc/tls.go`): the empty `ca_file` check is
  redundant (LoadCAPool fails and names the key anyway); an equivalent
  mutant survives. (22.6)
- The `LimitReader(maxBlobSyncBytes)` bound on pulled blobs is untested; an
  oversized blob still fails the hash check, but the bound is the disk
  guard. (22.8)
- `cluster.peer_port`, like the raft and gossip ports, is not validated
  (0, out of range, or equal to another cluster port). (22.8)
- `ClusterPeerAddr` and the raft advertise address use the advertise
  address, while `ClusterHasPeers` is also true for a non-loopback
  `cluster.bind_addr` alone: with a loopback advertise the peer listener
  binds the routable address but advertises 127.0.0.1. Fails closed. (22.8)
- Other `/internal/*` routes still sit on the public web router behind HMAC;
  the design note lists them as a follow-up. (22.8)
- Release notes for v0.11.0, in addition to batch 1: a node with peers opens
  the peer listener on `cluster.peer_port` (8302) and no longer serves blob
  sync or join on the web port, so every node must be upgraded together; an
  agent with processor roles needs `cluster.bind_addr` or `advertise_addr`
  set to its own IP; the `agent_server.tls` certificate needs both
  serverAuth and clientAuth EKU and a SAN for the dialled address. (22.6,
  22.8)
