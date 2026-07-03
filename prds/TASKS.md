# Tasks

Source PRD: security review follow-ups.

## Phase 1: Security follow-ups

| # | Task | PRD | Status | Notes |
|---|------|-----|--------|-------|
| 1.1 | M3: per-IP inbound SMTP rate limiting | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | smtp + config |
| 1.2 | M1: CRLF header-injection hardening (compose, bounce) | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | |
| 1.3 | H3: fail loudly on self-signed TLS fallback | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | tls.go + agent.go |
| 1.4 | M2: validate blob hash | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | |
| 1.5 | H4: non-root container | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | deploy only |
| 1.6 | M5: relay AUTH requires STARTTLS | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | |
| 1.7 | Low: honor max_message_size + parse MIME once | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | |
| 1.8 | Low: per-purpose key derivation | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | risky; opus |
| 1.9 | CI: build + publish image to GHCR; compose pulls it | [prd-security-followups.md](backlog/prd-security-followups.md) | TODO | deploy/ci |
