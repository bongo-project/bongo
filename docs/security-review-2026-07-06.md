# Security Review — bongo-go (internet-facing)

> **Status: historical, with three items still open.** A point-in-time snapshot
> from 2026-07-06. All findings were fixed except three multi-node cluster
> transport items — H4 (Raft plaintext fallback and peer-certificate
> verification), M8 (TLS for blob-sync and peer-join HTTP) and L6 (dedicated
> private cluster bind address). Those are not reachable in a single-node
> deployment but become live when a cluster is formed. See `SECURITY.md` for what
> that means if you are running Bongo across more than one node.

**Date:** 2026-07-06
**Scope:** Whole codebase (~50k LoC, `internal/` + `cmd/`), as deployed live at `mail.example.com`.
**Method:** Six parallel adversarial code auditors, one per attack surface (SMTP/pipeline; IMAP/POP3/DAV; web auth/session; web data/XSS; cluster/control-plane/TLS; storage/import). Each read the real code, traced data flow, and reported only plausibly-exploitable issues with `file:line` evidence. Findings were then deduplicated and re-calibrated against the actual deployment topology.
**Predecessor:** `docs/security-review-2026-07-03.md`. Everything that review fixed was re-verified and still holds (see Appendix).

---

## Deployment exposure model (how severity was calibrated)

bongo runs as a single Docker container behind the operator's Traefik. This materially bounds several findings:

- The `bongo` service **publishes no host ports** (`deploy/docker-compose.yml` has no `ports:` block). All ingress is via Traefik on the docker network.
- Traefik routes **only**: web UI (443/`websecure` → :8080) and mail entrypoints (25/587/143/993, TCP, PROXY-protocol v2).
- **Raft (8300) and gossip (8301) are neither published nor routed** → unreachable from the internet. Config is single-node: `advertise_addr = 127.0.0.1`, no peers.
- `/internal` and `/metrics` are additionally IP-allowlisted at Traefik to `127.0.0.1/32,10/8,172.16/12,192.168/16`.
- Mail listeners trust the PROXY-protocol client IP only from loopback/private upstreams.

**Consequence:** the cluster-transport findings (H3, H4, M8, M10, L6) are **not exploitable in this single-node deployment** — they are design flaws that become live the moment a real multi-node cluster is formed or those ports are exposed. The mail (public MX) and web-auth findings **are** live now.

---

## Priority remediation (act on these first)

| # | Finding | Severity | Live now? | Fix effort |
|---|---------|----------|-----------|-----------|
| H2 | `POST /login/2fa` has no rate limit → TOTP brute-forceable | High | **Yes** | Trivial (add existing middleware) |
| H5 | Unbounded DKIM verification → MX resource-exhaustion DoS | High | **Yes** | Trivial (`MaxVerifications`) |
| H1 | Inbound same-domain spoofing accepted; no DMARC enforcement | High | **Yes** | Moderate |
| M7 | IMAP `APPEND` unbounded literal → OOM/disk-fill | Medium | Yes (authed) | Small |
| M2 | SPF evaluator has no DNS-lookup cap → DoS/amplification | Medium | **Yes** | Small |
| M3 | Authenticated submission has no per-user outbound rate limit | Medium | Yes (authed) | Small |
| M11 | Mail HTML sanitizer allows raw `style` → tracker bypass + overlay phishing | Medium | **Yes** | Small |
| M12 | Calendar free/busy IDOR (any user reads any user's schedule) | Medium | Yes (authed) | Trivial |

The cluster Highs (H3/H4) should be fixed **before** any multi-node expansion.

---

## Remediation status (implemented 2026-07-06, branch `security-hardening-2026-07`)

All findings were addressed except a subset of multi-node-only cluster-transport
items, which were deferred with rationale (not reachable in the single-node
deployment; changing the live consensus path carries more risk than the
non-exploitable flaw). Every fix ships with tests; full `-race` suite, lint, and
integration suite are green.

**Fixed:** H1 (full DMARC engine, enforcing) · H2 (2FA rate limit) · H3 (gossip
encryption key wired) · H5 (DKIM verification cap) · M1 (forwarding loop/hop/
self-forward guards) · M2 (SPF DNS-lookup cap + cycle guard) · M3 (per-user
outbound rate limit) · M4 (session-secret min length) · M5 (session epoch
revocation) · M6 (live admin re-check) · M7 (IMAP APPEND cap) · M9 (/metrics
private-IP gate) · M10 (internal-auth replay nonce) · M11 (mail CSS allowlist) ·
M12 (calendar free/busy authz) · L1 (auth timing + generic disabled message) ·
L2 (double-submit CSRF token, enforced outside dev) · L4 (POP3 throttle) · L5
(POP3 STLS requirement) · L7 (user-id path validation) · L8 (`BlobStore.Verify`
hash validation) · L9 (blob-sync size limit) · L10 (import header cap).

**Deferred (multi-node transport, not reachable single-node — harden before
forming a cluster):** H4 (raft plaintext fallback + real peer-cert
verification) · M8 (TLS for blob-sync/join peer HTTP) · L6 (dedicated private
cluster bind address).

---

## Findings

### High

#### H1 — Inbound sender spoofing accepted (no SPF enforcement, no DMARC)
- **Surface:** SMTP/pipeline · **Live: yes (public MX)**
- **Location:** `internal/smtp/server.go:255-292`, `internal/antispam/scorer.go:99-113`, `internal/pipeline/deliver/deliver.go:213-220`; DMARC absent repo-wide.
- **Issue:** The envelope-sender spoof guard only runs on the *authenticated submission* path (`if s.submission && s.authed`, `server.go:266`), so it never fires for inbound MX mail. An unauthenticated attacker on port 25 can send `From: ceo@example.com` to a local victim. SPF is computed but only added as a weak spam signal (never rejects); there is no DMARC evaluation at all. A same-domain spoof with SPF `fail` + DKIM `none` scores ~0.30 — below the 0.5 Junk threshold — and lands in the **INBOX**.
- **Fix:** Add DMARC evaluation (SPF/DKIM alignment against the `From:` domain) after DATA and honour the published policy. Minimum: reject/quarantine inbound mail whose `From:`/`MAIL FROM` is a **local** domain but arrives unauthenticated with SPF `fail`/`softfail` and no aligned DKIM pass. bongo already knows its own domains via the resolver.

#### H2 — `POST /login/2fa` has no brute-force throttle → TOTP is brute-forceable
- **Surface:** Web auth · **Live: yes**
- **Location:** `internal/agent/agent.go:405,407`; `internal/web/handlers/auth.go:161-252`; `internal/auth/totp.go` (Skew=1).
- **Issue:** `POST /login` is wrapped with `loginRL.LoginRateLimit()`; `POST /login/2fa` is **not**. `TwoFactorVerify` records a failure but nothing enforces it on this route. With `pquerna/otp` `Skew=1`, 3 codes are valid at any instant (~90s window), so effective per-guess probability ≈ 3/1e6; at a few hundred req/s an attacker with a known/phished password bypasses 2FA in minutes. The TOTP `/login` step also re-mints a fresh 5-minute pending cookie without recording a failure, so the `/login` limiter never trips either.
- **Fix:** Add `loginRL.LoginRateLimit()` to `/login/2fa` (key on userID+IP). Hard-cap failed 2FA attempts per pending session (invalidate the pending cookie after ~5). Track consumed TOTP timesteps to block replay within the skew window.

#### H5 — Unbounded DKIM signature verification → MX resource-exhaustion DoS
- **Surface:** Storage/pipeline · **Live: yes (public MX)**
- **Location:** `internal/antispam/dkim_verify.go:13`; invoked per-message at `internal/pipeline/antispam/builtin.go:141` and `internal/pipeline/deliver/deliver.go:177`.
- **Issue:** `VerifyDKIM` calls `dkim.Verify(...)` with nil options → go-msgauth `MaxVerifications == 0` = **no cap**. Its `parallelVerify` spawns one goroutine + one `io.Pipe` per `DKIM-Signature` header and does a DNS TXT lookup + RSA/ed25519 verify each. A single message under the 25 MB cap can carry ~130k signature headers → ~130k goroutines + ~130k concurrent DNS lookups + crypto ops — and it runs **twice per message** (antispam + deliver stages).
- **Fix:** `dkim.VerifyWithOptions(r, &dkim.VerifyOptions{MaxVerifications: 5})`. Share the single verification result between the antispam and deliver stages instead of recomputing; run under a context timeout.

#### H3 — Gossip encryption key silently ignored (design flaw; not live single-node)
- **Surface:** Cluster · **Live: no** (gossip port not exposed; no peers)
- **Location:** `internal/cluster/gossip.go:31-58`; `internal/config/config.go:62-65`.
- **Issue:** `GossipConfig.EncryptionKey` is declared and documented but referenced nowhere — `NewGossipNode` never sets `mlCfg.SecretKey`/`Keyring`. An operator setting it gets plaintext, unauthenticated gossip. On a multi-node cluster (with default `BindAddr = 0.0.0.0`), any host reaching the gossip port could join and broadcast forged `NodeMeta` (attacker-controlled `HTTPAddr`), poisoning `Members()` — which the blob syncer trusts as its peer list, sending signed `/internal/blob/{hash}` requests to a hostile endpoint.
- **Fix:** In `NewGossipNode`, decode the key (base64, 16/24/32 bytes) and set `mlCfg.SecretKey`; fail hard on a malformed key; require a key for clustered `server` roles. Bind gossip to a private interface by default.

#### H4 — Raft peer transport: plaintext fallback + fake cert "pinning" (design flaw; not live single-node)
- **Surface:** Cluster · **Live: no** (raft port not exposed; advertises loopback; no peers)
- **Location:** `internal/cluster/raft.go:72-120`, `:92-97` (plaintext fallback), `:310-317` (`InsecureSkipVerify`).
- **Issue:** A TCP raft transport opens on `BindAddr:8300` even for a single node. If the cluster TLS cert is missing it **falls back to plaintext** (anyone reaching 8300 can drive the raft protocol — claim a higher term, push `InstallSnapshot`/`AppendEntries`, overwrite the FSM). Even with TLS, the dialer sets `InsecureSkipVerify: t.tlsCfg.RootCAs == nil`; the `#nosec` comment claims pinning but there is none, so any cert is accepted (MITM). Gated today only by the ports being unreachable.
- **Fix:** Use in-memory transport for a single-node server (no peers). For real clusters require mTLS (`RequireAndVerifyClientCert` + CA) and refuse to start clustered raft without it instead of plaintext fallback. Replace the fake pinning with real `VerifyPeerCertificate` fingerprint checks or a CA pool. Bind to a private interface by default.

### Medium

#### M1 — Mail forwarding has no loop / hop-count / self-forward protection
- **Surface:** SMTP/pipeline · **Location:** `internal/pipeline/deliver/deliver.go:190-211,273-293`; `internal/pipeline/rules/rules.go:140-144`.
- **Issue:** Account-level and rule-based forwarding append targets with no check that the target differs from the recipient and no hop ceiling. A forward to a local address (self, or two users forwarding to each other) routes back through port 25 and re-enters the pipeline — unbounded loop; `ForwardKeepCopy=false` removes even the throttling local copy. No `Received:` header accrues, so nothing breaks the cycle.
- **Fix:** Add and enforce a `Received:` hop count on ingest (drop >25); reject a forward target resolving to the same local recipient; carry a forward-depth counter in `QueueEnvelope`.

#### M2 — SPF evaluator has no DNS-lookup limit and no include-loop guard (RFC 7208 §4.6.4)
- **Surface:** SMTP/pipeline · **Live: yes** · **Location:** `internal/mail/spf.go:49-68,147-154,108-145`.
- **Issue:** `Check → evaluate → matchMechanism` recurses on `include:` and issues `LookupIP`/`LookupMX` per `a`/`mx` term with **no counter** and **no cycle detection**. RFC 7208 mandates a 10-DNS-term cap for exactly this reason. An unauthenticated sender controls the `MAIL FROM` domain and its DNS; a crafted deep/branching or cyclic `include:` tree makes bongo issue a burst of DNS queries per connection (resolver/CPU exhaustion, outbound DNS amplification). Only bound is the 10s deadline.
- **Fix:** Thread a shared lookup counter + recursion depth + visited-domain set through the evaluator; return `permerror` past 10 DNS terms or on a cycle.

#### M3 — Authenticated SMTP submission has no per-user outbound volume limit
- **Surface:** SMTP/pipeline · **Location:** `internal/smtp/server.go:330-338,368-387`.
- **Issue:** The inbound per-IP rate limiter is skipped for submission ("handled separately by the compose pipeline"), but the SMTP submission path enqueues straight to `relayQueue` and never touches the compose pipeline — so there is **no** outbound cap. One set of phished credentials = a full-speed spam cannon (100 rcpts/msg, unlimited msgs), risking blacklisting.
- **Fix:** Per-user outbound message/recipient sliding-window limit on the submission relay path; reject with `452`/`421` when exceeded.

#### M4 — No minimum strength check on `web.session_secret`
- **Surface:** Web auth · **Location:** `internal/agent/agent.go:313-323`; `internal/security/derive.go:18-22`.
- **Issue:** Only `len == 0` is checked outside dev. A weak secret (e.g. `"secret"`) is offline-guessable from one observed cookie (payload is base64 plaintext), after which an attacker forges any cookie including `"adm":true` → full account/admin takeover.
- **Fix:** Reject secrets below ~32 bytes of entropy at startup for non-dev roles; document `openssl rand -base64 32`.

#### M5 — Password change / 2FA-disable do not invalidate existing sessions
- **Surface:** Web auth · **Location:** `internal/web/handlers/security.go:206-263,169-203`; `internal/web/middleware/auth.go:42-47`.
- **Issue:** `SessionData` has no epoch; `RequireAuth` only re-checks `isDisabled` live. After a password change or 2FA disable, previously issued cookies (up to 30 days with "remember") stay valid — changing your password after a compromise does not log the attacker out, and there is no "log out other sessions."
- **Fix:** Add a per-user `session_epoch`/`password_changed_at`, embed it in the cookie, reject on mismatch in `RequireAuth`; bump on password change, 2FA disable, admin reset.

#### M6 — Admin/privilege state is snapshotted into the cookie, not re-evaluated
- **Surface:** Web auth · **Location:** `internal/web/middleware/auth.go:57-66,151-161`.
- **Issue:** `IsAdmin` is copied into the signed cookie at login and only re-read on expiry. Demoting an admin does not revoke rights until logout/expiry (up to 30 days) — unlike the disabled-user path, which is checked live.
- **Fix:** Re-read `IsAdmin` (and disabled) from the profile in `RequireAuth`/`RequireAdmin`, or gate behind the M5 session-epoch.

#### M7 — IMAP `APPEND`: unbounded literal fully buffered → OOM / disk-fill DoS
- **Surface:** IMAP · **Location:** `internal/imap/backend.go:284-303`; no quota anywhere.
- **Issue:** APPEND uses a synchronising literal (go-imap does not size-cap these), streamed to a blob and then read **entirely back into RAM** via `io.ReadAll` + `enmime.ReadEnvelope`. No per-message size limit and no per-user quota, so one authed connection issuing `APPEND INBOX {5000000000}` forces multi-GB heap + unbounded disk on the shared server.
- **Fix:** Enforce a max message size in `Append` (reject once `size` exceeds cap; `io.LimitReader`). Add a per-user storage quota in `DeliverMessage`. Avoid the read-back `ReadAll` — parse headers from a limited streaming reader.

#### M8 — Blob-sync and control-plane peer traffic use plaintext HTTP (design; not live single-node)
- **Surface:** Cluster · **Live: no** · **Location:** `internal/cluster/blobsync.go:107,129`; `internal/agent/agent.go:1162`.
- **Issue:** Blob pulls (`http://…/internal/blob/…`) and raft auto-join go over cleartext. On a multi-node deployment, user mail crosses the network unencrypted; the internal HMAC authenticates requests but gives no confidentiality and doesn't authenticate the response body (integrity is instead saved by content-address re-hashing).
- **Fix:** Use `https://` for peer HTTP with a shared/pinned CA (reuse cluster TLS), or tunnel over the TLS raft link. Default to TLS outside dev.

#### M9 — `/metrics` served unauthenticated on the main listener (Traefik-IP-gated)
- **Surface:** Cluster/control-plane · **Live: partially — mitigated by Traefik allowlist** · **Location:** `internal/agent/agent.go:310`.
- **Issue:** `/metrics` is registered outside the `RequireInternalAuth` group, so on bongo's own port it has no auth; today the only control is Traefik's `ipallowlist`. The repo guardrail says a proxy path-deny is not a boundary. Low sensitivity now (one SMTP counter) but exposes anything added later.
- **Fix:** Move `/metrics` inside `RequireInternalAuth`, or serve it on a loopback-bound admin listener (config already has an unused `Telemetry.Prometheus.Port = 9090`).

#### M10 — Internal HMAC has no replay nonce (90s window) (design; low impact single-node)
- **Surface:** Cluster/control-plane · **Location:** `internal/security/internal_http_auth.go:101-124`.
- **Issue:** Verification accepts any HMAC-valid request within ±90s; no nonce/single-use tracking. A captured signed request can be replayed for up to 90s. Most internal writes are idempotent, but a replayed `/internal/queue/{id}/retry|drop` causes unwanted state changes. Method+URI+body are bound (replay-only, not forgery).
- **Fix:** Add a random nonce to the signed payload; reject seen nonces within the skew window (small LRU). Tighten skew to ~5-10s.

#### M11 — Mail HTML sanitizer allows raw `style` → remote-image-blocker bypass + overlay phishing
- **Surface:** Web data/XSS · **Live: yes (attacker-controlled email)** · **Location:** `internal/web/handlers/mail.go:75-76`; rendered at `internal/web/templates/message.templ:165,236,290`; blocker `mail.go:1690`.
- **Issue:** Policy is `UGCPolicy()` + `AllowAttrs("style")` with no `AllowStyles` CSS allowlist, so style declarations pass through verbatim (verified empirically), and the body is injected into the authenticated app DOM (not a sandboxed iframe). Two consequences: (1) `<div style="background:url(http://attacker/track.png)">` loads remote content — the `<img>`-only remote-image blocker is bypassed (silent read-receipt); (2) `<div style="position:fixed;width:100%;height:100%;z-index:9999">` escapes the message pane and overlays the whole app with attacker content (fake login prompt) in-origin. No script execution (UGCPolicy strips handlers/`javascript:`) — hence Medium.
- **Fix:** Replace `AllowAttrs("style")` with an explicit CSS allowlist (`AllowStyles(...).Globally()` excluding `position`, `background`/`background-image` `url()`, `z-index`, full width+height), or render the body in a sandboxed `<iframe sandbox srcdoc=…>` without `allow-scripts`; strip `url()` from surviving style values.

#### M12 — Calendar free/busy endpoint has no authorization (IDOR)
- **Surface:** Web data · **Live: yes (any authed user)** · **Location:** `internal/web/handlers/calendar.go:282-324`; route `internal/agent/agent.go:620`.
- **Issue:** `FreeBusy` takes the target identity from the `user` query parameter with no check that the caller is the target/admin/shared. `/calendar/freebusy?user=<victim_id>&start=..&end=..` returns the exact start/end of every event on the victim's calendar. Every other calendar handler derives `userID` from context; this one does not.
- **Fix:** Restrict to the caller's own `userID`, or require an explicit sharing/ACL grant between caller and `targetUser`; 403/empty otherwise.

### Low

- **L1 — Username enumeration via auth timing** (all protocols). `internal/auth/local.go:40-65`: unknown user returns before bcrypt; valid user runs bcrypt — a measurable oracle across web login, IMAP, POP3, DAV. *Fix:* compare against a fixed dummy bcrypt hash on the unknown-user path. Also return the same generic message for disabled accounts (`auth.go:112-124`).
- **L2 — CSRF defence is header-only.** `internal/web/middleware/sameorigin.go:10-48`: relies on `Sec-Fetch-Site`/`Origin`/`Referer`; passes if all absent. Safe vs current browsers but no token fallback. *Fix:* add a double-submit/signed per-session CSRF token as defence-in-depth.
- **L4 — POP3 has no brute-force throttle** (prod-disabled). `internal/pop3/session.go:226-260`: no `authlimit` wired, unlike IMAP/DAV. *Fix:* thread a shared `*authlimit.Limiter` keyed on the PROXY client IP.
- **L5 — POP3 accepts plaintext USER/PASS without STLS** (prod-disabled). `internal/pop3/session.go:213-288`: no `requireTLS` gate. *Fix:* reject `PASS` when not on TLS unless dev.
- **L6 — Cluster listeners default to `0.0.0.0`** (design). `internal/config/config.go:403`: one `BindAddr` reused for raft/gossip. *Fix:* separate cluster bind defaulting to private/loopback.
- **L7 — Per-user SQLite/Bleve paths built from `userID` without validation** (latent). `internal/store/metadata.go:461`, `internal/store/search.go:391`: not attacker-controllable today (server UUIDs), but pipeline stages fall back to `userID := recipient` when `resolver == nil`. *Fix:* add `IsValidUserID` and enforce at `OpenUser`/`openUser`.
- **L8 — `BlobStore.Verify` builds a path from an unvalidated hash** (latent, no callers). `internal/store/blobstore.go:160-161`. *Fix:* add `IsValidHash` guard for consistency with `Get`/`Exists`/`Delete`.
- **L9 — Blob-sync pull streams peer content with no size limit.** `internal/cluster/blobsync.go:147`: `PutReader(resp.Body)` unbounded (authed peer only). *Fix:* `io.LimitReader`.
- **L10 — Unbounded folded-header accumulation in importer** (operator-only tool). `internal/importer/tags.go:127-164`. *Fix:* cap cumulative header length.

---

## Verified clean (checked, no exploitable gap)

- **Cross-user isolation (the highest-value target): solid across IMAP, POP3, CalDAV, CardDAV, and web** — every data op is keyed to the authenticated principal; Pebble keys are literal `/user/{userID}/…` strings that path-manipulated GUIDs cannot escape. The one exception found is M12 (calendar free/busy).
- **Blob IDOR:** `/mail/blob/{hash}` enforces `userOwnsBlob`; `/internal/blob/{hash}` behind `RequireInternalAuth`.
- **Attachment preview:** text escaped in `<pre>`, image/PDF allowlist (no SVG), `nosniff`, PDF `CSP: sandbox`, text/html forced to download.
- **Script-XSS in HTML email:** UGCPolicy strips event handlers, `<svg onload>`, `javascript:` (empirically confirmed).
- **SMTP:** open-relay guard, AUTH TLS-gating + advertisement, AUTH brute-force throttle, PROXY-protocol trust, CRLF/header-injection sanitisation, DKIM key handling, relay STARTTLS credential guard, timeouts/connection caps — all correct.
- **Blob hash→path & content-addressable poisoning:** `IsValidHash` gates `Get`/`Exists`/`Delete` and both blob HTTP handlers; fetched blobs re-hashed and rejected on mismatch.
- **tar extraction:** `os.OpenRoot` (Go 1.25) rejects `../`/symlink escape; only dir/regular files; per-file size cap.
- **Message-size DoS pre-parse:** 25 MB cap enforced by go-smtp before blob store/parse.
- **Cookie integrity/flags:** constant-time HMAC, per-purpose subkeys, `HttpOnly`+explicit `SameSite=Lax`+`Secure`; strict CSP (no `unsafe-inline`), `frame-ancestors 'none'`.
- **Internal control-plane:** all `/internal/*` routes behind HMAC (ts|method|URI|body-hash, constant-time); cluster join requires the secret.
- **HTTP server timeouts:** all three `http.Server` instances set read/header/write/idle timeouts.
- **Import path traversal (maildir/thunderbird):** path components come from `os.ReadDir` base names — the earlier-flagged traversals are false positives.

---

## Appendix — prior review (2026-07-03) fixes, re-verified

Confirmed still correct and in place: explicit SMTP relay policy; XFF rightmost/trusted-proxy handling; global + per-IP SMTP connection/rate caps; internal HMAC body signing (90s skew); Secure cookies + HSTS via `behind_tls_proxy`; SMTP AUTH throttle + TLS-gated AUTH mechanisms; envelope-sender spoof rejection on submission; blob cross-user ownership check; disabled-user live session revocation; attachment-preview XSS fix; IMAP/DAV auth throttling (`authlimit`); FSM replay dedup + atomic restore replace-semantics; PROXY-protocol client-IP handling.

*Note:* H1 (inbound spoofing) is the inbound-MX counterpart to the submission-side spoof guard the prior review added — the guard exists but does not cover unauthenticated inbound mail.
