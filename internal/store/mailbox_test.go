package store

import (
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// directApplier applies Raft commands directly to Pebble (no Raft, for testing).
type directApplier struct {
	store *PebbleStore
}

func (a *directApplier) ApplyCommand(cmd *bongov1.RaftCommand, _ time.Duration) error {
	if cmd.Op == bongov1.Op_OP_PUT && IsMailboxSyntheticCommand(cmd.Key) {
		_, err := ApplyMailboxCommand(a.store, cmd)
		return err
	}
	switch cmd.Op {
	case bongov1.Op_OP_PUT:
		return a.store.Set(cmd.Key, cmd.Value)
	case bongov1.Op_OP_DELETE:
		return a.store.Delete(cmd.Key)
	}
	return nil
}

func newMailboxTestEnv(t *testing.T) *MailboxStore {
	t.Helper()
	dir := t.TempDir()
	ps, err := NewPebbleStore(dir + "/pebble")
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	return NewMailboxStore(ps, &directApplier{store: ps})
}

func TestMailboxCRUD(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	// Create a mailbox.
	if err := ms.CreateMailbox(userID, "TestBox"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	// Get it back.
	meta, err := ms.GetMailbox(userID, "TestBox")
	if err != nil {
		t.Fatalf("GetMailbox: %v", err)
	}
	if meta.Name != "TestBox" {
		t.Errorf("name = %q, want %q", meta.Name, "TestBox")
	}
	if meta.UidNext != 1 {
		t.Errorf("uid_next = %d, want 1", meta.UidNext)
	}

	// List mailboxes.
	boxes, err := ms.ListMailboxes(userID)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	if len(boxes) != 1 {
		t.Fatalf("expected 1 mailbox, got %d", len(boxes))
	}

	// Create duplicate should fail.
	if err := ms.CreateMailbox(userID, "TestBox"); err == nil {
		t.Error("expected error creating duplicate mailbox")
	}

	// Delete mailbox.
	if err := ms.DeleteMailbox(userID, "TestBox"); err != nil {
		t.Fatalf("DeleteMailbox: %v", err)
	}
	if _, err := ms.GetMailbox(userID, "TestBox"); err == nil {
		t.Error("expected error getting deleted mailbox")
	}
}

func TestMailboxDeleteINBOXFails(t *testing.T) {
	ms := newMailboxTestEnv(t)
	if err := ms.DeleteMailbox("user1", "INBOX"); err == nil {
		t.Error("expected error deleting INBOX")
	}
}

func TestMailboxRename(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	if err := ms.CreateMailbox(userID, "OldName"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	// Add a message to the mailbox.
	msg := &bongov1.MailboxMessage{Uid: 1, MailGuid: "g1", InternalDate: 1000}
	msgBytes, _ := proto.Marshal(msg)
	ms.store.Set(mailboxMsgKey(userID, "OldName", 1), msgBytes)

	// Update meta to reflect the message.
	meta, _ := ms.GetMailbox(userID, "OldName")
	meta.UidNext = 2
	metaBytes, _ := proto.Marshal(meta)
	ms.store.Set(mailboxMetaKey(userID, "OldName"), metaBytes)

	// Rename.
	if err := ms.RenameMailbox(userID, "OldName", "NewName"); err != nil {
		t.Fatalf("RenameMailbox: %v", err)
	}

	// Old name should be gone.
	if _, err := ms.GetMailbox(userID, "OldName"); err == nil {
		t.Error("old mailbox still exists")
	}

	// New name should exist.
	newMeta, err := ms.GetMailbox(userID, "NewName")
	if err != nil {
		t.Fatalf("new mailbox not found: %v", err)
	}
	if newMeta.UidNext != 2 {
		t.Errorf("uid_next = %d, want 2", newMeta.UidNext)
	}

	// Message should be under new name.
	msgs, err := ms.ListMessages(userID, "NewName")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].MailGuid != "g1" {
		t.Errorf("mail_guid = %q, want %q", msgs[0].MailGuid, "g1")
	}
}

func TestEnsureDefaultMailboxes(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	if err := ms.EnsureDefaultMailboxes(userID); err != nil {
		t.Fatalf("EnsureDefaultMailboxes: %v", err)
	}

	boxes, err := ms.ListMailboxes(userID)
	if err != nil {
		t.Fatalf("ListMailboxes: %v", err)
	}
	if len(boxes) != len(DefaultMailboxes) {
		t.Fatalf("expected %d mailboxes, got %d", len(DefaultMailboxes), len(boxes))
	}

	// Calling again should be idempotent.
	if err := ms.EnsureDefaultMailboxes(userID); err != nil {
		t.Fatalf("EnsureDefaultMailboxes (2nd call): %v", err)
	}
}

func TestFlagOperations(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"
	ms.CreateMailbox(userID, "INBOX")

	// Add a message.
	msg := &bongov1.MailboxMessage{Uid: 1, MailGuid: "g1", Flags: 0, InternalDate: 1000}
	msgBytes, _ := proto.Marshal(msg)
	ms.store.Set(mailboxMsgKey(userID, "INBOX", 1), msgBytes)

	// SetFlags.
	if err := ms.SetFlags(userID, "INBOX", 1, FlagSeen|FlagFlagged); err != nil {
		t.Fatalf("SetFlags: %v", err)
	}
	got, _ := ms.GetMessage(userID, "INBOX", 1)
	if got.Flags != FlagSeen|FlagFlagged {
		t.Errorf("flags = %d, want %d", got.Flags, FlagSeen|FlagFlagged)
	}

	// AddFlags.
	if err := ms.AddFlags(userID, "INBOX", 1, FlagAnswered); err != nil {
		t.Fatalf("AddFlags: %v", err)
	}
	got, _ = ms.GetMessage(userID, "INBOX", 1)
	if got.Flags != FlagSeen|FlagFlagged|FlagAnswered {
		t.Errorf("flags after add = %d, want %d", got.Flags, FlagSeen|FlagFlagged|FlagAnswered)
	}

	// RemoveFlags.
	if err := ms.RemoveFlags(userID, "INBOX", 1, FlagFlagged); err != nil {
		t.Fatalf("RemoveFlags: %v", err)
	}
	got, _ = ms.GetMessage(userID, "INBOX", 1)
	if got.Flags != FlagSeen|FlagAnswered {
		t.Errorf("flags after remove = %d, want %d", got.Flags, FlagSeen|FlagAnswered)
	}
}

func TestCopyAndMoveMessage(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"
	ms.CreateMailbox(userID, "INBOX")
	ms.CreateMailbox(userID, "Archive")

	// Add a message to INBOX.
	msg := &bongov1.MailboxMessage{Uid: 1, MailGuid: "g1", Flags: FlagSeen, InternalDate: 1000}
	msgBytes, _ := proto.Marshal(msg)
	ms.store.Set(mailboxMsgKey(userID, "INBOX", 1), msgBytes)

	// Update INBOX meta uid_next.
	inboxMeta, _ := ms.GetMailbox(userID, "INBOX")
	inboxMeta.UidNext = 2
	metaBytes, _ := proto.Marshal(inboxMeta)
	ms.store.Set(mailboxMetaKey(userID, "INBOX"), metaBytes)

	// Copy to Archive.
	if err := ms.CopyMessage(userID, "INBOX", 1, "Archive"); err != nil {
		t.Fatalf("CopyMessage: %v", err)
	}

	// Original should still be in INBOX.
	if _, err := ms.GetMessage(userID, "INBOX", 1); err != nil {
		t.Error("original message removed from INBOX after copy")
	}

	// Copy should exist in Archive.
	archiveMsgs, _ := ms.ListMessages(userID, "Archive")
	if len(archiveMsgs) != 1 {
		t.Fatalf("expected 1 message in Archive, got %d", len(archiveMsgs))
	}
	if archiveMsgs[0].MailGuid != "g1" {
		t.Errorf("copied mail_guid = %q, want %q", archiveMsgs[0].MailGuid, "g1")
	}

	// Move from INBOX to Archive.
	if err := ms.MoveMessage(userID, "INBOX", 1, "Archive"); err != nil {
		t.Fatalf("MoveMessage: %v", err)
	}
	if _, err := ms.GetMessage(userID, "INBOX", 1); err == nil {
		t.Error("message still in INBOX after move")
	}
	archiveMsgs2, _ := ms.ListMessages(userID, "Archive")
	if len(archiveMsgs2) != 2 {
		t.Fatalf("expected 2 messages in Archive after move, got %d", len(archiveMsgs2))
	}
}

func TestExpunge(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"
	ms.CreateMailbox(userID, "INBOX")

	// Add 3 messages, mark 2 as deleted.
	for i, flags := range []uint32{FlagDeleted, 0, FlagDeleted | FlagSeen} {
		uid := uint32(i + 1)
		msg := &bongov1.MailboxMessage{Uid: uid, MailGuid: fmt.Sprintf("g%d", uid), Flags: flags}
		msgBytes, _ := proto.Marshal(msg)
		ms.store.Set(mailboxMsgKey(userID, "INBOX", uid), msgBytes)
	}

	expunged, err := ms.Expunge(userID, "INBOX")
	if err != nil {
		t.Fatalf("Expunge: %v", err)
	}
	if len(expunged) != 2 {
		t.Fatalf("expected 2 expunged, got %d", len(expunged))
	}

	// Only message 2 should remain.
	remaining, _ := ms.ListMessages(userID, "INBOX")
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].Uid != 2 {
		t.Errorf("remaining uid = %d, want 2", remaining[0].Uid)
	}
}

func TestAppendMessage(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"
	ms.CreateMailbox(userID, "INBOX")

	// Append first message.
	uid1, err := ms.AppendMessage(userID, "INBOX", "guid-1", FlagSeen, 1000)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if uid1 != 1 {
		t.Errorf("first uid = %d, want 1", uid1)
	}

	// Append second message.
	uid2, err := ms.AppendMessage(userID, "INBOX", "guid-2", FlagFlagged, 2000)
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if uid2 != 2 {
		t.Errorf("second uid = %d, want 2", uid2)
	}

	// Verify messages.
	msgs, err := ms.ListMessages(userID, "INBOX")
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].MailGuid != "guid-1" || msgs[0].Flags != FlagSeen {
		t.Errorf("msg[0] = {guid=%q, flags=%d}, want {guid-1, %d}", msgs[0].MailGuid, msgs[0].Flags, FlagSeen)
	}
	if msgs[1].MailGuid != "guid-2" || msgs[1].Flags != FlagFlagged {
		t.Errorf("msg[1] = {guid=%q, flags=%d}, want {guid-2, %d}", msgs[1].MailGuid, msgs[1].Flags, FlagFlagged)
	}

	// Verify UidNext was bumped.
	meta, _ := ms.GetMailbox(userID, "INBOX")
	if meta.UidNext != 3 {
		t.Errorf("UidNext = %d, want 3", meta.UidNext)
	}
}

func TestAppendMessageToNonExistentMailbox(t *testing.T) {
	ms := newMailboxTestEnv(t)
	_, err := ms.AppendMessage("user1", "NoSuchMailbox", "guid-1", 0, 1000)
	if err == nil {
		t.Error("expected error appending to non-existent mailbox")
	}
}

func TestStoreMailMeta(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	meta := &bongov1.MailMeta{
		Guid:    "test-guid",
		Subject: "Hello",
		From:    "sender@example.com",
	}
	if err := ms.StoreMailMeta(userID, meta); err != nil {
		t.Fatalf("StoreMailMeta: %v", err)
	}

	// Verify it was stored.
	key := []byte(fmt.Sprintf("/user/%s/mail/%s", userID, "test-guid"))
	data, err := ms.store.Get(key)
	if err != nil {
		t.Fatalf("Get mail meta: %v", err)
	}
	var got bongov1.MailMeta
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Subject != "Hello" {
		t.Errorf("subject = %q, want %q", got.Subject, "Hello")
	}
}

func TestDeliverMessage(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	meta := &bongov1.MailMeta{
		Guid:       "deliver-guid",
		Subject:    "Delivered",
		From:       "sender@example.com",
		To:         []string{"user@example.com"},
		ReceivedAt: 1700000000,
		RawMessage: &bongov1.BlobRef{Hash: "abc123", Size: 42},
	}

	uid, err := ms.DeliverMessage(userID, "INBOX", meta, FlagSeen)
	if err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}
	if uid != 1 {
		t.Fatalf("uid = %d, want 1", uid)
	}

	gotMetaBytes, err := ms.store.Get([]byte(fmt.Sprintf("/user/%s/mail/%s", userID, meta.Guid)))
	if err != nil {
		t.Fatalf("Get mail meta: %v", err)
	}
	var gotMeta bongov1.MailMeta
	if err := proto.Unmarshal(gotMetaBytes, &gotMeta); err != nil {
		t.Fatalf("Unmarshal meta: %v", err)
	}
	if gotMeta.Subject != meta.Subject {
		t.Errorf("subject = %q, want %q", gotMeta.Subject, meta.Subject)
	}

	msg, err := ms.GetMessage(userID, "INBOX", 1)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if msg.MailGuid != meta.Guid {
		t.Errorf("mail_guid = %q, want %q", msg.MailGuid, meta.Guid)
	}
	if msg.Flags != FlagSeen {
		t.Errorf("flags = %d, want %d", msg.Flags, FlagSeen)
	}
}

func TestDeliverMessageCreatesMailbox(t *testing.T) {
	ms := newMailboxTestEnv(t)

	meta := &bongov1.MailMeta{
		Guid:       "auto-create-guid",
		Subject:    "Auto Create",
		ReceivedAt: 1700000000,
	}

	if _, err := ms.DeliverMessage("user1", "Rules/Newsletters", meta, 0); err != nil {
		t.Fatalf("DeliverMessage: %v", err)
	}

	mb, err := ms.GetMailbox("user1", "Rules/Newsletters")
	if err != nil {
		t.Fatalf("GetMailbox: %v", err)
	}
	if mb.UidNext != 2 {
		t.Errorf("UidNext = %d, want 2", mb.UidNext)
	}
}

func TestCountMessages(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"

	ms.CreateMailbox(userID, "INBOX")

	// Add messages with different flags.
	for i, flags := range []uint32{0, FlagSeen, FlagSeen | FlagFlagged} {
		uid := uint32(i + 1)
		msg := &bongov1.MailboxMessage{Uid: uid, MailGuid: "g" + string(rune('1'+i)), Flags: flags}
		msgBytes, _ := proto.Marshal(msg)
		ms.store.Set(mailboxMsgKey(userID, "INBOX", uid), msgBytes)
	}

	total, _, unseen, err := ms.CountMessages(userID, "INBOX")
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if unseen != 1 {
		t.Errorf("unseen = %d, want 1", unseen)
	}
}

func TestSanitizeMailMeta(t *testing.T) {
	// \xff is invalid UTF-8
	meta := &bongov1.MailMeta{
		Subject:    "Hello \xff world",
		From:       "user\xff@example.com",
		MessageId:  "<msg\xff@example.com>",
		InReplyTo:  "<reply\xff@example.com>",
		References: "<ref\xff@example.com>",
		To:         []string{"to\xff@example.com", "valid@example.com"},
		Cc:         []string{"cc\xff@example.com"},
	}

	SanitizeMailMeta(meta)

	if meta.Subject != "Hello \uFFFD world" {
		t.Errorf("Subject = %q, want invalid bytes replaced", meta.Subject)
	}
	if meta.From != "user\uFFFD@example.com" {
		t.Errorf("From = %q, want invalid bytes replaced", meta.From)
	}
	if meta.To[0] != "to\uFFFD@example.com" {
		t.Errorf("To[0] = %q, want invalid bytes replaced", meta.To[0])
	}
	if meta.To[1] != "valid@example.com" {
		t.Errorf("To[1] = %q, want valid string unchanged", meta.To[1])
	}
	if meta.Cc[0] != "cc\uFFFD@example.com" {
		t.Errorf("Cc[0] = %q, want invalid bytes replaced", meta.Cc[0])
	}

	// Verify proto.Marshal succeeds after sanitization
	if _, err := proto.Marshal(meta); err != nil {
		t.Errorf("proto.Marshal failed after sanitization: %v", err)
	}
}

func TestSanitizeMailMeta_ValidUTF8Unchanged(t *testing.T) {
	meta := &bongov1.MailMeta{
		Subject:   "Valid UTF-8 subject",
		From:      "user@example.com",
		MessageId: "<msg@example.com>",
		To:        []string{"to@example.com"},
	}

	SanitizeMailMeta(meta)

	if meta.Subject != "Valid UTF-8 subject" {
		t.Errorf("Subject changed unexpectedly: %q", meta.Subject)
	}
	if meta.From != "user@example.com" {
		t.Errorf("From changed unexpectedly: %q", meta.From)
	}
}
