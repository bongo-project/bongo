# Tasks Archive

Append-only archive of fully-completed phases collapsed out of TASKS.md.

## Phase 1: Security follow-ups

| # | Task | PRD | Status | Notes |
|---|------|-----|--------|-------|
| 1.1 | M3: per-IP inbound SMTP rate limiting | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | messages_per_minute + max_connections_per_ip |
| 1.2 | M1: CRLF header-injection hardening (compose, bounce) | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | internal/mail sanitize helpers |
| 1.3 | H3: fail loudly on self-signed TLS fallback | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | LoadCert + mailListenerTLS |
| 1.4 | M2: validate blob hash | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | store.IsValidHash + handler 400 |
| 1.5 | H4: non-root container | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | non-root uid 10001, su-exec + setcap |
| 1.6 | M5: relay AUTH requires STARTTLS | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | shouldSendAuth + allow_insecure_auth |
| 1.7 | Low: honor max_message_size + parse MIME once | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | ParseSize wired; MIME-once deferred |
| 1.8 | Low: per-purpose key derivation | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | DeriveKey at choke points; verified live |
| 1.9 | CI: build + publish image to GHCR; compose pulls it | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | ghcr.io/lhaig/bongo-go |
| 1.10 | Discovered: raft tolerates absent cluster TLS cert on first boot | [prd-security-followups.md](done/prd-security-followups.md) | DONE (2026-07-03) | warn + plaintext fallback |
