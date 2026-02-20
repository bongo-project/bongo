package store

import (
	"testing"
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
		if err := sm.IndexMail("user1", m.guid, m.subject, m.from, nil, "", 1700000000, false); err != nil {
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

	if err := sm.IndexMail("user1", "m1", "User1 mail", "a@test.com", nil, "", 0, false); err != nil {
		t.Fatal(err)
	}
	if err := sm.IndexMail("user2", "m2", "User2 mail", "b@test.com", nil, "", 0, false); err != nil {
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

	if err := sm.IndexMail("user1", "m1", "Test subject", "a@test.com", nil, "", 0, false); err != nil {
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
