package deliver

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func multipartWithAttachment(subject, attB64 string) string {
	return "From: bob@example.com\r\nTo: alice@example.com\r\nSubject: " + subject + "\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-Id: <t@example.com>\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\n" + subject + "\r\n" +
		"--B\r\nContent-Type: application/pdf; name=\"doc.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"doc.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + attB64 + "\r\n--B--\r\n"
}

func dedupProc(t *testing.T, minSize int) (*DeliverProcessor, *mockApplier, *store.BlobStore) {
	t.Helper()
	bs, err := store.NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	applier := &mockApplier{}
	resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
	proc := NewDeliverProcessor(applier, resolver, newTestLogger())
	proc.SetAttachmentDedup(bs, minSize)
	return proc, applier, bs
}

func deliver(t *testing.T, proc *DeliverProcessor, raw, blobHash string) {
	t.Helper()
	env := &bongov1.QueueEnvelope{
		Id:         "env-" + blobHash,
		Sender:     "bob@example.com",
		Recipients: []string{"alice@example.com"},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   blobHash,
	}
	if _, err := proc.Process(context.Background(), env, strings.NewReader(raw)); err != nil {
		t.Fatalf("Process: %v", err)
	}
}

// TestDeliverSplitsLargeAttachment: an inbound message with a >= threshold
// attachment is stored as a skeleton + attachment blob, and reconstructs
// byte-exact.
func TestDeliverSplitsLargeAttachment(t *testing.T) {
	proc, applier, bs := dedupProc(t, 1024)
	att := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5Cg", 60) // ~3 KB
	raw := multipartWithAttachment("has attachment", att)

	deliver(t, proc, raw, "wholehash")

	if len(applier.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(applier.commands))
	}
	meta := unmarshalMailMeta(t, applier.commands[0])

	if len(meta.Layout) == 0 {
		t.Fatal("expected a split (non-empty layout)")
	}
	if len(meta.Attachments) != 1 || meta.Attachments[0].Hash == "" {
		t.Fatalf("expected 1 attachment with a hash, got %+v", meta.Attachments)
	}
	if meta.RawMessage.Hash == "wholehash" {
		t.Fatal("RawMessage should point at the skeleton, not the whole-raw blob")
	}
	if meta.RawMessage.Size != int64(len(raw)) {
		t.Errorf("RawMessage.Size = %d, want full size %d", meta.RawMessage.Size, len(raw))
	}
	got, err := store.RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}
	if !bytes.Equal(got, []byte(raw)) {
		t.Fatal("delivered split message did not reconstruct byte-exact")
	}
}

// TestDeliverBelowThresholdWholeRaw: a small attachment keeps whole-raw
// storage (no split). Since task 10.5, the deliver stage always persists the
// (trace-header-stamped) message through the blob store when one is
// configured, rather than reusing the SMTP-time env.BlobHash placeholder, so
// RawMessage.Hash is now a real content hash rather than "wholehash" -
// verified here by reconstructing through store.RawMessageBytes rather than
// comparing against the original placeholder (see task 10.5 RISK 1: any
// assertion on stored bytes must go through the same path every reader uses).
func TestDeliverBelowThresholdWholeRaw(t *testing.T) {
	proc, applier, bs := dedupProc(t, 1<<20) // 1 MB threshold
	raw := multipartWithAttachment("small", "QUJDRA==")

	deliver(t, proc, raw, "wholehash")

	meta := unmarshalMailMeta(t, applier.commands[0])
	if len(meta.Layout) != 0 {
		t.Fatal("below threshold must not split")
	}
	if meta.RawMessage.Hash == "wholehash" {
		t.Error("RawMessage.Hash should be a real content hash of the persisted (stamped) message, not the SMTP-time placeholder")
	}
	got, err := store.RawMessageBytes(bs, meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}
	if !bytes.Equal(got, []byte(raw)) {
		t.Errorf("stored whole-raw message does not match the original (no trace headers were configured for this harness, so content should be unchanged):\ngot  %q\nwant %q", got, raw)
	}
	// Attachment metadata is still recorded, but without a blob hash.
	if len(meta.Attachments) == 1 && meta.Attachments[0].Hash != "" {
		t.Error("whole-raw attachment should not carry a blob hash")
	}
}

// TestDeliverDisabledKeepsWholeRaw: with dedup disabled, storage is unchanged.
func TestDeliverDisabledKeepsWholeRaw(t *testing.T) {
	applier := &mockApplier{}
	resolver := &mockResolver{mapping: map[string]string{"alice@example.com": "user-alice"}}
	proc := NewDeliverProcessor(applier, resolver, newTestLogger()) // no SetAttachmentDedup
	raw := multipartWithAttachment("x", strings.Repeat("QUJD", 2000))

	deliver(t, proc, raw, "wholehash")
	meta := unmarshalMailMeta(t, applier.commands[0])
	if len(meta.Layout) != 0 || meta.RawMessage.Hash != "wholehash" {
		t.Fatal("dedup disabled must keep whole-raw storage")
	}
}

// TestDeliverDedupSharedAttachment: the same attachment across two different
// messages is stored once.
func TestDeliverDedupSharedAttachment(t *testing.T) {
	proc, _, bs := dedupProc(t, 1024)
	att := strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5Cg", 60)

	deliver(t, proc, multipartWithAttachment("first message", att), "hash1")
	countAfterFirst, _ := bs.Stats()

	deliver(t, proc, multipartWithAttachment("a totally different second one", att), "hash2")
	countAfterSecond, _ := bs.Stats()

	// The second delivery adds only its skeleton blob; the attachment is reused.
	if grew := countAfterSecond - countAfterFirst; grew != 1 {
		t.Fatalf("second delivery added %d blobs, want 1 (attachment deduplicated)", grew)
	}
}
