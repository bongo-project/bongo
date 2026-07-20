package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// rawDraftMessage returns the raw bytes + UID of the first message in Drafts.
func (e *composeRichTestEnv) rawDraftMessage(t *testing.T, userID string) ([]byte, uint32) {
	t.Helper()
	msgs, err := e.mailboxes.ListMessages(userID, "Drafts")
	if err != nil {
		t.Fatalf("ListMessages(Drafts): %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("no messages in Drafts mailbox")
	}
	key := fmt.Sprintf("/user/%s/mail/%s", userID, msgs[0].MailGuid)
	data, err := e.ps.Get([]byte(key))
	if err != nil {
		t.Fatalf("get MailMeta: %v", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}
	if meta.RawMessage == nil {
		t.Fatal("draft MailMeta.RawMessage is nil")
	}
	rc, err := e.blobs.Get(meta.RawMessage.Hash)
	if err != nil {
		t.Fatalf("BlobStore.Get: %v", err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return raw, msgs[0].Uid
}

// postDraft submits a draft with the given body_mode and body.
func (e *composeRichTestEnv) postDraft(t *testing.T, userID, email, bodyMode, body string) {
	t.Helper()
	form := url.Values{
		"to":        {"someone@example.com"},
		"subject":   {"Draft Subject"},
		"body":      {body},
		"body_mode": {bodyMode},
	}
	req := httptest.NewRequest(http.MethodPost, "/compose/draft", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUserContext(req, userID, email)
	w := httptest.NewRecorder()
	e.handler.SaveDraft(w, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("SaveDraft: want 303, got %d: %s", w.Code, w.Body.String())
	}
}

// getEditDraft invokes EditDraft for a Drafts UID with chi params wired.
func (e *composeRichTestEnv) getEditDraft(t *testing.T, userID, email string, uid uint32) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/compose/draft/Drafts/%d", uid), nil)
	req = withUserContext(req, userID, email)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", "Drafts")
	rctx.URLParams.Add("uid", fmt.Sprint(uid))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	e.handler.EditDraft(w, req)
	return w
}

// TestSaveDraftRichStoresHTML: a body_mode='rich' draft is stored as
// multipart/alternative with a text/html part — NOT text/plain full of raw tags.
func TestSaveDraftRichStoresHTML(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID, email := "user-draft-rich", "draft@example.com"

	env.postDraft(t, userID, email, "rich", "<p>hello draft</p>")

	raw, _ := env.rawDraftMessage(t, userID)
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("draft Content-Type = %q, want multipart/alternative (rich draft must store HTML)", mediaType)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var htmlContent string
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		d, _ := io.ReadAll(part)
		if strings.HasPrefix(part.Header.Get("Content-Type"), "text/html") {
			htmlContent = string(d)
		}
	}
	if !strings.Contains(htmlContent, "hello draft") {
		t.Fatalf("text/html part missing body content; got %q", htmlContent)
	}
}

// TestEditDraftRichReopensAsHTML: a rich draft reopens in rich mode with its
// HTML seeded into the editor (not raw tags in a plain textarea).
func TestEditDraftRichReopensAsHTML(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID, email := "user-draft-edit-rich", "draft2@example.com"

	env.postDraft(t, userID, email, "rich", "<p>rich round trip</p>")
	_, uid := env.rawDraftMessage(t, userID)

	w := env.getEditDraft(t, userID, email, uid)
	if w.Code != http.StatusOK {
		t.Fatalf("EditDraft: want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// templ HTML-escapes the x-data quotes, so match the escaped form.
	if !strings.Contains(body, "&#34;bodyMode&#34;:&#34;rich&#34;") {
		t.Fatal("rich draft should reopen in rich mode (bodyMode rich)")
	}
	if !strings.Contains(body, "data-initial-html") {
		t.Fatal("rich draft should seed the editor via data-initial-html")
	}
	if !strings.Contains(body, "rich round trip") {
		t.Fatalf("reopened draft missing body content:\n%s", body)
	}
}

// TestEditDraftPlainReopensAsText: a plain draft reopens in plain mode with the
// text in the textarea.
func TestEditDraftPlainReopensAsText(t *testing.T) {
	env := newComposeRichTestEnv(t)
	userID, email := "user-draft-edit-plain", "draft3@example.com"

	env.postDraft(t, userID, email, "plain", "just plain text")
	_, uid := env.rawDraftMessage(t, userID)

	w := env.getEditDraft(t, userID, email, uid)
	if w.Code != http.StatusOK {
		t.Fatalf("EditDraft: want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "&#34;bodyMode&#34;:&#34;plain&#34;") {
		t.Fatal("plain draft should reopen in plain mode")
	}
	if !strings.Contains(body, "just plain text") {
		t.Fatalf("reopened plain draft missing text:\n%s", body)
	}
	// A plain draft has no HTML part, so the rich editor must not be seeded.
	if strings.Contains(body, "data-initial-html") {
		t.Fatal("plain draft should not seed the rich editor")
	}
}
