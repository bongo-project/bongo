package store

import (
	"testing"
	"time"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func newVerifyTestEnv(t *testing.T) *AddressVerifyStore {
	t.Helper()
	dir := t.TempDir()

	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	return NewAddressVerifyStore(ps, &directApplier{store: ps})
}

func TestAddressVerifyStore_PutGetDelete(t *testing.T) {
	avs := newVerifyTestEnv(t)

	ch := &bongov1.AddressVerification{
		CodeHash:  []byte("abc123hash"),
		ExpiresAt: time.Now().Add(25 * time.Minute).Unix(),
		Attempts:  0,
	}

	if err := avs.Put("user1", "alice@example.com", ch); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok, err := avs.Get("user1", "alice@example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get returned not-found after Put")
	}
	if string(got.CodeHash) != "abc123hash" {
		t.Errorf("CodeHash = %q, want abc123hash", got.CodeHash)
	}
	if got.ExpiresAt != ch.ExpiresAt {
		t.Errorf("ExpiresAt = %d, want %d", got.ExpiresAt, ch.ExpiresAt)
	}

	// Delete and verify not-found.
	if err := avs.Delete("user1", "alice@example.com"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, ok2, err2 := avs.Get("user1", "alice@example.com")
	if err2 != nil {
		t.Fatalf("Get after Delete: %v", err2)
	}
	if ok2 {
		t.Error("challenge still present after Delete")
	}
}

func TestAddressVerifyStore_NotFound(t *testing.T) {
	avs := newVerifyTestEnv(t)

	_, ok, err := avs.Get("nonexistent-user", "nobody@example.com")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Error("expected not-found for non-existent challenge")
	}
}

func TestAddressVerifyStore_DeleteNonexistent_Idempotent(t *testing.T) {
	avs := newVerifyTestEnv(t)

	// Deleting a non-existent key must not error.
	if err := avs.Delete("user1", "nobody@example.com"); err != nil {
		t.Fatalf("Delete of non-existent key: %v", err)
	}
}

func TestAddressVerifyStore_IsolatedPerUser(t *testing.T) {
	avs := newVerifyTestEnv(t)

	ch1 := &bongov1.AddressVerification{
		CodeHash:  []byte("hash-user1"),
		ExpiresAt: time.Now().Add(25 * time.Minute).Unix(),
	}
	ch2 := &bongov1.AddressVerification{
		CodeHash:  []byte("hash-user2"),
		ExpiresAt: time.Now().Add(25 * time.Minute).Unix(),
	}

	if err := avs.Put("user1", "shared@example.com", ch1); err != nil {
		t.Fatalf("Put user1: %v", err)
	}
	if err := avs.Put("user2", "shared@example.com", ch2); err != nil {
		t.Fatalf("Put user2: %v", err)
	}

	got1, ok1, err := avs.Get("user1", "shared@example.com")
	if err != nil || !ok1 {
		t.Fatalf("Get user1: ok=%v err=%v", ok1, err)
	}
	got2, ok2, err := avs.Get("user2", "shared@example.com")
	if err != nil || !ok2 {
		t.Fatalf("Get user2: ok=%v err=%v", ok2, err)
	}

	if string(got1.CodeHash) != "hash-user1" {
		t.Errorf("user1 CodeHash = %q, want hash-user1", got1.CodeHash)
	}
	if string(got2.CodeHash) != "hash-user2" {
		t.Errorf("user2 CodeHash = %q, want hash-user2", got2.CodeHash)
	}
}

func TestAddressVerifyStore_AttemptCountPersisted(t *testing.T) {
	avs := newVerifyTestEnv(t)

	ch := &bongov1.AddressVerification{
		CodeHash:  []byte("somehash"),
		ExpiresAt: time.Now().Add(25 * time.Minute).Unix(),
		Attempts:  3,
	}
	if err := avs.Put("user1", "alice@example.com", ch); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok, err := avs.Get("user1", "alice@example.com")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", got.Attempts)
	}
}
