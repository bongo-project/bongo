# PRD: Hardening release v0.12.0

## 1. Introduction / Overview

v0.9.0 to v0.11.0 closed the scope of `prd-hardening-v0.9.md`. Its section
8.2 collected 63 review findings as candidates for the next release. This PRD
takes the 17 of them that affect the single-node production deployment and
fixes the scope of v0.12.0. The rest stay in that list (see section 5).

Scope agreed with the operator on 2026-10-24. Every item was checked against
`main` at `7becc69` on the same day; none was already fixed.

**The scope rule.** The in-scope list is section 3. Anything a review finds
during this release goes into section 8.2 of this PRD, not into TASKS.md,
unless the change under review introduced it. A gap a change opens is fixed
before that change merges.

| Phase | Theme | Stories | TASKS rows |
|-------|-------|---------|------------|
| A | Security | US-001 to US-008 | 23.1 to 23.8 |
| B | Mail and calendar correctness | US-009 to US-013 | 24.1 to 24.5 |
| C | Reliability and supply chain | US-014 to US-017 | 25.1 to 25.4 |

v0.12.0 is tagged when all three phases are done.

## 2. Goals

- No control-plane route is reachable from the public web listener.
- Bounces of forwarded mail reach only senders we have verified.
- Size, memory and claim limits count what is actually sent or held.
- Account revocation holds when the store is failing, on every protocol.
- One recipient's rules affect only that recipient's copy.
- A retried send does not duplicate mail.
- Calendar export, reminders and the month grid agree on time zones.
- Bad cluster and port configuration is refused by `Config.Validate`.
- The known flaky tests are fixed at the cause, and pinned dependencies are
  current.

## 3. User Stories

### Phase A: Security

Release note: control routes move to a loopback-only listener, bounces of
forwarded mail are relayed only for verified senders, and size, memory,
claim and revocation checks fail closed.

### US-001: Control routes leave the public web listener
**Source:** 8.2 (22.8); CLAUDE.md guardrail on `/internal/*`.

**Description:** As an operator, I want the `/internal/*` control routes
(queue retry and drop, user create, address repair, snapshot, dangling-mail
purge, cluster members) served only on a private listener. Today they sit on
the public web router (`internal/agent/agent.go:950-970`, `1753-1785`) behind
HMAC alone, so the session secret is the only thing between the internet and
those operations. `/internal/kv/*` (raw Pebble PUT/GET/DELETE) has no CLI
caller at all.

**Design:** a control listener on `server.control_addr`, default
`127.0.0.1:8081`. HMAC (`RequireInternalAuth`) stays. The CLI's default
`--http-addr` becomes `127.0.0.1:8081`. The peer listener (blob sync, join)
is unchanged.

**Acceptance Criteria:**
- [ ] Every `/internal/*` route except the peer routes answers 404 on the
      public web listener, with or without a valid HMAC signature, for roles
      server and dev.
- [ ] The same routes answer on the control listener with a valid signature
      and 401 without one.
- [ ] `server.control_addr` must be a loopback IP literal; `Config.Validate`
      refuses anything else, naming the key. Restart class in `config.Diff`.
- [ ] A bind failure on the control listener fails startup before ready.
- [ ] The control server has explicit timeouts and is shut down with the web
      server.
- [ ] `/internal/kv/*` is registered only in dev role; the integration test
      that uses it still passes.
- [ ] Every `bongo-go` CLI subcommand that calls a control route defaults to
      `127.0.0.1:8081`; `--http-addr` still overrides it.
- [ ] Release note: operators who call the CLI with an explicit
      `--http-addr` pointing at the web port must change it.

### US-002: Bounce relay only for SPF-pass or authenticated senders
**Source:** 8.2 (19.10).

**Description:** `senderAuthenticated` (`internal/pipeline/deliver/
deliver.go:1213`) accepts DMARC pass. DMARC aligns the header From, but
bounces go to the envelope sender, so SPF fail plus an aligned DKIM From
still gets an SRS0 sender and bounces reach a forged MAIL FROM.

**Acceptance Criteria:**
- [ ] SPF fail with DMARC pass is unauthenticated: the forward gets the
      no-relay sender, not SRS0.
- [ ] SPF pass, or a non-empty `AuthUserId`, still gets SRS0.
- [ ] `TestSenderAuthenticated` case "dmarc pass" flips to false.

### US-003: SMTP DATA honours the declared SIZE
**Source:** 8.2 (19.12).

**Description:** RCPT weighs the `SIZE=` from MAIL FROM against domain quota
(`internal/smtp/server.go:550-563`), but DATA never compares the message
with it, so a client can declare 1 KB and send up to the message cap.

**Acceptance Criteria:**
- [ ] A message larger than its declared SIZE plus a slack of a tenth and
      1 KiB is refused with `552 5.3.4` and nothing is enqueued. The slack
      (decided 2026-10-24, see 8.1) keeps clients whose estimate is a little
      low working; it still stops a 1 KB declaration carrying a 50 MB body.
- [ ] A message within the allowance, and a message with no SIZE, behave as
      today. BDAT is checked the same way as DATA.
- [ ] Applies to the MX and submission servers.

### US-004: A global budget for DAV request bodies in flight
**Source:** 8.2 (19.11).

**Description:** `davLimitBody` (`internal/agent/agent.go:4136`) reads each
authenticated CalDAV and CardDAV body (up to 10 MiB) into memory. There is
no bound on the total.

**Acceptance Criteria:**
- [ ] A process-wide budget of bytes held by DAV bodies, default 64 MiB.
      A request that would exceed it gets 503 with `Retry-After` and holds
      nothing.
- [ ] The budget is released when the handler returns, including on error
      and panic.
- [ ] A test holds bodies up to the budget and shows the next request is
      refused, then accepted after one is released.

### US-005: Send-as claim requests leak nothing and do not pile up
**Source:** 8.2 (19.2).

**Description:** in `internal/web/handlers/address_verify.go`,
RequestVerification's taken branch skips the blob write and enqueue, so it
answers measurably faster than the free branch (a timing oracle on whether
an address belongs to someone). AddSendAsAddress has no cap on a user's
pending claims.

**Acceptance Criteria:**
- [ ] The request path of RequestVerification does no work that depends on
      whether the address is taken: building and sending the mail moves off
      the request, so both branches return after the same synchronous work.
      The comment says the send is best-effort and a failure is logged.
- [ ] A user with 20 pending claims gets an error on the 21st, with a
      translated message; the count is of the caller's own claims only.

### US-006: Send-as claims are cleaned up and settled once
**Source:** 8.2 (19.2).

**Description:** `/user/{id}/addrverify/` keys are deleted only by
SubmitCode and CreateUser, so a claim outlives admin removal of the address
and domain deletion and still shows on the settings page. SubmitCode's
availability check and record write are separate Raft writes, so two
simultaneous correct submits for one address can both pass.

**Acceptance Criteria:**
- [ ] Removing an address from a user (admin) and deleting a domain delete
      the matching claims; the settings page no longer lists them.
- [ ] SubmitCode holds a per-address lock across check and write, so two
      concurrent correct submits for one address yield one owner. The comment
      says the lock is per process (multi-node needs a composite command,
      out of scope).

### US-007: Account checks fail closed on store errors
**Source:** 8.2 (19.8).

**Description:** IMAP `checkAccount` (`internal/imap/backend.go:264-284`)
allows the command when `AccountState` errors. SMTP `checkAccountLive`
(`internal/smtp/server.go:315`) and POP3 (`internal/pop3/session.go:236`)
do the same. The web stopped doing this in US-008 of the previous PRD.

**Acceptance Criteria:**
- [ ] With `AccountState` erroring, an authenticated IMAP command fails with
      a temporary error, SMTP answers 4xx, POP3 answers `-ERR`; none grant
      access.
- [ ] The stale "like the web session check" comment is corrected.

### US-008: The HTTPS redirect server and Serve errors do not fail silently
**Source:** 8.2 (20.4).

**Description:** the redirect server (`agent.go:1969-1988`) binds inside a
goroutine after ready, logs a bind failure and is never shut down. A web
Serve error after ready returns from Run without Shutdown, leaving ready
true.

**Acceptance Criteria:**
- [ ] The redirect listener binds before ready; a bind failure fails startup.
- [ ] The redirect server is shut down with the others.
- [ ] A Serve error after ready runs Shutdown and leaves ready false, for
      roles server, dev and agent.

### Phase B: Mail and calendar correctness

Release note: one recipient's rules no longer affect other recipients of the
same SMTP message, retrying a failed web send no longer duplicates, and
calendar export, reminders and the month view handle time zones.

### US-009: Rules apply per recipient on SMTP delivery
**Source:** 8.2 (21.1).

**Description:** SMTP puts all local recipients in one envelope
(`internal/smtp/delivery.go:108-122`); the rules stage sets one
`TargetMailbox` and one delete verdict for the whole envelope
(`internal/pipeline/rules/rules.go:73-165`). One recipient's rule files or
drops every other recipient's copy. Compose already writes one envelope per
local recipient.

**Acceptance Criteria:**
- [ ] SMTP local delivery writes one envelope per local recipient, sharing
      the blob.
- [ ] RCPT a and b, where a has a delete rule: b gets the message in INBOX.
      Where a has a move-to-Folder rule: b's copy is in INBOX.
- [ ] Antispam and DMARC results are the same for each copy.
- [ ] The rules stage documents that it expects one recipient; with more
      than one it logs a warning (no other caller should produce one).

### US-010: Retrying a web send does not duplicate
**Source:** 8.2 (21.1).

**Description:** compose writes per-recipient spool envelopes, the external
queue entry and the Sent copy separately (`internal/web/handlers/
compose.go:590-660`). A failure part-way returns 500 with earlier writes
kept, and a retry writes them again.

**Acceptance Criteria:**
- [ ] The compose form carries a send ID generated when the form renders.
- [ ] Every envelope ID, queue ID and Sent GUID is derived from the send ID,
      and each write is skipped when its object already exists.
- [ ] A test injects a failure after the first local envelope, retries with
      the same send ID, and finds exactly one copy per recipient, one queue
      entry and one Sent copy.
- [ ] A second, separate send of the same content is not suppressed.

### US-011: Calendar export keeps time zones
**Source:** 8.2 (21.6).

**Description:** `ExportCalendar` (`internal/web/handlers/calendar.go:618`)
copies only VEVENT children out of `ical_raw`, dropping the VTIMEZONEs that
import kept. Events without raw data get TZID parameters with no VTIMEZONE.

**Acceptance Criteria:**
- [ ] Each VTIMEZONE referenced by an exported event appears once in the
      export.
- [ ] Events without `ical_raw` export timed values in UTC (`Z`), so no TZID
      lacks a definition.
- [ ] Export, then import, of a zoned event gives the same instants.

### US-012: All-day reminders and the month grid use the user's zone
**Source:** 8.2 (21.4).

**Description:** reminders (`internal/agent/reminders.go:94`) fire relative
to the stored start, which for all-day events is 00:00 UTC; in New York that
is the evening before. MonthView (`calendar.go:103-108`) queries local month
bounds, so all-day events on the first or last day can miss the grid in some
zones.

**Acceptance Criteria:**
- [ ] An all-day reminder fires relative to midnight of the event date in
      the owner's preference time zone (`Preferences.Timezone`, default UTC).
- [ ] MonthView shows an all-day event on the 1st and on the last day of the
      month for users in UTC+14, UTC and UTC-12.
- [ ] Timed events are unchanged.

### US-013: Compose messages are translated
**Source:** 8.2 (21.1).

**Description:** compose's flash and error texts (`compose.go`) are
hard-coded English; only the quota paths use `i18n.T`.

**Acceptance Criteria:**
- [ ] Every user-visible string in `compose.go` goes through `i18n.T` with
      entries in en, de, es and fr; the catalog coverage test passes.
- [ ] A second grep finds no hard-coded user-facing English left in the file.

### Phase C: Reliability and supply chain

Release note: bad cluster and port configuration is refused at validation,
the known flaky tests are fixed, and the toolchain and pinned dependencies
are current.

### US-014: Config.Validate refuses bad binds and ports
**Source:** 8.2 (22.4, 22.8).

**Acceptance Criteria:**
- [ ] Role server with a wildcard `server.bind_addr`, no `advertise_addr`
      and no `cluster.bind_addr` fails `Validate`, naming the keys (today it
      fails only at startup).
- [ ] Every listener port is 1 to 65535; the raft, gossip, peer and control
      ports differ from each other and from the web, SMTP, IMAP and POP3
      ports. Errors name both keys.
- [ ] The production config (`advertise_addr = "127.0.0.1"`, wildcard bind)
      still validates.

### US-015: The known flaky tests are fixed at the cause
**Source:** 8.2 (19.1, 19.11, 20.4, 20.5).

**Acceptance Criteria:**
- [ ] No test in `internal/agent` binds the default gossip port 8301 or the
      dev IMAP port 1143.
- [ ] `TestReadyImpliesWebBound` no longer releases a port and binds it
      again.
- [ ] `test/integration/smtp_test.go` waits on a condition instead of
      sleeping 500 ms.
- [ ] Each changed test passes 20 runs under `-race`.

### US-016: Toolchain and pinned dependencies are current
**Source:** 8.2 (20.2).

**Acceptance Criteria:**
- [ ] Go and the Dockerfile base images are on the latest stable release,
      checked at implementation time.
- [ ] Each pinned action is on its latest major, still SHA-pinned.
- [ ] `hashicorp/raft`, `memberlist` and `go-metrics` are upgraded together
      if a newer raft exists; the Raft integration tests pass.
- [ ] The deploy template's own bongo image tag is the current release.

### US-017: The image-pin check reads YAML, not lines
**Source:** 8.2 (20.1).

**Acceptance Criteria:**
- [ ] The check parses the compose file as YAML, so a quoted key and a
      flow-style mapping are checked.
- [ ] Fixture tests in CI cover: pinned, unpinned, quoted key, flow style,
      the bongo exemption, a missing file and a file with no images.

## 4. Functional Requirements

FR-1 to FR-17 are the acceptance criteria of US-001 to US-017.

## 5. Non-Goals (Out of Scope)

Everything else in section 8.2 of `prd-hardening-v0.9.md`, in particular:
- Multi-node items: agent mode end to end, blob catch-up, version-aware
  rolling upgrades, compare-and-swap for profile writes, the per-node share
  limiter, peer-address and peer-port details.
- The CLI's own `--session-secret` against a dev node, agent hot reload.
- Address and alias legacy-identity conflicts (19.2, 19.3, 19.4).
- Calendar import duplicates on re-import, the unshare UI.
- certs-dumper tag, Docker Hub rate limit in the certs job.

## 6. Technical Considerations

### 6.1 Deployment impact

Only US-001 changes how production is operated: the CLI's default address
moves to the control listener. Before deploying, the infra repo must be
checked for scripts or jobs that call `bongo-go` with `--http-addr` pointing
at the web port. No other story changes a config key production sets.

### 6.2 Story notes

- US-009 changes the shape of SMTP envelopes. Bayes training and antispam
  run once per copy, which costs CPU in proportion to local recipients.
- US-010's send ID must be per form render, not per content, so a user who
  deliberately sends the same text twice gets two sends.

### 6.3 Process

- One PR per batch of related stories; Phase A merges first.
- Each story's first test reproduces the gap (red) before the fix.
- Each negative assertion is mutation-checked by the implementer and the
  reviewer.
- `make lint`, gosec, govulncheck, the image-pin check and `make test-all`
  pass before each merge.

## 7. Success Metrics

- All stories done, each with a test that fails on `7becc69`.
- No new TASKS.md rows apart from gaps opened by a change in this release.
- gosec medium findings do not increase.

## 8. Open Questions

### 8.1 Decisions (operator, 2026-10-24)

1. Scope: the 17 items in section 3, all three phases, one release.
2. Multi-node items stay deferred until a multi-node cluster is planned.
3. US-003 allows SIZE to be underestimated by a tenth plus 1 KiB. RFC 1870
   calls SIZE an estimate and Postfix does not enforce it in DATA, so a
   strict limit risked refusing legitimate mail.

### 8.2 Candidates for the next release

Findings from reviews during this release go here.

From the Phase 23 batch 1 reviews (2026-10-24):

- The plaintext `HTTPAddr` fallbacks in `joinTarget` (`internal/agent/
  agent.go`) and `BlobSyncer.endpoint` (`internal/cluster/blobsync.go`) now
  reach a 404 on the web port; they carry a comment and can be deleted. (23.1)
- The CLI does not read `server.control_addr`; an operator who changes it
  must pass `--http-addr`. (23.1)
- `fakeSplitDeliverer.StoreBlob` (`internal/smtp/relay_policy_test.go`)
  discards the `io.Copy` error, so a test built on it hides read-side
  refusals. (23.3)
- Rate-limit units are spent before a SIZE overrun is refused. (23.3)
- Release notes for v0.12.0:
  - The `/internal/*` control routes are served only on
    `server.control_addr` (default `127.0.0.1:8081`); the web port answers
    404. The `bongo-go` CLI defaults to that address; anything calling it
    with `--http-addr` set to the web port must change. (23.1)
  - Forwards of mail with SPF fail and DMARC pass get the no-relay sender
    and no bounce. Forwards queued before the upgrade keep their SRS0 sender
    until they drain or the SRS window expires. (23.2)
  - A message more than a tenth plus 1 KiB over its declared SIZE gets
    `552 5.3.4`. (23.3)
