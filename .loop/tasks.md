# Project: Sprint 13a - Security & Login Hardening

> Harden authentication with rate limiting, TOTP 2FA, security headers, HTTPS support, remote image blocking, and secure cookies.

Started: 2026-02-22
Status: in-progress
Source: docs/sprints/sprint-13a-security-hardening.md

---

## Tasks

### Phase 1: Foundation
- [x] 1. Login rate limiting middleware - Create `internal/web/middleware/ratelimit.go` with IP-based rate limiting (5 attempts/15min window, sync.RWMutex, background cleanup goroutine). Modify `internal/web/handlers/auth.go` Login() to record failures/resets. Wire LoginRateLimit middleware to POST /login in agent.go. Done when 6th failed login returns 429.
- [x] 2. Security headers middleware - Create `internal/web/middleware/security.go` with CSP, X-Frame-Options DENY, X-Content-Type-Options nosniff, Referrer-Policy, X-XSS-Protection 0, Permissions-Policy, Cache-Control no-store for HTML. Wire as first middleware on router in agent.go. Static assets get long-lived cache override.

### Phase 2: TOTP Core
- [x] 3. TOTP auth library and proto updates - Add `github.com/pquerna/otp` dependency. Add `recovery_codes` (repeated string, field 8), `totp_enabled` (bool, field 9) to UserProfile proto. Create `internal/auth/totp.go` with GenerateTOTP, ValidateTOTP, GenerateRecoveryCodes, ValidateRecoveryCode functions. Unit tests for all functions. (needs: 1)
- [x] 4. TOTP login flow - Modify Login() handler: after password verification, if totp_enabled, store pending auth in signed cookie (5min TTL), redirect to /login/2fa. Add TwoFactorPage (GET /login/2fa) and TwoFactorVerify (POST /login/2fa) handlers. Try TOTP first, then recovery code fallback (single-use). Wire routes in agent.go. (needs: 3)

### Phase 3: UI & Configuration
- [ ] 5. TOTP setup flow in preferences - Add SecuritySettings, SetupTOTP, ConfirmTOTP, DisableTOTP, ChangePassword handlers to preferences.go. Create security.templ with password change form, TOTP setup/disable, QR code display, recovery codes display. Add "Security" tab to settings navigation. Add admin 2FA policy toggle (Require2FA middleware, admin route, Pebble storage). Wire all routes in agent.go. (needs: 4)
- [x] 6. HTTPS support and secure cookies - Modify agent.go to use ListenAndServeTLS when TLS configured, self-signed in dev mode. Add HSTS header in security middleware when TLS active. Add Secure flag to session cookies when HTTPS. Add HTTPSRedirect option with separate HTTP listener. Update WebConfig with HTTPSRedirect and HTTPRedirectPort fields. (needs: 2)
- [x] 7. Remote image blocking in HTML emails - Modify mail.go to neutralize remote images after bluemonday sanitization (replace http(s) img src with blocked-image placeholder, preserve cid: and data:). Add "Load remote images" button with Alpine.js. Update message.templ with blocked images banner and CSS. (needs: 2)

### Phase 4: Verification
- [ ] 8. Integration tests - Create test/integration/security_test.go (build tag integration) with TestLoginRateLimit, TestTOTPSetup, TestTOTPLogin, TestRecoveryCode, TestSecurityHeaders, TestRemoteImageBlocking, TestPasswordChange, TestRequire2FAPolicy. All tests use dev agent. (needs: 5, 6, 7)

---

## Notes

- totp_secret field already exists in UserProfile proto (field 4, bytes) - unused
- Session cookies already HMAC-SHA256 signed, HttpOnly, SameSite=Lax
- WebConfig TLS struct exists but not wired
- bluemonday UGC policy in mail.go currently allows img tags
- HTTP router is chi v5 (NOT fiber for web routes)
- security.LoadOrGenerate() already exists for self-signed certs (SMTP/IMAP)
