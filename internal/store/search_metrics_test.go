package store

import (
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/bongo-project/bongo/internal/observability"
)

// TestSearchIndexMetricsMoveWithLRU drives real evictions, an explicit delete
// and Close, checking every removal path decrements the open gauge exactly
// once. Deltas, not absolutes: the collectors are process-wide.
func TestSearchIndexMetricsMoveWithLRU(t *testing.T) {
	sm, err := NewSearchManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewSearchManager: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = sm.Close()
		}
	})
	sm.maxOpen = 2
	openBase := testutil.ToFloat64(observability.SearchIndexesOpen)
	evictBase := testutil.ToFloat64(observability.SearchIndexEvictions)

	check := func(step string, wantOpen, wantEvict float64) {
		t.Helper()
		if got := testutil.ToFloat64(observability.SearchIndexesOpen) - openBase; got != wantOpen {
			t.Errorf("%s: open gauge delta = %v, want %v", step, got, wantOpen)
		}
		if got := testutil.ToFloat64(observability.SearchIndexEvictions) - evictBase; got != wantEvict {
			t.Errorf("%s: eviction delta = %v, want %v", step, got, wantEvict)
		}
	}

	for i := range 5 {
		if err := sm.IndexMail(fmt.Sprintf("user-%d", i), "g1", "subject", "a@example.com", nil, "body", 1, false, nil); err != nil {
			t.Fatalf("IndexMail: %v", err)
		}
	}
	check("five opened with limit two", 2, 3)

	// A cached reopen neither opens nor evicts.
	if err := sm.IndexMail("user-4", "g2", "subject", "a@example.com", nil, "body", 1, false, nil); err != nil {
		t.Fatalf("IndexMail: %v", err)
	}
	check("cached reopen", 2, 3)

	// Deleting a user is not a capacity eviction.
	if err := sm.DeleteUser("user-4"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	check("delete user", 1, 3)

	closed = true
	if err := sm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	check("closed", 0, 3)
}
