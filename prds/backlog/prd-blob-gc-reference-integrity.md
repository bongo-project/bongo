# PRD: Blob GC reference integrity

Backlog: **B15**. Raised 2026-08-30 after blob GC deleted blobs that were still
referenced on the production node.

## 1. Introduction / Overview

`blob-gc` is the only component in Bongo that deletes blobs. Blobs are
content-addressed and shared: a byte-identical message delivered to five people
is one blob, so deleting it because *one* reference was missed loses that
content for **all five**. There is no undo — a swept blob is gone from disk, and
its `MailMeta` survives pointing at nothing.

On 2026-08-30 a sweep on the production node deleted 17 blobs
(`blob-gc: swept orphaned blobs candidates:17 deleted:17 dry_run:false`,
08:41:59). From the next restart onward, two messages have logged
`index: could not read message body for indexing` and
`blobsync: pull failed ... not found on any peer` for blobs `7d9cab4a236a…`
and `7f746febce3e…`. Their bodies are gone; their metadata is not.

`blob_gc` is currently set to `"dryrun"` on the production node as mitigation,
so nothing is being deleted while this work is open.

The reference set is built by `collectReferencedBlobs`
(`internal/agent/agent.go`). Preliminary reading found one confirmed gap and two
silent failure paths in that function — see Technical Considerations. This PRD
covers diagnosing the reference set, fixing it, proving the fix, and returning
`blob_gc` to `"on"` safely.

## 2. Goals

- Identify every path by which a live blob can be absent from the reference set.
- Fix them, with a regression test per gap that fails against the current code.
- Make a reference-set failure loud rather than silent, so a future gap cannot
  silently widen the delete list.
- Determine whether the two already-deleted blobs were user mail or debris, and
  clear the dangling metadata that logs a warning on every restart.
- Return `blob_gc` to `"on"` with evidence that the candidate list is correct.

## 3. User Stories

### US-001: The reference set covers every blob-bearing record

**Description:** As an operator, I want every blob reachable from live state to
be counted as referenced, so the sweep never proposes a live blob for deletion.

**Acceptance Criteria:**
- [ ] Given a queued `OutboundMessage` with `blob_hash = "aaa"` and
      `attachments = [{hash: "bbb"}, {hash: "ccc"}]` under `/queue/pending/`,
      when `collectReferencedBlobs` runs, then the returned set contains all of
      `aaa`, `bbb` and `ccc`.
- [ ] The same holds for a message under `/queue/bounced/`.
- [ ] Given a `MailMeta` with `raw_message.hash = "ddd"` and
      `attachments = [{hash: "eee"}]`, the set contains both.
- [ ] Given a spool envelope in each of `incoming`, `processing`, `deliver`,
      `quarantine` and `error` with `blob_hash = "f<n>"`, the set contains every
      one of those five hashes.
- [ ] An enumeration test asserts that every `BlobRef`-bearing field in
      `proto/bongo/v1/` is exercised by at least one of the cases above, so a
      new blob-bearing field cannot be added without this test failing.

### US-002: A reference scan that cannot read its inputs fails, it does not under-report

**Description:** As an operator, I want a reference scan that hits an error to
abort the sweep, because an under-counted reference set and a genuinely empty
one are indistinguishable to the caller — and the difference is whether live
mail gets deleted.

**Acceptance Criteria:**
- [ ] Given a `/user/{id}/mail/{guid}` value that fails `proto.Unmarshal`, when
      `collectReferencedBlobs` runs, then it returns a non-nil error naming the
      offending key, rather than skipping the record.
- [ ] Given a queue value that fails `proto.Unmarshal`, same.
- [ ] Given `collectReferencedBlobs` returns an error, when `sweepOrphanedBlobs`
      runs, then it deletes nothing and logs at WARN.
- [ ] A test proves the sweep deletes nothing on a reference-collection error,
      by asserting blob count is unchanged.

### US-003: The sweep can be audited before it is trusted

**Description:** As an operator, I want to see exactly which blobs a sweep would
delete and why nothing references them, so I can confirm correctness before
re-enabling deletion.

**Acceptance Criteria:**
- [ ] `blob_gc = "dryrun"` logs each candidate hash individually, not only a
      count, at a level that survives the default log configuration.
- [ ] Each candidate line includes the blob's size and mtime.
- [ ] Given a dryrun sweep with 3 candidates, the log contains 3 identifiable
      candidate lines plus the existing summary line.

### US-004: The two deleted blobs are attributed

**Description:** As an operator, I want to know whether the two lost blobs were
real user mail or debris, so I know the severity of what already happened.

**Acceptance Criteria:**
- [ ] A written finding records, for each of `7d9cab4a236a…` and
      `7f746febce3e…`: the owning user ID, the mailbox, the subject and sender if
      recoverable from `MailMeta`, and whether that user still exists.
- [ ] The finding states whether each was reachable by a live user at the time of
      deletion.
- [ ] The dangling `MailMeta` records are removed, and a restart no longer logs
      `could not read message body for indexing` for either hash.
- [ ] Recovery is explicitly NOT attempted (see Non-Goals).

### US-005: blob_gc returns to "on" with evidence

**Description:** As an operator, I want deletion re-enabled only once the
candidate list has been proven correct on real data.

**Acceptance Criteria:**
- [ ] A dryrun period of at least 24h on the production node produces candidate
      lists that a documented check confirms contain no blob reachable from live
      state.
- [ ] The check is written down and repeatable, not a one-off inspection.
- [ ] `blob_gc` is set back to `"on"` in `deploy/bongo.prod.hcl` only after that
      check passes, in its own commit referencing this PRD.

## 4. Functional Requirements

- **FR-1:** `collectReferencedBlobs` must include `OutboundMessage.Attachments`
  for both `/queue/pending/` and `/queue/bounced/`.
- **FR-2:** `collectReferencedBlobs` must return an error, not skip, when a
  value fails to read or unmarshal.
- **FR-3:** `sweepOrphanedBlobs` must delete nothing when reference collection
  returns an error. (Already true; must be covered by a test.)
- **FR-4:** A test must enumerate `BlobRef`-bearing proto fields and fail if one
  is not covered by the reference-set tests.
- **FR-5:** Dryrun mode must log every candidate hash with size and mtime.
- **FR-6:** The two dangling `MailMeta` records must be removed.
- **FR-7:** `blob_gc` must be returned to `"on"` in `deploy/bongo.prod.hcl`
  after the verification in US-005.

## 5. Non-Goals (Out of Scope)

- **Recovering the two lost blob bodies.** Decided 2026-08-30: attribute the
  loss, accept it. Do not search backups or peers for the content.
- Redesigning blob storage, the grace period, or the two-pass sweep rule. The
  existing design is sound; this is about the reference set feeding it.
- The unrelated `orphan-gc` mail-metadata sweep, except where it is shown to
  interact with this bug.
- Blob replication and `blobsync` behaviour beyond the missing-blob warning.

## 6. Technical Considerations

Findings from a first pass over `internal/agent/agent.go`, to be confirmed
during implementation:

1. **Confirmed gap — queue attachments.** `OutboundMessage` carries two blob
   references: `blob_hash = 5` and `repeated BlobRef attachments = 14`,
   populated at `internal/queue/worker.go:166`. The queue branch of
   `collectReferencedBlobs` reads only `msg.BlobHash`. Any queued message using
   attachment-dedup (non-empty `layout`) therefore has its attachment blobs
   uncounted.
2. **Silent skips.** Both the mail and queue loops `continue` on a `Value()`
   error and on a failed `proto.Unmarshal`. A record that cannot be read
   contributes no references and says nothing, so corruption or schema drift
   silently widens the delete list. Contrast the spool branch, whose comment
   explicitly argues the opposite and returns an error.
3. **Interaction to check.** `orphan-gc: reclaimed orphaned mail metadata
   count:21` was logged at 08:27 on 2026-08-30, 14 minutes before the sweep that
   deleted 17 blobs. Determine whether metadata reclamation can strand a blob
   that the sweep then treats as garbage, and whether the ordering of the two
   sweeps matters.
4. **Scope of the `/mail/` filter.** The mail branch matches keys containing
   `/mail/`. Confirmed that `BlobRef` appears only in `MailMeta` and
   `OutboundMessage`, so calendar events and contacts do not hold blobs —
   `Contact.vcard_raw` is inline `bytes`. Note the key-space table in CLAUDE.md
   claims `contact/{guid} → contact + BlobRef (vCard)`, which is stale and
   should be corrected.
5. The sweep is leader-only and two-pass (`prev`), with a `minAge` grace of 1h.
   Neither is suspected; do not change them.

## 7. Success Metrics

- Every gap found has a test that fails against today's code and passes after.
- A 24h dryrun on production proposes no blob reachable from live state.
- `blob_gc = "on"` restored, with no missing-blob warnings on the following
  restart.
- No `could not read message body for indexing` lines after FR-6.

## 8. Open Questions

- Should reference collection failing hard (FR-2) risk the sweep never running
  on a node with one corrupt record? Suggested answer: yes — a sweep that never
  runs costs disk, a sweep on bad data costs mail. Confirm before implementing.
- Is a 24h dryrun (US-005) long enough given the 15m GC interval and 1h grace,
  or should it span a full backup/restart cycle?
- Should the enumeration test in FR-4 live in `internal/agent` or alongside the
  proto definitions?
