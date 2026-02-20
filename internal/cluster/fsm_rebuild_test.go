package cluster

import (
	"bytes"
	"io"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"

	"github.com/bongo-go/bongo-go/internal/store"
)

type mockSearchIndexer struct {
	profiles []string
	mails    []string
}

func (m *mockSearchIndexer) IndexMail(userID, guid string, subject, from string, to []string, body string, receivedAt int64, hasAttachment bool) error {
	m.mails = append(m.mails, userID+"/"+guid)
	return nil
}

func (m *mockSearchIndexer) IndexProfile(userID, email, displayName string) error {
	m.profiles = append(m.profiles, userID+"/"+email)
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
