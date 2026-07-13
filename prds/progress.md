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
