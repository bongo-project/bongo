# Progress Log

## 2026-07-03 — init
Initialized norman for the security-review follow-ups (docs/security-review-2026-07-03.md).
9 tasks in Phase 1. Prior context: Blocker+High findings already fixed and pushed
to main (SMTP relay policy, XFF, conn cap, HMAC body, cookies/HSTS, CSRF, PROXY
protocol, single-node server-mode fixes).

PATTERN: [smtp] Session relay policy lives in internal/smtp/server.go; real client
IP available via PROXY protocol (internal/netx). Backend has a per-IP conn counter
map (perIP) already — reuse/extend for rate limiting.
PATTERN: [headers] sanitizeHeaderValue(v) strips CRLF in internal/smtp/server.go —
reuse the same approach for compose/bounce header building.
PATTERN: [config] All config blocks are REQUIRED by strict hclsimple decoding;
adding fields is fine but every block must be present in config files.

## 2026-07-03 — 1.1 DONE (M3 inbound rate limiting)
internal/smtp/inbound_ratelimit.go (+test), server.go, config.go. Config: smtp.limits.messages_per_minute (def 60), max_connections_per_ip (def 50). 451 transient on exceed; submission exempt. Orchestrator fix: wired opportunistic prune() into Allow (map was unbounded). commit 44dfbfc.
PATTERN: [rate-limiting] inject nowFn for deterministic window tests; sweep map every N calls to bound memory without a background timer.

## 2026-07-03 — 1.5 DONE (H4 non-root container)
deploy/Dockerfile, entrypoint.sh (new), docker-compose.yml, README.md. Non-root uid 10001; entrypoint chowns /data /logs /certs then su-exec drops; setcap cap_net_bind_service on binary. Dropped no-new-privileges (kernel-incompatible with file caps). Host must chown secrets/dkim.key + logs/ to 10001.
PATTERN: [container] no-new-privileges and setcap file-caps are mutually exclusive for non-root privileged-port binding; omit no-new-privileges when using setcap+su-exec.

## 2026-07-03 — 1.9 DONE (CI publish to GHCR)
.github/workflows/publish-image.yml (new), deploy/docker-compose.yml (image: ghcr.io/lhaig/bongo-go:latest, build block removed), README §6 (pull instead of build). Triggers push main + v*.*.* tags. commit a82e670.

## 2026-07-03 — 1.2 DONE (M1 CRLF header hardening)
internal/mail/header.go (+test), bounce.go (+test), handlers/compose.go (+compose_inject_test.go). Shared SanitizeHeaderValue/SanitizeHeaderFilename in internal/mail. commit pending.
PATTERN: [test] to assert no injected header, split output on \r\n and compare whole lines, not substring Contains (avoids false positives).

## 2026-07-03 — 1.4 DONE (M2 blob hash validation)
internal/store/blobstore.go (+test), handlers/blob.go+mail.go (+tests), cluster/blobsync.go. store.IsValidHash manual 64-hex check; store guards + handler 400; shortHash for logs. commit pending.

## 2026-07-03 — 1.3 DONE (H3 loud TLS) + 1.10 DISCOVERED/DONE (raft first-boot)
1.3: internal/security/tls.go (LoadCert, SelfSignedTLSConfig) + agent.go mailListenerTLS; 4 cases, no silent self-sign for server/edge, first-boot tolerant. commit done.
1.10: raft.go tolerates absent cluster.tls cert (warn + plaintext) instead of hard-crash on first boot. Found via smoke test of the REAL prod config (cluster.tls shared with imap/pop3, points at not-yet-dumped cert). Full first-boot server-mode boot now verified: no crash, raft+imap warn. commit done.
PATTERN: [deploy] first-boot with a sidecar-provisioned cert: EVERY listener that reads that cert path (raft cluster.tls AND imap/pop3) must tolerate absence (warn+fallback), else one hard-fail crashes startup before the cert is written.

## 2026-07-03 — 1.6 DONE (M5 relay AUTH STARTTLS guard)
config.go SMTPRelay.AllowInsecureAuth; relay.go shouldSendAuth guard (refuse creds without TLS). commit done.

## 2026-07-03 — 1.7 DONE (Part A) / Part B deferred
config/size.go ParseSize + MaxMessageSizeBytes; smtp server.go uses it. MIME parse-once DEFERRED (Processor interface change too invasive for a perf-only item). commit done.

## 2026-07-03 — 1.8 DONE (per-purpose key derivation)
security/derive.go DeriveKey (HMAC-SHA256); applied in internal_http_auth (Sign+Verify), middleware/auth.go sign, handlers/security.go signPayload. blob-sync covered by internal-auth. Full -race suite + LIVE user-create round-trip (correct secret OK, wrong secret 401) verified. commit done.
PATTERN: [security] apply key derivation at the single sign/verify choke point per purpose so all callers stay consistent with zero call-site edits.

## 2026-07-12 — Phase 2 foundation batch (2.1, 2.2, 2.4)

Ran via norman Workflow (isolate=true, advisor=always). All three DONE; advisor caught a real bug (proto field 7 was already timezone -> revised to 14).

- 2.1 feat(preferences) 8a92763: signature_html field 14; buf regen (buf.yaml/buf.gen.yaml added earlier, 7c53b4e); PreferenceStore round-trips via whole-struct marshal (no per-field code). Test: TestPreferenceStore_SignatureHtmlRoundTrip.
- 2.2 feat(sanitize) 8cd140f: internal/web/sanitize package on bluemonday; img src limited to data:/cid: (cid: needs AllowURLSchemes, not just Matching regex); 5-case table test.
- 2.4 feat(web) 4e490fe: richeditor.js (window.RichEditor pure helpers, 1MB cap) + CSP-safe Alpine richEditor in bongo.js + layout script-load; node --test 16/16.

PATTERN [proto]: PreferenceStore Get/Set marshal the whole UserPreferences, so new scalar proto fields round-trip with zero store code — only the generated struct changes. Use next free field number (never reuse). buf rewrites the protoc header to (unknown); restore to v6.33.4.
PATTERN [sanitize]: bluemonday cid: scheme must be whitelisted via AllowURLSchemes globally; a Matching(regex) on img[src] alone is insufficient for non-standard schemes.
PATTERN [workflow]: Workflow `args` may arrive as a JSON string — guard with `const A = typeof args === 'string' ? JSON.parse(args) : args`.
NOTE [scope]: 2.4 worker over-reached into compose.templ (2.6) and i18n json (2.8); those changes were dropped at merge — only the reusable component landed. Watch for worker scope-creep; merge selectively.

## 2026-07-12 — 2.3 MIME builder (feat 091a703)

Non-isolated batch of 1 (switched off isolation: workflow worktrees were basing off stale commit 169da3b, missing in-session foundation commits). DONE, advisor PASS.
- compose_mime.go: buildMIME(mimeParts) selects text/plain | multipart/alternative | related | mixed; buildRFC5322WithAttachments now a thin wrapper (plain path byte-compatible). extractDataURIImages: data:->cid:, deterministic Content-ID from sha256(bytes), dedup, media-type whitelist (png/jpeg/gif/webp) to block header injection (advisor-flagged risk). 7 passing tests.
- Not wired into send handler yet (2.6).

PATTERN [mime]: struct-driven builder — buildAlternativeBody/buildRelatedBody each return (contentType, body []byte) so nested parts embed cleanly. Content-ID header is <id> (brackets, RFC 2392); cid: src is bare. Tests buffer all multipart.Part bodies eagerly (Part.Body is consumed on NextPart()).
PATTERN [workflow]: isolation worktrees base off a stale commit in this harness — for tasks depending on just-committed in-session work, run non-isolated (isolate:false) sequential batches so workers see current HEAD.
NOTE [strict-scope rule worked]: adding an explicit STRICT SCOPE clause to projectRules kept 2.3 within builder+helper+tests (no UI/i18n creep, unlike 2.4).

## 2026-07-13 — 2.5 Settings HTML signature (feat f5f0539)

htmx-alpine-pro, non-isolated, DONE/MINOR. (Prior run died on session usage limit — no code written; retried cleanly.)
- Generalised richEditor: _hidden() reads data-field-name (default 'body' via RichEditor.resolveFieldName); init() seeds data-initial-html via innerHTML (HTML precedence). Backward compatible for 2.6 compose (default body).
- settings.templ Composition: richEditor for signature, hidden name=signature_html, data-initial-html from prefs.SignatureHtml.
- SaveSettings: sanitize signature_html via internal/web/sanitize; derive plain Signature from the SANITIZED html (advisor-flagged: must derive post-sanitize so <script>alert(1)</script>Hi -> 'Hi', not 'alert(1)Hi'). Go tests + 22 node tests.

MINOR follow-up: SaveSettings guards `if sigHTML != "" ... else if sig != ""`, so an emptied signature can't be cleared (old value persists). Address in 2.8 polish or a small fix — prefer detecting form field presence over non-empty.
PATTERN [test]: settings write-then-read handler tests need a real Pebble store + write-through directApplier (stubApplier in preferences_test.go doesn't persist); follow mail_tags_test.go setup.

## 2026-07-13 — 2.6 rich compose body (feat b0c4ff0)

golang-pro, non-isolated, DONE/MINOR. Advisor caught two blockers pre-implementation: real sanitizer is sanitize.Compose (not Sanitize); and prefill+append would double the signature. Worker resolved both.
- compose.templ: two mutually-exclusive <template x-if="bodyMode==='plain'/'rich'"> blocks — plain renders a real <textarea name=body>, rich renders richEditor with hidden name=body. x-if removes the inactive element from the DOM so exactly one body field submits (no double-sig, no Alpine scope clash).
- Send(): body_mode branch. rich -> sanitize.Compose -> extractDataURIImages -> append SignatureHtml to html part + plain sig to derived text (reuses plainTextFromHTML from preferences.go) -> buildMIME multipart/alternative. plain -> unchanged text/plain.
- 4 handler tests (rich alternative, plain single, omitted->plain, quoted body). Full -race suite green.

PATTERN [alpine-csp]: for a plain/rich editor toggle, use two <template x-if> branches (not x-show) so the inactive input is removed from the DOM and only one same-named field submits.
MINORs deferred to new task 2.10: (b) new-message+plain loses signature (composeInitialBody returns "" for new, plain Send appends none); (c) SaveDraft always plain so a rich draft stores innerHTML as text/plain and reopens as raw HTML. Plus (a) from 2.5 (can't clear signature).

## 2026-07-13 — 2.7 inline image size caps (feat 8fccf98)

golang-pro, non-isolated, DONE/MINOR (lint). Advisor PROCEED with good risk notes (signature change compiler-safe; orphaned attachment blobs on reject acceptable via GC; dedup counts unique images once).
- compose_mime.go: perInlineImageCapBytes=1<<20, totalInlineImageCapBytes=10<<20, checkInlineImageCaps + sentinel errors; buildRichMessage returns err; Send maps to 400 before writes. Comment: inline images not blob-stored separately (already in referenced raw-message blob).
- Orchestrator fix: sentinel error strings were capitalized (ST1005) — lowercased them and mapped to friendly capitalized 400 messages in Send (added errors import). Tests assert body Contains the phrase, so still green.
PATTERN [lint]: Go sentinel error strings must be lowercase (ST1005); when the same text is user-facing, keep the sentinel lowercase and capitalize at the http.Error boundary via errors.Is mapping.

## 2026-07-13 — 2.8 shared RichEditorField + i18n/a11y (feat 70b0622)

htmx-alpine-pro, non-isolated, DONE/PASS. Advisor's dedupe risk (compose must keep name=body, no signature seeding on new msgs, 200px) handled: @RichEditorField("body","","200px","") for compose, @RichEditorField("signature_html",prefs.SignatureHtml,"90px",...) for settings; richeditor_test.go asserts both field names render. 11 i18n keys x4 locales; role=textbox/aria-multiline on contenteditable.

NOTE [design]: signature application differs by mode — RICH appends the HTML signature server-side in buildRichMessage (editor stays empty while composing); PLAIN relies on prefilling the plain signature into the textarea. composeInitialBody returning "" for new broke the plain path (bug 2.10b). Rich compose editor is seeded empty (initialHTML="").
Retargeted 2.10 to fixes (a)+(b); split (c) rich-draft round-trip into 2.11 (more involved draft-storage change, lower priority).

## 2026-07-13 — 2.9 end-to-end integration test (test 31cf517)

golang-pro, non-isolated, DONE/MINOR. Advisor plan overrode several AC errors (createTestNode helpers only serve /internal raft routes — must use agent.NewAgent like webui_test.go; needs `-tags integration`; resolve userID by prefix-scanning /user/*/profile not /system/alias; select Sent by unique subject; signature must contain text). Test passes: TestRichComposeEndToEnd {rich w/ inline image+signature, plain single part}.
- All 9 core Phase 2 tasks complete (2.1-2.9). 2.10 signature fixes done. Only 2.11 (rich-draft round-trip, lower priority) remains TODO.
PATTERN [integration]: web-UI integration tests need agent.NewAgent + a.Run (dev role, admin@localhost/admin) — the raft createTestNode/sharedStartHTTP helpers only expose /internal control-plane routes. Run with `go test -tags integration ./test/integration/`.

## 2026-07-17 — 2.12 close verify test gaps (test 0a74125)

golang-pro, DONE/PASS. Added TestComposeSendPlainMode_EmptyHTMLSignature_PlainFallback (US-002) and TestComposeSendRichMode_SignatureInlineImage_RoundTrips (US-005) to compose_rich_send_test.go. Advisor's honest caveat recorded: plain Send never appends a signature (prefill is client-side), so GAP1 documents plain-fallback structure, not server-side suppression. norman verify now 11/11 PASS.

## 2026-07-18 — norman import: multi-domain hosting

Imported prd-multidomain-hosting.md (research → backlog). Extracted 14 tasks across 4 phases
mapped to PRD Phases A–D:
- Phase 3 (A) Foundation & quick wins: 3.1 sender hygiene, 3.2 per-domain DKIM registry, 3.3 admin DKIM key mgmt.
- Phase 4 (B) Receiving: 4.1 address index, 4.2 migration, 4.3 unify delivery (alias inbound fix), 4.4 catch-all, 4.5 address mgmt UI.
- Phase 5 (C) Sending identity: 5.1 send-as, 5.2 verification (both paths).
- Phase 6 (D) Tenancy: 6.1 ownership+grants, 6.2 isolation/authz, 6.3 quotas, 6.4 adversarial isolation tests.

DECISION: recommend executing Phase 3 first (independent, low-risk, unblocks hosting-for-sending). Phases 4→5→6 are sequential; 6 is the security-critical bulk.
PATTERN: [dependency] - address index (4.1) is the keystone for delivery (4.3), address mgmt (4.5), and send-as (5.1); tenancy ownership (6.1) gates isolation (6.2) + quotas (6.3).

## 2026-07-18 — Task 3.1 DONE (FR-11 sender hygiene)

Worker: golang-pro (sonnet). Advisor plan review REVISE→folded in; code review PASS.
- Added `mail.ResolveServerDomain(dkimDomain, smtpHostname)`: DKIM domain → EffectiveTLDPlusOne(hostname) → hostname → os.Hostname() → "localhost".
- `BounceGenerator` gains a `domain` field (wired in agent.go:1007); fixed ALL four localhost sites: From postmaster@, Message-ID, MAILER-DAEMON envelope, Reporting-MTA.
- Welcome email: extracted pure `buildWelcomeEmail(...)`; both the header Message-ID and the stored `MailMeta.MessageId` now use `domainFromEmail(email)` instead of `@bongo`.
Files: internal/mail/bounce.go, internal/agent/agent.go, internal/web/handlers/admin.go (+ 3 tests). go build ./... OK; go test -race mail/handlers/queue green.
PATTERN: [mail] Server primary domain resolution lives in mail.ResolveServerDomain — reuse it for any outbound-hygiene domain need (mirrors agent.go ensureConfiguredDomain chain).
MINOR (non-blocking): bounce test asserts From + a no-@localhost byte scan but does not directly assert the MAILER-DAEMON envelope arg to Deliver.

## 2026-07-18 — Task 3.2 DONE (FR-1 per-domain DKIM registry)

Worker: golang-pro (sonnet). Advisor plan review caught a FALSE PREMISE (no DomainRecord protobuf existed — /system/domain/{d} stored plain "enabled"/"disabled" bytes) and steered to a NEW proto message under a SEPARATE key, avoiding admin.go churn (that is 3.3) and any live-data migration. Code review MINOR (non-blocking).
- New proto/bongo/v1/domain.proto (DkimKey-style message: dkim_enabled=1, dkim_selector=2, dkim_private_key=3 PEM); regenerated via buf — ALL *.pb.go show header-only churn (buf version line); signature_html confirmed intact.
- New internal/mail/dkim_registry.go: DKIMRegistry (RWMutex map lowercased-domain→*DKIMSigner). Sign(rawMsg, logger) picks signer by From-HEADER domain (FromRawMessageDomain); missing/unparseable From or no key → unsigned + structured skip log (domain+reason only, NO key material); nil registry = passthrough. Seed() preserves byte-compat for the primary domain from the config-built signer.
- queue/worker.go: Worker.signer → registry; processMessage calls registry.Sign. agent.go: builds registry, seeds primary (dkimDomain), passes to NewWorker.
Tests: internal/mail/dkim_registry_test.go (two keyed domains → d= matches From; unkeyed → no DKIM-Signature + skip logged; primary-only byte-compat). go build ./... OK; go test -race mail/queue green.
PATTERN: [dkim] Per-domain signing goes through mail.DKIMRegistry keyed by the From-HEADER domain (not envelope sender); registry.Add(domain, signer) is the hook for 3.3 to load DomainRecord keys.
PATTERN: [proto] Adding a new proto message triggers a 1-line buf-version header rewrite across ALL *.pb.go — expected, harmless; verify signature_html + real content are untouched.
MINOR (non-blocking): registry.Sign always returns nil error, so the worker's `if err != nil` DKIM branch is now dead code (kept for safety). 3.2 wires the registry + Add() hook but does NOT yet load per-domain keys from the store — that lands with the 3.3 admin key-gen.

## 2026-07-18 — Task 3.3 DONE (FR-2 admin DKIM key management)

Worker: golang-pro (sonnet). Advisor flagged two traps up front — both handled:
- NIL-REGISTRY TRAP: registry now created early (agent.go:377) BEFORE NewAdminHandler (553), injected via SetDKIMRegistry (571), and the SAME instance is passed to the queue worker (1040) → a key generated in the handler signs outbound mail immediately (shared registry.Add).
- SECRET LEAK: private key never logged (handler + store log only domain+selector+err) and never rendered — templates.DomainInfo has only DkimSelector + DkimTXTRecord (no private-key field). dkimTXTRecord() parses the stored PEM but emits ONLY the public key (rsaKey.Public → MarshalPKIXPublicKey → base64) as `<selector>._domainkey.<domain> TXT "v=DKIM1; k=rsa; p=..."`.
- GenerateDKIMKey: RSA-2048 (#nosec G403 justified), PKCS8 PEM, stores DomainRecord@/system/domain/{d}/dkim via Raft ApplyCommand (fails request on error), registry.Add for immediate signing, redirect+flash. Route POST /admin/domains/{domain}/dkim/generate is in the admin group (RequireAuth+RequireAdmin+RequireSameOrigin+CSRF).
- LoadStoredDKIMKeys (admin_dkim_store.go): PrefixScan /system/domain/, filter /dkim suffix (skips the enabled/disabled byte keys), unmarshal DomainRecord, Add enabled keys → signing survives restart.
- admin.templ: Generate button + selector + TXT record; make generate re-ran (admin_templ.go); i18n keys added to en/de/es/fr (parity).
Files: admin.go, admin_dkim_store.go (new), admin.templ/_templ.go, agent.go, i18n x4 (+ admin_dkim_test.go). go build + full `go test -race ./...` green (rc=0); gosec clean on handlers.
PATTERN: [dkim] The admin handler and queue worker share ONE mail.DKIMRegistry created at agent.go:~377; inject via setters BEFORE route/worker construction so generated keys sign immediately without a restart.
PATTERN: [security] Derive the DKIM public TXT from the stored private PEM at render time (dkimTXTRecord) and keep the private key OUT of the template DomainInfo struct — never add a private-key field to a view model.
NOTE: I mislabelled the 3.2 proto message as "DkimKey" in TASKS.md 3.2 row — it is actually bongov1.DomainRecord (domain.proto). 3.3 reuses it (no new proto).

## 2026-07-18 — Task 4.1 DONE (FR-3 global address index foundation)

Worker: golang-pro (sonnet). Advisor PROCEED (steered addresses table into the existing system.db SystemIndex, not a new DB; flagged two-tier non-atomicity). Code review clean.
- New proto/bongo/v1/address.proto — AddressRecord{user_id=1, domain=2, kind=3, can_send=4, verified=5}; buf regen produced ONLY address.pb.go (no header churn on other .pb.go this run); signature_html + DkimPrivateKey verified intact.
- New internal/store/address_store.go — AddressStore{pebble, applier, system}: Put (lowercases addr+domain, proto.Clone to avoid mutating caller, Raft OP_PUT first = source of truth, then SQLite upsert; SQLite error RETURNED not swallowed), Get (Pebble read, (nil,false,nil) on ErrNotFound), Delete (Raft then SQLite, idempotent), ListByUser (SQLite index). Key = /system/address/{lowercased-addr}. system handle nil-safe (edge/agent).
- internal/store/metadata.go — added idempotent `addresses` table + idx_addresses_user to SystemIndex schema (rebuildable from Pebble).
Tests: address_store_test.go (case-insensitive Put→Get, Delete both tiers, ListByUser, clean not-found), -race, directApplier + real SystemIndex. go build ./... OK; go test -race ./internal/store green.
PATTERN: [store] Global (non-per-user) derived indexes go in the system.db SystemIndex (users/domains/aliases/addresses); a store that mirrors Pebble→SQLite takes a *SystemIndex handle (nil-safe) alongside pebble+applier. Raft write FIRST (source of truth), SQLite second, return SQLite errors (document Pebble-may-lead-SQLite, self-heals on rebuild).
NOTE: address index is the keystone — 4.3 (delivery) must read PEBBLE (Get) for cross-node correctness since only the handling node's SQLite is updated; 4.2 migration + rebuild wiring populates SQLite on all nodes.
