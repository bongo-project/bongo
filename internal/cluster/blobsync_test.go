package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/store"
)

type mockMemberLister struct {
	members []MemberInfo
}

func (m *mockMemberLister) Members() []MemberInfo {
	return m.members
}

func TestBlobSyncPull(t *testing.T) {
	// Create a blob on the "remote" server.
	remoteDir := t.TempDir()
	remoteBlobs, err := store.NewBlobStore(remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("test blob content for sync")
	hash, err := remoteBlobs.Put(data)
	if err != nil {
		t.Fatal(err)
	}

	// Set up a test HTTP server that serves the blob.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blobHash := r.URL.Path[len("/internal/blob/"):]
		if blobHash != hash {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rc, err := remoteBlobs.Get(hash)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer rc.Close()
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			buf := make([]byte, 4096)
			for {
				n, err := rc.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
		}
	}))
	defer srv.Close()

	// Create a local blob store (empty).
	localDir := t.TempDir()
	localBlobs, err := store.NewBlobStore(localDir)
	if err != nil {
		t.Fatal(err)
	}

	members := &mockMemberLister{
		members: []MemberInfo{
			{Name: "peer-1", HTTPAddr: srv.Listener.Addr().String()},
		},
	}

	syncer := NewBlobSyncer(localBlobs, members, testLogger())

	// Pull the blob.
	if err := syncer.EnsureBlob(hash); err != nil {
		t.Fatalf("EnsureBlob: %v", err)
	}

	// Verify it exists locally.
	if !localBlobs.Exists(hash) {
		t.Error("blob not found locally after sync")
	}
}

func TestBlobSyncAlreadyLocal(t *testing.T) {
	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("already here")
	hash, err := blobs.Put(data)
	if err != nil {
		t.Fatal(err)
	}

	syncer := NewBlobSyncer(blobs, &mockMemberLister{}, testLogger())

	// Should be a no-op.
	if err := syncer.EnsureBlob(hash); err != nil {
		t.Fatalf("EnsureBlob for local blob: %v", err)
	}
}

func TestBlobSyncHashMismatch(t *testing.T) {
	// Server returns wrong content.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			w.Write([]byte("wrong content"))
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	members := &mockMemberLister{
		members: []MemberInfo{
			{Name: "peer-1", HTTPAddr: srv.Listener.Addr().String()},
		},
	}

	syncer := NewBlobSyncer(blobs, members, testLogger())

	fakeHash := sha256hex([]byte("expected content"))
	err = syncer.EnsureBlob(fakeHash)
	if err == nil {
		t.Error("expected error for hash mismatch")
	}
}

func TestBlobSyncEnqueueAndRun(t *testing.T) {
	remoteDir := t.TempDir()
	remoteBlobs, err := store.NewBlobStore(remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("async sync content")
	hash, err := remoteBlobs.Put(data)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blobHash := r.URL.Path[len("/internal/blob/"):]
		if blobHash != hash {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rc, err := remoteBlobs.Get(hash)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer rc.Close()
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			buf := make([]byte, 4096)
			for {
				n, err := rc.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
		}
	}))
	defer srv.Close()

	localDir := t.TempDir()
	localBlobs, err := store.NewBlobStore(localDir)
	if err != nil {
		t.Fatal(err)
	}

	members := &mockMemberLister{
		members: []MemberInfo{
			{Name: "peer-1", HTTPAddr: srv.Listener.Addr().String()},
		},
	}

	syncer := NewBlobSyncer(localBlobs, members, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go syncer.Run(ctx)

	syncer.Enqueue(hash)

	// Wait for sync to complete.
	deadline := time.Now().Add(5 * time.Second)
	for !localBlobs.Exists(hash) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()

	if !localBlobs.Exists(hash) {
		t.Error("blob not synced via Run")
	}
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
