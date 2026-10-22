package handlers

import (
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
	"github.com/bongo-project/bongo/internal/testutil"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestApplyEnvelopeBodyDeepMIMEIsFast requires the mail view to render a
// message that costs enmime seconds to parse without stalling, and to flag it so
// the template can tell the user the content was not shown.
func TestApplyEnvelopeBodyDeepMIMEIsFast(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	hash, err := bs.Put(testutil.DeepUnclosedMultipart(24))
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}
	meta := &bongov1.MailMeta{RawMessage: &bongov1.BlobRef{Hash: hash}}

	h := NewMailHandler(ps, bs, nil, discardLogger())
	detail := templates.MessageDetail{Mailbox: "INBOX", UID: 1}

	start := time.Now()
	h.applyEnvelopeBody(&detail, meta)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("applyEnvelopeBody took %v on a deeply nested message, want < 1s", elapsed)
	}
	if !detail.MIMETooComplex {
		t.Error("MIMETooComplex = false, want true so the view shows the notice")
	}
	if detail.TextBody != "" || detail.HTMLBody != "" {
		t.Errorf("degraded view carries body content: text=%q html=%q", detail.TextBody, detail.HTMLBody)
	}
}

// TestApplyEnvelopeBodyNormalMailNotFlagged is the paired control.
func TestApplyEnvelopeBodyNormalMailNotFlagged(t *testing.T) {
	dir := t.TempDir()
	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	bs, err := store.NewBlobStore(dir + "/blobs")
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	raw, _ := inboundZipRaw()
	hash, err := bs.Put([]byte(raw))
	if err != nil {
		t.Fatalf("blob Put: %v", err)
	}
	h := NewMailHandler(ps, bs, nil, discardLogger())
	detail := templates.MessageDetail{Mailbox: "INBOX", UID: 1}
	h.applyEnvelopeBody(&detail, &bongov1.MailMeta{RawMessage: &bongov1.BlobRef{Hash: hash}})
	if detail.MIMETooComplex {
		t.Error("ordinary message flagged as too complex")
	}
	if detail.TextBody == "" {
		t.Error("ordinary message lost its text body")
	}
}
