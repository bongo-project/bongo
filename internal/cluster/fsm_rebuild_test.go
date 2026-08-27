package cluster

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

type mockSearchIndexer struct {
	profiles     []string
	mails        []string
	deletedMails []string
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
			if got := mailBodyText(env.fsm.blobs, tt.meta, testLogger()); got != "" {
				t.Errorf("body = %q, want empty", got)
			}
		})
	}
}
