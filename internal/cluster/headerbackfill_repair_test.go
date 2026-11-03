package cluster

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// A row written by APPEND or import before MailMeta recorded Cc and List-Id has
// to_me already set (the live indexer saw no Cc), so only the blob can repair
// it. The backfill must find it, repair it, and then leave it alone.
func TestHeaderBackfillRepairsRowsWrittenBeforeCcAndListID(t *testing.T) {
	env := newFSMTestEnv(t)
	if _, err := env.metadata.System.DB().Exec(
		`INSERT INTO users (id, username) VALUES ('u1', 'u1@example.com')`); err != nil {
		t.Fatal(err)
	}
	raw := "From: a@example.com\r\nTo: other@example.org\r\nCc: Me <u1@example.com>\r\n" +
		"List-Id: Old Stuff <Repair.Lists.Example.org>\r\nSubject: s\r\n\r\nbody\r\n"
	hash, err := env.blobs.Put([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	meta := &bongov1.MailMeta{Guid: "old", From: "a@example.com", To: []string{"other@example.org"},
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(raw))}}
	val, _ := proto.Marshal(meta)
	if err := env.pebble.Set([]byte("/user/u1/mail/old"), val); err != nil {
		t.Fatal(err)
	}
	if err := env.metadata.IndexMail("u1", "old", "s", "a@example.com", 1, 10); err != nil {
		t.Fatal(err)
	}
	// The pre-fix indexer's write: to_me = 0, no list, no revision.
	if err := env.metadata.SetMailHeaders("u1", "old", store.MailHeaderValues{SenderAddr: "a@example.com"}); err != nil {
		t.Fatal(err)
	}
	if p := pendingHeaders(t, env, "u1"); len(p) != 1 {
		t.Fatalf("pending = %v: a row of the earlier revision must be pending", p)
	}

	b := newTestHeaderBackfill(env, 10)
	if got := b.RunPass(context.Background()); got != 1 {
		t.Fatalf("first pass filled %d, want 1", got)
	}
	if g, _ := env.metadata.QueryToMe("u1"); len(g) != 1 {
		t.Errorf("to-me = %v: Cc from the blob was not used", g)
	}
	if g, _ := env.metadata.QueryByListID("u1", "repair.lists.example.org"); len(g) != 1 {
		t.Errorf("list = %v: List-Id from the blob was not used", g)
	}
	if got := b.RunPass(context.Background()); got != 0 {
		t.Errorf("second pass filled %d, want 0", got)
	}
}
