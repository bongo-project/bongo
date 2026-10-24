package handlers

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A retry after the queue entry has been delivered and deleted (MarkDelivered)
// must not enqueue the external message again: only the marker can tell.
func TestComposeRetryAfterExternalDeliveredDoesNotRequeue(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.mailboxes = store.NewMailboxStore(env.ps, &failSentApplier{RaftApplier: env.applier})
	id := sendID()

	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusInternalServerError {
		t.Fatalf("first attempt status=%d want 500", w.Code)
	}
	pending, _ := env.qs.ListPending(50)
	if len(pending) != 1 {
		t.Fatalf("pending after first attempt=%d want 1", len(pending))
	}
	if err := env.qs.MarkDelivered(pending[0].Id); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}

	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("retry status=%d: %s", w.Code, w.Body.String())
	}
	if p, _ := env.qs.ListPending(50); len(p) != 0 {
		t.Errorf("pending after retry=%d want 0: external message was queued twice", len(p))
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 1 {
		t.Errorf("Sent=%d want 1", len(sent))
	}
}

// Local copies from attempt 1 and the Sent and external copies from attempt 2
// must carry one Message-ID, and every envelope, the Sent GUID and the queue ID
// must be derived from the send ID.
func TestComposeRetryKeepsMessageIDAndDerivedIDs(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.handler.mailboxes = store.NewMailboxStore(env.ps, &failSentApplier{RaftApplier: env.applier})
	id := sendID()

	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusInternalServerError {
		t.Fatalf("first attempt status=%d want 500", w.Code)
	}
	envs := env.spool.envelopes()
	if len(envs) != 2 {
		t.Fatalf("envelopes=%d want 2", len(envs))
	}
	rc, err := env.blobs.Get(envs[0].BlobHash)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("parse local copy: %v", err)
	}
	localMID := msg.Header.Get("Message-Id")
	if localMID == "" {
		t.Fatal("local copy has no Message-ID")
	}
	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("retry status=%d: %s", w.Code, w.Body.String())
	}

	for _, e := range envs {
		want := derivedSendID(uid, id, "local:"+e.Recipients[0]).String()
		if e.Id != want {
			t.Errorf("envelope id for %s = %q, want derived %q", e.Recipients[0], e.Id, want)
		}
	}
	sent, _ := env.mailboxes.ListMessages(uid, "Sent")
	if len(sent) != 1 {
		t.Fatalf("Sent=%d want 1", len(sent))
	}
	if want := derivedSendID(uid, id, "sent").String(); sent[0].MailGuid != want {
		t.Errorf("Sent guid = %q, want derived %q", sent[0].MailGuid, want)
	}
	raw, err := env.ps.Get([]byte("/user/" + uid + "/mail/" + sent[0].MailGuid))
	if err != nil {
		t.Fatal(err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.MessageId != localMID {
		t.Errorf("Sent Message-ID %q != local copy %q", meta.MessageId, localMID)
	}
	pending, _ := env.qs.ListPending(50)
	if len(pending) != 1 {
		t.Fatalf("pending=%d want 1", len(pending))
	}
	if pending[0].MessageGuid != localMID {
		t.Errorf("queue MessageGuid %q != local copy %q", pending[0].MessageGuid, localMID)
	}
	if want := derivedSendID(uid, id, "external").String(); pending[0].Id != want {
		t.Errorf("queue id = %q, want derived %q", pending[0].Id, want)
	}
}

func postSendFormWithFile(env *composeSendAsEnv, userID, email, id string, fields url.Values, name string, data []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("send_id", id)
	for k, vs := range fields {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	if name != "" {
		fw, _ := mw.CreateFormFile("attachments", name)
		_, _ = io.Copy(fw, bytes.NewReader(data))
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/compose/send", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	env.handler.Send(w, req)
	return w
}

// Every fingerprinted input, changed on its own, must be refused on a reused
// send ID. Otherwise the edit would be skipped as already done while the user
// is told it was sent.
func TestComposeSameSendIDEachChangedFieldConflicts(t *testing.T) {
	base := func() url.Values {
		f := idemFields("first draft")
		f.Set("cc", "cc1@external.example")
		return f
	}
	noop := func(url.Values) {}
	tests := []struct {
		name         string
		edit         func(url.Values)
		file         string
		data         []byte
		baseFile     string
		baseFileData []byte
	}{
		{name: "to", edit: func(f url.Values) { f.Set("to", "bob@example.com, carol@example.com, other@external.example") }},
		{name: "cc", edit: func(f url.Values) { f.Set("cc", "cc2@external.example") }},
		{name: "subject", edit: func(f url.Values) { f.Set("subject", "idempotency edited") }},
		{name: "body", edit: func(f url.Values) { f.Set("body", "first draft!") }},
		{name: "body_mode", edit: func(f url.Values) { f.Set("body_mode", "html") }},
		{name: "mode", edit: func(f url.Values) { f.Set("mode", "forward") }},
		{name: "attachment added", edit: noop, file: "a.txt", data: []byte("attached")},
		{name: "attachment content", edit: noop, file: "a.txt", data: []byte("attachee"), baseFile: "a.txt", baseFileData: []byte("attached")},
		{name: "attachment name", edit: noop, file: "b.txt", data: []byte("attached"), baseFile: "a.txt", baseFileData: []byte("attached")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, uid, email := newPipelineEnv(t)
			id := sendID()
			if w := postSendFormWithFile(env, uid, email, id, base(), tc.baseFile, tc.baseFileData); w.Code != http.StatusSeeOther {
				t.Fatalf("first status=%d: %s", w.Code, w.Body.String())
			}
			spoolBefore := len(env.spool.envelopes())
			f := base()
			tc.edit(f)
			w := postSendFormWithFile(env, uid, email, id, f, tc.file, tc.data)
			if w.Code != http.StatusConflict {
				t.Fatalf("edited resubmit status=%d want 409: %s", w.Code, w.Body.String())
			}
			if n := len(env.spool.envelopes()); n != spoolBefore {
				t.Errorf("envelopes changed %d -> %d", spoolBefore, n)
			}
			if p, _ := env.qs.ListPending(50); len(p) != 1 {
				t.Errorf("pending=%d want 1", len(p))
			}
			if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 1 {
				t.Errorf("Sent=%d want 1", len(sent))
			}
		})
	}
}

// A marker made for other content conflicts even when none of the steps of the
// resubmission has a marker of its own.
func TestComposeConflictScansWholeSendLog(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	env.addLocalUser(t, "u-dave", "dave@example.com")
	env.handler.mailboxes = store.NewMailboxStore(env.ps, &failSentApplier{RaftApplier: env.applier})
	id := sendID()

	first := url.Values{"to": {"bob@example.com, ext@external.example"}, "subject": {"s"}, "body": {"one"}}
	if w := postSendForm(env, uid, email, id, first); w.Code != http.StatusInternalServerError {
		t.Fatalf("first status=%d want 500", w.Code)
	}
	// Only a local recipient with no marker, and the Sent step never completed.
	second := url.Values{"to": {"dave@example.com"}, "subject": {"s"}, "body": {"two"}}
	w := postSendForm(env, uid, email, id, second)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409: %s", w.Code, w.Body.String())
	}
	for _, e := range env.spool.envelopes() {
		if e.Recipients[0] == "dave@example.com" {
			t.Error("conflicting content was delivered to dave")
		}
	}
}

// A replay of a delivered send must not be refused for quota that the first
// attempt's own copies now occupy.
func TestComposeReplayIgnoresQuotaOfCompletedSteps(t *testing.T) {
	env, uid, email := newPipelineEnv(t)
	fq := &fakeQuota{headroom: map[string]int64{}}
	env.handler.SetQuotaChecker(fq)
	id := sendID()

	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("first status=%d: %s", w.Code, w.Body.String())
	}
	for _, u := range []string{uid, "u-bob", "u-carol"} {
		fq.headroom[u] = 0
	}
	if w := postSendForm(env, uid, email, id, idemFields("hello")); w.Code != http.StatusSeeOther {
		t.Fatalf("replay status=%d want 303: %s", w.Code, w.Body.String())
	}
	assertOneCopyEach(t, env, uid)

	// A new send is still refused, so the zero headroom really is enforced.
	if w := postSendForm(env, uid, email, sendID(), idemFields("hello")); w.Code == http.StatusSeeOther {
		t.Errorf("new send accepted despite no quota headroom")
	}
}
