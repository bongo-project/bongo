package store

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func newTestStore(t *testing.T) *PebbleStore {
	t.Helper()
	store, err := NewPebbleStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func TestSetAndGet(t *testing.T) {
	store := newTestStore(t)

	key := []byte("/user/u1/mail/abc")
	val := []byte("hello world")

	if err := store.Set(key, val); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := store.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, val) {
		t.Errorf("got %q, want %q", got, val)
	}
}

func TestGetNotFound(t *testing.T) {
	store := newTestStore(t)

	_, err := store.Get([]byte("/nonexistent"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	store := newTestStore(t)

	key := []byte("/user/u1/mail/abc")
	if err := store.Set(key, []byte("data")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if err := store.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Get(key)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteNonexistent(t *testing.T) {
	store := newTestStore(t)

	// Deleting a nonexistent key should not error in Pebble.
	if err := store.Delete([]byte("/nonexistent")); err != nil {
		t.Errorf("Delete nonexistent key: %v", err)
	}
}

func TestPrefixScan(t *testing.T) {
	store := newTestStore(t)

	// Insert keys with different prefixes.
	keys := []string{
		"/user/u1/mail/001",
		"/user/u1/mail/002",
		"/user/u1/mail/003",
		"/user/u1/event/001",
		"/user/u2/mail/001",
	}
	for _, k := range keys {
		if err := store.Set([]byte(k), []byte("val-"+k)); err != nil {
			t.Fatalf("Set %q: %v", k, err)
		}
	}

	iter, err := store.PrefixScan([]byte("/user/u1/mail/"))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	defer iter.Close()

	var found []string
	for iter.Next() {
		found = append(found, string(iter.Key()))
	}

	if len(found) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(found), found)
	}
	for i, want := range []string{
		"/user/u1/mail/001",
		"/user/u1/mail/002",
		"/user/u1/mail/003",
	} {
		if found[i] != want {
			t.Errorf("key[%d] = %q, want %q", i, found[i], want)
		}
	}
}

func TestPrefixScanEmpty(t *testing.T) {
	store := newTestStore(t)

	iter, err := store.PrefixScan([]byte("/nonexistent/"))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	defer iter.Close()

	if iter.Next() {
		t.Error("expected no results for empty prefix scan")
	}
}

func TestPrefixScanValues(t *testing.T) {
	store := newTestStore(t)

	for i := 0; i < 3; i++ {
		k := fmt.Appendf(nil, "/data/%03d", i)
		v := fmt.Appendf(nil, "value-%d", i)
		if err := store.Set(k, v); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}

	iter, err := store.PrefixScan([]byte("/data/"))
	if err != nil {
		t.Fatalf("PrefixScan: %v", err)
	}
	defer iter.Close()

	i := 0
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			t.Fatalf("Value: %v", err)
		}
		want := fmt.Sprintf("value-%d", i)
		if string(val) != want {
			t.Errorf("value[%d] = %q, want %q", i, val, want)
		}
		i++
	}
	if i != 3 {
		t.Errorf("iterated %d items, want 3", i)
	}
}

func TestOverwrite(t *testing.T) {
	store := newTestStore(t)

	key := []byte("/user/u1/mail/abc")
	if err := store.Set(key, []byte("v1")); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := store.Set(key, []byte("v2")); err != nil {
		t.Fatalf("Set v2: %v", err)
	}

	got, err := store.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "v2" {
		t.Errorf("got %q, want %q", got, "v2")
	}
}

func TestWithCacheSize(t *testing.T) {
	store, err := NewPebbleStore(t.TempDir(), WithCacheSize(8<<20))
	if err != nil {
		t.Fatalf("NewPebbleStore with cache: %v", err)
	}
	defer store.Close()

	if err := store.Set([]byte("k"), []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get([]byte("k"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "v" {
		t.Errorf("got %q, want %q", got, "v")
	}
}
