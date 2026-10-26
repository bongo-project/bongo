# PRD: Sharing and contacts v0.16.0

## 1. Introduction / Overview

The feature-parity audit of the original Bongo
(`prds/research/parity-audit-original-bongo.md`, rows cited as #N) lists the
collaboration features a small office expects: shared folders and inboxes,
distribution groups, IMAP NAMESPACE, ACL and QUOTA, contact groups, shared
address books, CSV and LDIF exchange, and scheduled fetch from other mail
accounts. This PRD fixes the scope of v0.16.0 "Sharing and contacts".

Every story was checked against `main` at `6725448` on 2026-10-26. Where the
code disagrees with the audit, the story says so; section 8.2 lists the
corrections together. Three findings shape the design:

- **The IMAP library cannot serve ACL or QUOTA.** go-imap v2.0.0-beta.8's
  server dispatches a fixed command list (`imapserver/conn.go:217-291`); it
  supports NAMESPACE (`SessionNamespace`) but has no session interface and no
  handler for GETQUOTA, GETQUOTAROOT, SETACL, GETACL, DELETEACL, LISTRIGHTS
  or MYRIGHTS. Anything else gets `BAD Unknown command`. ACL and QUOTA need a
  library change (US-003).
- **Only domain quota is enforced.** `UserProfile.quota_bytes` is editable
  in admin but not enforced (`internal/web/handlers/quota.go:31`), and no
  per-user usage is computed, so there is nothing per-user to report over
  IMAP QUOTA yet.
- **There is no generic ACL key space, and aliases are one-to-one.** The
  grant key space arrives with calendar sharing in v0.15 US-003
  (`prd-calendar-v0.15.md` section 6.2); this release extends it. An alias
  maps to exactly one user (`AddressRecord.user_id`), so groups need their
  own records.

**The scope rule.** The in-scope list is section 3. Anything a review finds
during this release goes into section 8.2, not into TASKS.md, unless the
change under review introduced it.

| Phase | Theme | Stories |
|-------|-------|---------|
| A | Foundations: grants, namespaces, library, quota | US-001 to US-004 |
| B | Distribution groups | US-005 to US-006 |
| C | Shared folders and shared inboxes | US-007 to US-010 |
| D | Contacts | US-011 to US-015 |
| E | Scheduled fetch | US-016 |

TASKS rows are assigned by `norman import`. Dependencies: everything in C
needs US-001 and US-002; US-004 and US-009 need US-003; US-010 needs US-007;
group grantees need US-005; US-012 needs US-001.

### Size key

S: one focused session. M: two or three packages, a proto change or a new
form. L: a new subsystem or a cross-cutting change.

## 2. Goals

- A user can share a mail folder with another user or a group at a chosen
  level, and the recipient sees it in the web UI and in IMAP clients.
- A team can work from a shared inbox (`support@`) whose mail all members
  see and from which they can send.
- An admin can create a distribution group address that fans out to local
  members, optionally restricted to members as senders.
- IMAP clients get NAMESPACE, ACL and QUOTA answers.
- Contacts can be grouped, kept in several address books, shared, and moved
  in and out as CSV and LDIF.
- A user can pull mail from another POP3 or IMAP account on a schedule.

## 3. User Stories

### Phase A: Foundations

Release note: IMAP clients see personal, other users' and shared
namespaces, and per-user storage quota is enforced and reported over IMAP.

### US-001: Extend the grant key space to mailboxes, address books and groups
**Source:** CLAUDE.md (no generic ACL key space); v0.15 US-003. **Size:** M.

**What exists:** after v0.15, `/acl/calendar/…` and `/aclrev/…` with the
composite `/__acl__/` command. If v0.15 US-003 has not shipped when this
story starts, this story builds the key space as specified in section 6.2
first.

**Acceptance Criteria:**
- [ ] Kinds `mailbox` and `addressbook` are accepted. Mailbox grants carry
      `rights` (RFC 4314 letters `lrswipkxtea`); address-book grants carry
      `level` (`read` or `write`). Unknown letters or levels are refused.
- [ ] Grantee `g:{group_id}` is accepted for every kind once US-005 has
      shipped; a user's effective rights are the union of their own grant
      and those of every group they belong to.
- [ ] `MailboxMeta` gains `id` (field 5). The `set` operation assigns an id
      to a mailbox that has none, in the same Pebble batch as the grant, so
      a grant always names a mailbox by a stable id and a rename needs no
      grant change.
- [ ] Deleting a mailbox, an address book or a group revokes its grants in
      the same request; the leader sweep removes grants whose resource,
      owner or grantee is gone.
- [ ] At most 100 grants per resource and 500 per owner; the 101st is
      refused with a translated message.

**Cluster:** no new command type (the `/__acl__/` command exists from
v0.15), but new `kind` and grantee values. A v0.15 node applies them as
grants it does not understand; they are inert there. Upgrade every node
before sharing folders.

### US-002: IMAP NAMESPACE
**Source:** audit #15. **Size:** S.

**What exists:** the audit is correct that NAMESPACE is missing; it omits
that go-imap already supports it (`SessionNamespace`), so this is a method
plus a capability (`internal/imap/server.go:67-72`).

**Acceptance Criteria:**
- [ ] NAMESPACE returns personal `""` with delimiter `/`, other users
      `"Other Users/"` and shared `"Shared/"`, and `NAMESPACE` is in the
      capability list.
- [ ] CREATE and RENAME of a personal folder whose name starts with
      `Other Users/` or `Shared/` (or equals either) are refused with `NO`.
- [ ] Existing personal folders with those names are renamed once, by a
      leader migration, to `{name} (personal)` through the existing rename
      command, and each rename is logged (open question 3).

### US-003: An extension hook in go-imap for ACL and QUOTA
**Source:** this PRD's code check. **Size:** M.

**Design:** Bongo maintains a fork of go-imap v2 under the project's GitHub
organisation, wired with a `replace` directive, that adds optional
`SessionACL` and `SessionQuota` interfaces and their command handlers. The
change is offered upstream; the fork is dropped when upstream accepts an
equivalent. The alternative, a protocol proxy in front of go-imap, would
re-parse every command and is rejected (open question 1).

**Acceptance Criteria:**
- [ ] The fork adds parsing and responses for GETQUOTA, GETQUOTAROOT
      (RFC 9208) and SETACL, DELETEACL, GETACL, LISTRIGHTS, MYRIGHTS
      (RFC 4314), dispatched only when the session implements the
      interface; a session that does not still answers `BAD`.
- [ ] The capabilities `QUOTA`, `QUOTA=RES-STORAGE` and `ACL` are
      advertised only when the session implements the interface.
- [ ] The fork's own tests cover each command's syntax, including literal
      and quoted mailbox names.
- [ ] `THIRD_PARTY_NOTICES.md` and the depfloor test record the fork;
      govulncheck runs against it.
- [ ] An upstream pull request is opened and linked in the PR description.

### US-004: Per-user quota, enforced and reported over IMAP
**Source:** audit #14 (corrected). **Size:** M. **Needs:** US-003.

**What exists:** the audit says IMAP enforces quota on APPEND; it is the
domain quota (`DomainQuota.max_storage_bytes`, `internal/imap/backend.go:
629-645`). No per-user usage exists: the leader's usage recompute
(`agent.go:2897-3045`) sums per user and keeps only the domain total, and the
admin user page's "used" figure is never set (always 0).

**Acceptance Criteria:**
- [ ] The usage recompute also writes `/user/{id}/usage` (little-endian
      int64 bytes, same accounting as the domain total) for every user.
- [ ] A non-zero `UserProfile.quota_bytes` is enforced wherever the domain
      quota is today (SMTP RCPT, IMAP APPEND, web, DAV), with the same
      best-effort semantics; the domain quota still applies as well.
- [ ] GETQUOTAROOT INBOX returns root `"user"` with `STORAGE` used and limit
      in KiB when the user has a quota, and no root when they do not.
      GETQUOTA `"user"` returns the same. SETQUOTA answers `NO`.
- [ ] The admin user page shows real usage.
- [ ] Domain usage is never reported to an ordinary user over IMAP.

### Phase B: Distribution groups

Release note: admins can create group addresses that deliver to every
member, and restrict who may post to them.

### US-005: Distribution groups
**Source:** audit #120 (backlog B10), #41. **Size:** L.

**What exists:** nothing; the audit is correct. `ResolveRecipient` returns
one user (`internal/store/recipient_resolver.go:80`), and SMTP already
writes one envelope per local recipient sharing one blob (v0.12 US-009), so
expansion at RCPT fits the existing flow. B10 in TASKS.md asks for a
research PRD covering distribution groups against full mailing lists; this
story is the distribution-group half, and full list features are non-goals.

**Design:** `/system/group/{group_id}` holds a `Group` protobuf (address,
display name, domain, member user IDs, owner user IDs, posting policy). The
address is claimed in the global address index as an `AddressRecord` of kind
`group` with a new `group_id` field (6). Creating a group writes both keys
through a new composite command, `/__group__/`, which the FSM applies in one
Pebble batch only if the address key is absent (a compare-and-set inside
the FSM), so two admins cannot claim one address at once.

**Acceptance Criteria:**
- [ ] `/admin/groups` (global admins, and domain admins for their domains)
      lists, creates, edits and deletes groups; all mutations carry CSRF;
      a failed write fails the request.
- [ ] Creating a group on an address already held by a user, alias, identity
      or group is refused, including when two creates race (a test applies
      two commands for one address; one wins).
- [ ] RCPT to a group address expands to its members: each member gets one
      envelope (shared blob), their own rules, antispam result and quota
      check. A member over quota is skipped and logged; the others receive.
- [ ] Members are local users only; at most 1000 per group; nested groups
      are refused.
- [ ] A message that already carries `X-Bongo-Group: {group_id}` is not
      expanded again (loop protection), and the header is added on
      expansion.
- [ ] For submission and web compose, each expanded member counts against
      the sender's recipient budget.
- [ ] Groups appear in the GAL and in compose autocomplete for users of the
      same domain.

**Cluster:** a new command type. A v0.15 node stores `/__group__/…` as a
literal key and has no group. Upgrade every node before creating groups.

### US-006: Members-only and restricted posting
**Source:** audit #41. **Size:** S. **Needs:** US-005.

**Acceptance Criteria:**
- [ ] Posting policy is one of `anyone` (default), `domain` (authenticated
      users of the group's domain), `members` or `owners`.
- [ ] For any policy other than `anyone`, mail arriving on the MX port is
      refused at RCPT with `550 5.7.1 Posting to this group is restricted`;
      authenticated submission and web compose are checked against the
      policy using the authenticated account, never the From header.
- [ ] A table test covers each policy with a member, a non-member of the
      domain, an outsider over MX and an owner.

### Phase C: Shared folders and shared inboxes

Release note: folders can be shared with users and groups and appear in IMAP
clients under "Other Users". Shared inboxes let a team receive and send as
one address.

### US-007: Shared folders over IMAP
**Source:** audit #13. **Size:** L. **Needs:** US-001, US-002.

**What exists:** the audit is correct (MISSING; compiled out of the original
too). Every IMAP session method uses `s.userID` as the owner
(`internal/imap/backend.go`: Select, Status, Append, Fetch, Store, Copy,
Move, Expunge, Search, IDLE). Flags live on the per-mailbox
`MailboxMessage`, so they are shared state.

**Acceptance Criteria:**
- [ ] A resolver maps `Other Users/{address}/{folder}` to (owner, mailbox)
      and every session method goes through it; a name that does not
      resolve, or resolves to a folder the user holds no `l` right on,
      behaves exactly as a non-existent folder (no existence oracle).
- [ ] LIST shows shared folders under `Other Users/{owner address}/` for
      which the user has `l`. LSUB and SUBSCRIBE work on them.
- [ ] Rights are enforced per command: `r` for SELECT, EXAMINE, FETCH,
      SEARCH and STATUS; `s` for `\Seen`; `w` for other flags and keywords;
      `t` for `\Deleted`; `e` for EXPUNGE; `i` for APPEND and as COPY or MOVE
      destination; `k` for CREATE beneath; `x` for DELETE and RENAME. A test
      per right shows the command refused without it and allowed with it.
- [ ] Without `w`, `s` or `t`, SELECT opens read-only where the client asks
      for read-write (`[READ-ONLY]`).
- [ ] COPY and MOVE between owners use a new composite mailbox command that
      writes the destination record and, for MOVE, removes the source in one
      batch; the blob is shared, and the destination owner's quota is
      checked.
- [ ] IDLE and SSE notifications for a shared folder reach every session
      that has it selected.
- [ ] Flags, including `\Seen`, are shared by everyone with access (open
      question 2).

**Security:** the resolver is the single authorisation point; a test runs
every session method against a folder of another user with no grant and
expects the not-found response.

### US-008: Share folders and use them in the web UI
**Source:** audit #13. **Size:** M. **Needs:** US-007.

**Acceptance Criteria:**
- [ ] Folder settings offer sharing with a user or group at three presets:
      Read (`lrs`), Write (`lrswite`) and Full (`lrswipkxte`), plus "Can
      share" (`a`) for Full only. POST with CSRF; failure fails the request.
- [ ] Shared folders appear in the sidebar under "Shared with me", grouped by
      owner, with unread counts.
- [ ] Actions on a shared message are offered only when the rights allow
      them, and the handlers re-check the rights (a test per action with a
      Read grant).
- [ ] The owner sees who has access to each folder.

### US-009: IMAP ACL commands
**Source:** audit #13. **Size:** M. **Needs:** US-003, US-007.

**Acceptance Criteria:**
- [ ] MYRIGHTS returns the user's effective rights on any folder they can
      see; GETACL and LISTRIGHTS require `a`.
- [ ] SETACL and DELETEACL require `a`, accept identifiers that are a local
      user's address or a group address, and write through the same
      `/__acl__/` command as the web UI.
- [ ] An identifier that is not a local user or group is refused with `NO`
      and the same text whether or not it exists elsewhere.
- [ ] Negative rights (`-` identifiers) and `anyone` are refused.

### US-010: Shared inboxes
**Source:** audit #118. **Size:** M. **Needs:** US-007, US-008.

**Design:** a shared inbox is an account of kind `shared` (the `kind` field
from v0.15 US-015, or added here if that has not shipped). It cannot log
in. Admins grant members access to its folders, and members may send as its
address through a new send-as delegation: `CanSendAs` also allows an address
whose owner has granted the sender the RFC 4314 `p` (post) right on its
INBOX.

**Acceptance Criteria:**
- [ ] Admins create a shared account with an address and members (users or
      groups); members get the Write preset on all its folders.
- [ ] A shared account cannot authenticate on any protocol; the response is
      the same as a wrong password.
- [ ] A member can choose the shared address as From in compose and over
      submission; a non-member cannot (send-as tests for both, over SMTP
      and the web).
- [ ] Sent copies go to the sending member's Sent folder and, when the
      shared account has a Sent folder, also there.
- [ ] Removing a member removes their access and send-as on the next
      request (no cache).

### Phase D: Contacts

Release note: contact groups, several and shared address books, CSV and
LDIF import and export, a "This is me" card and a link from a contact to
every conversation with them.

### US-011: Contact groups
**Source:** audit #80. **Size:** M.

**What exists:** nothing; the audit is correct. A `KIND:group` card synced
by a client is stored raw and round-trips, but nothing interprets it.

**Acceptance Criteria:**
- [ ] `Contact` gains `kind` (10: empty or `group`) and `members` (11,
      repeated contact GUIDs). vCard 4 `KIND:group` with `MEMBER:urn:uuid:…`
      and Apple's `X-ADDRESSBOOKSERVER-KIND`/`X-ADDRESSBOOKSERVER-MEMBER`
      both map to it, and are written back in the form they arrived in.
- [ ] The contacts page lists groups, lets the user create, rename and
      delete them and add or remove members.
- [ ] Typing a group name in compose expands to the members' addresses;
      members without an address are skipped with a notice.
- [ ] At most 500 members per group; members that are deleted drop out.

### US-012: Several address books, and sharing them
**Source:** audit #82. **Size:** L. **Needs:** US-001.

**What exists:** one address book per user. CardDAV returns one fixed book
and refuses creating others (`internal/carddav/server.go:133-169`); the path
user is never used. The GAL covers local users only.

**Acceptance Criteria:**
- [ ] `/user/{id}/addressbook/{guid}` holds address-book metadata; `Contact`
      gains `addressbook_id` (12), empty meaning the default book, so
      existing contacts need no migration.
- [ ] CardDAV lists, creates, renames and deletes address books (MKCOL with
      the CardDAV resource type); deleting a book deletes its contacts with
      documented partial-failure semantics (contacts first, the book last,
      resumable).
- [ ] Address books can be shared with users or groups at `read` or `write`;
      shared books appear in the web UI and in the recipient's CardDAV home
      as `shared-{owner}-{guid}`, with privileges reported; writes to a
      `read` share answer 403.
- [ ] Compose autocomplete includes accepted shared books.
- [ ] Every handler checks the grant per request, with a negative test per
      route.

### US-013: CSV and LDIF import and export
**Source:** audit #83. **Size:** M.

**What exists:** vCard only (`contacts.go:230-345`); the audit is correct.

**Acceptance Criteria:**
- [ ] Import accepts CSV with the Google Contacts and Outlook header sets
      (mapped by header name, case-insensitive) and LDIF as Thunderbird
      exports it (`cn`, `givenName`, `sn`, `mail`, `telephoneNumber`, `o`).
      The format is detected from content, not the file name.
- [ ] Import is all-or-nothing with the existing quota check and the
      existing upload limits; a malformed row is reported by number and
      nothing is written.
- [ ] Export offers vCard, CSV (Google header set) and LDIF.
- [ ] CSV export neutralises formula injection: a cell starting with `=`,
      `+`, `-`, `@`, tab or carriage return is prefixed with `'`.
- [ ] Round trip: export then import of 100 contacts in each format gives
      the same names, addresses, phones and companies.

### US-014: "This is me"
**Source:** audit #78. **Size:** S.

**Acceptance Criteria:**
- [ ] A contact can be marked "This is me" (one per user, stored as
      `UserPreferences.self_contact_guid`).
- [ ] Compose offers "Attach my card", which attaches that contact as
      `.vcf`.
- [ ] Deleting the contact clears the preference.

### US-015: All conversations with a contact
**Source:** audit #79. **Size:** M.

**What exists:** `/search` passes the query to Bleve's query-string parser,
but `from` and `to` are keyword fields holding the whole header
(`internal/store/search.go:589-590`), so only an exact match of the raw
header hits. The audit's "full-text search can find a sender" is true only
for that exact form.

**Acceptance Criteria:**
- [ ] The search index gains `participants`: lowercased bare addresses from
      From, To and Cc. Existing indexes are rebuilt through the existing
      rebuild path, resumable and rate-limited.
- [ ] `/search?participant={address}` lists every message with that
      address in any of those headers, across mailboxes except Trash and
      Junk, grouped by thread.
- [ ] The contact detail card has "Show conversations", linking there; with
      several addresses on a card, all of them are included.
- [ ] A user only ever sees their own messages.

### Phase E: Scheduled fetch

### US-016: Scheduled fetch from remote POP3 and IMAP accounts
**Source:** audit #22. **Size:** L.

**What exists:** the audit is correct. `bongo-import imap` is a one-shot
IMAP client that APPENDs into Bongo over IMAP, bypassing rules and antispam;
there is no POP3 client and no scheduler.

**Acceptance Criteria:**
- [ ] `/settings/fetch` lets a user add up to 5 remote accounts (protocol,
      host, port, user name, password, folder or INBOX, leave on server,
      interval of at least 15 minutes). Mutations carry CSRF.
- [ ] Only implicit TLS (993, 995) or STARTTLS (143, 110) is allowed, with
      certificate verification; plaintext is refused.
- [ ] Remote hosts go through the safe dialer from v0.15 US-011: private,
      loopback and link-local addresses are refused after resolution.
- [ ] Passwords are encrypted at rest with AES-GCM under
      `DeriveKey(session_secret, "bongo/fetch-credentials/v1")` and never
      returned to the browser; rotating the session secret makes the user
      re-enter them (shown as an error on the settings page).
- [ ] A leader-only worker fetches due accounts. Fetched messages go through
      the local inbound pipeline (rules, antispam, quota); seen messages are
      tracked by UID (IMAP) or UIDL (POP3) at
      `/user/{id}/fetchseen/{account_guid}/{hash}` so nothing is imported
      twice, and the remote copy is deleted only after the local write
      succeeds, when "leave on server" is off.
- [ ] Per-message size is capped at the SMTP message limit; per run at most
      500 messages.
- [ ] After 5 consecutive authentication failures the account is paused and
      the user sees why; other errors back off.
- [ ] No password, and no message content, appears in logs.

## 4. Functional Requirements

FR-1 to FR-16 are the acceptance criteria of US-001 to US-016. In every
story:

- Every cookie-authenticated mutation is behind CSRF and same-origin checks.
- A persistence error on a user-visible write fails the request, unless the
  code says it is best-effort and logs the failure.
- Shared data is authorised in the handler on every request, never cached
  across requests, with a negative test per route and protocol command.
- New text is translated in en, de, es and fr, with a second grep pass.
- New config keys have defaults, validation and a class in `config/diff.go`.

## 5. Non-Goals (Out of Scope)

- IMAP PROXYAUTH (#16): dropped by the operator; admin impersonation is a
  security liability.
- POP3 SASL (#19): dropped by the operator.
- A full mailing-list server: subscribe and unsubscribe, external
  subscribers, archives, moderation queues, List-* headers, From rewriting
  or ARC for DMARC. This is the other half of B10 and needs its own research
  PRD.
- External members of distribution groups, and nested groups.
- Per-user `\Seen` on shared folders (open question 2).
- Negative ACL rights, `anyone` and anonymous IMAP access.
- An LDAP address book (#81): v0.17.
- Obsolete audit rows that relate to this release, not to be built:
  - NMAP store and queue protocols for third-party tools (#20): replaced by
    IMAP, DAV and the gRPC agent role.
  - Raw store access (#111): an internal protocol.
  - APOP (#18): needs reversible passwords.

## 6. Technical Considerations

### 6.1 Security

The server is internet-facing. This release lets users read and write each
other's data for the first time outside calendars.
- One resolver per protocol decides access (IMAP US-007, CardDAV US-012,
  web handlers); a folder or book the user cannot see answers exactly as one
  that does not exist.
- Rights are re-read on every request; a revoked grant takes effect on the
  next command, including in an already-selected IMAP session (the
  selected session re-checks on each command).
- US-016 stores third-party passwords. They are encrypted at rest, never
  logged, never sent back to the browser, and the fetcher refuses private
  addresses.
- No new route goes under `/internal/*`, and no new `http.Server` is
  planned.

### 6.2 The grant key space (from v0.15, extended)

```
/acl/{kind}/{owner_id}/{resource_id}/{grantee}      -> Grant protobuf
/aclrev/{grantee}/{kind}/{owner_id}/{resource_id}   -> empty marker
```

- `kind`: `calendar` (v0.15), `mailbox`, `addressbook` (this release).
- `resource_id`: calendar GUID, `MailboxMeta.id`, or address-book GUID;
  every segment escaped with `url.PathEscape`.
- `grantee`: `u:{user_id}` or `g:{group_id}`. No `anyone`.
- `Grant`: `level` (calendar and address book), `rights` (mailbox, RFC 4314
  letters), `state` (`pending` or `accepted`), `granted_by`, `created_at`,
  `updated_at`.
- All writes go through the composite `/__acl__/` command, applied in one
  Pebble batch, so a forward key never exists without its reverse marker.
  The `set` operation may also assign `MailboxMeta.id` in the same batch.
- Effective rights for a user: the union of `u:{user}` and every
  `g:{group}` they belong to. Group membership has its own reverse index,
  `/system/groupmember/{user_id}/{group_id}`, written by `/__group__/` in
  the same batch as the group.

Other new keys: `/system/group/{group_id}`, `/system/groupmember/…`,
`/user/{id}/usage`, `/user/{id}/addressbook/{guid}`,
`/user/{id}/fetch/{account_guid}`, `/user/{id}/fetchseen/…`. CLAUDE.md's key
table is updated by each story that adds a key.

### 6.3 Atomicity

- Grants and groups: composite commands, one batch each.
- Cross-owner COPY and MOVE: a new mailbox composite operation, one batch.
- Address-book deletion (US-012) and the folder rename migration (US-002):
  multi-step with documented, resumable partial-failure semantics.
- Fetch (US-016): order is local spool acceptance, then the seen record,
  then the remote delete. A crash after acceptance and before the seen
  record imports that one message twice; a crash before the remote delete
  leaves the remote copy in place. Delivery is at-least-once, documented in
  the worker.

### 6.4 Mixed-version clusters

Nodes do not publish their version, and new command types are not gated
(`prd-hardening-v0.9.md` 8.2, "Version-aware rolling upgrades"). This
release adds `/__group__/` and a cross-owner mailbox operation, and new
grant kinds. It is **not** rollable while groups, shares or cross-owner
moves are in use: upgrade every node first. Production runs one node. This
is the third consecutive release that needs a full upgrade before use; the
version-gating work should be scheduled before a multi-node deployment.

### 6.5 Library fork

US-003 introduces a maintained fork of go-imap. It adds a dependency the
project must keep current with upstream, and the depfloor and image checks
must cover it.

## 7. Success Metrics

- Every story done, each with a test that fails on `6725448`.
- Thunderbird and Apple Mail show a shared folder under Other Users, and
  Thunderbird's folder properties show its ACL and quota (manual check).
- A distribution group with 50 members delivers one copy each, with one
  blob.
- gosec medium findings do not increase; `make test-all` stays green.

## 8. Open Questions

1. US-003: fork go-imap under the project (proposed), or wait for an
   upstream change and ship shared folders without IMAP ACL commands and
   QUOTA first?
2. US-007: flags, including `\Seen`, are shared by everyone with access. Is
   per-user `\Seen` needed for shared inboxes? It would change the storage of
   flags.
3. US-002: rename existing personal folders called "Other Users" or
   "Shared", or make the prefixes configurable instead?
4. US-004: enforcing `quota_bytes` changes behaviour for any user an admin
   gave a quota on the assumption it was cosmetic. Should enforcement start
   with a warning-only period?
5. US-016: is storing third-party passwords acceptable at all, or should
   scheduled fetch wait for OAuth2 support for the large providers?

### 8.1 Decisions

1. (operator, 2026-10-26) Scope as listed in section 3.
2. (operator, 2026-10-26) IMAP PROXYAUTH (#16) and POP3 SASL (#19) are
   dropped.
3. (operator, 2026-10-27) ACL (RFC 4314) and QUOTA (RFC 9208) server
   support comes from a fork of go-imap v2 under the bongo-project
   organisation, pinned with a `replace` directive. Each change is offered
   upstream to emersion/go-imap as its own pull request, kept small and
   independent of Bongo; the fork is dropped once upstream releases them.

### 8.2 Candidates for the next release

Audit corrections found while writing this PRD (2026-10-26):

- #13 and #14: go-imap v2 cannot serve ACL or QUOTA without a library
  change; the audit does not say so.
- #14: the enforced quota is the domain quota, and APPENDLIMIT is already
  advertised.
- #79: field search works only on the exact raw header value.
- #82: the GAL covers local users only, filtered to the caller's domain;
  there are no extra books at all.

Release size: 16 stories (3 S, 9 M, 4 L). That is above the ~15-story guide
and has four L stories. Proposed split:

| Release | Stories | Why |
|---------|---------|-----|
| v0.16.0 | Phases A, B, C (US-001 to US-010) | Sharing, groups and shared inboxes: one design, reviewed together. |
| v0.16.1 | Phase D (US-011 to US-015) | Contacts work is independent apart from US-012 needing US-001. |
| v0.17 or later | Phase E (US-016) | Stores third-party passwords; open question 5 should be settled first. |

Other candidates:

- A full mailing-list server (the rest of B10).
- Per-user `\Seen` on shared folders.
- Version-aware rolling upgrades, before any multi-node deployment.
