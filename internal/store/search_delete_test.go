package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSearchManagerDeleteUserRemovesIndex is the search-index half of the
// per-user leak: DeleteUser previously touched neither store, so a deleted
// account left both a database and a Bleve index behind.
func TestSearchManagerDeleteUserRemovesIndex(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSearchManager(dir)
	if err != nil {
		t.Fatalf("NewSearchManager: %v", err)
	}
	t.Cleanup(func() { _ = sm.Close() })

	const userID = "22222222-2222-2222-2222-222222222222"
	if err := sm.IndexMail(userID, "guid-1", "hello", "sender@example.com",
		[]string{"rcpt@example.com"}, "body text", 1, false, nil); err != nil {
		t.Fatalf("IndexMail: %v", err)
	}

	idxDir := filepath.Join(dir, "search", "users", userID)
	if _, err := os.Stat(idxDir); err != nil {
		t.Fatalf("fixture invalid: index dir %s not created: %v", idxDir, err)
	}

	if err := sm.DeleteUser(userID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := os.Stat(idxDir); !os.IsNotExist(err) {
		t.Errorf("index dir still exists after DeleteUser (err=%v)", err)
	}

	// Idempotent, and the manager remains usable.
	if err := sm.DeleteUser(userID); err != nil {
		t.Errorf("second DeleteUser should be a no-op, got %v", err)
	}
	if err := sm.IndexMail(userID, "guid-2", "again", "sender@example.com",
		[]string{"rcpt@example.com"}, "body", 2, false, nil); err != nil {
		t.Errorf("IndexMail after DeleteUser: %v", err)
	}
}

func TestSearchManagerDeleteUserRejectsInvalidID(t *testing.T) {
	dir := t.TempDir()
	sm, err := NewSearchManager(dir)
	if err != nil {
		t.Fatalf("NewSearchManager: %v", err)
	}
	t.Cleanup(func() { _ = sm.Close() })

	if err := sm.DeleteUser("../../etc"); err == nil {
		t.Error("DeleteUser accepted an invalid user id")
	}
}
