# PRD: App passwords v0.15.3

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

### US-006: Add an Apple device with one profile
**Description:** As a user of an iPhone, iPad or Mac, I want to set up Mail,
Calendar, Contacts and Reminders in one step so that I never see or type a
password.

**Acceptance Criteria:**
- [ ] Add a device, with Apple chosen and a label given, creates an app password
      and returns a `.mobileconfig` holding the mail, CalDAV and CardDAV accounts
      of the signed-in user, with that app password in each.
- [ ] The response is sent with `Cache-Control: no-store`. The profile is built
      in the response and is not stored; a second download is not possible.
- [ ] The public `/.well-known/mobileconfig` is unchanged and contains no
      password.
- [ ] Manual check on an iPhone and a Mac: after installing the profile, Mail,
      Calendar, Contacts and Reminders all connect with no password prompt.
- [ ] The new device appears in the list from US-005 under its label, and
      revoking it stops all of those accounts.

### US-007: Hand a profile to a phone by QR code
**Description:** As a user at a desktop browser, I want to scan a code with my
phone so that I do not have to sign in to the web UI on the phone.

**Acceptance Criteria:**
- [ ] Add a device shows a QR code holding a one-time link. The link holds a
      random token, never the app password.
- [ ] Opening the link creates the app password and returns the profile from
      US-006. The phone needs no session.
- [ ] A second fetch of the link, a fetch after 5 minutes, and a fetch of an
      unknown token all return the same response, and create nothing.
- [ ] Only a hash of the token is stored, through Raft, so a link works once
      across all nodes. The token is not written to any log, including request
      logs.
- [ ] The page shows a warning that anyone who scans the code first gets the
      credential. It updates to show when the link has been used.
- [ ] Fetches of the link are throttled per IP like a failed login.

### US-008: Add any other client
**Description:** As a user of Thunderbird, Outlook or another client, I want the
password and the server settings together so that setup is one paste.

**Acceptance Criteria:**
- [ ] Add a device, with Other chosen, shows the app password once, with the
      user name, IMAP, SMTP, CalDAV and CardDAV settings beside it.
- [ ] The settings come from the same values as the autoconfig handlers, and a
      test asserts they match.
- [ ] Each value has a copy button.

### US-009: Require app passwords for an account
**Description:** As a user with 2FA, I want a per-account switch that stops my
account password working in mail clients, turned on at a point when my clients
will not break.

**Acceptance Criteria:**
- [ ] With the switch on, the account password fails on IMAP, POP3, SMTP
      submission, CalDAV and CardDAV, and an app password succeeds.
- [ ] With the switch off, both succeed.
- [ ] 2FA enrolment after this release goes on to Add a device, and turns the
      switch on when the user finishes that step.
- [ ] An account that had 2FA before this release keeps the switch off, and
      Security settings shows a notice offering to turn it on. Nothing changes
      on upgrade.
- [ ] Turning the switch off requires the same re-authentication as a 2FA change.
- [ ] The switch cannot be turned on by an app password, and is not offered to an
      account without 2FA.

### US-010: An admin revokes a user's app passwords
**Description:** As an admin responding to an incident, I want to revoke all of a
user's app passwords so that I do not have to disable the account.

**Acceptance Criteria:**
- [ ] The admin user page has a Revoke all app passwords action, behind the
      existing admin checks and CSRF protection. A domain admin can use it only
      for users in their domains.
- [ ] After it, every app password of that user fails on the next attempt, and
      the account password and web sessions are unchanged.
- [ ] The action is logged with the admin, the user and the count. No credential
      or hash is logged.

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

**FR-7** When an account has the switch from US-009 on, the account password
MUST NOT authenticate a protocol client. This is the requirement that makes the
feature worth building. See decision 1 in section 8.1.

**FR-8** A failed app-password attempt is subject to the same per-IP throttling
as a failed account-password attempt, and must not be a cheaper oracle for
enumerating users.

**FR-9** Revocation deletes the record. There is no disabled state to
resurrect.

**FR-10** Creation, revocation and first use are logged with the label and the
credential id. The plaintext and the hash are never logged.

**FR-11** Deleting a user removes their app passwords with the rest of their
data.

**FR-12** Add a device is the main way to create an app password. It offers
Apple (US-006, US-007) and Other (US-008). The plain create form from US-001 is
the same path as Other.

**FR-13** A personal profile holds the plaintext app password, so it counts as
the one response allowed by FR-2. It is never stored, cached or logged.

**FR-14** A QR link is a bearer credential until it is used. It is single use,
expires after 5 minutes, and is stored only as a hash.

**FR-15** The change-password form offers "Also revoke my app passwords", ticked
by default. If the password changes but a revoke fails, the user is told, not
shown plain success.

**FR-16** Creating an app password, by any route, requires the account password
through the existing re-authentication check.

**FR-17** A user holds at most 10 app passwords. Create refuses the 11th with a
message to revoke one first. Every create route goes through this check.

## 5. Non-Goals

- OAuth, XOAUTH2 or any token-based scheme in this PRD. Section 8.2 records why
  and what would change that.
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
- The account password fails on every protocol endpoint for an account with the
  switch on.
- An iPhone is set up for mail, calendars, contacts and reminders by scanning one
  QR code and installing one profile, with no password typed.
- An app password fails at `/login` and at the re-authentication prompt.
- Median IMAP LOGIN latency does not regress measurably against the current
  account-password path.
- No plaintext or hash of an app password appears in any log at any level.

## 8. Open Questions

1. Resolved: see decision 1 in section 8.1.

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

6. **Should the personal profile be signed?** Unsigned, Apple shows "Not
   verified" on install. It still installs. Signing with the server's TLS
   certificate may remove the warning; this has not been tested. Test it before
   US-006 is planned.

### 8.1 Decisions

1. (operator, 2026-11-05) FR-7 is a per-account switch, not enforced on upgrade.
   It turns on at the end of 2FA enrolment, after the user has added their
   devices. Accounts with 2FA from before this release turn it on themselves
   (US-009).
2. (operator, 2026-11-05) Setup goes through Add a device: a personal Apple
   profile, a QR code to hand it to a phone, and password with settings for other
   clients (US-006 to US-008).
3. (operator, 2026-11-05) Ships as v0.15.3, ahead of v0.16.0.
4. (operator, 2026-11-05) Open question 3: an admin can revoke all of a user's
   app passwords, in one logged action on the admin user page. This is US-010.
5. (operator, 2026-11-05) Open question 4: no per-protocol scoping. A scope field
   can be added later, with existing records meaning all protocols.
6. (planning, 2026-11-05) Open question 2: users without 2FA can create app
   passwords.
7. (planning, 2026-11-05) Open question 5: four groups of lowercase letters,
   at least 20 letters in total.
8. (planning, 2026-11-05) bcrypt cost on the protocol path: each app password
   carries a short public lookup ID, so an attempt compares one hash. There is no
   cache, so revocation needs no invalidation.
9. (operator, 2026-11-05) A session the
   require_2fa policy is holding for enrolment cannot create an app password.
   Require2FA exempts everything under /settings/security, so without this a
   password-only session could mint a credential that outlasts enrolment. Listing
   and revoking stay available. Decision 6 still applies when no policy holds the
   session.
10. (operator, 2026-11-05) Changing the account password offers "Also revoke my
    app passwords", ticked by default (FR-15).
11. (operator, 2026-11-05) Creating an app password asks for the account password
    again, through the existing re-authentication check (FR-16).
12. (operator, 2026-11-05) At most 10 app passwords per user (FR-17).

### 8.2 Candidates for a later release

OAuth for mail clients, checked 2026-11-05:

- Thunderbird 155 and 156 (September 2026) added custom OAuth configuration for
  IMAP, POP3 and SMTP, per release coverage not yet checked against Mozilla's
  notes. It is configured per client: no evidence was found of dynamic client
  registration, so a user cannot simply point it at a self-hosted server.
  Mozilla bug 1602166 asks for that.
- `draft-ietf-mailmaint-oauth-public`, the spec for this, expired on 12 February
  2026 in the revision found.
- Apple documents OAuth configuration for Exchange accounts only. No Apple
  documentation for OAuth on a custom IMAP server was found.
- No evidence was found of OAuth for CalDAV or CardDAV in Apple clients,
  Thunderbird or DAVx5 on a custom server.

Bongo would need an OAuth authorisation server, XOAUTH2 or OAUTHBEARER in SMTP
and IMAP, and a client that can use them. Revisit when Apple supports OAuth on a
custom server, or when Thunderbird can register itself with one. App passwords
stay needed for every other client either way.

Found while building the credential record and protocol login (2026-11-05):

- Open IMAP and POP3 sessions survive a revocation; the next login fails.
- On a follower, last_used_at and the first-use log line are never written,
  because the touch is not forwarded to the leader. One-node production is not
  affected. Fix before multi-node clusters.
- Lockout after wrong app passwords is tested for IMAP and SMTP, not POP3 or DAV
  Basic. Both call the same failure path.
- `apppassword.pb.go` was generated with protoc-gen-go v1.36.12; the other files
  are v1.36.11.

Found while building the settings UI and admin revoke (2026-11-05):

- The 2FA recovery codes page has the exposure the create response had: htmx can
  write it to localStorage history. Add `hx-history="false"` there.
- `AdminHandler.hxRedirect` on delete-user sets HX-Redirect on a 303, which htmx
  does not act on. Not reproduced.
- Labels accept bidi format characters. They show only to the owner and in logs.
- After a create, the address bar shows a POST-only URL, so reloading by address
  gives a 405. This follows from showing the password in the POST response.
- A deslop CLI scan of the phase's Go changes found one true new copy, now the
  shared `redirectAfterAction`. The other production groups are existing store
  and route patterns. 34 new test-only groups were not reviewed.
