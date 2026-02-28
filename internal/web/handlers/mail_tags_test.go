package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	"github.com/bongo-go/bongo-go/internal/web/middleware"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// directApplierTags applies Raft commands directly to Pebble without Raft consensus.
// Used in handler tests to avoid a real Raft cluster.
type directApplierTags struct {
	ps *store.PebbleStore
}

func (a *directApplierTags) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.ps.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.ps.Delete(cmd.Key)
	}
	return nil
}

// mailTagsTestEnv holds all the wired-up stores needed to exercise UpdateMessageTags.
type mailTagsTestEnv struct {
	ps        *store.PebbleStore
	applier   *directApplierTags
	mailboxes *store.MailboxStore
	tags      *store.TagStore
	handler   *MailHandler
}

func newMailTagsTestEnv(t *testing.T) *mailTagsTestEnv {
	t.Helper()
	dir := t.TempDir()

	ps, err := store.NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	applier := &directApplierTags{ps: ps}
	mailboxes := store.NewMailboxStore(ps, applier)
	tagStore := store.NewTagStore(ps, applier)

	h := NewMailHandler(ps, nil, mailboxes, discardLogger())
	h.SetApplier(applier)
	h.SetTagStore(tagStore)

	return &mailTagsTestEnv{
		ps:        ps,
		applier:   applier,
		mailboxes: mailboxes,
		tags:      tagStore,
		handler:   h,
	}
}

// seedMessage writes a MailMeta and a MailboxMessage into Pebble and returns the uid.
func (env *mailTagsTestEnv) seedMessage(t *testing.T, userID, mailbox, mailGUID string, initialTags []string) uint32 {
	t.Helper()

	// Create the mailbox.
	if err := env.mailboxes.CreateMailbox(userID, mailbox); err != nil {
		// Ignore "already exists" errors when re-seeding.
		_ = err
	}

	meta := &bongov1.MailMeta{
		Guid:       mailGUID,
		Subject:    "Test subject",
		From:       "sender@example.com",
		ReceivedAt: time.Now().Unix(),
		Tags:       initialTags,
	}
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal MailMeta: %v", err)
	}
	if err := env.applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, mailGUID)),
		Value: metaBytes,
	}, 5*time.Second); err != nil {
		t.Fatalf("write MailMeta: %v", err)
	}

	// Deliver the message into the mailbox so GetMessage works.
	mbMeta, err := env.mailboxes.GetMailbox(userID, mailbox)
	if err != nil {
		t.Fatalf("GetMailbox: %v", err)
	}
	uid := mbMeta.UidNext
	mbMeta.UidNext = uid + 1
	mbMetaBytes, _ := proto.Marshal(mbMeta)
	_ = env.applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mailbox/%s/meta", userID, mailbox)),
		Value: mbMetaBytes,
	}, 5*time.Second)

	msg := &bongov1.MailboxMessage{
		Uid:          uid,
		MailGuid:     mailGUID,
		InternalDate: meta.ReceivedAt,
		Tags:         initialTags,
	}
	msgBytes, _ := proto.Marshal(msg)
	_ = env.applier.ApplyCommand(&bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/%010d", userID, mailbox, uid)),
		Value: msgBytes,
	}, 5*time.Second)

	return uid
}

// doTagsRequest performs a POST /mail/{mailbox}/{uid}/tags request and returns the recorder.
func doTagsRequest(t *testing.T, h *MailHandler, userID, mailbox string, uid uint32, body any) *httptest.ResponseRecorder {
	t.Helper()

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/mail/%s/%d/tags", mailbox, uid), bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")

	// Inject chi URL params.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", mailbox)
	rctx.URLParams.Add("uid", fmt.Sprintf("%d", uid))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	// Inject user context.
	r = r.WithContext(middleware.WithUserContext(r.Context(), userID, userID+"@example.com"))

	w := httptest.NewRecorder()
	h.UpdateMessageTags(w, r)
	return w
}

func TestUpdateMessageTags_AddNewTags(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-001"

	uid := env.seedMessage(t, userID, mailbox, mailGUID, nil)

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "add",
		"tags":   []string{"work", "important"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp messageTagListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantTags := []string{"work", "important"}
	if len(resp.Tags) != len(wantTags) {
		t.Fatalf("tags = %v, want %v", resp.Tags, wantTags)
	}
	for i, tag := range wantTags {
		if resp.Tags[i] != tag {
			t.Errorf("tags[%d] = %q, want %q", i, resp.Tags[i], tag)
		}
	}
}

func TestUpdateMessageTags_AddToExistingTags(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-002"

	uid := env.seedMessage(t, userID, mailbox, mailGUID, []string{"work"})

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "add",
		"tags":   []string{"urgent"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp messageTagListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Should contain both old and new tag.
	tagSet := make(map[string]bool, len(resp.Tags))
	for _, t := range resp.Tags {
		tagSet[t] = true
	}
	if !tagSet["work"] || !tagSet["urgent"] {
		t.Errorf("tags = %v, want to contain [work, urgent]", resp.Tags)
	}
}

func TestUpdateMessageTags_RemoveTags(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-003"

	uid := env.seedMessage(t, userID, mailbox, mailGUID, []string{"work", "urgent", "receipt"})

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "remove",
		"tags":   []string{"urgent"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp messageTagListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	for _, tag := range resp.Tags {
		if tag == "urgent" {
			t.Errorf("tags should not contain %q after remove; got %v", "urgent", resp.Tags)
		}
	}

	// work and receipt should still be present.
	tagSet := make(map[string]bool, len(resp.Tags))
	for _, t := range resp.Tags {
		tagSet[t] = true
	}
	if !tagSet["work"] || !tagSet["receipt"] {
		t.Errorf("tags = %v, want to retain [work, receipt]", resp.Tags)
	}
}

func TestUpdateMessageTags_RemoveNonExistentTagIsNoop(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-004"

	uid := env.seedMessage(t, userID, mailbox, mailGUID, []string{"work"})

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "remove",
		"tags":   []string{"nonexistent"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp messageTagListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// work should still be there.
	if len(resp.Tags) != 1 || resp.Tags[0] != "work" {
		t.Errorf("tags = %v, want [work]", resp.Tags)
	}
}

func TestUpdateMessageTags_InvalidAction(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-005"
	uid := env.seedMessage(t, userID, mailbox, mailGUID, nil)

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "replace",
		"tags":   []string{"work"},
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateMessageTags_InvalidTagName(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-006"
	uid := env.seedMessage(t, userID, mailbox, mailGUID, nil)

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "add",
		"tags":   []string{"Invalid Tag!"},
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateMessageTags_EmptyTags(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-007"
	uid := env.seedMessage(t, userID, mailbox, mailGUID, nil)

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "add",
		"tags":   []string{},
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateMessageTags_MessageNotFound(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"

	// Create the mailbox but do not seed any messages.
	if err := env.mailboxes.CreateMailbox(userID, mailbox); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	w := doTagsRequest(t, env.handler, userID, mailbox, 9999, map[string]any{
		"action": "add",
		"tags":   []string{"work"},
	})

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUpdateMessageTags_InvalidUID(t *testing.T) {
	env := newMailTagsTestEnv(t)

	bodyBytes, _ := json.Marshal(map[string]any{"action": "add", "tags": []string{"work"}})
	r := httptest.NewRequest(http.MethodPost, "/mail/INBOX/notanumber/tags", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mailbox", "INBOX")
	rctx.URLParams.Add("uid", "notanumber")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(middleware.WithUserContext(r.Context(), "user1", "user1@example.com"))

	w := httptest.NewRecorder()
	env.handler.UpdateMessageTags(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateMessageTags_PebbleUpdated(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-008"

	uid := env.seedMessage(t, userID, mailbox, mailGUID, nil)

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "add",
		"tags":   []string{"receipts"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify the MailMeta was updated in Pebble.
	data, err := env.ps.Get([]byte(fmt.Sprintf("/user/%s/mail/%s", userID, mailGUID)))
	if err != nil {
		t.Fatalf("reading MailMeta from Pebble: %v", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal MailMeta: %v", err)
	}
	if len(meta.Tags) != 1 || meta.Tags[0] != "receipts" {
		t.Errorf("MailMeta.Tags = %v, want [receipts]", meta.Tags)
	}

	// Verify the MailboxMessage was updated in Pebble.
	msgData, err := env.ps.Get([]byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/%010d", userID, mailbox, uid)))
	if err != nil {
		t.Fatalf("reading MailboxMessage from Pebble: %v", err)
	}
	var msg bongov1.MailboxMessage
	if err := proto.Unmarshal(msgData, &msg); err != nil {
		t.Fatalf("unmarshal MailboxMessage: %v", err)
	}
	if len(msg.Tags) != 1 || msg.Tags[0] != "receipts" {
		t.Errorf("MailboxMessage.Tags = %v, want [receipts]", msg.Tags)
	}
}

func TestUpdateMessageTags_AutoCreatesTagDefinition(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-009"

	uid := env.seedMessage(t, userID, mailbox, mailGUID, nil)

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "add",
		"tags":   []string{"auto-created"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// The TagStore should now have a definition for "auto-created".
	tagMeta, err := env.tags.GetTag(userID, "auto-created")
	if err != nil {
		t.Fatalf("GetTag(auto-created): %v", err)
	}
	if tagMeta.Name != "auto-created" {
		t.Errorf("tag name = %q, want %q", tagMeta.Name, "auto-created")
	}
}

func TestUpdateMessageTags_DuplicateTagsDeduped(t *testing.T) {
	env := newMailTagsTestEnv(t)
	userID := "user1"
	mailbox := "INBOX"
	mailGUID := "msg-010"

	uid := env.seedMessage(t, userID, mailbox, mailGUID, nil)

	w := doTagsRequest(t, env.handler, userID, mailbox, uid, map[string]any{
		"action": "add",
		"tags":   []string{"work", "work", "work"},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp messageTagListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Tags) != 1 || resp.Tags[0] != "work" {
		t.Errorf("tags = %v, want [work] (deduplicated)", resp.Tags)
	}
}
