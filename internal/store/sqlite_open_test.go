package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// TestOpenSQLiteDBEveryConnectionWaitsOnBusy: busy_timeout is per connection,
// and *sql.DB is a pool. Setting it with db.Exec reached only the one pooled
// connection that ran the statement; every other connection kept SQLite's
// default of 0 and failed a contended write at once with SQLITE_BUSY
// (TestSweepGhostUserTrees in CI, from auth.CreateUser's address upsert).
func TestOpenSQLiteDBEveryConnectionWaitsOnBusy(t *testing.T) {
	db, err := openSQLiteDB(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	// Hold three distinct connections at once so the pool must open new ones.
	var conns []*sql.Conn
	for i := 0; i < 3; i++ {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for i, c := range conns {
		var timeout int
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		if timeout < 1000 {
			t.Errorf("connection %d: busy_timeout = %d, want the open-time setting on every connection", i, timeout)
		}
		var mode string
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if mode != "wal" {
			t.Errorf("connection %d: journal_mode = %q, want wal", i, mode)
		}
	}
	for _, c := range conns {
		_ = c.Close()
	}
}

// TestOpenSQLiteDBContendedWriteWaits: a second writer waits for the first to
// commit instead of failing with SQLITE_BUSY.
func TestOpenSQLiteDBContendedWriteWaits(t *testing.T) {
	db, err := openSQLiteDB(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Exec("CREATE TABLE t (v INTEGER)"); err != nil {
		t.Fatal(err)
	}

	holder, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.ExecContext(ctx, "INSERT INTO t VALUES (1)"); err != nil {
		t.Fatal(err)
	}

	// A second, fresh connection writes while the first holds the lock.
	writer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		_, err := writer.ExecContext(ctx, "INSERT INTO t VALUES (2)")
		done <- err
	}()

	time.Sleep(200 * time.Millisecond)
	if _, err := holder.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("contended write failed instead of waiting: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("contended write did not finish")
	}
}
