package store

import (
	"bytes"
	"fmt"
	"io"

	"github.com/bongo-go/bongo-go/internal/attsplit"
	"github.com/bongo-go/bongo-go/internal/observability"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// SplitStored is the result of storing a message in split form: the skeleton blob
// hash plus the attachment BlobRefs and layout to reconstruct the original.
type SplitStored struct {
	SkeletonHash string
	Attachments  []*bongov1.BlobRef
	Layout       []*bongov1.MessageSegment
}

// StoreSplit stores raw as a skeleton + deduplicated attachment blobs when it
// carries an attachment of at least minSize, then verifies byte-exact
// reconstruction. It returns:
//   - (result, nil) when the message was split and self-checked;
//   - (nil, nil)    when it should be stored whole (dedup disabled via minSize<=0
//     or no qualifying attachment);
//   - (nil, err)    when a split was attempted but a blob write or the self-check
//     failed — the caller must fall back to whole-raw storage and log err.
//
// The self-check makes splitting safe to run by default: a message that cannot be
// rebuilt byte-for-byte is never stored in split form.
func StoreSplit(bs *BlobStore, raw []byte, minSize int) (*SplitStored, error) {
	if bs == nil {
		return nil, nil
	}
	sm := PrepareSplit(raw, minSize)
	if sm == nil {
		return nil, nil
	}
	skeletonHash, err := bs.Put(sm.Skeleton)
	if err != nil {
		return nil, fmt.Errorf("store skeleton: %w", err)
	}
	for i, enc := range sm.Encoded {
		// Record dedup before writing: a blob that already exists is a saving
		// (the encoded bytes are not re-stored), a new one is a write.
		if bs.Exists(sm.Attachments[i].Hash) {
			observability.AttachmentDedupBlobsTotal.WithLabelValues("deduped").Inc()
			observability.AttachmentDedupBytesSavedTotal.Add(float64(len(enc)))
		} else {
			observability.AttachmentDedupBlobsTotal.WithLabelValues("written").Inc()
		}
		if _, err := bs.Put(enc); err != nil {
			return nil, fmt.Errorf("store attachment blob: %w", err)
		}
	}
	check := &bongov1.MailMeta{
		RawMessage:  &bongov1.BlobRef{Hash: skeletonHash},
		Attachments: sm.Attachments,
		Layout:      sm.Layout,
	}
	got, err := RawMessageBytes(bs, check)
	if err != nil {
		return nil, fmt.Errorf("split self-check read: %w", err)
	}
	if !bytes.Equal(got, raw) {
		return nil, fmt.Errorf("split self-check: reconstruction is not byte-exact")
	}
	return &SplitStored{SkeletonHash: skeletonHash, Attachments: sm.Attachments, Layout: sm.Layout}, nil
}

// SplitMessage is a message prepared for de-MIME'd ("split") storage: a skeleton
// blob (the message with large attachment payloads removed) plus the attachment
// blobs and the layout that reconstructs the exact original. The caller stores
// Skeleton and each Encoded[i] as blobs, sets MailMeta.RawMessage to the skeleton
// blob, MailMeta.Attachments to Attachments, and MailMeta.Layout to Layout.
type SplitMessage struct {
	Skeleton    []byte
	Layout      []*bongov1.MessageSegment
	Attachments []*bongov1.BlobRef // index-aligned with Encoded
	Encoded     [][]byte           // encoded attachment bytes to store as blobs
}

// PrepareSplit splits raw into a skeleton + attachment blobs when at least one
// attachment part's encoded body is >= minSize. It returns nil when the message
// would not be split (no qualifying attachment), so the caller keeps storing the
// whole raw as before.
func PrepareSplit(raw []byte, minSize int) *SplitMessage {
	if minSize <= 0 { // splitting disabled
		return nil
	}
	sk := attsplit.Split(raw, minSize)
	if len(sk.Parts) == 0 {
		return nil
	}
	skeleton, segs := sk.Layout()

	sm := &SplitMessage{Skeleton: skeleton}
	for _, seg := range segs {
		if seg.Attachment {
			sm.Layout = append(sm.Layout, &bongov1.MessageSegment{
				Kind: bongov1.MessageSegment_ATTACHMENT,
				// #nosec G115 -- attachment index is bounded by the message's MIME
				// part count (a handful), never anywhere near int32 overflow.
				AttachmentIndex: int32(seg.AttIndex),
			})
		} else {
			sm.Layout = append(sm.Layout, &bongov1.MessageSegment{
				Kind:   bongov1.MessageSegment_LITERAL,
				Length: int64(seg.Length),
			})
		}
	}
	for _, p := range sk.Parts {
		sm.Attachments = append(sm.Attachments, &bongov1.BlobRef{
			Hash:        p.Hash,
			Size:        int64(p.Size),
			ContentType: p.ContentType,
			Filename:    p.Filename,
		})
		sm.Encoded = append(sm.Encoded, p.Encoded)
	}
	return sm
}

// RawMessageBytes returns the complete original RFC822 message for meta, fetching
// blobs from bs. It reconstructs split records and passes legacy whole-raw
// records through unchanged. This is the single entry point every full-message
// reader (IMAP FETCH, web view, download, export, re-parse) should use instead
// of fetching meta.RawMessage directly.
func RawMessageBytes(bs *BlobStore, meta *bongov1.MailMeta) ([]byte, error) {
	if bs == nil {
		return nil, fmt.Errorf("raw message bytes: nil blob store")
	}
	return FullRawMessage(meta, func(hash string) ([]byte, error) {
		rc, err := bs.Get(hash)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	})
}

// FullRawMessage returns the complete original RFC822 bytes for meta. For a split
// record (meta.Layout non-empty) it reconstructs from the skeleton blob
// (meta.RawMessage) interleaved with the attachment blobs per the layout; for a
// legacy/unsplit record it returns the whole raw blob unchanged. get fetches a
// blob's bytes by hash.
func FullRawMessage(meta *bongov1.MailMeta, get func(hash string) ([]byte, error)) ([]byte, error) {
	if meta == nil || meta.RawMessage == nil {
		return nil, fmt.Errorf("full raw message: missing raw_message reference")
	}

	// Legacy / unsplit: raw_message is the whole message.
	if len(meta.Layout) == 0 {
		return get(meta.RawMessage.Hash)
	}

	skeleton, err := get(meta.RawMessage.Hash)
	if err != nil {
		return nil, fmt.Errorf("full raw message: skeleton blob: %w", err)
	}

	// Fetch each referenced attachment once.
	attCache := make(map[int][]byte)
	out := make([]byte, 0, len(skeleton))
	off := 0
	for i, seg := range meta.Layout {
		switch seg.Kind {
		case bongov1.MessageSegment_LITERAL:
			n := int(seg.Length)
			if n < 0 || off+n > len(skeleton) {
				return nil, fmt.Errorf("full raw message: literal segment %d out of skeleton range", i)
			}
			out = append(out, skeleton[off:off+n]...)
			off += n
		case bongov1.MessageSegment_ATTACHMENT:
			idx := int(seg.AttachmentIndex)
			if idx < 0 || idx >= len(meta.Attachments) {
				return nil, fmt.Errorf("full raw message: attachment index %d out of range", idx)
			}
			b, ok := attCache[idx]
			if !ok {
				b, err = get(meta.Attachments[idx].Hash)
				if err != nil {
					return nil, fmt.Errorf("full raw message: attachment %d: %w", idx, err)
				}
				attCache[idx] = b
			}
			out = append(out, b...)
		default:
			return nil, fmt.Errorf("full raw message: unknown segment kind %v at %d", seg.Kind, i)
		}
	}
	if off != len(skeleton) {
		return nil, fmt.Errorf("full raw message: skeleton not fully consumed (%d of %d bytes)", off, len(skeleton))
	}
	return out, nil
}
