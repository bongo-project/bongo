package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/bongo-go/bongo-go/internal/store"
)

// MemberLister provides the list of cluster members.
type MemberLister interface {
	Members() []MemberInfo
}

// BlobSyncer pulls missing blobs from peer nodes.
type BlobSyncer struct {
	blobs   *store.BlobStore
	members MemberLister
	logger  *slog.Logger
	queue   chan string
	client  *http.Client
}

// NewBlobSyncer creates a new BlobSyncer.
func NewBlobSyncer(blobs *store.BlobStore, members MemberLister, logger *slog.Logger) *BlobSyncer {
	return &BlobSyncer{
		blobs:   blobs,
		members: members,
		logger:  logger,
		queue:   make(chan string, 1024),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Enqueue adds a blob hash to the sync queue if it doesn't exist locally.
func (bs *BlobSyncer) Enqueue(hash string) {
	if bs.blobs.Exists(hash) {
		return
	}
	select {
	case bs.queue <- hash:
	default:
		bs.logger.Warn("blobsync: queue full, dropping hash", "hash", hash[:12])
	}
}

// Run processes the sync queue until the context is cancelled.
func (bs *BlobSyncer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case hash := <-bs.queue:
			if bs.blobs.Exists(hash) {
				continue
			}
			if err := bs.pull(hash); err != nil {
				bs.logger.Warn("blobsync: pull failed", "hash", hash[:12], "error", err)
			}
		}
	}
}

// EnsureBlob synchronously pulls a blob if it doesn't exist locally.
func (bs *BlobSyncer) EnsureBlob(hash string) error {
	if bs.blobs.Exists(hash) {
		return nil
	}
	return bs.pull(hash)
}

// pull fetches a blob from a peer that has it.
func (bs *BlobSyncer) pull(hash string) error {
	members := bs.members.Members()
	for _, m := range members {
		if m.HTTPAddr == "" {
			continue
		}

		// HEAD check.
		headURL := fmt.Sprintf("http://%s/internal/blob/%s", m.HTTPAddr, hash)
		resp, err := bs.client.Head(headURL)
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		resp.Body.Close()

		// GET the blob.
		getURL := fmt.Sprintf("http://%s/internal/blob/%s", m.HTTPAddr, hash)
		resp, err = bs.client.Get(getURL)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}

		storedHash, _, err := bs.blobs.PutReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("storing pulled blob: %w", err)
		}

		if storedHash != hash {
			bs.blobs.Delete(storedHash)
			return fmt.Errorf("hash mismatch: expected %s, got %s", hash, storedHash)
		}

		bs.logger.Debug("blobsync: pulled blob", "hash", hash[:12], "from", m.HTTPAddr)
		return nil
	}

	return fmt.Errorf("blob %s not found on any peer", hash[:12])
}

// verifyHash computes the SHA-256 hash of a reader's contents.
func verifyHash(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
