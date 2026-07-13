# Verification — Rich Signatures & HTML Composer

Date: 2026-07-13
Mode: `norman verify`, rigor: **adversarial** (3 lens verifiers per requirement — implementation-exists / tests-exercise-it / edge-cases — majority-combined, then a completeness critic over the full set). 34 verifier agents.

## Reconciled results (critic downgrades applied)

| ID | Requirement | Majority vote | Final |
|----|-------------|---------------|-------|
| US-001 | HTML signature editor in Settings | PASS | **PASS** |
| US-002 | Plain-text fallback preserved | PASS | **PARTIAL** (critic) |
| US-003 | Rich compose with plain/rich toggle | PASS | **PASS** |
| US-004 | Inline logo via cid: | PASS | **PASS** |
| US-005 | Received mail renders (integration) | PASS | **PARTIAL** (critic) |
| FR-3 | Server-side HTML sanitizer | PASS | **PASS** |
| FR-5 | MIME builder tree | PASS | **PASS** |
| FR-6 | data:->cid: extraction with stable IDs | PASS | **PASS** |
| FR-8 | Plain/rich toggle backward-compat | PASS | **PASS** |
| FR-10 | i18n + a11y for editor | PASS | **PASS** |
| SEC-caps | Server-side inline image size caps | PASS | **PASS** |

**9 PASS, 2 PARTIAL.** All 11 requirements are *implemented* and confirmed by reading the code; the two PARTIALs are test-coverage gaps, not defects. An adversarial XSS probe against the sanitizer (mixed-case `<SCRIPT>`, nested `<scr<script>ipt>`, `<img onerror>`, `javascript:` href) found no injection leak.

## Gaps (test coverage only — implementation is correct)

1. **US-002 — plain fallback with an *empty* signature.** The `plain_send_single_part` integration subtest runs with a non-empty HTML signature already set (the inverse of the criterion's precondition), and no test asserts the `text/plain` body *ends with the plain signature* when `signature_html` is empty and a plain `Signature` is set.
   - *Fix:* add a case: user with empty `signature_html` + plain `Signature="Regards, Jane"`, plain send → single `text/plain` part ending with `\n\n-- \nRegards, Jane`, no `text/html`, no `multipart/alternative`.

2. **US-005 — inline logo in the *signature*.** Every test puts the `data:` image in the message body; none embeds it in the HTML signature and asserts it becomes a `cid:` inline part on the received message.
   - *Fix:* set an HTML signature containing a `data:` logo, send rich, assert the logo is rewritten to `cid:` with a matching inline part (Content-ID + Content-Disposition: inline) on the sent message.

Both are addressable with small additions to `test/integration/rich_compose_test.go` (and/or `compose_rich_send_test.go`). Tracked as task **2.12**.

## Evidence highlights

- US-001: `preferences.go:117-120` (sanitize + derive), `preferences_signature_test.go` (Plain/ClearEmpty/ScriptStripped all pass), `richeditor.test.mjs` source-toggle round-trip (22/22).
- FR-3: `internal/web/sanitize` bluemonday blank policy; `sanitize_test.go` 5 cases; adversarial probe clean.
- FR-5/FR-6: `compose_mime.go` `buildMIME`/`extractDataURIImages`; `compose_mime_test.go` tree + dedup + sha256 Content-IDs.
- SEC-caps: `checkInlineImageCaps` (1MB/10MB) → 400 before writes; `compose_rich_send_test.go`.
- FR-10: 11 `editor.*`/`compose.bodyMode*` keys × 4 locales; `TestI18nCoverage` parity; `role=textbox` in `richeditor.templ`.
