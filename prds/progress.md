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

## 2026-07-18 — Task 4.2 DONE (FR-12 address-index migration)

Worker: golang-pro (sonnet). Advisor REVISE (folded in): pinned the alias-user_id trap + testable-core structure. Code review clean.
- New internal/store/address_migrate.go — store.PopulateAddressIndex(pebble, addrStore, sys, logger): phase 1 PrefixScan /user/*/profile → primary AddressRecord (email lowercased, domain after '@', can_send/verified=true, user_id from key), building a username(email)→user_id map; phase 2 PrefixScan /system/alias/ → alias AddressRecord (kind=alias, can_send=false, verified=true), RESOLVING the stored target USERNAME→user_id via the map + SystemIndex `SELECT id FROM users WHERE username=?` fallback. Malformed addr / unresolvable alias target = skip+log (never abort). Idempotent (Put upsert), additive (never deletes users/aliases).
- agent.go: AddressStore constructed at ~325 (a as RaftApplier, nil-safe sys); migrateAddressIndex() called after ensureConfiguredDomain (~1171), leader-gated (a.Raft==nil || !IsLeader() || AddressStore==nil → no-op); documents leadership-change limitation (matches ensureConfiguredDomain).
Tests: address_migrate_test.go (users+aliases seeded → correct primary/alias records incl username→id resolution; second run idempotent), -race, directApplier + real SystemIndex. go build ./... OK; go test -race ./internal/store green (5.6s).
PATTERN: [migration] Leader-only startup seeding mirrors ensureConfiguredDomain; keep the seeding CORE as a store-package func (testable with directApplier + SystemIndex, no Raft node) and let the Agent method do only the leader gate + wiring.
CRITICAL CARRY-FORWARD for 4.4/4.5: PopulateAddressIndex does BLIND UPSERTS every boot (documented CAUTION in address_migrate.go:34-36,107-108). The moment 4.5 lets users/admins edit address records (verified/can_send toggles, add/remove), this migration MUST become create-only (Get-before-Put) or a restart will clobber those edits. Do this as part of 4.5.

## 2026-07-18 — Task 4.3 DONE (FR-4 unify delivery / fix alias inbound, US-RECV-1)

Worker: golang-pro (sonnet), one advisor fix round (6 agents). Advisor PROCEED w/ guidance; code review MINOR.
- New internal/store/recipient_resolver.go — RecipientResolver.ResolveRecipient(email): normalises (strip <>, trim, ToLower) then 3 tiers: (1) AddressStore.Get (Pebble, node-correct — primary+alias); (2a) legacy /system/alias Pebble key → username→id via users table (enabled=1); (2b) direct users-table lookup by email (=username, enabled=1); else ERROR → caller 550. NO O(n) profile scan (advisor DoS concern avoided — indexed lookups only). TODO(4.4) catch-all seam marked between tier-2 and reject with an open-relay-safety note. Satisfies smtp + pipeline/{deliver,rules,antispam} consumer interfaces.
- agent.go: ONE sharedResolver = store.NewRecipientResolver(AddressStore, Store, sys) built once and injected into compose (SetLocalResolver), pipeline, AND smtp backend RCPT (SetResolver) — same instance everywhere, so RCPT-accept and deliver agree (no accept-then-drop). Fixes the split-brain (compose used to check aliases, inbound ResolveRecipient did not).
- compose.go resolveLocalUser now delegates to the shared resolver (also fixes a latent bug where its alias branch returned a username while its profile branch returned a userID).
- test/integration/alias_delivery_test.go (//go:build integration): external sender → RCPT alias → lands in owner INBOX (PASS, 22.9s); + unknown addr → 550 negative case. Unit recipient_resolver_test.go: user-hit / alias-via-index / legacy-fallback / miss.
go build ./... OK; go test -race store+smtp+handlers green; integration (-tags integration) green. SignatureHtml intact, no proto churn.
PATTERN [delivery] ONE shared store.RecipientResolver injected into every recipient-resolving consumer (SMTP RCPT guard + pipeline + compose) guarantees RCPT-accept and delivery agree; resolver reads Pebble (tier 1/2a) for node-correctness; fallbacks are indexed-only (never a profile PrefixScan on the public MX path).
MINOR (non-blocking): (1) SetLocalResolver docstring in compose.go slightly stale re: fallback wording; (2) tier-2a legacy-alias resolution needs BOTH pebble!=nil AND system!=nil (edge nodes skip it) — acceptable.

## 2026-07-18 — Task 4.4 DONE (FR-6 per-domain catch-all, US-RECV-3)

Worker: golang-pro (sonnet). Advisor PROCEED (warned off the mixed-case managed-domain cross-check trap — worker correctly relied on the missing-key check as the open-relay defense). Code review PASS.
- New internal/store/catchall_store.go — CatchAllStore (pebble-read + applier-write, 5s): Set(domain,ownerID)/Clear(domain)/Get(domain) at /system/catchall/{lowercased-domain}→ownerID (plain bytes). Exported store.CatchAllKey(domain) shared by store + resolver (key format defined once).
- recipient_resolver.go — Tier 3 slotted at the TODO(4.4) seam: AFTER explicit tiers 1/2, BEFORE reject. Reads CatchAllKey(domain) from Pebble (node-correct); domain via SplitN; pebble nil-guard (edge/agent); empty/no key → falls through to 550. Explicit-wins is structural; open-relay guard = no key ⇒ no acceptance.
- admin.go SetCatchAll/ClearCatchAll (validate owner user exists) at routes /admin/domains/{domain}/catchall[/delete] in the admin group (RequireAdmin + RequireSameOrigin/CSRF); admin.templ catch-all field on the domain view (make generate); i18n keys ×4 locales (parity). agent.go wires CatchAllStore into AdminHandler (~604).
Tests: catchall_store_test.go (set/get/clear), recipient_resolver_test.go (catch-all owner returned when set; reject when not; EXPLICIT beats catch-all), admin_catchall_test.go (handler + owner validation). go build OK; -race store/handlers/i18n/agent green.
PATTERN [delivery] Per-domain catch-all is the LAST resolver tier; the open-relay guarantee is simply "no /system/catchall/{domain} key ⇒ fall through to 550" — never accept unmatched mail for a domain without an explicit catch-all entry.

## 2026-07-18 — Task 4.5 DONE (FR-5 admin address management + migration create-only)

Worker: golang-pro (sonnet). Advisor PROCEED (flagged the map-before-skip trap). Code review MINOR.
- admin.go AddAddress/RemoveAddress (routes POST /admin/users/{userID}/addresses[/delete] in the admin group: RequireAdmin+SameOrigin+CSRF). Add: validate '@', derive domain, Kind=identity, CanSend/Verified=false; reject if owned by a DIFFERENT user (409). Remove: guard refuses primary (address==profile.Email OR Kind==primary) → flash, keeps account reachable. ApplyCommand errors fail the request.
- I FIXED review MINOR #1 myself: AddAddress now loadProfile(userID) → 404 if the user doesn't exist (no dangling record routing to a dead user_id) + TestAdminAddAddress_UnknownUser.
- store/address_migrate.go now CREATE-ONLY: emailToUserID map populated for EVERY primary BEFORE the Get-before-Put skip (advisor's top risk — alias resolution on re-run still works); resolves the 4.2 CAUTION. Doc comments updated.
- admin.templ: addresses section on the user page (list via AddressStore.ListByUser + add form + per-row remove) using CSS classes (0 inline styles, CSP-clean); AdminUserInfo gained Addresses+PrimaryEmail; make generate. i18n keys ×4 locales (parity).
Tests: admin_address_test.go (add OK, no-@, unknown-user 404, remove non-primary, primary-delete guard, cross-owner conflict), address_migrate_test.go (create-only: edited record unchanged on re-run, missing primary still created). FULL go test -race ./... = rc=0; integration green earlier.
PATTERN [migration] Create-only seeding = Get-before-Put, but populate any lookup maps (email→id) BEFORE the skip so dependent phases (alias resolution) still work on every re-run.
MINOR remaining (non-blocking): resolver delivery for added addresses is proven via AddressStore.Get (= resolver tier 1) + existing recipient_resolver_test.go, not a fresh end-to-end assert — effectively covered.

## 2026-07-18 — Task 5.1 DONE (FR-7 send-as, US-SEND-1)

Worker: golang-pro (sonnet). Advisor PROCEED (flagged critical back-compat RISK 1: default single-address path must bypass the Get gate — worker handled it). Code review MINOR (dead test code, I removed it).
- compose.go: ComposeHandler gains addresses *store.AddressStore (SetAddressStore, wired agent.go). buildFromPickerOptions(userID, primaryEmail): ListByUser→Get→keep owned && CanSend && Verified; rendered as a server-side <select name="from"> (CSP-safe, no Alpine) only when >1 option. Threaded into ComposePage/ComposePageFull (New/Reply/Forward) — templ signature change, make generate.
- SEND-AS GATE (compose.go Send ~286): fromAddr defaults to session email; enforcement runs ONLY when submitted `from` differs (case-insensitive) from session email → AddressStore.Get: nil store→403, not-found→403, rec.UserId!=userID→403, !(CanSend&&Verified)→403; else fromAddr=chosen. Runs BEFORE any blob/Sent/queue write. Default/empty `from` bypasses (back-compat for edge/agent/pre-migration).
- ALIGNMENT: fromAddr drives From header (buildRichMessage/buildRFC5322 → DKIM d= via 3.2 registry keyed on From-header domain), envelope SenderEmail (MAIL FROM/return-path, line 506), Message-ID host (senderDomain(fromAddr)), meta.From, search index.
- i18n From-label ×4 locales (parity). SignatureHtml intact, no proto churn.
Tests: compose_sendas_test.go (picker excludes not-verified/other-user; select b@d2→From/SenderEmail/Message-ID=d2; tampered unowned/unverified→403 nothing enqueued). go build OK; -race handlers+i18n green.
PATTERN [sendas] Send-as identity alignment = set ONE fromAddr and thread it into From header + envelope SenderEmail + Message-ID host; DKIM needs NO extra code (mail.DKIMRegistry signs by From-header domain). ENFORCE server-side only when from != session primary; the default primary path must bypass the address-index gate so it never regresses on un-migrated/edge nodes.

## 2026-07-18 — Task 5.2 DONE (FR-7b send-as verification, US-SEND-2)

Worker: golang-pro (sonnet). Advisor PROCEED (flagged non-atomic success path → worker ordered Put-first, Delete best-effort, documented). Code review MINOR (2 real: msgID userID[:8] panic + colliding Id — I fixed both to verify-{UnixNano}).
- PATH A admin grant: admin.go GrantSend sets an owned address CanSend=true+Verified=true via AddressStore.Put; route in RequireAdmin group; picker (5.1) shows it automatically.
- PATH B self-serve: new proto AddressVerification{code_hash, expires_at, attempts}; internal/store/address_verify_store.go (Put/Get/Delete via Raft). Handler internal/web/handlers/address_verify.go: request → crypto/rand code, store SHA-256 HASH only (never plaintext), email plaintext code to the claimed addr via the OUTBOUND QUEUE (buildVerificationEmail + blobs.Put + queue.Enqueue); submit → crypto/subtle constant-time compare, expiry check, Attempts++ with maxVerifyAttempts=5, on success AddressStore.Put(Verified+CanSend) then Delete challenge (consume). Code NEVER logged (log lines carry user_id/address/attempts only). Cross-user ownership rejected at all 3 sites (add rec.UserId!=userID→409, request→reject, submit→reject). Unverified never in picker (5.1 unchanged).
- address_verify.templ user settings section (add addr / request code / enter code), CSP-safe classes; i18n ×4 locales.
Tests: address_verify_store_test.go, address_verify_test.go (happy path, wrong/expired/reused code fail, unverified excluded + send rejected, cross-user claim rejected, code absent from captured log buffer), admin_grant_send_test.go. go build OK; -race store+handlers green. SignatureHtml intact.
PATTERN [verification] One-time email codes: crypto/rand generate → store SHA-256 hash + expiry + attempts (never the plaintext) → email plaintext via outbound queue → crypto/subtle constant-time compare on submit, expiry + max-attempts guard, consume (Put-then-Delete, ordered so a failed Delete safely re-fails, never silently grants). NEVER log the code — log user_id/address/attempts only.

## 2026-07-19 — Phase 6 scoping decisions (user-confirmed)

Before implementing tenancy, confirmed 4 load-bearing decisions with the user:
1. ISOLATION ENFORCEMENT: context-injected authorized-domain SET (resolved by middleware) + explicit per-handler filter/check + adversarial isolation tests (6.4). Defense-in-depth across ~30 admin endpoints; default-deny.
2. PACING: INCREMENTAL with checkpoints — 6.1 data model (ownership+grants) → USER REVIEW → 6.2 isolation layer → 6.3 quotas → 6.4 adversarial. Pause between for review (security surface).
3. USER↔DOMAIN: a user belongs to the domain of their PRIMARY address; the domain-admin for that domain governs them (one unambiguous owner per user).
4. QUOTAS: per-domain max-users AND per-domain aggregate storage; over-quota inbound → RCPT 5xx (already the FR-10 decision).
CURRENT AUTH STATE (grounding): is_admin is a single GLOBAL flag (cookie + live re-check); RequireAdmin is all-or-nothing; DomainRecord has NO owner field yet; no domain_admins model; ~30 admin routes (agent.go 621-651 + quarantine 1026-1029) all need scoping in 6.2.
DEPLOY: user chose to WAIT and bundle the Phase 5 (5.2 verification) + hygiene deploy WITH Phase 6 (no separate v0.6.0 now).

## 2026-07-19 — Task 6.1 DONE (FR-8 domain ownership + grant model — DATA MODEL ONLY, no enforcement)

Worker: golang-pro (sonnet). Advisor caught the clobber hazard: co-locating owner in the /dkim DomainRecord would be wiped by GenerateDKIMKey's fresh-record write → owner stored SEPARATELY. Code review MINOR.
- proto: DomainRecord gains owner_user_id=4 (regen, other protos + SignatureHtml intact). NOTE: this proto field is currently UNUSED — owner is actually stored as RAW BYTES at /system/domain/{domain}/owner (consistent with catch-all/alias), read via DomainAdminStore.GetOwner. 6.2 must use the store helper, NOT DomainRecord.OwnerUserId (always empty). Decide in 6.2: use the field or remove it.
- internal/store/domain_admin_store.go — DomainAdminStore (pebble-read + applier-write): SetOwner/GetOwner (/system/domain/{d}/owner); Grant/Revoke/ListForDomain/ListForUser (/system/domain_admin/{d}/{uid} — '_' after 'domain' so it never pollutes the /system/domain/ PrefixScans in listDomains/LoadStoredDKIMKeys). Grant VALIDATES domain+user exist; Revoke does NOT (intentional — lets you clean grants for deleted users). AuthorizedDomains(userID, isGlobalAdmin): global→allManagedDomains; else owned (scan /owner sub-keys == userID) + granted (ListForUser), deduped; regular→empty. DomainFromEmail helper in store pkg.
- admin.go + admin.templ: global-admin UI to set/change owner + grant/revoke domain-admins on the domain view; agent.go routes (RequireAdmin+CSRF); i18n ×4.
Tests: domain_admin_store_test.go (owner set/get/change; grant/revoke/list; AuthorizedDomains all 4 roles; grant rejects nonexistent domain/user). go build OK; -race store+i18n green.
MINORs (non-blocking): (1) Revoke no existence check — intentional/good; (2) owner+grant records dangle if user later deleted — DeleteUser cascade is a 6.2/follow-up concern; (3) vestigial owner_user_id proto field (see note above).
CHECKPOINT: stop here for user review of the 6.2 isolation-layer plan before enforcing.

## 2026-07-19 — Task 6.2 DONE (FR-9 tenant isolation layer, US-TENANT-2)

Worker: security-auditor (sonnet). Advisor PROCEED; code review PASS. Classified to security-auditor (apt for the security surface).
- middleware/admin_scope.go: AdminScope{Global bool, Domains map[string]bool} + HasDomain; RequireAdminOrDomainAdmin (replaces RequireAdmin on the admin group) computes scope via DomainAdminStore.AuthorizedDomains and injects it; empty-scope non-admins 403 at the door; AdminScopeFromContext + WithAdminScopeContext.
- handlers/admin_scope.go: requireGlobalAdmin(scope), requireDomainAccess(scope,domain), requireUserAccess(scope,userID)→loadProfile→DomainFromEmail(primary). Applied per the CONFIRMED categorization: domain-scoped lists FILTERED (ListDomains/ListUsers/ListAliases drop out-of-scope rows — zero leak); domain mutations gated by target domain; user mutations gated by target user's primary domain; global-only set (CreateDomain, cluster, logs, ToggleRequire2FA, queue×4, quarantine×4, Dashboard infra stats — Dashboard redirects domain-admins to /admin/domains). Quarantine group already RequireAdmin(global) so domain-admins (isAdmin=false) were already excluded.
- DeleteUser cascade: revokes the deleted user's domain-admin grants (ListForUser→Revoke) + clears owner refs (resolves 6.1 dangling MINOR).
- I CLOSED two cross-tenant escalations the reviewer flagged as informational: AddAddress now also requireDomainAccess(new address's domain) — a d1-admin can't allocate a d2 address even for their own d1 user; GrantSend now also requireDomainAccess(address's domain) — can't grant send-as for a domain you don't administer (spoof prevention). Added TestAddAddress_DomainAdmin_D2AddressForbidden (+D1AddressOK guard) and TestGrantSend_DomainAdmin_D2AddressForbidden.
Tests: admin_scope_test.go (env with global/d1-admin/regular/d1-user/d2-user; per-endpoint 403 + d1-OK + list-filter-no-leak); all existing admin tests updated to inject AdminScope{Global:true}. go build OK; -race web+store green (handlers 183s).
PATTERN [tenancy] Admin isolation = one AdminScope in context (RequireAdminOrDomainAdmin middleware) + explicit per-handler helper (requireGlobalAdmin / requireDomainAccess / requireUserAccess) called FIRST; lists FILTER (never just block) to avoid row leakage; default-deny. For address ops, scope BOTH the target user's domain AND the address's own domain (allocation + send-as are per-domain-namespace actions).
CHECKPOINT: stop for user review before 6.3 (quotas).

## 2026-07-19 — Task 6.3 DONE (FR-10 per-domain quotas, US-TENANT-3)

Worker: golang-pro (sonnet). Advisor PROCEED (flagged: count from Pebble not per-node SQLite; recompute raw_message.size ONLY not attachments — both handled). Code review MINOR (recompute didn't reset cleared domains — I fixed it).
- proto DomainQuota{max_users, max_storage_bytes}; store DomainQuotaStore (pebble-read + applier-write): GetQuota/SetQuota @ /system/domain/{d}/quota, GetUsage/SetUsage @ /system/domain/{d}/usage (separate keys — NOT the DKIM DomainRecord). SignatureHtml intact.
- MAX-USERS: CreateUser (admin.go) rejects with 400 "domain user limit reached" BEFORE createUserViaRaft when the target domain's user count >= MaxUsers (>0); also still requireDomainAccess(new email domain) from 6.2. 0/unset = unlimited.
- RECOMPUTE: leader-only computeAndWriteDomainUsage (mirrors runOrphanGC ticker); scans /user/*/mail/*, sums MailMeta.RawMessage.Size bucketed by DomainFromEmail(profile.Email), writes /usage via Raft. MY FIX: now writes usage for EVERY managed domain (scan /system/domain/ plain keys), resetting cleared domains to 0 (was: only domains with mail → a cleared domain kept stale non-zero usage and would reject inbound forever).
- RCPT ENFORCEMENT: internal/smtp Session.Rcpt — QuotaChecker interface (SetQuotaChecker, nil-safe for dev/agent/edge) called AFTER resolving to userID, BEFORE accepting; RecipientOverQuota(userID)→resolves user's primary domain, compares usage>=max_storage_bytes → 5xx permanent reject, no local copy. Fail-OPEN on checker error (don't block mail on transient errors).
- Admin UI: global-admin quota inputs (SetDomainQuota, requireGlobalAdmin, route /admin/domains/{domain}/quota) + read-only usage/limit on the domain view (domain-admins see the read-only part); i18n ×4.
Tests: domain_quota_store_test.go, admin_quota_test.go (set global-only/domain-admin 403+view, max-users at/under limit), recompute + RCPT over/under. go build OK; -race store+agent+handlers green.
PATTERN [quota] Per-domain quota = separate /quota + /usage keys (never the clobber-prone DKIM DomainRecord); storage attributed to the mailbox owner's PRIMARY domain; enforce max-users at create (Pebble count) + storage at RCPT (periodic-recompute value, fail-open). Recompute MUST reset cleared domains to 0 (write all managed domains, not just those with mail).
NOTE (single-node OK, multi-node follow-up): max-users count source — verify it reads Pebble profiles (replicated) vs per-node SQLite (can under-count on a follower). the deployment is single-node so not urgent.
CHECKPOINT: stop for user review before 6.4 (adversarial isolation sweep).

## 2026-07-19 — Task 6.4 DONE (adversarial tenant-isolation sweep — Phase 6 complete)

Worker: security-auditor (sonnet). Advisor REVISE (excellent guidance, folded in): quarantine is a SEPARATE group gated by RequireAdmin (global-only, not through the AdminScope harness); Dashboard is a 303 REDIRECT for domain-admins (not 403 — don't break it); regular-user-at-door is covered at the middleware layer; warned against OVER-CORRECTION (fabricating gaps). Code review MINOR.
- +12 tests (48→60) in admin_scope_test.go incl. TestAdversarialSweep_AllEndpoints_DomainAdmin (35-case table over the whole admin route group): RemoveAddress d2 403, SetCatchAll/ClearCatchAll d2 403 + d1 controls, SetDomainQuota/RetryMessage/DropMessage/LogStream global-only 403, NewUserForm domain-list leak assertion.
- RESULT: NO real isolation gaps — every handler's 6.2/6.3 guard held under adversarial probing. gosec clean.
- I CLOSED the RemoveAddress asymmetry the reviewer flagged: it now also requireDomainAccess(address's domain) — symmetric with AddAddress/GrantSend (an admin only mutates addresses in domains they administer; a d1-admin can't remove a d2 address even from their own d1 user). Added TestRemoveAddress_DomainAdmin_D2AddressForbidden (+D1AddressOK control).
go build OK; targeted -race green; full -race suite running as the Phase-6 gate.
PATTERN [security] The adversarial sweep's value when guards are already correct is COVERAGE (a table over every endpoint proves no endpoint was silently left unscoped) — build the table from the ACTUAL route list so nothing is omitted; assert only 'not 403' for positive controls (CreateDomain/CreateUser/DKIM have DNS/crypto/email side effects that flake on a specific 2xx).
PHASE 6 COMPLETE: all of 6.1-6.4 done. Multi-domain epic (Phases 3-6) complete. Next: full-suite gate → collapse Phase 6 → move PRD research→done → v0.6.0 deploy (bundles 5.2 + hygiene + all Phase 6).

## 2026-07-19 — Task 7.1 DONE (per-domain detail sub-screen + MX indicator) + backlog B1

Worker: htmx-alpine-pro (sonnet). Advisor caught: test goes in handlers/admin_scope_test.go not middleware; ShowDomain must 404 on unknown domain (global admin); MX-expected from config not hardcoded. Code review MINOR.
- New GET /admin/domains/{domain} → AdminHandler.ShowDomain (chi domain param, requireDomainAccess(scope,domain) — domain-admin 403 on others / 404 on unknown; global sees all), renders new templ AdminDomainDetail with stacked sections: DNS/MX status (✓/✗ + expected mail host; structured {label,ok,expected,found} so SPF/DKIM/DMARC append later), enable/disable, DKIM generate/rotate + selector + full TXT record (PROMINENT — fixes the off-screen bug), catch-all set/clear, owner (global-only), domain-admins grant/revoke, quota (global-only set + usage), delete (global-only). AdminDomainList slimmed to Domain·Status·MX·Manage (9-col overflow gone). Light <small> help blurbs; i18n ×4; CSP-safe classes.
- ISOLATION: route in RequireAdminOrDomainAdmin group; adversarial test (admin_scope_test.go) d1-admin→/admin/domains/{d2}=403, →{d1}=not-403.
- I FIXED 2 review MINORs: (1) added missing .u-mb-025 util class to bongo.css; (2) DeleteDomain now uses h.hxRedirect (was plain http.Redirect 303) so the worker's new hx-post+hx-confirm delete navigates correctly — matches DeleteUser (hxRedirect sets HX-Redirect + Location/303 fallback).
go build OK; -race handlers green. Deploying as v0.6.1.
PATTERN [admin-ui] Per-entity detail sub-screen (like ShowUser) is the pattern for admin objects with many settings — a slim list + a /{id} detail page beats a wide table; the new GET route MUST carry the same scope check (requireDomainAccess/UserAccess) as its mutations or it's a read leak.

## 2026-07-19 — Backlog B1 recorded: de-brand the repo (user-requested)
Remove ALL 157 deployment-domain refs (deploy/ configs + node name bongo-1, .env.example, autoconfig comment, docs/, prds/ logs, tests) → repo mentions only bongo/bongo-go with generic example.com placeholders. USER-AUTHORISED git history rewrite + FORCE-PUSH if needed. CAVEAT: live /mnt/data/bongo config is not in git (keeps real values); verify no runtime code depends on the deployment-domain literal first. Unscheduled.

## 2026-07-19 — Task 7.2 DONE (DNS config status checks + MX false-positive fix)

Worker: golang-pro (sonnet). Advisor caught the binary-OK-can't-represent-neutral/error risk → DNSCheckRow gained Status ("ok"|"fail"|"neutral"|"error", OK kept for compat). Code review MINOR.
- buildDNSChecks(domain, mailHost, expectedDKIMTXT, mx/spf/dkim/dmarc []string) → []DNSCheckRow: PURE (no network, table-tested). MX ✓ only if an MX host (trailing-dot-trimmed, lowercased) == mailHost (FIXES the existence-only bug — example.net→OVH now ✗); SPF spfAuthorisesHost (mx mechanism / a:<host> / references mailhost) else ✗; DKIM neutral when no key here, else compare published p= to OUR p= (extracted from DkimTXTRecord); DMARC ✓ if a record starts v=DMARC1.
- doDNSLookups(ctx, domain, selector, mailHost, dkimTXT): the 4 lookups run CONCURRENTLY under a short deadline (~2-3s) so worst case ~3s not ~12s; lookup errors → Status="error" ("could not check"), never a panic. ShowDomain (detail) does all 4; loadDomains (list) uses the fixed MX-only check → HasMX = points-to-us.
- mailHostname from a.Config.SMTP.Hostname; I ADDED the missing fallback to os.Hostname() when empty (review MINOR #1) + gofmt'd (MINOR #2).
- i18n ×4, CSP-safe rows (Status→CSS class, Found in <details>).
Tests: admin_test.go TestBuildDNSChecks (MX ✓/points-elsewhere-OVH ✗, SPF us/other/none, DKIM match/mismatch/neutral, DMARC present/absent) + extract-our-p= tests; injected values, no network. go build OK; -race handlers green.
PATTERN [dns] DNS-config verification = compare ACTUAL vs EXPECTED-for-this-server, not existence. Keep the compare logic a PURE function fed looked-up values (network stays out of tests); run the lookups concurrently with a bounded deadline; model 4 states (ok/fail/neutral/error) not a bool. MX-correct = an MX host equals the server mail hostname (SMTP.Hostname).

## 2026-07-19 — Task 7.3: Domain-owner DNS setup guide + 3 UI bug fixes

PATTERN: [dns-guide] Pure builder `buildDNSGuide(domain, mailHostname, v4, v6, dkimSelector, dkimTXTRecord) ([]DNSGuideRow, pending bool)` in internal/web/handlers/dns_guide.go — no I/O so the same values feed the on-page table, the BIND zone download, and unit tests; can never drift from the 7.2 status checks. `dnsGuideZone(domain, rows)` renders FQDN-expanded BIND lines (TXT quoted, others bare).
PATTERN: [dns-guide] Shared-host model (owner decision): MX + all SRV targets = server SMTP.Hostname (read dynamically, never hardcoded in repo). A/AAAA come from resolveServerIPs(mailHostname) — server resolves its own hostname, so no server IP in config.
PATTERN: [dns-guide] Never publish an empty-p= DKIM record (worse than none = "revoked"). buildDNSGuide omits the _domainkey row and returns pending=true when no key exists; UI shows a "generate a key first" notice instead.
PATTERN: [dns-guide] autodiscover/autoconfig are HTTP-based in bongo (serves /.well-known/autoconfig, /autodiscover/autodiscover.xml, /.well-known/mobileconfig); the DNS guide only needs A records for autoconfig./autodiscover. hosts + the RFC6186 SRV set to point clients at the server.
PATTERN: [ui] Reused existing `[data-copy-previous]` clipboard handler (bongo.js:1740) for per-row copy buttons — copies previousElementSibling.textContent; CSP-safe (no inline handlers).
Route: GET /admin/domains/{domain}/dns.zone -> DownloadDNSZone (requireDomainAccess + 404-on-unknown, attachment). Tests: 14 unit (builder/zone/extract) + 2 handler (adversarial 403 + global-admin attachment headers). -race green.

Also fixed 3 reported UI bugs (not tracked tasks):
- DKIM Create/Rotate redirected to /admin/domains (list) instead of /admin/domains/{domain} — now returns to the detail page with flash.
- Admin content overflowed right: `.admin-scroll` flex child lacked `min-width:0` (defaulted to min-width:auto, refused to shrink below widest child); added it. Plus `.dkim-txt-record` (<pre>) had no wrap CSS, and DNS <code> values didn't break — added pre-wrap/overflow-wrap; styled missing .dns-neutral/.dns-error.
- Ctrl/Cmd+C (copy) hijacked as compose shortcut: global keydown handler (bongo.js:1487) never checked modifiers; added early return when ctrlKey/metaKey/altKey held.

## 2026-07-21 — Task 9.1 (spike): byte-exact attachment extraction — DONE

Built `internal/attsplit` (Split/Reconstruct) proving the de-MIME approach is
FEASIBLE and byte-exact.

- Approach: manual MIME boundary scan finds each attachment part's exact ENCODED
  body byte-range in the raw (never decode via enmime). Split partitions the raw
  into an ordered sequence of literal spans + attachment references; Reconstruct
  concatenates them → byte-exact by construction (belt-and-braces vs the PRD's
  ingest self-check).
- 8 round-trip tests pass: base64, quoted-printable, nested multipart, multiple
  attachments, DKIM-signed (byte-equal ⇒ signature preserved), plain passthrough,
  below-threshold skip, and dedup (same encoded attachment in two different
  messages → identical SHA-256 → one blob).
- PATTERN: skeleton = partition of the raw (literal spans + attachment refs), not
  an edited raw. Byte-exactness is automatic; dedup key = SHA-256 of the exact
  encoded span.
- Recommended 9.2 on-disk form: skeleton blob = concatenated literal spans + an
  ordered splice index in MailMeta (each segment: literal-length | attachment
  hash+length).
- Caveats for production (feed into 9.2/9.4): assumes CRLF endings; add a
  delimiter-suffix check to avoid boundary-prefix ambiguity; decide handling of
  large INLINE (non-attachment) parts and top-level single-part attachments.

## 2026-07-21 — Task 9.2: skeleton storage + FullRawMessage retrieval — DONE

- proto: added `MailMeta.layout` (`repeated MessageSegment`, kinds LITERAL/
  ATTACHMENT) via buf generate. Non-empty layout marks a split record
  (raw_message = skeleton blob); empty = legacy whole-raw. Additive, backward
  compatible — full store -race suite still green.
- `attsplit.Layout()` exposes the skeleton bytes + ordered segments
  proto-agnostically (attsplit stays free of proto imports).
- `store.PrepareSplit(raw, minSize)` -> `SplitMessage` {skeleton, layout,
  attachment BlobRefs, encoded bytes}; returns nil below threshold so the caller
  keeps whole-raw storage.
- `store.FullRawMessage(meta, get)` reconstructs split records (skeleton
  interleaved with attachment blobs, attachment fetches cached) and passes legacy
  records through unchanged. Fails cleanly on out-of-range/missing blobs.
- PATTERN: the split marker is the presence of `layout`, not a content-type or
  flag — one code path, legacy and split coexist with no migration.
- Tests: byte-exact round-trip, legacy passthrough, below-threshold nil,
  shared-attachment dedup (2nd message adds only its skeleton blob), corrupt
  layout error.
- Next (9.3 config knob, then 9.4 ingest) can now store via PrepareSplit and read
  via FullRawMessage; 9.5 routes all full-message readers through FullRawMessage.

## 2026-07-22 — Tasks 9.3 (threshold config) + 9.5 (route readers) — DONE

9.3: config.Storage.AttachmentDedupMinSize ("attachment_dedup_min_size", default
"32KB") + AttachmentDedupMinBytes() (empty->32KB, "0"->disabled). PrepareSplit
guards minSize<=0 -> nil. Table test.

9.5: store.RawMessageBytes(bs, meta) is the single reconstruct-or-passthrough
entry point. Routed EVERY full-message reader through it:
- IMAP FETCH (imap/backend.go)
- web: applyEnvelopeBody, ExportEML, ServeAttachment, backfillThreadingFields,
  trainBayes (mail.go); rules test-run (rules.go); retro-scan scoring (scan.go);
  compose EditDraft + loadQuotedBody (compose.go)
- POP3 RETR/TOP: popMsg now carries *MailMeta (unmarshal into a pointer to avoid
  copylocks); fetchBlob removed.
- ExportEML Content-Length now uses the reconstructed length.
- DESIGN DECISION for 9.4: keep MailMeta.RawMessage.Size = FULL original message
  size for split records (not skeleton size), so IMAP RFC822.SIZE, POP3 LIST/STAT
  and quota usage (agent.go domainBytes += RawMessage.Size) stay correct with no
  change. FullRawMessage never reads Size.
- Behaviour-preserving today (all records are legacy -> passthrough); this
  de-risks the read path before 9.4 turns ingest splitting on.
- Tests: real-BlobStore round-trip (split + legacy). Full imap/pop3/config green,
  handlers lint 0.

## 2026-07-22 — Task 9.4: inbound ingest split — DONE (default on)

- DeliverProcessor.SetAttachmentDedup(blobs, minSize); wired in agent.go from
  config.Storage.AttachmentDedupMinBytes() (default 32KB, on by default).
- trySplit(raw,id): store.PrepareSplit -> Put skeleton + each attachment blob ->
  SELF-CHECK via store.RawMessageBytes (must byte-equal the original) -> on any
  error or mismatch, return nil so the caller keeps whole-raw storage (+ WARN).
  Computed once per message; skeleton/attachment refs shared across recipients.
  meta.RawMessage -> skeleton hash, meta.Attachments carry hashes, meta.Layout
  set. RawMessage.Size stays the FULL original size (per 9.5 decision).
- Tests: split+byte-exact reconstruct, below-threshold whole-raw, disabled
  whole-raw, shared-attachment dedup (2nd delivery adds only its skeleton blob).
- IMPORTANT CAVEAT (feeds 9.7): there is NO blob GC yet (only orphan-MAIL and
  orphan-USER sweeps; the only blob Delete is sync-integrity). So when a message
  is split, the original whole-raw blob (env.BlobHash) becomes orphaned but is
  NEVER collected -> per-unique-message storage temporarily INCREASES; only
  recurring-attachment dedup saves today. Net savings require 9.7's blob GC to
  reclaim orphaned raws. Forwarding is SAFE precisely because nothing deletes
  env.BlobHash (the forward OutboundMessage still reads it). 9.7 GC must treat
  BOTH mail-meta refs AND pending queue OutboundMessage.BlobHash as live.

## 2026-07-22 — Task 9.7: blob garbage collector — DONE (opt-in, dry-run capable)

- Verified the complete set of persistent blob references: MailMeta
  (raw_message + attachments, across every user/mailbox) and queue
  OutboundMessage.blob_hash. calendar/contacts/profile protos have NO blob
  refs (they inline their data), so the referenced-set is complete and safe.
- collectReferencedBlobs(ps) scans /user/*/mail/* MailMetas + /queue/pending|bounced.
- planBlobSweep (pure, unit-tested): a blob is deleted only when unreferenced AND
  older than minAge AND it was a candidate in the previous pass too (one-interval
  grace, mirrors sweepOrphanedMail). BlobStore.ModTime added for the age gate.
- sweepOrphanedBlobs is leader-only and hooked into the existing runOrphanGC
  (15m interval, 1h min-age).
- SAFETY POSTURE: config storage.blob_gc has three modes — "off" (DEFAULT: never
  runs), "dryrun" (logs what it would delete, deletes nothing), "on" (deletes).
  So no data-deleting sweep starts without the operator opting in, and dryrun
  lets it be validated against real production data first.
- This is the task that BANKS the 9.4 savings: it reclaims the whole-raw blobs
  orphaned when messages are split. Recommended rollout: deploy with blob_gc =
  "dryrun", confirm the logged candidates look right, then flip to "on".
- Tests: planBlobSweep (referenced kept / young skipped / 2-pass grace /
  newly-referenced spared), collectReferencedBlobs, BlobGCMode.

## 2026-07-22 — Task 9.6: outbound/sent-mail dedup (touches relay) — DONE

- store.StoreSplit(bs, raw, minSize): shared helper = PrepareSplit + Put blobs +
  byte-exact self-check; (result,nil)=split, (nil,nil)=whole-raw, (nil,err)=fall
  back+log. deliver.trySplit refactored onto it (removed duplicated logic).
- proto: OutboundMessage += layout (repeated MessageSegment) + attachments
  (repeated BlobRef). Non-empty layout => blob_hash is a skeleton.
- compose Send + SaveDraft: stopped storing decoded attachment blobs; StoreSplit
  the assembled raw; meta + proto.Clone'd recipientMeta + OutboundMessage all
  reference the split (skeleton + encoded attachment blobs + layout). Whole-raw
  fallback on any split error. ComposeHandler.SetAttachmentDedup wired from config.
- queue worker processMessage: when msg.Layout is non-empty, reconstruct the full
  raw byte-exact via store.RawMessageBytes BEFORE DKIM-sign + relay, so the wire
  message and signature are unchanged. Legacy (empty layout) reads the whole blob
  as before.
- KEY: unlike inbound (9.4), compose CONTROLS storage, so it stores the skeleton
  directly instead of a whole raw -> NO orphaned raw -> immediate per-message
  saving even before the blob GC is enabled, plus cross-message attachment dedup.
- Tests: StoreSplit round-trip; worker reconstructs a split OutboundMessage and
  gets past reconstruction to relay; compose SaveDraft with a large attachment
  splits, reconstructs byte-exact, and the attachment decodes to the original
  bytes. Full handler -race (410s), store/queue/deliver/agent -race, lint 0, gosec
  0 new (added one justified #nosec G115 for the bounded attachment index).

## 2026-08-16 — Task 10.1: outbound SMTP HELO must be fully qualified — DONE

- ROOT CAUSE (found from a live prod log line, not a code sweep): remote MX
  returned `504 5.5.2 <localhost>: Helo command rejected: need fully-qualified
  hostname`. internal/smtp/relay.go:190 called netsmtp.NewClient and never
  Hello(), so Go's hardcoded localName "localhost" (GOROOT/src/net/smtp/smtp.go:71)
  went out as the EHLO on EVERY outbound connection. All local users' mail to
  every FQDN-enforcing receiver was permanently rejected.
- New internal/smtp/helo.go: ResolveHELOName -> pure resolveHELOName with
  injected hostnameFn + resolveServerDomain so the order is testable without
  depending on the machine's real hostname.
- Relay gains an immutable heloName field resolved ONCE at startup (agent.go);
  NOT per-connection — the Relay is shared across queue-worker goroutines, and
  re-deriving per delivery adds a syscall + log spam per message.
- client.Hello() must be the FIRST command: Extension()/StartTLS()/Auth()/Mail()
  all trigger net/smtp's implicit EHLO, and Hello() after any of them errors
  with "Hello called after other methods". StartTLS then re-EHLOs reusing
  c.localName, so the post-TLS EHLO is qualified for free — do NOT call Hello
  a second time.
- ADVISOR CAUGHT A CONTRADICTION IN THE PRD: FR-20's step 3 said fall back to
  mail.ResolveServerDomain, but that function itself falls back to a bare
  os.Hostname() and then the literal "localhost" (bounce.go:62-65) — i.e. the
  prescribed chain could produce exactly what FR-21 forbids. Fix: gate EVERY
  candidate through isQualifiedHELO, and when nothing qualifies send an RFC 5321
  4.1.3 address literal instead of localhost.
- Fixed post-review (advisor PASS, but these were worth doing before commit):
  bare IPs are bracketed into address literals rather than sent bare; the
  startup warning became the tested const HELONotQualifiedWarning so the
  "names the config key" criterion is actually covered; the test mock's
  capturedHELO is mutex-guarded.
- Tests: pure resolution table (12 cases), qualified-name gate (11), IP
  bracketing (8), plus 4 WIRE-LEVEL tests through a go-smtp stub asserting the
  real pre-STARTTLS EHLO and that delivery SUCCEEDS against a server that
  rejects dotless HELO. Full -race suite green (33 pkgs), vet clean, gosec 0 new.

PATTERN: [smtp-client] Go's net/smtp hardcodes localName="localhost"; NewClient's
  `host` arg is only the TLS ServerName/AUTH name, NOT the HELO. Any outbound
  net/smtp client MUST call client.Hello(fqdn) as its first command or it
  silently announces itself as localhost and strict receivers reject the mail.
PATTERN: [spec] When a PRD prescribes "fall back to helper X", verify X's OWN
  fallback chain — ResolveServerDomain bottoming out at "localhost" made FR-20
  and FR-21 mutually contradictory. Advisor plan review caught it before a line
  was written; a worker following the FRs literally would have shipped the bug.
PATTERN: [testing] Assert protocol-level behaviour on the wire, not just the
  pure helper. A unit test of the name resolver alone would have passed while
  the relay still sent EHLO localhost, because the defect was a MISSING CALL.
  go-smtp populates Conn.Hostname() before NewSession, so a stub backend can
  capture the EHLO — and NewSession fires only on the first EHLO, so the
  captured value is by construction the pre-STARTTLS one.

## 2026-08-16 — Task 10.2: DKIM-sign generated bounces — DONE

- BounceGenerator gained a *DKIMRegistry (appended as the 5th ctor param) and
  signs the DSN via the EXISTING DKIMRegistry.Sign before Deliver. No new
  signing code — same helper the queue worker already uses (worker.go:198).
- agent.go passes a.DKIMRegistry (already in scope, seeded before the call
  site). Passing the REGISTRY not a resolved signer matters: admin-generated
  per-domain keys added later via Add are then picked up at call time.
- FR-2 needed an explicit pre-check (bg.dkim == nil || Get(domain) == nil)
  because Sign only logs at Info and returns (rawMsg, nil) on the no-key path
  — the caller cannot distinguish signed from unsigned from its return values.
  Note bg.dkim == nil must short-circuit before Get, which is not nil-safe.
- FR-9 honoured: NO antispam-bypass mechanism. The bounce passes the pipeline
  on its merits; it stops being junked because it now actually aligns.
- TEST HAZARD (flagged in the task brief, resolved properly): antispam.VerifyDKIM
  does a live DNS TXT lookup of <selector>._domainkey.<domain>, so a naive test
  would depend on public DNS. Solved with dkim.VerifyWithOptions + a stubbed
  LookupTXT returning the generated test key. No network in the suite.
- Fixed post-review: the h= assertion was vacuous — strings.Contains(raw, "from")
  matched lowercase "from" anywhere in the DSN. Now extracts the DKIM-Signature
  header (unfolding continuations) and checks its h= tag.
- Documented two hazards in bounce.go for 10.3/10.4.

PATTERN: [dkim] DKIMRegistry.Sign is deliberately non-failing: nil registry,
  unknown domain, and signing error ALL return the message unsigned with a log
  and a nil error. Callers that need to know whether signing actually happened
  must pre-check Get(domain) — the return values do not tell you.
PATTERN: [testing] Asserting a protocol field with strings.Contains over the
  WHOLE message is vacuous: "d=example.test" and "from" both occur naturally in
  a DSN body. Extract the specific header (unfold continuation lines first),
  then assert on its tags.
PATTERN: [testing] Verifying DKIM without live DNS: dkim.VerifyWithOptions
  accepts a LookupTXT func; return "v=DKIM1; k=rsa; p=" + base64(PKIX pubkey)
  for <selector>._domainkey.<domain>. Lets a test prove a signature genuinely
  verifies rather than merely exists.
PATTERN: [coupling] A pre-check that resolves an identity one way while the
  helper it guards resolves it another (here: bg.domain vs the From: header)
  agrees only by construction. Comment the invariant at the seam, or a later
  task changing the header silently desyncs the guard from the behaviour.

## 2026-08-16 — Task 10.3: RFC 3464/3834 DSN headers — DONE

- buildBounce gained a 5th param originalHeaders (empty = omit part 3), so the
  builder stays PURE and table-testable; the blob read lives in a new method
  BounceGenerator.originalHeaderBlock. BuildBounceMessage got the same param
  (no production callers — only tests), rather than adding a second wrapper.
- FR-4 Auto-Submitted: auto-replied; FR-6 explicit CTE per part (QP for the
  text part, 7bit for message/delivery-status since RFC 2045 6.4 FORBIDS
  QP/base64 on message/*, which also means Diagnostic-Code must really be
  ASCII, not merely declared so); FR-7 References alongside In-Reply-To, both
  omitted when MessageGuid is empty.
- FR-5 header retrieval: reads a bounded 64KB PREFIX of the blob, not a full
  reconstruct. Justified in a comment: OutboundMessage.blob_hash addresses a
  skeleton when layout is non-empty, but attachment spans only ever live
  INSIDE the top-level body, so the first literal piece always contains the
  complete top-level header block byte-for-byte — correct for split AND legacy
  records without materialising a multi-MB message on the bounce path.
  Reads one byte past the cap to tell "header-only message" (EOF, safe to use)
  from "cap exhausted with no blank line" (truncated, omit the part).
- ADVISOR CAUGHT A SPEC BUG IN MY BRIEF (second time this phase): I wrote
  "class in 2/4/5" for the enhanced status code, but every code this generator
  emits sits next to Action: failed, and RFC 3464 2.3.3 requires class 5 there.
  "250 2.1.5 ok" would have produced Status: 2.1.5 on a failure DSN. Fixed to
  scan for the first class-5 match — not the first match of any class, so a
  wrapped error quoting an earlier successful RCPT still reports the real
  failure code.
- Two pre-existing test cases encoded the OLD class-agnostic contract
  ("caller enforces policy" — the caller does not, it writes Status verbatim).
  UPDATED to the corrected contract, not deleted.
- Post-review: text/rfc822-headers now declares charset=utf-8. REVERTED one
  cosmetic reviewer suggestion (dropping the CRLF before the close delimiter):
  it would have made MIME framing depend on originalHeaderBlock's suffix.

PATTERN: [mime] RFC 2045 6.4 forbids quoted-printable/base64 on message/* parts,
  so message/delivery-status must be 7bit — which makes it the caller's job to
  ensure the content really is ASCII (asciiOnly), not just to declare it.
PATTERN: [dedup-storage] Reading only headers from a queued message does NOT
  need store.RawMessageBytes. blob_hash addresses a skeleton for split records,
  but attachment spans live only inside the top-level body, so a bounded prefix
  read of the skeleton yields the exact header block for split and legacy alike.
  Read one byte past the cap to distinguish EOF from truncation.
PATTERN: [spec] Second spec bug caught by advisor plan review this phase. Both
  were the same shape: a requirement I wrote that was locally plausible but
  contradicted a constraint elsewhere (FR-20 vs FR-21; status class vs
  Action: failed). Plan review before implementation is what catches these —
  a worker following the FRs literally ships the contradiction.
PATTERN: [review] Not every reviewer suggestion is an improvement. Dropping the
  explicit CRLF before a MIME close delimiter is "cosmetic" but transfers the
  framing guarantee to a helper's return-value suffix. Prefer the redundant
  byte over the action at a distance.

## 2026-08-16 — Task 10.4: null envelope sender + FR-2 coupling fix — DONE

- A (FR-3): Deliver("") instead of "MAILER-DAEMON@"+domain. RFC 5321 s6.1.
  The From: header is unchanged; only the envelope sender is null. Safe
  because 10.2's DKIM signing means the bounce passes DMARC on DKIM
  alignment, not on anything envelope/SPF-related.
- Regression risk PROVEN, not assumed: deliver-stage test shows an empty
  env.Sender does not blank MailMeta.SenderEmail (deliver.go:152 lets the
  From: header win), and a scorer test shows an empty EnvelopeSender adds no
  "envelope sender domain mismatch" reason.
- B: the FR-2 guard now derives its domain from the built bounceMsg via
  FromRawMessageDomain — the same helper Sign uses — so the guard and the
  signer cannot silently diverge.
- THE DIVERGENCE TEST IS NON-VACUOUS. I had told the worker to say so rather
  than write a test that cannot fail; the advisor instead found a real seam:
  DKIMRegistry.Get only lowercases, while FromRawMessageDomain -> normalizeDomain
  ALSO strips a trailing dot. So bg.domain "example.test." vs a registry seeded
  at "example.test" genuinely differ, and the test was confirmed to FAIL on
  pre-change code (spurious "no signer" warning while Sign signed happily).
- Fixed post-review: the fallback branch had the same bug INVERTED. When
  FromRawMessageDomain errors, Sign resolves with the same helper and fails
  identically, so delivery is GUARANTEED unsigned — but falling back to a
  bg.domain that has a registered signer suppressed the warning on exactly
  that unsigned delivery. Now warns unconditionally on parse failure; the test
  seeds a signer the old fallback would have found, so a regression fails it.
- Follow-up filed as task 10.8 (not a 10.4 regression): worker.go:206/239 can
  call GenerateAndDeliver with SenderEmail "" -> Deliver("", [""]), spooling
  an envelope with an empty recipient. Already reachable via an inbound
  external DSN hitting a forward rule; 10.4 adds a second route.

PATTERN: [testing] Before accepting "these two values cannot differ, so the
  test would be vacuous", look for a NORMALISATION seam. Two lookups of the
  "same" identity often differ in case/trailing-dot/trimming handling — that
  difference is exactly what makes a coupling test real.
PATTERN: [review] When you fix a divergence between a guard and the thing it
  guards, check the FALLBACK branch for the same divergence inverted. Fixing
  the happy path and leaving the error path is a common half-fix.
PATTERN: [testing] Assert slog level on "level=WARN", never
  strings.Contains(strings.ToUpper(out), "WARN") — the latter matches "warn"
  inside any field value, and silently breaks negative assertions.

## 2026-08-16 — Task 10.8: never DSN a null-sender message — DONE (ran in PARALLEL with 10.5)

- Both worker.go permanent-failure call sites route through one unexported
  generateBounce helper, so the guard cannot be half-applied.
- ADVISOR AVERTED A PRODUCTION PANIC. My test note said "use a fake/spy
  bouncer". Taken literally that means making bouncer an interface — but
  agent.go assigns BounceGen only `if smtpPipeline != nil` while passing it
  unconditionally to NewWorker, and a nil *mail.BounceGenerator stored in an
  interface is NON-nil, so `w.bouncer != nil` would pass and edge nodes would
  panic. Kept the concrete type; spied one level down via the existing mock
  deliverer (GenerateAndDeliver unconditionally calls deliverer.Deliver, so an
  empty channel proves it was not called).
- PARALLEL EXECUTION WORKED. 10.8 ran concurrently with 10.5 in the same
  working tree. Disjoint files, plus an explicit concurrency notice in the
  project rules: scope gates to your own packages, do not fix compile errors
  in the other task's packages, report BLOCKED rather than edit outside scope.
  Both landed clean; I verified each against a full-tree build afterwards.

PATTERN: [go] A nil pointer stored in an interface is NOT nil. Converting a
  concrete dependency to an interface "so tests can fake it" silently breaks
  every `x != nil` guard on it. Check how the dependency is CONSTRUCTED before
  widening its type — here it is conditionally assigned but unconditionally
  passed.
PATTERN: [concurrency] Two norman tasks can share a working tree safely when
  their FILES are disjoint, but they still share the BUILD. Give the second
  task scoped build/test commands and an explicit "these packages are not
  yours, report BLOCKED rather than edit them" rule.

## 2026-08-16 — Task 10.5: Authentication-Results + X-Spam-* trace headers — DONE

- New pure headers.go (strip/prepend/fold) testable without a processor.
- HAZARD 1 taken the honest way: added StageResult.dmarc_result to the proto so
  the antispam stage records the REAL verdict. The alternative — inferring
  dmarc=pass from the absence of a reason string — would have reported a pass
  for every message where DMARC never ran.
- Ordering: DKIM verified against ORIGINAL bytes -> strip -> prepend -> Put
  stamped blob -> trySplit(stamped) -> Size = len(stamped). The forward/relay
  path deliberately keeps env.BlobHash so relayed copies retain signatures.
- FIXED POST-REVIEW, FR-12 SECURITY BYPASS: detectLineEnding returned ok=false
  when the message had no blank-line separator, which made BOTH strip and
  prepend no-op — so a forged "X-Spam-Status: No" survived into storage on a
  bodyless message, the exact shape an attacker controls. A bodyless message is
  valid RFC 5322 and is all header block; it is now stripped like any other.
- Same function also scanned the WHOLE message for "\r\n\r\n", so an LF-only
  header block followed by a CRLF blank line in the BODY was misdetected as
  CRLF and headers were stamped with the wrong terminator. Now compares
  separator POSITIONS.
- Also fixed: composite.Process dropped DmarcResult when an external backend
  (rspamd) won, silently removing dmarc= on every rspamd deployment.
- Two tests encoding the old no-op contract were UPDATED, not deleted.
- Filed 10.9 (storage doubling: stamped blob orphans the SMTP-time blob and
  blob GC defaults to off) and 10.10 (Junk routing threshold changed from
  hardcoded 0.5 to the configured value — out of scope, needs a decision).

PATTERN: [security] A "cannot parse, return unchanged" guard is a BYPASS when
  the function it guards is a sanitiser. Ask what an attacker gains by making
  parsing fail — here, an unparseable (bodyless) message skipped trace-header
  stripping entirely.
PATTERN: [mime] Detect a header block's line ending by comparing the POSITIONS
  of "\r\n\r\n" and "\n\n", never by testing for CRLF first: a body blank line
  after an LF-only header block otherwise wins the vote.
PATTERN: [pipeline] A composite/fallback processor that returns one backend's
  result verbatim drops any field only the OTHER backend populates. Adding a
  field to StageResult means auditing every place a StageResult is chosen.

## 2026-08-16 — Task 10.6: View Original in the web UI — DONE

- New ViewSource handler + source.templ, modelled on ExportEML: same
  resolveMessage ownership check, same store.RawMessageBytes entry point so
  attachment-split records reconstruct correctly.
- Size cap 256 KiB of RAW bytes. Rationale worth keeping: the diagnostic
  payload (Authentication-Results, X-Spam-*) is at the START of a message, so
  head-truncation never costs it; HTML escaping can expand ~6x, so 256 KiB
  bounds the page at ~1.5 MB versus a 25 MB max_message_size producing a
  ~150 MB DOM. Cut at the last newline so no header/base64 line is severed,
  then ToValidUTF8. Truncation is always visible + links to Export.
- FIXED POST-REVIEW, VACUOUS SECURITY TEST: the cross-user 404 test built a
  SEPARATE store per user, so user2's handler had no message at that UID and
  the 404 proved only absence. It would have passed with the ownership check
  deleted. Now both users share one store, and a positive control asserts the
  owner gets 200 at the same UID before user2 is asserted to get 404.
- Also fixed a truncation assertion that only held for ASCII.

PATTERN: [testing] A cross-user/permission test that gives each user its OWN
  fixture proves nothing — the denial comes from absence, not from the guard.
  Share ONE dataset and add a positive control showing the legitimate user CAN
  read the exact resource the other is denied. Without that control, the test
  passes with the guard removed.
PATTERN: [ui] When capping attacker-controlled content for display, cap the
  RAW bytes before escaping (escaping can expand ~6x), cut on a line boundary,
  and re-validate UTF-8 after cutting — a byte-boundary cut produces invalid
  UTF-8 that then lands in the HTML page.

## 2026-08-16 — Task 10.7: operator troubleshooting docs — DONE (workflow failed; finished by hand)

- WORKFLOW FAILURE MODE WORTH REMEMBERING: the classify agent (stage 1) did the
  actual work — 40 tool calls, 72k tokens, real edits to docs/walkthrough.md and
  README.md — but never called StructuredOutput, so pipeline() dropped the item
  and returned [null]. Result: the content existed on disk but NO advisor plan
  review and NO code review ran on it. A "failed" workflow is not the same as a
  workflow that changed nothing — check the tree before assuming.
- Verified every factual claim by hand in place of the missing review. Three
  real defects found:
  (a) doc said "View Source" button; the user-visible label is "View Original"
      (mail.viewSource is the i18n KEY, its VALUE is "View Original"). An
      operator would have hunted for a control that does not exist.
  (b) two cross-links written as ](docs/multi-domain-hosting.md...) from inside
      docs/walkthrough.md -> resolve to docs/docs/... and 404.
  (c) claimed a DATA-stage RBL rejection is "not retried". Wrong: that path
      returns an error rather than a permanentFail (relay.go:145), so the relay
      tries the remaining MX hosts and the message stays queued with backoff,
      bouncing only when retries are exhausted.
- Verified correct as written: HELONotQualifiedWarning and the bounce no-signer
  WARN quoted verbatim; X-Spam-* / Authentication-Results names and formats
  against headers.go; "mail" default DKIM selector; admin labels; both anchors.

PATTERN: [docs] Quoting a UI label from the i18n KEY instead of its VALUE
  produces confident, wrong documentation. mail.viewSource renders as "View
  Original". Always read the locale VALUE for anything the operator will look
  for on screen.
PATTERN: [docs] Relative links inside docs/ must not be prefixed with docs/.
  Grep for "](docs/" in any file already under docs/ before committing.
PATTERN: [workflow] A workflow reporting failure may still have mutated the
  tree. The stage that failed to return structured output can have completed
  its side effects. Always `git status` after a failed run, and treat surviving
  content as UNREVIEWED — the downstream review stages never saw it.

## 2026-08-17 — Phase 11: closing the three adversarial-verification gaps — DONE

- WORKFLOW FAILED AGAIN, DIFFERENT CAUSE: the Haiku classifier returned invalid
  agentType values ('code' and 'user' — not real agent names), so BOTH implement
  stages errored with "agent type not found". 4 of 6 agents ran (2 classify,
  2 advise), 2 errored. Yet the classify/advisor agents had ALREADY WRITTEN the
  tests before the pipeline died — three new test functions on disk, with no
  implement or review stage ever running. Second time this session a "failed"
  workflow left real work behind.
- So both tests arrived UNREVIEWED and, critically, with nobody having proved
  the non-vacuousness the tasks were specifically about. I verified by hand.
- 11.1 TestBuiltinProcessorSignedBounce: real bounce from GenerateAndDeliver ->
  BuiltinProcessor.Process, LookupTXT stubbed to the signing key, DMARC stubbed
  p=quarantine, asserts pass/""/pass. The DKIM verdict is DERIVED from the bytes
  instead of being a literal string. Its paired negative control asserts an
  UNSIGNED bounce is escalated to tag/Junk on the same path — that pairing is
  what makes it real.
- 11.2 TestDeliverDKIMSignaturePreservedAfterStamping: reads STORED bytes via
  store.RawMessageBytes (the IMAP/POP3/Export path), asserts VerifyDKIM=="pass",
  plus a sanity check that stamping actually happened.
- I PROVED 11.2 NON-VACUOUS EMPIRICALLY: sabotaged prependHeaders to mutate a
  signed header, observed `VerifyDKIM(stamped bytes) = "fail", want pass`,
  restored headers.go byte-exact (git diff clean).
- 11.3 done by hand: blob_gc set in both shipped configs, storage-growth
  troubleshooting entry, and TWO dated PRD amendments (FR-12 > FR-13 with the
  reasoning; the storage consequence in Technical Considerations). Compiled
  default left `off` on purpose.

PATTERN: [workflow] A classifier can return an agentType that does not exist,
  killing the implement stage while its own side effects survive on disk. Treat
  ANY non-DONE workflow as "check the tree" — twice now the surviving content
  was complete work that simply never got reviewed.
PATTERN: [testing] The strongest proof a test is not vacuous is a PAIRED
  NEGATIVE CONTROL on the same code path — signed passes / unsigned escalates —
  not a comment claiming it was checked. Where no pairing is natural, sabotage
  the implementation, observe the specific failure message, and restore.
PATTERN: [verification] Adversarial verify earned its cost: the per-requirement
  sweep returned 6/6 PASS and every downgrade came from the completeness critic,
  which looks ACROSS requirements. Two of three gaps were cross-requirement
  interactions no single-requirement verifier could see.

## 2026-08-17 — CI red since ~22 July: one stale test taking out the whole suite

- CORRECTION TO MY OWN EARLIER DIAGNOSIS: I told the user "CI has been dead since
  2026-07-12, likely exhausted Actions minutes". WRONG. CI runs on every push; I
  had polled `gh run list` within seconds and seen nothing because runs had not
  registered yet. Reality: CI runs, `Publish image` goes GREEN, and the `CI` job
  goes RED — which is why deploys kept working and nobody noticed.
- MY VERIFICATION HAD THE SAME HOLE ALL SESSION: test/integration is behind
  `//go:build integration`, so `go test -race ./...` SILENTLY SKIPS IT. Every
  "full suite green" I reported excluded the entire integration suite.
- Root cause: TestMailBlobRequiresMessageOwnership scraped `/mail/blob/{hash}`
  out of a rendered message view. That URL shape is no longer emitted for new
  mail. mail.go:577 `if !isSplit { a.Hash = br.Hash }` deliberately withholds the
  hash for split records (the blob holds ENCODED bytes, not a servable download),
  and task 9.6 stopped storing decoded attachment blobs on compose. So the route
  is legacy-only now. Phase 9 landed 2026-07-22, ten days after the last green
  run — timeline fits exactly.
- THREE WRONG HYPOTHESES BEFORE THE RIGHT ONE, each disproved by experiment:
  (1) attachment below the 32KB dedup threshold -> made it 56KB, got WORSE
      (larger = split = hash withheld);
  (2) async delivery race -> real, but the message WAS arriving;
  (3) wrong folder (Sent vs INBOX) -> Sent uses the same message-scoped route.
  The answer only came from dumping what the page actually rendered.
- One failure cascaded: locally 1 failure, in CI dozens at a uniform ~15.0s then
  a 600s binary timeout with Raft goroutines still live. Fixing the one test took
  the whole suite green (0 failures, 366s).
- Rewrote the test around the route attachments are ACTUALLY served from, with a
  positive control (owner fetches the exact bytes the attacker is denied), and
  kept a legacy /mail/blob probe using the content-addressed hash derived from
  the payload rather than scraped from the UI.
- Aligned Go: go.mod 1.25.12 -> 1.26.2 (CI reads it via go-version-file) and
  deploy/Dockerfile golang:1.25 -> 1.26, whose comment states it is pinned to the
  go.mod directive.

PATTERN: [testing] `go test ./...` SKIPS build-tagged suites entirely and says
  nothing. If a repo has //go:build tags, a bare ./... run is not "the full
  suite" — check for tags before claiming green. Gate with -tags=integration.
PATTERN: [debugging] When a scraping test stops finding what it expects, DUMP
  WHAT THE PAGE ACTUALLY CONTAINS before theorising. Three plausible hypotheses
  cost more time than one probe would have.
PATTERN: [ci] A green deploy pipeline alongside a red test pipeline hides
  breakage indefinitely: the artefact still ships, so nothing feels broken.
  Check per-JOB status, not "did a run happen".

## 2026-08-17 — security job: 20 vulnerabilities + 3 gosec HIGH findings — FIXED

- The CI `security` job had been failing independently of the integration job.
  It runs govulncheck THEN gosec; govulncheck exited non-zero, so the step
  aborted and GOSEC HAD NEVER ACTUALLY RUN. Fixing one exposed the other.
- govulncheck: 20 vulnerabilities. 18 were Go stdlib, all cleared by moving the
  go directive to 1.26.6 (aligning to 1.26.2 was NOT far enough — several
  fixes land in later patch releases). Several were directly relevant to a mail
  server: net/mail x2, net/textproto, mime, html/template x3, crypto/tls,
  crypto/x509, net/http. Remaining 2 were deps: grpc v1.79.3 -> v1.82.1 and
  x/text v0.37.0 -> v0.39.0, bumped individually rather than by a blanket -u.
- gosec (3 HIGH, all confirmed PRE-EXISTING by re-running at the pre-Phase-10
  commit — identical findings):
  - G115 x2 in domain_quota_store.go. NOT a cosmetic conversion warning: a raw
    stored uint64 above MaxInt64 converts to a NEGATIVE int64, and
    IsOverStorageQuota returns `usage >= MaxStorageBytes` — so a negative usage
    makes every quota check return false and SILENTLY DISABLES the quota for
    that domain. Fixed by clamping both directions (fail closed on read,
    reject negatives on write), not by a #nosec.
  - G703 in config_discovery.go: os.Stat on a path from BONGO_CONFIG. Genuine
    false positive — setting that env var requires already controlling the
    process environment. Suppressed with a justified #nosec matching the
    repo's existing convention (cf. importer/state.go G304).
- Added TestDomainQuotaStore_UsageNeverGoesNegative. PROVEN NON-VACUOUS by
  sabotage: reverting the clamp produced `GetUsage = -1, must never be
  negative` and `IsOverStorageQuota = false for a domain whose stored usage
  exceeds its limit`, then restored byte-exact.

PATTERN: [ci] Sequential steps in one CI job MASK each other — govulncheck
  failing meant gosec never ran, for months. When a job goes green after a fix,
  check whether later steps were ever reached before, or you inherit a second
  failure you did not know existed.
PATTERN: [security] Treat a G115 integer-conversion finding as real until you
  trace what CONSUMES the value. Here the conversion fed a `>=` comparison
  whose failure mode was silently disabling a quota — a suppression would have
  hidden an actual enforcement bypass.
PATTERN: [ci] `gosec@latest` / `govulncheck@latest` in CI means a new upstream
  rule or advisory can turn the build red with no code change. Good for
  security freshness, bad for reproducibility — worth a conscious choice.
