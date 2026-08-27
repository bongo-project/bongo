package store

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// --- ValidateTagName ---

func TestValidateTagName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid names
		{name: "simple lowercase", input: "inbox", wantErr: false},
		{name: "alphanumeric", input: "tag123", wantErr: false},
		{name: "with hyphens", input: "my-tag", wantErr: false},
		{name: "leading digit", input: "2024-receipts", wantErr: false},
		{name: "single char", input: "a", wantErr: false},
		{name: "single digit", input: "1", wantErr: false},
		{name: "max length", input: strings.Repeat("a", MaxTagNameLen), wantErr: false},

		// Invalid names
		{name: "empty string", input: "", wantErr: true},
		{name: "uppercase letters", input: "MyTag", wantErr: true},
		{name: "space inside", input: "my tag", wantErr: true},
		{name: "leading space", input: " tag", wantErr: true},
		{name: "trailing space", input: "tag ", wantErr: true},
		{name: "leading hyphen", input: "-tag", wantErr: true},
		{name: "trailing hyphen", input: "tag-", wantErr: true},
		{name: "underscore", input: "my_tag", wantErr: true},
		{name: "dot", input: "my.tag", wantErr: true},
		{name: "slash", input: "my/tag", wantErr: true},
		{name: "special chars", input: "tag@name", wantErr: true},
		{name: "unicode", input: "tág", wantErr: true},
		{name: "too long", input: strings.Repeat("a", MaxTagNameLen+1), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTagName(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateTagName(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

// --- SetTags ---

func TestSetTags(t *testing.T) {
	tests := []struct {
		name     string
		initial  []string
		input    []string
		wantTags []string
		wantErr  bool
	}{
		{
			name:     "replace with valid tags",
			initial:  []string{"old"},
			input:    []string{"new", "fresh"},
			wantTags: []string{"new", "fresh"},
		},
		{
			name:     "set empty clears tags",
			initial:  []string{"old"},
			input:    []string{},
			wantTags: nil,
		},
		{
			name:     "deduplicates input",
			input:    []string{"a", "b", "a"},
			wantTags: []string{"a", "b"},
		},
		{
			name:    "invalid tag name rejected",
			input:   []string{"valid", "INVALID"},
			wantErr: true,
		},
		{
			name:    "exceeds per-message limit",
			input:   makeTagList(MaxTagsPerMessage + 1),
			wantErr: true,
		},
		{
			name:     "exactly at per-message limit",
			input:    makeTagList(MaxTagsPerMessage),
			wantTags: makeTagList(MaxTagsPerMessage),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := &bongov1.MailMeta{Tags: tc.initial}
			err := SetTags(meta, tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("SetTags() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if len(meta.Tags) != len(tc.wantTags) {
					t.Fatalf("tags = %v, want %v", meta.Tags, tc.wantTags)
				}
				for i, got := range meta.Tags {
					if got != tc.wantTags[i] {
						t.Errorf("tags[%d] = %q, want %q", i, got, tc.wantTags[i])
					}
				}
			}
		})
	}
}

// --- AddTags ---

func TestAddTags(t *testing.T) {
	tests := []struct {
		name     string
		initial  []string
		add      []string
		wantTags []string
		wantErr  bool
	}{
		{
			name:     "add to empty",
			initial:  nil,
			add:      []string{"inbox", "work"},
			wantTags: []string{"inbox", "work"},
		},
		{
			name:     "add to existing",
			initial:  []string{"inbox"},
			add:      []string{"work"},
			wantTags: []string{"inbox", "work"},
		},
		{
			name:     "deduplicates existing",
			initial:  []string{"inbox"},
			add:      []string{"inbox", "work"},
			wantTags: []string{"inbox", "work"},
		},
		{
			name:     "deduplicates within new tags",
			initial:  []string{"a"},
			add:      []string{"b", "b"},
			wantTags: []string{"a", "b"},
		},
		{
			name:    "invalid tag name rejected",
			initial: nil,
			add:     []string{"valid", "Not-valid"},
			wantErr: true,
		},
		{
			name:    "exceeds per-message limit",
			initial: makeTagList(MaxTagsPerMessage - 1),
			add:     []string{"newtag1", "newtag2"},
			wantErr: true,
		},
		{
			name:     "fills to exact per-message limit",
			initial:  makeTagList(MaxTagsPerMessage - 1),
			add:      []string{"newtag1"},
			wantTags: append(makeTagList(MaxTagsPerMessage-1), "newtag1"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := &bongov1.MailMeta{Tags: tc.initial}
			err := AddTags(meta, tc.add)
			if (err != nil) != tc.wantErr {
				t.Fatalf("AddTags() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if len(meta.Tags) != len(tc.wantTags) {
					t.Fatalf("tags = %v, want %v", meta.Tags, tc.wantTags)
				}
				for i, got := range meta.Tags {
					if got != tc.wantTags[i] {
						t.Errorf("tags[%d] = %q, want %q", i, got, tc.wantTags[i])
					}
				}
			}
		})
	}
}

// --- RemoveTags ---

func TestRemoveTags(t *testing.T) {
	tests := []struct {
		name     string
		initial  []string
		remove   []string
		wantTags []string
	}{
		{
			name:     "remove one of many",
			initial:  []string{"a", "b", "c"},
			remove:   []string{"b"},
			wantTags: []string{"a", "c"},
		},
		{
			name:     "remove all",
			initial:  []string{"a", "b"},
			remove:   []string{"a", "b"},
			wantTags: nil,
		},
		{
			name:     "remove absent tag is no-op",
			initial:  []string{"a", "b"},
			remove:   []string{"c"},
			wantTags: []string{"a", "b"},
		},
		{
			name:     "remove from empty is no-op",
			initial:  nil,
			remove:   []string{"a"},
			wantTags: nil,
		},
		{
			name:     "remove nothing",
			initial:  []string{"a", "b"},
			remove:   []string{},
			wantTags: []string{"a", "b"},
		},
		{
			name:     "preserves order",
			initial:  []string{"x", "y", "z"},
			remove:   []string{"y"},
			wantTags: []string{"x", "z"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := &bongov1.MailMeta{Tags: tc.initial}
			if err := RemoveTags(meta, tc.remove); err != nil {
				t.Fatalf("RemoveTags() unexpected error: %v", err)
			}
			if len(meta.Tags) != len(tc.wantTags) {
				t.Fatalf("tags = %v, want %v", meta.Tags, tc.wantTags)
			}
			for i, got := range meta.Tags {
				if got != tc.wantTags[i] {
					t.Errorf("tags[%d] = %q, want %q", i, got, tc.wantTags[i])
				}
			}
		})
	}
}

// --- GetTags ---

func TestGetTags(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	// Store a MailMeta with tags.
	meta := &bongov1.MailMeta{
		Guid:    "msg-1",
		Subject: "Hello",
		Tags:    []string{"inbox", "work"},
	}
	data, err := proto.Marshal(meta)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	if err := ms.store.Set(mailMetaKey(userID, "msg-1"), data); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := ms.GetTags(userID, "msg-1")
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(got) != 2 || got[0] != "inbox" || got[1] != "work" {
		t.Errorf("GetTags = %v, want [inbox work]", got)
	}
}

func TestGetTags_NotFound(t *testing.T) {
	ms := newMailboxTestEnv(t)
	_, err := ms.GetTags("user1", "nonexistent-guid")
	if err == nil {
		t.Error("expected error for non-existent mail GUID, got nil")
	}
}

func TestGetTags_EmptyTags(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	meta := &bongov1.MailMeta{Guid: "msg-2", Subject: "No tags"}
	data, _ := proto.Marshal(meta)
	ms.store.Set(mailMetaKey(userID, "msg-2"), data)

	got, err := ms.GetTags(userID, "msg-2")
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty tags, got %v", got)
	}
}

// --- CountUserDistinctTags ---

func TestCountUserDistinctTags(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	storeMailMetaWithTags := func(guid string, tags []string) {
		t.Helper()
		meta := &bongov1.MailMeta{Guid: guid, Tags: tags}
		data, err := proto.Marshal(meta)
		if err != nil {
			t.Fatalf("proto.Marshal: %v", err)
		}
		if err := ms.store.Set(mailMetaKey(userID, guid), data); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	// Initially no tags.
	count, err := ms.CountUserDistinctTags(userID)
	if err != nil {
		t.Fatalf("CountUserDistinctTags: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}

	// Store messages with overlapping tags.
	storeMailMetaWithTags("msg-1", []string{"inbox", "work"})
	storeMailMetaWithTags("msg-2", []string{"inbox", "personal"}) // "inbox" is shared
	storeMailMetaWithTags("msg-3", []string{"archive"})

	count, err = ms.CountUserDistinctTags(userID)
	if err != nil {
		t.Fatalf("CountUserDistinctTags: %v", err)
	}
	// Distinct tags: inbox, work, personal, archive = 4
	if count != 4 {
		t.Errorf("count = %d, want 4", count)
	}
}

func TestCountUserDistinctTags_MaxUserLimit(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user-limit"

	// Store messages that collectively use exactly MaxTagsPerUser distinct tags.
	for i := range MaxTagsPerUser {
		guid := makeGUID(i)
		tagName := makeTagName(i)
		meta := &bongov1.MailMeta{Guid: guid, Tags: []string{tagName}}
		data, err := proto.Marshal(meta)
		if err != nil {
			t.Fatalf("proto.Marshal: %v", err)
		}
		if err := ms.store.Set(mailMetaKey(userID, guid), data); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	count, err := ms.CountUserDistinctTags(userID)
	if err != nil {
		t.Fatalf("CountUserDistinctTags: %v", err)
	}
	if count != MaxTagsPerUser {
		t.Errorf("count = %d, want %d", count, MaxTagsPerUser)
	}
}

// --- deduplicateTags ---

func TestDeduplicateTags(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{name: "no duplicates", input: []string{"a", "b", "c"}, want: []string{"a", "b", "c"}},
		{name: "all same", input: []string{"a", "a", "a"}, want: []string{"a"}},
		{name: "mixed duplicates", input: []string{"a", "b", "a", "c", "b"}, want: []string{"a", "b", "c"}},
		{name: "nil input", input: nil, want: nil},
		{name: "empty input", input: []string{}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deduplicateTags(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("deduplicateTags(%v) = %v, want %v", tc.input, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- helpers ---

// makeTagList returns n distinct valid tag names: tag00, tag01, ...
func makeTagList(n int) []string {
	tags := make([]string, n)
	for i := range n {
		tags[i] = makeTagName(i)
	}
	return tags
}

func makeTagName(i int) string {
	return "tag" + formatIndex(i)
}

func makeGUID(i int) string {
	return "guid-" + formatIndex(i)
}

func formatIndex(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	// For i >= 10 use simple conversion: two-digit string
	tens := i / 10
	ones := i % 10
	return string(rune('0'+tens)) + string(rune('0'+ones))
}
