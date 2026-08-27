package handlers

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jhillyerd/enmime"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestSaveDraftLargeAttachmentSplitsAndReconstructs exercises the outbound split
// (task 9.6): a composed message with a large attachment is stored as a skeleton
// + deduplicated attachment blob, and reconstructs byte-exact with the attachment
// decoding back to the original bytes.
func TestSaveDraftLargeAttachmentSplitsAndReconstructs(t *testing.T) {
	e := newComposeRichTestEnv(t)
	e.handler.SetAttachmentDedup(1024)
	userID, email := "user-split-draft", "sd@example.com"

	attContent := bytes.Repeat([]byte("ATTACHMENT-PAYLOAD-"), 500) // ~9.5 KB

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("to", "someone@example.com")
	_ = mw.WriteField("subject", "big draft")
	_ = mw.WriteField("body", "see attached")
	_ = mw.WriteField("body_mode", "plain")
	fw, err := mw.CreateFormFile("attachments", "doc.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(attContent); err != nil {
		t.Fatalf("write attachment: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/compose/draft", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withUserContext(req, userID, email)
	rec := httptest.NewRecorder()
	e.handler.SaveDraft(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("SaveDraft: want 303, got %d: %s", rec.Code, rec.Body.String())
	}

	msgs, err := e.mailboxes.ListMessages(userID, "Drafts")
	if err != nil || len(msgs) == 0 {
		t.Fatalf("no draft stored: %v", err)
	}
	data, err := e.ps.Get([]byte(fmt.Sprintf("/user/%s/mail/%s", userID, msgs[0].MailGuid)))
	if err != nil {
		t.Fatalf("get draft meta: %v", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}

	// It must be a split record carrying a real attachment blob hash.
	if len(meta.Layout) == 0 {
		t.Fatal("large-attachment draft should be split (non-empty layout)")
	}
	if len(meta.Attachments) != 1 || meta.Attachments[0].Hash == "" {
		t.Fatalf("expected 1 attachment with a blob hash, got %+v", meta.Attachments)
	}

	// Reconstruct and confirm the attachment decodes to the original bytes.
	raw, err := store.RawMessageBytes(e.blobs, &meta)
	if err != nil {
		t.Fatalf("RawMessageBytes: %v", err)
	}
	parsed, err := enmime.ReadEnvelope(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse reconstructed: %v", err)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("reconstructed attachments = %d, want 1", len(parsed.Attachments))
	}
	if !bytes.Equal(parsed.Attachments[0].Content, attContent) {
		t.Fatal("reconstructed draft attachment does not match the original bytes")
	}
}
