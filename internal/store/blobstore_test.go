package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newTestBlobStore(t *testing.T) *BlobStore {
	t.Helper()
	bs, err := NewBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}
	return bs
}

func TestBlobPutGetRoundTrip(t *testing.T) {
	bs := newTestBlobStore(t)

	data := []byte("hello, blob store!")
	hash, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Verify hash is correct.
	expected := sha256Hex(data)
	if hash != expected {
		t.Errorf("hash = %s, want %s", hash, expected)
	}

	// Read back.
	rc, err := bs.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("data mismatch: got %q, want %q", got, data)
	}
}

func TestBlobDeduplication(t *testing.T) {
	bs := newTestBlobStore(t)

	data := []byte("dedup test content")
	hash1, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}

	hash2, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hashes differ: %s vs %s", hash1, hash2)
	}

	// Count files on disk: should be exactly one blob file.
	hashes, err := bs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(hashes) != 1 {
		t.Errorf("expected 1 blob on disk, got %d", len(hashes))
	}
}

func TestBlobPutReaderLarge(t *testing.T) {
	bs := newTestBlobStore(t)

	// Generate >1MB of random data.
	data := make([]byte, 1<<20+1024) // ~1MB + 1KB
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	hash, size, err := bs.PutReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("PutReader: %v", err)
	}

	if size != int64(len(data)) {
		t.Errorf("size = %d, want %d", size, len(data))
	}

	expected := sha256Hex(data)
	if hash != expected {
		t.Errorf("hash = %s, want %s", hash, expected)
	}

	// Read back and verify.
	rc, err := bs.Get(hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("large blob data mismatch")
	}
}

func TestBlobPutReaderDedup(t *testing.T) {
	bs := newTestBlobStore(t)

	data := []byte("streaming dedup")
	hash1, _, err := bs.PutReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("PutReader 1: %v", err)
	}

	hash2, _, err := bs.PutReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("PutReader 2: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("hashes differ: %s vs %s", hash1, hash2)
	}
}

func TestBlobVerify(t *testing.T) {
	bs := newTestBlobStore(t)

	data := []byte("verify me")
	hash, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Verify should pass.
	ok, err := bs.Verify(hash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Error("Verify returned false for valid blob")
	}

	// Corrupt the blob on disk.
	path := bs.blobPath(hash)
	if err := os.WriteFile(path, []byte("corrupted"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Verify should now fail.
	ok, err = bs.Verify(hash)
	if err != nil {
		t.Fatalf("Verify after corruption: %v", err)
	}
	if ok {
		t.Error("Verify returned true for corrupted blob")
	}
}

func TestBlobExists(t *testing.T) {
	bs := newTestBlobStore(t)

	data := []byte("exists test")
	hash, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !bs.Exists(hash) {
		t.Error("Exists returned false for stored blob")
	}

	fakeHash := hex.EncodeToString(sha256.New().Sum(nil))
	if bs.Exists(fakeHash) {
		t.Error("Exists returned true for non-existent blob")
	}
}

func TestBlobDelete(t *testing.T) {
	bs := newTestBlobStore(t)

	data := []byte("delete me")
	hash, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := bs.Delete(hash); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if bs.Exists(hash) {
		t.Error("blob still exists after delete")
	}

	// Delete nonexistent should not error.
	if err := bs.Delete(hash); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestBlobList(t *testing.T) {
	bs := newTestBlobStore(t)

	contents := []string{"blob-a", "blob-b", "blob-c"}
	expected := make(map[string]bool)

	for _, c := range contents {
		hash, err := bs.Put([]byte(c))
		if err != nil {
			t.Fatalf("Put %q: %v", c, err)
		}
		expected[hash] = true
	}

	hashes, err := bs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(hashes) != len(expected) {
		t.Fatalf("List returned %d hashes, want %d", len(hashes), len(expected))
	}

	for _, h := range hashes {
		if !expected[h] {
			t.Errorf("unexpected hash in List: %s", h)
		}
	}
}

func TestBlobConcurrentDifferent(t *testing.T) {
	bs := newTestBlobStore(t)

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := []byte(fmt.Sprintf("concurrent-blob-%d", i))
			_, err := bs.Put(data)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", i, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	hashes, err := bs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(hashes) != 10 {
		t.Errorf("expected 10 blobs, got %d", len(hashes))
	}
}

func TestBlobConcurrentSameContent(t *testing.T) {
	bs := newTestBlobStore(t)

	data := []byte("same content for all goroutines")
	var wg sync.WaitGroup
	hashes := make([]string, 10)
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := bs.Put(data)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", i, err)
				return
			}
			hashes[i] = h
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}

	// All hashes should be the same.
	for i := 1; i < 10; i++ {
		if hashes[i] != hashes[0] {
			t.Errorf("hash[%d] = %s, want %s", i, hashes[i], hashes[0])
		}
	}

	// Only one blob on disk.
	listed, err := bs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("expected 1 blob after concurrent dedup, got %d", len(listed))
	}
}

func TestBlobFileLayout(t *testing.T) {
	bs := newTestBlobStore(t)

	data := []byte("layout test")
	hash, err := bs.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Verify file is at {blobDir}/{prefix}/{hash}.
	expectedPath := filepath.Join(bs.blobDir, hash[:2], hash)
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("blob not at expected path: %s", expectedPath)
	}
}

func TestBlobGetNotFound(t *testing.T) {
	bs := newTestBlobStore(t)

	_, err := bs.Get("0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Error("expected error for non-existent blob")
	}
}

