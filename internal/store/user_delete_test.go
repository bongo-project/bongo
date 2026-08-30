package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMetadataManagerDeleteUserRemovesFiles covers the leak behind the ghost
// mailbox incident: deleting an account left its per-user SQLite database on
// disk, and anything still resolving to the dead ID kept writing to it.
func TestMetadataManagerDeleteUserRemovesFiles(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewMetadataManager(dir)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	const userID = "11111111-1111-1111-1111-111111111111"
	if _, err := mgr.OpenUser(userID); err != nil {
		t.Fatalf("OpenUser: %v", err)
	}
	// Force a write so the WAL sidecar exists too.
	if err := mgr.IndexMail(userID, "guid-1", "subject", "sender@example.com", 1, 10); err != nil {
		t.Fatalf("IndexMail: %v", err)
	}

	dbPath := filepath.Join(dir, "metadata", "users", userID+".db")
	if _, err := os.Stat(dbPath); err != nil {
		// Layout differs between versions; locate it rather than guess.
		matches, _ := filepath.Glob(filepath.Join(dir, "**", userID+".db"))
		if len(matches) == 0 {
			matches, _ = filepath.Glob(filepath.Join(dir, "metadata", "users", userID+".db"))
		}
		if len(matches) == 0 {
			t.Fatalf("fixture invalid: could not locate the user database under %s", dir)
		}
		dbPath = matches[0]
	}

	// A clean close checkpoints and removes the WAL, so after CloseUser the
	// sidecars usually do not exist and asserting their absence proves nothing.
	// Recreate them to stand in for the state an unclean shutdown leaves behind,
	// which is precisely what the sidecar removal is for.
	if err := mgr.CloseUser(userID); err != nil {
		t.Fatalf("CloseUser: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(dbPath+suffix, []byte("leftover"), 0o600); err != nil {
			t.Fatalf("seeding %s: %v", suffix, err)
		}
	}

	if err := mgr.DeleteUser(userID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists after DeleteUser (err=%v)", p, err)
		}
	}

	// Idempotent: a GC pass may retry after a partial failure.
	if err := mgr.DeleteUser(userID); err != nil {
		t.Errorf("second DeleteUser should be a no-op, got %v", err)
	}
	// And the manager still works afterwards.
	if _, err := mgr.OpenUser(userID); err != nil {
		t.Errorf("OpenUser after DeleteUser: %v", err)
	}
}

func TestMetadataManagerDeleteUserRejectsInvalidID(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewMetadataManager(dir)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	// A traversal attempt must not escape the users directory.
	if err := mgr.DeleteUser("../../etc"); err == nil {
		t.Error("DeleteUser accepted an invalid user id")
	}
}
