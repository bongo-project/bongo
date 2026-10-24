package cluster

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
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
	// peerTLS is true once SetPeerTLS has run: pulls then use https against the
	// peer listener and never fall back to the plaintext HTTPAddr.
	peerTLS bool
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

// SetPeerTLS switches the syncer to the peer listener: pulls go to
// https://<PeerAddr> with the cluster CA as the only trust root and the node
// certificate for client auth, and members that advertise no PeerAddr are
// skipped rather than contacted over plaintext. cfg is cloned.
func (bs *BlobSyncer) SetPeerTLS(cfg *tls.Config) {
	bs.client = &http.Client{
		Timeout:   bs.client.Timeout,
		Transport: &http.Transport{TLSClientConfig: cfg.Clone()},
	}
	bs.peerTLS = true
}

// PeerTLSEnabled reports whether pulls go to the mutually authenticated peer
// listener over https.
func (bs *BlobSyncer) PeerTLSEnabled() bool { return bs.peerTLS }

// endpoint returns the scheme and address to reach m for blob transfers. With
// peer TLS it is always https against the peer listener, and a member that
// advertises none (an older node) is skipped, never reached over plaintext.
func (bs *BlobSyncer) endpoint(m MemberInfo) (scheme, addr string, ok bool) {
	if bs.peerTLS {
		if m.PeerAddr == "" {
			bs.logger.Warn("blobsync: skipping member that advertises no peer address", "peer", m.Name)
			return "", "", false
		}
		return "https", m.PeerAddr, true
	}
	// The public web port no longer serves /internal/blob, so this plaintext
	// fallback reaches a 404 and should be removed.
	if m.HTTPAddr == "" {
		return "", "", false
	}
	return "http", m.HTTPAddr, true
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
		scheme, addr, ok := bs.endpoint(m)
		if !ok {
			continue
		}

		// HEAD check.
		headURL := fmt.Sprintf("%s://%s/internal/blob/%s", scheme, addr, hash)
		headReq, err := http.NewRequest(http.MethodHead, headURL, nil)
		if err != nil {
			continue
		}
		security.SignBlobSyncRequest(headReq, bs.secret)
		resp, err := bs.client.Do(headReq)
		if err != nil {
			bs.logger.Warn("blobsync: peer request failed", "peer", m.Name, "addr", addr, "hash", shortHash(hash), "error", err)
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
		getURL := fmt.Sprintf("%s://%s/internal/blob/%s", scheme, addr, hash)
		getReq, err := http.NewRequest(http.MethodGet, getURL, nil)
		if err != nil {
			continue
		}
		security.SignBlobSyncRequest(getReq, bs.secret)
		resp, err = bs.client.Do(getReq)
		if err != nil {
			bs.logger.Warn("blobsync: peer request failed", "peer", m.Name, "addr", addr, "hash", shortHash(hash), "error", err)
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

		bs.logger.Debug("blobsync: pulled blob", "hash", shortHash(hash), "peer", m.Name, "from", addr)
		return nil
	}

	return fmt.Errorf("blob %s not found on any peer", shortHash(hash))
}
