package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jhillyerd/enmime"

	"github.com/bongo-project/bongo/internal/mail"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	selfNote   = "PRIVATE-NOTE-NEVER-SENT"
	selfCardID = "selfcard1"
)

const selfVCard = "BEGIN:VCARD\r\nVERSION:3.0\r\nUID:urn:uuid:private-uid-value\r\nFN:Jane Doe\r\nN:Doe;Jane;;;\r\n" +
	"EMAIL;TYPE=work:jane@example.com\r\nTEL;TYPE=cell:+31 6 1234\r\nNOTE:" + selfNote + "\r\n" +
	"X-SOCIALPROFILE:twitter:privatehandle\r\nEND:VCARD\r\n"

// selfEnv is the pipeline env (alice, bob, carol) with a contact store.
func newSelfCardEnv(t *testing.T) (*composeSendAsEnv, *store.ContactStore, string, string) {
	t.Helper()
	env, uid, email := newPipelineEnv(t)
	cs := store.NewContactStore(env.ps, env.applier)
	env.handler.SetContacts(cs)
	return env, cs, uid, email
}

func markSelfCard(t *testing.T, cs *store.ContactStore, uid string) {
	t.Helper()
	if _, err := cs.Create(uid, &bongov1.Contact{Guid: selfCardID, DisplayName: "Jane Doe", Email: "jane@example.com", VcardRaw: []byte(selfVCard)}); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetSelfContact(uid, selfCardID); err != nil {
		t.Fatal(err)
	}
}

func selfSendFields(extra ...string) url.Values {
	v := url.Values{"to": {"bob@example.com"}, "subject": {"card"}, "body": {"hello"}}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return v
}

func parseRaw(t *testing.T, raw string) *enmime.Envelope {
	t.Helper()
	env, err := enmime.ReadEnvelope(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return env
}

// assertOneCard checks that raw carries exactly one attachment, the shared card.
func assertOneCard(t *testing.T, raw string) {
	t.Helper()
	env := parseRaw(t, raw)
	if len(env.Attachments) != 1 {
		t.Fatalf("attachments = %d, want 1:\n%s", len(env.Attachments), raw)
	}
	a := env.Attachments[0]
	if !strings.HasPrefix(a.ContentType, "text/vcard") || a.FileName != "Jane Doe.vcf" {
		t.Errorf("attachment = %q %q", a.ContentType, a.FileName)
	}
	body := string(a.Content)
	for _, want := range []string{"FN:Jane Doe", "jane@example.com", "+31 6 1234"} {
		if !strings.Contains(body, want) {
			t.Errorf("card misses %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{selfNote, "private-uid-value", "privatehandle"} {
		if strings.Contains(body, bad) {
			t.Errorf("card leaks %q:\n%s", bad, body)
		}
	}
}

func TestComposeOffersAttachMyCardOnlyWithSelfCard(t *testing.T) {
	offered := func(env *composeSendAsEnv, uid, email string) bool {
		req := httptest.NewRequest(http.MethodGet, "/compose", nil)
		req.Header.Set("HX-Request", "true")
		req = withUserContext(req, uid, email)
		w := httptest.NewRecorder()
		env.handler.NewMessage(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d", w.Code)
		}
		out := w.Body.String()
		if strings.Contains(out, `name="attach_self" value="1" checked`) {
			t.Error("the box must start unticked")
		}
		return strings.Contains(out, `name="attach_self"`)
	}

	t.Run("unset", func(t *testing.T) {
		env, _, uid, email := newSelfCardEnv(t)
		if offered(env, uid, email) {
			t.Error("offered without a card")
		}
	})
	t.Run("set", func(t *testing.T) {
		env, cs, uid, email := newSelfCardEnv(t)
		markSelfCard(t, cs, uid)
		if !offered(env, uid, email) {
			t.Error("not offered with a card")
		}
	})
	t.Run("dangling", func(t *testing.T) {
		env, cs, uid, email := newSelfCardEnv(t)
		markSelfCard(t, cs, uid)
		if err := env.ps.Delete([]byte("/user/" + uid + "/contact/" + selfCardID)); err != nil {
			t.Fatal(err)
		}
		if offered(env, uid, email) {
			t.Error("offered with a dangling mark")
		}
	})
	t.Run("group", func(t *testing.T) {
		env, cs, uid, email := newSelfCardEnv(t)
		markSelfCard(t, cs, uid)
		grp := "BEGIN:VCARD\r\nVERSION:4.0\r\nKIND:group\r\nFN:Team\r\nMEMBER:mailto:a@example.com\r\nEND:VCARD\r\n"
		if err := cs.Update(uid, selfCardID, &bongov1.Contact{DisplayName: "Team", VcardRaw: []byte(grp)}); err != nil {
			t.Fatal(err)
		}
		if offered(env, uid, email) {
			t.Error("offered with a group")
		}
	})
}

func TestSendAttachSelfPlain(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	w := postSendForm(env, uid, email, sendID(), selfSendFields("attach_self", "1"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	assertOneCard(t, blobText(t, env, envelopeFor(t, env, "bob@example.com").BlobHash))
	assertOneCard(t, sentRaw(t, env, uid))
}

func TestSendAttachSelfRich(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	w := postSendForm(env, uid, email, sendID(), selfSendFields("attach_self", "1", "body_mode", "rich", "body", "<p>hello</p>"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	assertOneCard(t, blobText(t, env, envelopeFor(t, env, "bob@example.com").BlobHash))
}

func TestSendWithoutAttachSelfHasNoCard(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	w := postSendForm(env, uid, email, sendID(), selfSendFields())
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	raw := blobText(t, env, envelopeFor(t, env, "bob@example.com").BlobHash)
	if n := len(parseRaw(t, raw).Attachments); n != 0 {
		t.Errorf("attachments = %d, want 0", n)
	}
}

func sendLogKeys(t *testing.T, env *composeSendAsEnv, uid string) int {
	t.Helper()
	it, err := env.ps.PrefixScan([]byte("/user/" + uid + "/sendlog/"))
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	n := 0
	for it.Next() {
		n++
	}
	return n
}

func TestSendAttachSelfMissingRefusedBeforeWrites(t *testing.T) {
	env, _, uid, email := newSelfCardEnv(t)
	rl := mail.NewRateLimiter(5)
	rrl := mail.NewRateLimiter(5)
	env.handler.SetRateLimiter(rl)
	env.handler.SetRecipientLimiter(rrl)
	fields := selfSendFields("attach_self", "1", "to", "bob@example.com, ext@external.example")
	w := postSendForm(env, uid, email, sendID(), fields)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400: %s", w.Code, w.Body.String())
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 0 {
		t.Errorf("Sent = %d, want none", len(sent))
	}
	if pending, _ := env.qs.ListPending(50); len(pending) != 0 {
		t.Errorf("queue = %d, want none", len(pending))
	}
	if n := len(env.spool.envelopes()); n != 0 {
		t.Errorf("envelopes = %d, want none", n)
	}
	if n := sendLogKeys(t, env, uid); n != 0 {
		t.Errorf("sendlog keys = %d, want none", n)
	}
	if rl.Remaining(email) != 5 || rrl.Remaining(email) != 5 {
		t.Errorf("limiters spent: message %d, recipients %d", rl.Remaining(email), rrl.Remaining(email))
	}
	// Dropping the tick sends normally.
	if w := postSendForm(env, uid, email, sendID(), selfSendFields()); w.Code != http.StatusSeeOther {
		t.Errorf("plain send: status=%d", w.Code)
	}
}

func TestSendAttachSelfRetryIdempotent(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	id := sendID()
	fields := selfSendFields("attach_self", "1", "to", "bob@example.com, ext@external.example")
	for i := range 2 {
		if w := postSendForm(env, uid, email, id, fields); w.Code != http.StatusSeeOther {
			t.Fatalf("attempt %d: status=%d: %s", i, w.Code, w.Body.String())
		}
	}
	if sent, _ := env.mailboxes.ListMessages(uid, "Sent"); len(sent) != 1 {
		t.Errorf("Sent = %d, want 1", len(sent))
	}
	if pending, _ := env.qs.ListPending(50); len(pending) != 1 {
		t.Errorf("queue = %d, want 1", len(pending))
	}
	if n := len(env.spool.envelopes()); n != 1 {
		t.Errorf("envelopes = %d, want 1", n)
	}
}

// The card bytes are part of the send fingerprint: a retry after the card was
// edited, or with the box toggled, must conflict instead of being skipped as done.
func TestSendAttachSelfCardEditedRetryConflicts(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	id := sendID()
	fields := selfSendFields("attach_self", "1")
	if w := postSendForm(env, uid, email, id, fields); w.Code != http.StatusSeeOther {
		t.Fatalf("first: status=%d: %s", w.Code, w.Body.String())
	}
	edited := strings.Replace(selfVCard, "+31 6 1234", "+31 6 9999", 1)
	if err := cs.Update(uid, selfCardID, &bongov1.Contact{DisplayName: "Jane Doe", Email: "jane@example.com", VcardRaw: []byte(edited)}); err != nil {
		t.Fatal(err)
	}
	if w := postSendForm(env, uid, email, id, fields); w.Code != http.StatusConflict {
		t.Fatalf("edited card retry: status=%d want 409", w.Code)
	}
	if w := postSendForm(env, uid, email, id, selfSendFields()); w.Code != http.StatusConflict {
		t.Fatalf("toggled box retry: status=%d want 409", w.Code)
	}
}

func postSelfDraft(t *testing.T, env *composeSendAsEnv, uid, email string, fields url.Values) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/compose/draft", strings.NewReader(fields.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, uid, email)
	w := httptest.NewRecorder()
	env.handler.SaveDraft(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("SaveDraft: %d: %s", w.Code, w.Body.String())
	}
}

func draftRaw(t *testing.T, env *composeSendAsEnv, uid string) (string, uint32) {
	t.Helper()
	msgs, _ := env.mailboxes.ListMessages(uid, "Drafts")
	if len(msgs) != 1 {
		t.Fatalf("Drafts = %d, want 1", len(msgs))
	}
	meta := env.handler.loadMailMeta(uid, msgs[0].MailGuid)
	return blobText(t, env, meta.RawMessage.Hash), msgs[0].Uid
}

func editDraftPage(t *testing.T, env *composeSendAsEnv, uid, email string, msgUID uint32) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/compose/draft/Drafts/%d", msgUID), nil)
	req = withUserContext(req, uid, email)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("mailbox", "Drafts")
	rc.URLParams.Add("uid", fmt.Sprint(msgUID))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	env.handler.EditDraft(w, req)
	b, _ := io.ReadAll(w.Body)
	return string(b)
}

func TestSaveDraftAttachSelfKeepsCardAndMarker(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	postSelfDraft(t, env, uid, email, selfSendFields("attach_self", "1"))
	raw, _ := draftRaw(t, env, uid)
	assertOneCard(t, raw)
	if !attachSelfHeaderOf([]byte(raw)) {
		t.Errorf("draft lacks the %s marker", attachSelfHeader)
	}
}

func TestSaveDraftAttachSelfMissingStillSaves(t *testing.T) {
	env, _, uid, email := newSelfCardEnv(t)
	postSelfDraft(t, env, uid, email, selfSendFields("attach_self", "1"))
	raw, _ := draftRaw(t, env, uid)
	if n := len(parseRaw(t, raw).Attachments); n != 0 || attachSelfHeaderOf([]byte(raw)) {
		t.Errorf("draft carries a card or marker without a self card")
	}
}

func TestEditDraftRestoresAttachSelf(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	postSelfDraft(t, env, uid, email, selfSendFields("attach_self", "1"))
	_, msgUID := draftRaw(t, env, uid)
	page := editDraftPage(t, env, uid, email, msgUID)
	if !strings.Contains(page, `name="attach_self" value="1" checked`) {
		t.Errorf("reopened draft does not tick the box:\n%s", page)
	}
}

func TestEditDraftWithoutMarkerLeavesBoxUnticked(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	postSelfDraft(t, env, uid, email, selfSendFields())
	_, msgUID := draftRaw(t, env, uid)
	page := editDraftPage(t, env, uid, email, msgUID)
	if !strings.Contains(page, `name="attach_self"`) || strings.Contains(page, `name="attach_self" value="1" checked`) {
		t.Errorf("box should be offered and unticked:\n%s", page)
	}
}

func TestSentFromDraftHasNoSelfMarker(t *testing.T) {
	env, cs, uid, email := newSelfCardEnv(t)
	markSelfCard(t, cs, uid)
	postSelfDraft(t, env, uid, email, selfSendFields("attach_self", "1"))
	if raw, _ := draftRaw(t, env, uid); !strings.Contains(raw, attachSelfHeader) {
		t.Fatal("precondition: the draft carries the marker")
	}
	// Sending what the reopened draft form submits.
	w := postSendForm(env, uid, email, sendID(), selfSendFields("attach_self", "1", "mode", "draft"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	for name, raw := range map[string]string{
		"delivered": blobText(t, env, envelopeFor(t, env, "bob@example.com").BlobHash),
		"sent":      sentRaw(t, env, uid),
	} {
		if strings.Contains(raw, attachSelfHeader) {
			t.Errorf("%s copy carries %s", name, attachSelfHeader)
		}
	}
}

func TestSelfCardAttachmentTooLarge(t *testing.T) {
	env, cs, uid, _ := newSelfCardEnv(t)
	big := strings.Replace(selfVCard, "EMAIL;TYPE=work:jane@example.com", "EMAIL;TYPE=work:"+strings.Repeat("a", selfCardMaxBytes)+"@example.com", 1)
	if _, err := cs.Create(uid, &bongov1.Contact{Guid: selfCardID, DisplayName: "Jane Doe", VcardRaw: []byte(big)}); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetSelfContact(uid, selfCardID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.handler.selfCardAttachment(uid); err != errSelfCardTooLarge {
		t.Fatalf("err = %v, want errSelfCardTooLarge", err)
	}
}
