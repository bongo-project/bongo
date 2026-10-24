package handlers

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// fakeSpool records the envelopes compose writes for local delivery.
type fakeSpool struct {
	mu   sync.Mutex
	envs []*bongov1.QueueEnvelope
	err  error
	// failOn fails the failOn-th Enqueue call (1-based) once; 0 disables it.
	failOn int
	calls  int
}

func (f *fakeSpool) Enqueue(env *bongov1.QueueEnvelope) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failOn != 0 && f.calls == f.failOn {
		return errors.New("injected spool failure")
	}
	if f.err != nil {
		return f.err
	}
	f.envs = append(f.envs, proto.Clone(env).(*bongov1.QueueEnvelope))
	return nil
}

func (f *fakeSpool) envelopes() []*bongov1.QueueEnvelope {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*bongov1.QueueEnvelope(nil), f.envs...)
}

// addLocalUser makes id/address a resolvable local account.
func (e *composeSendAsEnv) addLocalUser(t *testing.T, id, address string) {
	t.Helper()
	e.seedUser(t, id, address)
	e.seedAddress(t, address, id, address[strings.Index(address, "@")+1:], true, true)
	prof, err := proto.Marshal(&bongov1.UserProfile{Email: address})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.applier.ApplyCommand(&bongov1.RaftCommand{
		Op: bongov1.Op_OP_PUT, Key: []byte("/user/" + id + "/profile"), Value: prof,
	}, 0); err != nil {
		t.Fatal(err)
	}
}

func newPipelineEnv(t *testing.T) (*composeSendAsEnv, string, string) {
	t.Helper()
	env := newComposeSendAsEnv(t)
	env.addLocalUser(t, "u-alice", "alice@example.com")
	env.addLocalUser(t, "u-bob", "bob@example.com")
	env.addLocalUser(t, "u-carol", "carol@example.com")
	return env, "u-alice", "alice@example.com"
}

func inboxCount(env *composeSendAsEnv, user string) int {
	msgs, _ := env.mailboxes.ListMessages(user, "INBOX")
	return len(msgs)
}

func TestComposeLocalRecipientUsesPipeline(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	w := sendCompose(t, env, uid, email, []string{"bob@example.com"}, nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	envs := env.spool.envelopes()
	if len(envs) != 1 {
		t.Fatalf("envelopes=%d want 1", len(envs))
	}
	e := envs[0]
	if !e.Trusted || e.AuthUserId != uid || e.Sender != email || len(e.Recipients) != 1 || e.Recipients[0] != "bob@example.com" {
		t.Errorf("envelope = trusted=%v auth=%q sender=%q rcpts=%v", e.Trusted, e.AuthUserId, e.Sender, e.Recipients)
	}
	if e.Id == "" || e.BlobHash == "" || e.ReceivedAt == 0 {
		t.Errorf("envelope missing id/blob/time: %+v", e)
	}
	if n := inboxCount(env, "u-bob"); n != 0 {
		t.Errorf("recipient INBOX=%d, compose must not deliver directly", n)
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 1 {
		t.Errorf("Sent=%d want 1", len(sent))
	}
}

func TestComposeLocalEnvelopeSenderIsFromAlias(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.seedAddress(t, "alias@example.com", uid, "example.com", true, true)
	form := url.Values{
		"send_id": {sendID()},
		"to":      {"bob@example.com"},
		"from":    {"alias@example.com"},
		"subject": {"alias test"},
		"body":    {"hello"},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/send", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, uid, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	envs := env.spool.envelopes()
	if len(envs) != 1 || envs[0].Sender != "alias@example.com" {
		t.Fatalf("envelopes=%v want one with Sender alias@example.com", envs)
	}
}

func TestComposeLocalEnvelopePerRecipient(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	w := sendCompose(t, env, uid, email, []string{"bob@example.com"}, []string{"carol@example.com"})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	envs := env.spool.envelopes()
	if len(envs) != 2 {
		t.Fatalf("envelopes=%d want 2", len(envs))
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, e := range envs {
		if len(e.Recipients) != 1 {
			t.Fatalf("shared envelope: %v", e.Recipients)
		}
		seen[e.Recipients[0]] = true
		ids[e.Id] = true
	}
	if !seen["bob@example.com"] || !seen["carol@example.com"] || len(ids) != 2 {
		t.Errorf("recipients=%v ids=%v", seen, ids)
	}
}

func TestComposeLocalEnvelopeCarriesWholeMessage(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.SetAttachmentDedup(1024)

	att := bytes.Repeat([]byte("ATTACHMENT-PAYLOAD-"), 500)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("send_id", sendID())
	_ = mw.WriteField("to", "bob@example.com, ext@external.example")
	_ = mw.WriteField("subject", "big")
	_ = mw.WriteField("body", "see attached")
	fw, err := mw.CreateFormFile("attachments", "doc.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(att)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/compose/send", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withUserContext(req, uid, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}

	// The Sent copy must really be split, or this test proves nothing.
	sent, _ := env.mailboxes.ListMessages(uid, "Sent")
	if len(sent) != 1 {
		t.Fatalf("Sent=%d", len(sent))
	}
	metaBytes, err := env.ps.Get([]byte("/user/" + uid + "/mail/" + sent[0].MailGuid))
	if err != nil {
		t.Fatal(err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Layout) == 0 {
		t.Fatal("Sent copy was not split")
	}
	want, err := store.RawMessageBytes(env.blobs, &meta)
	if err != nil {
		t.Fatal(err)
	}

	envs := env.spool.envelopes()
	if len(envs) != 1 {
		t.Fatalf("envelopes=%d", len(envs))
	}
	if envs[0].BlobHash == meta.RawMessage.Hash {
		t.Fatal("envelope points at the skeleton blob")
	}
	rc, err := env.blobs.Get(envs[0].BlobHash)
	if err != nil {
		t.Fatalf("envelope blob: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, want) {
		t.Errorf("envelope blob (%d bytes) differs from the full message (%d bytes)", len(got), len(want))
	}
}

func TestComposeLocalSpoolFailure(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.spool.err = errors.New("disk full")
	w := sendCompose(t, env, uid, email, []string{"bob@example.com", "ext@external.example"}, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Errorf("Location=%q on failure", loc)
	}
	if strings.Contains(w.Body.String(), "sent") {
		t.Errorf("body mentions sent: %q", w.Body.String())
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 0 {
		t.Errorf("Sent=%d, want none", len(sent))
	}
	if p, _ := env.qs.ListPending(50); len(p) != 0 {
		t.Errorf("external queue=%d, want none", len(p))
	}
}

func TestComposeLocalNoSpool(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.SetSpool(nil)
	w := sendCompose(t, env, uid, email, []string{"bob@example.com"}, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", w.Code)
	}
	if n := inboxCount(env, "u-bob"); n != 0 {
		t.Errorf("fell back to direct delivery: INBOX=%d", n)
	}
	// The refusal spent no rate-limit budget: a limit of one still allows the
	// next send.
	env.handler.SetRateLimiter(mail.NewRateLimiter(1))
	if w := sendCompose(t, env, uid, email, []string{"bob@example.com", "ext@external.example"}, nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("mixed status=%d want 503", w.Code)
	}
	// External-only sends do not need the spool.
	if w := sendCompose(t, env, uid, email, []string{"ext@external.example"}, nil); w.Code != http.StatusSeeOther {
		t.Errorf("external-only status=%d", w.Code)
	}
}

func TestComposeExternalUnchangedByPipeline(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	w := sendCompose(t, env, uid, email, []string{"bob@example.com", "ext@external.example"}, nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d", w.Code)
	}
	p, _ := env.qs.ListPending(50)
	if len(p) != 1 || len(p[0].Recipients) != 1 || p[0].Recipients[0] != "ext@external.example" {
		t.Fatalf("outbound = %+v", p)
	}
	if len(env.spool.envelopes()) != 1 {
		t.Errorf("envelopes=%d want 1 (local only)", len(env.spool.envelopes()))
	}
}
