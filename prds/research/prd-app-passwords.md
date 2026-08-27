# PRD: App Passwords

## 1. Introduction / Overview

Enabling TOTP today protects the web UI and nothing else. CalDAV, CardDAV,
IMAP, POP3 and SMTP submission all authenticate through the same
`auth.Authenticator.Authenticate(username, password)` call and accept the
account password on its own. `/caldav/*` and `/carddav/*` are registered on the
root router, outside the `Require2FA` group (`internal/agent/agent.go`), so a
user who turns on 2FA gets a settings page implying their account is protected
while their calendar, contacts and mail remain reachable with the password
alone.

This is not a bug in the 2FA implementation. TOTP cannot work over HTTP Basic
Auth or SASL PLAIN: those protocols present one secret, once, with no channel
to prompt for a second factor. The standard answer, used by Google, Fastmail,
Migadu and Proton, is **app passwords** — long random per-device credentials
that authenticate protocol clients and are individually revocable.

This PRD covers introducing them.

## 2. Goals

- A user with 2FA enabled can use CalDAV, CardDAV, IMAP, POP3 and SMTP
  submission without their account password being a valid credential anywhere
  it cannot be protected by a second factor.
- Each credential is per-device, independently revocable, and revoking one
  never disturbs the others.
- A leaked app password cannot be used to reach the web UI, change the
  account password, disable 2FA, or read the other app passwords.
- The plaintext of an app password exists only once, in the response that
  created it. It is never stored, logged, or displayed again.
- Adopting app passwords does not require an existing user to reconfigure
  every client on the day it ships.

## 3. User Stories

### US-001: Generate an app password
**Description:** As a user, I want to generate a named credential for one
device so that I can configure my mail client without giving it my account
password.

**Acceptance Criteria:**
- [ ] POST to the create endpoint with `label="iPhone Mail"` returns a
      credential of at least 20 characters drawn from `crypto/rand`, displayed
      exactly once.
- [ ] Re-loading the settings page after creation shows the label, the created
      date and a masked form; the plaintext is not recoverable from any
      endpoint, page or log line.
- [ ] Reading the stored record shows a bcrypt hash, and no field of it equals
      the plaintext.
- [ ] Submitting an empty label returns 400 and creates nothing.

### US-002: Authenticate a protocol client with it
**Description:** As a user, I want my app password to work for IMAP, POP3,
SMTP, CalDAV and CardDAV so that all my clients keep working.

**Acceptance Criteria:**
- [ ] IMAP LOGIN with `(email, app_password)` succeeds and resolves to the
      owning user ID.
- [ ] The same credential succeeds for POP3 USER/PASS, SMTP AUTH PLAIN on the
      submission port, and HTTP Basic on `/caldav/` and `/carddav/`.
- [ ] An app password belonging to user A never authenticates user B, including
      when A and B are in different domains.
- [ ] A revoked credential fails on the very next authentication attempt, with
      no restart and no cache expiry.

### US-003: An app password cannot reach the web UI
**Description:** As a user, I want a leaked app password to be useless against
my account settings so that the blast radius is one device's mail access.

**Acceptance Criteria:**
- [ ] POST `/login` with `(email, app_password)` fails with the same error and
      the same timing as a wrong password, and creates no session cookie.
- [ ] It cannot satisfy the re-authentication prompt guarding a password change
      or a 2FA change (`internal/web/handlers/security.go`).

### US-004: Revoke
**Description:** As a user, I want to revoke a single device's credential
without touching the others.

**Acceptance Criteria:**
- [ ] Revoking one of three credentials leaves the other two working.
- [ ] Revoking is immediate: an IMAP session authenticating a second later is
      refused.
- [ ] An already-revoked or unknown id returns the same response as a
      successful revoke, so the endpoint is not an existence oracle.

### US-005: See what is in use
**Description:** As a user, I want to see when each credential was last used so
that I can tell a live device from a forgotten one.

**Acceptance Criteria:**
- [ ] Each row shows label, created date, and last-used date or "never".
- [ ] Last-used updates within one authentication of a client connecting.
- [ ] Last-used is coarse (date, not timestamp) so the list is not a precise
      activity log of the user's devices.

## 4. Functional Requirements

**FR-1** App passwords are generated from `crypto/rand`, at least 20 characters
of unambiguous alphabet, and grouped for transcription.

**FR-2** Stored as a bcrypt hash. The plaintext is never persisted, logged, or
returned after creation.

**FR-3** Stored at `/user/{user_id}/apppassword/{id}` and written through Raft
like every other user record.

**FR-4** Each record carries: id, label, created_at, last_used_at, and the hash.

**FR-5** Protocol authentication (IMAP, POP3, SMTP submission, CalDAV, CardDAV)
accepts either the account password or a valid app password, subject to FR-7.

**FR-6** Web session login and re-authentication accept **only** the account
password. An app password must fail there exactly as a wrong password does,
including timing.

**FR-7** When a user has 2FA enabled, the account password alone MUST NOT
authenticate a protocol client. This is the requirement that makes the feature
worth building; see Open Question 1 for the migration concern.

**FR-8** A failed app-password attempt is subject to the same per-IP throttling
as a failed account-password attempt, and must not be a cheaper oracle for
enumerating users.

**FR-9** Revocation deletes the record. There is no disabled state to
resurrect.

**FR-10** Creation, revocation and first use are logged with the label and the
credential id. The plaintext and the hash are never logged.

**FR-11** Deleting a user removes their app passwords with the rest of their
data.

## 5. Non-Goals

- OAuth, XOAUTH2 or any token-based scheme.
- Per-protocol scoping (a credential valid for IMAP but not CalDAV). See Open
  Question 4.
- Admin-issued credentials on a user's behalf.
- Expiring credentials on a schedule.
- Any change to how TOTP enrolment or recovery codes work.

## 6. Technical Considerations

**The authenticator has no notion of context.** `Authenticate(username,
password)` is called identically by the web login and by every protocol
(`internal/imap/backend.go`, `internal/pop3/session.go`,
`internal/smtp/server.go`, `internal/agent/agent.go` for DAV, and
`internal/web/handlers/auth.go`). FR-5 and FR-6 require these to diverge, so the
interface has to grow a way to distinguish them. The safer shape is a separate
explicit method for the protocol path rather than a boolean argument, so that a
new call site cannot silently opt into accepting app passwords by passing the
wrong flag. Every existing call site must be audited, since the default for
anything missed should be the restrictive behaviour.

**bcrypt cost on the protocol path is a real risk.** Web logins are rare; IMAP
and CalDAV clients re-authenticate constantly — a phone polling every minute,
several clients per user. bcrypt at `DefaultCost` is deliberately ~100ms of CPU.
Verifying every IMAP LOGIN against every stored app password for that user
multiplies that by the number of credentials. Naively implemented this is both
a latency problem and a self-inflicted DoS vector. Options: a lower cost for app
passwords (defensible — they are 20+ random characters, not a human-chosen
password, so offline brute force is not the threat bcrypt is defending against
here), or an in-memory verified-credential cache with a short TTL, or storing a
lookup prefix so only one hash is compared per attempt. This needs deciding
before implementation, not during.

**Existing timing-equalisation must be preserved.** `internal/auth/local.go`
compares against a dummy hash when the user does not exist, specifically to
avoid an account-enumeration oracle. Any new path must not reintroduce one — in
particular, "user exists but has no app passwords" must cost the same as "user
exists and has three".

**Revocation must not be cached away.** FR-9 requires the next attempt to fail.
Any cache added for the performance concern above needs an invalidation path,
and the test for US-004 should be written against the cached configuration.

**Session epoch.** Changing the account password already invalidates web
sessions via `security.SessionEpoch`. Decide whether revoking an app password
should participate in anything similar; it probably should not, since protocol
sessions are not cookie-based.

## 7. Success Metrics

- A user with 2FA enabled can complete first-time setup of a mail client and a
  CalDAV client using only app passwords.
- The account password fails on every protocol endpoint for a 2FA-enabled user.
- An app password fails at `/login` and at the re-authentication prompt.
- Median IMAP LOGIN latency does not regress measurably against the current
  account-password path.
- No plaintext or hash of an app password appears in any log at any level.

## 8. Open Questions

1. **Does FR-7 apply immediately or behind a setting?** Enforcing it on upgrade
   breaks every configured client belonging to a 2FA user the moment they
   deploy — including, on this deployment, the operator's own. A per-user opt-in
   ("require app passwords for mail clients"), defaulting off and prompted at
   2FA enrolment, avoids a silent breakage but leaves the gap open for anyone
   who does not opt in.

2. **Should users without 2FA also be able to create app passwords?**
   Recommended yes: they are useful for anyone who does not want their real
   password in a phone's settings, and it avoids a second code path.

3. **Should an admin be able to revoke a user's app passwords?** Useful during
   an incident. It also lets an admin lock a user out of mail without touching
   their password, which is a capability worth being deliberate about.

4. **Per-protocol scoping?** Listed as a non-goal above, but if it is wanted
   later the record needs a scope field from the start; adding one afterwards
   means migrating existing records.

5. **What is the display format?** Google uses 16 lowercase letters in four
   groups. Something similar is easy to transcribe and hard to typo; worth
   confirming before it appears in the UI.
