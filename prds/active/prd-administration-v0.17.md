# PRD: Administration and organisational deployments v0.17.0

## 1. Introduction / Overview

The feature-parity audit of the original Bongo
(`prds/research/parity-audit-original-bongo.md`, rows cited as #N) lists the
features an organisation, rather than an individual, asks of a mail server:
directory authentication, S/MIME, per-user controls, per-user backup and
restore, journaling, disclaimers, attachment policy, anti-spam settings in
the web admin, greylisting, certificate handling, a further translation and
a check for new releases. This PRD defines the scope of v0.17.0.

Every story was checked against `main` at `6725448` on 2026-10-26. Where the
code disagrees with the audit, the story says so; section 8.2 lists the
corrections together. The most important:
- **There is no greylisting at all** (#47 says "only through rspamd"). The
  rspamd backend maps rspamd's `greylist` action to `pass`, with a comment
  saying greylisting happens at the SMTP level, which it does not.
- **Production does not use Traefik** (#102 says it does). Since 2026-09-12
  Bongo terminates TLS itself with a lego-managed certificate, and the
  container is restarted after each renewal because nothing reloads
  certificates.
- **SIGHUP reloads only five keys** (`internal/config/diff.go:52-58`), so
  "HCL plus SIGHUP reload" (#49, #98) covers far less than it suggests.

**The scope rule.** The in-scope list is section 3. Anything a review finds
during this release goes into section 8.2, not into TASKS.md, unless the
change under review introduced it.

| Phase | Theme | Stories |
|-------|-------|---------|
| A | Operations: version check, certificates, greylisting | US-001 to US-005 |
| B | Mail policy | US-006 to US-009 |
| C | Settings in the web admin | US-010 to US-011 |
| D | Backup and export | US-012 to US-013 |
| E | Directory | US-014 to US-015 |
| F | Certificates for users and S/MIME | US-016 to US-017 |
| G | Translation | US-018 |
| H | Mail and sharing for users | US-019 to US-023 |

Imported on 2026-11-26: v0.17.0 is TASKS Phases 43 (H), 44 (A), 45 (B), 46 (C)
and 47 (G); Phases D, E and F are imported for v0.18.0. Dependencies: US-002 before
US-001 can be tested end to end (US-001 can be built against a fake
endpoint); US-004 needs US-003; US-010 needs US-005 for the greylisting page;
US-015 needs US-014; US-013 needs US-012; US-023 needs US-022.

### Size key

S: one focused session. M: two or three packages, a proto change or a new
form. L: a new subsystem or a cross-cutting change.

## 2. Goals

- An operator learns about a new or security release from the admin
  dashboard, and the check sends nothing beyond a version and a random
  instance ID.
- Certificates renew without a restart, and can be obtained by Bongo itself.
- Greylisting exists and is off by default.
- Admins can switch protocols and features off per user and set per-user
  send limits.
- Domains can add a disclaimer, strip dangerous attachments and journal all
  mail.
- Anti-spam and anti-virus settings can be changed in the web admin.
- One user's data can be exported, backed up and restored on its own.
- Users can authenticate against LDAP, and the directory is available as an
  address book.
- S/MIME signatures are verified in the web view; users can log in with a
  client certificate.
- The UI is available in Brazilian Portuguese.

## 3. User Stories

### Phase A: Operations

Release note: Bongo checks once a day for a newer release and shows a banner
to global admins (`update_check = false` disables it). Certificates reload
without a restart, Bongo can obtain its own certificate over ACME, and
greylisting is available.

### US-001: Version check
**Source:** audit #104; design set by the operator (8.1 item 2). **Size:** M.

**What exists:** the audit is correct. `/admin/version` returns the running
build (`internal/web/handlers/version.go`); there is no outbound call to the
project and no instance ID (the Raft `LocalID` is `server.name`, which can be
a hostname and must not be sent).

**Design:**
- Instance ID: 128 random bits from `crypto/rand`, hex-encoded, written once
  by the leader to `/system/instance_id` through Raft when absent, so a
  cluster has one ID. It is not derived from any secret or name.
- The leader checks once a day: the first check runs 5 to 60 minutes after
  it becomes leader (random), each later one 24 hours plus a random 0 to 2
  hours after the previous. A node that is not leader never checks.
- Request: `POST {server.update_check_url}` with body
  `{"version":"0.17.0","instance_id":"…"}` and `User-Agent: bongo-go`.
  Nothing else: no hostname, domain, user or message counts, configuration
  or OS. Timeout 10 seconds, no redirects, response capped at 16 KiB.
- Response: `{"latest":"0.17.2","release_notes_url":"https://…","security":true}`.
  `latest` must parse as a semantic version; `release_notes_url` must be an
  `https` URL on the same host as the check URL; anything else discards the
  response.
- The result is written to `/system/update_check` (latest, URL, security,
  checked at) so every node's dashboard shows the same banner.

**Acceptance Criteria:**
- [x] `server.update_check` (default true) and `server.update_check_url`
      (default the project endpoint, open question 1). With
      `update_check = false`, no request is ever made (a test with a
      counting fake server over a simulated week).
- [x] In dev mode no request is ever made, whatever the setting.
- [x] A test captures the request body and asserts it has exactly the two
      fields, and that no header carries a hostname, domain or address.
- [x] Only the leader checks: in a three-node test cluster, one request per
      simulated day.
- [x] Global admins (not domain admins) see a banner on `/admin` when
      `latest` is newer than the running version, linking the release
      notes; when `security` is true the banner uses the error style and
      says it is a security release. All text is translated.
- [x] A malformed, oversized, redirected or non-https response, or a release
      notes URL on another host, shows no banner and logs one line at debug
      level; errors do not retry more often than the daily schedule.
- [x] `/admin/version` additionally reports when the last check ran and its
      result.
- [x] `docs/` gains an admin guide section on the check and a privacy notes
      page stating what is sent, when, to where, what the endpoint stores,
      that Cloudflare sees the connecting IP address at its edge as any
      HTTPS service would, and how to turn the check off.

**Security:** the response is untrusted: the banner renders the version and
URL escaped and only after validation, so a compromised endpoint cannot
inject markup or link elsewhere. **Cluster:** `/system/instance_id` and
`/system/update_check` are plain PUTs; an old node ignores them.

### US-002: The update endpoint
**Source:** US-001 design. **Size:** S.

**Acceptance Criteria:**
- [x] A Cloudflare Worker answers `POST /v1/check` with the response JSON for
      the current release (configured as Worker variables at release time).
- [x] It stores only aggregate counts per day: the number of distinct
      instance IDs per version. To count distinct IDs it keeps the day's set
      of IDs and deletes it once the day's count is written, at most 48
      hours later. It never stores or logs IP addresses, and request logging
      in the Worker is off.
- [x] Requests with a body over 1 KiB, an invalid version or an ID that is
      not 32 hex characters get 400 and are not counted.
- [x] The Worker's source lives in this repository under `tools/` or in the
      project's website repository (open question 2), with a README naming
      what it stores.
- [x] The release checklist gains a step to update the Worker's variables.

**Superseded by decision 16 (2026-11-27):** the Worker was never deployed. The
endpoint is a Go service in this repository, run on the project's own hosting:
- [x] `cmd/bongo-update`, a third binary with its own image, serves the same
      `POST /v1/check` contract and `GET /releases/{version}` redirect, so
      v0.17 servers need no change. Plain HTTP behind the operator's reverse
      proxy, which terminates TLS; explicit server timeouts.
- [x] The latest version defaults to the binary's own release version, so
      deploying the new image is the release step; the security flag and the
      release notes URL are flags or environment variables.
- [x] Counts are kept in PostgreSQL: the same two tables as the Worker (the
      day's set of instance IDs, deleted once the day's count is written, at
      most 48 hours; daily counts kept 13 months), rolled up and pruned by an
      in-process hourly job. The DSN comes from the environment and is never
      logged.
- [x] It never reads, stores or logs the client address or any request
      header (`RemoteAddr`, `X-Forwarded-For` included), has no access log,
      and a test pins this as `config_test.go` did for the Worker.
- [x] `deploy/update-worker/`, its CI job and Makefile target are removed;
      the release checklist step and `docs/privacy.md` name the new service
      and no longer mention Cloudflare.
- [x] The Nomad job and the proxy route are the operator's (infra repo),
      with the proxy's access log off for this route. Nothing in this
      repository names where it is hosted.

### US-003: Certificates reload without a restart
**Source:** audit #102 (corrected). **Size:** M.

**What exists:** certificates are loaded once (`internal/security/tls.go`;
direct `tls.LoadX509KeyPair` calls in SMTP, the web listener, Raft and the
agent gRPC server), and every TLS key is a restart key
(`config/diff.go:89, 93, 105`). Production's renewal script restarts the
container (infra repository, `lego/bongo-cert-install.sh`).

**Acceptance Criteria:**
- [x] The web, SMTP, IMAP and POP3 listeners serve certificates through
      `GetCertificate` from a holder that swaps atomically.
- [x] SIGHUP reloads the certificate and key files; a pair that fails to
      parse, does not match, or has expired keeps the old certificate and
      logs an error naming the file.
- [x] A file watcher (polling the modification time every 60 seconds) does
      the same without a signal, so lego's renewal needs no hook.
- [x] Existing connections keep their session; new handshakes get the new
      certificate (a test swaps the files and checks the served serial).
- [x] The certificate paths stay restart keys; only the file contents
      reload.
- [x] Raft and agent mTLS certificates are out of scope (they need peer
      coordination).

**Deployment:** the infra repository's post-renewal restart can be removed
after this ships; the operator decides when.

### US-004: Obtain certificates over ACME
**Source:** audit #102. **Size:** M. **Needs:** US-003.

**Acceptance Criteria:**
- [x] `web.tls.acme` block: `enabled` (default false), `email`, `domains`
      (the mail host names), `directory_url` (default Let's Encrypt
      production).
- [x] Challenges use TLS-ALPN-01 on the web listener's port 443; HTTP-01 is
      not offered (no port 80 listener).
- [x] Certificates and the account key are stored in Pebble under
      `/system/acme/…` through Raft, so every node serves the same
      certificate; they are never logged.
- [x] The issued certificate is served by the US-003 holder for web, SMTP,
      IMAP and POP3.
- [x] Renewal starts 30 days before expiry, on the leader only; a failure is
      logged and retried with backoff, and the dashboard warns when the
      certificate expires within 14 days.
- [x] `Config.Validate` refuses ACME together with explicit certificate
      files for the same listener, and refuses `domains` that are not host
      names.

**Security:** the ACME account key and certificate keys in Pebble are
replicated and included in backups, as DKIM keys already are; the
documentation says so.

### US-005: Greylisting
**Source:** audit #47 (corrected). **Size:** M.

**What exists:** none, contrary to the audit. `mapRspamdAction`
(`internal/pipeline/antispam/rspamd.go:192-197`) maps `greylist` to `pass`
with a false comment. rspamd runs after the SMTP `250`, so it could not
greylist anyway.

**Acceptance Criteria:**
- [x] `smtp.greylisting` block: `enabled` (default false), `delay` (default
      5 minutes), `retry_window` (default 4 hours), `pass_lifetime` (default
      36 days).
- [x] At RCPT on the MX port, a new triplet (client network: the /24 for
      IPv4, the /64 for IPv6; envelope sender; recipient) gets
      `451 4.7.1 Greylisted, try again later`; a retry after `delay` and
      within `retry_window` passes, and passing records the client network
      as known for `pass_lifetime`.
- [x] Authenticated sessions, `smtp.trusted_networks`, submission ports and
      the null sender to a postmaster address are never greylisted.
- [x] State is kept outside Raft (one Raft write per unknown triplet would
      let a spam run amplify writes across the cluster) and shared between
      MX nodes over gossip, bounded in size and rate (decision 15), so a
      retry reaching another node passes.
- [x] Entries are bounded (1 million, oldest evicted) and expired entries
      are swept hourly.
- [x] The rspamd comment is corrected; rspamd's `greylist` action stays
      mapped to pass.

### Phase B: Mail policy

Release note: admins can turn protocols and features off per user and set
per-user send limits. Domains can add a disclaimer to outgoing mail, strip
attachment types and journal all mail.

### US-006: Per-user feature switches and send limits
**Source:** audit #105 (corrected). **Size:** M.

**What exists:** account disable only (`UserProfile.disabled`). Send limits
(`smtp.outbound_messages_per_minute`, `outbound_recipients_per_hour`,
`max_recipients`) are counted per user but set globally; the audit's "global
recipient and rate limits" is imprecise. `outbound_messages_per_minute`
covers SMTP submission only, not web compose. Per-protocol switches are a
non-goal of the app-passwords research PRD for app passwords, which does not
cover accounts.

**Acceptance Criteria:**
- [x] `UserProfile` gains `disabled_features` (17, repeated string from
      `imap`, `pop3`, `submission`, `dav`, `web`, `forwarding`, `rules`,
      `vacation`) and `send_limits` (18: messages per minute, recipients per
      hour, max recipients; 0 means the global value).
- [x] A disabled protocol refuses authentication with the same response as a
      wrong password, and the per-command account check
      (`AccountState`) ends existing sessions on the next command.
- [x] Disabled `forwarding`, `rules` or `vacation` hide the settings page
      and stop the pipeline from applying them.
- [x] Per-user limits override the global ones in SMTP submission and web
      compose; web compose also gets the messages-per-minute limit.
- [x] `/admin/users/{id}` edits both, with CSRF; domain admins can edit users
      of their domains only.
- [x] Disabling `web` for a global admin who is the only global admin is
      refused.

**Cluster:** additive fields; an old node does not enforce them during a
mixed window (release note).

### US-007: Server-side disclaimer
**Source:** audit #38. **Size:** M.

**What exists:** nothing; signatures are per-user and inserted at compose
time. DKIM signing happens in the queue worker
(`internal/queue/worker.go:220`), after enqueue, so a disclaimer added at the
enqueue sites (web compose, SMTP submission) is signed.

**Acceptance Criteria:**
- [ ] Per-domain plain-text and HTML disclaimer, edited by global and domain
      admins, with an "external recipients only" option (default on).
- [ ] Applied to mail from the domain's users at the enqueue sites: appended
      to `text/plain` parts and inserted before `</body>` in `text/html`
      parts (both alternatives of `multipart/alternative`); a message with
      no text part gets a `text/plain` part.
- [ ] `multipart/signed`, `application/pkcs7-mime` and
      `multipart/encrypted` messages are left untouched (a disclaimer would
      break the signature), and a metric counts them.
- [ ] Applied once: a message already carrying `X-Bongo-Disclaimer` for the
      domain is not changed again.
- [ ] Attachment-split storage applies after the disclaimer, so the stored
      and sent bytes agree; DKIM verifies on the received copy (an
      integration test).
- [ ] The HTML disclaimer is sanitised with the existing HTML policy on
      save.

### US-008: Attachment policy
**Source:** audit #39. **Size:** M.

**Acceptance Criteria:**
- [x] Per-domain policy for inbound mail: a list of blocked file extensions
      and MIME types (defaults offered: `exe`, `scr`, `js`, `vbs`, `bat`,
      `cmd`, `com`, `jar`, `ps1`, `msi`), and an action: strip, or
      quarantine the message.
- [x] A pipeline stage `attachment_policy`, configured like the existing
      stages, runs before deliver; a stripped part is replaced by a
      `text/plain` part naming the file and the policy (translated in the
      domain's default language, else English).
- [x] Archives are not opened; a blocked extension inside a ZIP is not
      detected (documented).
- [x] The stripped message is stored as a new blob; DKIM and ARC results
      recorded before stripping are kept in the trace headers.
- [x] Detection uses the declared type and the filename, including RFC 2231
      encoded names and double extensions (`invoice.pdf.exe`).

### US-009: Journal copy
**Source:** audit #40. **Size:** M.

**Acceptance Criteria:**
- [ ] A per-domain journal address (a local account or an external
      address), set by global admins only.
- [ ] Every message received by or sent from the domain's users is copied
      to it once: inbound in the deliver stage, outbound at the enqueue
      sites. A message both sent and received within the domain is
      journaled once.
- [ ] The copy is a journal report: a short `text/plain` part listing the
      envelope sender and every envelope recipient, including Bcc, and the
      original as `message/rfc822`. Local journal copies share the original
      blob for the attached message where possible.
- [ ] Mail to or from the journal account itself is not journaled.
- [ ] A journal copy that cannot be written fails the delivery with a
      temporary error (compliance requires completeness); this is stated in
      the admin page.
- [ ] Users cannot see or disable journaling; the admin guide says that
      enabling it may need notice to users under local law.

### Phase C: Settings in the web admin

Release note: anti-spam, anti-virus, DNSBL and greylisting settings can be
changed in the web admin without a restart.

### US-010: Anti-spam and anti-virus settings in the web admin
**Source:** audit #49, #98. **Size:** L.

**What exists:** settings are HCL only; SIGHUP reloads five keys
(`diff.go:52-58`); every rspamd, SpamAssassin, DNSBL, DMARC and ClamAV
setting other than `on_failure` is a restart key. The only global setting in
Pebble is `/system/settings/require_2fa`.

**Design:** web-editable settings live in Pebble under
`/system/settings/{area}` as protobufs and are applied live. HCL remains
authoritative for any key it sets explicitly: such a key is shown read-only
with "set in the configuration file". The config loader therefore records
which keys the file set.

**Acceptance Criteria:**
- [ ] Pages under `/admin/settings/` for: built-in scorer thresholds and
      enable; rspamd and SpamAssassin endpoint, timeout and enable; ClamAV
      socket, enable and failure action; DNSBL zones and weights; greylisting
      (US-005). Global admins only; CSRF; validation errors name the field.
- [ ] A saved change takes effect on the next message without a restart (a
      test changes the threshold and classifies one message either side).
- [ ] Settings stored in Pebble replicate to every node through Raft and
      apply there.
- [ ] A key set in HCL is not editable, and the page says so; a SIGHUP that
      changes an HCL key behaves as today.
- [ ] Credentials (for example an rspamd password) are write-only: the page
      shows whether one is set, never its value, and the value is not
      logged.
- [ ] Each change is logged with the admin's user ID, the key and old and
      new values (credentials excepted).

### US-011: Service overview
**Source:** audit #98. **Size:** S.

**Acceptance Criteria:**
- [ ] `/admin/services` lists each protocol listener (SMTP MX, submission,
      submissions, IMAP, IMAPS, POP3, POP3S, web, CalDAV, CardDAV) with
      enabled state, address, port, TLS mode and whether a restart is needed
      to apply a pending configuration change.
- [ ] The page is read-only; enabling or disabling a listener stays in HCL
      (listeners need a restart).
- [ ] For the agent role, connected remote agents are listed with role and
      version.

### Phase D: Backup and export

Release note: users can export all their data, and admins can back up and
restore one user.

### US-012: Export a whole account
**Source:** audit #109 (corrected). **Size:** M.

**What exists:** more than the audit says: one message as EML
(`/mail/{mailbox}/{uid}/export`), all contacts as vCard (`/contacts/export`)
and all calendars as ICS (`/calendar/export`). There is no mailbox or
whole-account export.

**Acceptance Criteria:**
- [ ] `/settings/export` builds a ZIP with one mbox file per mailbox, the
      vCard export, one ICS file per calendar and a JSON manifest; it is
      built in the background and offered for download for 24 hours.
- [ ] The ZIP is streamed to a temporary file, never held in memory; at
      most one export per user at a time and one per day.
- [ ] Admins can start the same export for a user of their domains; the
      download link is offered to the admin who started it only.
- [ ] Export files are deleted after 24 hours and on account deletion.
- [ ] Re-importing the mbox files with `bongo-import mbox` restores every
      message (a round-trip test).

### US-013: Per-user backup and restore, with search and rollback
**Source:** audit #110, backlog B13. **Size:** L. **Needs:** US-012.

**What exists:** a whole-data tar after stopping the container
(`deploy/README.md` section 10) and `scripts/verify-restore.sh`. B13 (a
scalable whole-server backup) is TODO and is not this story.

**Design:** a per-user backup is the user's Pebble keys (`/user/{id}/…`),
their address records, grants and group memberships, plus the blobs they
reference, written as content-addressed files. A blob already present in an
earlier backup of the same backup directory is not copied again, which makes
backups incremental. Blobs are written before the key file, and restore
writes blobs before keys, as the GC's reference rules require.

**Acceptance Criteria:**
- [ ] `bongo-go backup user {address} --to {dir}` and
      `bongo-go restore user {address} --from {dir} [--at {timestamp}]`
      through the control listener; the server reads Pebble from a
      checkpoint so the account stays online.
- [ ] A second backup of an unchanged account copies no blobs.
- [ ] `bongo-go backup search --from {dir} --user {address}` lists messages
      in a backup by subject, sender and date.
- [ ] Restore modes: the whole account, one mailbox, or selected messages,
      into the same account (rollback: the restored mailbox replaces the
      current one, after the current one is saved as a backup) or into a
      new mailbox `Restored {date}` (default).
- [ ] Restore writes through Raft in batches with documented partial-failure
      semantics: blobs first, then messages mailbox by mailbox; a failure
      leaves the restored mailbox incomplete and the command reports how far
      it got; rerunning it completes it without duplicates.
- [ ] Backup files are written with mode 0600 and contain no credentials
      beyond the password hash and TOTP secret already in the profile; the
      documentation says the files must be protected like the data
      directory.

### Phase E: Directory

Release note: users can authenticate against an LDAP directory, and the
directory is available as an address book.

### US-014: LDAP authentication and directory
**Source:** audit #122. **Size:** L.

**What exists:** nothing. `auth.LocalAuth` (`internal/auth/local.go:38`) is
the only implementation and the agent holds the concrete type
(`agent.go:110, 452`); each protocol declares its own small interface, so an
authenticator chain fits without protocol changes.

**Acceptance Criteria:**
- [ ] `auth.ldap` block: `url` (`ldaps://` or `ldap://` with StartTLS
      required), `bind_dn`, `bind_password`, `user_base`, `user_filter`,
      attribute mapping for mail and display name, `admin_group` (optional),
      `auto_provision` (default false), timeouts.
- [ ] An account carries `auth_source` (profile field 19: empty for local,
      `ldap`); LDAP accounts authenticate by binding as the user's DN, local
      accounts as today. A local admin account keeps working when the
      directory is down.
- [ ] With `auto_provision`, the first successful LDAP login creates the
      account on a hosted domain; without it, an admin creates it.
- [ ] A user disabled or removed in LDAP fails the next authentication and
      the next per-command account check within 5 minutes (a cached
      directory state with that lifetime).
- [ ] Filter values are escaped (RFC 4515); a test passes `*)(uid=*` as a
      user name and gets a refusal.
- [ ] Timing for an unknown user, a wrong password and a disabled account is
      equalised as in `LocalAuth`.
- [ ] Password change and reset in the web UI are not offered for LDAP
      accounts; TOTP 2FA still works.
- [ ] The bind password is never logged; a connection error logs the host
      only.

### US-015: The directory as an address book
**Source:** audit #81. **Size:** M. **Needs:** US-014 for the LDAP source.

**Design:** "read-only LDAP address book" is delivered as a read-only
directory address book, not as an LDAP server. When LDAP is configured the
GAL is filled from it; the GAL, LDAP-backed or not, is offered to clients as
a read-only CardDAV address book. Running an LDAPv3 listener for clients is
open question 4.

**Acceptance Criteria:**
- [ ] With LDAP configured, compose autocomplete (`/api/gal`) includes
      directory entries, scoped to the caller's domain, cached for 10
      minutes, at most 10 results.
- [ ] A read-only CardDAV address book "Directory" lists the domain's users
      (and directory entries); PUT and DELETE answer 403.
- [ ] Resource and shared accounts (v0.15, v0.16) appear with their kind.
- [ ] Users of one domain never see another domain's entries (a test with
      two domains).

### Phase F: Certificates for users and S/MIME

### US-016: Verify S/MIME signatures in the web view
**Source:** audit #117. **Size:** M.

**What exists:** nothing. A `smime.p7s` part shows as an ordinary attachment
(inferred from enmime's handling; `servableParts`,
`internal/web/handlers/mail.go:627`).

**Acceptance Criteria:**
- [ ] A `multipart/signed; protocol="application/pkcs7-signature"` message
      shows a badge: "Signed by {subject}" when the signature verifies
      against the system trust store and the signer's address matches From;
      "Signature not valid" otherwise, with the reason.
- [ ] An opaque-signed or encrypted `application/pkcs7-mime` message shows
      "Encrypted message; open it in a mail client that holds your key".
- [ ] Verification is bounded (input size, chain length, 1-second timeout)
      and never fetches anything from the network (no AIA, OCSP or CRL
      fetches; revocation is not checked, and the badge tooltip says so).
- [ ] The library choice is recorded with its licence in
      `THIRD_PARTY_NOTICES.md`.

### US-017: Client-certificate login for the web UI
**Source:** audit #117. **Size:** M.

**Acceptance Criteria:**
- [ ] `web.client_ca` names a CA bundle. When set, the web listener requests
      (does not require) a client certificate.
- [ ] A user can register a certificate's fingerprint in
      `/settings/security` (stored in the profile); a request presenting a
      registered, valid, unexpired certificate issued by that CA logs in
      without a password, still subject to 2FA if enabled.
- [ ] An unregistered or invalid certificate falls back to the password
      form; nothing distinguishes the cases in the response.
- [ ] Behind a TLS-terminating proxy (`behind_tls_proxy = true`) the feature
      is refused by `Config.Validate`; client-certificate headers from a
      proxy are not trusted.

### Phase G: Translation

### US-018: Brazilian Portuguese
**Source:** audit #125. **Size:** M.

**What exists:** en, de, es and fr with 706 keys each. Adding a locale today
means editing four hard-coded lists (`internal/i18n/i18n.go:25-30`,
`internal/web/middleware/locale.go:15-20`,
`internal/web/handlers/preferences.go:140-143`,
`internal/web/templates/settings.templ:140-145`).

**Acceptance Criteria:**
- [ ] The four lists are replaced by `i18n.AvailableLocales()`, so a new
      locale is one JSON file plus its display name.
- [ ] `pt_BR.json` has every key; `TestI18nCoverage` checks it like the
      others.
- [ ] The language selector offers "Português (Brasil)".
- [ ] Each string has been reviewed by a native speaker before release
      (open question 5); the PR records who.

### Phase H: Mail and sharing for users

Release note: messages from mailing lists and newsletters have an
Unsubscribe button. Where the sender supports one-click unsubscribe, Bongo
sends the request itself.

### US-019: One-click unsubscribe
**Source:** operator request, 2026-11-26. **Size:** M.

**What exists** (checked on `main` at `a0d2636`): `List-Id` is stored on
`MailMeta` (field 22) and drives the Mailing lists view (`lists.templ`).
`List-Unsubscribe` is read only to suppress vacation replies
(`internal/pipeline/deliver/vacation.go:195`); it is not stored and nothing
in the web UI offers it. `internal/netx/safeclient.go` is the HTTP client
that refuses non-public addresses on every connection (used by calendar
subscriptions).

**Acceptance Criteria:**
- [x] The message view parses `List-Unsubscribe` (RFC 2369) and
      `List-Unsubscribe-Post` (RFC 8058) from the stored message and shows
      an Unsubscribe button when the message offers a usable method. No new
      key or `MailMeta` field: the headers are read from the blob when the
      message is shown.
- [x] One-click (RFC 8058): offered when there is an `https` URI,
      `List-Unsubscribe-Post: List-Unsubscribe=One-Click`, and a valid DKIM
      signature on the stored message that covers both headers (RFC 8058
      section 4), verified when the user clicks. The server sends the POST
      (`List-Unsubscribe=One-Click`, form encoded) through the safe client:
      https only, no private or internal address on any connection or
      redirect, no cookies, a fixed User-Agent that names no user or
      domain, a short timeout and a small response cap. The response body
      is never rendered; the user sees success or a fixed failure message.
- [x] Without a valid signature or the `List-Unsubscribe-Post` header, an
      `https` URI is shown as a link the user opens in their own browser
      (`rel="noopener noreferrer"`), after a confirmation that names the
      host. The server never fetches it, because a GET can confirm the
      address to a tracker or act on the user's behalf.
- [x] A `mailto:` URI opens compose with the address, subject and body
      from the URI; the user sends it, so the normal send path, limits and
      Sent copy apply. `http:` and every other scheme are ignored.
- [x] No button on messages in Junk or below it, or on messages the
      antispam stage tagged as spam: unsubscribing from spam confirms the
      address. The view says to report the message as spam instead.
- [x] The Mailing lists view offers the same action per list, using the
      newest message of that list.
- [x] The POST route is CSRF-protected, needs the session user to own the
      message, and is rate limited per user (one budget, 429 with
      Retry-After). Logs carry the user id, message guid and URI host only:
      the full URI usually holds a token that identifies the subscriber.
- [x] Every new string is translated in all shipped locales.

### US-020: Shared folders are subscribed by default
**Source:** operator's v0.16 client check, 2026-11-26. **Size:** M.

**What exists** (checked on `main` at `ed1909f`): a shared folder is
unsubscribed until the user subscribes; presence of
`/user/{id}/sharedsub/{owner}/{mailbox_id}` means subscribed
(`internal/store/mailboxshare.go:142-149`). Thunderbird shows only
subscribed folders by default, so a share the user accepted, a folder of a
shared inbox they belong to, or a folder shared with their group does not
appear there until they use its Subscribe dialog. Apple Mail ignores
subscriptions and shows them. Group and shared-inbox grants are created
accepted and never pass through `/shares`, so subscribing on accept alone
would not cover them.

**Acceptance Criteria:**
- [x] A shared folder on which the user holds an accepted grant with `l`
      (direct, through a group, or as a shared-inbox member) is subscribed
      unless the user has unsubscribed from it: LSUB and `LIST (SUBSCRIBED)`
      return it, so Thunderbird shows it with default settings.
- [x] IMAP UNSUBSCRIBE records an opt-out marker keyed like today's key
      (owner and folder id, so a rename keeps it); SUBSCRIBE removes the
      marker. A plain Put or Delete, no new command. The marker authorises
      nothing; rights are re-checked on every listing as now.
- [x] The old `sharedsub` keys are no longer read. A leader pass deletes
      them once (they only ever meant "subscribed", which is now the
      default). The key table in CLAUDE.md is updated.
- [x] A test with each grant path (direct accepted, group, shared inbox)
      shows the folder in LSUB without any SUBSCRIBE; a test shows an
      UNSUBSCRIBE surviving a folder rename and a grant change from Read to
      Write.
- [x] `docs/shared-folders.md` drops the "subscribe first" step and explains
      unsubscribing.
- [x] Mixed versions: an older node still reads `sharedsub` and lists an
      unmarked shared folder as unsubscribed, so a client balanced across
      nodes sees it appear and vanish. Upgrade every node together
      (single-node production is unaffected).

### US-021: Share forms suggest users and groups as you type
**Source:** operator's v0.16 client check, 2026-11-26. **Size:** S.

**What exists:** the folder (`settings_folders.templ`), calendar
(`calendar.templ`) and address book (`contacts_books_share.templ`) share
forms take a typed address only. Compose autocomplete (`GET /api/gal`,
`ComposeHandler.GALSearch`) already searches the caller's own domain only,
and adds the caller's contacts and same-domain distribution groups.

**Acceptance Criteria:**
- [x] Each share form suggests matches as the user types, from people and
      distribution groups on the user's own primary domain, at most 10.
      The same domain scoping as compose, so no form can enumerate another
      domain's users.
- [x] Only shareable principals are offered: enabled people (no resources,
      no shared inboxes, not the user) and groups `shareableGroupOf` would
      accept. Personal contacts and contact groups are not offered, since a
      share needs a local account.
- [x] Typing a full address still works for a user on another hosted
      domain; the refusal rules and the uniform refusal message are
      unchanged.
- [x] Either a `share=1` mode on `/api/gal` or a small endpoint sharing its
      search code; it is GET only, behind the session, rate limited like
      compose autocomplete.
- [x] The suggestion list is keyboard and screen-reader usable (combobox
      pattern), as the compose recipient field is.
- [x] Every new string is translated in all shipped locales.

### US-022: Create, rename and delete folders in the web UI
**Source:** operator's v0.16 client check, 2026-11-26. **Size:** M.

**What exists** (checked on `main` at `ed1909f`): the web has no route that
creates, renames or deletes a folder; it creates one only as a side effect
(the import target in `MailHandler`). Folders come from mail clients. The
store already does all three for IMAP: `MailboxStore.CreateMailbox`,
`RenameMailbox` (keeps the folder id, so shares follow; does not rename
inferior folders) and `DeleteMailbox` (revokes the folder's grants
best-effort, `ErrMailboxGrantsNotRevoked`). The IMAP session refuses names
under `imap.other_users_prefix` and `imap.shared_prefix` and refuses to
delete INBOX (`internal/imap/backend.go:466-560`).

**Acceptance Criteria:**
- [x] Settings → Folders gains New folder, Rename and Delete for the user's
      own folders; the mail sidebar's folder menu links to the same actions.
- [x] The same name rules as IMAP CREATE and RENAME: no name under either
      sharing prefix, none of the virtual view names, no empty or
      over-long name, `/` as the hierarchy delimiter; one shared validation
      function used by both paths so they cannot drift.
- [x] The default folders (`store.DefaultMailboxes`: INBOX, Sent, Drafts,
      Trash, Junk) and Archive cannot be renamed or deleted from the web.
- [x] Delete asks for confirmation naming the folder and its message count,
      and, when the folder is shared, warns that the people it is shared
      with lose access. Messages are removed with the folder (as IMAP
      DELETE); a grant-revoke failure is logged and the page still reports
      success, as IMAP does.
- [x] Rename states that subfolders keep their own names (the store does not
      rename inferiors), or renames them in the same request if the design
      review prefers; either way the behaviour is documented and tested.
- [x] All three are POST routes behind CSRF and same-origin checks, scoped
      to the session user's own folders; a name that does not exist is a
      404. The folder list and the sidebar refresh after each action.
- [x] Every new string is translated in all shipped locales.

### US-023: Search and collapsible subfolders in Settings → Folders
**Source:** operator request, 2026-11-26. **Size:** S. **Needs:** US-022.

**What exists** (checked on `main` at `dbf122b`): Settings → Folders
(`settings_folders.templ`) renders one card per own folder, flat, in list
order, with no filter. A user with many folders gets a very long page. The
mail sidebar already collapses subfolders (`data-folder-toggle` in
`components.templ`, handled in `bongo-pages.js`); the settings page has
nothing like it.

**Acceptance Criteria:**
- [x] A search box at the top filters the folder list as the user types:
      a case-insensitive substring match on the full folder path, done in
      the browser over the rendered list (no new route, no server search).
      A folder matches when its path matches; its parent folders stay
      visible so the match keeps its place in the tree.
- [x] Folders are shown as a tree on `/`: a folder with subfolders has a
      toggle that shows or hides them, using the sidebar's pattern
      (button with `aria-expanded` and `aria-controls`). Top-level folders
      start expanded and deeper levels collapsed; while a search is active,
      every branch with a match is expanded.
- [x] A subfolder whose parent does not exist (IMAP allows `A/x` without
      `A`) is still shown, under a parent row that has no actions.
- [x] "No folders match" shows when the search finds nothing; clearing the
      search restores the previous expanded state.
- [x] Works without JavaScript errors after an htmx swap of the list (a
      create, rename or delete from US-022 re-renders it), with Alpine or
      the existing delegated handler, not a new framework.
- [x] Keyboard and screen-reader usable: the search box has a label, the
      result count is announced in a polite live region.
- [x] Every new string is translated in all shipped locales.

## 4. Functional Requirements

FR-1 to FR-23 are the acceptance criteria of US-001 to US-023. In every
story:

- Every cookie-authenticated mutation is behind CSRF and same-origin checks;
  admin pages keep domain-admin scoping.
- A persistence error on a user-visible write fails the request, unless the
  code says it is best-effort and logs the failure.
- No secret (bind passwords, ACME keys, scanner credentials, certificate
  keys) appears in logs or in a page.
- New text is translated in all shipped locales, with a second grep pass.
- New config keys have defaults, validation and a class in `config/diff.go`.

## 5. Non-Goals (Out of Scope)

- IMAP PROXYAUTH (#16): dropped by the operator; admin impersonation is a
  security liability.
- POP3 SASL (#19): dropped by the operator.
- S/MIME signing, encryption or decryption in the webmail, and storing
  users' private keys on the server.
- Enabling, disabling or moving protocol listeners from the web (they need a
  restart).
- A scalable whole-server backup (backlog B13), which this PRD's US-013
  complements but does not replace.
- Raft and agent mTLS certificate reload.
- Obsolete audit rows that relate to this release, not to be built:
  - Agent process management from the web (#99): one binary under systemd
    or Docker.
  - Setup wizard and XML setup import (#103): zero-config dev mode and HCL.
  - SNMP (#115): Prometheus `/metrics` instead.
  - ODBC authentication (#123).
  - CRAM-MD5 (#124): needs reversible passwords.
  - The director agent (#113): a stub in the original.

## 6. Technical Considerations

### 6.1 Security

The server is internet-facing.
- US-001 is the only call home. It sends two values, validates everything
  it receives, and renders only validated values, escaped.
- US-004 binds nothing new (TLS-ALPN-01 on 443) and stores keys in Pebble.
- US-005 keeps greylisting state off Raft so unauthenticated traffic cannot
  drive cluster writes.
- US-010 makes security settings editable from the web; only global admins
  can change them, every change is logged, and credentials are write-only.
- US-014 adds an outbound directory dependency to authentication; filter
  escaping, timing equalisation and a local-admin fallback are required.
- US-019 makes the server send an HTTP request to a URL chosen by the
  sender of a message. It goes only through the safe client (public
  addresses only, https only, bounded), only after a DKIM check and a click
  by the user, and never for mail classed as spam.
- US-013's backup CLI uses the control listener (loopback only, signed
  requests), not a public route. No new `/internal/*` route goes on the
  public router; any new `http.Server` has explicit timeouts.

### 6.2 Data and Raft

New keys: `/system/instance_id`, `/system/update_check`,
`/system/acme/…`, `/system/settings/{area}`, per-domain policy keys under
`/system/domain/{d}/…` (disclaimer, attachment policy, journal address).
Additive profile fields 17 to 19 (13 to 16 are capacity, resource_type, require_app_passwords and shared_members). Each write is a single PUT; restore
(US-013) is a documented multi-step operation. No new composite command
type.

### 6.3 Mixed-version clusters

Nodes do not publish their version (`prd-hardening-v0.9.md` 8.2,
"Version-aware rolling upgrades"). This release adds no command type, so it
is rollable by hand review, with these gaps during the window: old nodes do
not enforce per-user switches and limits, disclaimers, attachment policy or
journaling, and do not apply web-edited settings. Journaling (US-009) should
not be switched on until every node runs v0.17, or mail handled by an old
node is not journaled. US-001 adds the version to nothing in gossip; it is
not the version-gating work.

### 6.4 New dependencies

An LDAP client library (US-014), an ACME client (US-004) and a PKCS #7
verifier (US-016). Each is recorded in `THIRD_PARTY_NOTICES.md` and covered
by govulncheck.

## 7. Success Metrics

- Every story done, each with a test that fails on `6725448`.
- A production node renews its certificate with no restart.
- The update endpoint's stored data, inspected after a week, contains only
  per-day counts per version.
- gosec medium findings do not increase; `make test-all` stays green.

## 8. Open Questions

1. US-001 and US-002: which domain serves the update endpoint? The operator
   has not chosen it. Until then `server.update_check_url` has no usable
   default and the check cannot ship enabled.
2. US-002: should the Worker's source live in this repository or in the
   website repository?
3. US-002: how long are the per-day aggregate counts kept?
4. US-015: is an LDAPv3 listener for mail clients wanted? It would be a new
   internet-facing protocol; the proposal is CardDAV only.
5. US-018: who reviews the Brazilian Portuguese translation?
6. US-009: should journaling be available to domain admins, or global
   admins only (proposed)?
7. US-005: should greylisting state be shared across MX nodes in a later
   release?

### 8.1 Decisions

1. (operator, 2026-10-26) Scope as listed in section 3.
2. (operator, 2026-10-26) The version check: the leader checks once a day
   with jitter; it sends only its version and a random instance ID created at
   first start (no hostname, domain or user counts) to a JSON endpoint on
   the project's own domain, served by a Cloudflare Worker that stores only
   aggregate per-day counts by version and instance ID, never IP addresses.
   The admin dashboard shows a banner for a newer or security release.
   `update_check = false` turns it off; dev mode never checks. It is on by
   default and documented in the admin guide and privacy notes, as Gitea
   does. The response carries the latest version, a release notes URL and a
   security flag.
3. (operator, 2026-10-26) IMAP PROXYAUTH (#16) and POP3 SASL (#19) are
   dropped.
4. (operator, 2026-10-27) Open question 1: the update endpoint is served
   from `update.bongo-project.org` by a Cloudflare Worker; that is the
   default `server.update_check_url`.
5. (operator, 2026-10-27) Open question 2: the Worker source lives in this
   repository under `deploy/update-worker/`.
6. (operator, 2026-10-27) Open question 3: per-day aggregate counts are
   kept for 13 months and then deleted; the privacy notes say so.
7. (operator, 2026-10-27) Open question 5: pt-BR ships marked beta, with a
   public issue asking a native speaker to review it; it loses the beta
   label after review.
8. (operator, 2026-10-27) Open question 4: an LDAPv3 listener for mail
   clients is wanted, in addition to the CardDAV address book. Proposed
   constraints, to be confirmed in the design: read-only; off by default;
   no anonymous bind (users authenticate as themselves); LDAPS or StartTLS
   only; results scoped to the bound user's domain with a size limit;
   per-connection rate limits. It is a new internet-facing protocol, so it
   becomes its own story in Phase E with a security review.
9. (operator, 2026-10-27) Open question 6: domain admins may configure
   journaling for their own domain. Proposed constraint: a domain admin may
   set only a local account in that domain as the journal address; an
   external journal address still needs a global admin, so a delegated
   admin cannot copy all of a domain's mail off the server. Every change is
   audit-logged.
10. (operator, 2026-10-27) Open question 7: greylisting state is shared
   across MX nodes. It must not go through Raft, because unauthenticated
   senders would then drive cluster writes (section 6); the proposal is a
   bounded table exchanged over gossip (memberlist), capped in size and
   rate, losing at worst a retry window when a node restarts.
11. (operator, 2026-11-26) One-click unsubscribe is added to v0.17.0 as
   US-019 (Phase H). A full mailing-list server (self-service subscribe and
   unsubscribe, external subscribers, archives, moderation, DMARC-safe
   sending) is not in this PRD; it has its own research PRD,
   `prds/research/prd-mailing-lists.md`, with no release yet.
12. (operator, 2026-11-26) After the v0.16 client check: shared folders
   become subscribed by default (US-020; Thunderbird did not show a shared
   folder or a shared inbox that Apple Mail did), and the share forms
   suggest users and groups from the user's own domain as they type
   (US-021). Both in v0.17.0, Phase H.
13. (operator, 2026-11-26) The web UI gains folder create, rename and delete
   (US-022, v0.17.0, Phase H); until now only mail clients could.
14. (operator, 2026-11-26) Settings → Folders gains a search box and
   collapsible subfolders (US-023, v0.17.0, Phase H), so the page stays
   usable for people with many folders.
15. (operator, 2026-11-27) US-005 ships with greylisting state shared across
   MX nodes over gossip (decision 10) in v0.17.0, not per node: a bounded,
   rate-capped table exchanged over memberlist, never through Raft. The
   story's per-node criterion and its documentation note are superseded; a
   restart loses at worst one retry window. Size L, with a security review.
16. (operator, 2026-11-27) The update endpoint does not use Cloudflare
   (the estate is moving off it, decided 2026-10-11): the Worker is
   replaced by `cmd/bongo-update`, a Go service in this repository with
   PostgreSQL for the counts, deployed by the operator on their own hosting
   (US-002, row 44.6). The client contract is unchanged. If the endpoint is
   not live at the v0.17.0 release, servers record a failed check once a
   day (10 s timeout, a debug log line, no banner) and nothing else is
   affected.

### 8.2 Candidates for the next release

Found while building Phase 45 (2026-11-28):

- The pipeline has no temporary-failure path: any stage error moves the
  envelope to `error/`, where it waits for the error-spool purge and is never
  retried. Pre-existing for every stage; the attachment-policy stage now fails
  closed on a store error, so a brief Pebble error holds mail instead of
  delivering it unchecked. Candidate: a bounded retry with backoff in the
  runner before `error/`.
- Per-user send limits cover submission and web compose only; account and
  rule forwards and vacation replies keep their own caps
  (`pipeline.forward_max_per_hour`, the vacation hourly cap).
- The gRPC `DepositMessage` path accepts caller-supplied checked flags and
  stage results other than the attachment policy (for example a forged
  antispam verdict). Pre-existing; the listener is not started in production.
- Uuencoded bodies and `message/global` are not inspected by the attachment
  policy (documented).

Audit corrections found while writing this PRD (2026-10-26):

- #47: there is no greylisting, not even through rspamd; the rspamd mapping
  comment is false.
- #102: production does not use Traefik; Bongo terminates TLS and is
  restarted after each renewal.
- #49, #98: SIGHUP reloads five keys; almost every anti-spam and anti-virus
  setting needs a restart.
- #105: limits are per user but set globally, and the messages-per-minute
  limit does not cover web compose.
- #109: contacts and calendars can already be exported in full.
- `docs/config-reload.md` still says "DESIGN ONLY"; `/admin/version` is
  reachable by domain admins.
- The infra repository's `bongo.prod.hcl` still has a "behind Traefik"
  comment (for the infra session, not this repository).

Found in CI on 2026-11-26 (PR #250, a docs-only change):

- `TestFetchPageWiredWithTester` (`internal/agent/fetchworker_batch_test.go`)
  hung for 15 minutes until the 20-minute package timeout; the same code
  passed on main. The test's HTTP client has a 20 s timeout, so the hang is in
  starting the wiring agent (`runWiringAgent`), not in the request. Give the
  start-up a deadline that fails the test with a goroutine dump, then find
  the cause.

Found in CI on 2026-11-26 (PR #252, a docs-only change):

- `TestPebbleMetricsAreOptIn` (`internal/store/pebble_metrics_test.go:156`)
  failed once: the WAL fsync count moved from 9 to 10 between scrapes. The
  count belongs to the metrics-enabled store the test opened first, which can
  still fsync in the background after its last `Set`, so the test blames the
  plain store for it. Wait for the first store to go quiet (or close it)
  before taking the baseline.

Found while building US-021 (2026-11-26):

- `GET /api/gal` (compose autocomplete) has no rate limiter (a plain
  `r.Get` in `agent.go`), so US-021's "rate limited like compose
  autocomplete" adds none; share mode returns a subset of what compose
  already returns to the same caller. A per-user limiter on `/api/gal`,
  for compose and share mode alike, is a candidate.

Release size: 23 stories (4 S, 16 M, 3 L). That is above the ~15-story guide
with three L stories. Proposed split:

| Release | Stories | Why |
|---------|---------|-----|
| v0.17.0 | Phases A, B, C, G, H (US-001 to US-011, US-018 to US-023) | Operations, mail policy, web settings, the translation, one-click unsubscribe, two sharing fixes and web folder management with search: independent of each other's data model. |
| v0.18.0 | Phases D, E, F (US-012 to US-017) | Backup, LDAP and certificates for users: each is a new trust or data path and benefits from its own review. |

US-001 cannot ship enabled until open question 1 is answered; if the domain
is not chosen in time, US-001 and US-002 move with Phase D.
