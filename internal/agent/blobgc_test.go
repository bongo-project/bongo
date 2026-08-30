package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/config"
	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func TestPlanBlobSweep(t *testing.T) {
	now := time.Now()
	cutoff := now.Add(-time.Hour)
	referenced := map[string]bool{"ref1": true}
	blobs := map[string]time.Time{
		"ref1":  now.Add(-2 * time.Hour), // referenced -> always kept
		"old1":  now.Add(-2 * time.Hour), // unreferenced + old -> candidate
		"young": now.Add(-time.Minute),   // unreferenced but too young -> skipped
	}

	// First pass: candidates identified, but nothing deleted (no prior sighting).
	cur, del := planBlobSweep(referenced, blobs, map[string]bool{}, cutoff)
	if len(del) != 0 {
		t.Fatalf("first pass must delete nothing (grace), got %v", del)
	}
	if !cur["old1"] || cur["ref1"] || cur["young"] {
		t.Fatalf("candidate set wrong: %v", cur)
	}

	// Second pass: old1 was a candidate last time -> now eligible.
	_, del2 := planBlobSweep(referenced, blobs, cur, cutoff)
	if len(del2) != 1 || del2[0] != "old1" {
		t.Fatalf("second pass should delete only old1, got %v", del2)
	}

	// A blob that becomes referenced between passes is spared.
	_, del3 := planBlobSweep(map[string]bool{"ref1": true, "old1": true}, blobs, cur, cutoff)
	if len(del3) != 0 {
		t.Fatalf("newly-referenced blob must not be deleted, got %v", del3)
	}
}

func TestCollectReferencedBlobs(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer ps.Close()

	meta := &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: "rawhash"},
		Attachments: []*bongov1.BlobRef{{Hash: "atthash"}},
	}
	mb, _ := proto.Marshal(meta)
	if err := ps.Set([]byte("/user/u1/mail/g1"), mb); err != nil {
		t.Fatalf("Set mail: %v", err)
	}
	// A non-mail user key must be ignored (not treated as a MailMeta).
	if err := ps.Set([]byte("/user/u1/config/theme"), []byte("dark")); err != nil {
		t.Fatalf("Set config: %v", err)
	}
	// A queued outbound message references the whole-raw blob (e.g. a forward).
	om := &bongov1.OutboundMessage{Id: "o1", BlobHash: "queuehash"}
	ob, _ := proto.Marshal(om)
	if err := ps.Set([]byte("/queue/pending/00000000000000000001-o1"), ob); err != nil {
		t.Fatalf("Set queue: %v", err)
	}

	ref, err := collectReferencedBlobs(ps, nil)
	if err != nil {
		t.Fatalf("collectReferencedBlobs: %v", err)
	}
	for _, want := range []string{"rawhash", "atthash", "queuehash"} {
		if !ref[want] {
			t.Errorf("missing referenced hash %q (set: %v)", want, ref)
		}
	}
	if len(ref) != 3 {
		t.Errorf("referenced set size = %d, want 3: %v", len(ref), ref)
	}
}

// TestCollectReferencedBlobsCountsSpoolEnvelopes covers the one reference source
// that is not in Pebble.
//
// A QueueEnvelope sitting in the spool names a BlobHash and has no Pebble key,
// so a reference set built only from Pebble scans treats its body as garbage.
// With blob_gc enabled and the default one-hour grace, any message held longer
// than that -- the normal state for quarantined mail awaiting admin review --
// had its body reclaimed while the envelope survived, leaving an entry that
// renders as an empty message.
//
// Every spool directory is checked, not just quarantine/: a deferred message in
// incoming/ or a parked one in error/ is equally reclaimable and equally wrong
// to reclaim.
func TestCollectReferencedBlobsCountsSpoolEnvelopes(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	spool, err := pipeline.NewSpoolStore(dir+"/spool", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}

	spoolDirs := []string{"incoming", "processing", "deliver", "quarantine", "error"}
	for i, d := range spoolDirs {
		env := &bongov1.QueueEnvelope{
			Id:       "env-" + d,
			BlobHash: "spoolhash-" + d,
		}
		if err := spool.WriteEnvelope(d, env); err != nil {
			t.Fatalf("WriteEnvelope into %s: %v", d, err)
		}
		_ = i
	}

	ref, err := collectReferencedBlobs(ps, spool)
	if err != nil {
		t.Fatalf("collectReferencedBlobs: %v", err)
	}

	for _, d := range spoolDirs {
		want := "spoolhash-" + d
		if !ref[want] {
			t.Errorf("blob referenced only by a spool envelope in %s/ was not counted as live; "+
				"the GC would reclaim it after the grace period and leave an unreadable message "+
				"(set: %v)", d, ref)
		}
	}
}

// TestCollectReferencedBlobsSurfacesSpoolReadFailure pins that a spool read
// error is returned rather than swallowed. A reference set that silently lost
// the spool is indistinguishable from a spool that is genuinely empty, and the
// difference is whether the next sweep deletes live mail.
func TestCollectReferencedBlobsSurfacesSpoolReadFailure(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	spool, err := pipeline.NewSpoolStore(dir+"/spool", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	// Make one spool directory unreadable.
	if err := osChmod(dir+"/spool/quarantine", 0o000); err != nil {
		t.Skipf("cannot chmod spool dir (running as root?): %v", err)
	}
	t.Cleanup(func() { _ = osChmod(dir+"/spool/quarantine", 0o750) })

	if _, err := collectReferencedBlobs(ps, spool); err == nil {
		t.Error("an unreadable spool directory produced no error; the sweep would run " +
			"against an incomplete reference set and delete live mail")
	}
}

func osChmod(path string, mode uint32) error { return os.Chmod(path, os.FileMode(mode)) }

// --- Re-validation before delete (US-006) -----------------------------------
//
// planBlobSweep's two guards -- the mtime cutoff and the two-pass rule --
// both exist to protect "a blob written just before its referencing
// metadata". Neither guard holds for a deduplicated write: BlobStore.Put and
// PutReader early-return without touching the file when Exists(hash) is true,
// so a dedup hit leaves the ORIGINAL mtime in place. A year-old blob that
// becomes referenced again during the reference scan is therefore
// simultaneously unreferenced-in-the-snapshot, older than the cutoff, and a
// two-pass candidate -- deleted while a brand-new MailMeta points at it. This
// produced the 2026-08-30 incident (17 blobs deleted, 2 still referenced).

func blobFilePath(dataDir, hash string) string {
	return dataDir + "/blobs/" + hash[:2] + "/" + hash
}

func TestReconfirmSweep(t *testing.T) {
	tests := []struct {
		name          string
		toDelete      []string
		fresh         map[string]bool
		wantSurvivors []string
		wantSpared    []string
	}{
		{
			name:          "nothing referenced, all survive",
			toDelete:      []string{"a", "b", "c"},
			fresh:         map[string]bool{},
			wantSurvivors: []string{"a", "b", "c"},
			wantSpared:    nil,
		},
		{
			name:          "one re-referenced, spared, order preserved",
			toDelete:      []string{"a", "b", "c"},
			fresh:         map[string]bool{"b": true},
			wantSurvivors: []string{"a", "c"},
			wantSpared:    []string{"b"},
		},
		{
			name:          "all re-referenced, none deleted",
			toDelete:      []string{"a", "b"},
			fresh:         map[string]bool{"a": true, "b": true},
			wantSurvivors: nil,
			wantSpared:    []string{"a", "b"},
		},
		{
			name:          "empty candidate list",
			toDelete:      nil,
			fresh:         map[string]bool{"a": true},
			wantSurvivors: nil,
			wantSpared:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			survivors, spared := reconfirmSweep(tt.toDelete, tt.fresh)
			if !slicesEqual(survivors, tt.wantSurvivors) {
				t.Errorf("survivors = %v, want %v", survivors, tt.wantSurvivors)
			}
			if !slicesEqual(spared, tt.wantSpared) {
				t.Errorf("spared = %v, want %v", spared, tt.wantSpared)
			}
		})
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRevalidateSweepCandidatesSparesNewlyReferencedBlob is the direct
// acceptance test for the reconfirmation logic in isolation: a blob whose
// mtime is older than the cutoff and which was a candidate in the previous
// pass must NOT come back as a survivor if a reference to it appears after
// the initial scan.
//
// The scenario is built with the exact mechanics of the real bug: a dedup
// write (Put on already-existing content) leaves the file's mtime untouched,
// so aging the blob and then re-adding a Pebble reference reproduces "old
// mtime + fresh reference" without needing a second Put at all.
//
// This tests revalidateSweepCandidates directly, not deleteSweptBlobs -- the
// barrier's actual production entry point is covered by
// TestDeleteSweptBlobsSparesReferencedCandidate below. Keeping both: this one
// pins the filtering logic cheaply and precisely; that one proves the barrier
// cannot be bypassed by whatever calls deleteSweptBlobs.
func TestRevalidateSweepCandidatesSparesNewlyReferencedBlob(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	hash, err := blobs.Put([]byte("stamped message body"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Age the blob file past any plausible cutoff, simulating a year-old blob
	// that a dedup write did not touch.
	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(blobFilePath(dir, hash), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	// Initial scan: nothing references the blob yet, so it is a candidate.
	toDelete := []string{hash}

	// A reference is created between the scan and the delete -- e.g. a
	// redelivery re-derives the identical stamped bytes and writes a new
	// MailMeta pointing at the same hash.
	meta := &bongov1.MailMeta{RawMessage: &bongov1.BlobRef{Hash: hash}}
	mb, _ := proto.Marshal(meta)
	if err := ps.Set([]byte("/user/u1/mail/g1"), mb); err != nil {
		t.Fatalf("Set mail: %v", err)
	}

	survivors, spared, err := revalidateSweepCandidates(ps, nil, toDelete)
	if err != nil {
		t.Fatalf("revalidateSweepCandidates: %v", err)
	}
	if len(survivors) != 0 {
		t.Errorf("survivors = %v, want none: the blob was re-referenced between scan and delete", survivors)
	}
	if len(spared) != 1 || spared[0] != hash {
		t.Errorf("spared = %v, want [%s]", spared, hash)
	}
}

// TestRevalidateSweepCandidatesStillDeletesUnreferencedBlob is the control for
// the test above: a genuinely unreferenced, aged, two-pass candidate must
// still come back as a survivor. Without this, a re-validation step that
// dropped everything (e.g. by mistakenly treating an empty fresh set as "keep
// all") would pass the "spares" test above while silently disabling the GC.
func TestRevalidateSweepCandidatesStillDeletesUnreferencedBlob(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	hash, err := blobs.Put([]byte("truly orphaned"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(blobFilePath(dir, hash), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	survivors, spared, err := revalidateSweepCandidates(ps, nil, []string{hash})
	if err != nil {
		t.Fatalf("revalidateSweepCandidates: %v", err)
	}
	if len(spared) != 0 {
		t.Errorf("spared = %v, want none: nothing references this blob", spared)
	}
	if len(survivors) != 1 || survivors[0] != hash {
		t.Fatalf("survivors = %v, want [%s]", survivors, hash)
	}
}

// TestRevalidateSweepCandidatesSkipsScanWhenNothingToDelete pins that
// re-validation costs nothing on a quiet steady-state pass: an empty toDelete
// must not trigger a fresh reference scan at all. Proven by handing it a spool
// that would fail if read -- if the scan ran, the test would see an error.
func TestRevalidateSweepCandidatesSkipsScanWhenNothingToDelete(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	spool, err := pipeline.NewSpoolStore(dir+"/spool", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	if err := osChmod(dir+"/spool/quarantine", 0o000); err != nil {
		t.Skipf("cannot chmod spool dir (running as root?): %v", err)
	}
	t.Cleanup(func() { _ = osChmod(dir+"/spool/quarantine", 0o750) })

	survivors, spared, err := revalidateSweepCandidates(ps, spool, nil)
	if err != nil {
		t.Fatalf("revalidateSweepCandidates with nothing to delete must not scan (and so cannot fail), got: %v", err)
	}
	if survivors != nil || spared != nil {
		t.Errorf("survivors=%v spared=%v, want both nil for an empty candidate list", survivors, spared)
	}
}

// TestRevalidateSweepCandidatesFailedScanDeletesNothing pins the failure
// posture: if the fresh reference scan itself fails, the candidates must come
// back as "delete nothing", not fall back to the stale first-pass set.
// Under-counting references is exactly the failure mode that loses mail.
func TestRevalidateSweepCandidatesFailedScanDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	spool, err := pipeline.NewSpoolStore(dir+"/spool", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	if err := osChmod(dir+"/spool/quarantine", 0o000); err != nil {
		t.Skipf("cannot chmod spool dir (running as root?): %v", err)
	}
	t.Cleanup(func() { _ = osChmod(dir+"/spool/quarantine", 0o750) })

	survivors, spared, err := revalidateSweepCandidates(ps, spool, []string{"some-candidate-hash"})
	if err == nil {
		t.Fatal("expected an error from an unreadable spool directory")
	}
	if len(survivors) != 0 {
		t.Errorf("survivors = %v, want none: a failed re-validation scan must delete nothing", survivors)
	}
	if len(spared) != 0 {
		t.Errorf("spared = %v, want none on a failed scan", spared)
	}
}

// --- deleteSweptBlobs: the barrier itself -----------------------------------
//
// Re-validation now lives INSIDE deleteSweptBlobs rather than at the
// sweepOrphanedBlobs call site, precisely so that no caller -- present or
// future -- can reach Blobs.Delete without passing through it. The tests
// above prove the filtering logic is correct; these prove it is actually
// applied by the one function that deletes.

// blobGCTestAgent builds an Agent with everything deleteSweptBlobs touches: a
// BlobStore, a PebbleStore (for the internal re-validation scan), and a
// discard logger. spool may be nil -- collectReferencedBlobs tolerates that.
func blobGCTestAgent(t *testing.T, dir string) (*Agent, *store.PebbleStore) {
	t.Helper()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return &Agent{Blobs: blobs, Store: ps, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, ps
}

// TestDeleteSweptBlobsSparesReferencedCandidate is the acceptance test for
// US-006 at the actual barrier: a candidate re-referenced after it was
// identified must not be deleted when deleteSweptBlobs itself is called --
// not just when the extracted helper is called directly. This is the test
// that must fail if the re-validation call is ever removed from
// deleteSweptBlobs, which is exactly what happened when re-validation lived
// only at the sweepOrphanedBlobs call site (see task 14.2 notes: mutation E).
func TestDeleteSweptBlobsSparesReferencedCandidate(t *testing.T) {
	dir := t.TempDir()
	a, ps := blobGCTestAgent(t, dir)

	hash, err := a.Blobs.Put([]byte("stamped message body"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Reference appears after the candidate was identified but before this
	// call to deleteSweptBlobs -- the exact window that lost mail on
	// 2026-08-30.
	meta := &bongov1.MailMeta{RawMessage: &bongov1.BlobRef{Hash: hash}}
	mb, _ := proto.Marshal(meta)
	if err := ps.Set([]byte("/user/u1/mail/g1"), mb); err != nil {
		t.Fatalf("Set mail: %v", err)
	}

	deleted, spared, err := a.deleteSweptBlobs([]string{hash}, false)
	if err != nil {
		t.Fatalf("deleteSweptBlobs: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0: the blob was re-referenced before this call", deleted)
	}
	if len(spared) != 1 || spared[0] != hash {
		t.Errorf("spared = %v, want [%s]", spared, hash)
	}
	if !a.Blobs.Exists(hash) {
		t.Error("blob was deleted despite being re-referenced before the sweep; this is the 2026-08-30 incident reproduced")
	}
}

// TestDeleteSweptBlobsStillDeletesUnreferenced is the control: a genuinely
// unreferenced candidate must still be deleted by deleteSweptBlobs itself.
// Without this, a barrier that dropped every candidate unconditionally would
// pass the test above while silently disabling the GC.
func TestDeleteSweptBlobsStillDeletesUnreferenced(t *testing.T) {
	dir := t.TempDir()
	a, _ := blobGCTestAgent(t, dir)

	hash, err := a.Blobs.Put([]byte("truly orphaned"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	deleted, spared, err := a.deleteSweptBlobs([]string{hash}, false)
	if err != nil {
		t.Fatalf("deleteSweptBlobs: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	if len(spared) != 0 {
		t.Errorf("spared = %v, want none: nothing references this blob", spared)
	}
	if a.Blobs.Exists(hash) {
		t.Error("genuinely unreferenced blob survived; the barrier must not disable the GC")
	}
}

// TestDeleteSweptBlobsDryRunStillRevalidates pins the newest and easiest
// requirement to silently drop: dryRun must gate ONLY the Blobs.Delete call.
// The dryrun candidate list is what an operator reviews to decide whether to
// trust the sweep before re-enabling deletion (task 14.9) -- a dryrun list
// that skipped re-validation would over-report exactly what it exists to let
// the operator verify, showing a blob as "would delete" when a real pass
// would have spared it.
func TestDeleteSweptBlobsDryRunStillRevalidates(t *testing.T) {
	dir := t.TempDir()
	a, ps := blobGCTestAgent(t, dir)

	hash, err := a.Blobs.Put([]byte("stamped message body"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	meta := &bongov1.MailMeta{RawMessage: &bongov1.BlobRef{Hash: hash}}
	mb, _ := proto.Marshal(meta)
	if err := ps.Set([]byte("/user/u1/mail/g1"), mb); err != nil {
		t.Fatalf("Set mail: %v", err)
	}

	deleted, spared, err := a.deleteSweptBlobs([]string{hash}, true)
	if err != nil {
		t.Fatalf("deleteSweptBlobs: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 in dryrun", deleted)
	}
	if len(spared) != 1 || spared[0] != hash {
		t.Errorf("spared = %v, want [%s]: dryrun must still re-validate, not just skip deleting", spared, hash)
	}
}

// TestDeleteSweptBlobsSkipsScanWhenNothingToDelete pins that the barrier
// costs nothing on a quiet steady-state pass, now at the deleteSweptBlobs
// entry point: an empty toDelete must not trigger a fresh reference scan.
// Proven the same way as the revalidateSweepCandidates-level test: a spool
// that errors if it is ever read.
func TestDeleteSweptBlobsSkipsScanWhenNothingToDelete(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	spool, err := pipeline.NewSpoolStore(dir+"/spool", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	if err := osChmod(dir+"/spool/quarantine", 0o000); err != nil {
		t.Skipf("cannot chmod spool dir (running as root?): %v", err)
	}
	t.Cleanup(func() { _ = osChmod(dir+"/spool/quarantine", 0o750) })

	a := &Agent{Blobs: blobs, Store: ps, Spool: spool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	deleted, spared, err := a.deleteSweptBlobs(nil, false)
	if err != nil {
		t.Fatalf("deleteSweptBlobs with nothing to delete must not scan (and so cannot fail), got: %v", err)
	}
	if deleted != 0 || spared != nil {
		t.Errorf("deleted=%d spared=%v, want 0 and nil for an empty candidate list", deleted, spared)
	}
}

// TestDeleteSweptBlobsFailedRevalidationDeletesNothing pins the failure
// posture at the deleteSweptBlobs entry point: if the fresh reference scan
// fails, nothing is deleted and the error is surfaced, not swallowed into a
// zero deleted-count that looks like a quiet, healthy pass.
func TestDeleteSweptBlobsFailedRevalidationDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	spool, err := pipeline.NewSpoolStore(dir+"/spool", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewSpoolStore: %v", err)
	}
	if err := osChmod(dir+"/spool/quarantine", 0o000); err != nil {
		t.Skipf("cannot chmod spool dir (running as root?): %v", err)
	}
	t.Cleanup(func() { _ = osChmod(dir+"/spool/quarantine", 0o750) })

	hash, err := blobs.Put([]byte("some candidate"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	a := &Agent{Blobs: blobs, Store: ps, Spool: spool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	deleted, spared, err := a.deleteSweptBlobs([]string{hash}, false)
	if err == nil {
		t.Fatal("expected an error from an unreadable spool directory")
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0: a failed re-validation scan must delete nothing", deleted)
	}
	if len(spared) != 0 {
		t.Errorf("spared = %v, want none on a failed scan", spared)
	}
	if !blobs.Exists(hash) {
		t.Error("blob was deleted despite a failed re-validation scan")
	}
}

// TestDryRunDeletesNothing pins the dryrun contract.
//
// dryrun is what the documentation tells operators to run before enabling the
// sweep, and it is what this deployment fell back to on 2026-08-27 when the GC
// had to be made safe quickly. Until this test existed nothing anywhere
// asserted that it does not delete: the only occurrence of "dryrun" in any test
// file was a config string-normalisation case. The branch was correct, but a
// regression in it would have removed the one control operators reach for when
// they do not trust the sweep -- and the summary line would still have reported
// dry_run=true while blobs disappeared.
//
// The re-validation barrier now lives inside deleteSweptBlobs too (see
// "deleteSweptBlobs: the barrier itself" above), but that is a second,
// independent guarantee: even if re-validation somehow spared nothing, dryrun
// must still never call Blobs.Delete.
func TestDryRunDeletesNothing(t *testing.T) {
	dir := t.TempDir()
	a, _ := blobGCTestAgent(t, dir)

	hashes := make([]string, 0, 3)
	for _, body := range []string{"one", "two", "three"} {
		h, err := a.Blobs.Put([]byte(body))
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		hashes = append(hashes, h)
	}

	deleted, _, err := a.deleteSweptBlobs(hashes, true)
	if err != nil {
		t.Fatalf("deleteSweptBlobs: %v", err)
	}
	if deleted != 0 {
		t.Errorf("dryrun reported %d deletions, want 0", deleted)
	}
	for _, h := range hashes {
		if !a.Blobs.Exists(h) {
			t.Errorf("dryrun deleted blob %s; the mode that exists to delete nothing "+
				"deleted something, and the summary log would still say dry_run=true", h)
		}
	}
}

// TestNonDryRunDeletes is the control. Without it, a change that made
// deleteSweptBlobs never delete anything would satisfy the test above while
// silently disabling reclamation entirely -- the failure would be unbounded
// disk growth rather than lost mail, and nothing would report it.
func TestNonDryRunDeletes(t *testing.T) {
	dir := t.TempDir()
	a, _ := blobGCTestAgent(t, dir)

	h, err := a.Blobs.Put([]byte("reclaim me"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	deleted, _, err := a.deleteSweptBlobs([]string{h}, false)
	if err != nil {
		t.Fatalf("deleteSweptBlobs: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	if a.Blobs.Exists(h) {
		t.Error("blob survived a non-dryrun sweep; reclamation is not happening")
	}
}

// --- GC observability -------------------------------------------------------
//
// Every other log line in the orphan/blob GC is gated on an action or an error,
// so a sweep that is enabled, running, and correctly finding nothing to reclaim
// produced exactly the same output as one that never started: none at all. That
// cost a live investigation -- with blob_gc flipped to "on" in production, 40
// minutes of log watching could not distinguish "healthy, no orphans" from
// "never ran". Both start-up outcomes must announce themselves.

// captureLogs returns a logger writing to buf at Debug level.
func captureLogs(buf *strings.Builder) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestOrphanGCAnnouncesBlobGCModeOnStart(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	meta, err := store.NewMetadataManager(dir + "/metadata")
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { meta.Close() })

	cfg := config.Defaults()
	cfg.Storage.BlobGC = "on"

	var buf strings.Builder
	a := &Agent{
		Mailboxes: store.NewMailboxStore(ps, &testApplier{store: ps}),
		Metadata:  meta,
		Config:    cfg,
		logger:    captureLogs(&buf),
	}

	// Already-cancelled context: the announcement happens before the loop, so
	// runOrphanGC logs and then returns without ever ticking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.runOrphanGC(ctx, time.Hour, time.Hour)

	got := buf.String()
	if !strings.Contains(got, "orphan-gc: started") {
		t.Errorf("GC start was not logged; an operator cannot tell it is running.\nlogs:\n%s", got)
	}
	if !strings.Contains(got, "blob_gc=on") {
		t.Errorf("start line does not report the configured blob_gc mode, which is the "+
			"setting an operator flips and needs to confirm.\nlogs:\n%s", got)
	}
}

// --- US-007: candidate set integrity across failures and mode changes ------
//
// The two-pass rule ("unreferenced for two consecutive passes") is only
// meaningful if both passes are good ones under the same mode. Two gaps
// undermined that: a failed pass used to carry the stale candidate set
// forward unchanged (return prev), so "two consecutive passes" could mean
// "one real sighting, then an arbitrary run of failures, then one more real
// sighting with no fresh confirmation between them"; and nothing reset the
// set across a blob_gc mode change, so (if a future config-reload feature
// landed) a candidate confirmed under dryrun could be deleted on the very
// first pass after switching to "on".

// blobGCLeaderTestAgent starts a real dev-mode Agent with Raft, waits for it
// to become ready, and returns it. sweepOrphanedBlobs self-gates on
// a.Raft != nil && a.IsLeader(), so exercising the failure paths below for
// real (rather than constructing an Agent by hand, as blobGCTestAgent does
// for the delete barrier) needs a real leader.
//
// Polling a.Ready() rather than a.IsLeader() matters: Run() calls
// WaitForLeader and only assigns a.Spool afterwards, so a test goroutine
// polling IsLeader() directly could observe leadership before Run()'s own
// goroutine has gotten around to setting a.Spool, racing
// collectReferencedBlobs against a nil-vs-set Spool. a.ready is set true only
// at the very end of Run()'s startup sequence, well after a.Spool is
// assigned.
func blobGCLeaderTestAgent(t *testing.T) *Agent {
	t.Helper()
	cfg := config.Defaults()
	cfg.Server.Role = "dev"
	cfg.Web.Port = freePort(t)
	cfg.Server.BindAddr = "127.0.0.1"
	cfg.SMTP.Enabled = false

	a, err := NewAgent(cfg, testLogger())
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !a.Ready() {
		time.Sleep(20 * time.Millisecond)
	}
	if !a.Ready() {
		t.Fatal("agent did not become ready")
	}
	return a
}

// TestBlobGCFailureAndModeChangeReset groups every US-007 scenario that needs
// a real leader Agent (Raft, Pebble, spool, blob store) under ONE Agent
// instance, exercised as subtests rather than one-Agent-per-test.
//
// Each subtest is still an independent, individually red/green test -- run
// selectively via "go test -run TestBlobGCFailureAndModeChangeReset/name" --
// so covering both `return prev` sites in one shared parent does not let a
// half-revert survive; only the subtest for the reverted path goes red.
//
// Sharing one Agent is not just an optimisation: `go test -race` reliably
// found a genuine goroutine leak when this was four separate
// blobGCLeaderTestAgent(t) calls in a row. Run() launches
// `go a.runOrphanGC(ctx, ...)` without joining it, so a test's
// "cancel(); <-done" cleanup does not guarantee that goroutine has actually
// observed ctx.Done() and returned by the time the next test's NewAgent()
// runs. Back-to-back Agents let the next Agent's Pebble allocation reuse the
// address of the previous Agent's freed Config struct while that leaked
// goroutine was still mid-read of Config.Storage.BlobGCMode() for its startup
// log line -- a real, if narrow, pre-existing race in the test harness's
// shutdown sequencing, not in the blob-GC logic this task changed. One Agent
// per test file section avoids re-triggering it; a durable fix would join
// runOrphanGC (and the other `go a.sweepX(...)`-style workers) in Run()'s
// shutdown path, which is out of this task's scope.
func TestBlobGCFailureAndModeChangeReset(t *testing.T) {
	a := blobGCLeaderTestAgent(t)

	// TestSweepOrphanedBlobsFailedCollectReferencesReturnsEmptySet is the
	// isolated acceptance test for the collectReferencedBlobs failure path.
	t.Run("collect references failure returns empty set", func(t *testing.T) {
		quarantineDir := a.dataDir + "/spool/quarantine"
		if err := osChmod(quarantineDir, 0o000); err != nil {
			t.Skipf("cannot chmod spool dir (running as root?): %v", err)
		}
		t.Cleanup(func() { _ = osChmod(quarantineDir, 0o750) })

		got := a.sweepOrphanedBlobs(map[string]bool{"stale-candidate": true}, time.Hour, false)
		if len(got) != 0 {
			t.Fatalf("candidate set after a failed collectReferencedBlobs = %v, want empty: "+
				"a failed pass must not carry stale evidence forward", got)
		}
	})

	// TestSweepOrphanedBlobsFailedListReturnsEmptySet is the isolated
	// acceptance test for the Blobs.List() failure path, kept separate from
	// the subtest above for the same reason.
	t.Run("list failure returns empty set", func(t *testing.T) {
		blobsDir := a.dataDir + "/blobs"
		if err := osChmod(blobsDir, 0o000); err != nil {
			t.Skipf("cannot chmod blobs dir (running as root?): %v", err)
		}
		t.Cleanup(func() { _ = osChmod(blobsDir, 0o750) })

		got := a.sweepOrphanedBlobs(map[string]bool{"stale-candidate": true}, time.Hour, false)
		if len(got) != 0 {
			t.Fatalf("candidate set after a failed Blobs.List = %v, want empty: "+
				"a failed pass must not carry stale evidence forward", got)
		}
	})

	// TestSweepOrphanedBlobsFailedPassDoesNotCarryStaleCandidate is the
	// direct acceptance test for the AC: "a candidate seen before a failed
	// pass is NOT deleted on the next successful pass without a fresh
	// confirmation pass." Drives four real passes: sighted, failed,
	// re-sighted (must not delete), confirmed (must delete).
	t.Run("failed pass does not carry stale candidate forward", func(t *testing.T) {
		hash, err := a.Blobs.Put([]byte("candidate blob for failed-pass test"))
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		old := time.Now().Add(-365 * 24 * time.Hour)
		if err := os.Chtimes(blobFilePath(a.dataDir, hash), old, old); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}

		// Pass 1: successful, unreferenced and old -> first-sighting candidate.
		prev := a.sweepOrphanedBlobs(map[string]bool{}, time.Hour, false)
		if !prev[hash] {
			t.Fatalf("blob should be a candidate after the first successful pass, got %v", prev)
		}
		if !a.Blobs.Exists(hash) {
			t.Fatal("blob must not be deleted on the first sighting")
		}

		// Pass 2: forced failure via an unreadable spool directory. Must
		// discard prev, not carry the pass-1 sighting forward as evidence.
		quarantineDir := a.dataDir + "/spool/quarantine"
		if err := osChmod(quarantineDir, 0o000); err != nil {
			t.Skipf("cannot chmod spool dir (running as root?): %v", err)
		}
		prev = a.sweepOrphanedBlobs(prev, time.Hour, false)
		if err := osChmod(quarantineDir, 0o750); err != nil {
			t.Fatalf("restoring spool dir permissions: %v", err)
		}
		if len(prev) != 0 {
			t.Fatalf("candidate set after a failed pass = %v, want empty", prev)
		}

		// Pass 3: first successful pass after the failure. Without a fresh
		// confirmation this must NOT delete -- the failed pass must cost a
		// full grace interval, not zero.
		prev = a.sweepOrphanedBlobs(prev, time.Hour, false)
		if !a.Blobs.Exists(hash) {
			t.Fatal("blob was deleted on the first pass after a failure, using stale " +
				"pre-failure evidence with no fresh confirmation -- this is exactly the " +
				"unconfirmed-evidence deletion the fix exists to prevent")
		}
		if !prev[hash] {
			t.Fatalf("blob should be re-marked as a fresh candidate on pass 3, got %v", prev)
		}

		// Pass 4: second consecutive good pass after the reset -- now it
		// deletes. Control: without this, a fix that discarded prev on EVERY
		// pass (not just failed ones) would also pass the assertion above
		// while silently disabling the GC. The return is not threaded
		// anywhere: this is the last pass, and the assertion below reads the
		// blob store, not the candidate set.
		a.sweepOrphanedBlobs(prev, time.Hour, false)
		if a.Blobs.Exists(hash) {
			t.Fatal("blob should be deleted after two fresh consecutive confirmations post-failure")
		}
	})

	// TestBlobGCPassResetsCandidatesOnModeChange is the acceptance test for
	// the AC: "a candidate seen only in dry-run is not deleted on the first
	// pass after switching to on." It calls a.blobGCPass directly, threading
	// state across calls the same way runOrphanGC's loop does -- this is the
	// actual function the loop calls each tick, not a re-implementation of
	// its logic in the test, so a reset removed from runOrphanGC's wiring (as
	// opposed to from blobGCCandidatesAcrossModeChange itself) is caught here
	// too.
	t.Run("candidate confirmed only under dryrun is not deleted on first pass after switching to on", func(t *testing.T) {
		t.Cleanup(func() { a.Config.Storage.BlobGC = "off" })
		a.Config.Storage.BlobGC = "dryrun"

		hash, err := a.Blobs.Put([]byte("dryrun-only candidate"))
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(blobFilePath(a.dataDir, hash), old, old); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}

		prev := map[string]bool{}
		prevMode := a.Config.Storage.BlobGCMode() // "dryrun"

		// Pass 1 under dryrun: first sighting.
		prev, prevMode = a.blobGCPass(prev, prevMode, time.Hour)
		if !prev[hash] {
			t.Fatalf("blob should be a first-sighting candidate, got %v", prev)
		}

		// Pass 2 under dryrun: second sighting -- would delete, but dryrun
		// never does.
		prev, prevMode = a.blobGCPass(prev, prevMode, time.Hour)
		if !a.Blobs.Exists(hash) {
			t.Fatal("dryrun must never delete")
		}
		if !prev[hash] {
			t.Fatalf("blob should still be a candidate after the second dryrun pass, got %v", prev)
		}

		// Mode switches to "on" mid-process -- what backlog B9's SIGHUP
		// reload would do. Mutating Config directly here is single-threaded
		// (blobGCPass is only ever called from this same test goroutine), so
		// it simulates a reload without needing a live config-reload feature
		// or racing the background runOrphanGC goroutine this same Agent is
		// running (which only re-reads Config on its own, much longer,
		// ticks).
		a.Config.Storage.BlobGC = "on"

		// Pass 3: first pass under "on". Without the reset this deletes
		// immediately, using a candidate confirmed only under dryrun.
		prev, prevMode = a.blobGCPass(prev, prevMode, time.Hour)
		if !a.Blobs.Exists(hash) {
			t.Fatal("blob was deleted on the first pass after switching to \"on\": a dryrun-only " +
				"confirmation was carried across the mode change with no fresh confirmation")
		}
		if prevMode != "on" {
			t.Fatalf("prevMode = %q, want \"on\"", prevMode)
		}

		// Pass 4: second consecutive good pass under "on" -- now it deletes.
		// Control: without this, the reset could be permanently disabling
		// deletion instead of correctly gating it.
		_, _ = a.blobGCPass(prev, prevMode, time.Hour)
		if a.Blobs.Exists(hash) {
			t.Fatal("blob was never deleted after two fresh confirmations under \"on\": the " +
				"mode-change reset must not permanently disable deletion")
		}
	})
}

// TestBlobGCCandidatesAcrossModeChange is the direct acceptance test for
// blobGCCandidatesAcrossModeChange in isolation, cheap and precise. The
// wiring into runOrphanGC's actual loop is proven separately by the
// TestBlobGCFailureAndModeChangeReset subtest above, via blobGCPass -- the
// same function the loop calls.
func TestBlobGCCandidatesAcrossModeChange(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		prevMode string
		prev     map[string]bool
		want     map[string]bool
	}{
		{"same mode keeps candidates", "on", "on", map[string]bool{"h": true}, map[string]bool{"h": true}},
		{"dryrun to on resets", "on", "dryrun", map[string]bool{"h": true}, map[string]bool{}},
		{"on to dryrun resets", "dryrun", "on", map[string]bool{"h": true}, map[string]bool{}},
		{"off to dryrun resets", "dryrun", "off", map[string]bool{"h": true}, map[string]bool{}},
		{"dryrun to off resets", "off", "dryrun", map[string]bool{"h": true}, map[string]bool{}},
		{"same mode, empty prev stays empty", "off", "off", map[string]bool{}, map[string]bool{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blobGCCandidatesAcrossModeChange(tt.prev, tt.mode, tt.prevMode)
			if !mapsEqual(got, tt.want) {
				t.Errorf("blobGCCandidatesAcrossModeChange(%v, %q, %q) = %v, want %v",
					tt.prev, tt.mode, tt.prevMode, got, tt.want)
			}
		})
	}
}

func mapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestOrphanGCAnnouncesWhenItDoesNotStart(t *testing.T) {
	var buf strings.Builder
	// No Mailboxes/Metadata: the nil guard trips, as on an edge or agent role.
	a := &Agent{Config: config.Defaults(), logger: captureLogs(&buf)}

	a.runOrphanGC(context.Background(), time.Hour, time.Hour)

	got := buf.String()
	if !strings.Contains(got, "not started") {
		t.Errorf("GC bailed out silently; a configured blob_gc would do nothing with no "+
			"trace of why.\nlogs:\n%s", got)
	}
	if strings.Contains(got, "orphan-gc: started") {
		t.Errorf("GC claimed to have started when it returned early.\nlogs:\n%s", got)
	}
}
