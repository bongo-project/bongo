# Tasks

Source PRD: security review follow-ups.

## Phase 1: Security follow-ups — 10 tasks completed 2026-07-03 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 2: Rich Signatures & HTML Composer — 12 tasks completed 2026-07-20 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 3: Multi-Domain — Foundation & Quick Wins (PRD Phase A) — 3 tasks completed 2026-07-18 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 4: Multi-Domain — Receiving (PRD Phase B) — 5 tasks completed 2026-07-18 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 5: Multi-Domain — Sending Identity (PRD Phase C) — 2 tasks completed 2026-07-18 (see [TASKS-archive.md](TASKS-archive.md))

## Phase 6: Multi-Domain — Tenancy & Isolation (PRD Phase D) — 4 tasks completed 2026-07-19 (see [TASKS-archive.md](TASKS-archive.md))

---

**Multi-domain multi-tenant hosting epic (PRD Phases A–D / norman Phases 3–6): COMPLETE + DEPLOYED as v0.6.0 (2026-07-19).**
PRD in [done/prd-multidomain-hosting.md](done/prd-multidomain-hosting.md).

## Phase 7: Admin UX follow-ups

Post-epic UI improvements (user-requested). Docs get a dedicated sprint later.

| # | Task | Status | Notes |
|---|------|--------|-------|
| 7.1 | Per-domain detail sub-screen + DNS status: move all per-domain settings off the overflowing 9-col table onto a `/admin/domains/{domain}` page; list = Domain·Status·MX·Manage; MX-not-configured indicator (extensible to SPF/DKIM/DMARC); respect 6.2 isolation on the new route | DONE (2026-07-19) | ShowDomain (requireDomainAccess + 404-on-unknown); AdminDomainDetail stacked sections, DKIM prominent; list slimmed to 4 cols + Manage; MX ✓/✗; adversarial 403 test; I fixed 2 MINORs (u-mb-025 class, DeleteDomain→hxRedirect for hx-post delete) |
| 7.3 | Domain-owner DNS setup guide: server-generated per-domain DNS records (A/AAAA, MX→shared SMTP.Hostname, SPF, DKIM real key, DMARC, autodiscover/autoconfig/imaps/submission/caldavs/carddavs SRV) on the Manage page as a copyable table + downloadable BIND zone at `/admin/domains/{domain}/dns.zone`. Reuse the same expected values as the 7.2 status checks so guide and checks never drift. Domain-admins see it for their domains (respect 6.2 isolation). Empty-p= DKIM shows a "generate key first" notice, never a broken record. | DONE (2026-07-19) | dns_guide.go pure buildDNSGuide + dnsGuideZone (14 unit tests); resolveServerIPs from mailHostname; DownloadDNSZone route w/ requireDomainAccess + adversarial 403 + attachment test; DNSGuideRow type + Manage-page table w/ per-row copy (reuses data-copy-previous) + zone download; DKIM-pending notice; 10 i18n keys x4 locales; -race green |

## Backlog (unscheduled)

| # | Task | Status | Notes |
|---|------|--------|-------|
| B1 | De-brand the repository: replace all deployment-specific domain references (167 across ~25 files — deploy/ configs incl. bongo.prod.hcl + node name, .env.example, autoconfig comment, docs/, prds/ logs, tests) with generic placeholders (`example.com`, `mail.example.com`, node `bongo-1`) driven by config/env, so the repo mentions only `bongo`/`bongo-go`. | DONE (2026-07-20) | Working-tree scrub done: only 1 runtime match (a comment), rest configs/docs/tests; edge cases handled (reversed-domain assertion, case-insensitivity variant, ssh-key path); build + full `-race` suites green. Live `/mnt/data/bongo` config keeps real values (not in git). History also purged: git filter-branch rewrote all commits + tags (backup bundle kept), force-pushed main + tags to origin — zero legacy-domain references across every ref. |
| 7.2 | DNS config status checks (MX/SPF/DKIM/DMARC) on the domain detail page, comparing ACTUAL DNS vs EXPECTED-for-this-server. FIX MX bug: mark ✓ only if an MX host matches the server mail hostname (SMTP.Hostname), not just any-MX-exists (example.net points to OVH but shows valid). | DONE (2026-07-19) | buildDNSChecks pure fn (4-state ok/fail/neutral/error), concurrent timed lookups; MX now points-to-us; SPF authorises-server; DKIM published p== ours; DMARC v=DMARC1; list MX col uses fixed check; I fixed 2 MINORs (hostname fallback, gofmt) |
