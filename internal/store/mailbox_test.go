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
