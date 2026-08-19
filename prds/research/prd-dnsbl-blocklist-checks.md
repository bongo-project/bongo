# PRD: Configurable DNSBL / RBL checks on inbound mail

## 1. Introduction/Overview

Bongo has no blocklist support. Inbound mail is scored on SPF, DKIM, DMARC, heuristic phrases and
a Bayesian filter, but the connecting IP's reputation is never consulted — which is the single
cheapest spam signal most mail servers use.

This adds **configurable DNSBL lookups**: the operator lists the blocklist zones to consult in the
HCL config, and a listing contributes to the spam score.

**A listing must contribute to the score, not reject the message.** That recommendation comes from
direct experience: on 2026-08-17 this very server was listed on `b.barracudacentral.org` having
never sent a single message, purely because the IP had been recycled by the hosting provider. A
receiver configured to reject on that listing would have silently discarded legitimate mail, and
the sender would have seen only a 5xx. Blocklists are useful evidence and poor verdicts. Scoring
keeps the operator's threshold in control of the outcome and leaves a trail in the headers.

## 2. Goals

- An operator can list DNSBL zones in HCL, with a per-zone weight, and have them consulted on
  inbound mail.
- A listing raises the spam score; it never by itself rejects a message.
- A slow or dead blocklist never delays or blocks mail — lookups fail open.
- Every verdict is visible in the stored message, so "why was this junked" stays answerable.
- Zero behaviour change when no zones are configured (the default).

## 3. User Stories

### US-001: Configure blocklist zones

**Description:** As an operator, I want to list the blocklists I trust so that inbound mail from
known-bad IPs is scored accordingly.

**Acceptance Criteria:**
- [ ] Given a config with no `dnsbl` block, when mail is received, then no DNSBL lookups are
      performed and scoring is byte-identical to today.
- [ ] Given a configured zone with a weight, when the connecting IP is listed in that zone, then
      the message's spam score increases by that weight and the reason names the zone.
- [ ] Given two configured zones and an IP listed in both, then both weights apply and both zones
      are named in the reasons.
- [ ] Given a configured zone and an IP listed in none, then the score is unchanged and no reason
      is added.
- [ ] Given a zone weight that would push the score above the threshold on its own, the message is
      routed to Junk — but is never rejected at SMTP time.

### US-002: A dead blocklist cannot break mail delivery

**Description:** As an operator, I want blocklist failures to be harmless so that someone else's
outage cannot stop my mail.

**Acceptance Criteria:**
- [ ] Given a zone whose DNS lookup times out, when mail is received, then the message is accepted
      and scored as if the zone had returned nothing, and the timeout is logged.
- [ ] Given a zone that returns SERVFAIL or NXDOMAIN, the message is scored as not-listed.
- [ ] The total time spent on DNSBL lookups for one message is bounded by a configurable timeout,
      regardless of how many zones are configured.
- [ ] Lookups for multiple zones run concurrently, so N zones do not cost N timeouts.

### US-003: Only meaningful IPs are checked

**Description:** As an operator, I do not want lookups performed where the answer cannot mean
anything.

**Acceptance Criteria:**
- [ ] Given a connecting IP in a private range (RFC 1918), loopback, or link-local, then no DNSBL
      lookup is performed. A container or LAN address is not meaningfully listable, and querying
      it leaks internal addressing to the blocklist operator.
- [ ] Given a message submitted by an authenticated local user, no DNSBL lookup is performed —
      blocklists judge strangers, not your own users.
- [ ] Given an IPv6 connecting address, the query uses the reversed-nibble form, and a zone that
      does not support IPv6 is treated as not-listed rather than as an error.

### US-004: The verdict is visible

**Description:** As a user or admin, I want to see why a message was scored, so a false positive
can be diagnosed rather than guessed at.

**Acceptance Criteria:**
- [ ] Given a listed IP, the delivered message's `X-Spam-Reasons` header names the zone that
      listed it.
- [ ] The `Authentication-Results` header is not used for this — DNSBL is not an authentication
      method, and overloading it would be misleading.
- [ ] An INFO log records the IP, the zone, and the returned code for each listing.

## 4. Functional Requirements

- FR-1: Add a `dnsbl` block under `antispam` in HCL, holding a global enable, a timeout, and a
  list of zones each with a name and a weight.
- FR-2: A zone with no explicit weight uses a documented default.
- FR-3: Lookups query `<reversed-ip>.<zone>` and treat any `127.0.0.0/8` answer as a listing;
  NXDOMAIN as not listed.
- FR-4: All zone lookups for a message run concurrently under one shared timeout.
- FR-5: Any lookup error — timeout, SERVFAIL, malformed answer — is treated as not-listed and
  logged. It must never fail the message.
- FR-6: Private, loopback and link-local source addresses are skipped without a lookup.
- FR-7: Authenticated or otherwise trusted submissions are skipped without a lookup.
- FR-8: A listing adds its weight to the antispam score and appends a reason naming the zone.
- FR-9: DNSBL never triggers an SMTP rejection on its own, regardless of weight.
- FR-10: Results are cached per IP for a configurable TTL, so a sender delivering several messages
  is not looked up repeatedly.
- FR-11: The resolver must be injectable, following the existing `DNSResolver` interface in
  `internal/mail/spf.go`, so tests run without network access.

## 5. Non-Goals (Out of Scope)

- No reject-at-RCPT mode. If it is wanted later it should be a separate, explicit decision with
  its own PRD — the failure mode is silent loss of legitimate mail.
- No URI/domain blocklists (SURBL/URIBL) — this covers connecting-IP lists only.
- No allowlists/whitelists.
- No shipped default zone list. Which blocklists to trust is an operator policy decision, and
  baking in a default silently makes it ours.
- No automatic delisting workflow or reputation monitoring for the operator's own IP.

## 6. Technical Considerations

- **Reuse the SPF resolver seam.** `internal/mail/spf.go` defines a `DNSResolver` interface with a
  `defaultResolver` using `net.DefaultResolver`. Following it keeps DNSBL testable offline, which
  matters because a test that queries a real blocklist is both slow and antisocial.
- **The connecting IP must be the real one.** `internal/smtp/server.go` takes `remoteIP` from the
  connection, and `internal/netx/proxyproto.go` unwraps PROXY protocol when enabled. Behind
  Traefik without PROXY protocol every connection appears to come from the proxy, so DNSBL would
  be checking the proxy's reputation. The docs must state that DNSBL requires
  `smtp.proxy_protocol` when behind a proxy, or it is worse than useless.
  **Planned change (operator, 2026-08-19):** Bongo is intended to face the internet directly
  rather than sitting behind Traefik for SMTP. That removes this caveat entirely — the connecting
  IP becomes the real one with no PROXY protocol needed — and it also makes the existing
  X-Forwarded-For guardrails moot for the mail path. Worth confirming which deployment shape is in
  place before enabling DNSBL, since the two have opposite requirements.
- **Ordering.** The lookup belongs in the antispam stage alongside the existing signals rather
  than at SMTP time, which keeps it out of the connection path and means a slow zone delays
  nothing the sender is waiting on.
- **Interaction with the trusted-submission fix.** FR-7 depends on the pipeline being able to tell
  an authenticated submission from inbound internet mail. `QueueEnvelope` currently has no such
  field. If that work has not landed, FR-7 can be satisfied by FR-6 alone for same-host traffic
  (a Docker or LAN address is private), but the general case needs it.

**Constraints**

- Never log message content. IP and zone only.
- Table-driven tests with an injected resolver, `-race`, and no network access in any test.

## 7. Success Metrics

- With no `dnsbl` block configured, scoring is unchanged and no DNS traffic is generated.
- A message from an IP listed in a configured zone is scored higher and says so in
  `X-Spam-Reasons`.
- With a zone pointed at an unreachable resolver, mail still delivers, within the configured
  timeout.
- No test performs a live blocklist query.

## 8. Open Questions

1. **Which zones, if any, to document as suggestions?** Naming zones in the example config is a
   soft endorsement and carries their terms of use — several blocklists prohibit commercial use
   or high-volume querying without a subscription, and query the public resolver from a busy
   server and you will be rate-limited into silence. Suggest documenting the *shape* with a
   placeholder rather than shipping real zone names.
2. **Weights.** Is a single weight per zone enough, or should the returned `127.0.0.x` code select
   a weight? Zen encodes distinct list types in the final octet (policy vs exploit vs spam
   source), and they do not deserve equal confidence. Starting with one weight per zone is
   simpler; per-code weighting can follow if it proves too blunt.
3. **Should a listing on a high-confidence zone be allowed to exceed the threshold alone?** That
   is a weighting decision rather than a code one, but it is worth deciding deliberately, because
   it is the point at which a third party's mistake can junk your mail unaided.
