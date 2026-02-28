package store

import (
	"testing"

	"github.com/blevesearch/bleve/v2"
)

func newTestSearchManager(t *testing.T) *SearchManager {
	t.Helper()
	sm, err := NewSearchManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewSearchManager: %v", err)
	}
	t.Cleanup(func() {
		if err := sm.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return sm
}

func TestSearchIndexAndQuery(t *testing.T) {
	sm := newTestSearchManager(t)

	// Index some messages.
	msgs := []struct {
		guid    string
		subject string
		from    string
	}{
		{"m1", "Meeting tomorrow", "alice@example.com"},
		{"m2", "Project update", "bob@example.com"},
		{"m3", "Meeting notes", "alice@example.com"},
	}

	for _, m := range msgs {
		if err := sm.IndexMail("user1", m.guid, m.subject, m.from, nil, "", 1700000000, false, nil); err != nil {
			t.Fatalf("IndexMail %s: %v", m.guid, err)
		}
	}

	// Search by subject.
	results, err := sm.Search("user1", "meeting", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'meeting', got %d", len(results))
	}

	// Search by sender.
	results, err = sm.Search("user1", "from:alice@example.com", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'from:alice', got %d", len(results))
	}
}

func TestSearchEmptyResults(t *testing.T) {
	sm := newTestSearchManager(t)

	results, err := sm.Search("user1", "nonexistent", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSearchMultipleUsers(t *testing.T) {
	sm := newTestSearchManager(t)

	if err := sm.IndexMail("user1", "m1", "User1 mail", "a@test.com", nil, "", 0, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.IndexMail("user2", "m2", "User2 mail", "b@test.com", nil, "", 0, false, nil); err != nil {
		t.Fatal(err)
	}

	// Each user should only see their own mail.
	r1, err := sm.Search("user1", "mail", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != 1 || r1[0].GUID != "m1" {
		t.Errorf("user1 results: %+v", r1)
	}

	r2, err := sm.Search("user2", "mail", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2) != 1 || r2[0].GUID != "m2" {
		t.Errorf("user2 results: %+v", r2)
	}
}

func TestGALIndexAndSearch(t *testing.T) {
	sm := newTestSearchManager(t)

	if err := sm.IndexProfile("u1", "alice@example.com", "Alice Smith"); err != nil {
		t.Fatal(err)
	}
	if err := sm.IndexProfile("u2", "bob@example.com", "Bob Jones"); err != nil {
		t.Fatal(err)
	}

	results, err := sm.SearchGAL("alice", 10)
	if err != nil {
		t.Fatalf("SearchGAL: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 GAL result, got %d", len(results))
	}
	if results[0].UserID != "u1" {
		t.Errorf("user ID = %q, want %q", results[0].UserID, "u1")
	}
}

func TestSearchCloseAndReopen(t *testing.T) {
	dir := t.TempDir()

	sm, err := NewSearchManager(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := sm.IndexMail("user1", "m1", "Test subject", "a@test.com", nil, "", 0, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := sm.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen and verify data persists.
	sm2, err := NewSearchManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sm2.Close()

	results, err := sm2.Search("user1", "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result after reopen, got %d", len(results))
	}
}

func TestBleveIndexMailTags(t *testing.T) {
	sm := newTestSearchManager(t)

	cases := []struct {
		name    string
		guid    string
		subject string
		tags    []string
	}{
		{
			name:    "message with work and urgent tags",
			guid:    "m1",
			subject: "Budget review",
			tags:    []string{"work", "urgent"},
		},
		{
			name:    "message with personal tag only",
			guid:    "m2",
			subject: "Dinner plans",
			tags:    []string{"personal"},
		},
		{
			name:    "message with no tags",
			guid:    "m3",
			subject: "Newsletter",
			tags:    nil,
		},
	}

	for _, tc := range cases {
		if err := sm.IndexMail("user1", tc.guid, tc.subject, "sender@example.com", nil, "", 1700000000, false, tc.tags); err != nil {
			t.Fatalf("IndexMail %s: %v", tc.guid, err)
		}
	}

	// Filter by tag using a term query on the keyword field.
	termQuery := bleve.NewTermQuery("work")
	termQuery.SetField("tags")
	req := bleve.NewSearchRequest(termQuery)
	result, err := sm.SearchWithRequest("user1", req)
	if err != nil {
		t.Fatalf("SearchWithRequest: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 result for tag 'work', got %d", result.Total)
	}
	if result.Hits[0].ID != "m1" {
		t.Errorf("expected hit m1, got %s", result.Hits[0].ID)
	}
}

func TestBleveIndexMailTagsExactMatch(t *testing.T) {
	sm := newTestSearchManager(t)

	// Index two messages: one with "newsletter" tag, one with "news" tag.
	// Keyword field must not tokenise, so "news" must not match "newsletter".
	if err := sm.IndexMail("user1", "m1", "Subject A", "a@test.com", nil, "", 0, false, []string{"newsletter"}); err != nil {
		t.Fatal(err)
	}
	if err := sm.IndexMail("user1", "m2", "Subject B", "b@test.com", nil, "", 0, false, []string{"news"}); err != nil {
		t.Fatal(err)
	}

	termQuery := bleve.NewTermQuery("news")
	termQuery.SetField("tags")
	req := bleve.NewSearchRequest(termQuery)
	result, err := sm.SearchWithRequest("user1", req)
	if err != nil {
		t.Fatalf("SearchWithRequest: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 exact match for tag 'news', got %d", result.Total)
	}
	if result.Hits[0].ID != "m2" {
		t.Errorf("expected hit m2, got %s", result.Hits[0].ID)
	}
}

func TestBleveIndexMailTagsMultiple(t *testing.T) {
	sm := newTestSearchManager(t)

	// Three messages: m1 has "work", m2 has "work" and "urgent", m3 has "personal".
	if err := sm.IndexMail("user1", "m1", "Report", "a@test.com", nil, "", 0, false, []string{"work"}); err != nil {
		t.Fatal(err)
	}
	if err := sm.IndexMail("user1", "m2", "Deadline", "b@test.com", nil, "", 0, false, []string{"work", "urgent"}); err != nil {
		t.Fatal(err)
	}
	if err := sm.IndexMail("user1", "m3", "Party", "c@test.com", nil, "", 0, false, []string{"personal"}); err != nil {
		t.Fatal(err)
	}

	// Searching for "work" should return m1 and m2.
	termQuery := bleve.NewTermQuery("work")
	termQuery.SetField("tags")
	req := bleve.NewSearchRequest(termQuery)
	req.Size = 10
	result, err := sm.SearchWithRequest("user1", req)
	if err != nil {
		t.Fatalf("SearchWithRequest: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("expected 2 results for tag 'work', got %d", result.Total)
	}
}

func TestBleveIndexMailTagsNilIsEmpty(t *testing.T) {
	sm := newTestSearchManager(t)

	// Message with nil tags should not appear in a tag query.
	if err := sm.IndexMail("user1", "m1", "No tags here", "a@test.com", nil, "", 0, false, nil); err != nil {
		t.Fatal(err)
	}

	termQuery := bleve.NewTermQuery("anytag")
	termQuery.SetField("tags")
	req := bleve.NewSearchRequest(termQuery)
	result, err := sm.SearchWithRequest("user1", req)
	if err != nil {
		t.Fatalf("SearchWithRequest: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 results for tag query on untagged message, got %d", result.Total)
	}
}
