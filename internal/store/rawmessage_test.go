package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// fakeBlobs is an in-memory content-addressed store for tests.
type fakeBlobs map[string][]byte

func (f fakeBlobs) put(b []byte) string {
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	f[h] = b
	return h
}

func (f fakeBlobs) get(h string) ([]byte, error) {
	b, ok := f[h]
	if !ok {
		return nil, fmt.Errorf("blob %s not found", h)
	}
	return b, nil
}

// storeSplit stores a SplitMessage into fakeBlobs and returns the MailMeta that
// references it, mirroring what the ingest path (task 9.4) will do.
func storeSplit(f fakeBlobs, sm *SplitMessage) *bongov1.MailMeta {
	skHash := f.put(sm.Skeleton)
	for i := range sm.Attachments {
		f.put(sm.Encoded[i]) // hash already equals sm.Attachments[i].Hash
	}
	return &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: skHash, Size: int64(len(sm.Skeleton)), ContentType: "message/rfc822"},
		Attachments: sm.Attachments,
		Layout:      sm.Layout,
	}
}

func bigAttachmentMessage(subject string) string {
	att := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5Cg", 60) // ~3 KB
	return "From: a@ext.test\r\nTo: b@example.com\r\nSubject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\n" + subject + "\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"doc.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + att + "\r\n--B--\r\n"
}

func TestFullRawMessageRoundTrip(t *testing.T) {
	raw := []byte(bigAttachmentMessage("hello"))
	f := fakeBlobs{}

	sm := PrepareSplit(raw, 1024)
	if sm == nil {
		t.Fatal("PrepareSplit returned nil for a message with a large attachment")
	}
	if len(sm.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(sm.Attachments))
	}
	// Skeleton must be materially smaller than the raw (attachment removed).
	if len(sm.Skeleton) >= len(raw) {
		t.Fatalf("skeleton (%d) not smaller than raw (%d)", len(sm.Skeleton), len(raw))
	}

	meta := storeSplit(f, sm)
	if len(meta.Layout) == 0 {
		t.Fatal("split record must carry a non-empty layout")
	}

	got, err := FullRawMessage(meta, f.get)
	if err != nil {
		t.Fatalf("FullRawMessage: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("reconstruction not byte-exact:\nwant %d bytes\ngot  %d bytes", len(raw), len(got))
	}
}

func TestFullRawMessageLegacyPassthrough(t *testing.T) {
	raw := []byte("From: a@ext.test\r\nSubject: legacy\r\n\r\nwhole raw, no layout\r\n")
	f := fakeBlobs{}
	h := f.put(raw)

	meta := &bongov1.MailMeta{RawMessage: &bongov1.BlobRef{Hash: h}} // no Layout
	got, err := FullRawMessage(meta, f.get)
	if err != nil {
		t.Fatalf("FullRawMessage: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("legacy passthrough altered the message")
	}
}

func TestPrepareSplitBelowThresholdIsNil(t *testing.T) {
	// A small attachment under the threshold must not trigger a split.
	raw := []byte("Subject: small\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n" +
		"--B\r\nContent-Type: application/octet-stream; name=\"t.bin\"\r\n" +
		"Content-Disposition: attachment; filename=\"t.bin\"\r\n\r\nAAAA\r\n--B--\r\n")
	if sm := PrepareSplit(raw, 4096); sm != nil {
		t.Fatalf("PrepareSplit should be nil below threshold, got %d attachments", len(sm.Attachments))
	}
}

func TestFullRawMessageSharedAttachmentDedup(t *testing.T) {
	f := fakeBlobs{}
	raw1 := []byte(bigAttachmentMessage("first"))
	raw2 := []byte(bigAttachmentMessage("second is quite different"))

	sm1 := PrepareSplit(raw1, 1024)
	sm2 := PrepareSplit(raw2, 1024)
	if sm1 == nil || sm2 == nil {
		t.Fatal("expected both messages to split")
	}
	// Same attachment content -> same hash -> one stored blob.
	if sm1.Attachments[0].Hash != sm2.Attachments[0].Hash {
		t.Fatal("shared attachment should hash equal")
	}

	meta1 := storeSplit(f, sm1)
	before := len(f)
	meta2 := storeSplit(f, sm2)
	// Storing the second message adds its skeleton but reuses the attachment blob.
	if grew := len(f) - before; grew != 1 {
		t.Fatalf("second message added %d blobs, want 1 (skeleton only; attachment deduped)", grew)
	}

	for i, meta := range []*bongov1.MailMeta{meta1, meta2} {
		got, err := FullRawMessage(meta, f.get)
		if err != nil {
			t.Fatalf("msg %d FullRawMessage: %v", i, err)
		}
		want := [][]byte{raw1, raw2}[i]
		if !bytes.Equal(got, want) {
			t.Fatalf("msg %d not byte-exact", i)
		}
	}
}

// TestRawMessageBytesRealStore exercises the helper every reader uses against a
// real BlobStore, for both a split record and a legacy whole-raw record.
func TestRawMessageBytesRealStore(t *testing.T) {
	bs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	// Split record.
	raw := []byte(bigAttachmentMessage("real store"))
	sm := PrepareSplit(raw, 1024)
	if sm == nil {
		t.Fatal("expected a split")
	}
	skHash, err := bs.Put(sm.Skeleton)
	if err != nil {
		t.Fatalf("put skeleton: %v", err)
	}
	for _, enc := range sm.Encoded {
		if _, err := bs.Put(enc); err != nil {
			t.Fatalf("put attachment: %v", err)
		}
	}
	splitMeta := &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: skHash, Size: int64(len(raw))},
		Attachments: sm.Attachments,
		Layout:      sm.Layout,
	}
	got, err := RawMessageBytes(bs, splitMeta)
	if err != nil {
		t.Fatalf("RawMessageBytes(split): %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("split record did not reconstruct byte-exact via real store")
	}

	// Legacy record.
	legacy := []byte("From: x@y\r\nSubject: legacy\r\n\r\nbody\r\n")
	lh, _ := bs.Put(legacy)
	legacyMeta := &bongov1.MailMeta{RawMessage: &bongov1.BlobRef{Hash: lh}}
	got2, err := RawMessageBytes(bs, legacyMeta)
	if err != nil {
		t.Fatalf("RawMessageBytes(legacy): %v", err)
	}
	if !bytes.Equal(got2, legacy) {
		t.Fatal("legacy passthrough altered the message via real store")
	}
}

// TestStoreSplitRoundTrip: StoreSplit stores + self-checks, and the result
// reconstructs byte-exact (this is exactly what the queue relay relies on).
func TestStoreSplitRoundTrip(t *testing.T) {
	bs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	raw := []byte(bigAttachmentMessage("store split"))

	split, err := StoreSplit(bs, raw, 1024)
	if err != nil {
		t.Fatalf("StoreSplit: %v", err)
	}
	if split == nil {
		t.Fatal("expected a split")
	}
	if len(split.Layout) == 0 || split.SkeletonHash == "" {
		t.Fatal("split result missing skeleton/layout")
	}

	// Reconstruct exactly as the queue worker does, from the split refs.
	meta := &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: split.SkeletonHash},
		Attachments: split.Attachments,
		Layout:      split.Layout,
	}
	got, err := RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("StoreSplit result did not reconstruct byte-exact")
	}

	// Below threshold: no split.
	small := []byte("Subject: tiny\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nhi\r\n--B--\r\n")
	if s, err := StoreSplit(bs, small, 1<<20); s != nil || err != nil {
		t.Fatalf("below-threshold StoreSplit = (%v, %v), want (nil, nil)", s, err)
	}
}

// TestFullRawMessageCorruptLayout: a layout that references a missing attachment
// blob fails cleanly rather than panicking.
func TestFullRawMessageCorruptLayout(t *testing.T) {
	f := fakeBlobs{}
	sm := PrepareSplit([]byte(bigAttachmentMessage("x")), 1024)
	meta := storeSplit(f, sm)
	delete(f, meta.Attachments[0].Hash) // drop the attachment blob

	if _, err := FullRawMessage(meta, f.get); err == nil {
		t.Fatal("expected an error when an attachment blob is missing")
	}
}
