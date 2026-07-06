package cluster

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/bongo-go/bongo-go/internal/security"
	"github.com/bongo-go/bongo-go/internal/store"
)

// maxBlobSyncBytes bounds a single blob pulled from a peer, so a compromised
// member cannot exhaust local disk by streaming unbounded data. Generous enough
// for any legitimate mail blob (the SMTP/IMAP message caps are well below this).
const maxBlobSyncBytes = 64 << 20 // 64 MiB

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
	secret  []byte
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

// SetSharedSecret sets the auth secret used for internal blob fetches.
func (bs *BlobSyncer) SetSharedSecret(secret []byte) {
	bs.secret = append([]byte(nil), secret...)
}

// shortHash returns the first 12 characters of h, or h itself if shorter.
// Used for safe log truncation — never panics on short input.
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// Enqueue adds a blob hash to the sync queue if it doesn't exist locally.
// Invalid hashes (not 64-char lowercase hex) are silently dropped with a warning.
func (bs *BlobSyncer) Enqueue(hash string) {
	if !store.IsValidHash(hash) {
		bs.logger.Warn("blobsync: ignoring invalid hash", "hash", shortHash(hash))
		return
	}
	if bs.blobs.Exists(hash) {
		return
	}
	select {
	case bs.queue <- hash:
	default:
		bs.logger.Warn("blobsync: queue full, dropping hash", "hash", shortHash(hash))
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
				bs.logger.Warn("blobsync: pull failed", "hash", shortHash(hash), "error", err)
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
		headReq, err := http.NewRequest(http.MethodHead, headURL, nil)
		if err != nil {
			continue
		}
		security.SignInternalRequest(headReq, bs.secret)
		resp, err := bs.client.Do(headReq)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			if closeErr := resp.Body.Close(); closeErr != nil {
				bs.logger.Debug("blobsync: failed to close HEAD response body", "hash", hash, "error", closeErr)
			}
			continue
		}
		if err := resp.Body.Close(); err != nil {
			bs.logger.Debug("blobsync: failed to close HEAD response body", "hash", hash, "error", err)
			continue
		}

		// GET the blob.
		getURL := fmt.Sprintf("http://%s/internal/blob/%s", m.HTTPAddr, hash)
		getReq, err := http.NewRequest(http.MethodGet, getURL, nil)
		if err != nil {
			continue
		}
		security.SignInternalRequest(getReq, bs.secret)
		resp, err = bs.client.Do(getReq)
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			if closeErr := resp.Body.Close(); closeErr != nil {
				bs.logger.Debug("blobsync: failed to close non-OK GET response body", "hash", hash, "error", closeErr)
			}
			continue
		}

		// Bound the amount read from a peer so a malicious/compromised member
		// cannot stream unbounded data to exhaust disk/temp space. An oversized
		// blob is truncated, which fails the hash check below and is rejected.
		storedHash, _, err := bs.blobs.PutReader(io.LimitReader(resp.Body, maxBlobSyncBytes))
		if closeErr := resp.Body.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return fmt.Errorf("storing pulled blob: %w", err)
		}

		if storedHash != hash {
			if err := bs.blobs.Delete(storedHash); err != nil {
				bs.logger.Warn("blobsync: failed to delete mismatched blob", "hash", storedHash, "error", err)
			}
			return fmt.Errorf("hash mismatch: expected %s, got %s", hash, storedHash)
		}

		bs.logger.Debug("blobsync: pulled blob", "hash", shortHash(hash), "from", m.HTTPAddr)
		return nil
	}

	return fmt.Errorf("blob %s not found on any peer", shortHash(hash))
}
