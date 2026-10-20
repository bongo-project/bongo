# Security review: norman secure

**Date:** 2026-10-20
**Commit reviewed:** `78b6477` (v0.7.0)
**Scope:** both tracks, full.
- **Track A:** 7 done PRDs, up to 8 security requirements each. Each requirement was checked through 3 verifier lenses, then mutated, then reviewed by a completeness critic.
- **Track B:** 8 threat areas. Each finding went to 3 skeptics, followed by a coverage critic.

**Scale:** 327 agents, about 3,000 tool calls, 32 minutes.

**Previous report:** the Phase 10 verification, 2026-08-16. It is still in git history.

## Summary

The reviewers confirmed 46 of 47 Track B findings. That rate is the expected skeptic over-agreement, so a finding is real only once a failing test reproduces it.

The orchestrator re-read two of them against the code and both are real: the 2FA-pending cookie (H3) and the address claim (H1). Several findings were reported by more than one auditor, so the table below deduplicates them into 30 distinct issues.

In Track A, 54 requirements were verified. 30 passed, 16 were PARTIAL and 8 failed. Five PASS guarantees survived a mutation and were downgraded to PARTIAL. 22 qualifying requirements fell beyond the per-PRD cap and were not verified.

Most of the serious issues come from two root causes:
1. **Identity binding of outbound and inbound addresses.** Unverified address records route mail. The From header is never checked against the authenticated user. The DKIM key is chosen by the From domain.
2. **Domain-admin scope checks look only at the target's domain.** They never check the target's role, or the domain of the second object in a request.

## Confirmed findings (deduplicated, ranked)

| # | Area | Sev | Finding | Where | Scenario |
|---|---|---|---|---|---|
| H1 | authz / abuse | HIGH | Any user can claim any address through self-serve send-as. The recipient resolver then delivers that address's mail to them. | `internal/web/handlers/address_verify.go:155-165`, `internal/store/recipient_resolver.go:68-75` | POST /settings/addresses with `address=ceo@otherTenant` or `boss@gmail.com` writes an unverified AddressRecord. Resolver tier 1 ignores `Verified`, so inbound MX mail and web-compose "local" sends to that address land in the attacker's INBOX. Accounts created through `auth.CreateUser` and aliases created through `CreateAlias` never write `/system/address/` (only boot-time `migrateAddressIndex` does), so every account or alias created since the last restart can be claimed. |
| H2 | authz | HIGH | A domain admin can reset the password of, disable or delete a global admin, or another domain's admin, whose primary address is in their domain. | `internal/web/handlers/admin_scope.go:42-54`, `admin.go:467-497` | `requireUserAccess` compares only the target's domain. Resetting `globaladmin@d1` as the d1 domain admin returned 303 with the temporary password in the URL, so the domain admin can log in as global admin. ToggleDisabled, UpdateUser and DeleteUser have the same gap. |
| H3 | authn | HIGH | The 2FA-pending cookie is accepted as a full session cookie. | `internal/web/middleware/auth.go:29,219-253,256-280` | Both cookies are signed with the same purpose key, and PendingData's JSON parses as SessionData with no epoch, which RequireAuth then skips checking. Anyone with the password but no TOTP copies `bongo_2fa_pending` into `bongo_session` and is authenticated (admin flag included) for 5 minutes, as often as they like. The TOTP-setup cookie reuses the same purpose (`handlers/security.go:374`). Reproduced with an overlay test. |
| H4 | lifecycle / authn | HIGH | Deleting an account does not revoke its web sessions. | `internal/web/middleware/auth.go:146-160`, `internal/agent/agent.go:207` | The live-state lookup returns `Found=false` for a deleted profile, and RequireAuth fails open on that. A deleted user, including a deleted global admin, keeps the cookie's rights until it expires (up to 30 days with "remember"). |
| H5 | abuse / authz | HIGH | SMTP submission does not check the header From, and the queue DKIM-signs with whichever hosted domain key matches the From domain. | `internal/smtp/server.go:336-345`, `internal/queue/worker.go:213-216`, `internal/mail/dkim_registry.go:66` | An authenticated user on tenant A submits on 587 with envelope sender = own address and `From: ceo@tenantB`. The mail leaves DKIM-signed `d=tenantB`. Trusted submissions also skip DMARC (`pipeline/antispam/builtin.go:325`), so local delivery accepts the spoof too. This is also Track A US-SEND-1, US-SEND-2 and US-DKIM-1 (FAIL). |
| H6 | abuse | HIGH | Forwarding re-sends untrusted inbound mail through the outbound queue, which DKIM-signs it as any hosted domain named in its From. | `internal/pipeline/deliver/deliver.go:492-493` | An outsider sends `From: ceo@hostedDomain` to a user who has forwarding set. The forwarded copy goes out with a valid hosted-domain DKIM signature. That launders spoofed mail into a passing DKIM signature. |
| M1 | input | MEDIUM | Unauthenticated MIME-nesting CPU bomb. | `internal/pipeline/antispam/builtin.go:204` (plus `rules.go:182`, `deliver.go:233`, `cluster/mailbody.go:46`, web/IMAP views) | Unclosed nested multipart boundaries double enmime's parse time per level: depth 22 took 1.3s on a 1.25KB message, so depth 30 runs to minutes. There is no depth cap and no per-message timeout, with only 5 pipeline workers. A few small inbound messages stall delivery for everyone, and they are re-parsed on every view. Auditors rated it HIGH; it is graded MEDIUM here because it is DoS. |
| M2 | authz / exposure | MEDIUM | Domain-admin cross-tenant writes and reads through the second object in a request. | `admin.go:1645` CreateAlias, `:1324` SetCatchAll, `:1454` GrantDomainAdmin, `:1639`/`:2442` ListAliases | A d1 admin can point a d1 alias or catch-all at a d2 user and grant a d2 user d1 admin. The alias page renders every username on the server (`loadUserEmails` is unfiltered). SetCatchAll's 400 on an unknown user reveals which user IDs exist. |
| M3 | lifecycle / authz | MEDIUM | DeleteDomain does not cascade. | `admin.go:1201-1229`, `middleware/admin_scope.go:71-73` | Domain-admin grants, catch-all, aliases, address records and the DKIM key all survive. If the domain is re-created for a new tenant, the old domain admins regain control and the old catch-all owner receives its mail. |
| M4 | config | MEDIUM | The Traefik `bongo-autoconfig-sub` router has no path restriction or deny-internal middleware. | `deploy/docker-compose.yml:135-140`, `internal/security/internal_http_auth.go:155` | `https://autoconfig.<domain>/internal/...` reaches the control plane from the internet. HMAC still gates it, but `VerifyInternalRequest` runs `io.ReadAll` on the body before checking the signature, so an unauthenticated client can stream large bodies and OOM the 512MB container. Found by the coverage critic; not reproduced live. |
| M5 | config | MEDIUM | The CSRF middleware's form parse bypasses upload body caps. | `internal/web/middleware/csrf.go:91` | `ParseMultipartForm` runs before each handler's MaxBytesReader, so any authenticated user can OOM-kill the container with one large upload. |
| M6 | authn | MEDIUM | Per-IP auth throttles are reset by any successful login from the same IP. | `internal/authlimit/authlimit.go:103` | An attacker logs into their own account between guesses, which gives unlimited password and TOTP guessing against other accounts from one IP. |
| M7 | lifecycle | MEDIUM | Existing IMAP sessions survive account disable, password reset and deletion. | `internal/imap/backend.go:207` | The user binding happens once at LOGIN and is never re-checked. |
| M8 | abuse | MEDIUM | Forwarding is unverified and unthrottled, and it also forwards spam-tagged mail. | `internal/web/handlers/preferences.go:136` | Any user can set a forward to any external address, which turns the server into an amplifier and damages the sending IP's reputation. |
| M9 | abuse | MEDIUM | No connection cap on the IMAP/IMAPS listeners, global or per IP. | `internal/imap/server.go:89` | Connection exhaustion. |
| M10 | abuse | MEDIUM | IMAP APPEND bypasses the domain storage quota. | `internal/imap/backend.go:503` | Unbounded storage by any user. |
| M11 | abuse | MEDIUM | Bayes training does one serial Raft write per token, with an uncapped Subject. | `internal/antispam/bayes.go:82` | One click on a crafted message can generate tens of thousands of Raft writes. |
| M12 | config | MEDIUM | The Raft transport is reachable from any container on the shared Traefik network in the shipped single-node deployment. | `internal/cluster/raft.go:83` | SECURITY.md says not to expose 8300, but the compose network is shared. Auditors rated it HIGH; graded MEDIUM because it needs a co-tenant container. |
| M13 | config | MEDIUM | The container keeps CHOWN, DAC_OVERRIDE, FOWNER, SETUID and SETGID, and has `no-new-privileges` removed. | `deploy/docker-compose.yml:39-56` | This is Track A security-followups T5 (FAIL). `sysctls net.ipv4.ip_unprivileged_port_start=0` with `user: 10001` would allow binding the low ports while keeping NNP. |
| L1 | authn / authz | LOW | The admin and quarantine route groups skip Require2FA, so a system-wide `require_2fa` policy does not cover the admin console. | `internal/agent/agent.go:876` | |
| L2 | authn | LOW | Protocol logins enforce disable only through the derived SQLite `users.enabled` flag. An index rebuild or snapshot restore re-enables disabled accounts for IMAP, SMTP, POP3 and DAV. | `internal/auth/local.go:84`, `internal/cluster/fsm_rebuild.go:68` | |
| L3 | authn | LOW | Re-auth for disabling 2FA and changing the password is password-only and unthrottled. TOTP codes are replayable, and logout does not revoke the session server-side. | `internal/web/handlers/security.go:170` | |
| L4 | abuse / config | LOW | PROXY protocol and X-Forwarded-For are trusted from any RFC1918 peer, so a co-tenant container can forge client IPs, defeating lockout, rate limits and DNSBL. | `internal/netx/proxyproto.go:39-51` | |
| L5 | abuse | LOW | The outbound submission limit counts messages, not recipients. The send-as verification mailer can target unlimited addresses. | `internal/smtp/server.go:457` | |
| L6 | lifecycle | LOW | Legacy aliases still point at a deleted user's address, so a new account reusing it receives them. A catch-all whose owner was deleted keeps accepting the domain's unmatched mail and silently drops it. | `admin.go:522`, `store/recipient_resolver.go:115-120` | |
| L7 | exposure | LOW | The send-as page reveals whether any address on any tenant is owned. | `address_verify.go:152` | |
| L8 | exposure | LOW | SMTP DATA-phase internal errors are echoed verbatim to the client as a permanent 554. | `internal/smtp/server.go:486` | |
| L9 | exposure | LOW | The admin password-reset temporary password is placed in the redirect URL query string, so it ends up in logs and history. | `admin.go:496` | |
| L10 | exposure | LOW | `/mail/blob/{hash}` sends private attachments with `Cache-Control: public, max-age=31536000, immutable`. | `internal/web/handlers/mail.go:1240` | |
| L11 | exposure | LOW | Message subjects are logged at INFO level. | `internal/imap/backend.go:608` | |
| L12 | authz | LOW | Free/busy for another user scans the whole `/user/` keyspace on every request. | `internal/store/calendars.go:169` | |
| L13 | config | LOW | Supply chain issues: the `alpine:3.20` runtime image is end-of-life, and the build and runtime bases are mutable tags. The publish workflow uses mutable action tags with `packages:write`, and CI has no `permissions` block. | `deploy/Dockerfile:41`, `.github/workflows/publish-image.yml:26` | |
| L14 | config | LOW | certs-dumper writes every Traefik-managed domain's private key into bongo's `/certs`, which the entrypoint chowns to bongo. | `deploy/docker-compose.yml:188` | |
| L15 | exposure | LOW | Inline parts that have a filename are served by `/mail/blob/{hash}` as raw encoded spans, without reconstruction. ServeBlob on a split message's raw hash returns the skeleton. | `mail.go:595-613,1225` | This is a correctness issue with a privacy-neutral impact (Track A attachment-dedup FR-5). |

## Rejected by the skeptics

- **The case-insensitive login fallback stores the raw typed login string, including CRLF, as the session email.** The mechanism is real, but From goes through `SanitizeHeaderValue` and go-smtp rejects a CRLF envelope sender, so it only affects the user's own session. Canonicalising the session email after `Authenticate` is still worth doing as hardening.

## Track A: requirement verdicts

| Requirement | Verdict | Notes |
|---|---|---|
| multidomain US-TENANT-2 (domain admin isolated to own domain) | FAIL | H2, M2, M3 |
| multidomain FR-9 (admin authz scoped, default-deny) | FAIL | H2, M2 |
| multidomain US-SEND-1 (send-as only verified, owned addresses) | FAIL | Enforced only in the web compose picker; SMTP submission does not check (H5) |
| multidomain US-SEND-2 (send-capable only by grant or proof) | FAIL | H1, H5 |
| multidomain US-DKIM-1 (never sign with another domain's key) | FAIL | H5, H6 |
| multidomain US-DKIM-2 (DKIM private key never logged or displayed) | PARTIAL | Holds in code. **Mutation survived**: logging the key in `LoadStoredDKIMKeys`, appending it to the TXT record, or putting it in the redirect URL all leave tests green. Also, a mixed-case domain in the URL rotates a separate key. |
| multidomain US-RECV-1 (RCPT accepts only resolvable addresses) | PARTIAL | Tier 1 ignores user and domain enabled state (and H1) |
| multidomain US-RECV-3 (catch-all never overrides an explicit address) | PARTIAL | Catch-all owner existence and enabled state are not checked (L6) |
| outbound FR-12 (strip inbound Authentication-Results / X-Spam-*) | PASS | |
| outbound FR-14 (View Original authenticated, owner-scoped) | PASS | |
| outbound FR-15 (raw source HTML-escaped) | PASS | |
| outbound FR-9 (nothing skips antispam) | PASS | |
| outbound US-003 (null envelope sender on DSNs) | PASS | |
| outbound non-goal: no local-origin antispam bypass | PASS | |
| outbound non-goal: no admin view of other users' source | PARTIAL | Holds in code. **Mutation survived**: no test runs ViewSource with an admin context against another user's UID. |
| outbound constraint: raw content and DKIM keys never logged | PARTIAL | The DKIM PEM survives domain deletion in Pebble and the Raft log (M3) |
| blob-gc US-001 (reference set covers every blob-bearing record) | PASS | |
| blob-gc US-002 (an unparseable record fails the sweep) | PASS | |
| blob-gc FR-10 (structural mail-key parse) | PASS | |
| blob-gc US-006 (re-validate before delete) | PARTIAL | See the attachment-dedup US-009 window below |
| blob-gc US-007 (two-pass grace) | PASS | |
| blob-gc FR-3 (fail closed on reference error) | PARTIAL | |
| blob-gc US-005 (dryrun confirmation before enabling) | PARTIAL | |
| blob-gc FR-11 (log each deleted hash at INFO) | PASS | |
| dnsbl FR-9 (never rejects on its own) | PARTIAL | |
| dnsbl FR-5, FR-4, FR-6, FR-7, US-001, US-004 | PASS | |
| trusted-submission FR-2, US-001 (both), US-002 (both), US-003 (clamd unreachable), US-004 | PASS | |
| trusted-submission US-003 (EICAR blocked) | PARTIAL | |
| security-followups T1 (MX per-IP rate limit), T4 (blob hash validation), T6 (relay AUTH requires STARTTLS), T7 (max_message_size) | PASS | |
| security-followups T2 (CRLF header hardening) | PARTIAL | |
| security-followups T3 (fail loudly instead of an ephemeral TLS cert) | PARTIAL | |
| security-followups T5 (non-root, only NET_BIND_SERVICE, NNP kept) | FAIL | M13 |
| security-followups T8 (per-purpose key derivation) | FAIL | H3: the pending and session cookies share a purpose, and blob sync shares the control-plane subkey |
| attachment-dedup US-003 (byte-exact reconstruction preserves DKIM), US-005 (stored once) | PASS | |
| attachment-dedup US-007 (ingest self-check) | PARTIAL | |
| attachment-dedup US-009 (GC keeps shared attachments) | PARTIAL | Window: `StoreSplit` writes attachment blobs before MailMeta commits, and nothing references an existing deduped attachment H during that window. If GC's re-validation runs then, H is deleted and the new message points at a missing blob. |
| attachment-dedup US-008 (outbound uses the split model, Sent is byte-exact) | FAIL | Sent holds the unsigned bytes. IMAP APPEND stores the payload twice; SMTP submission does not split. Correctness and storage, not security. |
| attachment-dedup partial-failure write ordering | PARTIAL | Same window as US-009 |
| attachment-dedup FR-5 (single FullRawMessage read path) | PARTIAL | L15 |

Two further mutation survivals did not change a verdict and are listed under US-DKIM-2 and the admin-view non-goal. Full per-lens evidence is in the workflow journal, `wf_95879acd-183`.

## Critic gaps and uncovered areas

These hold user data but no auditor examined them. They are candidates for a narrower follow-up review:
- Bulk mail actions (`mail.go` BulkTagAction, BulkAction, UpdateMessage). Scoping was spot-checked and is correct, but there is no cap on the length of `MessageIDs` or `Tags`.
- IMAP FETCH, SEARCH and Bleve query cost, and EXPUNGE sequence handling.
- CalDAV/CardDAV write paths: iCal/vCard parser robustness, the PUT quota gap, ETag handling.
- Shared-calendar mutation: whether a read-only sharee can edit or delete the owner's events.
- The importer's remote IMAP credential handling (logging, TLS verification).
- `internal/agent/grpc` DepositMessage auth. It is not started in the server role.
- Domain-admin quarantine scoping and the `/admin/quarantine/bulk` spool-id handling.

## Out of scope or capped

- 22 qualifying requirements beyond the cap of 8 per PRD were not verified:
  - trusted-submission 5
  - dnsbl 5
  - blob-gc 4
  - multidomain 3
  - outbound 2
  - attachment-dedup 2
  - security-followups 1
- Rich-signatures PRD: pure UI, not selected.
- Multi-node cluster weaknesses already listed in SECURITY.md. M12 is reported only because it is reachable in the single-node compose deployment.
- No live or production testing. All reproductions used overlay or scratch tests.
