# Verification: Mail delivery and rules v0.14.0

**Date:** 2026-11-01
**Commit verified:** `a13cbbe` (main after Phase 28)
**PRD:** [done/prd-mail-delivery-v0.14.md](done/prd-mail-delivery-v0.14.md)
**Rigor:** adversarial. Two verifiers per requirement: one checked the implementation and edge cases, the other checked whether the tests would go red if the guarantee broke. A completeness critic ran over the whole result. All verifiers were read-only, and the mutation evidence comes from the sweep that follows Phase 29.
**Scale:** 33 agents, 686 tool calls, 11 minutes.

**Previous report:** the norman secure review of 2026-10-20 (v0.7.0). Every finding in it was fixed in Phase 18, and the report is still in git history.

## Summary

| Requirement | Verdict | Main reason |
|-------------|---------|-------------|
| US-001 postmaster@ | PARTIAL | Delivery to the owner's INBOX is not tested end to end. The startup report wiring is untested. |
| US-002 bounce copy | PARTIAL | The config-to-generator wiring is untested. An alias domain whose target is disabled drops the copy. |
| US-003 port 465 | FAIL | The admin DNS guide has no `_submissions._tcp` row. docs/install.md omits 465. Autoconfig advertises 465 when SMTP is disabled. |
| US-004 vacation | PARTIAL | The branch that answers a send-as identity found only in To/Cc is untested. Rule forward combined with vacation is untested. |
| US-005 multi-action rules | PARTIAL | The first-match migration runs only on a node that is leader at startup, and is untested at agent level. |
| US-006 match any | PASS | |
| US-007 MIME and header conditions | PARTIAL | No delivery-pipeline test uses these conditions. Engine and tester tests cover them. |
| US-008 queue tuning | PARTIAL | Agent wiring is untested, and so is the concurrency limit. No test shows that the attempt count no longer bounces. |
| US-009 disk full | PARTIAL | The gate wiring into SMTP, IMAP and DAV is untested. Removing any `SetStorageGate` call leaves the suite green. |
| US-010 DSN parameters | PARTIAL | The EnqueueLocal parameter copy and `delay_warning_after` wiring are untested. (The xtext finding was withdrawn, see item 8.) |
| US-012 domain aliasing | PARTIAL | No SMTP or integration test covers an alias domain. The alias refusals are untranslated. |
| US-014 Archive | PARTIAL | Swipe and `e` in a virtual view pick the verb from the URL mailbox, so an archived message gets 400 and the row stays dismissed. CSRF on the routes is untested. |
| US-015 Reply to List | PARTIAL | Reply-all does not strip the user's own address from the reply's own To. The reply-list route is untested. |
| US-017 bongo sendmail | PARTIAL | An authenticated RCPT to an unknown local part on a hosted domain is relayed (critic, below). docs/sendmail.md is wrong about the invocation name and the exit 67 example. |
| US-018 CLI lists | PARTIAL | A trailing-dot `--domain` returns an empty list. Mixed-case legacy domain keys can show the wrong state. Table rendering is only partly tested. |
| Functional and technical requirements (4, 6) | PARTIAL | i18n of the new admin refusals and the rule flashes is missing. CSRF on new routes is not tested through the production router. 6.4 cannot be checked from this repository. |

Totals: 1 PASS, 14 PARTIAL, 1 FAIL. Most PARTIAL verdicts are about the tests, not the behaviour: the code is right, but deleting the agent line that wires it in would leave every test green.

## Behaviour defects

1. **Authenticated RCPT to an unknown local part on a hosted domain is relayed** (critic, US-017, US-012). `internal/smtp/server.go` falls through to `addExternal` on any resolver miss other than `ErrRecipientUnavailable` when the session is authenticated, and no hosted-domain check comes first. The message is queued to our own MX, refused there with `550 5.7.1`, and bounces later. It also spends one slot of the sender's outbound recipient budget, and `bongo sendmail` exits 0 for a typo instead of 67. This was recorded in 8.2 as a v0.15 candidate. It is now Phase 29.
2. **The admin DNS guide has no `_submissions._tcp` 465 record** (US-003, the only FAIL). `internal/web/handlers/dns_guide.go` was not updated, although deploy/README.md calls it the guide operators copy from. docs/install.md does not list 465 among the ports to open.
3. **Autoconfig advertises 465 when SMTP is disabled** (US-003). The gate in agent.go checks only the port and the certificate.
4. **The rules first-match migration can be skipped** in a rolling upgrade (US-005). It runs only when the node is already leader at startup.
5. **Archive and unarchive in virtual views** (US-014). Swipe and `e` choose the verb from the URL mailbox, so an archived message in Starred gets `400 Already archived` and the swiped row is not reset.
6. **Reply-all keeps the user's own address** when it is in the reply's own To list, for example Reply-To or a reply to one's own sent message (US-015).
7. **Bounce copy on an alias domain whose target is disabled** goes to postmaster@ the alias. Nothing resolves it and it is dropped, where a disabled plain domain falls back to `smtp.postmaster_account` (US-002).
8. ~~**ENVID and ORCPT accept characters outside xtext**~~ (US-010). Withdrawn on 2026-11-01: go-smtp v0.24.0 refuses a raw `=` in these parameters and passes Bongo only decoded values, so `ENVID=a=b` already gets 501. The verifier reasoned from `decodeXtext` alone. Task 29.8 added tests that pin the behaviour.
9. **Untranslated user-visible text:** the CreateDomain alias refusals, the SetCatchAll refusal on an alias domain, and the "Rule created" and "Rule updated" flashes (section 4).
10. **CLI `--domain` with a trailing dot** silently lists nothing. Duplicate mixed-case legacy domain keys pick their state from an unstable sort (US-018).
11. **docs/sendmail.md** says any name ending in `sendmail` works (only the exact name does). Its exit 67 example is a reply Bongo never sends to an authenticated session.

## Test gaps (the code is right, the tests would not notice a break)

- Agent wiring with no test: `configureBouncePostmasterCopy`, `reportPostmasterTargets` at boot, the queue options (`max_lifetime`, `max_retry_interval`, `concurrency`, `delay_warning_after`), the storage gate into SMTP, IMAP, CalDAV and CardDAV, and the autoconfig effective ports.
- Production-router routes: CSRF on archive, unarchive, bulk archive, `/settings/vacation` and `alias_of` on `/admin/domains` is tested only by wrapping a handler in the middleware. The reply-list route is never requested through the router.
- Queue: no test shows that `concurrency` limits deliveries, or that a message with 10 or more attempts and a recent `created_at` is rescheduled rather than bounced.
- Vacation: the send-as identity in To/Cc branch, and a rule-requested forward combined with vacation.
- End to end: postmaster@ to the domain owner's INBOX, mail to an alias-domain address through SMTP, and multi-recipient DSN parameters through EnqueueLocal.
- CLI: table rendering of admin domains, disabled accounts, quotas, usage and DKIM.

## Accepted as built (no task)

- `queue.concurrency = 0` means the default of 5. It cannot be told apart from an unset key, and every config value in Bongo defaults this way.
- `abuse@` resolves through the postmaster tier (8.1 decision 7).
- IMAP `NO [UNAVAILABLE]` and DAV 507 bodies are English. The criterion requires translation only in the web UI.
- `validDomainName` accepts a trailing dot. This predates v0.14 and applies to every domain.
- A message/rfc822 attachment is one MIME leaf. This is documented in `rules/parse.go`.
- Input to `bongo sendmail` with no header block is refused with 65.
- `RuleStore.Save` does not enforce 1 to 10 actions. The form and the delivery-time cap do.
- The bongo.js swipe and shortcut are checked by source matching only. The repository has no JS test runner.
- 6.4 (publish 465 in the infra repository, check `min_free_mb` against production free space) is outside this repository and goes with the v0.14 infra handoff.

## Gaps

All defects and test gaps above are Phase 29 in [TASKS.md](TASKS.md). The mutation sweep over Phase 28 and Phase 29 runs after Phase 29.
