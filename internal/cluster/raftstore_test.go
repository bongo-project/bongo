package cluster

import (
	"testing"

	"github.com/hashicorp/raft"

	"github.com/bongo-go/bongo-go/internal/store"
)

func newTestRaftStore(t *testing.T) *RaftStore {
	t.Helper()
	ps, err := store.NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() {
		if err := ps.Close(); err != nil {
			t.Errorf("closing pebble: %v", err)
		}
	})
	return NewRaftStore(ps)
}

func TestLogStoreEmpty(t *testing.T) {
	rs := newTestRaftStore(t)

	first, err := rs.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex: %v", err)
	}
	if first != 0 {
		t.Errorf("FirstIndex = %d, want 0", first)
	}

	last, err := rs.LastIndex()
	if err != nil {
		t.Fatalf("LastIndex: %v", err)
	}
	if last != 0 {
		t.Errorf("LastIndex = %d, want 0", last)
	}

	var log raft.Log
	err = rs.GetLog(1, &log)
	if err != raft.ErrLogNotFound {
		t.Errorf("GetLog(1) = %v, want ErrLogNotFound", err)
	}
}

func TestLogStoreAndRetrieve(t *testing.T) {
	rs := newTestRaftStore(t)

	logs := []*raft.Log{
		{Index: 1, Term: 1, Type: raft.LogCommand, Data: []byte("cmd-1")},
		{Index: 2, Term: 1, Type: raft.LogCommand, Data: []byte("cmd-2")},
		{Index: 3, Term: 2, Type: raft.LogCommand, Data: []byte("cmd-3")},
	}

	if err := rs.StoreLogs(logs); err != nil {
		t.Fatalf("StoreLogs: %v", err)
	}

	first, err := rs.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex: %v", err)
	}
	if first != 1 {
		t.Errorf("FirstIndex = %d, want 1", first)
	}

	last, err := rs.LastIndex()
	if err != nil {
		t.Fatalf("LastIndex: %v", err)
	}
	if last != 3 {
		t.Errorf("LastIndex = %d, want 3", last)
	}

	var log raft.Log
	if err := rs.GetLog(2, &log); err != nil {
		t.Fatalf("GetLog(2): %v", err)
	}
	if log.Index != 2 {
		t.Errorf("log.Index = %d, want 2", log.Index)
	}
	if log.Term != 1 {
		t.Errorf("log.Term = %d, want 1", log.Term)
	}
	if string(log.Data) != "cmd-2" {
		t.Errorf("log.Data = %q, want %q", log.Data, "cmd-2")
	}
}

func TestLogStoreSingle(t *testing.T) {
	rs := newTestRaftStore(t)

	log := &raft.Log{Index: 5, Term: 3, Type: raft.LogCommand, Data: []byte("single")}
	if err := rs.StoreLog(log); err != nil {
		t.Fatalf("StoreLog: %v", err)
	}

	first, _ := rs.FirstIndex()
	last, _ := rs.LastIndex()
	if first != 5 || last != 5 {
		t.Errorf("first=%d, last=%d, want 5, 5", first, last)
	}
}

func TestDeleteRange(t *testing.T) {
	rs := newTestRaftStore(t)

	for i := uint64(1); i <= 10; i++ {
		if err := rs.StoreLog(&raft.Log{Index: i, Term: 1, Data: []byte("data")}); err != nil {
			t.Fatalf("StoreLog(%d): %v", i, err)
		}
	}

	// Delete logs 3-7 inclusive.
	if err := rs.DeleteRange(3, 7); err != nil {
		t.Fatalf("DeleteRange: %v", err)
	}

	// Logs 1-2 should still exist.
	for _, i := range []uint64{1, 2} {
		var log raft.Log
		if err := rs.GetLog(i, &log); err != nil {
			t.Errorf("GetLog(%d) after delete: %v", i, err)
		}
	}

	// Logs 3-7 should be gone.
	for i := uint64(3); i <= 7; i++ {
		var log raft.Log
		if err := rs.GetLog(i, &log); err != raft.ErrLogNotFound {
			t.Errorf("GetLog(%d) = %v, want ErrLogNotFound", i, err)
		}
	}

	// Logs 8-10 should still exist.
	for _, i := range []uint64{8, 9, 10} {
		var log raft.Log
		if err := rs.GetLog(i, &log); err != nil {
			t.Errorf("GetLog(%d) after delete: %v", i, err)
		}
	}

	first, _ := rs.FirstIndex()
	if first != 1 {
		t.Errorf("FirstIndex after delete = %d, want 1", first)
	}
}

func TestStableStoreRoundTrip(t *testing.T) {
	rs := newTestRaftStore(t)

	// Set and get a byte slice.
	if err := rs.Set([]byte("key1"), []byte("value1")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, err := rs.Get([]byte("key1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("Get = %q, want %q", val, "value1")
	}

	// Get a missing key returns empty slice.
	val, err = rs.Get([]byte("missing"))
	if err != nil {
		t.Fatalf("Get(missing): %v", err)
	}
	if len(val) != 0 {
		t.Errorf("Get(missing) = %q, want empty", val)
	}
}

func TestStableStoreUint64(t *testing.T) {
	rs := newTestRaftStore(t)

	if err := rs.SetUint64([]byte("term"), 42); err != nil {
		t.Fatalf("SetUint64: %v", err)
	}

	val, err := rs.GetUint64([]byte("term"))
	if err != nil {
		t.Fatalf("GetUint64: %v", err)
	}
	if val != 42 {
		t.Errorf("GetUint64 = %d, want 42", val)
	}

	// Missing key returns 0.
	val, err = rs.GetUint64([]byte("missing"))
	if err != nil {
		t.Fatalf("GetUint64(missing): %v", err)
	}
	if val != 0 {
		t.Errorf("GetUint64(missing) = %d, want 0", val)
	}
}

func TestStableStoreOverwrite(t *testing.T) {
	rs := newTestRaftStore(t)

	if err := rs.Set([]byte("key"), []byte("v1")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := rs.Set([]byte("key"), []byte("v2")); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}

	val, err := rs.Get([]byte("key"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(val) != "v2" {
		t.Errorf("Get = %q, want %q", val, "v2")
	}
}

func TestLogKeyOrdering(t *testing.T) {
	// Verify that zero-padded keys sort correctly for large index values.
	rs := newTestRaftStore(t)

	indices := []uint64{1, 100, 10000, 1000000000, 18446744073709551615}
	for _, idx := range indices {
		if err := rs.StoreLog(&raft.Log{Index: idx, Term: 1, Data: []byte("d")}); err != nil {
			t.Fatalf("StoreLog(%d): %v", idx, err)
		}
	}

	first, err := rs.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex: %v", err)
	}
	if first != 1 {
		t.Errorf("FirstIndex = %d, want 1", first)
	}

	last, err := rs.LastIndex()
	if err != nil {
		t.Fatalf("LastIndex: %v", err)
	}
	if last != 18446744073709551615 {
		t.Errorf("LastIndex = %d, want max uint64", last)
	}
}

// TestLastIndexIsCorrectAcrossDigitBoundaries guards the assumption that makes
// LastIndex a seek rather than a scan: log keys are zero-padded to a fixed
// width, so the lexicographically greatest key is the numerically greatest
// index. Without the padding, "9" would sort above "10" and LastIndex would
// silently return the wrong entry — which for a raft log store means data loss,
// not a slow query. The indices below straddle several digit boundaries.
func TestLastIndexIsCorrectAcrossDigitBoundaries(t *testing.T) {
	rs := newTestRaftStore(t)

	indices := []uint64{1, 2, 9, 10, 11, 99, 100, 101, 999, 1000, 8194, 12345}
	for _, idx := range indices {
		if err := rs.StoreLog(&raft.Log{Index: idx, Term: 1, Type: raft.LogCommand, Data: []byte("x")}); err != nil {
			t.Fatalf("StoreLog(%d): %v", idx, err)
		}
	}

	last, err := rs.LastIndex()
	if err != nil {
		t.Fatalf("LastIndex: %v", err)
	}
	if want := uint64(12345); last != want {
		t.Errorf("LastIndex = %d, want %d — key ordering is not numeric, so seeking to the last key is unsafe", last, want)
	}

	first, err := rs.FirstIndex()
	if err != nil {
		t.Fatalf("FirstIndex: %v", err)
	}
	if want := uint64(1); first != want {
		t.Errorf("FirstIndex = %d, want %d", first, want)
	}
}

// TestLastIndexAfterTruncation checks LastIndex still reports correctly after
// the kinds of range deletion raft performs (prefix truncation after a
// snapshot, and suffix truncation of conflicting entries). A cached last-index
// would need explicit invalidation here; seeking derives it from the data, so
// it cannot go stale.
func TestLastIndexAfterTruncation(t *testing.T) {
	rs := newTestRaftStore(t)

	for i := uint64(1); i <= 100; i++ {
		if err := rs.StoreLog(&raft.Log{Index: i, Term: 1, Type: raft.LogCommand, Data: []byte("x")}); err != nil {
			t.Fatalf("StoreLog(%d): %v", i, err)
		}
	}

	// Prefix truncation: raft discards entries covered by a snapshot.
	if err := rs.DeleteRange(1, 50); err != nil {
		t.Fatalf("DeleteRange(1,50): %v", err)
	}
	if first, err := rs.FirstIndex(); err != nil || first != 51 {
		t.Errorf("FirstIndex after prefix truncation = %d (err %v), want 51", first, err)
	}
	if last, err := rs.LastIndex(); err != nil || last != 100 {
		t.Errorf("LastIndex after prefix truncation = %d (err %v), want 100", last, err)
	}

	// Suffix truncation: raft discards conflicting tail entries.
	if err := rs.DeleteRange(90, 100); err != nil {
		t.Fatalf("DeleteRange(90,100): %v", err)
	}
	if last, err := rs.LastIndex(); err != nil || last != 89 {
		t.Errorf("LastIndex after suffix truncation = %d (err %v), want 89", last, err)
	}
}
