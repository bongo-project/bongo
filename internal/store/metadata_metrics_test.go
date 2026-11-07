package store

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/bongo-project/bongo/internal/observability"
)

// TestUserDBMetricsMoveWithLRU drives a real eviction and checks the open gauge
// and eviction counter. Deltas, not absolutes: the collectors are process-wide.
func TestUserDBMetricsMoveWithLRU(t *testing.T) {
	mgr, err := NewMetadataManager(t.TempDir(), WithMaxOpen(2))
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	openBase := testutil.ToFloat64(observability.UserDBsOpen)
	evictBase := testutil.ToFloat64(observability.UserDBEvictions)

	check := func(step string, wantOpen, wantEvict float64) {
		t.Helper()
		if got := testutil.ToFloat64(observability.UserDBsOpen) - openBase; got != wantOpen {
			t.Errorf("%s: open gauge delta = %v, want %v", step, got, wantOpen)
		}
		if got := testutil.ToFloat64(observability.UserDBEvictions) - evictBase; got != wantEvict {
			t.Errorf("%s: eviction delta = %v, want %v", step, got, wantEvict)
		}
	}

	for _, id := range []string{"user-a", "user-b"} {
		if _, err := mgr.OpenUser(id); err != nil {
			t.Fatalf("OpenUser %s: %v", id, err)
		}
	}
	check("two opened", 2, 0)

	if _, err := mgr.OpenUser("user-c"); err != nil {
		t.Fatalf("OpenUser user-c: %v", err)
	}
	if mgr.OpenCount() != 2 {
		t.Fatalf("OpenCount = %d, want 2", mgr.OpenCount())
	}
	check("third evicts oldest", 2, 1)

	if _, err := mgr.OpenUser("user-c"); err != nil {
		t.Fatalf("reopen cached: %v", err)
	}
	check("cached reopen", 2, 1)

	// An explicit close is not a capacity eviction.
	if err := mgr.CloseUser("user-c"); err != nil {
		t.Fatalf("CloseUser: %v", err)
	}
	check("explicit close", 1, 1)

	// DeleteUser goes through CloseUser: it must not decrement twice.
	if err := mgr.DeleteUser("user-b"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	check("delete of an open user", 0, 1)

	// Deleting a user that is not open must not move the gauge.
	if err := mgr.DeleteUser("user-a"); err != nil {
		t.Fatalf("DeleteUser user-a: %v", err)
	}
	check("delete of a closed user", 0, 1)

	for _, id := range []string{"user-d", "user-e"} {
		if _, err := mgr.OpenUser(id); err != nil {
			t.Fatalf("OpenUser %s: %v", id, err)
		}
	}
	check("refilled", 2, 1)

	if _, err := mgr.OpenUser("user-f"); err != nil {
		t.Fatalf("OpenUser user-f: %v", err)
	}
	check("second eviction", 2, 2)

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	check("manager closed", 0, 2)
}

func TestWithMaxOpenIgnoresNonPositive(t *testing.T) {
	for _, n := range []int{0, -5} {
		mgr, err := NewMetadataManager(t.TempDir(), WithMaxOpen(n))
		if err != nil {
			t.Fatalf("NewMetadataManager: %v", err)
		}
		if mgr.maxOpen != 256 {
			t.Errorf("WithMaxOpen(%d): maxOpen = %d, want default 256", n, mgr.maxOpen)
		}
		if err := mgr.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}
}
