# PRD: Security Review Follow-ups

Source: `docs/security-review-2026-07-03.md` (Medium/Low + remainder items after
the Blocker/High hardening pass). Target: internet-facing mail server for
example.com.

## Goals
Close the remaining medium/low security findings so the server is hardened
beyond the "safe to expose" baseline, without regressing existing behaviour.

## Tasks / Acceptance Criteria

### T1 — M3: per-IP inbound SMTP rate limiting
- Add a per-source-IP throttle on the inbound MX path (connections and/or
  accepted messages per sliding window), keyed on the real client IP (now
  available via PROXY protocol).
- Exceeding the limit returns a transient SMTP error (421 for connections /
  450-451 for messages), not a permanent reject.
- Limits are config-driven with sane defaults; 0/unset disables.
- Table-driven tests for the limiter (allow under limit, block over, window
  reset). No data race (`go test -race`).

### T2 — M1 remainder: CRLF header-injection hardening
- Strip or reject CR/LF (and stray control chars) in all user-controlled values
  interpolated into generated mail headers in
  `internal/web/handlers/compose.go` (`buildRFC5322WithAttachments`:
  Subject/To/Cc/From, attachment filename) and `internal/mail/bounce.go`.
- Prefer a shared helper (reuse `sanitizeHeaderValue` pattern) or proper MIME
  encoding for filenames.
- Tests proving an injected `\r\nBcc:` cannot add a header.

### T3 — H3 remainder: fail loudly on self-signed TLS fallback
- `internal/security` (LoadOrGenerate) + mail-listener wiring: when a cert is
  configured but missing/unloadable for a `server`/`edge` role, log a prominent
  WARN/ERROR (and fail hard where a cert was explicitly configured) rather than
  silently serving an ephemeral self-signed cert.
- dev role keeps the current self-signed convenience behaviour.
- Test or clear manual verification of the warning path.

### T4 — M2: validate blob hash
- Reject any blob hash not matching `^[a-f0-9]{64}$` at `ServeBlob`
  (`internal/web/handlers/mail.go`) and the `/internal/blob` handlers
  (`internal/web/handlers/blob.go`), and defensively in
  `internal/store/blobstore.go` (Get/Exists/Delete). Guard the `hash[:2]` /
  `hash[:12]` slices against short input.
- Table-driven tests for valid/invalid hashes (including `..`, percent-encoded,
  short).

### T5 — H4 remainder: non-root container
- `deploy/Dockerfile` + `deploy/docker-compose.yml`: run as a dedicated non-root
  UID with `CAP_NET_BIND_SERVICE`, keeping /logs (bind mount) and the DKIM key
  readable/writable by that UID (entrypoint chown-then-drop, or documented host
  UID alignment). Keep no-new-privileges + cap_drop ALL.
- `docker compose config` still valid; image builds; container boots healthy.

### T6 — M5: relay AUTH requires STARTTLS
- `internal/smtp/relay.go`: do not send PLAIN credentials to a smart host unless
  STARTTLS completed, unless a new `smtp.relay.allow_insecure_auth` flag is set.
- Test the guard.

### T7 — Low: config + efficiency cleanups
- Honor `smtp.limits.max_message_size` (parse the size string) instead of the
  hardcoded 25MB in `internal/smtp/server.go`.
- Parse MIME once per message and pass the parsed envelope down the pipeline
  stages instead of re-parsing 3x (antispam/rules/deliver).
- Tests where practical; no behaviour regression.

### T8 — Low: per-purpose key derivation
- Derive distinct subkeys from `web.session_secret` (e.g. HKDF/HMAC with a
  purpose label) for: session-cookie HMAC, internal control-plane HMAC, and
  blob-sync secret — applied consistently at every signer AND verifier so
  nothing breaks. Add a short code comment noting admin/disabled flags are
  snapshotted into the session cookie until expiry.
- Full `go test ./...` passes (internal auth, blob sync, cookies round-trip).

### T9 — CI: build and publish container image to GHCR
- Add a GitHub Actions workflow that builds `deploy/Dockerfile` and pushes to
  `ghcr.io/lhaig/bongo-go` on push to `main` and on version tags. Login via the
  built-in `GITHUB_TOKEN` (packages: write). Tags: `latest`, the commit SHA, and
  semver on tag pushes. Target `linux/amd64` (the deploy target).
- Update `deploy/docker-compose.yml` so the remote server PULLS the image
  (`image: ghcr.io/lhaig/bongo-go:latest`) instead of building locally (the repo
  isn't on the server). Keep a documented way to build locally for dev.
- Update `deploy/README.md` deploy steps: `docker compose pull && docker compose
  up -d` instead of `--build`.
- Acceptance: workflow YAML is valid and self-consistent; compose still validates
  with `docker compose config`.

## Non-Goals
- Composite Raft commands for cross-user send + queue atomicity.
- Moving the control-plane onto a dedicated private listener (Traefik already
  denies it publicly).
