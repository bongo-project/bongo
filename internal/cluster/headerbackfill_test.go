package cluster

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func storeHeaderValues(sender string) store.MailHeaderValues {
	return store.MailHeaderValues{ToMe: true, SenderAddr: sender}
}

// seedHeaderMail stores n messages for userID with a Pebble MailMeta and a
// maildocument row whose header columns are still NULL. Message i is sent from
// sender<i>@example.com to the user's address; odd messages carry a List-Id.
func seedHeaderMail(t *testing.T, env *fsmTestEnv, userID string, n int) {
	t.Helper()
	if _, err := env.metadata.System.DB().Exec(
		`INSERT OR IGNORE INTO users (id, username) VALUES (?, ?)`, userID, userID+"@example.com"); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		guid := fmt.Sprintf("m%03d", i)
		meta := &bongov1.MailMeta{
			Guid:       guid,
			From:       fmt.Sprintf("Sender %d <Sender%d@Example.com>", i, i),
			To:         []string{"Me <" + userID + "@example.com>"},
			ReceivedAt: int64(1000 + i),
		}
		if i%2 == 1 {
			meta.ListId = "Go Users <go-users.example.org>"
		}
		val, _ := proto.Marshal(meta)
		if err := env.pebble.Set([]byte("/user/"+userID+"/mail/"+guid), val); err != nil {
			t.Fatal(err)
		}
		if err := env.metadata.IndexMail(userID, guid, "s", meta.From, meta.ReceivedAt, 1); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestHeaderBackfill(env *fsmTestEnv, batch int) *HeaderBackfill {
	return &HeaderBackfill{
		ps: env.pebble, blobs: env.blobs, metadata: env.metadata, logger: testLogger(),
		batch: batch, pause: time.Millisecond, interval: time.Hour,
	}
}

func pendingHeaders(t *testing.T, env *fsmTestEnv, userID string) []string {
	t.Helper()
	g, err := env.metadata.PendingHeaders(userID, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestHeaderBackfillFillsColumns(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 6)
	if got := newTestHeaderBackfill(env, 4).RunPass(context.Background()); got != 6 {
		t.Fatalf("filled = %d, want 6", got)
	}
	if p := pendingHeaders(t, env, "u1"); len(p) != 0 {
		t.Fatalf("pending = %v", p)
	}
	toMe, err := env.metadata.QueryToMe("u1")
	if err != nil || len(toMe) != 6 {
		t.Fatalf("to-me = %v, %v", toMe, err)
	}
	// The sender is normalised: display name dropped, lower-cased.
	got, err := env.metadata.QueryBySenders("u1", map[string]bool{"sender3@example.com": true})
	if err != nil || len(got) != 1 || got[0] != "m003" {
		t.Fatalf("sender query = %v, %v", got, err)
	}
	groups, err := env.metadata.ListGroups("u1")
	if err != nil || len(groups) != 1 || groups[0].ID != "go-users.example.org" ||
		groups[0].Name != "Go Users" || len(groups[0].GUIDs) != 3 {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
}

func TestHeaderBackfillToMeIsPerUser(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 2)
	// u2's address is not in u1's mail: nothing is to_me for u2's identity.
	seedHeaderMail(t, env, "u2", 2)
	if _, err := env.metadata.System.DB().Exec(`UPDATE users SET username = 'someone.else@example.com' WHERE id = 'u2'`); err != nil {
		t.Fatal(err)
	}
	newTestHeaderBackfill(env, 10).RunPass(context.Background())
	if g, _ := env.metadata.QueryToMe("u1"); len(g) != 2 {
		t.Fatalf("u1 to-me = %v", g)
	}
	if g, _ := env.metadata.QueryToMe("u2"); len(g) != 0 {
		t.Fatalf("u2 to-me = %v, want none: the mail names u2@example.com, not u2's address", g)
	}
}

func TestHeaderBackfillStopsOnCancelAndResumes(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 7)

	ctx, cancel := context.WithCancel(context.Background())
	b := newTestHeaderBackfill(env, 3)
	b.pause = time.Hour // a cancel must interrupt the pause
	b.afterBatch = cancel

	done := make(chan int, 1)
	go func() { done <- b.RunPass(ctx) }()
	select {
	case got := <-done:
		if got != 3 {
			t.Fatalf("filled before cancel = %d, want 3 (one batch)", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunPass did not return after cancel")
	}
	if n := len(pendingHeaders(t, env, "u1")); n != 4 {
		t.Fatalf("pending after cancel = %d, want 4", n)
	}
	if got := newTestHeaderBackfill(env, 3).RunPass(context.Background()); got != 4 {
		t.Fatalf("resumed fill = %d, want 4", got)
	}
}

func TestHeaderBackfillPausesBetweenBatches(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 6)
	b := newTestHeaderBackfill(env, 2)
	b.pause = 40 * time.Millisecond
	var (
		mu    sync.Mutex
		times []time.Time
	)
	b.afterBatch = func() {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
	}
	b.RunPass(context.Background())
	if len(times) != 3 {
		t.Fatalf("batches = %d, want 3", len(times))
	}
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < 35*time.Millisecond {
			t.Errorf("gap between batches %d and %d = %v, want at least the pause", i-1, i, gap)
		}
	}
}

func TestHeaderBackfillCancelBeforeStartDoesNothing(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := newTestHeaderBackfill(env, 3).RunPass(ctx); got != 0 {
		t.Fatalf("pre-cancelled pass filled %d rows", got)
	}
}

func TestHeaderBackfillRunReturnsOnCancel(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { newTestHeaderBackfill(env, 3).Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(pendingHeaders(t, env, "u1")) != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel: goroutine leak at shutdown")
	}
}

func TestHeaderBackfillDoesNotRereadFilledRows(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 3)
	// m000 already derived (by the live indexer): its sentinel sender must stay,
	// even though its MailMeta would now yield something else.
	if err := env.metadata.SetMailHeaders("u1", "m000", storeHeaderValues("kept@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := newTestHeaderBackfill(env, 10).RunPass(context.Background()); got != 2 {
		t.Fatalf("filled = %d, want 2", got)
	}
	g, _ := env.metadata.QueryBySenders("u1", map[string]bool{"kept@example.com": true})
	if len(g) != 1 || g[0] != "m000" {
		t.Fatalf("filled row changed: %v", g)
	}
}

func TestHeaderBackfillMissingMetaDoesNotLoop(t *testing.T) {
	env := newFSMTestEnv(t)
	seedHeaderMail(t, env, "u1", 3)
	if err := env.pebble.Delete([]byte("/user/u1/mail/m001")); err != nil {
		t.Fatal(err)
	}
	b := newTestHeaderBackfill(env, 10)
	if got := b.RunPass(context.Background()); got != 3 {
		t.Fatalf("filled = %d, want 3 (the deleted message is closed too)", got)
	}
	if p := pendingHeaders(t, env, "u1"); len(p) != 0 {
		t.Fatalf("pending = %v: a missing MailMeta must still stop the row being pending", p)
	}
	if g, _ := env.metadata.QueryToMe("u1"); len(g) != 2 {
		t.Fatalf("to-me = %v, want the two surviving messages", g)
	}
}

func TestHeaderBackfillListIDFromBlob(t *testing.T) {
	env := newFSMTestEnv(t)
	if _, err := env.metadata.System.DB().Exec(
		`INSERT INTO users (id, username) VALUES ('u1', 'u1@example.com')`); err != nil {
		t.Fatal(err)
	}
	// A message from before MailMeta recorded List-Id: the header is only in the blob.
	raw := "From: a@example.com\r\nList-Id: Old Stuff <Old.Lists.Example.org>\r\nSubject: s\r\n\r\nbody\r\n"
	hash, err := env.blobs.Put([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	// And one whose blob is gone.
	lostHash, err := env.blobs.Put([]byte("From: b@example.com\r\nList-Id: <lost.example.org>\r\n\r\nx\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	for guid, h := range map[string]string{"old": hash, "lost": lostHash} {
		meta := &bongov1.MailMeta{Guid: guid, From: "a@example.com", To: []string{"u1@example.com"},
			RawMessage: &bongov1.BlobRef{Hash: h, Size: 10}}
		val, _ := proto.Marshal(meta)
		if err := env.pebble.Set([]byte("/user/u1/mail/"+guid), val); err != nil {
			t.Fatal(err)
		}
		if err := env.metadata.IndexMail("u1", guid, "s", "a@example.com", 1, 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.blobs.Delete(lostHash); err != nil {
		t.Fatal(err)
	}

	if got := newTestHeaderBackfill(env, 10).RunPass(context.Background()); got != 2 {
		t.Fatalf("filled = %d, want 2", got)
	}
	groups, _ := env.metadata.ListGroups("u1")
	if len(groups) != 1 || groups[0].ID != "old.lists.example.org" || groups[0].Name != "Old Stuff" {
		t.Fatalf("groups = %+v: the blob's List-Id must be used", groups)
	}
	if p := pendingHeaders(t, env, "u1"); len(p) != 0 {
		t.Fatalf("pending = %v: an unreadable blob must not leave the row pending", p)
	}
	if g, _ := env.metadata.QueryToMe("u1"); len(g) != 2 {
		t.Fatalf("to-me = %v: the columns MailMeta answers are still written", g)
	}
}

func TestIndexStoreObjectFillsHeaderColumns(t *testing.T) {
	env := newFSMTestEnv(t)
	if _, err := env.metadata.System.DB().Exec(
		`INSERT INTO users (id, username) VALUES ('u1', 'me@example.com')`); err != nil {
		t.Fatal(err)
	}
	// An alias known only to the addresses index counts as the user's.
	if _, err := env.metadata.System.DB().Exec(
		`INSERT INTO addresses (address, user_id, kind) VALUES ('alias@example.com', 'u1', 'alias')`); err != nil {
		t.Fatal(err)
	}
	for i, to := range [][]string{
		{"Friend <friend@example.com>", "ME@example.com"},
		{"Alias <alias@example.com>"},
		{"catchall-target@example.com"},
	} {
		meta := &bongov1.MailMeta{
			Guid: fmt.Sprintf("m%d", i), From: "Bob <Bob@Example.com>", To: to, ReceivedAt: 1700000000,
			ListId: "<dev.example.org>",
		}
		val, _ := proto.Marshal(meta)
		env.fsm.Apply(makeProtoLog(uint64(i+1), &bongov1.RaftCommand{
			Op: bongov1.Op_OP_PUT, Key: []byte(fmt.Sprintf("/user/u1/mail/m%d", i)), Value: val,
		}))
	}
	env.fsm.WaitIdle(5 * time.Second)

	toMe, _ := env.metadata.QueryToMe("u1")
	if len(toMe) != 2 {
		t.Fatalf("to-me = %v, want m0 (account email, any case) and m1 (alias); a delivery to an unowned address is not to me", toMe)
	}
	if g, _ := env.metadata.QueryByListID("u1", "dev.example.org"); len(g) != 3 {
		t.Fatalf("list = %v", g)
	}
	if p := pendingHeaders(t, env, "u1"); len(p) != 0 {
		t.Fatalf("pending = %v", p)
	}
	// A later flag or tag PUT re-runs IndexMail; it must not reset the columns.
	if err := env.metadata.IndexMail("u1", "m0", "s", "x", 1, 1); err != nil {
		t.Fatal(err)
	}
	if g, _ := env.metadata.QueryToMe("u1"); len(g) != 2 {
		t.Fatalf("to-me after re-index = %v", g)
	}
}

func TestRebuildFillsHeaderColumns(t *testing.T) {
	env := newFSMTestEnv(t)
	profile := &bongov1.UserProfile{Email: "me@example.com"}
	pv, _ := proto.Marshal(profile)
	if err := env.pebble.Set([]byte("/user/u1/profile"), pv); err != nil {
		t.Fatal(err)
	}
	// The alias exists in Pebble only; the derived addresses table is empty,
	// as it is after a snapshot restore.
	rec, _ := proto.Marshal(&bongov1.AddressRecord{UserId: "u1", Kind: "alias"})
	if err := env.pebble.Set([]byte("/system/address/alias@example.com"), rec); err != nil {
		t.Fatal(err)
	}
	for guid, to := range map[string]string{"a": "me@example.com", "b": "alias@example.com", "c": "other@example.com"} {
		meta := &bongov1.MailMeta{Guid: guid, From: "x@example.com", To: []string{to}, ReceivedAt: 5, ListId: "<l.example.org>"}
		val, _ := proto.Marshal(meta)
		if err := env.pebble.Set([]byte("/user/u1/mail/"+guid), val); err != nil {
			t.Fatal(err)
		}
	}
	if err := rebuildDerivedIndexes(env.fsm.store, env.fsm.blobs, env.fsm.metadata, nil, testLogger()); err != nil {
		t.Fatal(err)
	}
	toMe, _ := env.metadata.QueryToMe("u1")
	if len(toMe) != 2 {
		t.Fatalf("to-me = %v, want a and b", toMe)
	}
	if g, _ := env.metadata.QueryByListID("u1", "l.example.org"); len(g) != 3 {
		t.Fatalf("list = %v", g)
	}
	if p := pendingHeaders(t, env, "u1"); len(p) != 0 {
		t.Fatalf("pending after rebuild = %v", p)
	}
}
