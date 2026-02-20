package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestMetadata(t *testing.T) *MetadataIndex {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "metadata.db")
	idx, err := NewMetadataIndex(dbPath)
	if err != nil {
		t.Fatalf("NewMetadataIndex: %v", err)
	}
	t.Cleanup(func() {
		if err := idx.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return idx
}

func TestSchemaCreation(t *testing.T) {
	idx := newTestMetadata(t)

	// Verify tables exist by querying them.
	tables := []string{"storeobject", "maildocument", "eventdocument", "conversation", "accesscontrol"}
	for _, table := range tables {
		var name string
		err := idx.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestSchemaIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metadata.db")

	// Open twice to verify IF NOT EXISTS works.
	idx1, err := NewMetadataIndex(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	idx1.Close()

	idx2, err := NewMetadataIndex(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	idx2.Close()
}

func TestIndexAndQueryByType(t *testing.T) {
	idx := newTestMetadata(t)

	now := time.Now().Truncate(time.Second)

	objects := []StoreObject{
		{GUID: "mail-1", UserID: "u1", Type: DocTypeMail, Size: 1024, CreatedAt: now, ModifiedAt: now},
		{GUID: "mail-2", UserID: "u1", Type: DocTypeMail, Size: 2048, CreatedAt: now.Add(-time.Hour), ModifiedAt: now},
		{GUID: "event-1", UserID: "u1", Type: DocTypeEvent, Size: 512, CreatedAt: now, ModifiedAt: now},
		{GUID: "mail-3", UserID: "u2", Type: DocTypeMail, Size: 4096, CreatedAt: now, ModifiedAt: now},
	}

	for _, obj := range objects {
		if err := idx.IndexObject(obj); err != nil {
			t.Fatalf("IndexObject %q: %v", obj.GUID, err)
		}
	}

	// Query u1 mail.
	results, err := idx.QueryByType("u1", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 mail objects for u1, got %d", len(results))
	}
	// Should be ordered by created_at DESC.
	if results[0].GUID != "mail-1" {
		t.Errorf("first result GUID = %q, want %q", results[0].GUID, "mail-1")
	}
	if results[1].GUID != "mail-2" {
		t.Errorf("second result GUID = %q, want %q", results[1].GUID, "mail-2")
	}

	// Query u1 events.
	events, err := idx.QueryByType("u1", DocTypeEvent)
	if err != nil {
		t.Fatalf("QueryByType events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event for u1, got %d", len(events))
	}

	// Query u2 mail.
	u2mail, err := idx.QueryByType("u2", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType u2: %v", err)
	}
	if len(u2mail) != 1 {
		t.Fatalf("expected 1 mail for u2, got %d", len(u2mail))
	}
}

func TestIndexObjectUpsert(t *testing.T) {
	idx := newTestMetadata(t)

	now := time.Now().Truncate(time.Second)
	obj := StoreObject{
		GUID: "mail-1", UserID: "u1", Type: DocTypeMail,
		Size: 1024, CreatedAt: now, ModifiedAt: now,
	}

	if err := idx.IndexObject(obj); err != nil {
		t.Fatalf("first IndexObject: %v", err)
	}

	// Update size and modified time.
	obj.Size = 2048
	obj.ModifiedAt = now.Add(time.Minute)
	if err := idx.IndexObject(obj); err != nil {
		t.Fatalf("second IndexObject: %v", err)
	}

	results, err := idx.QueryByType("u1", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after upsert, got %d", len(results))
	}
	if results[0].Size != 2048 {
		t.Errorf("size = %d, want 2048", results[0].Size)
	}
}

func TestQueryByDateRange(t *testing.T) {
	idx := newTestMetadata(t)

	base := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

	objects := []StoreObject{
		{GUID: "m1", UserID: "u1", Type: DocTypeMail, CreatedAt: base.Add(-48 * time.Hour), ModifiedAt: base},
		{GUID: "m2", UserID: "u1", Type: DocTypeMail, CreatedAt: base.Add(-24 * time.Hour), ModifiedAt: base},
		{GUID: "m3", UserID: "u1", Type: DocTypeMail, CreatedAt: base, ModifiedAt: base},
		{GUID: "m4", UserID: "u1", Type: DocTypeMail, CreatedAt: base.Add(24 * time.Hour), ModifiedAt: base},
	}

	for _, obj := range objects {
		if err := idx.IndexObject(obj); err != nil {
			t.Fatalf("IndexObject %q: %v", obj.GUID, err)
		}
	}

	// Query last 24 hours from base.
	start := base.Add(-24 * time.Hour)
	results, err := idx.QueryByDateRange("u1", DocTypeMail, start, base)
	if err != nil {
		t.Fatalf("QueryByDateRange: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results in range, got %d", len(results))
	}

	// Results should be ordered DESC.
	if results[0].GUID != "m3" {
		t.Errorf("first = %q, want %q", results[0].GUID, "m3")
	}
	if results[1].GUID != "m2" {
		t.Errorf("second = %q, want %q", results[1].GUID, "m2")
	}
}

func TestQueryByDateRangeEmpty(t *testing.T) {
	idx := newTestMetadata(t)

	now := time.Now()
	results, err := idx.QueryByDateRange("u1", DocTypeMail, now.Add(-time.Hour), now)
	if err != nil {
		t.Fatalf("QueryByDateRange: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestQueryByTypeEmpty(t *testing.T) {
	idx := newTestMetadata(t)

	results, err := idx.QueryByType("nonexistent", DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
