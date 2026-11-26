# Verification: Sharing, groups, contacts and fetch v0.16

PRD: `prds/active/prd-sharing-contacts-v0.16.md`. Verified 2026-11-25 on `main` at
`04c3964`, rigour `adversarial`: two opus verifiers per PRD phase (implementation and
tests; edge cases and constraints), one for the cross-cutting requirements, and a
completeness critic. Raw verdicts: 12 agents, every key test run narrowly and passing.

## Summary

| Item | Verdict |
|------|---------|
| US-001, US-002, US-005, US-006 | PASS |
| US-008 to US-016 | PASS |
| US-004 | PASS (test gap noted) |
| US-003 | PARTIAL: upstream branch not prepared, fork syntax tests incomplete |
| US-007 | PARTIAL: cross-owner transfer indexing untested |
| FR-csrf, FR-persist, FR-config, FR-logcomp, FR-keytable | PASS |
| TC-security, TC-atomicity | PASS |
| FR-authz | PARTIAL: expired-pending negative tests missing on three read paths |
| FR-i18n | PARTIAL: about 48 untranslated http.Error bodies |
| FR-mixedversion | PARTIAL: cluster checklist not updated |
| Success metric: 50-member group | PARTIAL: no test at that scale |
| Success metric: manual client check | Not recorded |

Stories: 14 PASS, 2 PARTIAL, 0 FAIL. Cross-cutting: 7 PASS, 3 PARTIAL.

## Gaps

### US-003 (go-imap fork)
- **Upstream branch (criterion FAIL).** The story is done only when a QUOTA-first
  branch, rebased on upstream v2, is ready on bongo-project/go-imap for the operator's
  upstream pull request. Only the combined `bongo` branch exists. TASKS 39.1 is marked
  DONE regardless. The pull request text itself stays with the operator.
- The fork's ACL tests use atom mailbox names only; no quoted or literal name for
  SETACL, DELETEACL, GETACL, LISTRIGHTS or MYRIGHTS. No fork test checks that an ACL
  command answers BAD when the session lacks `SessionACL` (QUOTA has one).

### US-007 (shared folders over IMAP)
- No test covers the FSM indexing the destination owner's MailMeta from a cross-owner
  COPY or MOVE (`NewMails`, `internal/cluster/fsm.go:507`). Removing that loop fails
  no test.

### US-004 (per-user quota)
- Domain usage is never reported over IMAP by construction, but no test seeds a domain
  quota and asserts GETQUOTA and GETQUOTAROOT stay silent. Test gap only.

### FR-authz
- Expired-pending negative tests are missing for compose autocomplete over shared
  books (`TestGALSearchSharedBooks`), the shared-mail sidebar and the contacts
  sidebar "Shared with me". Pending is covered; expiry relies on store tests.

### FR-i18n
- About 48 new literal English `http.Error` bodies in `internal/web/handlers`
  (for example `shares.go:200`, `contacts_self.go:47`, `folder_share.go:123`,
  `mail_shared.go`). `bongo-pages.js:381-393` shows the response text on an htmx
  error, so users see them in English. The deliberate constant bodies
  (`folder not found`, `address book not found`) are documented and stay.
- All 300 new locale keys exist in de, es and fr; the templ second pass is clean.

### FR-mixedversion
- PRD 6.4 asks `docs/multi-node-cluster-checklist.md` to list every composite command
  that needs a full upgrade. It predates v0.16 and names none of `/__group__/`,
  `/__mailbox_assignid__/`, `/__mailbox_xfer__/`, `/__user_usage__/` or the new
  `/__acl__/` kinds and grantees. CLAUDE.md's guardrails do cover them.

### Success metrics
- No test or recorded check delivers to a 50-member group and asserts one copy each
  with one blob.
- The Thunderbird and Apple Mail check (shared folder under Other Users, ACL and quota
  in folder properties) is not recorded.

## Sweep
Invariants mode, 2026-11-26, after Phase 42, on `main` at `a2cb5ee`. Five agents listed 78
guarantees across grants, IMAP sharing and quota, groups and shared inboxes, contacts, and
fetch; 64 were kept (duplicates and low-cost ones dropped) and broken one at a time with
`go test -overlay`, the working tree unchanged throughout.

swept 64: 64 guarded, 0 confirmed unguarded, 0 refuted, 0 could-not-break/stale.

The harness was checked first by hand (dropping the accepted-state check in
`EffectiveGrant` fails 8 store tests), and two results were re-run by hand afterwards
(`SyncSharedInboxGrants` keeping non-members; `FetchHandler` sealing before the new fields
apply): both fail their named tests. Per-invariant breaks and failing tests are in the
sweep transcript, not repeated here.

## Gaps closed (Phase 42, 2026-11-26)
- US-003: QUOTA-only branch `upstream/quota` (72dbdd3) on bongo-project/go-imap, one commit on
  upstream v2, ready for the operator's upstream pull request. Fork ACL tests with quoted and
  literal names, and BAD without `SessionACL` (bongo-project/go-imap#2).
- US-007: `TestFSMCrossOwnerTransferIndexesDestinationAndNotifiesOnce` checks SQLite and Bleve
  for the destination owner; fails with the `NewMails` loop removed.
- US-004 and FR-authz: `TestQuotaDomainNeverReported`, `TestGALSearchSharedBooksExpiredPending`,
  `TestSharedSidebarExpiredPending`, `TestContactsSidebarSkipsExpiredPending`.
- FR-i18n: 49 `http.Error` bodies translated (36 keys, en/de/es/fr); guard test
  `TestHandlerErrorBodiesAreLocalized`. `Forbidden` in admin_groups.go stays literal so it matches
  the out-of-scope body (no existence oracle).
- FR-mixedversion: "Mixed-version clusters" section in `docs/multi-node-cluster-checklist.md`.
- Success metric: `TestGroupFiftyMembersOneCopyEachOneBlob` (50 envelopes, one blob).
- Still open: the manual Thunderbird and Apple Mail check (operator).
