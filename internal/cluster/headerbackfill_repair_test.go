package cluster

import (
	"context"
	"database/sql"
	"path/filepath"
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

// A v0.15 row (headers_rev = 1, Cc only in SQLite) must get its participants,
// Cc included, from the blob, and then be left alone.
func TestHeaderBackfillFillsParticipantsForRevisionOneRows(t *testing.T) {
	env := newFSMTestEnv(t)
	if _, err := env.metadata.System.DB().Exec(
		`INSERT INTO users (id, username) VALUES ('u1', 'u1@example.com')`); err != nil {
		t.Fatal(err)
	}
	raw := "From: a@example.com\r\nTo: other@example.org\r\nCc: Carl <carl@example.net>\r\nSubject: s\r\n\r\nbody\r\n"
	hash, err := env.blobs.Put([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	meta := &bongov1.MailMeta{Guid: "r1", From: "a@example.com", To: []string{"other@example.org"},
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(raw))}}
	val, _ := proto.Marshal(meta)
	if err := env.pebble.Set([]byte("/user/u1/mail/r1"), val); err != nil {
		t.Fatal(err)
	}
	if err := env.metadata.IndexMail("u1", "r1", "s", "a@example.com", 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := env.metadata.SetMailHeaders("u1", "r1", store.MailHeaderValues{SenderAddr: "a@example.com", Complete: true}); err != nil {
		t.Fatal(err)
	}
	// Written by v0.15: revision 1.
	db, err := sql.Open("sqlite", filepath.Join(env.dir, "metadata", "users", "u1.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE maildocument SET headers_rev = 1`); err != nil {
		t.Fatal(err)
	}
	if g, _ := env.metadata.QueryByParticipants("u1", []string{"carl@example.net"}); len(g) != 0 {
		t.Fatalf("participants before the pass: %v", g)
	}
	if p := pendingHeaders(t, env, "u1"); len(p) != 1 {
		t.Fatalf("pending = %v: a revision 1 row must be pending", p)
	}
	b := newTestHeaderBackfill(env, 10)
	if got := b.RunPass(context.Background()); got != 1 {
		t.Fatalf("first pass filled %d, want 1", got)
	}
	for _, a := range []string{"carl@example.net", "other@example.org", "a@example.com"} {
		if g, _ := env.metadata.QueryByParticipants("u1", []string{a}); len(g) != 1 {
			t.Errorf("%s = %v after the pass", a, g)
		}
	}
	if got := b.RunPass(context.Background()); got != 0 {
		t.Errorf("second pass filled %d, want 0", got)
	}
}

func TestHeaderBackfillMissingMetaClearsParticipants(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 1)
	if err := env.metadata.SetMailHeaders("u1", "m000", store.MailHeaderValues{
		Participants: []store.Participant{{Addr: "gone@example.com", Roles: store.RoleTo}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.pebble.Delete([]byte("/user/u1/mail/m000")); err != nil {
		t.Fatal(err)
	}
	newTestHeaderBackfill(env, 10).RunPass(context.Background())
	if g, _ := env.metadata.QueryByParticipants("u1", []string{"gone@example.com"}); len(g) != 0 {
		t.Errorf("participants of a message with no MailMeta = %v, want none", g)
	}
}
