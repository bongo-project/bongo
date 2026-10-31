# PRD: Mail delivery and rules v0.14.0

## 1. Introduction / Overview

The feature-parity audit of the original Bongo
(`prds/research/parity-audit-original-bongo.md`, rows cited as #N) lists
mail-delivery and filtering features that this reimplementation lacks or only partly has.
This PRD fixes the scope of v0.14.0 "Mail delivery and rules": the RFC 5321
`postmaster` mailbox, implicit-TLS submission, vacation replies, richer
rules, domain aliasing, DSN, queue and disk-space controls, Reply to List,
Archive, a `sendmail` command for programs on the host, and
CLI commands that list users, domains and aliases.

Every story was checked against `main` at `6725448` on 2026-10-26. Where the
code disagrees with the audit, the story says so; section 8.2 lists the
corrections together.

**The scope rule.** The in-scope list is section 3. Anything a review finds
during this release goes into section 8.2, not into TASKS.md, unless the
change under review introduced it.

| Phase | Theme | Stories |
|-------|-------|---------|
| A | Postmaster and submission | US-001 to US-003 |
| B | Vacation and rules | US-004 to US-007 |
| C | Queue, DSN and storage | US-008 to US-010, US-012 |
| D | Web mail views | US-014, US-015 |
| E | Local submission and operator CLI | US-017, US-018 |

TASKS rows are assigned by `norman import`. Phase A merges first: US-002
needs US-001, and US-017 prefers US-003. Phase B's US-005 lands before US-006
and US-007, which extend the same proto and form. In Phase C, US-008 lands
before US-010 (the delay warning uses the new queue timing).

Story numbers are kept from the first draft. US-011 (DSN relay) and US-016
(mail views) moved to v0.15, where they are US-018 and US-019; US-013
(over-quota text) is deferred (section 8.1, decision 8).

### Size key

S: one focused session, one package plus a template. M: two or three
packages, a proto or schema change, or a new form. L: a new subsystem or a
cross-cutting change. This PRD has no L story.

## 2. Goals

- `postmaster@` exists on every hosted domain, so replies to Bongo's own
  bounces and DMARC aggregate reports are delivered.
- Mail clients can submit over implicit TLS on port 465 (RFC 8314).
- A user can set an out-of-office reply that never answers lists, bulk mail,
  other robots, spam or forged senders, and answers each sender at most once
  per interval (RFC 3834).
- A rule can match any of its conditions, run several actions, and match on
  the MIME type of a part; the actions that are accepted today but do
  nothing (flag, mark read, copy) work.
- Senders can ask for and get standard delivery status notifications
  (RFC 3461, RFC 3464), including delay warnings.
- The server refuses new work with a temporary error before its disk fills,
  and the operator can tune queue lifetime, retry interval and concurrency.
- Users can archive and unarchive, and reply to a mailing list or to the
  Reply-To address.
- Programs on the host can send mail through a `sendmail`-compatible command.
- Operators can list users, domains and aliases from the command line.

## 3. User Stories

### Phase A: Postmaster and submission

Release note: `postmaster@` now reaches a person on every hosted domain, and
the server listens for implicit-TLS submission on port 465.

### US-001: `postmaster@` resolves on every hosted domain
**Source:** audit #33. **Size:** S.

**What exists:** the audit is correct. `postmaster` appears only as a sender
(`internal/mail/bounce.go:309`, `forward_verify.go:172`,
`address_verify.go:755`) and in the DMARC advice
(`internal/web/handlers/dns_guide.go:58`, `rua=mailto:postmaster@`).
`RecipientResolver.resolve` (`internal/store/recipient_resolver.go:95-203`)
has no special case, so `postmaster@` resolves only through an explicit
alias or a catch-all. RFC 5321 section 4.5.1 requires the mailbox.

**Design:** a resolver tier, not an alias written at domain creation. A
written alias would need a target that usually does not exist when the
domain is created, and would not cover existing or boot-seeded domains
(`Agent.ensureConfiguredDomain`, `agent.go:3055-3100`). The tier runs after
the address, legacy alias, user and identity tiers and before the catch-all:
for local part `postmaster` (case-insensitive) on a hosted domain, it returns
the domain owner (`DomainAdminStore.GetOwner`), else the account named by a
new key `smtp.postmaster_account` (a full address of a local account), else
nothing.

**Acceptance Criteria:**
- [ ] RCPT `<Postmaster@example.com>` on a hosted domain with owner alice is
      accepted and delivered to alice's INBOX.
- [ ] With no owner and `smtp.postmaster_account = "admin@example.org"`, the
      message goes to that account. With neither, RCPT gets
      `550 5.1.1`, and startup logs one warning naming the domains that have
      no postmaster target.
- [ ] An explicit address record or alias for `postmaster@example.com` takes
      precedence over the tier (a test with both).
- [ ] The tier wins over the domain's catch-all.
- [ ] `postmaster@` on a domain that is not hosted is not resolved (relay
      rules are unchanged).
- [ ] The resolver maps a bare `postmaster` (no domain) to
      `smtp.postmaster_account`, or to a not-local error if unset (unit test).
      Over SMTP, go-smtp v0.24.0 refuses `RCPT TO:<postmaster>` with
      `501 5.5.2` at the parser, before Bongo sees it; recorded in
      SECURITY.md and in 8.2 as an upstream candidate. (Amended 2026-10-31
      by the 28.1 plan review.)
- [ ] The tier applies only when it has a live target: the owner first,
      then `smtp.postmaster_account`. A disabled or deleted owner falls
      through to the account. With no live target, resolution continues to
      the catch-all, and gives 550 5.1.1 only when there is none.
      `ResolveAccount` (strict) never uses the tier. (Added 2026-10-31.)
- [ ] `Config.Validate` refuses a `smtp.postmaster_account` that is not a
      syntactically valid address; a valid address with no account logs a
      warning at startup rather than failing (accounts are created after
      startup).

**Security:** the tier must not become an existence oracle for other local
parts; it matches the exact local part only. **Cluster:** read path only; no
new key or command. A node on the old binary keeps refusing `postmaster@`
during a mixed window, which is harmless.

### US-002: Copy bounces to the postmaster
**Source:** audit #26. **Size:** S. **Needs:** US-001.

**What exists:** the audit is correct. `BounceGenerator.GenerateAndDeliver`
(`internal/mail/bounce.go:76-146`) delivers each bounce to the original
sender only, through the local inbound pipeline. The deliver stage drops a
recipient it cannot resolve (`deliver.go:538-541`), so a copy can only land
once US-001 makes `postmaster@` resolve.

**Acceptance Criteria:**
- [ ] A new key `queue.bounce_copy_postmaster` (default false, hot-reloadable
      or restart class decided and recorded in `config/diff.go`).
- [ ] When true, each bounce generated for a sender on a hosted domain is
      also delivered to `postmaster@` that domain; for any other sender, to
      `smtp.postmaster_account`.
- [ ] The copy carries the delivery-status part and the original headers
      only (`text/rfc822-headers`), never the original body, whatever the
      sender's RET (US-010).
- [ ] No copy is made of a bounce that is itself suppressed (null sender,
      unauthenticated forward: `Worker.generateBounce`,
      `internal/queue/worker.go:351-370`).
- [ ] A failure to deliver the copy is logged and does not affect the bounce
      to the sender (the comment says the copy is best-effort).

### US-003: Implicit-TLS submission on port 465
**Source:** audit #4. **Size:** S.

**What exists:** the audit is correct. `SMTPConfig` has `port` and
`submission_port` only (`internal/config/config.go:232-259`). go-smtp v0.24.0
treats any `*tls.Conn` as TLS for AUTH, so wrapping the listener with
`tls.NewListener`, as IMAPS does (`internal/imap/server.go:170-195`), is
enough on the protocol side. One trap the audit missed: a session counts as
submission only when its local port equals `Backend.subPort`
(`internal/smtp/server.go:66, 257-263`), and AUTH, the authentication
requirement in `Mail`, send-as checks and the outbound limiters all key off
that. A third listener must be marked as submission explicitly.

**Acceptance Criteria:**
- [ ] A new key `smtp.submissions_port`, default 465, dev port 2465; 0
      disables the listener. It is included in the port-collision check
      (`internal/config/ports.go:108-161`) and is a restart key.
- [ ] The listener stacks `LimitListener`, PROXY protocol (when
      `smtp.proxy_protocol` is on, honoured only from
      `server.trusted_proxies`) and then TLS, matching the 587 listener, and
      uses the submission TLS configuration (TLS 1.2 floor). The per-IP cap
      is enforced per session in `Backend.NewSession`, as on 25 and 587,
      after PROXY has supplied the real client IP. (Amended 2026-10-31 by the
      28.3 plan review: a listener-level per-IP limit before PROXY would
      count the proxy's address.)
- [ ] The 465 listener uses the `smtp.tls` certificate; in dev a generated
      self-signed certificate. In other roles with no `smtp.tls`
      certificate it is not started and one warning is logged; it never
      serves plaintext. A configured certificate that fails to load fails
      startup. (Added 2026-10-31.)
- [ ] Submission status on 465 is set per listener and does not depend on
      the local port. (Added 2026-10-31.)
- [ ] A session on the 465 listener is a submission session: unauthenticated
      MAIL gets 530, AUTH PLAIN and LOGIN are offered immediately, send-as
      and rate limits apply exactly as on 587 (the same tests run against
      both listeners).
- [ ] STARTTLS is not offered on 465.
- [ ] Mozilla autoconfig and Autodiscover list 465 (SSL) first and 587
      (STARTTLS) second, using the effective ports (dev ports in dev mode).
      The Apple profile carries one outgoing server: 465 with SSL when the
      465 listener is enabled, 587 otherwise. When 465 is not served it is
      omitted everywhere. (Amended 2026-10-31.)
- [ ] The DNS guide adds `_submissions._tcp` 465 next to `_submission._tcp`.
- [ ] A bind failure on 465 fails startup before ready.

**Deployment:** the production compose file lives in the infra repository and
must publish 465 before the upgrade, or the operator sets
`smtp.submissions_port = 0` until it does. Release note required.

### Phase B: Vacation and rules

Release note: users can set an out-of-office reply. Rules can match any
condition, run several actions and test MIME types; flag, mark read and copy
now do what they say.

### US-004: Vacation reply with RFC 3834 loop protection
**Source:** audit #57. **Size:** M.

**What exists:** nothing; the audit is correct. No code reads
`Auto-Submitted`, `Precedence` or `List-*`; only the bounce writes
`Auto-Submitted: auto-replied` (`bounce.go:316`). The hook is the
per-recipient loop in the deliver stage where account forwarding is decided
(`internal/pipeline/deliver/deliver.go:541-563`).

**Design:**
- `UserPreferences` gains additive fields: `vacation_enabled` (15),
  `vacation_subject` (16), `vacation_body` (17, plain text, at most 4 KiB),
  `vacation_start` (18) and `vacation_end` (19) as unix seconds (0 means
  open), `vacation_interval_days` (20, 1 to 30, default 7).
- Reply tracking: `/user/{user_id}/vacation/sent/{hex(sha256(lower(sender)))}`
  holding 8 bytes of big-endian unix seconds. The leader's orphan sweep
  deletes entries older than 30 days; disabling vacation deletes the user's
  entries. The key is added to the CLAUDE.md key table in the same change.
- Replies to an external sender go through the outbound queue with a new
  `OUTBOUND_ORIGIN_AUTOREPLY` (additive enum value, DKIM-signed). Replies to
  a local sender go through the local pipeline, as bounces do.

**Acceptance Criteria:**
- [ ] `/settings/vacation` (GET form, POST save) with CSRF, translated in en,
      de, es and fr. Saving with an end before the start is refused with a
      translated message. A save that fails to persist fails the request.
- [ ] A reply is sent only when all of these hold, each covered by a table
      test case that would otherwise reply:
      vacation is enabled and now is within the window; the envelope sender
      is not empty and its local part is not `mailer-daemon`, `postmaster`,
      `owner-*`, `*-request` or `noreply`/`no-reply`; there is no
      `Auto-Submitted` header other than `no`; there is no `Precedence` of
      `bulk`, `list` or `junk`; there is no `List-Id`, `List-Post` or
      `List-Unsubscribe`; one of the user's own addresses (primary, alias or
      identity) appears in To or Cc; the message was not classified as spam,
      quarantined, deleted by a rule or filed to Junk or Trash; SPF passed
      for the envelope sender's domain (a forged envelope sender gets no
      reply, for the reason v0.12 US-002 gives for bounces).
- [ ] The same sender gets at most one reply per `vacation_interval_days`
      (a second message within the interval: no reply; after it: a reply).
- [ ] The tracking key is written before the reply is enqueued. If that
      write fails, no reply is sent and the failure is logged (failing
      closed prevents loops). If the enqueue fails after the write, one
      reply is lost and logged; the comment says so.
- [ ] The reply has envelope sender `<>`, `From:` the address the message
      was sent to (when it is one of the user's addresses that may send,
      else the primary), `Auto-Submitted: auto-replied`, `In-Reply-To` and
      `References` from the original, and `Subject:` the configured subject
      or "Auto: " plus the original subject, with no CR or LF in any header.
- [ ] At most 100 vacation replies per user per hour (a per-node limiter,
      like the forward limiter); beyond it replies are skipped and counted
      in a metric.
- [ ] Rules that forward or account forwarding still apply; vacation does
      not change delivery.

**Security:** header values from the original are never copied into the
reply's headers except `Message-ID` into `In-Reply-To`/`References`, and only
after validating it as a msg-id. **Cluster:** the new preference fields are
additive; an old node delivering during a mixed window sends no reply. Check
that the old queue worker signs and relays an unknown origin value (proto3
keeps the number; `shouldSign` excludes FORWARD only) and record the result
in the release notes.

### US-005: Several actions per rule, and the existing actions work
**Source:** audit #53, and #56 (corrected). **Size:** M.

**What exists:** the audit is correct that a rule has one `FilterAction`
(`proto/bongo/v1/rules.proto`, field 6), but row #56 ("move, copy, delete,
forward, stop: BUILT") is wrong in four ways:
- Only the first enabled matching rule ever fires: the pipeline calls
  `Engine.Evaluate` (`internal/pipeline/rules/rules.go:122`), not
  `EvaluateWithStop`, so `stop_processing` has no effect.
- `flag` and `mark_read` are recorded in the stage reasons
  (`rules.go:139-142`) and nothing applies them.
- `copy` behaves as `move`: it sets the single `TargetMailbox`
  (`rules.go:134-137`).
- A rule move overrides the Junk routing of spam.

**Design:** `FilterRule` gains `repeated FilterAction actions = 10`; field 6
stays for compatibility. On read, an empty `actions` means `[action]`. On
write, both are set (`action` = first of `actions`), so an older node reads
something sensible during a mixed window. All enabled rules run in priority
order until one with `stop_processing` matches. To keep today's behaviour
for existing rules (only the first match fires), a one-time, idempotent
leader migration sets `stop_processing = true` on every rule written before
this release, guarded by the marker `/system/migrations/rules-first-match-v1`.
Each rule is one PUT; a crash part-way is resumed by the next run (the marker
is written last).

**Acceptance Criteria:**
- [ ] A rule holds 1 to 10 actions; the form adds and removes action rows;
      the server refuses 0 or more than 10 with a translated message.
- [ ] Terminal actions (move, delete) and non-terminal actions (copy, flag,
      mark read, tag, forward) combine as follows: the first terminal action
      reached wins and later terminal actions are ignored; non-terminal
      actions from every matched rule accumulate.
- [ ] `flag` sets `\Flagged` and `mark_read` sets `\Seen` on the delivered
      copy (IMAP FETCH FLAGS shows them).
- [ ] `copy` to Folder delivers one copy to Folder and one to the normal
      destination; both share the blob (no second blob write).
- [ ] Spam classified for Junk stays in Junk even when a rule moves it,
      unless the rule's target is Junk or Trash.
- [ ] Two matching rules A (priority 1) and B (priority 2) both apply when A
      has `stop_processing` false; only A applies when it is true.
- [ ] After the migration, every pre-existing rule has
      `stop_processing = true`, and a message that matched two old rules is
      handled exactly as before the upgrade (a test with fixtures written in
      the old shape).
- [ ] The rule test page (`testRuleAgainstMail`) shows every action that
      would run.

**Cluster:** the migration runs only on the leader. Whether every member
runs the new version cannot be checked today (see 6.3), but the migration is
idempotent and writes only additive fields, and an old node ignores
`stop_processing` anyway (it applies the first match), so running it during
a mixed window is safe.

### US-006: Match any condition (OR)
**Source:** audit #52. **Size:** S. **Needs:** US-005.

**What exists:** AND only (`matchAllConditions`,
`internal/rules/engine.go:49-59`); the audit is correct.

**Acceptance Criteria:**
- [ ] `FilterRule.match` (field 11): `"all"` (default, also for the empty
      string) or `"any"`. The form offers "all of" / "any of".
- [ ] With `any`, a rule with conditions from=a and subject=b matches a
      message meeting only one of them; with `all`, it does not.
- [ ] A rule with no conditions never matches in either mode.
- [ ] Any other value is refused on save.

### US-007: MIME-type condition and header-condition fixes
**Source:** audit #54. **Size:** S. **Needs:** US-005.

**What exists:** only `has_attachment`, from enmime's attachment count; MIME
types are not kept (`rules.go` `parseMessage`). Two further gaps: the
`header:` lookup is an exact, case-sensitive map key (`engine.go:88-91`), so
`header:List-ID` misses `List-Id`; and the form has no header option while
`parseRuleForm` (`internal/web/handlers/rules.go:298-337`) accepts any field
string.

**Acceptance Criteria:**
- [ ] A condition field `mime_type` with operators equals, contains and
      matches, tested against the lowercased `type/subtype` (no parameters)
      of every leaf part, including nested multiparts and inline parts; the
      condition holds if any part matches. A message with a nested
      `application/pdf` attachment matches `mime_type equals
      application/pdf`.
- [ ] `header:Name` matches case-insensitively on the header name.
- [ ] The form offers "Header" with a name input; `parseRuleForm` refuses a
      field outside the known set and a header name that is not an RFC 5322
      field name.
- [ ] Parsing is bounded: at most 100 parts are inspected per message.

### Phase C: Queue, DSN and storage

Release note: senders can request delivery notifications; delay warnings,
queue lifetime, retry interval and concurrency are configurable; the server
refuses new mail with a temporary error when its disk is nearly full.

### US-008: Tunable queue lifetime, retry interval and concurrency
**Source:** audit #25 (corrected). **Size:** S.

**What exists:** the audit cites the wrong keys. `config.go:998-1017` is
`pipeline.workers` (the inbound pipeline) and `queue.poll_interval`. Outbound
concurrency is a constant (`concurrency = 5`, `internal/queue/worker.go:22`),
as are `maxAttempts = 10` and `batchSize = 100`. `backoff`
(`internal/queue/store.go:343-355`) doubles from 2 minutes; the 24-hour cap
only applies after attempt 10, which is never reached, so a message is
bounced about 17 hours after its first attempt. The comment at
`worker.go:294` ("seven days") does not match.

**Acceptance Criteria:**
- [ ] New keys: `queue.max_lifetime` (default `"5d"`), `queue.max_retry_interval`
      (default `"1h"`), `queue.concurrency` (default 5, 1 to 64). Restart
      class.
- [ ] Retries back off from 2 minutes, doubling, capped at
      `max_retry_interval`; a message is bounced when the next attempt would
      fall after `created_at + max_lifetime` (a clock-injected test walks a
      message to its bounce at the configured lifetime).
- [ ] `maxAttempts` is removed as the bounce criterion; attempts are still
      counted and shown in `/admin/queue`.
- [ ] Messages queued before the upgrade are judged by their `created_at`
      (field 7, already present).
- [ ] The stale comment is corrected.

### US-009: Refuse new work when the disk is nearly full
**Source:** audit #30. **Size:** S.

**What exists:** nothing; the audit is correct. Everything lives under
`storage.data_dir` (Pebble, blobs, metadata, spool), and Raft under its own
directory (`config.go:1232`).

**Acceptance Criteria:**
- [ ] A key `storage.min_free_mb` (default 1024; 0 disables). A checker
      samples free space (`statfs`) of the data directory and the Raft
      directory every 10 seconds and caches the result.
- [ ] Below the threshold: SMTP MAIL on MX and submission answers
      `452 4.3.1 Insufficient system storage`; IMAP APPEND answers
      `NO [UNAVAILABLE]`; web compose, import and upload answer 507 with a
      translated message; CalDAV and CardDAV PUT answer 507.
- [ ] Raft apply, the outbound queue worker, blob GC and reads are never
      refused (they free space or must not diverge).
- [ ] Crossing the threshold logs once per transition (low, then
      recovered), sets a gauge `bongo_storage_low` and shows a banner on
      `/admin`.
- [ ] Tests inject the free-space function; no test fills a disk.

### US-010: DSN parameters accepted and honoured
**Source:** audit #6. **Size:** M. **Needs:** US-008.

**What exists:** the audit is correct and understated. Bongo's bounce is
already RFC 3464 (`multipart/report`, `message/delivery-status`,
`bounce.go:295-400`), but go-smtp's `EnableDSN` is never set, so MAIL
`RET`/`ENVID` and RCPT `NOTIFY`/`ORCPT` are refused today. Only failure
reports exist; there are no delay or success reports.

**Acceptance Criteria:**
- [ ] MX and submission advertise `DSN`. `OutboundMessage` gains additive
      fields for RET, ENVID, and per-recipient NOTIFY and ORCPT; the spool
      envelope carries the same for local delivery.
- [ ] `NOTIFY=NEVER` suppresses the failure report for that recipient.
- [ ] `RET=FULL` attaches the original as `message/rfc822` (truncated to
      the headers when it exceeds 1 MiB); `RET=HDRS` and no RET attach
      `text/rfc822-headers`, as today.
- [ ] `ENVID` appears as `Original-Envelope-Id` and `ORCPT` as
      `Original-Recipient` in the report; both are validated as xtext and
      refused with `501` when malformed.
- [ ] `NOTIFY` including `DELAY` produces one `Action: delayed` report
      after `queue.delay_warning_after` (default `"4h"`); without it, no
      delay report is sent.
- [ ] `NOTIFY=SUCCESS` on a recipient delivered locally produces one
      `Action: delivered` report to the envelope sender.
- [ ] Reports go only to senders who would get a bounce today (the null
      sender and unauthenticated-forward suppression is unchanged).

**Security:** a success report to a forged sender is backscatter; the rule
above applies to every report type, with a test per type.

### US-012: Domain aliasing
**Source:** audit #35. **Size:** M.

**What exists:** nothing; the audit is correct. A domain is the key
`/system/domain/{d}` (`"enabled"`/`"disabled"`) plus a SQLite row;
`DomainHosted` checks only that the key exists.

**Design:** `/system/domain/{alias}/alias_of` holds the target domain. An
alias domain is also a hosted domain (its own `/system/domain/{alias}` key),
so MX acceptance, DKIM key generation and the DNS guide work as for any
domain. Resolution: when no tier resolves `local@alias` itself, the resolver
retries with `local@target`. Creating an alias domain is two Raft writes;
`alias_of` is written first, so a failure after it leaves an inert key on a
domain that is not hosted (MX still refuses). This partial-failure order is
documented in the handler instead of adding a new command type.

**Acceptance Criteria:**
- [ ] `/admin/domains` lets a global admin create an alias domain for an
      existing domain; domain admins of the target can see it.
- [ ] With `example.net` aliased to `example.com`, mail to
      `alice@example.net` reaches alice; an explicit address on
      `example.net` wins over the rewrite; an unknown local part gets the
      target domain's catch-all if any, else 550.
- [ ] A chain (an alias of an alias) and an alias of itself are refused.
- [ ] Deleting a domain that has aliases is refused with a message listing
      them; deleting an alias domain removes `alias_of` with the rest of its
      configuration (`PurgeDomainConfig`).
- [ ] Sending as `alice@example.net` still requires a send-as identity;
      aliasing grants receipt only.
- [ ] The rewrite is applied once; quota is charged to the target domain's
      owner as for any delivery to alice.

### Phase D: Web mail views

Release note: Archive and Unarchive, and Reply to List.

### US-014: Archive and Unarchive
**Source:** audit #87 (corrected). **Size:** S.

**What exists:** less than the audit implies. Archive is only a recognised
name (`isSystemMailbox`, `templates/components.templ:33`; a sidebar icon,
`handlers/sidebar.go:63`). It is not a default mailbox
(`store.DefaultMailboxes`, `mailbox.go:35`), IMAP LIST sets no `\Archive`
attribute (`imap/backend.go:529-535`), `BulkAction` has no archive action
(`mail.go:705-766`), and the mobile swipe commented as "archive" moves to
Trash (`static/js/bongo.js:721-726`). v0.13 US-006 (TASKS 27.6) adds an
Archive icon button only.

**Acceptance Criteria:**
- [ ] `POST /mail/{mailbox}/{uid}/archive` and a bulk `archive` action move
      the message to `Archive`, creating it if absent, with CSRF; a failed
      move fails the request.
- [ ] In the Archive mailbox, the button reads Unarchive and moves the
      message to INBOX.
- [ ] IMAP LIST returns `\Archive` on the Archive mailbox (RFC 6154).
- [ ] The keyboard shortcut `e` archives the open message (and is listed in
      the shortcut help).
- [ ] The mobile swipe archives the message (section 8.1, decision 9);
      in the Archive mailbox it unarchives. The swipe comment matches.

### US-015: Reply to List, and replies that respect Reply-To
**Source:** audit #90. **Size:** S.

**What exists:** `ComposeHandler.Reply` (`compose.go:212-269`) addresses the
reply to `meta.From` and ignores `Reply-To`. Reply-all removes only the
user's primary address, not aliases or identities. Nothing parses
`List-Post`.

**Acceptance Criteria:**
- [ ] `MailMeta` gains `reply_to` (21), `list_id` (22) and `list_post` (23),
      filled at delivery. For older messages the reply handler parses the
      headers from the blob when the fields are empty.
- [ ] Reply goes to `Reply-To` when present, else `From`.
- [ ] Reply-all removes every address of the user (primary, aliases,
      identities) from To and Cc.
- [ ] A "Reply to list" button appears when `List-Post` holds a `mailto:`
      URI; `GET /compose/reply-list/{mailbox}/{uid}` addresses the reply to
      that address only. `List-Post: NO` shows no button.
- [ ] A `List-Post` with any other scheme, or an invalid address, shows no
      button.

### Phase E: Local submission and operator CLI

### US-017: `bongo sendmail`
**Source:** audit #21. **Size:** M. **Prefers:** US-003.

**What exists:** nothing; the audit is correct. The CLI talks to the control
listener with requests signed by `web.session_secret`; no control route
accepts a message, and there is no Unix socket listener.

**Design decision (proposed; open question 1):** the command is an SMTP
submission client. It authenticates to the local server on 465 (or 587 with
STARTTLS) with an account's credentials read from a file. The alternatives
were rejected for these reasons:
- A signed control route would need `web.session_secret` on every host user
  or program that sends mail. That secret signs sessions, CSRF tokens and
  SRS; handing it to cron jobs and web applications is unacceptable.
- A Unix socket trusted through peer credentials needs a mapping from host
  UIDs to accounts and does not work across the container boundary
  production uses.
- Port 25 from a trusted network delivers locally only (external RCPT still
  needs AUTH, `server.go:582`).
Submission keeps every existing control (send-as policy, rate limits, DKIM,
quota) with no new trust boundary, works from other hosts too, and can later
use app passwords (`prds/research/prd-app-passwords.md`).

**Acceptance Criteria:**
- [ ] `bongo sendmail [flags] [recipient...]`, and the same behaviour when
      the binary is invoked through a link named `sendmail`.
- [ ] Flags: `-t` (recipients from To, Cc and Bcc, added to any on the
      command line), `-f sender` (envelope sender), `-F name` (full name for
      a generated From), `-i` and `-oi` (a line with a single dot does not end
      input); `-odi`, `-odb`, `-oem` and `-v` are accepted and ignored.
      `-bs` is not supported in this release and exits with `EX_USAGE`, as
      does any other flag.
- [ ] Without `-i`, input ends at EOF or a line holding a single dot. LF line
      endings are converted to CRLF. `Bcc` is removed before submission.
      `Date`, `Message-ID` and, when absent, `From` are added.
- [ ] Credentials come from `/etc/bongo/sendmail.conf` (or `--config`):
      server address, port, account and password. The command refuses to
      read the file if it is group- or world-readable and exits with
      `EX_CONFIG` (78). There is no password flag or environment variable.
- [ ] TLS certificate verification is on; `insecure_skip_verify` exists for
      a self-signed dev certificate and is refused when the server address
      is not loopback.
- [ ] Exit codes follow sysexits: 0 on acceptance, 64 usage, 65 data error,
      67 recipient refused, 69 server unavailable, 75 temporary failure, 77
      authentication refused, 78 configuration.
- [ ] A sender the account may not send as is refused by the server's
      send-as policy and reported with exit 77 and the server's reply text.
- [ ] The password is never logged or echoed, including in `-v` output.
- [ ] `docs/` gains a page on configuring cron and PHP's `sendmail_path`.

### US-018: List users, domains and aliases from the CLI
**Source:** production operations, 2026-10-31. **Size:** S.

**What exists:** `bongo user` has only `create`. There is no `domain` or
`alias` command. An operator on the host cannot list accounts, hosted domains
or aliases without the web UI. The CLI reaches the server through the control
listener (`server.control_addr`, default 127.0.0.1:8081) with requests signed
by `web.session_secret`, as `user create`, `address orphans` and
`mail dangling` already do.

**Acceptance Criteria:**
- [ ] `bongo user list` prints one line per account: email, display name,
      enabled or disabled, global admin, and the domains it administers.
      `--domain example.com` limits the list to that domain.
- [ ] `bongo domain list` prints one line per hosted domain: name, enabled or
      disabled, catch-all target if set, quota and usage, and whether a DKIM
      key exists.
- [ ] `bongo alias list` prints one line per alias: alias address and target.
      `--domain` limits it as for users.
- [ ] Each command accepts `--json` and then prints a JSON array with the same
      fields, for scripts.
- [ ] The data comes from new read-only GET routes on the control listener
      only, signed like the existing control routes; nothing is added to the
      public web router. An unsigned or wrongly signed request gets 401.
- [ ] Results are read from Pebble (the source of truth), not the per-node
      SQLite users table, so a follower lists the same accounts as the leader.
- [ ] No password hash, TOTP secret, recovery code, DKIM private key or
      session material appears in any output.
- [ ] A server that is not running or not reachable gives a one-line error
      naming the control address and a non-zero exit code.
- [ ] `docs/install.md` lists the commands under creating the first
      administrator.

## 4. Functional Requirements

FR-1 to FR-18 are the acceptance criteria of the stories in section 3
(US-011, US-013 and US-016 are not in this release). In every story:

- Every new cookie-authenticated mutating route sits behind the existing
  CSRF middleware and same-origin check.
- A persistence error on a user-visible write fails the request; a
  best-effort write says so in a comment and logs the failure with context.
- New user-facing text goes through `t(ctx, …)` or `i18n.T` with en, de, es
  and fr entries, followed by a second grep for hard-coded strings.
- New config keys get a default, `Config.Validate` checks and a class in
  `internal/config/diff.go`.
- No message content, password or token appears in logs.

## 5. Non-Goals (Out of Scope)

- Sieve and ManageSieve. Rules stay Bongo's own format.
- A rule action that bounces or rejects to the sender (#58): backscatter.
- `bongo sendmail -bs` (an SMTP dialogue on standard input); a candidate
  for a later release.
- Copying the whole bounced message to the postmaster.
- Obsolete audit rows that relate to this release, not to be built:
  - ETRN (#7): a dial-up-era queue start.
  - VRFY and EXPN (#8): address harvesting.
  - Restricting remote delivery to time windows (#29): bandwidth scheduling
    for dial-up links.
  - Bouncing infected mail to the sender and postmaster (#45): backscatter;
    Bongo quarantines instead.
  - Username mapping modes (#36): accounts log in by full address.
  - SMTP-after-POP (#114): replaced by SMTP AUTH.
- Per-user storage quota enforcement (`UserProfile.quota_bytes`); it is a
  separate decision (see 8.2).

## 6. Technical Considerations

### 6.1 Security

The server is internet-facing. In addition to section 4:
- No new route goes on `/internal/*` of the public web router; US-017
  deliberately uses the public submission ports with authentication
  instead. The read-only US-018 routes (`/internal/users`, `/domains`,
  `/aliases`) live on the loopback control listener only, signed like the
  existing control routes, as that story requires.
- Every generated message (vacation, DSN, bounce copy) is built with header
  values that are validated or fixed, never copied raw from the triggering
  message.
- Every new kind of automatic mail (vacation, delay and success reports) is
  sent only where a bounce would be, so none of them becomes a backscatter
  source.

### 6.2 Data and Raft

- New keys: `/user/{id}/vacation/sent/{hash}` (US-004),
  `/system/domain/{d}/alias_of` (US-012) and
  `/system/migrations/rules-first-match-v1` (US-005). Each is a plain PUT or
  DELETE; the release adds no new composite command type.
- Multi-key writes: US-012 documents its write order and partial-failure
  result. US-005's migration is idempotent and resumable.
- All proto changes are additive fields or enum values.

### 6.3 Mixed-version clusters

Nodes do not publish their version and nothing gates new behaviour on it
(`prd-hardening-v0.9.md` section 8.2, "Version-aware rolling upgrades",
carried as a non-goal in `prd-hardening-v0.12.md` section 5). This release is
reviewed by hand for rolling compatibility: additive proto fields, no new
command types, no change to peer routes. Behaviour during a mixed window: an
old node does not route `postmaster@`, send vacation replies, honour new
rule fields beyond `action`, or offer DSN; no data diverges. Production runs
one node, so none of this affects it.

### 6.4 Deployment

US-003 needs port 465 published in the infra repository's compose file, or
`smtp.submissions_port = 0`. US-009's default threshold (1 GiB) must be
checked against production's free space before deploying.

## 7. Success Metrics

- Every story done, each with a test that fails on `6725448`.
- A DMARC aggregate report sent to `postmaster@` a hosted domain is
  delivered.
- No vacation reply is generated for any message in the existing
  list-mail and spam test fixtures.
- gosec medium findings do not increase; `make test-all` stays green.

## 8. Open Questions

None. The questions raised while drafting are answered in 8.1.

### 8.1 Decisions

1. (operator, 2026-10-26) Scope as listed in section 3, including a
   `bongo sendmail` subcommand with the common sendmail and LSB flags.
2. (operator, 2026-10-26) POP3 SASL (#19) and IMAP PROXYAUTH (#16) are
   dropped; neither is in this or any planned release.
3. (operator, 2026-10-27) `bongo sendmail` authenticates as a mail
   client over submission, reading its credentials from a file only it can
   read (mode 0600). The signed control route is rejected.
4. (operator, 2026-10-27) Port 465 (implicit TLS) is on by default.
   Production must publish it through the infra repository before the
   release is deployed.
5. (operator, 2026-10-27) The outbound queue lifetime is 5 days, replacing
   the accidental ~17 hours.
6. (operator, 2026-10-27) New rules continue after a match by default;
   existing rules keep first-match behaviour through a one-time migration.
7. (operator, 2026-10-27) `abuse@` resolves per domain the same way as
   `postmaster@`.
8. (operator, 2026-10-27) The proposed split is accepted: US-016 (views)
   and US-011 (DSN relay) move to v0.15, and US-013 (custom over-quota
   text) is deferred. The defects in rules and queue lifetime found by this
   PRD are fixed first. (A mention of greylisting here was a leftover and
   was removed on 2026-10-31; no greylisting work is in this release.)
9. (operator, 2026-10-31) The mobile swipe archives rather than deletes
   (US-014).
10. (operator, 2026-10-31) US-018, CLI listing of users, domains and
    aliases, is added after production operations found no way to list
    them.

### 8.2 Candidates for the next release

Audit corrections found while writing this PRD (2026-10-26):

- #56 is wrong: rules are first-match only, `flag` and `mark_read` do
  nothing, `copy` moves, and a rule overrides Junk routing. Fixed by US-005.
- #25 cites the wrong keys: outbound concurrency is not configurable, and
  the effective lifetime is about 17 hours, not seven days as the code
  comment says.
- #87 overstates: Archive is a recognised name only; nothing archives.
- #27 and #14: the enforced quota is the domain quota; per-user
  `quota_bytes` is editable in admin but not enforced, and the admin user
  page's "used" figure is always 0.
- #6 is understated: `RET`/`ENVID`/`NOTIFY` are refused outright today, and
  the relay cannot pass DSN or SIZE parameters.

Release size: the first draft had 17 stories (10 S, 7 M, no L), above the
~15-story guide. The moves below were accepted (8.1, decision 8) and US-018
was added, so the release has 15 stories (10 S, 5 M). Done:

| Story | Size | Outcome |
|-------|------|---------|
| US-016 views | M | Moved to v0.15 as US-019, next to the Summary page that uses its "to me" index. |
| US-011 DSN relay | M | Moved to v0.15 as US-018; US-010 stands without it. |
| US-013 quota text | S | Deferred; listed under other candidates below. |

Other candidates noted while checking the code:

- Configurable over-quota refusal text (former US-013, deferred; audit
  #27). RCPT answers a fixed `552 5.2.2 Mailbox quota exceeded`. Proposed
  then: a global `smtp.quota_message` and a per-domain override, printable
  ASCII, at most 200 characters, no CR or LF, status code kept at 5.2.2.
- Per-user storage quota enforcement and a per-user usage figure (also
  needed by v0.16 IMAP QUOTA).
- A DSN for an authenticated forward whose sender was not SRS-rewritten is
  spooled to an external address and silently dropped
  (`deliver.go` around 1050).
- `RetryNow` (`internal/queue/store.go:230`) does not reset the attempt
  count.
- The autoconfig handler advertises raw ports, not dev ports (already noted
  in the v0.12 PRD 8.2); US-003 fixes it for the ports it touches.

Found while implementing (2026-10-31):

- go-smtp v0.24.0 refuses `RCPT TO:<postmaster>` (no domain) with
  `501 5.5.2` in its parser, so RFC 5321's bare postmaster cannot reach
  Bongo. The resolver handles it; an upstream change is needed (US-001).
- A forward to `postmaster@` or `abuse@` on a hosted domain now counts as a
  hosted target and skips forward verification, the same trust as any
  hosted account (US-001).
- Release notes: an older binary cannot decode `smtp.postmaster_account`
  or `smtp.submissions_port`; set them only after every node is upgraded.
  With 465 on by default, a configured `smtp.tls` certificate that fails to
  load now fails startup instead of a warning (US-003).
- The spam-stays-in-Junk rule (US-005) holds in the default stage order
  only. If an operator orders the rules stage before antispam, the builtin
  backend overwrites a rule's target with Junk. Stage order is not
  validated.
- US-005 combinations to note in the release: delete together with copy or
  forward keeps the copies and forwards and skips the normal destination;
  copies of spam are suppressed outside Junk and Trash. During a mixed
  window an old node treats `[copy X, flag]` as a move to X, and an old
  node's rule form drops actions after the first.
- US-004's "Auto: " plus the original subject conflicts with 6.1 (never
  copy original header values). The original subject is decoded, stripped
  of control characters, cut by runes and re-encoded, not copied raw; with
  no subject at all the reply says "Auto-reply".
- Release notes: an older queue worker signs and relays an
  `OUTBOUND_ORIGIN_AUTOREPLY` message and never bounces it (null sender).
  Vacation replies are not serialised across nodes, so two nodes can each
  answer the same sender once during the interval (US-004).
- `queue.bounce_copy_postmaster` is a new HCL key an older binary refuses;
  set it only after every node is upgraded (US-002).
- Release notes: an older node ignores `FilterRule.match`, so it evaluates
  an any-rule as all, and saving the rule through an older node turns it
  back into all (US-006).
- Release notes (US-007): upgrade every node before creating a
  `mime_type` rule; an older node reads the field as empty, so a `matches`
  pattern that accepts an empty string fires there. Rule saves are now
  validated per field: a rule stored earlier with an operator its field
  does not support (for example `size contains`) is refused when edited
  until the condition is corrected. The rule tester now uses the delivery
  parser, so its body text is capped at 8192 bytes and messages with a
  missing blob are skipped.
- Release notes (US-008): failed outbound mail is now retried for 5 days
  (`queue.max_lifetime`) instead of bouncing after about 17 hours. The
  three new `queue.*` keys are refused by an older binary; set them only
  after every node is upgraded.
- Release notes (US-009): below `storage.min_free_mb` (default 1024 MB)
  the server refuses new mail and uploads with a temporary error. Check the
  default against production free space before deploying (6.4). The
  monitor samples `storage.data_dir` and its Raft snapshots; it does not
  read `cluster.raft.data_dir`. A failing free-space check never refuses
  work.
- US-010: reports, like bounces, are delivered through the local inbound
  pipeline, so a report to an external sender is dropped by the deliver
  stage; only local senders receive them until DSN relaying (v0.15 US-018) is
  built. No success report is sent for mail filed as spam, Junk or
  quarantined (it would confirm a live address). A success report can
  repeat if an envelope is retried after a crash. With a `queue.max_lifetime`
  of 4h or less and the default `delay_warning_after`, no delay report can
  be sent; the agent warns at startup. Release notes: the new proto fields
  and `queue.delay_warning_after` need every node upgraded first.
- Release notes (US-012): create alias domains only after every node is
  upgraded; an older node never rewrites, so mail to an alias address
  accepted there gets 550. A verified send-as identity on the alias domain
  outranks the rewrite, as it would on any newly hosted domain. A catch-all
  cannot be set on an alias domain (the target's applies).
- Release notes (US-014): swiping left on a message on a phone now
  archives it; before, it moved the message to Trash. IMAP clients see
  `\Archive` on an existing Archive mailbox. Browsers that cached the old
  bongo.js under the same version keep the old swipe until the cache is
  refreshed; the asset version changes with each release.
- US-017 as built: `-bm` and `-B<type>` (Debian cron passes `-B8BITMIME`)
  are accepted and ignored as well as the listed flags. Only a qualified
  `-f` is honoured; an unqualified one (cronie's `-f root`) and an
  unqualified `From:` are replaced by the account address. Quota refusal at
  DATA exits 69. `insecure_skip_verify` is allowed for a literal loopback
  IP or `localhost` only, not for a name that resolves to loopback.
- Found while implementing US-017: an authenticated submission to an
  unknown local part on a hosted domain (for example `nobody@localhost` in
  dev) is accepted and relayed instead of refused at RCPT, so `bongo
  sendmail` cannot report exit 67 for it. Candidate for v0.15.
- US-018 as built: `--json` output carries an `id` field the text table
  does not show (a superset, for scripts). Each list is capped at 100000
  rows; a truncated list prints the rows and exits non-zero.
