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

Host setup (see memory `deploy-example-host-test`): `/usr/local/sbin/verify-restore.sh` and
`/usr/local/bin/bongo-go`, both root-owned, with `(root) NOPASSWD:` on the script only. Re-run with
`ssh example-host 'sudo -n /usr/local/sbin/verify-restore.sh --latest'`.

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
