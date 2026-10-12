# Norman Progress Log

> Compressed 2026-08-22. Full history: [progress-archive.md](progress-archive.md).
> Entries below this header are appended as normal.

## Patterns and learnings

### Configuration & Deployment

PATTERN: [config] A NON-POINTER block field is MANDATORY under strict hclsimple/gohcl decoding — every config file lacking that block fails to decode ("Missing <name> block"), including the deployed bongo.prod.hcl. Adding fields to an existing block is safe; adding a new block is not, unless the field is a POINTER (`*FooConfig`) with nil-safe accessors. Precedent and the full explanation: the SpamAssassin field at internal/config/config.go:251-257.
PATTERN: [config] A dev-mode convenience default that overrides configuration UNCONDITIONALLY is a bug, not a shortcut: it silently discards operator intent, and it forces every in-process test to share one fixed resource.
PATTERN: [deploy] first-boot with a sidecar-provisioned cert: EVERY listener that reads that cert path (raft cluster.tls AND imap/pop3) must tolerate absence (warn+fallback), else one hard-fail crashes startup before the cert is written.
PATTERN: [deploy] Verify WHICH compose file owns a running container before deploying — `docker inspect <c> --format '{{json .Config.Labels}}' | grep config_files`.
PATTERN: [deploy] Shipping the code is not the same as fixing the bug. A feature gated on config that is not set changes nothing in production — check the live config before reporting a fix.
PATTERN: [scope] A dormant validation gate is a deployment-affecting change the moment you enable it. Enabling "validation that should have been running all along" is never a side effect of another task.
PATTERN: [docs] Verify what a security/trust feature does NOT grant before documenting it. Trusted networks skip origin authentication but confer no relay rights.

### Security & Trust

PATTERN: [security] apply key derivation at the single sign/verify choke point per purpose so all callers stay consistent with zero call-site edits.
PATTERN: [security] Derive the DKIM public TXT from the stored private PEM at render time and keep the private key OUT of the template DomainInfo struct — never add a private-key field to a view model.
PATTERN: [security] A "cannot parse, return unchanged" guard is a BYPASS when the function it guards is a sanitiser. Ask what an attacker gains by making parsing fail.
PATTERN: [security] A trust/authz flag in exactly ONE helper and comment it as the only permitted writer. `grep -rn "Trusted:\|\.Trusted ="` over non-test code proves "nothing else may set it".
PATTERN: [security] Trust is server-local and must never be accepted over the wire. Any RPC/HTTP ingress that reconstructs an internal envelope must strip server-derived security fields from the caller-supplied payload.
PATTERN: [security] Check a code comment's factual CLAIM, not just its reasoning. "External backends do their own SPF/DKIM checks" was stated confidently, sounded plausible, and was false.
PATTERN: [security] Prefer making a finding untrue over annotating it away. url.PathEscape makes the redirect target structurally single-segment; the #nosec only asserts that someone once believed it was safe.
PATTERN: [security] Before trusting a source IP, check whether an external party can cause the server to SEE that IP. With PROXY protocol enabled the answer depends on whether the port is directly reachable.
PATTERN: [security] Treat a G115 integer-conversion finding as real until you trace what CONSUMES the value. Here the conversion fed a `>=` comparison whose failure mode was silently disabling a quota.

### SMTP, DKIM & Mail

PATTERN: [smtp] Session relay policy lives in internal/smtp/server.go; real client IP available via PROXY protocol (internal/netx). Backend has a per-IP conn counter map (perIP) already — reuse/extend for rate limiting.
PATTERN: [smtp-client] Go's net/smtp hardcodes localName="localhost"; NewClient's `host` arg is only the TLS ServerName/AUTH name, NOT the HELO. Any outbound net/smtp client MUST call client.Hello(fqdn) as its first command.
PATTERN: [dkim] Per-domain signing goes through mail.DKIMRegistry keyed by the From-HEADER domain (not envelope sender); registry.Add(domain, signer) is the hook to load DomainRecord keys.
PATTERN: [dkim] The admin handler and queue worker share ONE mail.DKIMRegistry created at agent.go:~377; inject via setters BEFORE route/worker construction so generated keys sign immediately without a restart.
PATTERN: [dkim] DKIMRegistry.Sign is deliberately non-failing: nil registry, unknown domain, and signing error ALL return the message unsigned with a log and a nil error. Callers that need to know whether signing happened must pre-check Get(domain).
PATTERN: [mail] Server primary domain resolution lives in mail.ResolveServerDomain — reuse it for any outbound-hygiene domain need.
PATTERN: [headers] sanitizeHeaderValue(v) strips CRLF in internal/smtp/server.go — reuse the same approach for compose/bounce header building.

### Storage, Indexing & Deduplication

PATTERN: [store] Global (non-per-user) derived indexes go in the system.db SystemIndex; a store that mirrors Pebble→SQLite takes a *SystemIndex handle (nil-safe) alongside pebble+applier. Raft write FIRST (source of truth), SQLite second, return SQLite errors.
PATTERN: [store] Per-domain quota = separate /quota + /usage keys (never the clobber-prone DKIM DomainRecord); storage attributed to the mailbox owner's PRIMARY domain; enforce max-users at create + storage at RCPT. Recompute MUST reset cleared domains to 0.
PATTERN: [migration] Leader-only startup seeding mirrors ensureConfiguredDomain; keep the seeding CORE as a store-package func (testable with directApplier + SystemIndex, no Raft node).
PATTERN: [migration] Create-only seeding = Get-before-Put, but populate any lookup maps (email→id) BEFORE the skip so dependent phases still work on every re-run.
PATTERN: [dedup-storage] Reading only headers from a queued message does NOT need store.RawMessageBytes. blob_hash addresses a skeleton for split records, but attachment spans live only inside the top-level body.

### Delivery, Recipients & Tenancy

PATTERN: [delivery] ONE shared store.RecipientResolver injected into every recipient-resolving consumer (SMTP RCPT guard + pipeline + compose) guarantees RCPT-accept and delivery agree; resolver reads Pebble for node-correctness.
PATTERN: [delivery] Per-domain catch-all is the LAST resolver tier; the open-relay guarantee is simply "no /system/catchall/{domain} key ⇒ fall through to 550" — never accept unmatched mail for a domain without an explicit catch-all entry.
PATTERN: [dependency] address index (4.1) is the keystone for delivery (4.3), address mgmt (4.5), and send-as (5.1); tenancy ownership (6.1) gates isolation (6.2) + quotas (6.3).
PATTERN: [sendas] Send-as identity alignment = set ONE fromAddr and thread it into From header + envelope SenderEmail + Message-ID host; DKIM needs NO extra code. ENFORCE server-side only when from != session primary.
PATTERN: [verification] One-time email codes: crypto/rand generate → store SHA-256 hash + expiry + attempts (never the plaintext) → email plaintext via outbound queue → crypto/subtle constant-time compare on submit. NEVER log the code.
PATTERN: [tenancy] Admin isolation = one AdminScope in context (RequireAdminOrDomainAdmin middleware) + explicit per-handler helper called FIRST; lists FILTER (never just block) to avoid row leakage. For address ops, scope BOTH the target user's domain AND the address's own domain.

### MIME & Headers

PATTERN: [mime] struct-driven builder — buildAlternativeBody/buildRelatedBody each return (contentType, body []byte) so nested parts embed cleanly. Content-ID header is <id> (brackets, RFC 2392); cid: src is bare.
PATTERN: [mime] RFC 2045 6.4 forbids quoted-printable/base64 on message/* parts, so message/delivery-status must be 7bit — which makes it the caller's job to ensure the content really is ASCII.
PATTERN: [mime] Detect a header block's line ending by comparing the POSITIONS of "\r\n\r\n" and "\n\n", never by testing for CRLF first: a body blank line after an LF-only header block otherwise wins the vote.
PATTERN: [sanitize] bluemonday cid: scheme must be whitelisted via AllowURLSchemes globally; a Matching(regex) on img[src] alone is insufficient for non-standard schemes.

### Testing & Verification

PATTERN: [testing] Assert protocol-level behaviour on the wire, not just the pure helper. A unit test of the name resolver alone would have passed while the relay still sent EHLO localhost.
PATTERN: [testing] Asserting a protocol field with strings.Contains over the WHOLE message is vacuous: "d=example.test" and "from" both occur naturally in a DSN body. Extract the specific header (unfold continuation lines first).
PATTERN: [testing] Verifying DKIM without live DNS: dkim.VerifyWithOptions accepts a LookupTXT func; return "v=DKIM1; k=rsa; p=" + base64(PKIX pubkey) for <selector>._domainkey.<domain>.
PATTERN: [testing] Before accepting "these two values cannot differ, so the test would be vacuous", look for a NORMALISATION seam. Two lookups of the "same" identity often differ in case/trailing-dot/trimming handling.
PATTERN: [testing] A cross-user/permission test that gives each user its OWN fixture proves nothing — the denial comes from absence, not from the guard. Share ONE dataset and add a positive control.
PATTERN: [testing] A helper that allocates a resource by opening-and-closing cannot be called in a loop for DISTINCT resources — the kernel may reuse what it just reclaimed.
PATTERN: [testing] Prove an isolation fix by INJECTING the failure it is meant to contain. "The suite passes" does not demonstrate that one failure stays contained; only a deliberate failure does.
PATTERN: [testing] A "still applies" guard test is vacuous unless the DISABLED case would actually fail. Pick an input BELOW threshold and let only the mechanism carry it over, and add the paired negative control.
PATTERN: [testing] Prove a guard test is real by INJECTING the regression it claims to catch and watching it fail. Cheap, and the only actual evidence.
PATTERN: [testing] The strongest proof a test is not vacuous is a PAIRED NEGATIVE CONTROL on the same code path — signed passes / unsigned escalates.
PATTERN: [testing] `go test ./...` SKIPS build-tagged suites entirely and says nothing. Gate with -tags=integration.
PATTERN: [test] to assert no injected header, split output on \r\n and compare whole lines, not substring Contains (avoids false positives).

### Code Review & Implementation

PATTERN: [review] When you fix a divergence between a guard and the thing it guards, check the FALLBACK branch for the same divergence inverted. Fixing the happy path and leaving the error path is a common half-fix.
PATTERN: [review] Not every reviewer suggestion is an improvement. Dropping the explicit CRLF before a MIME close delimiter transfers the framing guarantee to a helper's return-value suffix.
PATTERN: [review] Worker-written tests need the same scrutiny as worker-written code. Two of three substantive issues in a round were test-quality, not implementation.
PATTERN: [review] When an advisor identifies a real problem, check its PRESCRIPTION separately from its diagnosis. The diagnosis may be correct but the prescribed fix may have side effects.
PATTERN: [review] An outlier is a signal: when eight sibling call sites carry an annotation and one does not, the odd one out is usually an oversight from a later change.
PATTERN: [review] When behaviour was pinned wrongly, INVERT the test rather than deleting it, and say in the comment what the earlier revision did and why it was wrong. The test then documents the trap.
PATTERN: [coupling] A pre-check that resolves an identity one way while the helper it guards resolves it another agrees only by construction. Comment the invariant at the seam.

### Specifications & Architecture

PATTERN: [spec] When a PRD prescribes "fall back to helper X", verify X's OWN fallback chain — ResolveServerDomain bottoming out at "localhost" made FR-20 and FR-21 mutually contradictory.
PATTERN: [spec] Second spec bug caught by advisor plan review this phase. Both were the same shape: a requirement locally plausible but contradicting a constraint elsewhere.
PATTERN: [design] "Quarantine on failure" is only a safe policy if the quarantine is drainable. Check the drain path exists BEFORE choosing quarantine as a failure mode.
PATTERN: [design] A boolean named `enabled` must actually disable. Treating it as informational while a different field selects behaviour inverts the operator's plain reading.
PATTERN: [design] A directory in a spool/queue layout is not a work queue just because it is named like one. Grep for what READS it before assuming a move into it does anything.
PATTERN: [design] When an interface param list reaches ~5 and the next addition is a security boolean, introduce a metadata struct. A trailing bool is exactly how a careless caller sets a security flag by accident.

### UI, Frontend & Documentation

PATTERN: [alpine-csp] for a plain/rich editor toggle, use two <template x-if> branches (not x-show) so the inactive input is removed from the DOM and only one same-named field submits.
PATTERN: [admin-ui] Per-entity detail sub-screen is the pattern for admin objects with many settings — a slim list + a /{id} detail page beats a wide table. The new GET route MUST carry the same scope check as its mutations.
PATTERN: [ui] When capping attacker-controlled content for display, cap the RAW bytes before escaping (escaping can expand ~6x), cut on a line boundary, and re-validate UTF-8 after cutting.
PATTERN: [ui] Reused existing `[data-copy-previous]` clipboard handler (bongo.js:1740) for per-row copy buttons — copies previousElementSibling.textContent; CSP-safe (no inline handlers).
PATTERN: [dns] DNS-config verification = compare ACTUAL vs EXPECTED-for-this-server, not existence. Keep the compare logic PURE, run the lookups concurrently with a bounded deadline, model 4 states (ok/fail/neutral/error).
PATTERN: [dns-guide] Pure builder `buildDNSGuide(...) ([]DNSGuideRow, pending bool)` — no I/O so the same values feed the on-page table, the BIND zone download, and unit tests; can never drift.
PATTERN: [dns-guide] Shared-host model: MX + all SRV targets = server SMTP.Hostname (read dynamically, never hardcoded). A/AAAA come from resolveServerIPs(mailHostname).
PATTERN: [dns-guide] Never publish an empty-p= DKIM record (worse than none = "revoked"). buildDNSGuide omits the _domainkey row and returns pending=true when no key exists.
PATTERN: [docs] Quoting a UI label from the i18n KEY instead of its VALUE produces confident, wrong documentation.
PATTERN: [docs] Relative links inside docs/ must not be prefixed with docs/. Grep for "](docs/" in any file already under docs/ before committing.
PATTERN: [docs] Deleting a false claim is not the same as correcting it. Removing "antivirus is a no-op" left testers with no guidance where they previously had wrong guidance.
PATTERN: [docs] The riskiest documentation sentences are the confident ones about failure modes ("fails to start if X"). Trace the actual code path for every failure-mode claim.

### Protobuf & Code Generation

PATTERN: [proto] PreferenceStore Get/Set marshal the whole UserPreferences, so new scalar proto fields round-trip with zero store code — only the generated struct changes. Use next free field number (never reuse).
PATTERN: [proto] Adding a new proto message triggers a 1-line buf-version header rewrite across ALL *.pb.go — expected, harmless; verify signature_html + real content are untouched.

### Workflows & Processes

PATTERN: [workflow] Workflow `args` may arrive as a JSON string — guard with `const A = typeof args === 'string' ? JSON.parse(args) : args`.
PATTERN: [workflow] isolation worktrees base off a stale commit in this harness — for tasks depending on just-committed in-session work, run non-isolated (isolate:false) sequential batches so workers see current HEAD.
PATTERN: [workflow] A workflow reporting failure may still have mutated the tree. Always `git status` after a failed run, and treat surviving content as UNREVIEWED.
PATTERN: [workflow] A classifier can return an agentType that does not exist, killing the implement stage while its own side effects survive on disk. Treat ANY non-DONE workflow as "check the tree".
PATTERN: [lint] Go sentinel error strings must be lowercase (ST1005); when the same text is user-facing, keep the sentinel lowercase and capitalize at the http.Error boundary.
PATTERN: [go] A nil pointer stored in an interface is NOT nil. Converting a concrete dependency to an interface "so tests can fake it" silently breaks every `x != nil` guard on it.

### Concurrency, CI & Observability

PATTERN: [concurrency] Two norman tasks can share a working tree safely when their FILES are disjoint, but they still share the BUILD. Give the second task scoped build/test commands and an explicit "these packages are not yours, report BLOCKED" rule.
PATTERN: [ci] Sequential steps in one CI job MASK each other — govulncheck failing meant gosec never ran, for months. When a job goes green after a fix, check whether later steps were ever reached before.
PATTERN: [ci] `gosec@latest` / `govulncheck@latest` in CI means a new upstream rule or advisory can turn the build red with no code change. Good for security freshness, bad for reproducibility.
PATTERN: [ci] When CI reports a finding you cannot reproduce, FIX THE TOOLING GAP FIRST — install the same version CI uses and reproduce locally.
PATTERN: [ci] An intentionally fragile CI step needs its rationale AT THE STEP. Unpinned scanners look like drift to anyone who did not choose it.
PATTERN: [ci] A green deploy pipeline alongside a red test pipeline hides breakage indefinitely: the artefact still ships, so nothing feels broken. Check per-JOB status, not "did a run happen".
PATTERN: [container] no-new-privileges and setcap file-caps are mutually exclusive for non-root privileged-port binding; omit no-new-privileges when using setcap+su-exec.
PATTERN: [rate-limiting] inject nowFn for deterministic window tests; sweep map every N calls to bound memory without a background timer.
PATTERN: [pipeline] A composite/fallback processor that returns one backend's result verbatim drops any field only the OTHER backend populates. Adding a field to StageResult means auditing every place a StageResult is chosen.
PATTERN: [integration] web-UI integration tests need agent.NewAgent + a.Run (dev role, admin@localhost/admin) — the raft createTestNode/sharedStartHTTP helpers only expose /internal routes. Run with `go test -tags integration ./test/integration/`.

### Debugging, Observability & Operations

PATTERN: [debugging] Before adding instrumentation, check whether the system already persisted the measurement.
PATTERN: [debugging] Establish whether an outlier is the norm before chasing it. Median over 42 historical messages reframed this from "delivery is slow" to "the first delivery after a restart is slow".
PATTERN: [debugging] Fixing real bugs found along the way is not the same as fixing the reported bug. Three genuine defects can be found and none was the cause.
PATTERN: [debugging] When a scraping test stops finding what it expects, DUMP WHAT THE PAGE ACTUALLY CONTAINS before theorising.
PATTERN: [correctness] A timeout argument is not a bound unless you check what the callee does with it. `raft.Apply(cmd, timeout)` bounds ENQUEUE only; `future.Error()` is unbounded.
PATTERN: [observability] Sending a subsystem's logs to io.Discard converts a five-minute diagnosis into a multi-hour one. Consensus, storage and anything else that can block the write path should never be silent.
PATTERN: [ops] A backup verification script is itself untested until it has run against a real archive.

### Imports & Planning

PATTERN: [import] Verify a PRD's cited call sites before turning them into tasks. Three of the tasks changed shape once the actual signatures and the runner's error path were read.
PATTERN: [import] Re-check a PRD's stated dependencies at import time. This one was written two phases ago and its central "this field does not exist yet" caveat had since become false.

## Key decisions

- **2026-07-18** Phase 3 (multi-domain sending) recommended to execute first: independent, low-risk, unblocks hosting-for-sending. Phases 4→5→6 are sequential; Phase 6 is the security-critical bulk.
- **2026-07-19** Phase 6 scoping confirmed with operator: (1) context-injected authorized-domain SET + explicit per-handler filter/check + adversarial isolation tests; (2) incremental with checkpoints; (3) user belongs to domain of PRIMARY address; (4) per-domain max-users AND aggregate storage.
- **2026-07-19** Operator chose to WAIT and bundle Phase 5 (5.2 verification) + hygiene WITH Phase 6 (no separate v0.6.0).
- **2026-08-19** Antivirus failure policy = quarantine. Prerequisite 12.4 (bulk rescan) added because the existing release path was non-functional — it moved mail to a directory nothing reads.
- **2026-08-18** Keep both security scanners at @latest so new findings surface, accepting that CI can go red with no code change. govulncheck queries the live Go vulnerability database; gosec is a static analyser so @latest buys new rule classes.

## Recent entries (full detail)

## 2026-08-22 — Backups VERIFIED (first real run of verify-restore.sh)

All four checks pass against `bongo_2026-08-22_03-05-02.tar.gz` (9.9M): extracts with data/ +
secrets/, sampled blob hashes to its own filename, DKIM key parses, and a real server starts
against the restored data and reports ready. "We have backups" is now evidence rather than an
assumption.

**The first run failed on a bug in the script itself**, not the backup: `web.session_secret is
required outside dev mode`. The prod config deliberately keeps the secret out of the file
(BONGO_WEB_SESSION_SECRET), so a config derived from it has none. Fixed in `1a0e0b1`-era commit by
injecting a throwaway secret — the test instance has every listener disabled and is deleted on
exit, so it needs A secret, not THE secret. Verified end to end locally against a synthetic archive
built from a REAL Pebble store before asking the operator to reinstall it.

Host setup: `/usr/local/sbin/verify-restore.sh` and `/usr/local/bin/bongo-go`, both
root-owned, with `(root) NOPASSWD:` on the script only. Re-run over ssh with
`sudo -n /usr/local/sbin/verify-restore.sh --latest`.

Not yet checked: DKIM key vs published DNS (`VERIFY_DKIM_DOMAIN`), which sudo's env_reset strips.

PATTERN: [ops] A backup verification script is itself untested until it has run against a real
archive. This one had a latent failure that only a production run could expose — the same class of
problem it exists to catch.
PATTERN: [security] A NOPASSWD sudoers rule pointing at a user-writable script is full passwordless
root in disguise. Scoping is only real if the target is root-owned and not writable by the granted
user.

## 2026-08-22 — norman import: Phase 13 (DNSBL / blocklist checks)

Imported `prd-dnsbl-blocklist-checks.md` (research -> backlog). 5 tasks from 4 user stories / 11 FRs.

Verified the PRD's cited dependencies before importing rather than trusting them — it was written
before Phase 12 and two of its assumptions had changed:
- FR-7's trust skip is now directly implementable: `env.GetTrusted()` exists (`builtin.go:169`).
  The PRD says "QueueEnvelope currently has no such field" and offers a fallback; that fallback is
  no longer needed.
- The `DNSResolver` seam is real (`internal/mail/spf.go:41`, LookupTXT + LookupIP), so DNSBL's
  A-record queries fit without inventing an abstraction.
- `proxy_protocol = true` on prod, so the PRD's decisive caveat is already satisfied — but it
  inverts if bongo moves to face the internet directly, which the operator has planned. Captured
  in 13.5 rather than left as a footnote.

Also carried B8 forward into 13.2: config validation must happen at the point of use, NOT by wiring
`cfg.Validate()` into startup.

PATTERN: [import] Re-check a PRD's stated dependencies at import time. This one was written two
phases ago and its central "this field does not exist yet" caveat had since become false.

## 2026-08-22 — 13.1 DNSBL lookup engine — DONE

Commit: 9ddfa33. New package `internal/dnsbl` (dnsbl.go, query.go, cache.go + tests).
Advisor plan returned REVISE (amended FR-3); code review returned MINOR with 7 findings,
all fixed by the orchestrator before commit:

1. Cache poisoning — a cancelled/expired parent context produced an all-timeout Result that
   was cached for a full TTL, silencing DNSBL for that IP. `lookup` now returns `cacheable`,
   false when `parent.Err() != nil`. Guard: TestCheck_CancelledParentIsNotCached.
2. Collector could drop a buffered result — `select` picks pseudo-randomly when both
   `<-ch` and `<-ctx.Done()` are ready. Added a non-blocking drain before writing zones off.
3. Cached `Result.Zones` aliased one backing array across all callers. Cloned on get and set.
4. Comment cited the 2026-08-17 barracudacentral incident as a shared-resolver policy error;
   the PRD says it was a genuine listing of a recycled IP. Rewrote the comment to the real
   rationale and recorded the FR-3 deviation in the PRD itself.
5. Non-positive timeout/TTL not clamped — a missing HCL value in 13.2 would silently disable
   DNSBL. Now clamped to defaults with a Warn.
6. FR-4 timing assertion allowed 5x the timeout with only one hanging zone — a per-zone-timeout
   implementation passed it. Tightened to 2x with three hanging zones.
7. `queryName` failure path was the one silent fail-open branch. Now logs and records Err.

Gate: `make test-all` green (35 packages), gosec clean, golangci-lint 0 issues.

PATTERN: [testing] A latency guard can pass against a broken implementation if some OTHER
mechanism still bounds the observable time. Injecting per-zone timeouts did NOT fail the FR-4
test, because the collector's shared deadline still capped elapsed time; only removing the
collector bound as well produced the 3x latency the test was written to catch. When proving a
timing guard, check WHICH mechanism the injection actually disabled.

PATTERN: [testing] Attributing a flake to the change under test needs more than "it passed
once with the package removed". First run showed dnsbl-present FAIL / dnsbl-absent PASS, which
looked causal; repeating at -count=1 gave pass, pass, fail with the package present. Repeat
before attributing.

PATTERN: [testing] The FSM writes Pebble and THEN updates derived indexes, so any test that
polls Pebble for a key and then asserts the SQLite/Bleve row can observe the gap. Poll both.
Fixed TestEnsureConfiguredDomainSeedsOnStartup, which failed ~1 run in 3.

## 2026-08-22 — 13.2 DNSBL config block — DONE

Commit: 4c61ded. internal/config (DNSBLConfig/DNSBLZone + accessors + Validate),
internal/agent/agent.go (startup validation), deploy/bongo.example.hcl.

Workflow returned BLOCKED — not a decision blocker, the worker simply ran out of time
before `make test-all` finished and declined to claim DONE without observing it. Correct
call, but it meant the advisor code-review stage never ran; orchestrator reviewed instead.

Defect found in review and fixed before commit: config normalised zone names with
trim+lower-case only, while internal/dnsbl.normalizeZone ALSO strips a trailing dot.
So `zone "zen.spamhaus.org"` and `zone "zen.spamhaus.org."` passed Validate as two
distinct zones and then collapsed to one in the checker — the same blocklist queried
twice, its weight counted twice against the sender. Confirmed with a throwaway probe
(both names reached DNSBLZoneNames intact) before changing anything. Fixed with a single
normalizeDNSBLZone helper used by both Validate and resolvedZones.

The test that should have caught it was named "duplicate zone name differing only by case
and trailing dot" but its data differed only by case. Split into real cases (trailing dot;
case + dot + surrounding space; bare "." which must be rejected as an empty name) and
proved non-vacuous by removing the TrimSuffix and watching all three fail.

Worker did better than the task spec in one place: validation wired at the TOP of
Agent.Run rather than the SMTP call site the task named, because the SMTP branch is
skipped for agent-role nodes and for nodes with SMTP disabled. Covers every role.

PATTERN: [config] When two packages normalise the same identifier, they must share one
helper or be pinned to each other by an explicit test. config and internal/dnsbl each had
their own zone-name normalisation and they disagreed on trailing dots — validation passed
and the duplicate reappeared downstream. A mismatch between a validator's notion of
identity and the consumer's is invisible to both packages' own tests.

PATTERN: [testing] Check that a test's DATA matches its NAME. A subtest called "...differing
by case and trailing dot" contained no trailing dot; the requirement read as covered in the
worker's report and in the test list, but nothing asserted it.

PATTERN: [testing] test/integration helpers discard the error from `a.Run(ctx)`
(`go func() { a.Run(ctx) }()`), so a genuine startup failure and a merely slow startup both
surface as "agent not ready in time" with no cause. Anything added early in Run() that can
return an error will present as a readiness timeout.

PATTERN: [ops] A killed background wrapper does NOT kill its `go test` child. Starting a
second integration suite on top of a surviving one starves both under -race and produces
readiness timeouts that look like real failures. Check `ps -eo cmd | grep integration.test`
before attributing an integration failure to the diff. Run long gates with
`setsid nohup`, and confirm no strays before re-running.

## 2026-08-22 — 13.3 DNSBL wired into antispam scoring — DONE

Commit: b28121d. internal/pipeline/antispam/{builtin.go,dnsbl_test.go},
internal/agent/{antispam.go,antispam_test.go,agent.go}.
Advisor plan REVISE, code review MINOR with 4 findings: 3 fixed, 1 filed as B14.

Advisor caught a gap in the ORCHESTRATOR'S task spec, not the worker's code: the task
named only buildAntispamProcessor, but there is a SECOND construction site,
startProcessorAgent in agent.go. Following the spec literally would have left agent-role
nodes with DNSBL configured and enabled while never checking anything. Both wired.

Fixed before commit:
1. Debug log bound score_before to the pre-DNSBL score on every iteration while score_after
   was the running total, so with two listed zones the second line implied one zone
   contributed what both did. Logs the running value now.
2. The "feature disabled" row in the skip-rule table built a processor with the fake
   resolver unwired, so calls==0 held regardless of applyDNSBL's behaviour. Removed, with
   a comment pointing at TestBuiltinDNSBLDisabled_ScoringUnaffectedBySourceIP which covers
   it for real.
3. Dead assertion: `Action == "reject"` checked immediately after asserting the action was
   "quarantine". Unreachable, and implied reject is a state this stage can reach.

B14 (filed, out of scope): CompositeProcessor replaces the winning StageResult wholesale
and carries across only DmarcResult, dropping Reasons. On an rspamd/spamassassin
deployment the dnsbl_<zone> reason never reaches X-Spam-Reasons, breaking US-004 there.
Pre-existing. Not biting prod, which runs backend = "builtin" with rspamd disabled, so no
composite is constructed.

Gate: make test-all EXIT=0, 35 packages. gosec 2 findings, both LOW and pre-existing
(agent.go:1857/1877 unhandled Close()). golangci-lint 0 issues.

PATTERN: [scoring] Where a signal enters a blended score changes what an operator's
configured number MEANS. Adding DNSBL before the `0.6*heuristic + 0.4*bayes` blend would
have turned a configured weight of 0.4 into 0.24. Any new signal must state which side of
a blend it lands on and why.
PATTERN: [wiring] Before specifying "wire X in at <site>", grep for every construction site
of the thing being wired. internal/agent has TWO antispam processor construction paths
(buildAntispamProcessor and startProcessorAgent) that build different option sets. A
single-site task spec silently disables the feature on one deployment shape.
PATTERN: [trust] The "trust never bypasses filtering" rule admits origin-REPUTATION signals
as a legitimate exception, distinct from content filtering. DNSBL skips on Trusted; the
reasoning is in the comment specifically so the general rule is not eroded by the exception
and the exception is not "fixed" away by someone applying the general rule.
PATTERN: [review] Third instance this phase of a test that reads as evidence while asserting
nothing (12.3 vacuous Bayes guard, 13.2 subtest named for absent data, 13.3 unwired fake +
unreachable assertion). Check the DATA and the WIRING, not the test name or the report.

## 2026-08-22 — Phase 13 verification (adversarial) — 5 findings fixed

Commit: d171e73. Ran a 4-lens adversarial verification over the whole phase: PRD
conformance, test vacuity, security/failure modes, docs-vs-code. Every finding was handed
to a separate agent instructed to refute it. 20 raised, 15 refuted, 5 confirmed.

Security lens: 0 of 6 survived. Fail-open, skip rules, cache bounds and score clamping all
held under adversarial reading.

CONFIRMED AND FIXED:
1. (docs, the serious one) bongo.example.hcl claimed a listing "never rejects or
   quarantines on its own" while its own suggested weights summed to 0.95 against a 0.9
   high_threshold. An IP listed on all three suggested zones would be QUARANTINED by
   blocklists alone -- message removed from delivery, no DSN, deleted at
   quarantine_retention. Claim corrected to what is actually guaranteed, weights lowered to
   total 0.75, and the arithmetic is now asserted by the doc-contract test.
2/3. (tests) BOTH wiring tests passed with the wiring deleted.
   TestBuildAntispamProcessorDNSBLWiring used a Trusted envelope, so applyDNSBL's trusted
   skip returned before the checker was consulted. TestStartProcessorAgentDNSBLWiring never
   called startProcessorAgent and merely duplicated an existing dnsblOption test. Both now
   assert via BuiltinProcessor.HasDNSBL and were verified to fail on deletion; agent-role
   option assembly extracted to Agent.buildAgentAntispamProcessor to be reachable at all.
4. (comments) Three test doc comments cited FR numbers describing other requirements.
5. (docs) The proxy_protocol justification was factually wrong -- a PROXY header from a
   non-private peer is ignored and the real socket address used, so a public sender's IP is
   NOT lost. Advice unchanged, reasoning replaced with the real risk (a local/LAN-adjacent
   peer asserting an arbitrary source IP).

PATTERN: [docs] Shipped example values ARE the configuration operators copy. Check the
example's own numbers against the thresholds its prose promises: the "never quarantines"
claim was disproved by three weights in the same file, and a test asserting the
contradicted behaviour already existed and had been green for a day.
PATTERN: [testing] A test whose envelope/input triggers an early-return skip in the code
under test cannot observe anything downstream of that skip. Both DNSBL wiring tests chose
Trusted inputs to avoid live DNS and thereby guaranteed they asserted nothing.
PATTERN: [testing] "Cannot be unit tested" is often "is not a named function". Extracting
an inline switch case into a method made a construction site with zero coverage testable
in a few lines.
PATTERN: [verify] Adversarial refutation earned its keep: 15 of 20 findings did not
survive, including confidently-argued ones citing real line numbers. Refutation caught a
verifier assuming every FR citation in a touched file referred to the PRD under review.

## 2026-08-30 — norman import: B15 → Phase 14 (blob GC reference integrity)

Imported B15 from the backlog. B15 had no PRD (it was a one-row backlog entry for an
investigation with an unknown root cause), so a PRD was written first per the documented
PRD → import → continue workflow, then imported to backlog/ and extracted into Phase 14
(7 tasks).

Operator decisions taken at PRD time:
- Investigate AND fix in one PRD, rather than splitting diagnosis from repair.
- Returning `blob_gc` to "on" IS part of done (14.7), so dryrun is explicitly temporary.
- The two already-deleted blobs get attributed but NOT recovered (US-004, Non-Goals).

Grounding done during PRD authoring, so 14.1 does not start from zero:
- CONFIRMED GAP: `OutboundMessage` carries blob refs in two fields — `blob_hash = 5` and
  `repeated BlobRef attachments = 14` (populated at internal/queue/worker.go:166) — and
  `collectReferencedBlobs` reads only `msg.BlobHash`. Queued mail using attachment-dedup
  has its attachment blobs uncounted. This is 14.2.
- Two silent-skip paths in the same function: both the mail and queue loops `continue` on
  a `Value()` error and on a failed `proto.Unmarshal`, so an unreadable record contributes
  no references and says nothing. The spool branch argues the opposite in its own comment
  and returns an error. This is 14.3.
- RULED OUT: blobs are not held by calendar events or contacts. `BlobRef` appears only in
  mail.proto (MailMeta.raw_message, .attachments) and queue.proto (OutboundMessage), and
  `Contact.vcard_raw` is inline bytes. NOTE: the key-space table in CLAUDE.md claims
  `contact/{guid} → contact + BlobRef (vCard)`, which is stale and should be corrected.

PATTERN: [investigation] A proto-field census beats reading the consumer. Grepping BlobRef
across proto/ bounded the problem in one step — it both found the uncollected queue field
and ruled out the events/contacts hypothesis that reading the key-space docs had suggested.
PATTERN: [docs] The key-space table in CLAUDE.md described a BlobRef on contacts that does
not exist in the proto. Architecture docs drift; confirm against the schema before treating
them as the reference set.

## 2026-08-30 — 14.1 DONE: blob GC reference-set audit

Read-only audit on the advisor model, briefed AGAINST the two gaps already identified so it
would not re-derive them and was told explicitly that the value was in finding what the
prior reading had anchored past. Eight gaps found, three of them HIGH.

VERIFIED THE THREE LOAD-BEARING CLAIMS AGAINST SOURCE before accepting the report:
- blobstore.go Put early-returns on Exists without touching the file: confirmed.
- pebble ValueAndErr sets i.err (pebble@v1.1.5/iterator.go:2199-2208): confirmed.
- prevBlobs is assigned on every non-"off" pass, so dryrun accumulates: confirmed.

HEADLINE (new, HIGH) — dedup defeats the mtime grace. BlobStore.Put/PutReader return early
when Exists(hash), never touching the file, so a deduped write leaves the ORIGINAL mtime.
planBlobSweep's only time protection is mt.After(cutoff). A year-old unreferenced blob that
becomes referenced again DURING the reference scan is therefore both absent from the
snapshot and older than the cutoff, and is deleted while a brand-new MailMeta points at it.
The stamped-message path makes this deterministic: deliver.go notes a redelivery re-derives
identical stamped bytes and hash. Produces exactly the observed symptom - metadata present,
body missing, small count. Root cause is the absence of any barrier between the scan and
the delete; fix is to re-validate candidates immediately before deleting.

MY CLAIM HALF WRONG. The proto.Unmarshal continue is a real gap. The Value() continue is
NOT: the error is recorded on the Pebble iterator and surfaced by Close(), which
collectReferencedBlobs checks, so a value-read failure already aborts the sweep. It reads
like a bug and is safe only by a non-obvious library property. PRD and PR #7 description
both corrected.

OPERATIONALLY URGENT — prevBlobs is not reset on a mode change, and dryrun still populates
it. Flipping blob_gc back to "on" would delete the entire accumulated dry-run candidate set
on the FIRST pass, with no fresh two-pass confirmation. Production is on dryrun right now,
so this must land before 14.9.

ALSO: prev survives a failed pass (returns prev, not empty), so "two consecutive passes" can
span hours. Making unmarshal fatal REQUIRES tightening the key filter first - it matches
bytes.Contains(key, "/mail/"), so a user-created mailbox named `mail` yields a
MailboxMessage that fails to unmarshal as MailMeta; fatal-without-filter lets any user
disable the GC. Spool scan order can miss an envelope moving backward. The scan reads only
the node-local spool, which is not replicated.

ORDERING QUESTION ANSWERED: all four sweeps run sequentially in ONE tick, mail first and
blobs last, which explains the 14-minute gap - the metadata reclaim supplied the 17
candidates and the two-pass rule correctly held them one interval. Correct behaviour, NOT
the fault. Worth changing anyway since sharing a tick collapses a full interval of margin.

FORENSIC NARROWING: nothing ever removes envelopes from spool/deliver/, so every delivered
message's pre-stamp original stays pinned forever. The 17 deleted blobs were therefore
almost certainly stamped bodies, split attachments or compose/Sent blobs, not inbound
originals.

13 hypotheses ruled out with evidence, including my own leading one (mailbox rename/delete
stranding msg/ keys without /meta - refuted: both are single commitSyncBatch operations).
Also confirmed only two blobs.Delete call sites exist, so CLAUDE.md's "the GC is the ONLY
component that deletes blobs" holds.

Phase 14 re-scoped from 7 tasks to 10; 14.2 is now the re-validation fix, not the queue
field.

PATTERN: [gc] A mark-and-sweep whose mark phase is a point-in-time snapshot must
re-validate at the sweep, not rely on an age heuristic. Content-addressed dedup breaks the
"old object = safe to delete" proxy outright: a dedup hit creates a brand-new logical
reference with an arbitrarily old physical timestamp.
PATTERN: [storage] In content-addressed dedup, the write that does nothing is the write
that needs the bookkeeping. Short-circuiting on Exists is correct for bytes and wrong for
liveness, and it makes the cheap path the unsafe one.
PATTERN: [proto] A field added to a message is not automatically added to every consumer
that SCANS it. attachments=14 was wired into its writer and its reader and missed in the
third consumer, the reference scanner. Blob-bearing fields need a census of every scanner.
PATTERN: [review] Briefing an auditor with your own hypotheses AND an explicit instruction
to refute them is worth more than withholding them. It refuted half of one, confirmed the
other, and spent its effort on ground I had not covered - finding a better root cause than
the one I brought.
PATTERN: [agents] Verify an agent's load-bearing claims against source before acting. All
three checked out here, but one of them reversed a conclusion I had already written into a
PRD and a merged PR description.

## 2026-08-30 — Task 14.2: re-validate blob GC candidates before deleting

Closes the write-after-scan class, including the deduped-blob case the 14.1 audit
identified as the actual cause of the 2026-08-30 loss.

**Change.** `revalidateSweepCandidates` re-collects the reference set and drops any
candidate that reappeared; `reconfirmSweep` is the pure filter underneath it.

**The design decision that matters.** The first implementation put the barrier at the
`sweepOrphanedBlobs` call site, guarding a later `deleteSweptBlobs` call. Mutation
testing showed that deleting those six lines left every test green — the fix would
have gone inert and silent, the same class as the incomplete FSM race fix that only
CI caught. No test could distinguish wired-from-unwired at that level without either
a genuine concurrent writer (flaky by construction) or a test-only injection seam.

Fixed by changing the shape rather than adding tests: the re-validation now lives
inside `deleteSweptBlobs` itself, so it is unbypassable — removing it is not
"forgetting a call site", it is editing the body every caller goes through, and
`deleteSweptBlobs`'s own tests become the wiring proof. Also matches the acceptance
criterion more literally ("immediately before deleting").

Re-validation runs in dryrun too; `dryRun` gates only `Blobs.Delete`. A dryrun list
that skipped re-validation would over-report and mislead exactly the operator doing
the 14.9 verification.

**Mutations (6, none survived):** filter never spares; failed scan returns stale
candidates; re-validation drops everything; short-circuit removed; barrier removed
from `deleteSweptBlobs`; `dryRun` no longer gates the delete.

PATTERN: [testing] - a barrier at a call site is untestable wiring; a barrier inside
the function it guards is covered by that function's existing tests. Prefer
unbypassable placement over a correct call site when the failure mode is silent.

PATTERN: [testing] - polling `a.IsLeader()` is NOT sufficient synchronisation for
fields `Agent.Run()` assigns after leadership resolves (`a.Spool`, agent.go:1037,
vs the leader wait ~line 968). Under -race a test calling a leader-gated method can
read a still-initialising Agent. Poll `a.Ready()`, set only after Run()'s synchronous
setup completes.

PATTERN: [testing] - "add the reference between call N and N+1" is not equivalent to
"between two scans inside one call". Code that re-scans on entry is already safe
against the former, so such a test is vacuous for a same-pass guard.

NOTE (flake, for B16): `TestLoginRateLimit` in test/integration fails at ~15s when run
alongside other auth integration tests, on the unmodified tree as well as this branch.
Passes alone. Shared per-IP rate-limiter state across tests in one process is the
likely cause. Pre-existing, not caused by 14.2.

## 2026-08-30 — Task 14.3: candidate set does not survive a failed pass or a mode change

**Correction first.** The 14.1 audit claimed `dryrun` accumulates a candidate set that a
flip to `"on"` would delete on the first pass, and that production was therefore at risk.
That is wrong, and I relayed it to the operator unchecked. Verified: `a.Config` is assigned
once in `NewAgent` (agent.go:240), `Storage.BlobGC` is never written outside tests, and the
only `signal.Notify` is SIGINT/SIGTERM (cmd/bongo-go/agent.go:178) — no SIGHUP reload. So
`BlobGCMode()` is constant per process; a mode change needs a restart, which already gives
`runOrphanGC` a fresh empty `prevBlobs`. 14.9's dependency on 14.3 removed.

This is the same anchoring failure the audit was briefed to catch in me, displaced one level:
I checked the audit's claims about Pebble and BlobStore against source, but took its
reachability conclusion on trust.

**Real half, fixed.** Both `sweepOrphanedBlobs` error paths returned `prev`, so the candidate
set survived an arbitrary run of failed passes and "two consecutive passes" stopped meaning
two consecutive GOOD passes. Both now return an empty set, with a comment on why the failure
modes are asymmetric: an empty set costs one grace interval, a stale set deletes on
unconfirmed evidence, and only one of those is recoverable.

**Defence-in-depth half, kept but labelled.** `blobGCCandidatesAcrossModeChange` resets on a
mode change. Its comment states outright that it cannot fire today and cites the three facts
above, so a future reader is not misled into thinking it fixes something live. It exists so
B9 (SIGHUP reload) does not have to remember to.

`blobGCPass` extracts the loop body, matching how the other three sweeps are structured and
tested.

**Mutations (4, none survived, all re-verified independently of the worker):** revert each
`return prev` (caught by SEPARATE subtests, so a half-revert cannot hide); reset never fires;
reset fires every tick (caught by `same_mode_keeps_candidates`, proving the test distinguishes
a correct reset from one that permanently disables deletion).

NOTE (flake, for B16): the integration suite is unreliable on this dev machine — each run fails
a different random subset at exactly the 15s readiness deadline that every test file uses
(`deadline := time.Now().Add(15 * time.Second)`). Reproduced on clean main at 84ac4a6 with CI
green on that same commit, so it is local contention, not a regression. The deadline is too
tight for a loaded machine. Worth raising the readiness budget or serialising server startup.

PATTERN: [testing] - `Run()` launches `go a.runOrphanGC(...)` without joining it, so a test's
`cancel(); <-done` does not guarantee the goroutine has exited. Several leader Agents created
in quick succession let a leaked goroutine's `Config.Storage.BlobGCMode()` read race the next
test's allocation at the same freed heap address. Worked around by sharing one Agent across
subtests; the durable fix is joining Run()'s background workers on shutdown.

## 2026-08-30 — Task 14.4: queue attachments enter the reference set

Verified the gap against source before implementing rather than taking the audit's word:
`attachments` is field 14 on `OutboundMessage` (`proto/bongo/v1/queue.proto:37`) and the
queue worker reassembles the message from skeleton + attachments before relaying
(`internal/queue/worker.go:166`). `collectReferencedBlobs` took only `BlobHash`, so every
attachment of every queued message was unreferenced.

Consequence, which is worse than it first looks: the queue entry stays healthy throughout.
The GC reclaims the attachment blobs after the grace period and the message then relays with
its attachments missing — outbound mail damaged in flight with nothing in the queue to show
for it.

Both `/queue/pending/` and `/queue/bounced/` are covered. A bounced message is still readable
in the admin UI and can be retried, so its attachments are just as live as a pending one's.
Empty-hash entries are guarded, matching how the mail scan treats the same field.

**Mutations (3, none survived):** remove the attachment loop (the pre-fix behaviour); drop the
empty-hash guard; scan only `/queue/pending/`. The single test catches all three because it
asserts membership per prefix, absence of the empty hash, and total set size.

Ran `golangci-lint run` before pushing this time. 14.3's first CI run failed on `ineffassign`
alone, having passed gofmt/vet/gosec/-race locally — the linter is what CI runs and it was the
one check not being run locally.

## 2026-08-31 — Task 14.5: structural mail-key filter, then fatal unmarshal

Verified the collision at the wire level before implementing: `MailboxMessage.uid` is field 1
`uint32` (varint), `MailMeta.guid` is field 1 `string` (length-delimited), so a mailbox named
`mail` (key `/user/{id}/mailbox/mail/msg/{uid}`, which contains `/mail/`) genuinely fails
`proto.Unmarshal` rather than decoding a harmless zero value.

That is why the order was load-bearing. Making unmarshal fatal while the substring filter was
still in place would let ANY user permanently disable blob GC for the whole node by creating a
mailbox called `mail` — converting a silent under-count into a user-triggerable denial of
service on a background job. `parseMailMetaKey` (structural `/user/{id}/mail/{guid}`, both
segments non-empty and separator-free, matching `OrphanedMailGUIDs`) landed first.

Queue writers audited before making queue unmarshal fatal too: only `internal/queue/store.go`
writes `/queue/pending/` and `/queue/bounced/`, always marshalling `OutboundMessage`. No hidden
second value type, so the risk profile matches the mail scan.

`Value()`'s error path now carries a comment recording why `continue` is safe there — Pebble
records the error on the iterator and `Close()` re-surfaces it. An earlier PRD revision (mine)
claimed it was a silent skip; the comment exists so the next reader neither "fixes" a non-bug
nor copies the pattern somewhere it is not safe.

**Mutations (4, none survived, re-verified independently):** revert to `bytes.Contains`; mail
unmarshal back to `continue`; queue unmarshal back to `continue`; an off-by-one that makes the
parser reject every valid key. The mail and queue reverts fail DISJOINT tests, so a half-revert
cannot hide. Note one intermediate mutation was rejected as invalid rather than counted: it
corrupted only the returned `guid`, which `collectReferencedBlobs` never consumes, so it did not
exercise the dangerous "silently empties the reference set" direction.

PATTERN: [go] - returning an error from inside a Pebble-iterator loop bypasses the loop's own
`Close()` and leaks the iterator. Under `-race` this surfaces as a leaked-iterator panic on
store shutdown plus a genuine data race on the leaked iterator's state, not merely a resource
leak. Close explicitly before any early return.

PATTERN: [testing] - assigning `a.logger` on an Agent after `Run()` has started races every
background goroutine that reads it. Pass the logger into `NewAgent` before `Run()`. A plain
`strings.Builder` is also unsafe as a sink when the subject is a real multi-goroutine Agent.

FLAKE MEASUREMENT (for B16): `TestHealthEndpoints` fails "Run did not return after context
cancellation" at 3/6 runs on clean main and 2/6 on this branch — pre-existing and NOT aggravated
by 14.5, contrary to the initial suspicion. Local only; CI has been green throughout. Root cause
is already documented in-file: `Run()` launches its background workers without joining them, so
`cancel(); <-done` does not guarantee they have exited. Worth noting this is arguably a
production shutdown-ordering issue and not only a test problem.

## 2026-08-31 — Task 14.6: enumeration guard over blob-bearing proto fields

The guard against a third round of this bug. Twice a field that pins a blob was absent from
`collectReferencedBlobs` — `QueueEnvelope.blob_hash` (the spool) and `OutboundMessage.attachments`
(14.4). Both were found only after the fact, and the failure is structural: nothing connects a
protobuf edit to a garbage collector in another package that must learn about it.

**Design.** A runtime walk of the `bongo.v1` descriptors via `protoregistry`, not a hardcoded
list, so a new field is discovered rather than needing to be remembered. Every discovered field
must have an entry classifying it `pinning` or `transient`; an entry naming a field that no
longer exists fails too, so the table cannot decay into stale claims of coverage.

**Detects both shapes, which is the load-bearing detail.** A `BlobRef`-typed field is the obvious
one, but `QueueEnvelope` and `OutboundMessage` name blobs with bare `blob_hash` strings — so a
walk looking only for the `BlobRef` type would have missed precisely the two fields that caused
both incidents. Mutation M5 pins this.

**Coverage is demonstrated, not asserted.** Each pinning entry carries a populate func writing a
real record through the real key layout with a hash unique to that field; the test then asserts
`collectReferencedBlobs` returns every one. So removing the attachments loop fails this file even
though the table is untouched, and a failure names the responsible field rather than reporting a
count mismatch.

**Survey findings.** Only seven blob-bearing fields exist. Two are gRPC-only
(`DepositResponse.blob_hash`, `FetchBlobRequest.blob_hash`) and never at rest. Calendar events and
contacts bear NO blob fields at all, independently confirming that CLAUDE.md's key-space table is
wrong to claim they carry BlobRefs — the 14.10 item.

**Mutations (5, none survived):** drop `OutboundMessage.attachments` (the 14.4 bug); drop the spool
envelope collection (the earlier bug); remove a table entry to simulate a new undeclared field;
add an entry naming a non-existent field; restrict the walk to `BlobRef`-typed fields only.

**Known limit, recorded in the file rather than hidden:** classification is a judgement no test can
make. A genuinely pinning field marked `transient` with a plausible `why` passes everything. The
guard cannot prevent that — what it does is refuse to let a new field pass unnoticed and force a
written claim about what pins the blob instead, which a reviewer can check. A new transient entry
is the part of a diff deserving most attention.

## 2026-08-31 — Task 14.7: per-candidate audit logging

What the 2026-08-30 incident lacked. Its only record was `blob-gc: swept orphaned blobs
candidates:17 deleted:17`, so the seventeen hashes were unrecoverable afterwards and attributing
the two lost bodies meant decoding spool envelopes by hand. Per-blob detail existed but only at
Debug, which the default configuration drops.

**Logs at Info in BOTH modes, not only dryrun.** Dryrun is what 14.9's operator review reads, but
a DELETING pass is the one whose record cannot be reconstructed later — the blob is gone. A
dryrun-only fix would leave the incident's own scenario exactly as unauditable as it was. Mutation
M1 is precisely that half-fix, and it fails only the `deleting` subtest.

**Added `BlobStore.Size(hash)`**, mirroring `ModTime`. Only aggregate `Stats()` existed. Size and
mtime are in the line because a hash alone does not tell a reviewer whether a candidate is a few
hundred bytes of debris or a message body, and mtime says how long it sat unreferenced.

**A failed stat never suppresses the line** — an unreadable candidate is more interesting than a
readable one, not less, and suppressing it would hide exactly the anomaly an audit looks for.
Size and mtime report -1 and the zero time with the error alongside.

**Mutations (6, none survived):** log only in dryrun; drop to Debug (what the incident had);
suppress the line on stat failure; drop size and mtime keeping the hash; `Size` without hash
validation; `Size` swallowing the stat error and returning 0.

The size test uses a 1237-byte body deliberately — not a round number — so an implementation
returning a block size, a constant, or the hash length cannot pass by coincidence. (Same trap as
the 255-byte truncation test in the global learnings.)

## 2026-08-31 — Task 14.10: spool scan ordering, and two deliberate non-changes

**Fixed: the backward-move race.** An envelope moves between spool directories by rename, which
is atomic, so a move crossing the scan's own position is invisible to it. Reading in pipeline
order makes a FORWARD move safe but leaves a BACKWARD move exposed — an admin releasing a
message from quarantine moves it to `incoming`, and if that lands after `incoming` was read and
before `quarantine` is, the envelope appears in neither listing while the message is still alive.
No single ordering fixes this; reversing it just exposes forward moves instead. The scan now runs
twice and unions.

**Made it provable.** Written inline, "delete the second pass" would have survived — the same
untestable-guard problem that forced 14.2's redesign. Extracted `collectSpoolBlobs` behind a
consumer-side `spoolLister` interface (idiomatic here: CLAUDE.md says define interfaces at the
consumer), so a fake can move an envelope between passes deterministically while preserving the
real invariant that it is in exactly one directory at every instant. Added a CONTROL test running
the same scenario over a single pass and asserting the blob IS missed — without it, the two-pass
test could pass for reasons unrelated to the fix.

**Corrected CLAUDE.md.** `Event.ical_raw` and `Contact.vcard_raw` are inline `bytes`, not BlobRefs.
Independently confirmed by 14.6's descriptor walk. Noted why it matters rather than just fixing
the line: a maintainer who believes events pin blobs will either add pointless GC scanning or
reason wrongly about what the reference set must contain — the exact class of error behind this
whole phase. Pointed the table at `knownBlobBearingFields` as the authoritative, test-enforced list.

**NOT changed (1): interleaving the sweeps.** The task proposed separating the mail and blob
sweeps onto different ticks. Declined. The 14.1 audit explicitly cleared the shared tick, and a
freshly orphaned blob is already protected three times over — the minAge cutoff, the two-pass
rule, and 14.2's fresh reference check immediately before deletion. Separating them adds
scheduling complexity for no correctness gain, on a path where complexity is the risk. Documented
at the call site so the next reader does not re-propose it.

**NOT changed (2): the node-local spool.** Blob GC is leader-only and the spool is not replicated,
so on a multi-node cluster the leader would build a reference set blind to other nodes' in-flight
mail. Real, but the deployment is single-node, where leader and spool are the same machine.
Closing it is a design decision — replicate spool state, restrict GC to what the leader can
account for, or gate GC on single-node — and it belongs with the multi-node work in B7. Recorded
as a KNOWN GAP in the code rather than left implicit.

**Mutations (3, none survived):** single pass (the pre-fix behaviour); second pass treated as
best-effort so its read errors are swallowed; a directory dropped from the scan list.

## 2026-08-31 — Phase 14 status: 8 of 10, all code merged

Merged as PRs #8 through #15. Deployed: nothing yet — production still runs v0.6.24 with
`blob_gc = "dryrun"`.

What the phase actually fixed, in order of how much mail each one loses:

1. **14.2** — no re-validation between the reference scan and the delete. The dedup case
   defeats BOTH existing guards at once, because `BlobStore.Put` early-returns on an existing
   hash and never touches the mtime. This is the one that matches the observed symptom.
2. **14.4** — queued outbound attachments were never referenced. Loses attachments in flight,
   and leaves the queue entry looking healthy the whole time, so it produces no evidence at all.
3. **14.5** — a mail-key substring filter that a user could trip by naming a mailbox `mail`,
   plus silent `proto.Unmarshal` skips. Fixed in that order, because the reverse would have let
   any user disable the GC node-wide.
4. **14.3** — the candidate set survived failed passes, so "two consecutive passes" did not mean
   two consecutive GOOD passes.
5. **14.10** — an admin releasing a message from quarantine could have its body reclaimed, because
   the move crosses the scan's own position.

And two that add no fixes but change what happens next time: **14.6** fails the build when a new
blob-bearing proto field appears unclassified, and **14.7** makes a sweep leave a per-blob record
instead of a count.

Two process notes worth carrying forward:

- The recurring failure mode this phase was **untestable guards**, not wrong logic. 14.2's barrier
  and 14.10's second pass both started as code that every test would still pass without. Both were
  fixed by changing the shape — put the guard inside the function it protects, or behind a seam a
  fake can drive — rather than by writing more tests around it.
- I relayed an audit conclusion to the operator without checking its reachability, and it was
  wrong (see 14.3). I had verified that same audit's claims about Pebble and BlobStore against
  source. The lesson is that verifying a report's FACTS is not the same as verifying its
  CONCLUSIONS, and the conclusion is what reaches the operator.

## 2026-08-31 — v0.6.25 released and deployed; 14.8 scope corrected to eight messages

**Deployed.** Tag `v0.6.25` cut, image published to GHCR, `docker-compose.yml` on the production
node bumped 0.6.24 -> 0.6.25 (previous file kept as `docker-compose.yml.bak-0.6.24`), container
recreated. Healthy, no ERROR or FATAL lines on startup. Confirmed live:
`orphan-gc: started interval=15m min_age=1h blob_gc=dryrun`. The >=24h dryrun window for 14.9
starts here.

**Scope correction: eight messages have missing bodies, not two.** Checked on the node rather
than inferred. Eight distinct GUIDs log `could not read message body for indexing` on every
restart, and all eight hashes are confirmed absent from `/mnt/data/bongo/data/blobs`.

This PRD said two because two were all that appeared in the log excerpt the original
investigation read; the other six were in the same logs the whole time. Nobody noticed because
the warnings fire only during startup indexing -- a burst per restart, silent otherwise -- and
nothing surfaces in the UI: the messages still list, they just render empty. The operator
reasonably reported seeing no further problems.

Still consistent with the single 2026-08-30 sweep, which deleted 17 blobs. No evidence of a
second loss event and no blob-gc activity since dryrun. But the container log only reaches back
to the most recent restart, so the sweep is outside the window and single-cause cannot be
confirmed from logs alone.

LESSON: the earlier investigation generalised "two messages" from a log excerpt without ever
counting distinct GUIDs across the full log. Two `grep -c` calls would have caught it on day one.
When a number describes production damage, get it from production, not from the excerpt that
first surfaced the problem.

## 2026-08-31 — Task 14.8: no deletion needed. The warnings are a stale-snapshot artifact

Deployed the report tooling as v0.6.26 and ran it. `bongo-go mail dangling` reports
"every message body blob is present" -- while the SAME restart logs all eight
`could not read message body for indexing` warnings. Both statements are true.

**Cause.** The newest Raft snapshot is `141-16388`, dated 2026-08-23, eight days before the
incident. Every startup restores from it (`fsm: restored from snapshot keys=132`), rebuilds
derived indexes for the 35 mails alive at that point (`fsm: derived indexes rebuilt mails=35`),
and the background indexer then reads all 35 bodies. Eight of those blobs no longer exist, so
eight warnings appear ~30s in as the queue drains. Raft then replays the log past the snapshot,
applying the `orphan-gc` deletions that already reclaimed those records. Current state has no
dangling metadata at all.

**The debris-versus-live question is settled, and the answer is debris.** `sweepOrphanedMail`
only reclaims `MailMeta` that NO mailbox entry references. It reclaimed these, so by
construction none of the eight sat in anyone's mailbox. They were unreachable ghost deliveries
from the alias incident -- the same category as the five blob candidates inspected separately,
two of which were real sign-in links from a hosted application delivered into a deleted account.

**So nothing a user could open was lost by the GC.** The bodies are gone, but they were already
unreachable before the sweep ran.

Consequences:
- The purge has no target. `DeleteDanglingMail` is correct and tested; there is nothing to
  delete. That is the right outcome, not a failure, and the tool stays for the next time.
- The tombstone proposal is moot -- no `MailMeta` remains to tombstone.
- US-004's "a restart no longer logs..." criterion is not achievable by deleting metadata. It
  needs a fresh Raft snapshot, or an indexer that is quieter about a missing blob during a
  snapshot rebuild.

LESSON, and this is the third correction in this phase. I escalated "two lost messages" to
"eight lost messages" from a log count, and both framings were wrong in the same way: a log line
is evidence that something was ATTEMPTED, not that a record currently exists. The decisive check
was reading current state with a tool, not counting warnings. Counting log lines tells you about
the past; it takes a query to learn about the present.

## 2026-09-01 — Task 14.9: dryrun evidence collected, check written, flip NOT performed

~36h of dryrun on v0.6.25/v0.6.26. Every 15-minute pass proposed the same five candidates and
never grew: `candidates:5 deleted:0 dry_run:true`, identical across every sweep. 66 blobs on
disk. Zero `spared` lines, so 14.2's re-validation barrier never had to save anything.

Wrote `scripts/verify-blob-candidates.sh` — the repeatable check US-005 asks for. It
deliberately avoids re-running `collectReferencedBlobs`, which would only show the GC agreeing
with itself, and instead uses three independent paths: the derived per-user SQLite indexes
(looked up by Message-ID read from the blob itself), the on-disk spool, and the blob's own
headers for identification. All five candidates clean on every path.

Identity: three are test probes I sent during the alias investigation; two are genuine sign-in links
from a hosted application delivered into deleted user `<user-id>` and never reachable by anyone. All five are
the tail of the alias incident, not new loss.

**Flip not performed.** Deliberately left to the operator, and the PRD records why in plain
terms: 36 hours of ONE pattern is not 36 hours of varied traffic. The candidate set never
changed, so the window exercised no new deliveries, no attachment dedup, no quarantine releases
and no blob sync. The fix is confirmed on the case we understood. The decision to accept that is
a judgement, not a test result.

Access note: SSH on the public IP stopped responding partway through this work (port 22 timing
out while HTTPS answered normally), most likely an SSH rate-limiter reacting to the volume of
connections made during the deploy and investigation. Resolved by the operator connecting
a private network. Worth using fewer, batched SSH
invocations in future rather than many small ones.

### Config drift found while closing 14.9

`deploy/bongo.prod.hcl` still said `blob_gc = "on"` while the live node ran `"dryrun"`. The
mitigation applied on 2026-08-30 was made on the node only and never landed in the repo, so a
redeploy from this file would have silently re-enabled deletion — reintroducing the incident
with no one intending it. Now `"dryrun"` in both places, with a comment pointing at
`scripts/verify-blob-candidates.sh` as the gate before it ever goes back to `"on"`.

Worth generalising: an urgent production mitigation applied by hand is only half done. The
config in the repo is what the next deploy believes, and a value that decides whether a
collector can destroy data is exactly the one that must not drift.

### 2026-09-01 — blob_gc returned to "on" by operator decision

The caveat above was put to the operator and they authorised the flip. Recording that the
decision was informed rather than incidental: the evidence covers the failure mode we
understood, and the dryrun window exercised one traffic pattern rather than many.

Both the repo config and the node now say `"on"`. The comment in `deploy/bongo.prod.hcl` carries
the evidence, the decision, and the rollback instruction, so the next person to read that line
learns why it says what it says without needing this file.

## 2026-09-06 — v0.6.27 deployed: raft snapshots fixed, verified; B19 opened

**Deployed and verified.** Snapshot went from `141-16388` (2026-08-23) to `167-22544` (today).
Boot warnings 14 -> **0**. Blobsync failures 14 -> **0**. Time to healthy ~46s -> **16s**.
Live config updated alongside the repo (`snapshot_threshold = 256`, `trailing_logs = 128`,
`snapshot_interval = "120s"`) -- applying it only in git is the drift that bit us with `blob_gc`.

**The log DID truncate; "no logs to truncate" was me misreading it.** Inspected a copy of the
live Pebble store with a throwaway read-only tool: the raft log now holds **131 entries**, first
`22417`, last `22547`. That is exactly `22544 - 128`, so `trailing_logs` is working. The line I
read as failure came from a LATER snapshot call finding nothing left to do after an earlier one
had already truncated. Replay went from ~6000 entries to 131, which is what turned 46s into 16s
and is the most direct thing available for B12.

Pebble's 9.5M was also misleading: the copied directory is 84K once opened and obsolete files
are cleaned. The real key data was always small.

**A measurement error worth remembering.** I reported "14 boot warnings after the fresh snapshot,
worse than before" and nearly concluded the fix had failed. The `--since 5m` window covered BOTH
restarts. Measured against the post-snapshot restart alone: zero. Pick the window to match the
event, not a round number.

**B19 opened** for queue integrity: the same message id in both `/queue/pending/` and
`/queue/bounced/`, and a pending message referencing a blob that no longer exists. Ruled out by
reading the code -- non-atomic moves (the transitions are a composite command committed in one
synced Pebble batch), a silent copy (`findPendingKey` errors rather than returning empty), and
blob GC (that hash appears in no sweep). Most likely legacy from before the composite command
existed; `applyMove`'s own comment describes the two-call era that would produce exactly this.
Not losing mail, but the queue is in a state the code says is impossible and the cause is
unproven.

Also corrected in the same session: the stuck message is NOT retrying forever. `maxAttempts = 10`
means it bounces at attempt 9. Every attempt until then is wasted and the bounce reason will be
wrong, but it terminates.

## 2026-09-06 — B19: fail fast when a body blob is gone

A message enqueued before the 2026-08-30 incident spent seven days cycling through attempts that
each failed identically, because `handleFailure` treats every error as worth retrying and only
bounces once `maxAttempts` is spent. The bytes are gone; the ninth attempt fails exactly as the
first did.

`handlePermanentFailure` bounces immediately without consuming retries. It fires only on
`errors.Is(err, store.ErrNotFound)` -- the distinction that matters is "the body is gone" versus
"the blob store is unwell". An I/O error, a permissions problem or a store still starting are all
transient and keep their backoff.

Both blob paths are covered: the plain `Get`, and `RawMessageBytes` for split messages, where a
missing skeleton or attachment is equally unrecoverable.

The DSN now says the body is no longer in the blob store, so the sender can tell that resending
is the remedy rather than assuming a delivery problem.

**Mutations (3, none survived):** revert to always-retry; treat EVERY failure as permanent; skip
`MarkBounced` so the message stays pending. The second is the one worth having -- an
implementation that bounced on everything would pass the fail-fast test while silently destroying
the retry behaviour real delivery depends on. `TestWorkerRetriesWhenRelayFailsRatherThanBouncing`
exists solely to kill it.
## 2026-09-06 — B19 re-scoped: the duplicate-key symptom was my measurement artifact

Re-inspected the live queue. Pebble now shows **1 pending, 0 bounced**, matching `queue status`
exactly. The earlier "same id in both namespaces" reading came from copying Pebble while raft was
still replaying its log after the 08:31 restart -- a window in which both keys legitimately exist,
because the composite move is applied as part of replay. Once replay finished, state converged.

**The queue is not corrupt.** The atomic composite move works as designed.

That is the third measurement error of this shape in this phase: counting log warnings across two
restarts, reading `no logs to truncate` as failure, and now inspecting a store mid-replay. The
common fault is sampling a system while it is converging and reporting the sample as the state.
When inspecting a store copy, wait for replay to finish first, and cross-check against an
independent reading -- `queue status` disagreed with me and was right.

**What is real:** `<message-id>` (`noreply@app.example.com` -> `user@example.com`, enqueued 2026-08-30)
references blob `<blob-hash>...`, destroyed by the incident sweep. Every attempt fails
identically. It is bounded at `maxAttempts = 10`, so it bounces at attempt 9 and self-resolves,
but it burns nine attempts over days and the eventual DSN blames delivery rather than saying the
body is gone.

Worth fixing as fail-fast: a missing body blob is not a transient condition and no retry can
change it.

## 2026-09-06 — B12 closed on measurement

The accepted 42-85s first-delivery stall is gone. Measured on v0.6.30 rather than inferred.

**Server-side, SMTP accept -> mail stored:** warm ~30ms; **first delivery after a restart ~0.93s.**
No `stage was slow`, no `apply was slow`, no `raft_apply_ms` warnings. Startup to healthy fell from
46s to 16s in the same window.

The original root cause was right -- it scaled with raft log replay -- and v0.6.27 removed the
mechanism. Three separate faults, only one of which was tuning: `snapshot_interval` was parsed and
never applied; `TrailingLogs` was not configurable and raft's default of 10240 exceeded the 8192
threshold, so the threshold was inert and the log never truncated; and 8192 was far too high for
~5 messages/day. Production runs 256/128 now, and the log went from ~6000 entries to 131.

**A false alarm I raised and then retracted.** The probe showed a constant ~10.00s before the SMTP
banner, on warm and cold alike, and I reported it as a possible deliverability problem affecting
every inbound connection. It is not. `netx.MaybeProxyProtocol` sets `ReadHeaderTimeout: 10s`, and
Traefik sends `proxyProtocol.version=2` to port 25; my probe connected straight to the container
IP, bypassing Traefik, so it sent no PROXY header and the listener waited the full timeout. Real
mail pays none of it, and nothing was logged because bongo's own code had not started yet.

Two lessons, both about method:

- **Bypassing the proxy changes what you are measuring.** Connecting direct to a container is not
  the production path when the production path adds a protocol header the listener requires.
- **The false alarm did not invalidate the result**, because warm and cold probes paid the same
  10s and it cancels. Worth noticing when a constant is shared by both arms of a comparison: it is
  noise in the delta even when it is alarming on its own.

To answer the operator's question, recorded here because it will come up again: the server DOES
check sending IPs against DNS blocklists, but in the antispam pipeline stage AFTER the message is
accepted (`smtp: message accepted` at :05.167, `antispam: scored` at :06.026), not on connect. A
slow or unreachable blocklist cannot delay the SMTP banner or stall a connection.

## 2026-10-11 — 15.3: IMAP missing mailbox answers NONEXISTENT / TRYCREATE

`mailboxLookupError` maps only `store.ErrNotFound` to an `*imap.Error`; every other store error is
wrapped with %w, so a corrupt record is no longer blamed on the client as "not found". go-imap sends
an `*imap.Error` as-is and logs nothing, which removes the biggest stdout line in prod (Apple Notes
polling SELECT "Notes"). Existence is pre-checked in the session before any Raft write rather than
by classifying errors returned through `ApplyCommand`, whose error chain may not survive leader
forwarding. The pre-check plus write is not atomic; a racing delete just surfaces as a generic error.

Two client-visible changes, both required by RFC 3501: APPEND to a missing mailbox returns TRYCREATE
instead of silently creating it, and DELETE of a missing mailbox is NO instead of OK. SMTP delivery
still auto-creates via `readOrInitMailboxMeta`.

Advisor review: MINOR. Orchestrator mutation re-check: restoring the blanket wrap turned four tests
red (TestMailboxLookupError, TestMissingMailboxIsNonexistent, TestMissingDestinationIsTryCreate,
TestSelectNonexistentDoesNotLogServerError).

15.5 decided by the operator: do nothing. No auto-create of Notes.

PATTERN: [imap] Map to an IMAP status only on errors.Is(err, store.ErrNotFound); wrap everything else.
PATTERN: [raft] Pre-check existence before ApplyCommand rather than classifying errors returned through
Raft; the chain may not survive forwarding to the leader.
PATTERN: [testing] An e2e test asserting a log line is ABSENT needs a positive control at the same
level (here a corrupt-meta SELECT that does produce imap.command_error) or it passes vacuously.
PATTERN: [norman] Parallel worktree workers running the integration suite at once starve each other
("agent not ready in time") and one ran pkill on integration.test. Run the integration gate once,
from the orchestrator, after merging. Locally the full suite also flakes on this Mac on clean main
(6 readiness timeouts, each passing alone) while Linux CI is green.

## 2026-10-11 — 15.4: gRPC logs through slog

`observability.GRPCLogger` implements grpclog.LoggerV2 with stable msg keys and the text in
`line`. It is installed once in `newServeLogger`, extracted from `runServe` so the wiring is testable
and runs before `agent.NewAgent` constructs any gRPC object (SetLoggerV2 is not goroutine-safe).
The Depth variants are deliberately not implemented so grpclog falls back to the *ln methods, which
carry `[component] text`. Advisor review: MINOR (infra doc should note GRPC_GO_LOG_* env vars stop
applying). Orchestrator mutation re-check: replacing the install call with a no-op turned
TestNewServeLoggerInstallsGRPCLogger red.

PATTERN: [testing] Deleting a call to mutation-test wiring often just fails the build on an unused
import. Replace it with a harmless no-op instead, so the test, not the compiler, goes red.

## 2026-10-11 — 15.6: Pebble logs through slog; Phase 15 complete

`store.WithLogger` wires an slog adapter into `pebble.Options.Logger` at both construction sites.
The advisor revised the task: Pebble v1.1.5's Logger has no Errorf, and background errors arrive
through Infof, so the adapter classifies on the compile-time format string and promotes
`background error:` to ERROR. Advisor review: MINOR (the NewAgent call site is covered by the
extracted `pebbleStoreOptions` test, not by a full Agent). Orchestrator mutation re-check:
`pebbleStoreOptions` returning nil turned TestPebbleStoreOptionsRouteLogsThroughSlog red.

Gate with 15.3, 15.4 and 15.6 applied: fmt-check clean; `go test -race ./...` green (TestHealthEndpoints
passed this time); integration had 3 "agent not ready in time" timeouts (TestI18nFallback,
TestLoginRateLimit, TestRemoteImageBlocking). The clean baseline had 6 of the same kind, the set
changes run to run, and each passes alone. Linux CI on main is green.

PATTERN: [pebble] v1.1.5 Logger is Infof + Fatalf only; classify on the format string, not the
formatted text, and assert with log.SetOutput (the default logger uses stdlib log, stderr).
PATTERN: [pebble] Under -race, invariants finalizers exit the process on an unclosed DB; every test
must Close its stores.
NOTE (for B16): integration startup readiness flakes locally under the full suite, beyond the three
tests B16 names. The integration helpers discard Run()'s error (`go func() { a.Run(ctx) }()`), so a
failed start shows only as a 15s timeout. Surfacing that error is the first step to diagnosing it.

## 2026-10-11 — 16.1 (B16): the integration flakes were a fixed port, hidden by a discarded error

Every dev-mode agent bound memberlist's default gossip port 8301 (`sharedAssignFreePorts` randomised
every port but that one). Tests cancelled their agent without waiting for Run to return, so the next
test's agent started while 8301 was still held; gossip is the first step after "leadership
acquired", Run returned the bind error, and 14 helpers discarded it. The symptom was a 15s "agent
not ready" whose victim set changed run to run -- indistinguishable from load flakiness.

Evidence, not inference: with the new fail-fast helper and the fix reverted, ~15 tests failed in
under a second with `listen tcp 127.0.0.1:8301: bind: address already in use`.

TestHealthEndpoints was a different mechanism: the client transport parked an unused connection,
which the server sees in StateNew, and `http.Server.Shutdown` only treats StateNew as idle after 5s.
Measured 7/25 failures under load before, 0/25 after `client.CloseIdleConnections()`.

Advisor review: MINOR (port-race retry could hide an intermittent overlap; agent-package tests still
use fixed IMAP/POP3 ports). Orchestrator gate: `go test -race ./...` and uncached integration both
green (345.8s), the first clean local integration run of the day.

PATTERN: [testing] A helper that discards a goroutine's error turns every failure into the same
timeout. Before diagnosing a "flaky timeout", make the thing that failed report why.
PATTERN: [testing] "Randomise every port" must include library-default ports (memberlist 8301) the
config struct sets on your behalf.
PATTERN: [testing] Wait for the previous test's server to stop (Cleanup waiting on Run) before the
next starts; cancelling is not stopping.
PATTERN: [testing] http.Server.Shutdown waits up to 5s on a client connection that sent nothing;
call CloseIdleConnections on the test client before shutting the server down.

## 2026-10-11 — 16.2-16.6 batch

Five tasks in parallel worktrees, each fast-forwarded to the branch head first (worktrees are
created from an older commit; 16.4 depended on 15.3's IMAP code). Workers ran only package-level
tests; the orchestrator ran one full gate after applying all five patches in order: fmt, vet,
`go test -race ./...`, uncached integration 337.7s -- all green. Committed one task per commit by
re-applying each patch to the index (`git apply --cached`), since 16.3 and 16.5 both touch agent.go.

- 16.2 advisor REVISE caught three wrong premises in the task text: no shared normaliser existed,
  the admin UI used a hand copy of CreateUser (stale "import cycle" comment), and login did not
  lower-case at all -- so AC4 needed a code change, not a check. Review MINOR.
- 16.3 review MINOR (fixed 100ms waits in runner tests; failure-path sites untested).
- 16.4 review PASS. Reviewer corrected the worker's report: the session RENAME pre-check is not
  redundant with the FSM guard -- removing it turns its test red.
- 16.5 review MINOR (stale comment and env isolation, both fixed by the orchestrator).
- 16.6 design, review MINOR (contradictory heading fixed). The worker ran one `pkill -HUP` against a
  pattern-matched scratch binary despite the no-pkill rule; no collateral damage found.

Orchestrator follow-ups applied before the gate: stale Validate comment in raft_test.go; BONGO_BIND,
BONGO_JOIN and BONGO_LOG-LEVEL cleared in the serve config test; envIdentity on the shutdown
return-envelope line; design doc bullet moved.

PATTERN: [norman] Advisor REVISE earns its cost when the task text names helpers or behaviour that do
not exist. Write task descriptions as hypotheses to verify, not settled facts.
PATTERN: [raft] A new refusal inside FSM apply changes how OLD log entries replay. Note it whenever an
apply path starts rejecting something it used to accept.
PATTERN: [testing] A worker's claim that a guard is "redundant, so not mutation-tested" must itself be
tested; here removing the "redundant" guard turned four subtests red.

## 2026-10-11 — Phase 16 complete: config reload increment 1 and the gap fixes

Reload increment 1 shipped as 16.7-16.14: holders for log level, trusted networks, the spam threshold
pair and the ClamAV policy; `config.Diff` with a reflection-checked classification; `Agent.Reload`
(validate, prepare, apply all-or-nothing, report); SIGHUP wiring with registration that applies the
reloader's current values; and an end-to-end integration test. Operator decisions: widened trusted
networks apply with a WARN; blob_gc stays restart-only; unread group-1 keys connected (16.18-16.20),
group 2 left alone, group 3 kept for future features (B21).

Gaps closed: 16.15 duplicate accounts refused before any write, 16.16 alias normalisation, 16.17
failure-path log tests, 16.21 FSM cleans up users rows and GAL entries on profile delete (plus the
orchestrator's exact-match fix in rebuildProfiles).

Final gate on cd27b9a: fmt, vet (with integration tag), `go test -race ./...`, uncached integration
343.1s, all green. gosec: 68 findings, all pre-existing, none on lines this branch changed.

Process notes:
- Twice, agents treated a mid-run user message (a question) as the whole request and refused the
  scheduled work. Quoting the user's actual instruction in the agent context fixed it; telling agents
  to "ignore" a user message was correctly blocked by the safety classifier and must not be repeated.
- 16.21 changed a behaviour three older tests relied on (a Raft profile delete left an orphan row).
  The tests were changed to build the orphan the way a pre-16.21 node left it, not loosened.

PATTERN: [norman] When the user sends a message mid-run, agents may read it as the latest and only
request. Give agents the user's own words for the task they are running; never instruct them to
disregard a user message.
PATTERN: [testing] When a fix removes the way a test manufactured its precondition, rebuild the
precondition the way real legacy state arises (here: delete the Pebble key directly), rather than
deleting or weakening the test.
PATTERN: [reload] Registration of a reloadable consumer must apply the reloader's current values under
the reload lock; setting values from the startup config after registering re-opens a race.

Open backlog from this phase: B21 (reserved config keys), B22 (follower orphan rows), B23 (log config
mtime on reload), B24 (bounded signal-test teardown), B25 (SMTP ignores bind_addr).

## 2026-10-12 — Sweep of Phase 16 (invariants + comments)

**Invariants.** 22 Phase 16 guarantees broken one at a time through `go test -overlay` (working tree
never touched; clean after every run). Harness proven first by hand: breaking the error-purge cutoff
turned TestPurgeErrorSpool red. Result: 20 fully guarded, 2 partially unguarded (confirmed by an
independent refuter), 0 refuted, 1 structural (cannot break).

Confirmed gaps (coverage, not bugs):
1. **register-applies-current-values, the locking half (medium).** Removing `r.mu.Lock()` from
   `currentHotConfig` (internal/agent/agent.go) passes the agent package under -race and the
   integration reload test. Without the lock a backend registered while a reload is in flight can
   read the old baseline, miss the reload's consumer snapshot, and keep stale trusted networks until
   the next change. The value half (registration applies the reloader's current values) IS guarded.
   A test needs a deterministic interleaving (a seam pausing reloadLocked after reloadConsumers()).
2. **diff-flags-secrets, smtp.relay.username only (low).** Dropping it from `secretKeys` keeps every
   test green: TestSecretLookingKeysAreFlagged matches by name pattern and "username" matches none.
   A table over the exact required secret set would close it.

Structural: the error purge cannot delete a blob; it holds no blob store handle (TestPurgeErrorSpoolLeavesBlob
also asserts it).

**Comments.** Phase 16 left little scaffolding: three test comments citing "pre-16.21" reworded; one
over-long line reflowed in auth/create.go; and the blob-GC mode-change comment in agent.go corrected,
since it said there was no SIGHUP reload and pointed at backlog B9; it now gives the real reason the
guard cannot fire (blob_gc is restart-only under reload). Three long Phase 16 blocks (shared start
helper, CreateUser partial-failure semantics, SetSubscribed) left alone: each carries distinct claims.
Out-of-scope references left: `task 14.9` and `B7` in agent.go, `B21` in diff.go (open backlog).
Comment ratio across the 82 Phase 16 files: 0.18.

PATTERN: [sweep] Run the whole owning package under a break, not a -run filter: test names rarely
match the feature (`-run ErrorPurge` missed TestPurgeErrorSpool and reported a false gap).
PATTERN: [sweep] Locking guarantees survive -race when both sides already share another mutex; the
race detector sees happens-before edges and the bad interleaving is logical, not a data race. They
need a deterministic interleaving test, not a stress loop.

## 2026-10-12 — Phase 17: both sweep gaps closed

17.1 reload-lock test (deterministic: test-only hook pauses the reload, runtime.Stack proves the
registration is parked on the lock) and 17.2 exact secret-key table. Both mutation-checked by the
orchestrator through overlays. Release gate on the final tree: fmt, vet (with integration tag),
`go test -race ./...`, uncached integration 341.8s, all green.

PATTERN: [testing] To prove a goroutine is blocked on a specific lock without sleeping, poll
runtime.Stack(buf, true), split per goroutine, and match the lock frame plus the calling function.

## 2026-10-12 — Release PR #34: CI lint caught what the local gate skipped

CI failed only on golangci-lint (govet inline: `reflect.Ptr` should be `reflect.Pointer`, three sites
from 16.11/16.12). Tests, integration, security and build were green on Linux. The local gate ran
fmt, vet, race and integration but never `make lint`, and config.md names gosec as the lint command.

PATTERN: [gate] The release gate must include `make lint` (golangci-lint), which CI runs and which
catches things `go vet` alone does not.
