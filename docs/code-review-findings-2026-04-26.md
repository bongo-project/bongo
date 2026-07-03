# Code Review Findings

Date: 2026-04-26

Repository: `bongo-go`

Scope: full package test run, targeted race-detector run, static review of web, store, auth, queue, and server lifecycle paths

Test commands run:

```sh
go test ./...
go test -race ./internal/web/... ./internal/cluster ./internal/agent ./internal/store
```

Result: all of the above passed.

## Executive Summary

This codebase has real engineering depth. The package structure is coherent, the storage model is intentional, and the test surface is much better than average for a project of this size.

The main risks are systemic rather than incidental:

- best-effort write handling still needs continued discipline to avoid silent drift returning
- cross-user or queue-plus-mailbox workflows still cannot be made fully transactional without a larger composite-command design
- the remaining risks are now mostly backend consistency and explicit mail-delivery semantics rather than frontend CSP debt

The result is a codebase that is structurally strong but still exposed to a handful of high-impact operational and security failures.

## Status Update

After the remediation pass, the original critical exposures are no longer present.

Fixed in code:

- anonymous access to `/internal/*` routes
- session-secret logging and non-dev secret auto-generation
- recovery-code fail-open login behavior
- implicit cookie `SameSite` policy
- duplicated and overly trusting client IP extraction
- missing HTTP server timeout hardening
- several ignored persistence failures in auth/mail/compose/admin paths
- mailbox delivery now uses one explicit synthetic delivery primitive across web, IMAP append, inbound pipeline delivery, imports, and welcome mail
- implicit FSM side effects from plain `/user/{id}/mail/{guid}` writes have been removed
- mailbox synthetic commands now commit via Pebble batches rather than a series of separate synced writes

Partially improved but still worth follow-up:

- multi-recipient send is much stricter now, but a cross-user send plus outbound queue enqueue is still not one transactional Raft unit
- same-origin enforcement is now in place, but there is not yet a full token-based CSRF system

Still open:

- whether to introduce broader composite commands for cross-user send plus outbound queue workflows

## Findings

### 1. Fixed: unauthenticated `/internal/*` control endpoints were exposed on the public web router

Evidence:

- route registration before auth middleware in `internal/agent/agent.go:286-326`
- additional queue control routes in `internal/agent/agent.go:828-832`
- no auth checks in:
  - `internal/web/handlers/kv.go`
  - `internal/web/handlers/user.go`
  - `internal/web/handlers/join.go`
  - `internal/web/handlers/blob.go`
  - `internal/web/handlers/queue.go`
  - `internal/web/handlers/cluster.go`

Impact:

- remote creation of users via `/internal/user/create`
- remote Raft key read/write/delete via `/internal/kv/*`
- remote queue inspection and manipulation via `/internal/queue/*`
- remote cluster membership disclosure via `/internal/cluster/members`
- remote cluster join attempts via `/internal/cluster/join`
- remote blob retrieval via `/internal/blob/{hash}`

Because the main server binds to `cfg.Server.BindAddr`, which defaults to `0.0.0.0`, these are not naturally loopback-only endpoints.

Implemented fix:

- `/internal/*` routes now require signed internal authentication
- cluster auto-join and blob-sync clients now sign internal requests

Remaining follow-up:

- moving internal control-plane routes onto a dedicated listener would still be stronger defense in depth

### 2. Fixed: session secret fallback was unsafe and the generated secret was logged

Evidence:

- fallback behavior in `internal/agent/agent.go:332-337`
- raw secret logged in `internal/agent/agent.go:337`

Details:

If `web.session_secret` is empty, the server generates a session secret for all roles, not just dev mode, despite the comment saying “Auto-generate in dev mode”. It then logs the generated secret in hex.

This secret signs:

- the session cookie in `internal/web/middleware/auth.go`
- the 2FA pending cookie in `internal/web/middleware/auth.go`

Anyone with log access can forge authenticated sessions.

Implemented fix:

- generated secrets are no longer logged
- auto-generation is now restricted to dev mode
- non-dev startup now fails if `web.session_secret` is unset

### 3. Fixed with follow-up: mailbox mutations and single-copy delivery now use explicit synthetic commands with batched Pebble commits

Evidence:

- synthetic mailbox operations now exist in `internal/store/mailbox.go` for append/copy/move/rename/delete/expunge

Details:

Historically, `AppendMessage` claimed it wrote the mailbox entry and bumped `UidNext` atomically, but it actually performed two separate `ApplyCommand` calls. Copy, move, rename, delete, and expunge also had multi-step partial-failure windows. Inbound delivery was even less consistent because some code paths relied on a plain `/user/{id}/mail/{guid}` write and an FSM side effect to place the message into a mailbox, while others manually wrote both pieces.

Implemented fix:

- `AppendMessage`, `CopyMessage`, `MoveMessage`, `RenameMailbox`, `DeleteMailbox`, and `Expunge` now use synthetic mailbox commands
- those synthetic commands now commit through Pebble batches instead of a series of separate synced writes
- a new synthetic single-copy delivery command now writes `MailMeta` plus mailbox state together
- compose send, draft save, import, IMAP append, inbound pipeline delivery, and admin welcome mail all route through the same explicit delivery primitive
- plain mail-meta writes no longer create mailbox entries as an implicit FSM side effect

Remaining follow-up:

- broader atomicity for cross-user send plus queue workflows would still require additional composite commands or a transactional redesign

### 4. Partially fixed: user-facing handlers ignored persistence failures and could report success after partial writes

Evidence:

- ignored writes in:
  - `internal/web/handlers/auth.go:206`
  - `internal/web/handlers/compose.go:340`
  - `internal/web/handlers/compose.go:470`
  - `internal/web/handlers/compose.go:545`
  - `internal/web/handlers/compose.go:559`
  - `internal/web/handlers/mail.go:747`
  - `internal/web/handlers/mail.go:761`
  - `internal/web/handlers/mail.go:1565`
  - `internal/web/handlers/admin.go:792`
  - `internal/web/handlers/admin.go:806`
- bulk actions dropping mailbox errors in `internal/web/handlers/mail.go:528-558`
- mailbox creation ignored during import in `internal/web/handlers/mail.go:630-632`

Details:

This is not one local bug. It is a recurring pattern:

- mutate state
- ignore `ApplyCommand` or mailbox error
- continue rendering success

This creates silent drift between:

- user-facing success responses
- mailbox metadata
- message entries
- secondary indexing state

Implemented fix:

- multiple previously ignored Raft/mailbox write failures now fail the request or log explicit warnings
- bulk mail actions no longer silently swallow mailbox errors
- import mailbox creation failure is no longer ignored
- single-message delete no longer ignores flagging/expunge failures

Remaining follow-up:

- the repo should continue treating ignored persistence results as a review smell
- some secondary-index and metadata-backfill behavior is still intentionally best-effort

### 5. Fixed: recovery-code login was fail-open if code consumption could not be persisted

Evidence:

- recovery code path in `internal/web/handlers/auth.go:185-218`
- ignored persistence result in `internal/web/handlers/auth.go:206`

Details:

If a recovery code matches, login completes even when the updated profile cannot be written back. That means a nominally one-time recovery code may remain reusable.

Implemented fix:

- recovery-code consumption now fails closed and does not complete login if persistence fails

### 6. Mostly fixed: compose and draft flows no longer save sender-visible state before the main downstream delivery work

Evidence:

- sender metadata stored before later failure points in `internal/web/handlers/compose.go:292-304`
- rate-limit rejection happens after sender-side persistence in `internal/web/handlers/compose.go:321-324`
- queue failure happens after sender-side persistence in `internal/web/handlers/compose.go:361-363`
- draft save ignores persistence result in `internal/web/handlers/compose.go:465-472`

Details:

For `Send`, the old flow persisted the sender’s message metadata and Sent-folder copy before rate limiting, local-recipient delivery, and outbound queue insertion. That meant a request could fail after the sender already saw a saved copy.

Implemented fix:

- external-recipient rate limiting now happens before sender-visible persistence
- draft save now goes through the synthetic single-copy delivery path
- local-recipient delivery and outbound queue insertion now fail the request instead of silently degrading into a warning
- the sender `Sent` copy is written only after the downstream delivery work succeeds

Remaining follow-up:

- a send involving multiple local recipients and/or external queue enqueue is still not one transactional Raft unit
- if stricter end-to-end send semantics are required, the flow still needs a composite-command redesign

### 7. Fixed: spoofable client IP handling was duplicated across auth and middleware

Evidence:

- `internal/web/middleware/ratelimit.go:105-124`
- `internal/web/handlers/auth.go:261-279`

Details:

Both code paths trust the first `X-Forwarded-For` value unconditionally. This is safe only behind a trusted proxy that overwrites the header. Otherwise attackers can spoof the client IP and bypass rate limits or poison logs.

Implemented fix:

- client IP extraction is now centralized
- forwarded headers are no longer blindly trusted; trust is limited to local/trusted peers

Remaining follow-up:

- if deployments need explicit configurable trusted proxy ranges, that should still be added

### 8. Partially fixed: authenticated mutating routes had no explicit CSRF protection and cookies relied on implicit browser defaults

Evidence:

- many mutating routes under cookie auth in `internal/agent/agent.go:422-595`
- session cookie without explicit `SameSite` in `internal/web/middleware/auth.go:61-68`
- pending 2FA cookie without explicit `SameSite` in `internal/web/middleware/auth.go:214-220`
- TOTP setup cookie without explicit `SameSite` in `internal/web/handlers/security.go:302-309`
- current tests explicitly prefer omitted `SameSite` in `internal/web/middleware/auth_test.go:21-22`
- no CSRF token or origin-check middleware found in the web stack

Details:

The code currently leans on modern browser defaults rather than explicit policy. That is not strong enough for an admin-capable cookie-authenticated app.

Implemented fix:

- auth-related cookies now set explicit `SameSite=Lax`
- cookie-authenticated mutating routes now enforce same-origin checks
- tests were updated to assert explicit cookie policy

Remaining follow-up:

- this is origin-based CSRF mitigation, not yet a full token-based CSRF system

### 9. Fixed: `Cache-Control: no-store` for HTML was added too late to be reliable

Evidence:

- `internal/web/middleware/security.go:74-86`

Details:

The middleware waits until `next.ServeHTTP` returns and only then decides whether to set `Cache-Control: no-store`. In a real `net/http` writer, headers may already be committed by then.

The current tests do not catch this because `httptest.ResponseRecorder` is permissive.

Implemented fix:

- security middleware now sets `Cache-Control: no-store` by default before the response is committed
- handlers that need different caching semantics, such as static assets, override it explicitly
- tests now cover the committed-header behavior directly

### 10. Fixed: the web server was started without standard HTTP timeout hardening

Evidence:

- `internal/agent/agent.go:605-608`
- `internal/agent/agent.go:1125-1128`
- redirect server launched via bare `http.ListenAndServe` in `internal/agent/agent.go:949`

Details:

The main `http.Server` instances are created with only `Addr` and `Handler`. No `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, or `IdleTimeout` are set.

Implemented fix:

- timeout values were added to the main HTTP servers
- timeout values were added to the HTTP-to-HTTPS redirect server

### 11. Fixed: CSP is hardened on both script and style paths

Evidence:

- `internal/web/middleware/security.go:57-66`
- specifically:
  - `script-src 'self'`
  - `style-src 'self'`

Details:

The script-side CSP is now materially tighter: inline script blocks and literal inline event handlers were removed, the app switched to Alpine's CSP-compatible runtime, and `unsafe-eval` / `unsafe-inline` were dropped from `script-src`.

The style-side hardening is also complete:

- template `style=` / `:style=` / `x-bind:style` usage was reduced from roughly `196` occurrences to `0`
- app-owned `x-show` usage was removed to avoid framework-driven inline display toggles
- app-owned direct `.style` writes were removed from `internal/web/static/js/bongo.js`
- remaining geometry needs were converted to generated external CSS classes in `internal/web/static/css/generated_geometry.css`

Recommended fix:

- maintain the new constraints during future UI work so inline styles and style-driven runtime mutations do not regress

## Recurring Themes

These are the patterns that repeated enough to justify process-level guidance:

1. Internal APIs are being treated as “internal by naming” instead of being technically isolated.
2. Multi-step Raft mutations often assume success across steps without transactional semantics.
3. Write failures are frequently ignored in user-facing paths.
4. Security decisions are sometimes delegated to defaults instead of being encoded explicitly.

## Good

- Strong package decomposition for a fairly ambitious system.
- Full `go test ./...` and targeted race-detector coverage pass cleanly.
- Clear storage model with source-of-truth vs derived-index separation.
- Good amount of supporting docs and sprint-level design intent.

## Bad

- Several high-impact issues come from repeated patterns, not one-off oversights.
- Some comments and tests overstate guarantees the implementation does not actually provide.
- Security-sensitive routes and cookies are not hardened to the level the product surface now needs.

## Ugly

- the remaining hard problems are mostly consistency-model questions rather than obvious coding bugs
- several mailbox/message multi-step Raft writes still do not have true atomicity
- the CSP cleanup is complete; the remaining meaningful risk is in backend semantics

## Remaining Fix Order

1. Decide whether expunge and other remaining mailbox multi-step writes need true atomicity or just documented partial-failure semantics.
2. Continue auditing remaining best-effort write paths and document intentional ones.
3. Consider moving internal control-plane routes to a dedicated private listener for stronger isolation.
