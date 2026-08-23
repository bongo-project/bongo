package agent

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/pipeline"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
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
