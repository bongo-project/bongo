package cluster

import (
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/testutil"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// TestMailBodyTextDeepMIMEIsFast requires the indexer to survive a message that
// costs enmime seconds to parse. It must not stall the background indexer.
func TestMailBodyTextDeepMIMEIsFast(t *testing.T) {
	env := newFSMTestEnv(t)
	raw := testutil.DeepUnclosedMultipart(24)
	hash, err := env.fsm.blobs.Put(raw)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	meta := &bongov1.MailMeta{Guid: "deep", RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(raw))}}

	start := time.Now()
	_, _, _, _ = mailContent(env.fsm.blobs, meta, testLogger())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("mailBodyText took %v on a deeply nested message, want < 1s", elapsed)
	}
}
