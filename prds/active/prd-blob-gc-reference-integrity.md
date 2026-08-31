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

### US-002: A reference scan that cannot parse a record fails, it does not under-report

**Description:** As an operator, I want a reference scan that cannot parse a
record to abort the sweep, because an under-counted reference set and a
genuinely empty one are indistinguishable to the caller — and the difference is
whether live mail gets deleted.

**CORRECTED by the 14.1 audit.** This story originally also covered `Value()`
read errors. That half was wrong: `pebbleIterator.Value()` calls Pebble's
`ValueAndErr`, which sets `i.err` on the iterator
(`pebble@v1.1.5/iterator.go:2199-2208`); `collectReferencedBlobs` checks both
iterator `Close()` calls and returns the error, and `sweepOrphanedBlobs` then
aborts the pass. A value-read failure already aborts the sweep. Only the
`proto.Unmarshal` path silently drops references.

**Prerequisite:** the key filter must be tightened FIRST. `agent.go` matches
`bytes.Contains(key, "/mail/")`, so a user-created IMAP mailbox literally named
`mail` yields `/user/{id}/mailbox/mail/msg/...`, whose `MailboxMessage` value
fails to unmarshal as `MailMeta` on a wire-type mismatch. Making unmarshal fatal
without tightening the filter lets any user permanently disable the GC by
creating a mailbox called `mail`.

**Acceptance Criteria:**
- [ ] The mail-key filter parses structurally (`/user/{id}/mail/{guid}`, no
      further separator) as `OrphanedMailGUIDs` already does, rather than
      substring-matching `/mail/`.
- [ ] Given a mailbox named `mail` containing a message, when
      `collectReferencedBlobs` runs, then that key is not treated as a `MailMeta`
      and no error is raised.
- [ ] Given a `/user/{id}/mail/{guid}` value that fails `proto.Unmarshal`, then
      `collectReferencedBlobs` returns a non-nil error naming the offending key.
- [ ] Given a queue value that fails `proto.Unmarshal`, same.
- [ ] Given `collectReferencedBlobs` returns an error, `sweepOrphanedBlobs`
      deletes nothing, logs at WARN, and a test asserts the blob count is
      unchanged.
- [ ] A comment records why the `Value()` error path is safe, since it reads as
      if it swallows the error and is only safe by a non-obvious property of
      Pebble.

### US-006: A blob is re-validated immediately before it is deleted

**Description:** As an operator, I want the sweep to re-check a candidate against
a fresh reference set just before deleting it, so a reference created during the
scan cannot be missed.

**Rationale (14.1 audit, the headline finding).** The `minAge` grace exists so
"a blob written just before its referencing metadata is never reclaimed". That
guarantee does NOT hold for a deduplicated write. `BlobStore.Put` and `PutReader`
return early when `Exists(hash)` is true and never touch the file, so the mtime
stays at the ORIGINAL write time. A year-old blob that becomes referenced again
during a scan is therefore both unreferenced-in-the-snapshot and older than the
cutoff, so it is deleted while a brand-new `MailMeta` points at it. The stamped-
message path makes this deterministic: `deliver.go` notes that a redelivery
re-derives identical stamped bytes and the same hash. This produces exactly the
observed symptom — metadata present, body missing, small count.

**Acceptance Criteria:**
- [ ] Given a blob whose mtime is older than the cutoff and which was a candidate
      in the previous pass, when a reference to it is created after
      `collectReferencedBlobs` returns but before deletion, then the blob is NOT
      deleted.
- [ ] The re-validation runs only on passes that would delete something, so a
      steady-state quiet pass costs nothing extra.
- [ ] A test writes a blob, ages its mtime past the cutoff, marks it a candidate,
      then adds a reference between scan and sweep, and asserts it survives.
- [ ] A test proves a genuinely unreferenced blob is still deleted, so the
      re-validation does not disable the GC.

### US-007: The candidate set does not survive a mode change or a failed pass

**Description:** As an operator, I want the two-pass rule to mean two consecutive
GOOD passes under the CURRENT mode, so re-enabling deletion cannot act on stale
evidence.

**Rationale (14.1 audit, HALF CORRECTED during 14.3.)**

The audit reported two holes. The first was **wrong, and the error was mine for
relaying it without checking**: it claimed `dryrun` accumulates a candidate set
that a flip to `"on"` would delete on the first pass, and that this put the
production node at risk right now. It does not. `a.Config` is assigned once in
`NewAgent` (`agent.go:240`), `Storage.BlobGC` is never written outside tests, and
the only `signal.Notify` in the tree is SIGINT/SIGTERM for shutdown
(`cmd/bongo-go/agent.go:178`) — there is no SIGHUP reload (that is backlog B9).
So `BlobGCMode()` returns a constant for the life of a process: changing the mode
requires a restart, and a restart gives `runOrphanGC` a fresh empty `prevBlobs`.
Flipping to `"on"` therefore gets the normal two-pass grace. **14.9 is not blocked
by this**, and production was never in the state the audit described.

The mode-change reset is still worth implementing, but as defence-in-depth rather
than a live fix: it costs a few lines and becomes load-bearing the moment B9 lands
a config reload, at which point the hole would be real and silent.

The second hole is **real and reachable today**. On a `collectReferencedBlobs` or
`Blobs.List()` failure, `sweepOrphanedBlobs` returns `prev` unchanged, so the
candidate set survives an arbitrary run of failed passes. "Unreferenced for two
consecutive passes" then no longer means two consecutive *good* passes: a
candidate marked long ago can be deleted on the first pass that succeeds after a
gap of failures. 14.2's re-validation now bounds the damage — a blob referenced in
the meantime is spared at the delete barrier — but the grace should still mean
what its comment says.

**Acceptance Criteria:**
- [ ] Changing `blob_gc` between `off`, `dryrun` and `on` resets the candidate
      set, so the next deletion needs two fresh passes.
- [ ] On a `collectReferencedBlobs` or `Blobs.List()` error, `sweepOrphanedBlobs`
      returns an empty set, matching the non-leader path.
- [ ] A test proves a candidate seen only in dry-run is not deleted on the first
      pass after switching to `on`.

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

### US-004: The lost blobs are attributed

**SCOPE CORRECTED 2026-08-31: eight messages, not two.** Checked directly on the
production node after deploying v0.6.25. Eight distinct message GUIDs log
`index: could not read message body for indexing` on every restart, and all
eight of their blob hashes are confirmed absent from `/mnt/data/bongo/data/blobs`
-- genuinely deleted, not merely unreadable:

```
0f1a639c-9c76-4210-b8d7-e5dbe07bcf85   1c136290-5e0f-4102-8c05-830da2db0005
2c1ec4ce-60a3-4298-bf61-90e64f806ec4   850a29c6-2ba3-4c83-9b00-33981958ede8
bce1ebfd-bf01-4191-a3e6-a935f73d3afb   c19d1d4d-2188-441b-b5c6-156926dc8d19
c5773e08-aa6f-49a0-b2fa-56a6e344ce32   e50e6a74-69aa-4845-88a2-745cd518ea32
```

This PRD said two because two were all that appeared in the log excerpt the
original investigation read. The other six were in the same logs throughout. The
warnings only fire during startup indexing, so they arrive in a burst per restart
and are silent otherwise, and nothing surfaces in the UI -- the messages still
list, they render empty. That is why the operator reported seeing no further
problems.

Still consistent with the single 2026-08-30 sweep, which deleted 17 blobs: eight
dangling references is a subset of that. No evidence of a second loss event, and
no blob-gc activity at all since `blob_gc` went to `dryrun`. But the container log
only reaches back to the most recent restart, so the sweep itself is outside the
window and single-cause cannot be confirmed from logs alone.

The acceptance criteria below apply to all eight.

### US-004 (original text, retained for the criteria)

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
  after the verification in US-005, and only after FR-9.
- **FR-8:** `sweepOrphanedBlobs` must re-validate each candidate against a fresh
  reference set immediately before deleting it (US-006).
- **FR-9:** The candidate set must reset on a `blob_gc` mode change, and must be
  emptied rather than carried forward on a failed pass (US-007).
- **FR-10:** The mail-key filter must parse `/user/{id}/mail/{guid}`
  structurally instead of substring-matching `/mail/` (US-002 prerequisite).
- **FR-11:** Any pass that deletes must log each deleted hash at INFO. The
  existing per-pass detail is Debug-only, which is why the 2026-08-30 incident
  left counts and no hashes.

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

**Audited in 14.1 (2026-08-30). Eight gaps found; findings below supersede the
initial reading where they conflict.**

0. **Headline (HIGH) — dedup defeats the mtime grace, and there is no barrier
   between the scan and the delete.** See US-006. This is the only gap that
   produces exactly the observed symptom and it is a whole class, not one field.
   Fix first.

1. **Confirmed gap — queue attachments.** `OutboundMessage` carries two blob
   references: `blob_hash = 5` and `repeated BlobRef attachments = 14`,
   populated at `internal/queue/worker.go:166`. The queue branch of
   `collectReferencedBlobs` reads only `msg.BlobHash`. Any queued message using
   attachment-dedup (non-empty `layout`) therefore has its attachment blobs
   uncounted.
2. **Silent skips — HALF REFUTED by the audit.** The `proto.Unmarshal`
   `continue` is a real gap. The `Value()` `continue` is NOT: the error is
   recorded on the Pebble iterator and surfaced by `Close()`, which
   `collectReferencedBlobs` checks, so a value-read failure aborts the sweep.
   It reads like a bug and is safe only by a non-obvious library property —
   worth a comment, not a fix. See US-002.

2b. **Ordering answered.** All four sweeps run sequentially in ONE 15-minute
   tick, `sweepOrphanedMail` first and `sweepOrphanedBlobs` last, which explains
   the 14-minute gap between the `count:21` reclaim and the 17 deletions: the
   metadata reclaim supplied the candidates, and the two-pass rule correctly
   held them for one interval. This is correct behaviour and NOT the fault —
   something re-referenced 2 of those blobs between the two scans, which is
   gap 0. Worth changing anyway: running the two sweeps in the same tick
   collapses a full interval of safety margin for free.

2c. **Lower-severity gaps** (full detail in progress.md): the spool scan lists
   its five directories in a fixed order, so an envelope moving BACKWARD
   (quarantine→incoming on admin release, processing→incoming on shutdown) can
   be missed by a single pass; and the reference scan reads only the node-local
   spool, which is not replicated, so a blob referenced solely by a peer's
   quarantine envelope is invisible to the leader.

2d. **Over-retention, not loss:** nothing removes envelopes from `spool/deliver/`,
   so every delivered message's pre-stamp original stays pinned forever. Useful
   forensics: the 17 deleted blobs were therefore almost certainly stamped
   bodies, split attachments or compose/Sent blobs, not inbound originals.
3. **Interaction to check.** `orphan-gc: reclaimed orphaned mail metadata
   count:21` was logged at 08:27 on 2026-08-30, 14 minutes before the sweep that
   deleted 17 blobs. Determine whether metadata reclamation can strand a blob
   that the sweep then treats as garbage, and whether the ordering of the two
   sweeps matters.
4. **Scope of the `/mail/` filter.** The mail branch matches keys containing
   `/mail/`. Confirmed that `BlobRef` appears only in `MailMeta` and
   `OutboundMessage`, so calendar events and contacts do not hold blobs —
   `Contact.vcard_raw` and `CalendarEvent.ical_raw` are inline `bytes`, and no
   calendar or contact path calls `blobs.Put`. Note the key-space table in
   CLAUDE.md claims a `BlobRef` on BOTH `event/{guid}` and `contact/{guid}`;
   both are stale and should be corrected.
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
