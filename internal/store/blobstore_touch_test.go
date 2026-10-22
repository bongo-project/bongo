package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func backdateBlob(t *testing.T, bs *BlobStore, hash string) time.Time {
	t.Helper()
	old := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(bs.blobPath(hash), old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	return old
}

func TestDedupHitRefreshesMtime(t *testing.T) {
	data := []byte("a deduplicated attachment body")
	tests := []struct {
		name string
		put  func(bs *BlobStore) error
	}{
		{"Put", func(bs *BlobStore) error { _, err := bs.Put(data); return err }},
		{"PutReader", func(bs *BlobStore) error { _, _, err := bs.PutReader(bytes.NewReader(data)); return err }},
		{"writeAtomically existing", func(bs *BlobStore) error {
			return bs.writeAtomically(sha256Hex(data), func(f *os.File) error { _, err := f.Write(data); return err })
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bs := newTestBlobStore(t)
			hash, err := bs.Put(data)
			if err != nil {
				t.Fatalf("Put: %v", err)
			}
			old := backdateBlob(t, bs, hash)
			if err := tc.put(bs); err != nil {
				t.Fatalf("dedup write: %v", err)
			}
			mt, err := bs.ModTime(hash)
			if err != nil {
				t.Fatalf("ModTime: %v", err)
			}
			if !mt.After(old.Add(time.Hour)) {
				t.Errorf("mtime = %v, want refreshed well past %v", mt, old)
			}
		})
	}
}

func TestDeleteIfNotModifiedSince(t *testing.T) {
	cutoff := time.Now().Add(-time.Hour)
	tests := []struct {
		name        string
		backdate    bool
		wantDeleted bool
	}{
		{"old blob deleted", true, true},
		{"young blob spared", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bs := newTestBlobStore(t)
			hash, _ := bs.Put([]byte("candidate " + tc.name))
			if tc.backdate {
				backdateBlob(t, bs, hash)
			}
			got, err := bs.DeleteIfNotModifiedSince(hash, cutoff)
			if err != nil || got != tc.wantDeleted {
				t.Fatalf("got (%v, %v), want (%v, nil)", got, err, tc.wantDeleted)
			}
			if bs.Exists(hash) == tc.wantDeleted {
				t.Errorf("Exists = %v after delete=%v", bs.Exists(hash), tc.wantDeleted)
			}
		})
	}

	t.Run("missing blob", func(t *testing.T) {
		bs := newTestBlobStore(t)
		got, err := bs.DeleteIfNotModifiedSince(sha256Hex([]byte("nope")), cutoff)
		if err != nil || got {
			t.Errorf("got (%v, %v), want (false, nil)", got, err)
		}
	})
	t.Run("invalid hash", func(t *testing.T) {
		bs := newTestBlobStore(t)
		if _, err := bs.DeleteIfNotModifiedSince("../x", cutoff); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("err = %v, want ErrInvalidHash", err)
		}
	})
}

func TestPutAfterConditionalDeleteRecreatesBlob(t *testing.T) {
	bs := newTestBlobStore(t)
	data := []byte("deleted then re-put")
	hash, _ := bs.Put(data)
	backdateBlob(t, bs, hash)
	if ok, err := bs.DeleteIfNotModifiedSince(hash, time.Now().Add(-time.Hour)); !ok || err != nil {
		t.Fatalf("delete = (%v, %v)", ok, err)
	}
	if _, err := bs.Put(data); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !bs.Exists(hash) {
		t.Error("blob not recreated")
	}
}

// The lock is what makes stat-then-remove atomic with respect to a dedup
// touch: a touch landing between the two would be lost.
func TestDeleteIfNotModifiedSinceHoldsLockWhileRemoving(t *testing.T) {
	bs := newTestBlobStore(t)
	hash, err := bs.Put([]byte("blob removed under lock"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	backdateBlob(t, bs, hash)

	called := false
	lockHeld := false
	bs.beforeRemove = func() {
		called = true
		if bs.mu.TryLock() {
			bs.mu.Unlock()
			return
		}
		lockHeld = true
	}
	ok, err := bs.DeleteIfNotModifiedSince(hash, time.Now().Add(-time.Hour))
	if err != nil || !ok {
		t.Fatalf("DeleteIfNotModifiedSince = %v, %v; want true, nil", ok, err)
	}
	if !called {
		t.Fatal("beforeRemove hook was not reached")
	}
	if !lockHeld {
		t.Error("bs.mu was not held between the mtime check and the removal")
	}
}

// The touch must hold bs.mu while it runs Chtimes, or it can interleave with
// the GC's check-then-remove and lose the race the lock exists to close.
func TestTouchIfExistsHoldsLockWhileTouching(t *testing.T) {
	bs := newTestBlobStore(t)
	hash, err := bs.Put([]byte("blob touched under lock"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	called := false
	lockHeld := false
	bs.chtimes = func(name string, atime, mtime time.Time) error {
		called = true
		if bs.mu.TryLock() {
			bs.mu.Unlock()
		} else {
			lockHeld = true
		}
		return os.Chtimes(name, atime, mtime)
	}
	hit, err := bs.touchIfExists(hash)
	if err != nil || !hit {
		t.Fatalf("touchIfExists = %v, %v; want true, nil", hit, err)
	}
	if !called {
		t.Fatal("chtimes hook was not reached")
	}
	if !lockHeld {
		t.Error("bs.mu was not held while touching the blob")
	}
}

// A readable blob whose mtime cannot be refreshed (EPERM) is still a dedup
// hit: the writer's rename would fail on the same directory, so reporting a
// miss would turn a working delivery into a failure.
func TestTouchIfExistsChtimesDeniedTreatsReadableBlobAsPresent(t *testing.T) {
	bs := newTestBlobStore(t)
	hash, err := bs.Put([]byte("blob we may read but not touch"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	bs.chtimes = func(string, time.Time, time.Time) error { return os.ErrPermission }
	for range 2 {
		hit, err := bs.touchIfExists(hash)
		if err != nil || !hit {
			t.Errorf("touchIfExists = %v, %v; want true, nil", hit, err)
		}
	}
	if !bs.warnedTouchPerm.Load() {
		t.Error("permission warning was not recorded")
	}
	if _, err := bs.Put([]byte("blob we may read but not touch")); err != nil {
		t.Errorf("Put on a permission-denied dedup hit: %v", err)
	}
}

// A blob in an unsearchable directory is not readable either, so the touch
// reports a miss and the caller's write proceeds as before.
func TestTouchIfExistsPermissionDeniedFallsThrough(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	bs := newTestBlobStore(t)
	// A path component that is a non-searchable directory yields EACCES from
	// Chtimes without needing a second uid.
	data := []byte("blob in an unsearchable prefix dir")
	hash := sha256Hex(data)
	dir := filepath.Dir(bs.blobPath(hash))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
	hit, err := bs.touchIfExists(hash)
	if err != nil || hit {
		t.Errorf("touchIfExists = %v, %v; want false, nil", hit, err)
	}
}
