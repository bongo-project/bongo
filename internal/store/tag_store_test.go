package store

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// newTagTestEnv creates a TagStore backed by a fresh Pebble instance.
func newTagTestEnv(t *testing.T) *TagStore {
	t.Helper()
	dir := t.TempDir()
	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return NewTagStore(ps, &directApplier{store: ps})
}

// --- CreateTag ---

func TestCreateTag(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	meta, err := ts.CreateTag(userID, "inbox")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if meta.Name != "inbox" {
		t.Errorf("name = %q, want %q", meta.Name, "inbox")
	}
	if meta.ColourIndex != 0 {
		t.Errorf("colour_index = %d, want 0 (first tag)", meta.ColourIndex)
	}
	if meta.CreatedAt == 0 {
		t.Error("created_at must be set")
	}
	if meta.SortOrder != 0 {
		t.Errorf("sort_order = %d, want 0 (first tag)", meta.SortOrder)
	}
}

func TestCreateTag_ColourCycles(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	// Create tagColourPaletteSize + 1 tags; the last one should wrap to colour 0.
	for i := range tagColourPaletteSize + 1 {
		name := makeTagName(i)
		meta, err := ts.CreateTag(userID, name)
		if err != nil {
			t.Fatalf("CreateTag(%q): %v", name, err)
		}
		want := int32(i % tagColourPaletteSize)
		if meta.ColourIndex != want {
			t.Errorf("tag %q colour_index = %d, want %d", name, meta.ColourIndex, want)
		}
	}
}

func TestCreateTag_SortOrderIncrement(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	for i := range 5 {
		name := makeTagName(i)
		meta, err := ts.CreateTag(userID, name)
		if err != nil {
			t.Fatalf("CreateTag: %v", err)
		}
		if meta.SortOrder != int32(i) {
			t.Errorf("tag %q sort_order = %d, want %d", name, meta.SortOrder, i)
		}
	}
}

func TestCreateTag_DuplicateRejected(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	if _, err := ts.CreateTag(userID, "work"); err != nil {
		t.Fatalf("first CreateTag: %v", err)
	}
	if _, err := ts.CreateTag(userID, "work"); err == nil {
		t.Error("expected error creating duplicate tag, got nil")
	}
}

func TestCreateTag_InvalidNameRejected(t *testing.T) {
	ts := newTagTestEnv(t)
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "uppercase", input: "MyTag"},
		{name: "leading hyphen", input: "-tag"},
		{name: "trailing hyphen", input: "tag-"},
		{name: "spaces", input: "my tag"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ts.CreateTag("user1", tc.input); err == nil {
				t.Errorf("expected error for name %q, got nil", tc.input)
			}
		})
	}
}

func TestCreateTag_LimitEnforced(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "limit-user"

	// Fill to exactly MaxTagsPerUser.
	for i := range MaxTagsPerUser {
		if _, err := ts.CreateTag(userID, makeTagName(i)); err != nil {
			t.Fatalf("CreateTag(%d): %v", i, err)
		}
	}

	// One more should fail.
	_, err := ts.CreateTag(userID, "overflow")
	if err == nil {
		t.Error("expected error when exceeding MaxTagsPerUser, got nil")
	}
}

// --- GetTag ---

func TestGetTag(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	_, err := ts.CreateTag(userID, "work")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	got, err := ts.GetTag(userID, "work")
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if got.Name != "work" {
		t.Errorf("name = %q, want %q", got.Name, "work")
	}
}

func TestGetTag_NotFound(t *testing.T) {
	ts := newTagTestEnv(t)
	_, err := ts.GetTag("user1", "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- ListTags ---

func TestListTags_Empty(t *testing.T) {
	ts := newTagTestEnv(t)
	tags, err := ts.ListTags("user1")
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("expected 0 tags, got %d", len(tags))
	}
}

func TestListTags_SortedBySortOrderThenName(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	// Create tags in non-alphabetical order; sort_order is assigned at
	// creation time (0, 1, 2, ...) regardless of name.
	names := []string{"work", "archive", "inbox"}
	for _, name := range names {
		if _, err := ts.CreateTag(userID, name); err != nil {
			t.Fatalf("CreateTag(%q): %v", name, err)
		}
	}

	tags, err := ts.ListTags(userID)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tags))
	}

	// Expected order: by sort_order 0=work, 1=archive, 2=inbox.
	want := []string{"work", "archive", "inbox"}
	for i, twc := range tags {
		if twc.Tag.Name != want[i] {
			t.Errorf("tags[%d].name = %q, want %q", i, twc.Tag.Name, want[i])
		}
	}
}

func TestListTags_CountsMessages(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	// Create two tags.
	if _, err := ts.CreateTag(userID, "inbox"); err != nil {
		t.Fatalf("CreateTag inbox: %v", err)
	}
	if _, err := ts.CreateTag(userID, "work"); err != nil {
		t.Fatalf("CreateTag work: %v", err)
	}

	// Store mail meta with tag references using raw Pebble writes (bypassing
	// Raft in tests is acceptable here, matching the mailbox test pattern).
	storeMailWithTags := func(guid string, tags []string) {
		t.Helper()
		m := &bongov1.MailMeta{Guid: guid, Tags: tags}
		data, err := proto.Marshal(m)
		if err != nil {
			t.Fatalf("proto.Marshal: %v", err)
		}
		if err := ts.pebble.Set(mailMetaKey(userID, guid), data); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	storeMailWithTags("m1", []string{"inbox", "work"})
	storeMailWithTags("m2", []string{"inbox"})
	storeMailWithTags("m3", []string{"work"})

	tags, err := ts.ListTags(userID)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	counts := make(map[string]int)
	for _, twc := range tags {
		counts[twc.Tag.Name] = twc.Count
	}

	if counts["inbox"] != 2 {
		t.Errorf("inbox count = %d, want 2", counts["inbox"])
	}
	if counts["work"] != 2 {
		t.Errorf("work count = %d, want 2", counts["work"])
	}
}

func TestListTags_IsolatedByUser(t *testing.T) {
	ts := newTagTestEnv(t)

	if _, err := ts.CreateTag("user-a", "alpha"); err != nil {
		t.Fatalf("CreateTag user-a: %v", err)
	}
	if _, err := ts.CreateTag("user-b", "beta"); err != nil {
		t.Fatalf("CreateTag user-b: %v", err)
	}

	tagsA, err := ts.ListTags("user-a")
	if err != nil {
		t.Fatalf("ListTags user-a: %v", err)
	}
	if len(tagsA) != 1 || tagsA[0].Tag.Name != "alpha" {
		t.Errorf("user-a tags = %v, want [alpha]", tagsA)
	}

	tagsB, err := ts.ListTags("user-b")
	if err != nil {
		t.Fatalf("ListTags user-b: %v", err)
	}
	if len(tagsB) != 1 || tagsB[0].Tag.Name != "beta" {
		t.Errorf("user-b tags = %v, want [beta]", tagsB)
	}
}

// --- RenameTag ---

func TestRenameTag(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	original, err := ts.CreateTag(userID, "old-name")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	renamed, err := ts.RenameTag(userID, "old-name", "new-name")
	if err != nil {
		t.Fatalf("RenameTag: %v", err)
	}

	if renamed.Name != "new-name" {
		t.Errorf("name = %q, want %q", renamed.Name, "new-name")
	}
	// All other fields should be preserved.
	if renamed.ColourIndex != original.ColourIndex {
		t.Errorf("colour_index = %d, want %d", renamed.ColourIndex, original.ColourIndex)
	}
	if renamed.SortOrder != original.SortOrder {
		t.Errorf("sort_order = %d, want %d", renamed.SortOrder, original.SortOrder)
	}
	if renamed.CreatedAt != original.CreatedAt {
		t.Errorf("created_at changed: got %d, want %d", renamed.CreatedAt, original.CreatedAt)
	}

	// Old key should be gone.
	if _, err := ts.GetTag(userID, "old-name"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for old name, got %v", err)
	}

	// New key should exist.
	got, err := ts.GetTag(userID, "new-name")
	if err != nil {
		t.Fatalf("GetTag new-name: %v", err)
	}
	if got.Name != "new-name" {
		t.Errorf("new tag name = %q, want %q", got.Name, "new-name")
	}
}

func TestRenameTag_SameNameRejected(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	if _, err := ts.CreateTag(userID, "my-tag"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	if _, err := ts.RenameTag(userID, "my-tag", "my-tag"); err == nil {
		t.Error("expected error when renaming to same name, got nil")
	}
}

func TestRenameTag_NonExistentSource(t *testing.T) {
	ts := newTagTestEnv(t)
	if _, err := ts.RenameTag("user1", "ghost", "new-name"); err == nil {
		t.Error("expected error renaming non-existent tag, got nil")
	}
}

func TestRenameTag_TargetAlreadyExists(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	if _, err := ts.CreateTag(userID, "tag-a"); err != nil {
		t.Fatalf("CreateTag tag-a: %v", err)
	}
	if _, err := ts.CreateTag(userID, "tag-b"); err != nil {
		t.Fatalf("CreateTag tag-b: %v", err)
	}

	if _, err := ts.RenameTag(userID, "tag-a", "tag-b"); err == nil {
		t.Error("expected error renaming to existing tag name, got nil")
	}
}

func TestRenameTag_InvalidNewNameRejected(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	if _, err := ts.CreateTag(userID, "good-name"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	if _, err := ts.RenameTag(userID, "good-name", "Bad Name!"); err == nil {
		t.Error("expected error for invalid new tag name, got nil")
	}
}

// --- DeleteTag ---

func TestDeleteTag(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	if _, err := ts.CreateTag(userID, "to-delete"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	if err := ts.DeleteTag(userID, "to-delete"); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}

	if _, err := ts.GetTag(userID, "to-delete"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteTag_NonExistentReturnsNotFound(t *testing.T) {
	ts := newTagTestEnv(t)
	err := ts.DeleteTag("user1", "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// --- UpdateTagColour ---

func TestUpdateTagColour(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	if _, err := ts.CreateTag(userID, "my-tag"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	updated, err := ts.UpdateTagColour(userID, "my-tag", 7)
	if err != nil {
		t.Fatalf("UpdateTagColour: %v", err)
	}
	if updated.ColourIndex != 7 {
		t.Errorf("colour_index = %d, want 7", updated.ColourIndex)
	}

	// Verify persisted.
	got, err := ts.GetTag(userID, "my-tag")
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}
	if got.ColourIndex != 7 {
		t.Errorf("persisted colour_index = %d, want 7", got.ColourIndex)
	}
}

func TestUpdateTagColour_BoundaryValues(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	if _, err := ts.CreateTag(userID, "my-tag"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	tests := []struct {
		colourIndex int32
		wantErr     bool
	}{
		{colourIndex: 0, wantErr: false},
		{colourIndex: tagColourPaletteSize - 1, wantErr: false},
		{colourIndex: -1, wantErr: true},
		{colourIndex: tagColourPaletteSize, wantErr: true},
	}

	for _, tc := range tests {
		_, err := ts.UpdateTagColour(userID, "my-tag", tc.colourIndex)
		if (err != nil) != tc.wantErr {
			t.Errorf("UpdateTagColour(colour=%d) error = %v, wantErr %v",
				tc.colourIndex, err, tc.wantErr)
		}
	}
}

func TestUpdateTagColour_NonExistentTag(t *testing.T) {
	ts := newTagTestEnv(t)
	if _, err := ts.UpdateTagColour("user1", "ghost", 3); err == nil {
		t.Error("expected error updating colour on non-existent tag, got nil")
	}
}

// --- tagKey / tagPrefix (unit) ---

func TestTagKey(t *testing.T) {
	got := string(tagKey("alice", "work"))
	want := "/user/alice/tag/work"
	if got != want {
		t.Errorf("tagKey = %q, want %q", got, want)
	}
}

func TestTagPrefix(t *testing.T) {
	got := string(tagPrefix("alice"))
	want := "/user/alice/tag/"
	if got != want {
		t.Errorf("tagPrefix = %q, want %q", got, want)
	}
}

// --- countTags (unit) ---

func TestCountTags(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	n, err := ts.countTags(userID)
	if err != nil {
		t.Fatalf("countTags: %v", err)
	}
	if n != 0 {
		t.Errorf("initial count = %d, want 0", n)
	}

	if _, err := ts.CreateTag(userID, "alpha"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if _, err := ts.CreateTag(userID, "beta"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	n, err = ts.countTags(userID)
	if err != nil {
		t.Fatalf("countTags after inserts: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}

// --- CreatedAt is a recent timestamp ---

func TestCreateTag_CreatedAtIsRecent(t *testing.T) {
	ts := newTagTestEnv(t)
	before := time.Now().Unix()

	meta, err := ts.CreateTag("user1", "recent")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	after := time.Now().Unix()
	if meta.CreatedAt < before || meta.CreatedAt > after {
		t.Errorf("created_at %d not in [%d, %d]", meta.CreatedAt, before, after)
	}
}

// --- Persistence: data survives round-trip through Pebble ---

func TestTagRoundTrip(t *testing.T) {
	ts := newTagTestEnv(t)
	userID := "user1"

	created, err := ts.CreateTag(userID, "persist-me")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	got, err := ts.GetTag(userID, "persist-me")
	if err != nil {
		t.Fatalf("GetTag: %v", err)
	}

	if !proto.Equal(created, got) {
		t.Errorf("round-trip mismatch:\ncreated: %v\ngot:     %v", created, got)
	}
}
