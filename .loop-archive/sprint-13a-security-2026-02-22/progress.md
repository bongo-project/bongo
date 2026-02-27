# Progress Log

## Patterns Discovered
- chi: Use `r.With(mw).Post(...)` to scope middleware to a single route
- SecurityHeaders middleware wraps ResponseWriter to inspect Content-Type after handler runs for conditional Cache-Control
- LoginLimiter interface in handlers decouples from middleware package

---

## Execution Log

### 2026-02-22 - Project Setup
- Imported 8 tasks across 4 phases from sprint-13a-security-hardening.md
- Key areas: rate limiting, TOTP 2FA, security headers, HTTPS, remote image blocking
- Existing infrastructure: auth middleware in internal/web/middleware/auth.go, session cookies with HMAC-SHA256, totp_secret proto field exists unused
---

### 2026-02-22 - Task 1: Login rate limiting [sonnet]
- Created internal/web/middleware/ratelimit.go with LoginRateLimiter
- Modified handlers/auth.go to record failures/resets via LoginLimiter interface
- Wired in agent.go with r.With() scoped to POST /login only
- Files: middleware/ratelimit.go, middleware/ratelimit_test.go, handlers/auth.go, agent/agent.go
---

### 2026-02-22 - Task 2: Security headers middleware [sonnet]
- Created internal/web/middleware/security.go with all required headers
- CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy
- Conditional HSTS when r.TLS != nil, conditional Cache-Control no-store for HTML
- Static assets preserve their own Cache-Control (long-lived)
- Files: middleware/security.go, middleware/security_test.go, agent/agent.go
---
