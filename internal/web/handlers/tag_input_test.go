package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Typed tag names are folded into the stored form rather than rejected: the
// message tag picker sent "Bob" as typed and the server answered 400.
func TestUpdateMessageTags_FoldsTypedNames(t *testing.T) {
	env := newMailTagsTestEnv(t)
	uid := env.seedMessage(t, "user1", "INBOX", "fold-001", nil)

	w := doTagsRequest(t, env.handler, "user1", "INBOX", uid, map[string]any{
		"action": "add",
		"tags":   []string{"Bob", " My Tag "},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp messageTagListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := []string{"bob", "my-tag"}; !slices.Equal(resp.Tags, want) {
		t.Errorf("tags = %v, want %v", resp.Tags, want)
	}
}

func TestBulkTagAction_FoldsTypedNames(t *testing.T) {
	env := newMailTagsTestEnv(t)
	uid := env.seedMessage(t, "user1", "INBOX", "fold-bulk-001", nil)

	w := doBulkTagsRequest(t, env.handler, "user1", map[string]any{
		"action":      "add",
		"tags":        []string{"Bob"},
		"message_ids": []map[string]any{{"mailbox": "INBOX", "uid": uid}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	w = doTagsRequest(t, env.handler, "user1", "INBOX", uid, map[string]any{
		"action": "add",
		"tags":   []string{"bob"},
	})
	var resp messageTagListResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if want := []string{"bob"}; !slices.Equal(resp.Tags, want) {
		t.Errorf("tags after bulk add of %q = %v, want %v", "Bob", resp.Tags, want)
	}
}

func TestBulkTagAction_RejectsNameThatFoldsToNothing(t *testing.T) {
	env := newMailTagsTestEnv(t)
	uid := env.seedMessage(t, "user1", "INBOX", "fold-bulk-002", nil)

	w := doBulkTagsRequest(t, env.handler, "user1", map[string]any{
		"action":      "add",
		"tags":        []string{"!!!"},
		"message_ids": []map[string]any{{"mailbox": "INBOX", "uid": uid}},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCreateTag_FoldsTypedName(t *testing.T) {
	ts := newFakeTagStore()
	h := tagsTestHandler(ts)

	req := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(`{"name":"Work Stuff"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()
	h.CreateTag(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	if _, ok := ts.tags["work-stuff"]; !ok {
		t.Errorf("stored tags = %v, want work-stuff", ts.tags)
	}
}

func TestUpdateTag_RenameFoldsTypedName(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["old-name"] = &bongov1.TagMeta{Name: "old-name"}
	h := tagsTestHandler(ts)

	newName := "New Name"
	body, _ := json.Marshal(updateTagRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/tags/old-name", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "name", "old-name")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()
	h.UpdateTag(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if _, ok := ts.tags["new-name"]; !ok {
		t.Errorf("stored tags = %v, want new-name", ts.tags)
	}
}

func TestRuleActionTagsFoldsAndDedupes(t *testing.T) {
	got := ruleActionTags([]string{"Bob", "bob", "!!!", "My Tag"})
	if want := []string{"bob", "my-tag"}; !slices.Equal(got, want) {
		t.Errorf("ruleActionTags = %v, want %v", got, want)
	}
}
