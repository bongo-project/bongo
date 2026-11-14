# PRD: Sharing and contacts v0.16.0

## 1. Introduction / Overview

The feature-parity audit of the original Bongo
(`prds/research/parity-audit-original-bongo.md`, rows cited as #N) lists the
collaboration features a small office expects: shared folders and inboxes,
distribution groups, IMAP NAMESPACE, ACL and QUOTA, contact groups, shared
address books, CSV and LDIF exchange, and scheduled fetch from other mail
accounts. This PRD fixes the scope of v0.16.0 "Sharing and contacts".

Every story was first checked against `main` at `6725448` on 2026-10-26 and
re-checked against `e545272` (v0.15.14) on 2026-11-14. That baseline predates
v0.13 to v0.15, so the re-check found infrastructure several stories now
build on: the calendar grant key space with invitations, app passwords and
`auth.CheckStanding`, the header-derived mail views and their backfill
worker, the CalDAV shared collections, and the SSRF-safe feed client. Where
the code disagrees with the audit, the story says so; section 8.2 lists the
corrections together. Three findings shape the design:

- **The IMAP library cannot serve ACL or QUOTA.** go-imap v2.0.0-beta.8's
  server dispatches a fixed command list (`imapserver/conn.go:216-291`); it
  supports NAMESPACE (`SessionNamespace`) but has no session interface and no
  handler for GETQUOTA, GETQUOTAROOT, SETACL, GETACL, DELETEACL, LISTRIGHTS
  or MYRIGHTS. Anything else gets `BAD Unknown command`. ACL and QUOTA need a
  library change (US-003).
- **Only domain quota is enforced.** `UserProfile.quota_bytes` is editable
  in admin but not enforced (`internal/web/handlers/quota.go:30`), and no
  per-user usage is computed, so there is nothing per-user to report over
  IMAP QUOTA yet.
- **The grant key space exists but serves calendars only, and aliases are
  one-to-one.** v0.15 shipped `/acl/` and `/aclrev/` with the composite
  `/__acl__/` command (`internal/store/aclcommand.go`). Its kinds, levels,
  grantee shape, revokes and orphan sweep are all calendar-specific, so this
  release extends each of them (US-001). An alias maps to exactly one user
  (`AddressRecord.user_id`), so groups need their own records.

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
needs US-001 and US-002; US-004, US-007 and US-009 need US-003; US-010 needs
US-007; group grantees need US-005; US-012 needs US-001.

### Size key

S: one focused session. M: two or three packages, a proto change or a new
form. L: a new subsystem or a cross-cutting change.

## 2. Goals

- A user can share a mail folder with another user or a group at a chosen
  level, and the recipient sees it in the web UI and in IMAP clients once
  they accept the invitation.
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
**Source:** CLAUDE.md (grant key space); v0.15 US-003. **Size:** L.

**What exists:** `internal/store/aclcommand.go` and `proto/bongo/v1/acl.proto`
from v0.15. Everything in it is calendar-specific:

- `aclKinds` holds only `calendar` (line 73), and `validateACLPayload`
  refuses any other kind and requires a calendar level on every `set`
  (lines 161-177).
- `Grant.rights` (field 2) exists and `aclPayload` carries `Rights`, but
  `ACLRequest` has no `Rights` field, so `BuildACLCommand` never sends it
  (lines 198-202).
- `ACLKey` and `ACLRevKey` always write a `u:` grantee (lines 101-107), and
  `parseACLKey` drops any key whose grantee is not `u:` (line 406), so
  `ListGrantRefs` and the orphan sweep would never see a group grant.
- `RevokeUserGrants` issues calendar operations only (lines 533-537), and
  `GrantOrphaned` probes the owner, the grantee and the calendar key only
  (lines 663-678).
- The share flow is pending until accepted: a web share creates a `pending`
  grant and mails the grantee a link to `/calendar/shares`
  (`internal/web/handlers/calendar_share_invite.go`); `accept` is
  conditional in the FSM and refuses a pending grant older than
  `PendingGrantTTL` (30 days); `set` with `keep_state` keeps an existing
  grant's state. The handler caps a calendar at 50 live grants
  (`internal/web/handlers/calendar.go:154`).

**Acceptance Criteria:**
- [ ] Kinds `mailbox` and `addressbook` are accepted. Validation is per
      kind: calendar grants keep `freebusy`, `read`, `write`; mailbox grants
      carry `rights` (RFC 4314 letters `lrswipkxtea`, no level);
      address-book grants carry `level` (`read` or `write`). Unknown kinds,
      letters or levels are refused by `BuildACLCommand` and again by
      `PlanACL`. `ACLRequest` gains `Rights`.
- [ ] Mailbox and address-book shares use the v0.15 invitation flow
      unchanged: the grant is created `pending`, the grantee is mailed a
      link, only an `accepted` grant authorises, and `PendingGrantTTL`,
      conditional `accept` and `keep_state` apply as they do to calendars
      (decision 8). The invitations page lists every kind.
- [ ] Grantee `g:{group_id}` is accepted for every kind once US-005 has
      shipped. The key builders take a typed grantee (`u:` or `g:`) instead
      of prepending `u:`, and `parseACLKey` accepts both. A user's effective
      rights are the union of their own accepted grant and the accepted
      grants of every group they belong to.
- [ ] Revoke operations cover every kind: `RevokeUserGrants` revokes the
      user's grants as owner and as grantee for all kinds, and deleting a
      group revokes its `g:` grants in the same request.
- [ ] `GrantOrphaned` dispatches on kind: a mailbox grant is orphaned when
      the owner's mailbox with that id is gone, an address-book grant when
      the book is gone, and a `g:` grant when the group is gone. The
      two-sightings rule and the "only `ErrNotFound` counts" rule stay.
- [ ] Deleting a mailbox or an address book revokes its grants in the same
      request; the leader sweep removes what that misses.
- [ ] At most 50 live grants per resource, the calendar cap (decision 9).
      The 51st is refused with a translated message. Expired pending grants
      stay outside the cap, as for calendars.
- [ ] Mailboxes get a stable id; see US-001a below. Mailbox grants name the
      mailbox by that id, so a rename needs no grant change.

**US-001a, the mailbox id (part of US-001):**
- [ ] `MailboxMeta` gains `id` (field 5). A new mailbox composite command,
      not `/__acl__/`, assigns an id to a mailbox that has none, and writes
      the lookup key `/user/{user_id}/mailboxid/{id}` (value: the mailbox
      name) in the same Pebble batch (decision 10). The web share handler
      and SETACL assign the id before writing the grant.
- [ ] `applyMailboxRename` copies `id` to the new `MailboxMeta` (today it
      builds the new record field by field, `internal/store/mailbox.go:
      483-490`, and would drop it) and rewrites the lookup key in the same
      batch. Mailbox delete removes the lookup key in the same batch.
- [ ] Resolving a grant to a folder, for LIST and for the orphan sweep,
      reads the lookup key; it never scans the owner's mailboxes.

**Cluster:** no new grant command type, but new kinds, a new grantee form,
the `rights` field and a new mailbox command. A node on a v0.15 binary
refuses a `mailbox` or `addressbook` grant command at validation: the FSM
logs "acl synthetic command failed" and stores nothing
(`internal/cluster/fsm.go:411-416`). The applied-index watermark still
advances, so the grant is not replayed when that node is upgraded, and the
replicas stay diverged after the upgrade, not only during the mixed window.
The v0.15 node's `revoke_owner` and sweep also ignore the new kinds. The
new mailbox command is stored as a literal key there. Upgrade every node
before anyone shares a folder or an address book.

### US-002: IMAP NAMESPACE
**Source:** audit #15. **Size:** S.

**What exists:** the audit is correct that NAMESPACE is missing; it omits
that go-imap already supports it (`SessionNamespace`,
`imapserver/session.go:80`), so this is a method plus a capability
(`internal/imap/server.go:70-75`). The hierarchy delimiter is `/`
(`internal/imap/backend.go:571`).

**Acceptance Criteria:**
- [ ] NAMESPACE returns personal `""` with delimiter `/`, other users
      `"{other_users_prefix}/"` and shared `"{shared_prefix}/"`, and
      `NAMESPACE` is in the capability list.
- [ ] The prefixes are configurable (decision 5): `imap.other_users_prefix`
      (default `Other Users`) and `imap.shared_prefix` (default `Shared`).
      Validation refuses an empty value, a value containing `/`, `INBOX` in
      any case, and two equal prefixes. Both are restart class in
      `config/diff.go`, since open sessions hold the namespace.
- [ ] CREATE and RENAME of a personal folder whose name starts with either
      prefix followed by `/`, or equals either prefix, are refused with
      `NO`. The web folder forms refuse the same names.
- [ ] Existing personal folders are never renamed, and there is no startup
      clash report: there are no user accounts to migrate (decision 14).

### US-003: ACL and QUOTA in a go-imap fork
**Source:** this PRD's code check; upstream research (2026-11-14).
**Size:** M.

**Design:** Bongo maintains `bongo-project/go-imap`, a fork of
`emersion/go-imap` based on the upstream `v2` branch head rather than the
v2.0.0-beta.8 tag. It keeps the module path `github.com/emersion/go-imap/v2`
so `go.mod` pins it with a `replace` directive. The ACL and QUOTA work
starts from upstream pull request #730 (`SessionACL`, closed unmerged;
its net change applies cleanly to `v2` head) and from `0kaba0hub/go-imap`
branch `yarilo-patches` commit `c90f7a7` (`SessionQuota`). The
yarilo-patches copy of the ACL change (`362b37e`) is not used: it depends
on that fork's METADATA and CONDSTORE work. Both sources are MIT-licensed and are credited in the fork's history
and in this repository's notices. The server-side code reuses the types the
root package already has (`acl.go`; `CapACL` and `CapQuota` in
`capability.go`). The fork is planned as long-lived (decision 17). The
alternative, a protocol proxy in front of go-imap, would re-parse every
command and is rejected.

Moving from beta.8 to the `v2` head changes more than ACL and QUOTA.
Upstream #785 serves LSUB as LIST with `SelectSubscribed` and
`SelectRecursiveMatch`; Bongo's `List` filters on `SelectSubscribed` and
does not implement `SelectRecursiveMatch`
(`internal/imap/backend.go:518-530`). The `v2` head also adds
`SelectData.ReadOnly`, which US-007 uses for read-only shared folders.

**Acceptance Criteria:**
- [ ] `go.mod` requires `github.com/emersion/go-imap/v2` and replaces it
      with a pinned version of `bongo-project/go-imap` based on upstream
      `v2` head.
- [ ] The fork adds parsing and responses for GETQUOTA, GETQUOTAROOT
      (RFC 9208) and SETACL, DELETEACL, GETACL, LISTRIGHTS, MYRIGHTS
      (RFC 4314), dispatched only when the session implements
      `SessionQuota` or `SessionACL`; a session that does not still answers
      `BAD`.
- [ ] The capabilities `QUOTA`, `QUOTA=RES-STORAGE` and `ACL` are
      advertised only when the session implements the interface.
- [ ] The fork's own tests cover each command's syntax, including literal
      and quoted mailbox names.
- [ ] After the upgrade, the existing IMAP tests pass, and LSUB is retested
      against the new dispatch: an unsubscribed folder is not returned, a
      subscribed one is, and `SelectRecursiveMatch` does not return every
      folder (`subscription_e2e_test.go` extended).
- [ ] Licensing and checks: go-imap, PR #730 and the QUOTA commit are MIT, so
      they do not belong in `THIRD_PARTY_NOTICES.md` part 2 (Apache-2.0
      NOTICE files). A short section there names the fork, its upstream and
      the credited commits. `internal/depfloor/depfloor_test.go` gains a
      check that the `replace` line points at the fork, since
      `TestMinimumVersions` matches `require` lines only. govulncheck runs
      against the replaced module.
- [ ] Upstream: go-imap's contributing rules (github.com/emersion/.github)
      forbid contributions made by autonomous tools and require the human
      author to write the pull request title and description, with
      `Signed-off-by` and `Assisted-by` trailers. The upstream change is
      therefore prepared as a branch on the fork, QUOTA first as a small
      change on its own; the operator raises it in #emersion on Libera,
      then writes and opens the pull request. The story is done when the
      branch is ready; it does not wait for upstream.

### US-004: Per-user quota, enforced and reported over IMAP
**Source:** audit #14 (corrected). **Size:** M. **Needs:** US-003.

**What exists:** the audit says IMAP enforces quota on APPEND; it is the
domain quota (`DomainQuota.max_storage_bytes`, `internal/imap/backend.go:
672-691`). Every protocol asks one method,
`DomainQuotaStore.RecipientWouldExceedQuota(userID, addBytes)`
(`internal/store/domain_quota_store.go:195`), used by SMTP RCPT, IMAP
APPEND, the web and DAV. No per-user usage exists: the leader's usage
recompute (`runUsageRecompute` and `computeAndWriteDomainUsage`,
`internal/agent/agent.go:3419-3570`) adds mail, event, contact and task
sizes straight into per-domain totals, and the admin user page's "used"
figure is never set (`UsedBytes`, `internal/web/templates/admin.templ:58`,
always 0).

**Acceptance Criteria:**
- [ ] The usage recompute also writes `/user/{id}/usage` (little-endian
      int64 bytes, the encoding of the domain usage key, same accounting as
      the domain total) for every user. The per-user values travel in one
      composite command per pass, not one Raft write per user.
- [ ] A non-zero `UserProfile.quota_bytes` is enforced inside
      `RecipientWouldExceedQuota`, so every caller (SMTP RCPT, IMAP APPEND,
      web, DAV) gets it with the same best-effort semantics; the domain
      quota still applies as well. Enforcement starts with the release
      (decision 6); no upgrade warning is needed (decision 14).
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
one user (`internal/store/recipient_resolver.go:198`). It runs tiered
lookups: the address record, the legacy `/system/alias/` key, the users
table, catch-all, the postmaster and abuse role addresses, with alias
domains rewritten and disabled domains refused. A dead or empty target
returns unavailable and never falls through to a later tier. SMTP already
writes one envelope per local recipient sharing one blob (v0.12 US-009), so
expansion at RCPT fits the existing flow. B10 (`prds/TASKS-archive.md`)
asks for a research PRD covering distribution groups against full mailing
lists; this story is the distribution-group half, and full list features
are non-goals.

**Design:** `/system/group/{group_id}` holds a `Group` protobuf (address,
display name, domain, member user IDs, owner user IDs, posting policy). The
address is claimed in the global address index as an `AddressRecord` of kind
`group` with a new `group_id` field (6) and an empty `user_id`. Creating a
group writes both keys and the membership reverse index through a new
composite command, `/__group__/`, which the FSM applies in one Pebble batch
only if the address is free (a compare-and-set inside the FSM), so two
admins cannot claim one address at once.

Design notes:
- Taken addresses are judged today by `auth.CheckAddressFree`
  (`internal/auth/create.go:95-148`): the `users` and `aliases` SQLite
  tables and the `/system/address/` and `/system/alias/` keys. The handler
  runs that check; the FSM compare-and-set checks both Pebble prefixes.
  User and alias creation remain check-then-act, so the FSM closes the race
  between two groups only.
- Every reader of `AddressRecord.user_id` must handle a group record:
  resolver tier 1, `CanSendAs` (`internal/store/sendas.go:20`, a group
  address is never sendable), `CheckAddressFreeFor`, and the SQLite
  `addresses` index the FSM maintains. The group lookup sits in tier 1,
  before the dead-target check.
- An address on an alias domain resolves through the alias-domain rewrite
  to the group on the target domain. Catch-all never matches a group
  address.
- Expanded mail is not "to me" for the header views (`to_me` is judged
  against the member's own addresses), and vacation replies are not sent
  for it.

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
- [ ] Fan-out has its own cap, separate from the external-recipient budget
      (`internal/smtp/server.go:74`, which counts external recipients only
      and does not change): a per-sender limit on expanded member envelopes
      per window, with a default and a config key, applied on MX,
      submission and web compose (decision 11).
- [ ] Groups appear in the GAL and in compose autocomplete for users of the
      same domain.

**Cluster:** a new command type. A node on a v0.15 binary stores
`/__group__/…` as a literal key and has no group. Upgrade every node before
creating groups.

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
**Source:** audit #13. **Size:** L. **Needs:** US-001, US-002, US-003.

**What exists:** the audit is correct (MISSING; compiled out of the original
too). Every IMAP session method uses `s.userID` as the owner
(`internal/imap/backend.go:355-1890`: Select, Create, Delete, Rename,
Subscribe, Unsubscribe, List, Status, Append, Poll, Idle, Unselect,
Expunge, Search, Fetch, Store, Copy, Move). Mailbox existence goes through
`requireMailbox` and `requireNoMailbox` (lines 211, 222). Flags live on the
per-mailbox `MailboxMessage`, so they are shared state. Keywords are tags on
the owner's `MailMeta` (`applyKeywords`, line 1657). `checkAccount` re-reads
the account on every command, and IDLE re-checks every 30 seconds
(`idleRecheckInterval`, line 239). COPY and MOVE are deliberately not
quota-gated (lines 1738-1742).

**Acceptance Criteria:**
- [ ] A resolver maps `{other_users_prefix}/{address}/{folder}` to (owner,
      mailbox) and every session method goes through it, by way of
      `requireMailbox` and `requireNoMailbox`; a name that does not
      resolve, or resolves to a folder the user holds no accepted `l` right
      on, behaves exactly as a non-existent folder (no existence oracle).
- [ ] LIST shows shared folders under `{other_users_prefix}/{owner
      address}/` for which the user has `l`. LSUB and SUBSCRIBE work on
      them.
- [ ] Rights are enforced per command: `r` for SELECT, EXAMINE, FETCH,
      SEARCH and STATUS; `s` for `\Seen`; `w` for other flags and keywords;
      `t` for `\Deleted`; `e` for EXPUNGE; `i` for APPEND and as COPY or MOVE
      destination; `k` for CREATE beneath; `x` for DELETE and RENAME. A test
      per right shows the command refused without it and allowed with it.
- [ ] Rights are re-read on every command, on the same path as
      `checkAccount`, and in IDLE on the 30-second recheck.
- [ ] Without `w`, `s` or `t`, SELECT opens read-only where the client asks
      for read-write, through `SelectData.ReadOnly` (`[READ-ONLY]`, from the
      US-003 library upgrade).
- [ ] Keywords set with `w` on a shared message change the owner's
      `MailMeta` tags and use the owner's tag namespace; a keyword that
      would create a tag beyond the owner's tag limit is refused.
- [ ] COPY and MOVE between owners use a new composite mailbox command that,
      in one batch, writes the destination owner's `MailMeta` (the blob is
      shared) and `MailboxMessage` and, for MOVE, removes the source. The
      destination owner's derived indexes are updated from the command
      result, as for a delivery, and the destination owner's quota is
      checked (unlike a same-owner COPY, a cross-owner copy adds usage).
- [ ] IDLE and SSE notifications for a shared folder reach every session
      that has it selected (the notifier is keyed by owner and mailbox,
      `internal/imap/notify.go:54`).
- [ ] Flags, including `\Seen`, are shared by everyone with access
      (decision 4).

**Security:** the resolver is the single authorisation point; a test runs
every session method against a folder of another user with no grant, and
with a pending grant, and expects the not-found response.

### US-008: Share folders and use them in the web UI
**Source:** audit #13. **Size:** M. **Needs:** US-007.

**Acceptance Criteria:**
- [ ] Folder settings offer sharing with a user or group at three presets:
      Read (`lrs`), Write (`lrswite`) and Full (`lrswipkxte`), plus "Can
      share" (`a`) for Full only. POST with CSRF; failure fails the request.
      The share is pending and the grantee is mailed an invitation, reusing
      the calendar share form and `/calendar/shares` flow, generalised to a
      shares page for every kind (decision 8).
- [ ] Accepted shared folders appear in the sidebar under "Shared with me",
      grouped by owner, with unread counts, within the sidebar sections of
      the unified navigation (#173).
- [ ] Shared folders are left out of All Mail, the virtual mailboxes and the
      header views (`/mail/view/…`).
- [ ] Actions on a shared message are offered only when the rights allow
      them, and the handlers re-check the rights (a test per action with a
      Read grant).
- [ ] The owner sees who has access to each folder, and whether each grant
      is pending or accepted.

### US-009: IMAP ACL commands
**Source:** audit #13. **Size:** M. **Needs:** US-003, US-007.

**Acceptance Criteria:**
- [ ] MYRIGHTS returns the user's effective rights on any folder they can
      see; GETACL and LISTRIGHTS require `a`.
- [ ] SETACL and DELETEACL require `a`, accept identifiers that are a local
      user's address or a group address, and write through the same
      `/__acl__/` command as the web UI. A SETACL for a new grantee creates
      a pending grant and sends the same invitation; a SETACL that changes
      an existing grant's rights keeps its state (`keep_state`).
- [ ] An identifier that is not a local user or group is refused with `NO`
      and the same text whether or not it exists elsewhere.
- [ ] Negative rights (`-` identifiers) and `anyone` are refused.

### US-010: Shared inboxes
**Source:** audit #118. **Size:** M. **Needs:** US-007, US-008.

**What exists:** `UserProfile.kind` (field 12,
`proto/bongo/v1/profile.proto:19`) already distinguishes `resource`
accounts, and one function, `auth.CheckStanding`
(`internal/auth/local.go:61`), refuses a disabled or resource account
after the password compare on every credential path: web login,
`AuthenticateProtocol` (account and app passwords) and device-link
redemption.

**Design:** a shared inbox is an account of kind `shared`. It cannot log
in. Admins assign members; members get folder access through grants and
send through the existing send-as mechanism, kept separate from grants
(decision 12): `CanSendAs` (`internal/store/sendas.go`) also allows the
shared account's address when its `AddressRecord` has `CanSend` and
`Verified` set and the sender is in the shared account's member list. The
member list is stored with the shared account, not derived from folder
grants, and is read on every request.

**Acceptance Criteria:**
- [ ] Admins create a shared account with an address and members (users or
      groups); members get the Write preset on all its folders.
- [ ] `CheckStanding` refuses kind `shared`, so a shared account cannot
      authenticate on any protocol and the response is the same as a wrong
      password. It cannot hold app passwords or device links.
- [ ] A member can choose the shared address as From in compose and over
      submission; a non-member cannot (send-as tests for both, over SMTP
      and the web). Folder rights alone never permit sending.
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
and refuses creating others (`internal/carddav/server.go:149-185`); the path
user is never used. The GAL covers local users only. go-webdav v0.7.0
passes MKCOL to `CreateAddressBook` (`carddav/server.go:709-732`) but
answers PROPPATCH on an address book with 501 (line 622) and has no
privilege hook (line 500). CalDAV already solves the same problems for
shared calendars: `shared-{owner}-{calendar}` collection names with a
validated parser (`internal/caldav/share.go:30-52`) and a PROPFIND rewrite
that reports read-only shares (`internal/caldav/privileges.go`,
`PrivilegeFilter`).

**Acceptance Criteria:**
- [ ] `/user/{id}/addressbook/{guid}` holds address-book metadata; `Contact`
      gains `addressbook_id` (12), empty meaning the default book.
- [ ] CardDAV lists, creates and deletes address books (MKCOL with the
      CardDAV resource type); deleting a book deletes its contacts with
      documented partial-failure semantics (contacts first, the book last,
      resumable), and revokes its grants in the same request.
- [ ] Renaming a book is web-only (decision 16); CardDAV PROPPATCH rename is
      a section 8.2 candidate.
- [ ] Address books can be shared with users or groups at `read` or `write`
      through the pending-invitation flow (decision 8); accepted shared
      books appear in the web UI and in the recipient's CardDAV home as
      `shared-{owner}-{guid}`, parsed with the CalDAV rules (UUID owner, no
      `/`). Privileges are reported through the CalDAV `PrivilegeFilter`
      approach, generalised for CardDAV; writes to a `read` share answer 403.
- [ ] Compose autocomplete includes accepted shared books.
- [ ] Every handler checks the grant per request, with a negative test per
      route, including a pending grant.

### US-013: CSV and LDIF import and export
**Source:** audit #83. **Size:** M.

**What exists:** vCard only (`internal/web/handlers/contacts.go:230-347`,
`ExportContacts` and `ImportContacts`); the audit is correct.

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
      `UserPreferences.self_contact_guid`, field 22).
- [ ] Compose offers "Attach my card", which attaches that contact as
      `.vcf`.
- [ ] Deleting the contact clears the preference.

### US-015: All conversations with a contact
**Source:** audit #79. **Size:** M.

**What exists:** `/search` passes the query to Bleve's query-string parser,
but `from` and `to` are keyword fields holding the whole header
(`internal/store/search.go:603-604`), so only an exact match of the raw
header hits. The audit's "full-text search can find a sender" is true only
for that exact form. v0.15 added header-derived columns to the per-user
SQLite `maildocument` table (`to_me`, `sender_addr`, `list_id`, `list_name`,
versioned by `headers_rev`; `internal/store/metadata.go:160-170`,
`metadata_headers.go`), filled by the resumable `HeaderBackfill` worker
(`internal/agent/agent.go:1924`). The full index rebuild runs only after a
snapshot restore or an unclean shutdown since bf3a527, and took about
1,700 s for 75,000 messages, so it is not a migration path.

**Acceptance Criteria:**
- [ ] Participants are stored in SQLite, not Bleve (decision 13): lowercased
      bare addresses from From, To and Cc, in a per-user table keyed by
      message GUID and address, written by `IndexMail` and derived for
      existing mail by the header backfill, under a bumped `headers_rev`.
      The backfill is resumable and rate-limited. The Bleve mapping does not
      change.
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
there is no POP3 client and no scheduler. `internal/importer` has an IMAP
message source (with Gmail folder mapping) the fetcher can reuse. The
SSRF-safe client in `internal/netx/safeclient.go` is HTTP-only: its dialer
is unexported (line 166) and its port rules are for HTTP; only
`IsForbiddenAddr` (line 87) is exported. `security.DeriveKey`
(`internal/security/derive.go:18`) exists; nothing uses AES-GCM yet.

**Acceptance Criteria:**
- [ ] `/settings/fetch` lets a user add up to 5 remote accounts (protocol,
      host, port, user name, password, folder or INBOX, leave on server,
      interval of at least 15 minutes). Mutations carry CSRF.
- [ ] Only implicit TLS (993, 995) or STARTTLS (143, 110) is allowed, with
      certificate verification; plaintext is refused.
- [ ] Remote hosts go through a raw-TCP dialer added to `internal/netx` that
      shares the safe client's address check (the same `Control` hook and
      `IsForbiddenAddr`): private, loopback and link-local addresses are
      refused after resolution, on every connection.
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
  across requests, with a negative test per route and protocol command,
  including a pending and an expired-pending grant.
- New text is translated in en, de, es and fr, with a second grep pass.
- New config keys have defaults, validation and a class in `config/diff.go`.
- Every new log prefix or `component` value (for example `groups`,
  `fetch`, `acl`) is added to `config.LogComponents`
  (`internal/config/logcomponents.go`); `TestLogComponentsMatchSource`
  fails otherwise.
- Every new key added to Pebble is added to CLAUDE.md's key table by the
  story that adds it.

## 5. Non-Goals (Out of Scope)

- IMAP PROXYAUTH (#16): dropped by the operator; admin impersonation is a
  security liability.
- POP3 SASL (#19): dropped by the operator.
- A full mailing-list server: subscribe and unsubscribe, external
  subscribers, archives, moderation queues, List-* headers, From rewriting
  or ARC for DMARC. This is the other half of B10 and needs its own research
  PRD.
- External members of distribution groups, and nested groups.
- Per-user `\Seen` on shared folders (decision 4).
- Negative ACL rights, `anyone` and anonymous IMAP access.
- Renaming an address book over CardDAV (decision 16).
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
  that does not exist. A pending grant authorises nothing.
- Rights are re-read on every request; a revoked grant takes effect on the
  next command, including in an already-selected IMAP session (the
  selected session re-checks on each command, and IDLE on its 30-second
  recheck).
- Shared inboxes cannot log in: `auth.CheckStanding` refuses them on every
  credential path, and send-as stays in `CanSendAs`, separate from grants.
- US-016 stores third-party passwords. They are encrypted at rest, never
  logged, never sent back to the browser, and the fetcher refuses private
  addresses on every connection.
- No new route goes under `/internal/*`, and no new `http.Server` is
  planned.

### 6.2 The grant key space (from v0.15, extended)

```
/acl/{kind}/{owner_id}/{resource_id}/{grantee}      -> Grant protobuf
/aclrev/{grantee}/{kind}/{owner_id}/{resource_id}   -> empty marker
```

- `kind`: `calendar` (v0.15), `mailbox`, `addressbook` (this release).
- `resource_id`: calendar GUID, `MailboxMeta.id`, or address-book GUID;
  every ID segment escaped with `url.PathEscape`.
- `grantee`: `u:{user_id}` (v0.15) or `g:{group_id}` (this release). No
  `anyone`. Every prefix ends in `/`, as today.
- `Grant` (`proto/bongo/v1/acl.proto`): `level` (calendar and address
  book), `rights` (mailbox, RFC 4314 letters), `state` (`pending` or
  `accepted`), `granted_by`, `created_at`, `updated_at`. No proto change is
  needed.
- All writes go through the composite `/__acl__/` command, applied in one
  Pebble batch, so a forward key never exists without its reverse marker.
  `accept` stays conditional and time-bounded by `PendingGrantTTL`, judged
  from the stored `updated_at` and the command's `at`, never the clock.
- `PlanACL` runs its scans inside Apply, on Raft's single goroutine. With
  50 live grants per resource for every kind (decision 9), `revoke_resource`
  touches about 50 grants, and `revoke_owner` and `revoke_grantee` about 50
  per resource the user owns or holds, across all kinds; expired pending
  grants are outside the cap and never deleted, so the bound stays soft.
  The comment on `PlanACL` is updated to say so.
- Effective rights for a user: the union of their accepted `u:{user}` grant
  and every accepted `g:{group}` grant of a group they belong to. Group
  membership has its own reverse index,
  `/system/groupmember/{user_id}/{group_id}`, written by `/__group__/` in
  the same batch as the group. "Shared with me" is a prefix scan of
  `/aclrev/u:{me}/{kind}/` plus `/aclrev/g:{group}/{kind}/` for each group.

Other new keys: `/system/group/{group_id}`, `/system/groupmember/…`,
`/user/{id}/mailboxid/{id}`, `/user/{id}/usage`,
`/user/{id}/addressbook/{guid}`, `/user/{id}/fetch/{account_guid}`,
`/user/{id}/fetchseen/…`.

### 6.3 Atomicity

- Grants and groups: composite commands, one batch each.
- Mailbox ids: a mailbox composite command writes `MailboxMeta.id` and the
  lookup key in one batch; rename and delete keep the lookup key in the
  same batch as the mailbox.
- Cross-owner COPY and MOVE: a new mailbox composite operation, one batch,
  covering the destination `MailMeta` and `MailboxMessage` and the source
  removal for MOVE.
- Per-user usage: one composite command per recompute pass.
- Address-book deletion (US-012): multi-step with documented, resumable
  partial-failure semantics.
- Fetch (US-016): order is local spool acceptance, then the seen record,
  then the remote delete. A crash after acceptance and before the seen
  record imports that one message twice; a crash before the remote delete
  leaves the remote copy in place. Delivery is at-least-once, documented in
  the worker.

### 6.4 Mixed-version clusters

Nodes do not publish their version, and new command types are not gated
(`prds/done/prd-hardening-v0.9.md` 8.2, "Version-aware rolling upgrades").
v0.15.x already needs a full upgrade before use for `/__acl__/`,
`/__bayes_train__/`, `/__apppassword_touch__/` and
`/__apppassword_ticket__/`. This release adds `/__group__/`, the mailbox-id
command, the cross-owner mailbox operation and the per-user usage command,
and new grant kinds and grantees that a v0.15 node refuses (US-001). It is
**not** rollable while groups, shares or cross-owner moves are in use:
upgrade every node first. Production runs one node. The version-gating
work should be scheduled before a multi-node deployment, and
`docs/multi-node-cluster-checklist.md` should list every composite command
that needs it.

Tests: following CLAUDE.md's guardrails, every test applier that can see a
grant, a group, a mailbox id or a cross-owner move dispatches the matching
synthetic command (`IsACLSyntheticCommand` and the new predicates, for
example `IsGroupSyntheticCommand`), including `directApplier` in
`internal/store/mailbox_test.go`. Otherwise the command is stored as a
literal key and negative tests pass vacuously.

### 6.5 Library fork

US-003 introduces `bongo-project/go-imap`, a long-lived fork based on
upstream `v2` head, with the module path kept and a `replace` directive in
`go.mod`. It adds a dependency the project must keep current with upstream:
each upstream change is merged into the fork, the IMAP tests run against
it, and the depfloor test checks the `replace` line. Upstream contribution
follows go-imap's contributing rules: the operator writes and opens each
pull request from a prepared branch, QUOTA first.

## 7. Success Metrics

- Every story done, each with a test that fails on `e545272` (v0.15.14).
- Thunderbird and Apple Mail show a shared folder under Other Users, and
  Thunderbird's folder properties show its ACL and quota (manual check).
- A distribution group with 50 members delivers one copy each, with one
  blob.
- gosec medium findings do not increase; `make test-all` stays green.

## 8. Open Questions

All five original questions are resolved; the decisions are in 8.1.

1. US-003: fork go-imap or wait for upstream? Resolved by decisions 3
   and 17.
2. US-007: per-user `\Seen`? Resolved by decision 4.
3. US-002: rename folders or configure the prefixes? Resolved by
   decisions 5 and 14.
4. US-004: warning-only period for `quota_bytes`? Resolved by decisions 6
   and 14.
5. US-016: store third-party passwords? Resolved by decision 7.

Open:

6. US-010: members of a shared inbox are assigned by an admin. Do their
   folder grants go through the invitation flow like any share, or are
   admin-assigned grants created `accepted`?
7. US-016: decision 7 asks for "a dedicated key". Is a key derived from the
   session secret with its own purpose string enough, or should it be a
   separate configured secret, so that rotating the session secret does not
   invalidate stored passwords?

### 8.1 Decisions

1. (operator, 2026-10-26) Scope as listed in section 3.
2. (operator, 2026-10-26) IMAP PROXYAUTH (#16) and POP3 SASL (#19) are
   dropped.
3. (operator, 2026-10-27) ACL (RFC 4314) and QUOTA (RFC 9208) server
   support comes from a fork of go-imap v2 under the bongo-project
   organisation, pinned with a `replace` directive. Each change is offered
   upstream to emersion/go-imap as its own pull request, kept small and
   independent of Bongo; the fork is dropped once upstream releases them.
   Refined by decision 17.
4. (operator, 2026-10-27) Open question 2: flags, including `\Seen`, stay
   shared by everyone with access in v0.16. Per-user `\Seen` remains a
   candidate for a later release.
5. (operator, 2026-10-27) Open question 3: the shared namespace prefixes are
   configurable, defaulting to "Shared" and "Other Users". Existing user
   folders are never renamed; if a prefix clashes with an existing folder,
   the server reports it at startup and the admin picks another prefix.
   The startup report is dropped by decision 14.
6. (operator, 2026-10-27) Open question 4: `quota_bytes` is enforced from
   the release that ships US-004, with no warning-only period. The upgrade
   notes must say so prominently. The upgrade-notes work is dropped by
   decision 14.
7. (operator, 2026-10-27) Open question 5: scheduled fetch may store
   third-party passwords, encrypted at rest with a dedicated key, never
   logged and deletable by the user. It ships after its own security
   review; OAuth2 for the large providers can follow later.
8. (operator, 2026-11-14) Folder and address-book shares use the v0.15
   pending-invitation flow (email, accept, 30-day `PendingGrantTTL`), the
   same as calendars.
9. (operator, 2026-11-14) Grant caps match calendars: 50 live grants per
   resource. The 100-per-resource and 500-per-owner caps are dropped, and
   the FSM scan bound is restated accordingly (section 6.2).
10. (operator, 2026-11-14) `MailboxMeta.id` is assigned by its own mailbox
    composite command, not inside `/__acl__/`. Rename copies the id, and an
    id-to-name lookup key is added.
11. (operator, 2026-11-14) Group fan-out gets its own cap, separate from the
    external-recipient budget.
12. (operator, 2026-11-14) Shared-inbox send-as uses the existing send-as
    mechanism (`AddressRecord.CanSend`/`Verified`, checked in `CanSendAs`),
    not an RFC 4314 right. Grants and send-as stay separate mechanisms.
13. (operator, 2026-11-14) US-015 stores participants in a SQLite column or
    table filled by a resumable backfill worker (the `HeaderBackfill`
    pattern), with no Bleve schema change and no index rebuild.
14. (operator, 2026-11-14) There are no user accounts yet. The US-002
    startup clash report and the US-004 upgrade-warning work are dropped.
15. (operator, 2026-11-14) The release split in section 8.2 stands. UI
    unification shipped in #173, so it no longer competes for v0.16.
16. (operator, 2026-11-14) Renaming an address book is web-only in v0.16.
    CardDAV PROPPATCH rename is a section 8.2 candidate.
17. (operator, 2026-11-14, from upstream research) The fork
    `bongo-project/go-imap` is based on upstream `emersion/go-imap` branch
    `v2` head, not v2.0.0-beta.8, and keeps the module path
    `github.com/emersion/go-imap/v2` for a `replace` directive. It starts
    from upstream PR #730 (`SessionACL`) and `0kaba0hub/go-imap` branch
    `yarilo-patches` commit `c90f7a7` (`SessionQuota`), MIT, credited. LSUB is retested after the upgrade (upstream #785), and
    `SelectData.ReadOnly` serves read-only shared folders. The fork is
    planned as long-lived. go-imap's contributing rules forbid autonomous
    tools contributing and require the human author to write the pull
    request title and description, with `Signed-off-by` and `Assisted-by`
    trailers, so the upstream change is prepared as a branch and written
    and opened by the operator, QUOTA first as a small pull request, raised
    in #emersion on Libera beforehand.

### 8.2 Candidates for the next release

Audit corrections found while writing this PRD (2026-10-26):

- #13 and #14: go-imap v2 cannot serve ACL or QUOTA without a library
  change; the audit does not say so.
- #14: the enforced quota is the domain quota, and APPENDLIMIT is already
  advertised.
- #79: field search works only on the exact raw header value.
- #82: the GAL covers local users only, filtered to the caller's domain;
  there are no extra books at all.

Release size: 16 stories (3 S, 8 M, 5 L). That is above the ~15-story guide
and has five L stories. Split (decision 15):

| Release | Stories | Why |
|---------|---------|-----|
| v0.16.0 | Phases A, B, C (US-001 to US-010) | Sharing, groups and shared inboxes: one design, reviewed together. |
| v0.16.1 | Phase D (US-011 to US-015) | Contacts work is independent apart from US-012 needing US-001. |
| v0.17 or later | Phase E (US-016) | Stores third-party passwords; it ships after its own security review (decision 7). |

Other candidates:

- A full mailing-list server (the rest of B10).
- Per-user `\Seen` on shared folders.
- Version-aware rolling upgrades, before any multi-node deployment.
- Renaming an address book over CardDAV (PROPPATCH), which go-webdav v0.7.0
  answers with 501 and would need a wrapper like
  `internal/caldav/mkcalendar.go`.
- **Sidebar order is fixed** (found after v0.15.1, 2026-11-04; the other
  findings from that review shipped in v0.15.2,
  `prds/done/prd-ui-fixes-v0.15.2.md`). Candidate: let a user reorder the
  sidebar, or at least move the header views (To me, From my contacts,
  Mailing lists) above the folders, stored in the user's preferences.
