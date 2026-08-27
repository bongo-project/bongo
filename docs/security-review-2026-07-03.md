# Internet-Exposure Security Review — bongo-go

Date: 2026-07-03
Context: readiness to run as a real, internet-facing mail server for **example.com**
(single node, Docker, behind an existing Traefik reverse proxy).
Method: 4 parallel focused security reviews (SMTP open-relay, mail DoS/injection,
IMAP/POP3/TLS, web UI + deployment) plus direct code tracing.

## Verdict

**Not ready to face the internet as-is.** The core storage/web engineering is
solid and the prior 2026-04-26 web/CSP hardening holds, but the **mail-protocol
edge** has blockers: unauthenticated mail injection/sender-spoofing, an
open-relay boundary that is accidental rather than enforced, brute-force
protection that is bypassable, no SMTP connection limits, and (in the deploy
config) `/metrics` + `/internal/*` reachable from the internet. There is also a
functional blocker: authenticated mail clients cannot send outbound mail.

Fix the **Blockers** below before exposing port 25/587/443 to the internet.

---

## Blockers (fix before internet exposure)

### B1 — Submission (587) accepts unauthenticated mail; anonymous internal-sender spoofing (SMTP)
`internal/smtp/server.go:70,82,88,111` — `Session.authed` is set in `AuthPlain`
but **never read**. Neither port 25 nor 587 requires AUTH. An anonymous client
can `MAIL FROM:<ceo@example.com>` → `RCPT TO:<staff@example.com>` and it is
delivered to the local inbox as internal-looking mail (phishing/spam injection).
Authentication currently grants no extra capability.
**Fix:** on the submission listener, reject `MAIL`/`RCPT` when `!authed`
(`530 5.7.0 Authentication required`). Tie `MAIL FROM` to the authenticated
user's allowed addresses.

### B2 — No explicit relay / local-domain policy; open-relay safety is accidental (SMTP)
`internal/smtp/server.go:111` (`Rcpt` accepts every recipient, no validation) +
`internal/pipeline/deliver/deliver.go:175-180` (unresolved recipients silently
`skip`ped). Not an open relay **today** only because the deliver stage drops
non-local recipients — there is no `IsLocalDomain`/relay-policy check anywhere in
`internal/smtp/`. Any future catch-all alias or smart-host fallback silently
turns this into a full open relay. Also, the raw message is written to the blob
store **before** recipient validation (`internal/smtp/delivery.go:57-62`) →
unauthenticated disk-fill DoS.
**Fix:** add an explicit `IsLocalDomain(domain)` + recipient-exists check in
`Rcpt()`; for unauthenticated sessions reject non-local recipients with
`550 5.7.1 relay denied`; for authenticated sessions allow external recipients →
outbound queue (this also fixes B8). Add dedicated relay-policy tests.

### B3 — Authenticated mail clients cannot send outbound (functional blocker)
Both 25 and 587 share one backend → `DeliveryPipeline` → deliver stage, which
drops non-local recipients. A Thunderbird/Apple Mail client sending to an
external address gets `250 OK` but the mail is **silently dropped**
(`deliver.go:178`). Outbound only works from the web compose UI.
**Fix:** implement the authenticated-relay path from B1/B2 (authed session →
enqueue external recipients to the outbound relay queue).

### B4 — Login brute-force protection bypassable via X-Forwarded-For
`internal/web/middleware/request.go:19-34` trusts the **leftmost** XFF value.
Traefik *appends* the real client IP, so a client-supplied
`X-Forwarded-For: 1.2.3.4` makes `ClientIP()` return the attacker-chosen value.
The login limiter (5/15min, keyed on ClientIP) is defeated by rotating the forged
header. SMTP AUTH has **no** brute-force protection at all
(`internal/smtp/server.go:76`, `internal/auth/local.go:40`).
**Fix:** behind one trusted proxy, take the **rightmost** XFF entry (or a
configured trusted-proxy count). Add per-IP/per-account failed-AUTH throttling to
SMTP.

### B5 — No SMTP connection cap + per-message memory amplification (DoS)
`internal/smtp/server.go:249-264` binds with plain `net.Listen`; `max_connections`
(config `1024`) is never wired, and there is no per-IP cap. Each 25MB message is
buffered 2–3× in RAM (`server.go:130-137`, `delivery.go:57`,
`deliver.go:93,99`). Connection flood → goroutine/FD/OOM exhaustion.
**Fix:** wrap both listeners in `netutil.LimitListener(l, maxConn)` + a per-IP
connection counter; stream instead of `io.ReadAll` (use `PutReader` /
`io.MultiReader`).

### B6 — `/metrics` and `/internal/*` reachable from the internet (deploy)
`deploy/docker-compose.yml` Traefik router uses a **host-only** rule (no path
filter), so every path on bongo:8080 is public. `/metrics`
(`internal/agent/agent.go:292`) is unauthenticated → operational info leak.
`/internal/*` is HMAC-protected but internet-reachable, and the HMAC signs only
`ts|method|URI` (**not the body**, `internal/security/internal_http_auth.go:25`)
with a 5-minute skew → a captured `POST /internal/user/create` could be replayed
with a swapped body to mint an admin within the window.
**Fix:** add a Traefik path-block middleware for `/metrics` and `/internal/` on
the bongo router (or bind them to a private listener). Sign the body and shorten
the skew in the internal HMAC.

---

## High (strongly recommended before real use)

### H1 — Auth cookies lack `Secure`; no HSTS behind the TLS-terminating proxy
`internal/agent/agent.go:344-351` sets `tlsEnabled=false` under `plain_http`, so
session/2FA cookies are emitted without `Secure` (`auth.go:66`), and HSTS is only
set when `r.TLS != nil` (`security.go:32`), which is never true behind Traefik.
The public HTTPS site ships no HSTS and cookies that can leak over induced HTTP.
**Fix:** honor `X-Forwarded-Proto: https` from the trusted proxy (or add
`web.force_secure_cookies`) to set Secure + HSTS; or add a Traefik `headers`
middleware with `stsSeconds`.

### H2 — Quarantine admin POSTs missing same-origin/CSRF
`internal/agent/agent.go:767-776` — the quarantine admin group omits
`RequireSameOrigin` (present everywhere else). With `SameSite=Lax`, a cross-site
top-level POST can release/delete quarantined (spam/malware) mail.
**Fix:** add `r.Use(middleware.RequireSameOrigin())` to the group. Also add it to
`POST /login` (`agent.go:362`).

### H3 — Silent self-signed TLS fallback on mail listeners
`internal/security/tls.go:68-97` + `internal/agent/agent.go:864-868,905-909`:
IMAP/POP3 read the **cluster** cert (no dedicated `imap.tls`/`pop3.tls` block) and
silently generate an ephemeral `localhost` self-signed cert if the file is
missing — with no warning. On an internet listener this means clients get an
untrusted/mismatched cert (MITM training). *Mitigated in this deploy* because
`bongo.prod.hcl` points `cluster.tls` at the real LE cert and disables POP3, but
the silent-fallback window (before the cert is dumped) remains.
**Fix:** give IMAP/POP3/SMTP their own TLS block with the public hostname; log
loudly and **fail hard** on the self-signed branch for `server`/`edge` roles.

### H4 — Container runs as root (deploy)
`deploy/Dockerfile:39-41` runs as root; compose sets no `no-new-privileges`,
`cap_drop`, or `read_only`. A MIME/SMTP parsing RCE = container root.
**Fix:** non-root user + `CAP_NET_BIND_SERVICE`, `security_opt:
[no-new-privileges:true]`, `cap_drop: [ALL]`.

---

## Medium

- **M1 — CRLF/header injection in compose** `internal/web/handlers/compose.go:624-666`:
  user `Subject`/`To`/`Cc`/attachment `filename` interpolated into headers with no
  CRLF stripping → authenticated user can inject `Bcc:` etc. Same pattern (relying
  on external invariants) in `internal/mail/bounce.go:60-93` and the `Received-SPF`
  header (`server.go:129`). Fix: strip `\r`/`\n` from all header-bound fields.
- **M2 — Blob hash not validated** `internal/store/blobstore.go:199-204`,
  `internal/web/handlers/mail.go:861`, `blob.go:31`: `blobPath` joins an
  unvalidated `hash`. Not cleanly exploitable today (chi keeps the param
  percent-encoded) but fragile. Fix: reject non-`^[a-f0-9]{64}$` at the handlers.
  Also `hash[:12]`/`hash[:2]` slices panic on short input (`blobsync.go`).
- **M3 — No inbound SMTP rate limiting** `flood_threshold` is wired only to the
  outbound compose limiter (`agent.go:828`); the inbound MX path has none. Fix:
  per-IP inbound limiter in `Rcpt`/`NewSession`.
- **M4 — SMTP TLS sets no explicit MinVersion** `internal/smtp/server.go:235`
  (web/IMAP/POP3 pin TLS 1.2; SMTP relies on the toolchain default). Effectively
  1.2 today but should be explicit. Fix: `MinVersion: tls.VersionTLS12`.
- **M5 — Smart-host relay sends AUTH even if STARTTLS failed**
  `internal/smtp/relay.go:120-125,174-185` (only if `smtp.relay` is configured with
  credentials). Fix: refuse PLAIN auth without TLS unless explicitly allowed.
- **M6 — EHLO banner is `localhost`** `internal/smtp/server.go:210,222` — should be
  `mail.example.com` for rDNS/SPF/deliverability alignment.

## Low / config

- `max_message_size` config string ignored; 25MB hardcoded (`server.go:213,225`).
- MIME parsed up to 3× per message (`antispam/builtin.go:101`, `rules/rules.go:156`,
  `deliver.go:99`) — CPU amplification; parse once.
- Internal HMAC reuses `web.session_secret` for session cookies, internal auth, and
  blob-sync; derive per-purpose subkeys.
- Admin/`Disabled` flags snapshotted into the session cookie at login — demotion
  takes effect only at session expiry.

## Mitigated by current deploy config (note, don't regress)

- POP3 cleartext-auth (no STARTTLS enforcement, `pop3/session.go:181`) and POP3
  Slowloris/brute-force — **POP3 is disabled** in `bongo.prod.hcl`.
- IMAP/POP3 cert = cluster cert with `localhost` SANs — mitigated because
  `cluster.tls` points at the real LE cert for `mail.example.com`.

## Confirmed good (no change needed)

- Passwords: bcrypt default cost, constant-time compare (`auth/create.go:27`,
  `auth/local.go:60`). No weak hashing anywhere.
- IMAP: cleartext auth gated behind TLS, STARTTLS injection-safe, per-user
  authorization (no IDOR), no credential logging.
- Max message size (25MB) and max recipients enforced by go-smtp; command/line
  length bounded (2000).
- Prior 2026-04-26 fixes verified intact: `/internal/*` all behind HMAC auth,
  no session-secret logging, recovery-code fail-secure, explicit `SameSite`,
  centralized XFF helper, HTTP timeouts, RequireAdmin integrity, CSP + security
  headers (except HSTS, H1), no secrets in the image.
- Web compose outbound is safe (server-derived sender, external rate-limited).

---

## Remediation status (2026-07-03, same day)

All Blockers and High items were fixed and verified (build + `go test ./...` +
`-race` on changed packages, plus a full server-mode boot):

- **B1/B2/B3 — FIXED.** Explicit relay policy in `internal/smtp/server.go`:
  submission requires AUTH (`530`); unauthenticated senders may deliver only to
  local recipients, external recipients are refused (`550`); authenticated
  sessions relay external recipients via the outbound queue. Tests in
  `internal/smtp/relay_policy_test.go`.
- **B4 — FIXED.** `ClientIP` now takes the rightmost `X-Forwarded-For` entry
  (`internal/web/middleware/request.go`); test updated to assert spoof
  resistance.
- **B5 — FIXED (global cap).** Both SMTP listeners wrapped in
  `netutil.LimitListener`; blob storage streamed via `PutReader`. Per-IP cap
  added but see NEW-1 (ineffective behind a TCP proxy).
- **B6 — FIXED.** Traefik router now denies `/internal` + `/metrics` from public
  source ranges (`deploy/docker-compose.yml`); internal HMAC now signs the body
  and the skew is 90s (`internal/security/internal_http_auth.go`, tests added).
- **H1 — FIXED.** `web.behind_tls_proxy` marks cookies Secure; HSTS emitted when
  `X-Forwarded-Proto: https` from a trusted peer (`security.go`, `request.go`).
- **H2 — FIXED.** `RequireSameOrigin` added to the quarantine admin group and
  `POST /login`.
- **H3 — partially addressed.** Deploy already points `cluster.tls` at the real
  cert and disables POP3; SMTP TLS now pins `MinVersion` 1.2 (M4). The silent
  self-sign fallback itself is unchanged (still a follow-up).
- **H4 — FIXED (cap-drop).** Compose drops ALL caps except `NET_BIND_SERVICE` +
  `no-new-privileges`. Full non-root UID remains a follow-up.
- **M4/M6 — FIXED.** SMTP TLS MinVersion pinned; EHLO banner uses
  `smtp.hostname`. **M1 — partially:** CRLF stripped from the generated
  `Received-SPF` header; compose-handler header sanitization still a follow-up.

### Found and fixed during verification (server mode was untested before)

The security smoke test was the first time the server ran in real `server` role
(a role-resolution bug had masked it). Three deployment-breaking bugs surfaced
and were fixed:

- **DEP-1 — `serve --config` silently ran in dev/ephemeral mode.** `runServe`
  defaulted `role` to `"dev"` and force-set it in viper, overriding the config
  file. Fixed to only override when `--role`/`--dev` is given
  (`cmd/bongo-go/agent.go`). Without this, the deployment would lose all mail on
  restart.
- **DEP-2 — Raft unadvertisable bind address.** `bind_addr = 0.0.0.0` is not a
  valid Raft advertise address; added `advertise_addr = 127.0.0.1` for the
  single node (`deploy/bongo.prod.hcl`).
- **DEP-3 — single-node server never bootstrapped Raft** (only dev did), so it
  hung waiting for a leader. Fixed so a `server` with no `join` peers bootstraps
  itself (`internal/cluster/raft.go`).

### New finding

- **NEW-1 (HIGH for deliverability) — FIXED.** Client IP was lost behind the
  Traefik TCP passthrough (bongo saw Traefik's IP), breaking inbound SPF and the
  per-IP cap. Implemented PROXY protocol: SMTP/IMAP/POP3 listeners now wrap with
  a PROXY-protocol reader (`internal/netx/proxyproto.go`) gated by
  `proxy_protocol` config, and the Traefik TCP routers send
  `proxyProtocol.version=2`. The header is only trusted from loopback/private
  upstreams so a direct attacker cannot spoof the client IP.

## Follow-up remediation (2026-07-03, second pass)

All remaining Medium/Low items were implemented (norman-driven, one commit each):

- **M1 — DONE.** CRLF/control chars stripped from all user values written into
  generated headers (composer + bounce) via `internal/mail.SanitizeHeaderValue` /
  `SanitizeHeaderFilename`.
- **M2 — DONE.** `store.IsValidHash` (`^[a-f0-9]{64}$`); blob handlers return 400,
  store guards Get/Exists/Delete, blobsync slices guarded.
- **M3 — DONE.** Per-IP inbound rate limit (`messages_per_minute`, 451 transient)
  and config-driven `max_connections_per_ip`, keyed on the real client IP.
- **M5 — DONE.** Relay refuses PLAIN AUTH without STARTTLS unless
  `smtp.relay.allow_insecure_auth`.
- **H3 — DONE.** No silent self-signed cert on mail listeners (loud WARN + first-boot
  tolerance; hard-fail on a configured-but-unloadable cert).
- **H4 — DONE.** Container runs as non-root uid 10001 (su-exec + setcap).
- **Low — DONE:** `max_message_size` honoured; per-purpose key derivation
  (`security.DeriveKey`) for cookie vs internal-auth vs blob-sync; cookie
  admin-flag staleness documented. **Deferred:** MIME parse-once (invasive
  Processor interface change, perf-only).
- **DEP-4 (discovered) — DONE.** Raft tolerated an absent `cluster.tls` cert on
  first boot (warn + plaintext) instead of crashing — the deploy shares that cert
  path with the sidecar-provisioned mail cert. Full first-boot server-mode boot
  now verified.

## Suggested fix order
1. B1+B2+B3 (one coherent SMTP auth/relay-policy change) — closes the mail-edge
   blocker and enables client sending.
2. B6 + H4 (deploy config: block `/metrics` + `/internal` at Traefik, drop root) —
   fast, low risk.
3. B4 (XFF rightmost) + H2 (quarantine CSRF) + H1 (Secure cookies + HSTS).
4. B5 (connection cap + streaming) + M3 (inbound rate limit).
5. M1/M2/M4/M6 and the Low items.
