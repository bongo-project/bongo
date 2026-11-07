# Verification: App passwords v0.15.3

**Date:** 2026-11-06
**Commit verified:** `67fa569`, then re-checked after the fixes in Phase 36 tasks 36.10 to 36.12 and 36.14
**PRD:** [done/prd-app-passwords-v0.15.3.md](done/prd-app-passwords-v0.15.3.md)
**Rigor:** adversarial. Three requirement groups each had two read-only verifiers, one judging whether tests would go red and one attacking the behaviour. A completeness critic then reviewed the whole result and settled disagreements. Operator decisions in PRD 8.1 override the story text where they differ.
**Scale:** 7 agents, 255 tool calls, 67 minutes. The fix batch used 13 more agents.

**Previous report:** the v0.15.2 verification, still in git history.

## Summary

The first pass had no FAIL. Its 82 verdicts across the lenses were 45 PASS and 37 PARTIAL, most of them missing tests. One fix batch followed. Verdicts after that batch:

| Requirement | Verdict | Main reason |
|-------------|---------|-------------|
| US-001 generate | PASS | Created date was untested; now tested at store and row level. |
| US-002 protocol login | PASS | Real /caldav/ and /carddav/ route test added. |
| US-003 no web access | PASS | One-compare test added on the web path. |
| US-004 revoke | PASS | Revoking now also ends open sessions (decision 20). |
| US-005 what is in use | PASS | Created date as US-001. |
| US-006 Apple profile | PASS by operator acceptance | Server side verified. Not checked on a device (decision 24). |
| US-007 QR link | PASS by operator acceptance | Server side verified. POST on iOS Safari and prefetch not checked on a device (decision 24). |
| US-008 other clients | PASS | Page values now compared row by row with a non-default fixture. |
| US-009 switch | PASS | Finish no longer shown without 2FA. Per-protocol rows added. |
| US-010 admin revoke | PASS | Open sessions end; route added to the CSRF table. |
| FR-2 and the no-plaintext metric | PASS | Swapped-fields log line fixed (decision 23). |
| FR-8 and Goal 3 | PASS | Guess limit reset fixed (decision 23). |
| Other FRs, decisions 5 to 19 | PASS | |

## Behaviour defects

Each was reproduced before any action was taken.

1. **A leaked app password lifted the guess limit on the account password.** `Limiter.Succeed` forgives (IP, account) failures after any success. Reproduced: 400 wrong guesses from one IP with no throttle, then the right password accepted; the control locked at 5. **Fixed** in 36.14: an app password success forgives nothing, on all four protocols.
2. **A revoked app password left open sessions working.** A held SMTP session kept relaying, and IMAP and POP3 sessions survived, including after Revoke all. **Fixed** in 36.10. A session keeps the lookup ID and `AccountState` checks the record.
3. **Swapped user name and password fields logged the app password in plain text.** Reproduced on IMAP; POP3 and SMTP log the same field. **Fixed** in 36.14: `auth.LogUsername` replaces a name with that shape. A swapped account password is still logged; recorded in 8.2.
4. **Docs contradicted the switch.** `docs/caldav-carddav.md`, `docs/sendmail.md`, `docs/walkthrough.md` and `SECURITY.md` are corrected.
5. **Finish was shown to an account without 2FA** at `/settings/security/devices?setup=1`. The server refused the POST. **Fixed** in 36.11.
6. **The plain create form answered a throttled user in bare English text.** **Fixed** in 36.11. `reauthenticate` keeps this for change password, disable 2FA and switch-off; recorded in 8.2.

## Test gaps closed

All 16 are closed in 36.12: DAV routes, web comparison count, first-use label, a user with no records, writes through Raft, dummy hash cost, CSRF table rows and admin refusal, Add a device values and wiring, created date, wrong password with the revoke box, the lock in Redeem, the 5 minute TTL as a literal, no-store on refusals, per-protocol switch rows and decision 17 at session level, POP3 and DAV lockout, and the decision 16 copy.

The reviewers ran mutants through `go test -overlay`: 11 by the 36.10 worker and 6 more by its reviewer, and 23 by the 36.12 reviewer. All went red on assertion failures. Not re-run in review: the item 8 hard-coded port and host mutants, and the `PasswordHashCost` mutant for item 6. The password-change box cannot be proven alone for session ending, because a password change moves the epoch anyway.

## Sweep

A separate `sweep invariants` run was not done. Each batch ran its own mutants, the verifiers ran dozens more, and 36.12 closed the survivors.

## Not proved

- Real-device install, POST on iOS Safari, iOS prefetch, and the Apple password key names. The operator accepted these without a device check (decision 24).
- CSRF is proven in a test agent in the server role, not on a deployed server.
- `govulncheck` was not run in the verification pass.
