package agent

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func dedupWindowMessage(subject string) []byte {
	att := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5Cg", 60)
	return []byte("From: a@ext.test\r\nTo: b@example.com\r\nSubject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\n" + subject + "\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"doc.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + att + "\r\n--B--\r\n")
}

// backdateAllBlobs makes every stored blob look two hours old.
func backdateAllBlobs(t *testing.T, a *Agent, dir string) {
	t.Helper()
	hashes, err := a.Blobs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, h := range hashes {
		if err := os.Chtimes(blobFilePath(dir, h), old, old); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}
}

// planOrphans returns the candidates a two-pass sweep would delete, with the
// cutoff it used.
func planOrphans(t *testing.T, a *Agent) ([]string, time.Time) {
	t.Helper()
	hashes, _ := a.Blobs.List()
	mtimes := map[string]time.Time{}
	for _, h := range hashes {
		mt, err := a.Blobs.ModTime(h)
		if err != nil {
			t.Fatalf("ModTime: %v", err)
		}
		mtimes[h] = mt
	}
	cutoff := time.Now().Add(-time.Hour)
	cur, _ := planBlobSweep(map[string]bool{}, mtimes, map[string]bool{}, cutoff)
	_, toDelete := planBlobSweep(map[string]bool{}, mtimes, cur, cutoff)
	return toDelete, cutoff
}

func commitSplitMeta(t *testing.T, ps *store.PebbleStore, key string, sp *store.SplitStored) *bongov1.MailMeta {
	t.Helper()
	meta := &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: sp.SkeletonHash},
		Attachments: sp.Attachments,
		Layout:      sp.Layout,
	}
	mb, _ := proto.Marshal(meta)
	if err := ps.Set([]byte(key), mb); err != nil {
		t.Fatalf("Set: %v", err)
	}
	return meta
}

// The GC re-validation pass runs between StoreSplit (which dedups against an
// old, now-unreferenced attachment blob) and the MailMeta commit.
func TestDedupHitSurvivesGCRevalidationBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	a, ps := blobGCTestAgent(t, dir)

	if _, err := store.StoreSplit(a.Blobs, dedupWindowMessage("first"), 1024); err != nil {
		t.Fatalf("StoreSplit first: %v", err)
	}
	// Its MailMeta is gone: the old attachment blob is now unreferenced.
	backdateAllBlobs(t, a, dir)
	toDelete, cutoff := planOrphans(t, a)
	if len(toDelete) == 0 {
		t.Fatal("setup: expected orphaned blobs to be planned for deletion")
	}

	raw2 := dedupWindowMessage("second")
	sp, err := store.StoreSplit(a.Blobs, raw2, 1024)
	if err != nil || sp == nil {
		t.Fatalf("StoreSplit second: %v %v", sp, err)
	}
	if sp.BlobsDeduped == 0 {
		t.Fatal("setup: second message must dedup against the old attachment blob")
	}

	// GC re-validation runs here: after StoreSplit, before the commit.
	if _, _, err := a.deleteSweptBlobs(toDelete, cutoff, false); err != nil {
		t.Fatalf("deleteSweptBlobs: %v", err)
	}

	meta := commitSplitMeta(t, ps, "/user/u1/mail/g2", sp)
	got, err := store.RawMessageBytes(a.Blobs, meta)
	if err != nil {
		t.Fatalf("message references a missing blob after GC: %v", err)
	}
	if !bytes.Equal(got, raw2) {
		t.Error("reconstructed message differs from the original")
	}
}

// Touching alone is not enough: the plan read the old mtime, the dedup hit
// touched the blob afterwards, and the delete path must notice.
func TestDeleteSweptBlobsSparesBlobTouchedAfterPlan(t *testing.T) {
	dir := t.TempDir()
	a, _ := blobGCTestAgent(t, dir)

	data := []byte("shared attachment bytes")
	hash, err := a.Blobs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	backdateAllBlobs(t, a, dir)
	toDelete, cutoff := planOrphans(t, a)
	if len(toDelete) != 1 || toDelete[0] != hash {
		t.Fatalf("setup: toDelete = %v", toDelete)
	}

	if _, err := a.Blobs.Put(data); err != nil { // dedup hit
		t.Fatalf("Put dedup: %v", err)
	}

	deleted, spared, err := a.deleteSweptBlobs(toDelete, cutoff, false)
	if err != nil {
		t.Fatalf("deleteSweptBlobs: %v", err)
	}
	if deleted != 0 || len(spared) != 1 {
		t.Errorf("deleted=%d spared=%v, want 0 and [%s]", deleted, spared, hash)
	}
	if !a.Blobs.Exists(hash) {
		t.Error("blob deduped after the plan was deleted")
	}

	// Dry run reports it as spared too.
	_, sparedDry, err := a.deleteSweptBlobs(toDelete, cutoff, true)
	if err != nil || len(sparedDry) != 1 {
		t.Errorf("dryrun spared=%v err=%v, want the touched blob spared", sparedDry, err)
	}
}

// Control: an old blob nobody touched is still reclaimed.
func TestDeleteSweptBlobsStillDeletesUntouchedOldBlob(t *testing.T) {
	dir := t.TempDir()
	a, _ := blobGCTestAgent(t, dir)

	hash, err := a.Blobs.Put([]byte("really orphaned, never re-put"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	backdateAllBlobs(t, a, dir)
	toDelete, cutoff := planOrphans(t, a)

	deleted, spared, err := a.deleteSweptBlobs(toDelete, cutoff, false)
	if err != nil || deleted != 1 || len(spared) != 0 {
		t.Fatalf("deleted=%d spared=%v err=%v, want 1 deleted", deleted, spared, err)
	}
	if a.Blobs.Exists(hash) {
		t.Error("orphaned old blob survived")
	}
}

// The production caller must hand deleteSweptBlobs the plan's own cutoff. A
// dedup write landing between plan and delete is only protected if the delete
// compares against the cutoff the plan used, not against the delete-time clock.
func TestSweepOrphanedBlobsSparesDedupWriteBetweenPlanAndDelete(t *testing.T) {
	a := blobGCLeaderTestAgent(t)
	dir := a.dataDir

	data := []byte("orphaned until a delivery dedups against it")
	hash, err := a.Blobs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	backdateAllBlobs(t, a, dir)

	// Pass 1 seeds the candidate set; nothing is deleted on first sighting.
	prev := a.sweepOrphanedBlobs(map[string]bool{}, time.Hour, false)
	if !prev[hash] {
		t.Fatalf("setup: %s should be a candidate after pass 1, got %v", hash, prev)
	}

	// Pass 2: the hook lands a dedup Put after the plan, before the delete.
	hookRan := false
	a.blobSweepBeforeDelete = func() {
		hookRan = true
		if _, err := a.Blobs.Put(data); err != nil {
			t.Errorf("dedup Put: %v", err)
		}
	}
	a.sweepOrphanedBlobs(prev, time.Hour, false)
	if !hookRan {
		t.Fatal("hook did not run: the blob was not planned for deletion")
	}
	if !a.Blobs.Exists(hash) {
		t.Error("blob deduped between plan and delete was deleted")
	}
}
