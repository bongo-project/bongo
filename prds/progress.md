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
