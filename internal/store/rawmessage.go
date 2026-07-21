package store

import (
	"fmt"

	"github.com/bongo-go/bongo-go/internal/attsplit"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

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
	sk := attsplit.Split(raw, minSize)
	if len(sk.Parts) == 0 {
		return nil
	}
	skeleton, segs := sk.Layout()

	sm := &SplitMessage{Skeleton: skeleton}
	for _, seg := range segs {
		if seg.Attachment {
			sm.Layout = append(sm.Layout, &bongov1.MessageSegment{
				Kind:            bongov1.MessageSegment_ATTACHMENT,
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
