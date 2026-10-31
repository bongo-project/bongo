package store

import "testing"

// Pebble pools iterators: a second Close of the same pebble.Iterator acts on
// whichever iterator reused its allocation. The wrapper must make a repeated
// Close a no-op so that one caller's mistake cannot break another's scan.
func TestPebbleIteratorCloseTwice(t *testing.T) {
	s := newTestStore(t)
	for _, k := range []string{"/user/a/x", "/user/b/x", "/user/c/x"} {
		if err := s.Set([]byte(k), []byte("v")); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	first, err := s.PrefixScan([]byte("/user/"))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	first.Next()
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Takes the allocation the first iterator returned to Pebble's pool.
	second, err := s.PrefixScan([]byte("/user/"))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	defer second.Close()

	if err := first.Close(); err != nil {
		t.Fatalf("second Close of the same iterator: %v", err)
	}

	n := 0
	for second.Next() {
		n++
	}
	if n != 3 {
		t.Fatalf("other iterator saw %d keys after a repeated Close, want 3", n)
	}
}
