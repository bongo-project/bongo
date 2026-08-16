# Verification: Phase 10 — Outbound Deliverability and View Original

**Date:** 2026-08-16
**PRD:** [prd-outbound-deliverability-and-view-original.md](active/prd-outbound-deliverability-and-view-original.md)
**Rigor:** `adversarial` — 3 independent lenses per requirement (implementation-exists /
tests-actually-exercise-it / edge-cases), majority-combined, then a completeness critic over
the full verdict set.
**Scale:** 19 verifier agents, 541 tool calls.
**Deployed as:** v0.6.13 (commit `e21b970`), live and healthy.

## Result

| Requirement | Lenses (exists / tested / edge) | Combined | After critic |
|---|---|---|---|
| US-006 Fully-qualified HELO (FR-19..22) | PASS / PASS / PASS | PASS | **PASS** |
| US-001 DKIM-signed bounces (FR-1,2,9) | PASS / PASS / PASS | PASS | **PARTIAL** |
| US-002 RFC 3464/3834 DSN headers (FR-4..8) | PASS / PASS / PASS | PASS | **PASS** |
| US-003 Null envelope sender (FR-3) | PASS / PASS / PASS | PASS | **PASS** |
| US-004 Trace headers (FR-10..13) | PASS / PARTIAL / PASS | PASS | **PARTIAL** |
| US-005 View Original (FR-14..18) | PASS / PARTIAL / PASS | PASS | **PASS** |

**4 PASS, 2 PARTIAL, 0 FAIL** after applying the critic's downgrades.

Note the pattern: the per-requirement sweep returned six PASSes. Every downgrade came from the
completeness critic — the stage that looks across requirements rather than at one in isolation.
Two of the three gaps are cross-requirement interactions that no single-requirement verifier
could have seen.

## Gap 1 — FR-12 and FR-13 are in direct tension, and FR-12 silently won (US-004 -> PARTIAL)

**The criterion:** "Trace headers are prepended above existing headers and do not invalidate a
pre-existing DKIM signature: `antispam.VerifyDKIM` still returns `pass` for a message that
verified before the headers were added."

**What is actually true:** FR-12 strips inbound `Authentication-Results` and `X-Spam-*` headers
unconditionally. If a sender oversigned — listed `x-spam-status` in their DKIM `h=` tag — the
stored copy no longer verifies. `stripTraceHeaders` in `internal/pipeline/deliver/headers.go`
documents this as deliberate. The two requirements cannot both hold, FR-12 was chosen, and the
PRD was never amended to say so.

**Why it matters beyond bookkeeping:** IMAP, POP3 and the Export route all serve the stamped
copy, so a message that authenticated on arrival can fail DKIM in the user's own mail client.
US-004 exists to put evidence of authenticity into the stored message; for oversigned senders it
partly undermines that.

**Why it is PARTIAL and not FAIL:** the ordinary case — a sender who did not oversign — almost
certainly still verifies, because stamping only prepends and never touches signed headers.
Nothing proves it. `TestDeliverDKIMSignatureCoveringStrippedHeaderStillVerifiesArrivalTime`
(`trace_headers_test.go:242`) tests the *opposite* case and asserts only the arrival-time verdict
on `meta.DkimResult`. No test calls `VerifyDKIM` on the stored bytes after stamping.

**The machinery to prove it already exists** in the same file: `signedTestMessage` + `stubDKIMPass`
+ the exported `antispam.LookupTXT` hook.

## Gap 2 — 10.5 doubles steady-state storage on a default deployment (US-004 -> PARTIAL)

Every delivered message now writes a second blob (the stamped copy) and orphans the DATA-time
blob. The code comment says the original is "reclaimed by the normal blob GC" — but `BlobGCMode()`
defaults to **off** (`internal/config/config.go:84`), and the only `blobs.Delete` on this path is
inside that opt-in GC.

Before 10.5 this orphaning happened only for messages large enough to trigger attachment
splitting. 10.5 generalises it to **100% of inbound mail**.

Not in the PRD's Technical Considerations or Open Questions, not covered by a test
(`TestDeliverStampedBytesActuallyPersisted` asserts the new blob exists, never that the old one is
handled), and not in the 10.7 operator docs.

**Already tracked as task 10.9** — the critic found it independently, which corroborates the
earlier analysis. The live deployment is unaffected (`blob_gc = "on"`, sweeping confirmed); the
exposure is for *new* deployments following the example config.

## Gap 3 — the phase's headline outcome is never tested end to end (US-001 -> PARTIAL)

**The criterion** names `BuiltinProcessor.Process` specifically, because that is the only place
the whole chain closes: signed DSN bytes -> `antispam.VerifyDKIM` -> dkimResult/dkimDomain ->
`applyDMARC` -> action + TargetMailbox.

**What the test does:** `TestApplyDMARC_SignedBouncePassesQuarantinePolicy`
(`dmarc_integration_test.go:211`) never calls `Process`. It calls the unexported `proc.applyDMARC`
directly, passing `dkimDomain="example.test"` and `dkimResult="pass"` as literal strings. The DKIM
verdict — the entire deliverable of task 10.2 — is asserted by fiat rather than derived from the
bytes `BounceGenerator` actually emits.

**The test's own justification is stale.** It says `applyDMARC` is called directly because
`VerifyDKIM` "requires a live DNS TXT lookup" — but commit `e21b970` (task 10.5) added the
exported `antispam.LookupTXT` stub hook for exactly this, and `trace_headers_test.go` already uses
it to drive a real `pass` offline.

So the phase's central claim — a real generated bounce surviving our own DMARC evaluation — is
stitched from two half-tests: `bounce_test.go` proves the signature verifies under go-msgauth, and
`dmarc_integration_test.go` proves DMARC passes *given* a hand-typed "pass". The seam between them
is untested, including whether `Process`'s header extraction handles a `multipart/report` DSN and
whether `VerifyDKIM` returns pass over the exact bytes emitted after the 10.3/10.4 changes.

## What was verified as genuinely solid

- **US-006** — wire-level tests capture the actual pre-STARTTLS EHLO through a go-smtp stub, with
  a negative control proving the strict-server fixture is real. Confirmed the only non-test
  `netsmtp.NewClient` is the fixed one, so there is no second unfixed outbound path.
- **US-002, US-003** — clean across all three lenses.
- **US-005** — the ownership test now shares one store with a positive control, so its 404 is
  attributable to the guard rather than to absence.

## Recommended follow-ups

1. Close Gap 3 with a genuine end-to-end test using the existing `LookupTXT` hook. Highest value:
   it tests the thing the phase was actually for.
2. Close Gap 1 with a test proving the non-oversigned case still verifies after stamping, and
   amend the PRD to record that FR-12 takes precedence over FR-13.
3. Gap 2 is task 10.9, plus a line in the PRD's Technical Considerations.
