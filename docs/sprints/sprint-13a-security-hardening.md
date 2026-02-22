# Sprint 13a: Security & Login Hardening

**Phase:** 1 (Foundation)
**Goal:** Harden authentication with rate limiting, TOTP two-factor auth, security
headers, HTTPS support, remote image blocking, and secure cookie settings.

---

## Context

Sprints 01–12c deliver a fully functional mail/calendar/contacts/admin server. The
authentication system works but lacks production security hardening — no rate limiting,
no 2FA, no security headers, no HTTPS enforcement, and HTML emails can load remote
images (tracking pixels).

**Existing infrastructure:**
- Login handler: `internal/web/handlers/auth.go` — `Login()` authenticates via bcrypt,
  creates signed session cookie, redirects to INBOX
- Session cookies: HMAC-SHA256 signed, HttpOnly, SameSite=Lax, 24h/30d expiry.
  Missing: Secure flag, CSRF token
- `totp_secret` field already in UserProfile proto (field 4, bytes) — unused
- Auth middleware: `RequireAuth()` and `RequireAdmin()` in `internal/web/middleware/auth.go`
- No security headers middleware exists
- HTTP server: plain HTTP only (`ListenAndServe`), TLS config struct exists in WebConfig
  but not wired
- HTML sanitization: bluemonday UGC policy in mail.go — allows img tags (remote tracking)
- Password handling: bcrypt with DefaultCost, no strength validation
- Config: `WebConfig{TLS TLSConfig}` defined but unused
- Preferences: settings.templ has Appearance/Display/Composition/Localization/Rules tabs,
  no security section

---

## Tasks

### 1. Login rate limiting

Prevent brute-force attacks on the login endpoint.

**Create `internal/web/middleware/ratelimit.go`:**
- `RateLimiter` struct: map of IP → attempt bucket (count + last attempt time)
- `LoginRateLimit(maxAttempts int, window time.Duration) func(http.Handler) http.Handler`:
  - Extract client IP from `r.RemoteAddr` (strip port) or `X-Forwarded-For` if behind proxy
  - Check attempt count for IP in current window
  - If exceeded: return 429 Too Many Requests with `Retry-After` header
  - On failed login (redirected back to /login with error): increment counter
  - On successful login: reset counter for IP
  - Cleanup: background goroutine prunes expired entries every 5 minutes
- Default: 5 attempts per 15-minute window (configurable)
- Thread-safe with `sync.RWMutex`

**Modify `internal/web/handlers/auth.go` → `Login()`:**
- On failed authentication: call `rateLimiter.RecordFailure(ip)`
- On success: call `rateLimiter.Reset(ip)`
- Check disabled account BEFORE password check (avoid bcrypt cost on disabled accounts)

**Wire into agent.go:**
- Apply `LoginRateLimit` middleware to `POST /login` route only

**Done when:** 6th failed login attempt from same IP within 15 minutes returns 429.
Successful login resets the counter. Rate limit window expires after 15 minutes.

### 2. TOTP two-factor authentication

Add optional TOTP 2FA with QR code setup and recovery codes.

**Add Go dependency:** `github.com/pquerna/otp` (TOTP library)

**Add to UserProfile proto (`proto/bongo/v1/profile.proto`):**
```protobuf
repeated string recovery_codes = 8;  // hashed recovery codes
bool totp_enabled = 9;               // whether 2FA is active
```
Run `proto/generate.go` to regenerate.

**Server-wide 2FA enforcement policy:**
- Add `Require2FA bool` to a system settings store in Pebble (`/system/settings/require_2fa`)
- Admin can toggle "Require 2FA for all users" in Hawkeye admin panel (Settings section
  or Dashboard)
- When enabled: any user without `totp_enabled` is forced to `/settings/security/totp/setup`
  on login instead of INBOX. They cannot access any other page until TOTP is configured.
- Add `Require2FAMiddleware` that checks: if server policy requires 2FA AND user doesn't
  have it enabled → redirect to TOTP setup page with flash "Your administrator requires
  two-factor authentication. Please set it up to continue."
- Admin users are also subject to the policy (lead by example)
- The middleware runs AFTER `RequireAuth` in the protected route group

**Create `internal/auth/totp.go`:**
- `GenerateTOTP(email string) (*otp.Key, error)` — generate new TOTP key with
  issuer "Bongo" and account name = email
- `ValidateTOTP(secret, code string) bool` — validate 6-digit code against secret
- `GenerateRecoveryCodes(count int) ([]string, [][]byte)` — generate 8 random
  recovery codes (plaintext for display + bcrypt hashes for storage)
- `ValidateRecoveryCode(hashedCodes [][]byte, code string) (bool, int)` — check code
  against stored hashes, return match index for removal

**Modify `internal/web/handlers/auth.go` → `Login()`:**
- After successful password verification:
  - If `profile.TotpEnabled`:
    - Don't create session yet
    - Store pending auth in short-lived signed cookie (`bongo_2fa_pending`, 5min TTL)
    - Redirect to `/login/2fa`
  - If not enabled: create session as before

**Add new handlers to auth.go:**
- `TwoFactorPage(w, r)` — GET `/login/2fa`:
  - Verify `bongo_2fa_pending` cookie exists
  - Render 2FA code input form
- `TwoFactorVerify(w, r)` — POST `/login/2fa`:
  - Validate `bongo_2fa_pending` cookie
  - Parse `code` from form
  - Try TOTP validation first: `ValidateTOTP(profile.TotpSecret, code)`
  - If TOTP fails, try recovery code: `ValidateRecoveryCode(profile.RecoveryCodes, code)`
  - If recovery code used: remove from stored codes via Raft, show warning
  - On success: create full session cookie, redirect to INBOX
  - On failure: increment rate limiter, redirect back with error

**Done when:** User with TOTP enabled logs in → enters password → redirected to 2FA
page → enters 6-digit code → authenticated. Recovery code works as fallback (single-use).

### 3. TOTP setup flow in preferences

Web UI for enabling/disabling 2FA in user settings.

**Add to `internal/web/handlers/preferences.go`:**
- `SecuritySettings(w, r)` — GET `/settings/security`:
  - Load profile, check totp_enabled status
  - Render security settings page
- `SetupTOTP(w, r)` — POST `/settings/security/totp/setup`:
  - Generate new TOTP key
  - Generate QR code as PNG (using `key.Image(200, 200)`)
  - Store pending secret in session or signed cookie (not yet committed)
  - Render setup page with QR code + manual entry key + verification form
- `ConfirmTOTP(w, r)` — POST `/settings/security/totp/confirm`:
  - Parse verification code from form
  - Validate against pending secret
  - If valid: store `totp_secret` in profile, generate 8 recovery codes,
    set `totp_enabled = true`, store recovery code hashes via Raft
  - Display recovery codes ONCE (user must save them)
- `DisableTOTP(w, r)` — POST `/settings/security/totp/disable`:
  - Require current password for confirmation
  - Clear `totp_secret`, `totp_enabled`, `recovery_codes` via Raft
  - Redirect with flash "2FA disabled"
- `ChangePassword(w, r)` — POST `/settings/security/password`:
  - Parse current_password + new_password + confirm_password
  - Verify current password via bcrypt
  - Validate new password strength (min 8 chars, not same as current)
  - Hash new password, update profile via Raft
  - Flash "Password changed"

**Create `internal/web/templates/security.templ`:**
- `SecuritySettings(totpEnabled bool, email string)`:
  - Password change form (current, new, confirm)
  - TOTP section: status (enabled/disabled), setup/disable button
- `TOTPSetup(qrCodeBase64, manualKey string)`:
  - QR code image (base64 PNG inline)
  - Manual entry key (copyable text)
  - Verification code input + confirm button
- `TOTPRecoveryCodes(codes []string)`:
  - Display 8 recovery codes in monospace grid
  - Warning: "Save these codes. They won't be shown again."
  - "I've saved my codes" confirmation button

**Add "Security" tab to settings navigation** (settings.templ):
- Tabs: Appearance | Display | Composition | Localization | Rules | **Security**

**Add admin 2FA policy toggle:**
- Add to admin dashboard or a new `/admin/settings` page:
  - "Require 2FA for all users" toggle (checkbox/switch)
  - POST `/admin/settings/require-2fa` → store in Pebble `/system/settings/require_2fa`
  - Show count of users without 2FA enabled (so admin knows impact)

**Register routes in agent.go (protected group):**
```
r.Get("/settings/security", prefsH.SecuritySettings)
r.Post("/settings/security/totp/setup", prefsH.SetupTOTP)
r.Post("/settings/security/totp/confirm", prefsH.ConfirmTOTP)
r.Post("/settings/security/totp/disable", prefsH.DisableTOTP)
r.Post("/settings/security/password", prefsH.ChangePassword)
```

**Register admin route:**
```
r.Post("/admin/settings/require-2fa", adminH.ToggleRequire2FA)
```

**Wire `Require2FAMiddleware` into protected route group** (after RequireAuth):
- Exempt paths: `/settings/security/*`, `/logout` (so user can set up TOTP or log out)
- All other protected routes redirect to TOTP setup when policy enforced + user non-compliant

**Done when:** User goes to Settings → Security → clicks "Enable 2FA" → scans QR code
→ enters verification code → sees recovery codes → 2FA active. Can disable with
password confirmation. Can change password.

### 4. Security headers middleware

Add standard security headers to all HTTP responses.

**Create `internal/web/middleware/security.go`:**
- `SecurityHeaders() func(http.Handler) http.Handler`:
  - `Content-Security-Policy`: `default-src 'self'; script-src 'self' 'unsafe-inline';
    style-src 'self' 'unsafe-inline'; img-src 'self' data: cid:; font-src 'self';
    connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`
  - `X-Frame-Options`: `DENY`
  - `X-Content-Type-Options`: `nosniff`
  - `Referrer-Policy`: `strict-origin-when-cross-origin`
  - `X-XSS-Protection`: `0` (disabled — CSP is the modern replacement)
  - `Permissions-Policy`: `camera=(), microphone=(), geolocation=()`
  - `Cache-Control`: `no-store` for HTML pages (allow caching for static assets)

**Wire into agent.go:**
- Apply as first middleware on the router: `r.Use(middleware.SecurityHeaders())`
- For static assets (`/static/*`): override Cache-Control with long-lived cache

**Done when:** Every HTTP response includes all security headers. Verified with
browser dev tools or curl.

### 5. HTTPS support and secure cookies

Enable TLS for the web server and enforce secure cookie settings.

**Modify `internal/agent/agent.go`:**
- If `a.Config.Web.TLS.CertFile != ""` and `a.Config.Web.TLS.KeyFile != ""`:
  - Load TLS cert/key via `tls.LoadX509KeyPair()`
  - Set `a.httpServer.TLSConfig`
  - Call `ListenAndServeTLS()` instead of `ListenAndServe()`
  - Log "web: started (HTTPS)" with address
- If TLS not configured: `ListenAndServe()` as before (HTTP)
- Dev mode: use `security.LoadOrGenerate()` for self-signed cert (same as SMTP/IMAP)

**Add HTTPS redirect (optional):**
- If HTTPS enabled and `config.Web.HTTPSRedirect` is true:
  - Start a second HTTP listener on port 80 (or configurable)
  - All requests → 301 redirect to HTTPS equivalent URL
  - `go http.ListenAndServe(":80", http.RedirectHandler(...))`

**Add HSTS header in security middleware:**
- If TLS is active: add `Strict-Transport-Security: max-age=31536000; includeSubDomains`
- Only set when serving over HTTPS (check `r.TLS != nil`)

**Modify `internal/web/middleware/auth.go` → `CreateSessionCookie()`:**
- Add `secure bool` parameter (or detect from config)
- Set `cookie.Secure = true` when HTTPS is active
- This prevents session cookie from being sent over plain HTTP

**Update config:**
- Add `HTTPSRedirect bool` to WebConfig
- Add `HTTPRedirectPort int` to WebConfig (default 80)

**Done when:** `bongo-go serve` with TLS cert/key configured serves HTTPS. Session
cookies have Secure flag. HSTS header present. Optional HTTP→HTTPS redirect works.

### 6. Remote image blocking in HTML emails

Prevent tracking pixels and malicious content loading in HTML email display.

**Modify `internal/web/handlers/mail.go`:**
- After bluemonday sanitization, run a second pass to neutralize remote images:
  - Replace `<img src="http(s)://...">` with a placeholder:
    `<div class="blocked-image" data-src="original-url">[Remote image blocked]</div>`
  - Preserve `<img src="cid:...">` (inline/embedded images) and `<img src="data:...">`
  - Use `html.Tokenizer` or regex to find and replace external image sources
- Add "Load remote images" button in message view:
  - Alpine.js click handler: find all `.blocked-image` elements, restore original
    `src` from `data-src`, replace with `<img>` tags
  - Button text: "Load remote images" with shield icon

**Modify `internal/web/templates/message.templ`:**
- Add "Remote images blocked" banner above message body when blocked images detected
- "Load images" button (shows only when blocked images present)
- CSS for `.blocked-image` placeholder (grey box with icon)

**Update CSP for image loading:**
- Default CSP blocks external images (`img-src 'self' data: cid:`)
- When user clicks "Load images": either relax CSP via meta tag or load through
  a proxy endpoint

**Done when:** HTML email with remote `<img>` tags shows "[Remote image blocked]"
placeholders. Click "Load remote images" → images load. Inline/embedded images
(cid:, data:) display normally.

### 7. Integration tests

**Create `test/integration/security_test.go` (build tag `integration`):**

**TestLoginRateLimit:**
1. Start dev agent
2. Attempt 5 failed logins (wrong password) from same connection
3. 6th attempt → verify 429 response
4. Wait for window to expire (or use shorter test window)
5. Verify login works again

**TestTOTPSetup:**
1. Login as admin
2. POST `/settings/security/totp/setup` → get TOTP secret from response
3. Generate valid TOTP code using `otp` library with the secret
4. POST `/settings/security/totp/confirm` with code → verify success
5. Verify recovery codes returned (8 codes)
6. Logout

**TestTOTPLogin:**
1. (TOTP already enabled from setup test, or set up fresh)
2. POST `/login` with correct password → verify redirect to `/login/2fa`
3. POST `/login/2fa` with valid TOTP code → verify redirect to INBOX
4. Logout, login again with invalid code → verify error

**TestRecoveryCode:**
1. Login with password → redirected to 2FA
2. POST `/login/2fa` with recovery code → verify success
3. Verify recovery code is consumed (same code fails on next attempt)

**TestSecurityHeaders:**
1. GET any page
2. Verify response headers: CSP, X-Frame-Options, X-Content-Type-Options,
   Referrer-Policy, Permissions-Policy
3. If HTTPS: verify HSTS header present

**TestRemoteImageBlocking:**
1. Deliver email with `<img src="https://tracker.example.com/pixel.gif">`
2. Load message in web UI (GET /mail/INBOX/{uid})
3. Verify response body contains "blocked-image" class, NOT the original URL
4. Verify inline images (cid:) are preserved

**TestPasswordChange:**
1. Login as admin
2. POST `/settings/security/password` with current + new password
3. Logout
4. Login with old password → verify failure
5. Login with new password → verify success

**TestRequire2FAPolicy:**
1. Login as admin
2. POST `/admin/settings/require-2fa` → enable server-wide 2FA requirement
3. Create regular user without TOTP
4. Login as regular user → verify forced redirect to `/settings/security/totp/setup`
5. Verify user cannot access `/mail/INBOX` (redirects to TOTP setup)
6. Complete TOTP setup for user
7. Verify user can now access `/mail/INBOX` normally
8. Disable policy → verify users without TOTP can login normally again

**Done when:** `go test -race -tags=integration ./test/integration/security_test.go` passes.

---

## Exit Criteria

- [ ] Login rate limiting: 5 failed attempts in 15min → 429 Too Many Requests
- [ ] Rate limit counter resets on successful login
- [ ] TOTP setup: QR code generation, verification, recovery codes displayed once
- [ ] TOTP login flow: password → 2FA page → code verification → session
- [ ] Recovery codes work as single-use 2FA fallback
- [ ] TOTP disable requires password confirmation
- [ ] Admin can toggle "Require 2FA for all users" server-wide policy
- [ ] Users without TOTP forced to setup page when server policy requires 2FA
- [ ] Password change in settings (current + new + confirm)
- [ ] Security headers on all responses (CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy)
- [ ] HTTPS support when TLS cert/key configured
- [ ] Secure cookie flag set when HTTPS active
- [ ] HSTS header when serving over HTTPS
- [ ] Remote images blocked in HTML emails by default
- [ ] "Load remote images" button restores blocked images
- [ ] Inline/embedded images (cid:, data:) not blocked
- [ ] "Security" tab in settings navigation
- [ ] `go test -race ./...` passes
- [ ] Security integration tests pass (rate limit, TOTP, headers, images, password)

## What NOT to do in this sprint

- No CSRF tokens (SameSite=Lax + HttpOnly cookies provide sufficient protection for now)
- No WebAuthn / FIDO2 hardware keys (future enhancement)
- No session revocation list (would require server-side session store, future)
- No login audit log / history (future enhancement)
- No password strength meter in UI (future polish)
- No account lockout (rate limiting is sufficient for v1)
- No email notification on security events (future)
- No HTTP→HTTPS redirect in dev mode (only production)
