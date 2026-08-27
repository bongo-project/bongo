package pipeline

import (
	"context"
	"io"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Processor handles one stage of the message pipeline.
type Processor interface {
	// Name returns the stage identifier (e.g. "antispam", "antivirus").
	Name() string
	// Process evaluates a message and returns a verdict.
	Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error)
}

// BlobReader reads message content from the blob store by content-addressable hash.
type BlobReader interface {
	ReadBlob(hash string) (io.ReadCloser, error)
}
