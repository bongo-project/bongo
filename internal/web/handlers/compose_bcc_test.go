package handlers

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// hasBccHeader reports whether raw carries a Bcc header line.
func hasBccHeader(raw string) bool {
	head, _, _ := strings.Cut(raw, "\r\n\r\n")
	return regexp.MustCompile(`(?im)^bcc:`).MatchString(head)
}

func bccFields(to, cc, bcc string) url.Values {
	return url.Values{"to": {to}, "cc": {cc}, "bcc": {bcc}, "subject": {"blind"}, "body": {"hello"}}
}

func envelopeFor(t *testing.T, env *composeSendAsEnv, rcpt string) *bongov1.QueueEnvelope {
	t.Helper()
	for _, e := range env.spool.envelopes() {
		if len(e.Recipients) == 1 && e.Recipients[0] == rcpt {
			return e
		}
	}
	t.Fatalf("no envelope for %s", rcpt)
	return nil
}

func sentRaw(t *testing.T, env *composeSendAsEnv, uid string) string {
	t.Helper()
	msgs, _ := env.mailboxes.ListMessages(uid, "Sent")
	if len(msgs) != 1 {
		t.Fatalf("Sent messages = %d, want 1", len(msgs))
	}
	meta := env.handler.loadMailMeta(uid, msgs[0].MailGuid)
	if meta == nil {
		t.Fatal("no Sent meta")
	}
	return blobText(t, env, meta.RawMessage.Hash)
}

func TestComposeBccLocalRecipientReceivesWithoutHeader(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	w := postSendForm(env, uid, email, sendID(), bccFields("bob@example.com", "", "carol@example.com"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	for _, rcpt := range []string{"bob@example.com", "carol@example.com"} {
		raw := blobText(t, env, envelopeFor(t, env, rcpt).BlobHash)
		if hasBccHeader(raw) {
			t.Errorf("delivered copy for %s has a Bcc header:\n%s", rcpt, raw)
		}
		if strings.Contains(raw, "carol@example.com") {
			t.Errorf("delivered copy for %s names the Bcc recipient", rcpt)
		}
	}
}

func TestComposeBccExternalQueuedWithoutHeader(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	w := postSendForm(env, uid, email, sendID(), bccFields("bob@example.com", "", "hidden@external.example"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	pending, _ := env.qs.ListPending(50)
	if len(pending) != 1 {
		t.Fatalf("pending=%d want 1", len(pending))
	}
	m := pending[0]
	if len(m.Recipients) != 1 || m.Recipients[0] != "hidden@external.example" {
		t.Fatalf("envelope recipients = %v, want the Bcc address", m.Recipients)
	}
	raw := blobText(t, env, m.BlobHash)
	if hasBccHeader(raw) || strings.Contains(raw, "hidden@external.example") {
		t.Errorf("queued raw reveals the Bcc recipient:\n%s", raw)
	}
}

func TestComposeBccSentCopyKeepsHeader(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	w := postSendForm(env, uid, email, sendID(), bccFields("bob@example.com", "", "carol@example.com"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	raw := sentRaw(t, env, uid)
	if !hasBccHeader(raw) || !strings.Contains(raw, "Bcc: carol@example.com\r\n") {
		t.Errorf("Sent copy lacks the Bcc header:\n%s", raw)
	}
}

func TestComposeBccCountsTowardsRecipientCap(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.SetMaxRecipients(2)
	if w := postSendForm(env, uid, email, sendID(), bccFields("bob@example.com", "carol@example.com", "")); w.Code != http.StatusSeeOther {
		t.Fatalf("To+Cc at the cap: status=%d: %s", w.Code, w.Body.String())
	}
	w := postSendForm(env, uid, email, sendID(), bccFields("bob@example.com", "carol@example.com", "x@external.example"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("To+Cc+Bcc over the cap: status=%d want 400", w.Code)
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 1 {
		t.Errorf("Sent=%d, the refused send must store nothing", len(sent))
	}
}

func TestComposeBccGroupHonoursPostingPolicy(t *testing.T) {
	env, gs, g := newGroupComposeEnv(t)
	setGroupPolicy(t, gs, g, "owners")
	w := postSendForm(env, "u-alice", "alice@example.com", sendID(), bccFields("dave@example.com", "", "team@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403: %s", w.Code, w.Body.String())
	}
	if got := envelopeRecipients(env); len(got) != 0 {
		t.Errorf("a refused send spooled for %v", got)
	}
}

func TestComposeBccGroupMembersGetNoBccHeader(t *testing.T) {
	env, _, _ := newGroupComposeEnv(t)
	w := postSendForm(env, "u-alice", "alice@example.com", sendID(), bccFields("dave@example.com", "", "team@example.com"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	raw := blobText(t, env, envelopeFor(t, env, "bob@example.com").BlobHash)
	if hasBccHeader(raw) || strings.Contains(raw, "team@example.com") {
		t.Errorf("member copy reveals the Bcc group:\n%s", raw)
	}
}

func TestComposeBccChangedOnRetryConflicts(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	id := sendID()
	if w := postSendForm(env, uid, email, id, bccFields("bob@example.com", "", "carol@example.com")); w.Code != http.StatusSeeOther {
		t.Fatalf("first status=%d: %s", w.Code, w.Body.String())
	}
	if w := postSendForm(env, uid, email, id, bccFields("bob@example.com", "", "other@external.example")); w.Code != http.StatusConflict {
		t.Fatalf("changed Bcc status=%d want 409", w.Code)
	}
	if w := postSendForm(env, uid, email, id, bccFields("bob@example.com", "", "carol@example.com")); w.Code != http.StatusSeeOther {
		t.Fatalf("identical retry status=%d want 303", w.Code)
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 1 {
		t.Errorf("Sent=%d want 1", len(sent))
	}
}

func TestComposeBccOnlySend(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	w := postSendForm(env, uid, email, sendID(), bccFields("", "", "carol@example.com"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	raw := blobText(t, env, envelopeFor(t, env, "carol@example.com").BlobHash)
	if hasBccHeader(raw) {
		t.Errorf("delivered copy has a Bcc header:\n%s", raw)
	}
}

func TestComposeNoRecipientRefused(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	if w := postSendForm(env, uid, email, sendID(), bccFields("", "", "")); w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestComposeBccWithRichBody(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	f := bccFields("bob@example.com", "", "carol@example.com")
	f.Set("body_mode", "rich")
	f.Set("body", "<p>hi</p>")
	if w := postSendForm(env, uid, email, sendID(), f); w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	if raw := blobText(t, env, envelopeFor(t, env, "bob@example.com").BlobHash); hasBccHeader(raw) {
		t.Error("rich delivered copy has a Bcc header")
	}
	if raw := sentRaw(t, env, uid); !hasBccHeader(raw) {
		t.Error("rich Sent copy lacks the Bcc header")
	}
}

func TestSaveDraftKeepsBccAndReopens(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID, email := "user-draft-bcc", "draft@example.com"
	form := url.Values{
		"to": {"someone@example.com"}, "bcc": {"hidden@example.net"},
		"subject": {"d"}, "body": {"x"}, "body_mode": {"plain"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/draft", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	rec := httptest.NewRecorder()
	env.handler.SaveDraft(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("SaveDraft status=%d: %s", rec.Code, rec.Body.String())
	}
	raw, uid := env.rawDraftMessage(t, userID)
	if !strings.Contains(string(raw), "Bcc: hidden@example.net\r\n") {
		t.Errorf("draft raw lacks the Bcc header:\n%s", raw)
	}
	page := env.getEditDraft(t, userID, email, uid).Body.String()
	if !regexp.MustCompile(`name="bcc"[^>]*value="hidden@example.net"`).MatchString(page) {
		t.Errorf("reopened draft does not fill the Bcc field")
	}
}

// With a split attachment the Sent copy (Bcc header) and the delivered copy
// (none) are two skeletons over the same attachment blobs; the queue entry
// must reconstruct to the message without a Bcc header.
func TestComposeBccSplitAttachment(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.SetAttachmentDedup(1024)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("send_id", sendID())
	_ = mw.WriteField("to", "bob@example.com")
	_ = mw.WriteField("bcc", "hidden@external.example")
	_ = mw.WriteField("subject", "big")
	_ = mw.WriteField("body", "see attached")
	fw, err := mw.CreateFormFile("attachments", "doc.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(bytes.Repeat([]byte("ATTACHMENT-PAYLOAD-"), 500))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/compose/send", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withUserContext(req, uid, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}

	sent, _ := env.mailboxes.ListMessages(uid, "Sent")
	sentMeta := env.handler.loadMailMeta(uid, sent[0].MailGuid)
	if len(sentMeta.Layout) == 0 {
		t.Fatal("Sent copy was not split")
	}
	sentFull, err := store.RawMessageBytes(env.blobs, sentMeta)
	if err != nil || !hasBccHeader(string(sentFull)) {
		t.Fatalf("Sent copy lacks Bcc after reconstruction (err %v)", err)
	}

	pending, _ := env.qs.ListPending(50)
	if len(pending) != 1 {
		t.Fatalf("pending=%d", len(pending))
	}
	q := pending[0]
	queued, err := store.RawMessageBytes(env.blobs, &bongov1.MailMeta{
		RawMessage: &bongov1.BlobRef{Hash: q.BlobHash}, Layout: q.Layout, Attachments: q.Attachments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasBccHeader(string(queued)) || strings.Contains(string(queued), "hidden@external.example") {
		t.Error("queued message reveals the Bcc recipient")
	}
	if raw := blobText(t, env, envelopeFor(t, env, "bob@example.com").BlobHash); hasBccHeader(raw) {
		t.Error("local copy has a Bcc header")
	}
}
