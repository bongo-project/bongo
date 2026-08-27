package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// --- fakes ---

// fakeTagStore is an in-memory tagStore implementation for tests.
type fakeTagStore struct {
	tags   map[string]*bongov1.TagMeta
	counts map[string]int
	// errors to inject
	listErr   error
	createErr error
	renameErr error
	deleteErr error
	colourErr error
}

func newFakeTagStore() *fakeTagStore {
	return &fakeTagStore{
		tags:   make(map[string]*bongov1.TagMeta),
		counts: make(map[string]int),
	}
}

func (f *fakeTagStore) ListTags(userID string) ([]store.TagWithCount, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]store.TagWithCount, 0, len(f.tags))
	for _, meta := range f.tags {
		out = append(out, store.TagWithCount{Tag: meta, Count: f.counts[meta.Name]})
	}
	return out, nil
}

func (f *fakeTagStore) GetTag(userID, name string) (*bongov1.TagMeta, error) {
	meta, ok := f.tags[name]
	if !ok {
		return nil, errNotFound
	}
	return meta, nil
}

func (f *fakeTagStore) CreateTag(userID, name string) (*bongov1.TagMeta, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if _, exists := f.tags[name]; exists {
		return nil, errNotFound // reuse sentinel for "already exists"
	}
	meta := &bongov1.TagMeta{
		Name:        name,
		ColourIndex: 0,
		CreatedAt:   time.Now().Unix(),
		SortOrder:   int32(len(f.tags)),
	}
	f.tags[name] = meta
	return meta, nil
}

func (f *fakeTagStore) RenameTag(userID, oldName, newName string) (*bongov1.TagMeta, error) {
	if f.renameErr != nil {
		return nil, f.renameErr
	}
	old, ok := f.tags[oldName]
	if !ok {
		return nil, errNotFound
	}
	if _, exists := f.tags[newName]; exists {
		return nil, errNotFound
	}
	newMeta := &bongov1.TagMeta{
		Name:        newName,
		ColourIndex: old.ColourIndex,
		CreatedAt:   old.CreatedAt,
		SortOrder:   old.SortOrder,
	}
	delete(f.tags, oldName)
	f.tags[newName] = newMeta
	return newMeta, nil
}

func (f *fakeTagStore) DeleteTag(userID, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.tags[name]; !ok {
		return errNotFound
	}
	delete(f.tags, name)
	return nil
}

func (f *fakeTagStore) UpdateTagColour(userID, name string, colourIndex int32) (*bongov1.TagMeta, error) {
	if f.colourErr != nil {
		return nil, f.colourErr
	}
	meta, ok := f.tags[name]
	if !ok {
		return nil, errNotFound
	}
	meta.ColourIndex = colourIndex
	return meta, nil
}

// tagsTestHandler builds a TagsHandler with in-memory fakes only (no Pebble/Raft).
func tagsTestHandler(ts *fakeTagStore) *TagsHandler {
	return &TagsHandler{
		tags:    ts,
		pebble:  nil, // no pebble: removeTagFromAllMessages is a no-op
		applier: nil,
	}
}

// --- ListTags ---

func TestListTags_Empty(t *testing.T) {
	h := tagsTestHandler(newFakeTagStore())

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.ListTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []tagResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d tags, want 0", len(got))
	}
}

func TestListTags_WithTags(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["urgent"] = &bongov1.TagMeta{Name: "urgent", ColourIndex: 2, SortOrder: 0}
	ts.tags["work"] = &bongov1.TagMeta{Name: "work", ColourIndex: 5, SortOrder: 1}
	ts.counts["urgent"] = 3

	h := tagsTestHandler(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.ListTags(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got []tagResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tags, want 2", len(got))
	}

	// Build a map by name to avoid order dependency.
	byName := make(map[string]tagResponse)
	for _, r := range got {
		byName[r.Name] = r
	}

	if r, ok := byName["urgent"]; !ok {
		t.Error("missing tag 'urgent'")
	} else {
		if r.ColourIndex != 2 {
			t.Errorf("urgent.colour_index = %d, want 2", r.ColourIndex)
		}
		if r.Count != 3 {
			t.Errorf("urgent.count = %d, want 3", r.Count)
		}
	}
}

func TestListTags_StoreError(t *testing.T) {
	ts := newFakeTagStore()
	ts.listErr = errNotFound

	h := tagsTestHandler(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.ListTags(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// --- CreateTag ---

func TestCreateTag_Success(t *testing.T) {
	h := tagsTestHandler(newFakeTagStore())

	body := `{"name":"new-tag"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.CreateTag(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}

	var got tagResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "new-tag" {
		t.Errorf("name = %q, want %q", got.Name, "new-tag")
	}
}

func TestCreateTag_MissingName(t *testing.T) {
	h := tagsTestHandler(newFakeTagStore())

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.CreateTag(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateTag_InvalidJSON(t *testing.T) {
	h := tagsTestHandler(newFakeTagStore())

	req := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.CreateTag(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCreateTag_StoreError(t *testing.T) {
	ts := newFakeTagStore()
	ts.createErr = errNotFound

	h := tagsTestHandler(ts)

	body := `{"name":"tag-name"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.CreateTag(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- UpdateTag ---

func TestUpdateTag_Rename(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["old-name"] = &bongov1.TagMeta{Name: "old-name", ColourIndex: 3}

	h := tagsTestHandler(ts)

	newName := "new-name"
	body, _ := json.Marshal(updateTagRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/tags/old-name", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "name", "old-name")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.UpdateTag(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got tagResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Name != "new-name" {
		t.Errorf("name = %q, want %q", got.Name, "new-name")
	}
}

func TestUpdateTag_Recolour(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["my-tag"] = &bongov1.TagMeta{Name: "my-tag", ColourIndex: 0}

	h := tagsTestHandler(ts)

	var colour int32 = 7
	body, _ := json.Marshal(updateTagRequest{ColourIndex: &colour})
	req := httptest.NewRequest(http.MethodPut, "/api/tags/my-tag", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "name", "my-tag")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.UpdateTag(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var got tagResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ColourIndex != 7 {
		t.Errorf("colour_index = %d, want 7", got.ColourIndex)
	}
}

func TestUpdateTag_NotFound(t *testing.T) {
	h := tagsTestHandler(newFakeTagStore())

	newName := "other"
	body, _ := json.Marshal(updateTagRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/tags/missing", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "name", "missing")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.UpdateTag(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestUpdateTag_NoFields(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["my-tag"] = &bongov1.TagMeta{Name: "my-tag"}

	h := tagsTestHandler(ts)

	body := `{}`
	req := httptest.NewRequest(http.MethodPut, "/api/tags/my-tag", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "name", "my-tag")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.UpdateTag(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdateTag_InvalidJSON(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["my-tag"] = &bongov1.TagMeta{Name: "my-tag"}

	h := tagsTestHandler(ts)

	req := httptest.NewRequest(http.MethodPut, "/api/tags/my-tag", strings.NewReader("bad"))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "name", "my-tag")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.UpdateTag(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestUpdateTag_RenameError(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["my-tag"] = &bongov1.TagMeta{Name: "my-tag"}
	ts.renameErr = errNotFound

	h := tagsTestHandler(ts)

	newName := "other"
	body, _ := json.Marshal(updateTagRequest{Name: &newName})
	req := httptest.NewRequest(http.MethodPut, "/api/tags/my-tag", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withChiParam(req, "name", "my-tag")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.UpdateTag(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// --- DeleteTag ---

func TestDeleteTag_Success(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["to-delete"] = &bongov1.TagMeta{Name: "to-delete"}

	h := tagsTestHandler(ts)

	req := httptest.NewRequest(http.MethodDelete, "/api/tags/to-delete", nil)
	req = withChiParam(req, "name", "to-delete")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.DeleteTag(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	// Confirm the tag was removed from the fake store.
	if _, exists := ts.tags["to-delete"]; exists {
		t.Error("tag still exists after DeleteTag")
	}
}

func TestDeleteTag_NotFound(t *testing.T) {
	h := tagsTestHandler(newFakeTagStore())

	req := httptest.NewRequest(http.MethodDelete, "/api/tags/ghost", nil)
	req = withChiParam(req, "name", "ghost")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.DeleteTag(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDeleteTag_StoreError(t *testing.T) {
	ts := newFakeTagStore()
	ts.tags["my-tag"] = &bongov1.TagMeta{Name: "my-tag"}
	ts.deleteErr = errNotFound

	h := tagsTestHandler(ts)

	req := httptest.NewRequest(http.MethodDelete, "/api/tags/my-tag", nil)
	req = withChiParam(req, "name", "my-tag")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.DeleteTag(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// --- removeTagFromAllMessages (nil pebble/applier path) ---

func TestRemoveTagFromAllMessages_NilPebble(t *testing.T) {
	h := &TagsHandler{pebble: nil, applier: nil}
	// Should return nil (no-op) rather than panic.
	if err := h.removeTagFromAllMessages("user1", "some-tag"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- ContentType on JSON responses ---

func TestListTags_ContentType(t *testing.T) {
	h := tagsTestHandler(newFakeTagStore())

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.ListTags(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}
}

func TestCreateTag_ContentType(t *testing.T) {
	h := tagsTestHandler(newFakeTagStore())

	body := `{"name":"test-tag"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUserContext(req, "user1", "user1@example.com")
	rec := httptest.NewRecorder()

	h.CreateTag(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}
}

// --- removeTagFromAllMessages with fakeIterator ---

// fakePebble is a minimal in-memory tagPebbleStore for testing removeTagFromAllMessages.
type fakePebble struct {
	records map[string][]byte
}

func (f *fakePebble) PrefixScan(prefix []byte) (store.Iterator, error) {
	var keys []string
	for k := range f.records {
		if strings.HasPrefix(k, string(prefix)) {
			keys = append(keys, k)
		}
	}
	return &fakeIter{records: f.records, keys: keys, idx: -1}, nil
}

type fakeIter struct {
	records map[string][]byte
	keys    []string
	idx     int
}

func (it *fakeIter) Next() bool {
	it.idx++
	return it.idx < len(it.keys)
}

func (it *fakeIter) Key() []byte {
	return []byte(it.keys[it.idx])
}

func (it *fakeIter) Value() ([]byte, error) {
	return it.records[it.keys[it.idx]], nil
}

func (it *fakeIter) Close() error {
	return nil
}

// fakeRaftApplier records applied commands.
type fakeRaftApplier struct {
	cmds []*bongov1.RaftCommand
}

func (f *fakeRaftApplier) ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error {
	f.cmds = append(f.cmds, cmd)
	return nil
}

func TestRemoveTagFromAllMessages_RemovesTagFromMessages(t *testing.T) {
	marshalMsg := func(msg *bongov1.MailMeta) []byte {
		b, _ := proto.Marshal(msg)
		return b
	}

	// Two messages: one tagged "urgent", one tagged "other".
	msg1 := &bongov1.MailMeta{Tags: []string{"urgent", "work"}}
	msg2 := &bongov1.MailMeta{Tags: []string{"other"}}

	fp := &fakePebble{
		records: map[string][]byte{
			"/user/user1/mail/guid-001": marshalMsg(msg1),
			"/user/user1/mail/guid-002": marshalMsg(msg2),
		},
	}
	applier := &fakeRaftApplier{}

	h := &TagsHandler{
		tags:    newFakeTagStore(),
		pebble:  fp,
		applier: applier,
	}

	if err := h.removeTagFromAllMessages("user1", "urgent"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only msg1 had "urgent" — expect exactly one Raft command.
	if len(applier.cmds) != 1 {
		t.Errorf("got %d raft commands, want 1", len(applier.cmds))
	}
}
