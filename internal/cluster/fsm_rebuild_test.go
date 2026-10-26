package cluster

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/mimesafe"
	"github.com/bongo-project/bongo/internal/store"
)

type mockSearchIndexer struct {
	profiles        []string
	mails           []string
	deletedMails    []string
	deletedProfiles []string
	// bodies records the body text handed to IndexMail per user/guid. Without
	// it the mock cannot tell an indexed body from a blank one, which is the
	// whole question for body search.
	bodies map[string]string
}

func (m *mockSearchIndexer) IndexMail(userID, guid string, subject, from string, to []string, body string, receivedAt int64, hasAttachment bool, tags []string, spamScore ...float64) error {
	m.mails = append(m.mails, userID+"/"+guid)
	if m.bodies == nil {
		m.bodies = map[string]string{}
	}
	m.bodies[userID+"/"+guid] = body
	return nil
}

func (m *mockSearchIndexer) IndexProfile(userID, email, displayName string) error {
	m.profiles = append(m.profiles, userID+"/"+email)
	return nil
}

func (m *mockSearchIndexer) DeleteProfile(userID string) error {
	m.deletedProfiles = append(m.deletedProfiles, userID)
	return nil
}

func (m *mockSearchIndexer) DeleteMailIndex(userID, guid string) error {
	m.deletedMails = append(m.deletedMails, userID+"/"+guid)
	return nil
}

func TestRebuildDerivedIndexes(t *testing.T) {
	env := newFSMTestEnv(t)

	// Insert a profile.
	profile := &bongov1.UserProfile{
		Email:       "user1@example.com",
		DisplayName: "User One",
	}
	profileBytes, _ := proto.Marshal(profile)
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/u1/profile"),
		Value: profileBytes,
	}))

	// Insert a mail.
	mail := &bongov1.MailMeta{
		Guid:       "m1",
		Subject:    "Hello",
		From:       "sender@example.com",
		To:         []string{"user1@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: "abc", Size: 100},
	}
	mailBytes, _ := proto.Marshal(mail)
	env.fsm.Apply(makeProtoLog(2, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/u1/mail/m1"),
		Value: mailBytes,
	}))

	// Now snapshot and restore into a new FSM.
	snap, err := env.fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	defer snap.Release()

	var buf bytes.Buffer
	sink := &bufferSink{buf: &buf}
	if err := snap.Persist(sink); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	dir2 := t.TempDir()
	ps2, err := store.NewPebbleStore(dir2 + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer ps2.Close()

	bs2, err := store.NewBlobStore(dir2)
	if err != nil {
		t.Fatalf("NewBlobStore: %v", err)
	}

	mm2, err := store.NewMetadataManager(dir2)
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	defer mm2.Close()

	search := &mockSearchIndexer{}

	fsm2 := NewFSM(ps2, bs2, mm2, testLogger())
	fsm2.SetSearchManager(search)

	rc := io.NopCloser(&buf)
	if err := fsm2.Restore(rc); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Verify profiles were indexed.
	if len(search.profiles) != 1 {
		t.Errorf("expected 1 profile indexed, got %d", len(search.profiles))
	}

	// Verify mails were indexed.
	if len(search.mails) != 1 {
		t.Errorf("expected 1 mail indexed, got %d", len(search.mails))
	}

	// Verify SQLite has the mail.
	objs, err := mm2.QueryByType("u1", store.DocTypeMail)
	if err != nil {
		t.Fatalf("QueryByType: %v", err)
	}
	if len(objs) != 1 {
		t.Errorf("expected 1 mail in SQLite, got %d", len(objs))
	}
}

// rawTestMessage is a small RFC 5322 message whose body contains a word that
// appears nowhere in its headers, so a test can tell body indexing from
// subject indexing.
const rawTestMessage = "From: sender@example.com\r\n" +
	"To: user1@example.com\r\n" +
	"Subject: Hello\r\n" +
	"\r\n" +
	"the quick brown pumpernickel jumped over the lazy dog\r\n"

// TestDeliveredMailIsIndexedWithItsBody covers body full-text search for
// RECEIVED mail.
//
// Body text is not carried in MailMeta, so indexing it needs a blob read and a
// MIME parse. That was never done on the delivery path: runDerivedIndexes
// passed "" for the body, and since it is the only path that indexes delivered
// mail, searching for a word in the body of an email you received found
// nothing. Sent mail worked, because compose indexes the body it already has.
func TestDeliveredMailIsIndexedWithItsBody(t *testing.T) {
	env := newFSMTestEnv(t)
	search := &mockSearchIndexer{}
	env.fsm.SetSearchManager(search)

	hash, err := env.fsm.blobs.Put([]byte(rawTestMessage))
	if err != nil {
		t.Fatalf("Put raw message: %v", err)
	}

	mail := &bongov1.MailMeta{
		Guid:       "m1",
		Subject:    "Hello",
		From:       "sender@example.com",
		To:         []string{"user1@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(rawTestMessage))},
	}
	mailBytes, _ := proto.Marshal(mail)
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/u1/mail/m1"),
		Value: mailBytes,
	}))

	got := search.bodies["u1/m1"]
	if !strings.Contains(got, "pumpernickel") {
		t.Errorf("indexed body = %q, want it to contain the body text; "+
			"a word that appears only in the body is unsearchable for received mail", got)
	}
}

// TestRebuiltIndexAlsoHasBodies pins that a rebuilt index answers the same
// queries as an incrementally built one. Without it a snapshot restore would
// quietly produce an index missing every body, and nothing would report an
// error -- the rebuild would "succeed".
func TestRebuiltIndexAlsoHasBodies(t *testing.T) {
	env := newFSMTestEnv(t)

	hash, err := env.fsm.blobs.Put([]byte(rawTestMessage))
	if err != nil {
		t.Fatalf("Put raw message: %v", err)
	}
	mail := &bongov1.MailMeta{
		Guid:       "m1",
		Subject:    "Hello",
		From:       "sender@example.com",
		To:         []string{"user1@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: hash, Size: int64(len(rawTestMessage))},
	}
	mailBytes, _ := proto.Marshal(mail)
	env.fsm.Apply(makeProtoLog(1, &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte("/user/u1/mail/m1"),
		Value: mailBytes,
	}))

	// Rebuild against a fresh indexer, as a restore does.
	rebuilt := &mockSearchIndexer{}
	if err := rebuildDerivedIndexes(env.fsm.store, env.fsm.blobs, env.fsm.metadata, rebuilt, testLogger()); err != nil {
		t.Fatalf("rebuildDerivedIndexes: %v", err)
	}

	got := rebuilt.bodies["u1/m1"]
	if !strings.Contains(got, "pumpernickel") {
		t.Errorf("rebuilt index body = %q, want the body text; a restored node "+
			"would be unable to answer body searches that the original could", got)
	}
}

// TestMailBodyTextDegradesToEmpty pins the failure behaviour. A message whose
// blob is missing or unparseable must still be indexed by subject and
// correspondents rather than failing the index update outright.
func TestMailBodyTextDegradesToEmpty(t *testing.T) {
	env := newFSMTestEnv(t)

	tests := []struct {
		name string
		meta *bongov1.MailMeta
	}{
		{"no raw message ref", &bongov1.MailMeta{Guid: "m1"}},
		{"blob missing from store", &bongov1.MailMeta{
			Guid:       "m2",
			RawMessage: &bongov1.BlobRef{Hash: strings.Repeat("a", 64)},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _, _, _ := mailContent(env.fsm.blobs, tt.meta, testLogger()); got != "" {
				t.Errorf("body = %q, want empty", got)
			}
		})
	}
}

// A blob that reads but cannot be parsed records an empty preview (ok=true):
// retrying a parse failure would never succeed, so the backfill must not
// revisit it forever.
func TestMailContentParseFailureRecordsEmptyPreview(t *testing.T) {
	env := newFSMTestEnv(t)
	hash, err := env.blobs.Put([]byte("\x00\x01 not a mail message at all"))
	if err != nil {
		t.Fatal(err)
	}
	meta := &bongov1.MailMeta{Guid: "m1", RawMessage: &bongov1.BlobRef{Hash: hash, Size: 30}}
	if _, _, err := mimesafe.ReadEnvelope([]byte("\x00\x01 not a mail message at all")); err == nil {
		t.Fatal("fixture must fail to parse for this test to mean anything")
	}
	body, preview, has, ok := mailContent(env.fsm.blobs, meta, testLogger())
	if !ok || body != "" || preview != "" || has {
		t.Fatalf("got body=%q preview=%q has=%v ok=%v, want empty with ok=true", body, preview, has, ok)
	}
}

// mailContent with no blob store is retryable (ok=false); a message with no
// raw-message hash can never become readable, so it records an empty preview
// (ok=true) rather than staying pending forever.
func TestMailContentNilBlobsAndEmptyHash(t *testing.T) {
	env := newFSMTestEnv(t)
	hash, err := env.blobs.Put([]byte(rawTestMessage))
	if err != nil {
		t.Fatal(err)
	}
	withBlob := &bongov1.MailMeta{Guid: "m1", RawMessage: &bongov1.BlobRef{Hash: hash}}
	noHash := &bongov1.MailMeta{Guid: "m2"}
	emptyHash := &bongov1.MailMeta{Guid: "m3", RawMessage: &bongov1.BlobRef{}}

	if _, _, _, ok := mailContent(nil, withBlob, testLogger()); ok {
		t.Error("nil blob store: ok = true, want false so the backfill retries")
	}
	if _, _, _, ok := mailContent(nil, noHash, testLogger()); ok {
		t.Error("nil blob store, no hash: ok = true, want false")
	}
	for _, m := range []*bongov1.MailMeta{noHash, emptyHash} {
		body, preview, has, ok := mailContent(env.fsm.blobs, m, testLogger())
		if !ok || body != "" || preview != "" || has {
			t.Errorf("%s: got body=%q preview=%q has=%v ok=%v, want empty with ok=true", m.Guid, body, preview, has, ok)
		}
	}
}

// A rebuild must leave a message whose blob is absent pending (NULL preview)
// so the backfill retries it, while still filling a readable one.
func TestRebuildLeavesUnreadableBlobPending(t *testing.T) {
	env := newFSMTestEnv(t)
	hashes := seedUnfilledMail(t, env, "u1", 2)
	if err := env.blobs.Delete(hashes["m001"]); err != nil {
		t.Fatal(err)
	}
	if err := rebuildDerivedIndexes(env.fsm.store, env.fsm.blobs, env.fsm.metadata, nil, testLogger()); err != nil {
		t.Fatal(err)
	}
	pending, err := env.metadata.PendingPreviews("u1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != "m001" {
		t.Fatalf("pending = %v, want [m001]", pending)
	}
	rows, _ := env.metadata.MailPreviews("u1", []string{"m000"})
	if rows["m000"].Preview != "body number 0" {
		t.Fatalf("readable message preview = %+v", rows["m000"])
	}
}

// TestRebuildProfilesMatchesProfileKeyExactly feeds a tag and a Bayes token
// that happen to be named "profile". Under a suffix match they were indexed
// as the user's profile, overwriting the directory entry with junk.
func TestRebuildProfilesMatchesProfileKeyExactly(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer ps.Close()

	real, _ := proto.Marshal(&bongov1.UserProfile{Email: "user1@example.com", DisplayName: "User One"})
	decoy, _ := proto.Marshal(&bongov1.UserProfile{Email: "decoy@example.com", DisplayName: "Decoy"})
	for key, val := range map[string][]byte{
		"/user/u1/profile":            real,
		"/user/u1/tag/profile":        decoy,
		"/user/u1/bayes/spam/profile": decoy,
	} {
		if err := ps.Set([]byte(key), val); err != nil {
			t.Fatalf("Set %s: %v", key, err)
		}
	}

	search := &mockSearchIndexer{}
	n, err := rebuildProfiles(ps, nil, search, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("rebuildProfiles: %v", err)
	}
	if n != 1 || len(search.profiles) != 1 || search.profiles[0] != "u1/user1@example.com" {
		t.Fatalf("indexed %d profiles %v, want only u1/user1@example.com", n, search.profiles)
	}
}

func TestReconcileUserEnabled(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer ps.Close()
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	defer mm.Close()

	disabled, _ := proto.Marshal(&bongov1.UserProfile{Email: "off@example.com", Disabled: true})
	enabled, _ := proto.Marshal(&bongov1.UserProfile{Email: "on@example.com"})
	decoy, _ := proto.Marshal(&bongov1.UserProfile{Email: "decoy@example.com", Disabled: true})
	for key, val := range map[string][]byte{
		"/user/off/profile":       disabled,
		"/user/on/profile":        enabled,
		"/user/same/profile":      enabled,
		"/user/decoy/tag/profile": decoy,
	} {
		if err := ps.Set([]byte(key), val); err != nil {
			t.Fatalf("Set %s: %v", key, err)
		}
	}
	for _, r := range []struct {
		id      string
		enabled int
	}{{"off", 1}, {"on", 0}, {"same", 1}, {"decoy", 1}, {"orphan", 1}} {
		if _, err := mm.System.DB().Exec(
			"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, ?, ?, 0)",
			r.id, r.id+"@example.com", r.id, r.enabled); err != nil {
			t.Fatal(err)
		}
	}

	n, err := ReconcileUserEnabled(ps, mm, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("ReconcileUserEnabled: %v", err)
	}
	if n != 2 {
		t.Errorf("changed = %d, want 2", n)
	}
	want := map[string]int{"off": 0, "on": 1, "same": 1, "decoy": 1, "orphan": 1}
	for id, w := range want {
		var got int
		if err := mm.System.DB().QueryRow("SELECT enabled FROM users WHERE id = ?", id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("user %s enabled = %d, want %d", id, got, w)
		}
	}
	var count int
	_ = mm.System.DB().QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count != 5 {
		t.Errorf("rows = %d, want 5 (reconcile must not insert)", count)
	}
}

func TestReconcileOrphanUsers(t *testing.T) {
	ps, err := store.NewPebbleStore(t.TempDir() + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer ps.Close()
	mm, err := store.NewMetadataManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewMetadataManager: %v", err)
	}
	defer mm.Close()

	live, _ := proto.Marshal(&bongov1.UserProfile{Email: "live@example.com"})
	if err := ps.Set([]byte("/user/live/profile"), live); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"live", "orphan"} {
		if _, err := mm.System.DB().Exec(
			"INSERT INTO users (id, username, display_name, enabled, created_at) VALUES (?, ?, ?, 1, 0)",
			id, id+"@example.com", id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mm.System.DB().Exec(
		"INSERT INTO aliases (address, target_user_id) VALUES ('o-alias', 'orphan')"); err != nil {
		t.Fatal(err)
	}

	search := &mockSearchIndexer{}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	n, err := ReconcileOrphanUsers(ps, mm, search, logger)
	if err != nil {
		t.Fatalf("ReconcileOrphanUsers: %v", err)
	}
	if n != 1 {
		t.Fatalf("removed = %d, want 1", n)
	}
	if len(search.deletedProfiles) != 1 || search.deletedProfiles[0] != "orphan" {
		t.Errorf("GAL deletions = %v, want [orphan]", search.deletedProfiles)
	}
	count := func(q string) int {
		var c int
		if err := mm.System.DB().QueryRow(q).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := count("SELECT COUNT(*) FROM users WHERE id='orphan'"); c != 0 {
		t.Errorf("orphan row survived")
	}
	if c := count("SELECT COUNT(*) FROM aliases WHERE target_user_id='orphan'"); c != 0 {
		t.Errorf("orphan alias row survived")
	}
	if c := count("SELECT COUNT(*) FROM users WHERE id='live'"); c != 1 {
		t.Errorf("live row removed")
	}

	n, err = ReconcileOrphanUsers(ps, mm, search, logger)
	if err != nil || n != 0 {
		t.Fatalf("second run = %d, %v; want 0, nil", n, err)
	}
	if len(search.deletedProfiles) != 1 {
		t.Errorf("second run touched GAL: %v", search.deletedProfiles)
	}
	if logBuf.Len() != 0 {
		t.Errorf("reconcile logged: %s", logBuf.String())
	}
}
