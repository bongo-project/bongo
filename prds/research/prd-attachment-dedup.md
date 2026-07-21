# PRD: Attachment deduplication (de-MIME on ingest)

## 1. Introduction / Overview

Bongo's original design (see the project PRD §5.2–5.3) stores message bodies and
attachments as **separate content-addressed blobs**, so a large attachment sent
to, or forwarded between, many people is stored **once** and referenced by each
message's metadata. The proto reflects this: `MailMeta` carries both a
`RawMessage` blob ref and a `repeated BlobRef attachments`.

That design was only half-built:

- **Outbound (compose)** stores per-attachment blobs *and* the full assembled raw
  message — so the attachment bytes live in **both** places.
- **Inbound (delivery pipeline)** never extracts attachments at all: it stores
  the whole raw message and records attachment metadata (filename/size/type)
  with **no hash** (an explicit "future work" deferral in `deliver.go`).

Because the **complete raw RFC822 message is always stored whole**, and it
contains the attachment payloads, the cross-message dedup the design promised
(e.g. "forward the same 5 MB PDF to 10 people → 1 copy") is **not actually
happening**. Storage grows linearly with recipients-of-differing-messages.

This feature completes the original design: store each message as a **skeleton**
(the raw with large attachment payloads removed) plus **separate, deduplicated
attachment blobs**, and **reconstruct the exact original RFC822 bytes on
demand** for IMAP, download and export.

## 2. Goals

- Store each qualifying attachment payload **once** per unique encoded content,
  shared across all messages (inbound and outbound) that carry it.
- Remove the redundant storage of attachment payloads inside the stored raw
  message.
- Preserve **byte-exact** message fidelity: anything that reads the full message
  (IMAP FETCH/BODY, web view, download, EML export) gets the original bytes,
  unchanged, so **DKIM signatures still verify** after a storage round-trip.
- Unify inbound and outbound onto one storage/reconstruction path.
- Ship safely: no fidelity regression, even though it is on by default.

## 3. Decisions (agreed)

| Decision | Choice | Implication |
|----------|--------|-------------|
| Dedup key | **Encoded bytes** | Store the attachment exactly as encoded in the message; dedupe on the hash of those bytes. Reconstruction is concatenation → byte-exact → DKIM-safe. Dedupes same-source duplicates (ESP/newsletter/forward); does not dedupe the same file re-encoded by a different client. |
| Existing mail | **New mail only** | De-MIME at ingest going forward. No backfill migration; existing whole-raw messages keep working via the reconstruction path's passthrough. |
| Rollout | **Default on** | Enabled for all new mail once merged (no gating flag). Safety comes from the ingest-time self-check (FR-7), not a flag. |
| Threshold | **Size-gated** | Only split out attachment parts whose encoded body is ≥ a configurable threshold (default 32 KB). Small/inline parts stay in the skeleton. |

## 4. User stories

### US-001: Inbound attachment stored as a deduplicated blob
**As** the delivery pipeline, **I want** each qualifying inbound attachment part
stored as its own content-addressed blob **so that** the same attachment across
different messages is stored once.

**Acceptance criteria:**
- [ ] Given an inbound message with one attachment part whose encoded body is ≥
  the threshold, when it is delivered, then `MailMeta.Attachments[0].Hash` is a
  non-empty SHA-256 hash and a blob with that hash exists in the blob store.
- [ ] Given two inbound messages (different headers/bodies) carrying the
  **identical encoded** attachment, when both are delivered, then the blob store
  contains **exactly one** blob for that attachment content (the second `Put` is
  a no-op).
- [ ] Given an inbound attachment part whose encoded body is `< threshold`, when
  delivered, then that part is **not** extracted (it stays in the skeleton) and
  no separate blob is created for it.

### US-002: Skeleton replaces the whole raw for split messages
**As** the storage layer, **I want** the stored message to omit the extracted
attachment payloads **so that** those bytes are not stored twice.

**Acceptance criteria:**
- [ ] Given a message split into a skeleton + one attachment blob, when I sum the
  stored skeleton size + the (deduped) attachment blob size for that single
  message, then it is less than the size of the original whole raw by
  approximately the extracted attachment's encoded size.
- [ ] The skeleton records, in order, the position and hash of each extracted
  part sufficient to splice the parts back verbatim.

### US-003: Byte-exact reconstruction (DKIM preserved)
**As** any reader of the full message, **I want** the exact original bytes
**so that** signatures and structure are intact.

**Acceptance criteria:**
- [ ] Given a delivered message that was DKIM-signed and passed verification at
  ingest, when the full raw is reconstructed from skeleton + attachment blobs,
  then the reconstructed bytes are **identical** to the original raw
  (`bytes.Equal`), and re-running DKIM verification on the reconstructed bytes
  yields the same PASS result.
- [ ] An IMAP `FETCH (BODY[])` of a split message returns bytes identical to a
  `FETCH (BODY[])` of the same message stored whole (verified against a
  pre-split capture).
- [ ] `ExportEML` and the web download of the full message return the exact
  original bytes.

### US-004: Retrieval works for both split and legacy messages
**As** the retrieval paths, **I want** one function that returns the full raw for
any message **so that** legacy whole-raw messages and new split messages are read
uniformly.

**Acceptance criteria:**
- [ ] Given a legacy message stored as a whole raw (no splice metadata), when the
  full raw is requested, then the stored raw is returned unchanged.
- [ ] Given a split message, when the full raw is requested, then it is
  reconstructed. Both cases go through the same retrieval entry point.

### US-005: Same attachment across different senders → one copy
**As** the operator, **I want** a shared attachment stored once **so that**
storage does not scale with recipients.

**Acceptance criteria:**
- [ ] Given the same encoded attachment delivered in N separate messages to N
  different users, then the blob store holds exactly one copy of that attachment
  content, referenced by all N `MailMeta` records.

### US-006: Configurable threshold
**As** the operator, **I want** to tune the split threshold.

**Acceptance criteria:**
- [ ] Given `attachments.dedup_min_size` (or equivalent) set in config, when a
  message is ingested, then parts are split only if their encoded body ≥ that
  value; an unset value uses the 32 KB default.

### US-007: Ingest-time fidelity self-check with fallback
**As** the ingest path, **I want** to verify reconstruction before committing to
the split form **so that** a message that cannot be reproduced byte-for-byte is
never stored in a lossy way.

**Acceptance criteria:**
- [ ] Given a message being split at ingest, when the just-built skeleton +
  attachment blobs do **not** reconstruct to bytes identical to the original raw,
  then the message falls back to whole-raw storage (current behaviour) and the
  fallback is logged with the message id and reason.
- [ ] Given the self-check passes, then the split form is stored.

### US-008: Outbound unified onto the same model
**As** compose/send, **I want** to store the skeleton + attachment blobs (not the
whole raw plus separate attachment blobs) **so that** outbound also dedupes and
stops double-storing.

**Acceptance criteria:**
- [ ] A sent message with a qualifying attachment stores a skeleton and a
  deduped attachment blob, and does **not** store the attachment payload a
  second time inside a whole raw.
- [ ] The Sent copy reconstructs byte-exact (US-003), including any DKIM
  signature applied on send.

### US-009: GC keeps shared attachment blobs until the last reference
**As** garbage collection, **I want** an attachment blob retained while any
message references it.

**Acceptance criteria:**
- [ ] Given attachment blob H referenced by two messages, when one message is
  permanently deleted, then H still exists.
- [ ] When the last referencing message is permanently deleted, then H becomes
  eligible for collection.

## 5. Functional requirements

- **FR-1:** At ingest (inbound delivery and outbound compose), parse the MIME
  structure and identify attachment parts whose **encoded** body length ≥ the
  configured threshold.
- **FR-2:** For each such part, store its exact encoded body bytes as a
  content-addressed blob (dedup via existing content-addressing) and record a
  `BlobRef{hash, size, content_type, filename}` in `MailMeta.Attachments`.
- **FR-3:** Build a skeleton blob = the original raw with each extracted part's
  encoded body removed, plus ordered splice metadata (position + hash + length)
  sufficient for verbatim reconstruction. Store the skeleton where the whole raw
  is stored today; mark the record as "split" (e.g. splice metadata present).
- **FR-4:** Provide a single `FullRawMessage(meta)` retrieval function that
  returns the whole raw: passthrough for legacy/unsplit records, reconstruct for
  split records.
- **FR-5:** Route **all** full-message readers through FR-4: IMAP FETCH/BODY,
  web message view (`applyEnvelopeBody`), `ServeBlob`/`ServeAttachment` where they
  read the whole message, `ExportEML`, backup/export, DKIM/antispam re-parse.
- **FR-6:** Reconstruction must be byte-exact; the encoded-bytes storage choice
  guarantees this by concatenation.
- **FR-7:** Perform an ingest-time reconstruction self-check; on any mismatch,
  fall back to whole-raw storage and log it. This makes default-on safe.
- **FR-8:** Ensure blob reference-counting / GC treats attachment blob hashes
  (and skeleton hashes) as live references, so shared attachments survive until
  the last referencing message is gone.
- **FR-9:** Config knob `dedup_min_size` (default 32 KB) controls the threshold.
- **FR-10:** Backward compatibility: existing whole-raw messages continue to be
  read and served unchanged (no migration).

## 6. Non-goals (out of scope)

- **Decoded-content dedup** (deduping the same file across different transfer
  encodings). Explicitly rejected in favour of byte-exact encoded-bytes dedup.
- **Backfill / migration** of already-stored messages. New mail only.
- **Chunk-level or body dedup** beyond whole attachment parts; no
  content-defined chunking of bodies.
- **Compression** of blobs.
- A rollout **feature flag** (the self-check + fallback is the safety mechanism
  instead).

## 7. Technical considerations

- **Skeleton representation.** Prefer a representation that reconstructs by pure
  concatenation (e.g. skeleton = ordered list of literal spans and
  attachment-hash placeholders, or the raw with payloads elided plus byte-offset
  splice records). Whatever is chosen must round-trip byte-exact and be cheap to
  splice on read.
- **DKIM interplay.** DKIM body/hd hashes cover the encoded body as transmitted.
  Because we store the exact encoded bytes and reconstruct verbatim, signatures
  remain valid. The ingest self-check (FR-7) is the backstop. Outbound messages
  are DKIM-signed by the queue worker; confirm the stored Sent copy corresponds
  to the signed bytes and reconstructs to them.
- **`enmime` gives decoded content**, not the exact encoded span. Extraction
  likely needs to locate the encoded body span in the raw (part boundaries +
  headers) rather than re-encoding `enmime`'s decoded output. This is the crux of
  byte-exactness and should be prototyped first.
- **Retrieval hot path.** Reconstruction adds work to every full-message read;
  keep it streaming where possible and measure IMAP FETCH latency for large
  messages. Small messages (no split) are unaffected.
- **GC.** Confirm the current blob reference model (the project PRD describes
  reference-counted GC; verify what is actually implemented) counts
  `MailMeta.Attachments[].Hash`. Update it if it only counts `RawMessage`.
- **Blob-sync (multi-node).** Attachment blobs sync like any other blob; no new
  transport work, but the skeleton + attachment blobs must all be present on a
  node before a message reads correctly.
- **Partial-failure semantics.** Storing a skeleton + N attachment blobs is
  multiple writes; define the ordering so a message is never referenced before
  all its blobs are durable (write blobs first, then the metadata via Raft).

## 8. Success metrics

- **Fidelity:** 100% of split messages reconstruct byte-exact; IMAP FETCH and EML
  export are identical to pre-split captures; DKIM re-verify matches ingest. Zero
  fidelity fallbacks caused by bugs (fallbacks only for genuinely
  non-reproducible inputs).
- **Storage:** measurable reduction in blob bytes for a representative corpus
  containing repeated attachments (e.g. the same attachment across N messages
  occupies ~1×, not N×). Report bytes saved / dedup ratio.
- **Performance:** ingest overhead and full-message retrieval latency within an
  agreed budget (to be set during planning) for large (e.g. 10 MB) attachments.

## 9. Open questions

- Exact skeleton on-disk representation (spans+placeholders vs elided-raw
  +offsets) — decide during a short spike (see Technical considerations).
- Whether to also split very large **inline** (non-attachment) parts, or restrict
  strictly to `Content-Disposition: attachment` / named parts.
- Where the threshold config lives (`storage`, a new `attachments` block, or
  `pipeline`) and whether it applies to outbound identically.
- Metrics: add `bongo_attachment_dedup_*` counters (blobs written vs deduped,
  bytes saved) for observability?
