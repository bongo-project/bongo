# PRD: Trusted submission, and wiring up the scanners that already exist

## 1. Introduction/Overview

Two problems, both of which a tester meets within their first hour.

### Problem 1 — mail between accounts on your own server lands in Junk

Observed in production on 2026-08-19. An application submits mail as `registrations@example.com`
to `user@example.com`. Both are local accounts on a domain the server hosts. It is filed as spam
every time. From the server's own logs:

```
"msg":"antispam: scored","score":0.20,"action":"tag",
"reasons":["SPF softfail","DKIM missing","dmarc_quarantine"],
"sender":"registrations@example.com"

"msg":"smtp: message enqueued","from":"registrations@example.com",
"recipients":["user@example.com"],"source_ip":"172.19.0.1"
```

The score is **0.20** — far below the 0.5 threshold. The spam filter did not do this. **DMARC
escalation did**: the connecting address is `172.19.0.1`, a Docker bridge gateway, so SPF is
evaluated against a private IP and softfails; the submitting application does not DKIM-sign; and
`example.com` publishes `p=quarantine`. Neither identifier aligns, so the message is escalated
to Junk.

**Authenticating would not currently fix it.** `QueueEnvelope` (`proto/bongo/v1/pipeline.proto`)
carries no indication of whether a message arrived authenticated. The SMTP session knows —
`s.authed` and `s.submission` in `internal/smtp/server.go` — but that never reaches the pipeline.
So a correctly configured client using AUTH on :587 is judged exactly as if it were a stranger
connecting to :25 from the open internet.

This is the same shape as the bounce bug fixed in Phase 10: locally-originated mail evaluated
against origin-authentication checks that only make sense for mail from strangers. It is worse in
impact, because it affects ordinary use rather than failures. Every tester who emails themselves
will hit it.

### Problem 2 — scanners that are implemented but never constructed

The config accepts them and validation passes, so an operator reasonably believes they are
running. They are not:

| Component | State |
|---|---|
| ClamAV antivirus | `internal/pipeline/antivirus/clamav.go` fully implemented with tests; `clamav` is in `knownBackends`; `AntivirusConfig.ClamAV` exists with `enabled`/`socket`. But `internal/agent/agent.go` logs `"antivirus stage enabled but not yet implemented, skipping"` and never constructs it. |
| rspamd | `internal/pipeline/antispam/rspamd.go` implemented, `RspamdConfig` exists, `rspamd` is in `knownBackends` — never constructed outside tests. |
| SpamAssassin | `internal/pipeline/antispam/spamassassin.go` implemented — never constructed, and `spamassassin` is **not** in `knownBackends`, so configuring it is rejected. |
| composite | `internal/pipeline/antispam/composite.go` runs builtin plus an external backend and takes the stronger verdict — never constructed. |

Enabling antivirus today produces a log line and no scanning. That is worse than not offering the
option, because it silently converts an operator's explicit safety decision into nothing.

## 2. Goals

- Mail submitted by an authenticated local user is not judged by SPF/DKIM/DMARC alignment.
- Mail from an operator-configured trusted network is treated the same way.
- Trusted mail is still **scored for spam and scanned for viruses** — trust removes origin
  authentication, not filtering.
- Enabling ClamAV actually scans; enabling rspamd or SpamAssassin actually uses them.
- No behaviour change for inbound internet mail.

## 3. User Stories

### US-001: Authenticated submission is not judged as a stranger (do first)

**Description:** As a user, I want mail I send through my own server to reach the inbox, not Junk.

**Acceptance Criteria:**
- [ ] Given a message accepted on the submission port from a session that completed AUTH, when it
      reaches the antispam stage, then SPF, DKIM-alignment and DMARC evaluation are skipped and
      none of `SPF softfail`, `SPF fail`, `DKIM missing`, `DKIM fail` or any `dmarc_*` reason is
      recorded.
- [ ] Given that same message, the antispam stage still runs: phrase and Bayesian scoring apply,
      and a message that scores above the threshold on content alone is still routed to Junk.
- [ ] Given an authenticated submission from `registrations@example.test` to `lance@example.test`
      where `example.test` publishes `p=quarantine`, the message is delivered to INBOX with
      action `pass` and `TargetMailbox` empty. This is the production reproduction above.
- [ ] Given an UNAUTHENTICATED message on the MX port, all existing checks apply unchanged —
      byte-identical scoring to today.
- [ ] The trust flag is set **only** by the SMTP server from its own session state. No header,
      envelope field or client-supplied value can set it.

### US-002: Operator-configured trusted networks

**Description:** As an operator, I want mail from an application on my own host to be treated as
internal, without giving it credentials.

**Acceptance Criteria:**
- [ ] Given a configured trusted CIDR and a connection from inside it, the message is treated as
      trusted exactly as an authenticated submission is.
- [ ] Given a connection from outside every configured CIDR, it is untrusted.
- [ ] Given no configured trusted networks (the default), only authentication confers trust —
      no address is trusted implicitly, including loopback and private ranges.
- [ ] A malformed CIDR in config is a startup error naming the offending value, not a silent
      skip.

### US-003: ClamAV actually scans

**Description:** As an operator, I want enabling antivirus to mean mail is scanned.

**Acceptance Criteria:**
- [ ] Given `antivirus.clamav.enabled = true` and a reachable clamd socket, when a message
      containing the EICAR test string is delivered, then the antivirus stage reports an infection
      and the message is not delivered to the inbox.
- [ ] Given the same config and a clean message, it is delivered normally.
- [ ] Given `enabled = true` and clamd **unreachable**, the failure is logged at ERROR and the
      configured failure policy is applied. The policy is explicit, not accidental.
- [ ] Given the antivirus stage enabled with no backend available, the server no longer logs
      `"not yet implemented"` — that message is removed.

### US-004: External antispam backends are selectable

**Description:** As an operator, I want to point antispam at rspamd or SpamAssassin.

**Acceptance Criteria:**
- [ ] Given `backend = "rspamd"` on the antispam stage and a reachable rspamd, its verdict is used.
- [ ] Given `backend = "spamassassin"`, config validation accepts it and SpamAssassin is used.
- [ ] Given an external backend that is unreachable, the builtin scorer is used and the failure is
      logged — mail is never dropped because a scanner is down.
- [ ] Given `backend = "builtin"` or no backend, behaviour is unchanged.

## 4. Functional Requirements

**Trust (US-001, US-002)**

- FR-1: Add a trust indicator to `QueueEnvelope` recording that a message arrived authenticated or
  from a trusted network.
- FR-2: `internal/smtp/server.go` sets it from session state (`s.authed`, or source IP within a
  configured trusted CIDR) when enqueuing. Nothing else may set it.
- FR-3: The antispam stage skips SPF result interpretation, DKIM alignment and DMARC evaluation
  when the flag is set.
- FR-4: The antispam stage **continues** to run phrase and Bayesian scoring for trusted mail, and
  the antivirus stage continues to run. FR-3 removes origin authentication only.
- FR-5: Add a `trusted_networks` list of CIDRs under `smtp` in config, defaulting to empty.
- FR-6: An unparseable CIDR fails startup with an error naming it.

**Scanner wiring (US-003, US-004)**

- FR-7: Construct `antivirus.NewClamAVProcessor` for the antivirus stage using
  `AntivirusConfig.ClamAV`, replacing the "not yet implemented" branch in `internal/agent/agent.go`.
- FR-8: Select the antispam processor from the stage's `Backend`: `builtin`, `rspamd`,
  `spamassassin`, or a composite of builtin plus one external.
- FR-9: Add `spamassassin` to `knownBackends` in `internal/config/config.go`.
- FR-10: Add a `spamassassin` config block matching the shape of `RspamdConfig`.
- FR-11: An unavailable external scanner must degrade to the builtin scorer, never fail the
  message.
- FR-12: Document the antivirus failure policy explicitly in the example config: whether an
  unreachable scanner means accept-unscanned or defer.

## 5. Non-Goals (Out of Scope)

- No DNSBL — specified separately in `prd-dnsbl-blocklist-checks.md`.
- No general antispam bypass. Phase 10 explicitly rejected a "local origin" flag that skipped
  scoring, because inbound internet mail enters through the same `EnqueueLocal` path and such a
  flag would sit one careless caller away from a full bypass. FR-3 skips **origin authentication
  checks only**, and FR-4 exists to keep that distinction enforced by tests.
- No agent-role work, and no gRPC mTLS — separate, larger, and not needed for either problem here.
- No outbound DKIM signing changes. The submitting application should ideally sign or authenticate,
  but this must work regardless.

## 6. Technical Considerations

- **The reproduction is available in production logs**, which makes US-001 straightforward to
  verify end to end rather than only in unit tests.
- **`internal/pipeline/antispam/builtin.go`** holds both the scorer call and `applyDMARC`; the
  skip belongs there, at the point the origin signals are interpreted, not in the SMTP layer.
- **Trust must be unforgeable.** `QueueEnvelope` is serialised to the spool on disk and re-read by
  the runner, so anything able to write a spool file could set the flag. That is already true of
  `TargetMailbox` and is bounded by filesystem permissions, but it argues for the flag being set in
  exactly one place and never derived from message content.
- **ClamAV's default timeout is 60s** (`internal/pipeline/antivirus/clamav.go:21`). On the
  delivery path that is a long time to hold a message; consider whether the pipeline stage needs a
  shorter one.
- **The composite processor already implements the interesting case** — builtin plus external,
  stronger verdict wins — including carrying `DmarcResult` across, which was fixed during Phase 10.

**Constraints**

- Table-driven tests, `-race`, and `-tags=integration` for anything exercising a real server.
- No test may require a running clamd, rspamd or spamd; the external clients need injectable
  transports.

## 7. Success Metrics

- The production reproduction — `registrations@` to `lance@` on the same hosted domain — lands in
  INBOX rather than Junk, and its stored headers show no `dmarc_*` reason.
- A message from the open internet failing SPF and DMARC is still junked, with scoring unchanged.
- An EICAR test message is caught with ClamAV enabled.
- Configuring `backend = "spamassassin"` is accepted and used.
- `make test-all` passes; `govulncheck` and `gosec` clean.

## 8. Open Questions

1. **Antivirus failure policy.** If clamd is unreachable, is mail accepted unscanned or deferred
   with a 4xx? Accepting unscanned quietly weakens a control the operator switched on; deferring
   means a scanner outage stops inbound mail. Postfix-style deployments usually defer. Needs an
   explicit decision, and it should be configurable with a documented default.
2. **Should authenticated submission skip antivirus too?** Probably not — a compromised account
   sending malware is exactly the case scanning should catch — but it deserves a deliberate
   answer rather than falling out of the implementation.
3. **What should the application actually do?** Even after this, submitting on :25 unauthenticated
   from a container is the wrong shape. Trusted networks make it work; AUTH on :587 would be
   better and would survive the network changing. Worth deciding which is documented as the
   recommended pattern for co-located applications.
