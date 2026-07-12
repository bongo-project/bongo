package store

import (
	"fmt"
	"testing"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
	"google.golang.org/protobuf/proto"
)

func TestOrphanedMailGUIDs(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"
	if err := ms.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	old := time.Now().Add(-2 * time.Hour).Unix()
	recent := time.Now().Unix()

	// referenced: MailMeta with a live mailbox message pointing at it — not orphaned.
	if err := ms.StoreMailMeta(userID, &bongov1.MailMeta{Guid: "referenced", ReceivedAt: old}); err != nil {
		t.Fatalf("StoreMailMeta referenced: %v", err)
	}
	refMsg := &bongov1.MailboxMessage{Uid: 1, MailGuid: "referenced"}
	b, _ := proto.Marshal(refMsg)
	if err := ms.store.Set(mailboxMsgKey(userID, "INBOX", 1), b); err != nil {
		t.Fatalf("set mailbox msg: %v", err)
	}

	// orphanOld: old MailMeta with no mailbox message — should be detected.
	if err := ms.StoreMailMeta(userID, &bongov1.MailMeta{Guid: "orphan-old", ReceivedAt: old}); err != nil {
		t.Fatalf("StoreMailMeta orphan-old: %v", err)
	}

	// orphanRecent: recent MailMeta with no mailbox message — skipped (grace window).
	if err := ms.StoreMailMeta(userID, &bongov1.MailMeta{Guid: "orphan-recent", ReceivedAt: recent}); err != nil {
		t.Fatalf("StoreMailMeta orphan-recent: %v", err)
	}

	orphans, err := ms.OrphanedMailGUIDs(userID, time.Hour)
	if err != nil {
		t.Fatalf("OrphanedMailGUIDs: %v", err)
	}

	if len(orphans) != 1 || orphans[0] != "orphan-old" {
		t.Fatalf("orphans = %v, want [orphan-old]", orphans)
	}
}

func TestOrphanedMailGUIDsAfterExpunge(t *testing.T) {
	ms := newMailboxTestEnv(t)
	userID := "user1"
	if err := ms.CreateMailbox(userID, "INBOX"); err != nil {
		t.Fatalf("CreateMailbox: %v", err)
	}

	// A delivered message: MailMeta + mailbox entry, flagged Deleted, old.
	old := time.Now().Add(-2 * time.Hour).Unix()
	if err := ms.StoreMailMeta(userID, &bongov1.MailMeta{Guid: "g1", ReceivedAt: old}); err != nil {
		t.Fatalf("StoreMailMeta: %v", err)
	}
	msg := &bongov1.MailboxMessage{Uid: 1, MailGuid: "g1", Flags: FlagDeleted}
	b, _ := proto.Marshal(msg)
	if err := ms.store.Set(mailboxMsgKey(userID, "INBOX", 1), b); err != nil {
		t.Fatalf("set mailbox msg: %v", err)
	}

	// Not orphaned yet — the mailbox entry still references it.
	if orphans, _ := ms.OrphanedMailGUIDs(userID, time.Hour); len(orphans) != 0 {
		t.Fatalf("before expunge: orphans = %v, want none", orphans)
	}

	if _, err := ms.Expunge(userID, "INBOX"); err != nil {
		t.Fatalf("Expunge: %v", err)
	}

	// After expunge the mailbox entry is gone but MailMeta remains → orphaned.
	orphans, err := ms.OrphanedMailGUIDs(userID, time.Hour)
	if err != nil {
		t.Fatalf("OrphanedMailGUIDs: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != "g1" {
		t.Fatalf("after expunge: orphans = %v, want [g1]", orphans)
	}

	// Deleting the MailMeta key clears it.
	key := []byte(fmt.Sprintf("/user/%s/mail/%s", userID, "g1"))
	if err := ms.store.Delete(key); err != nil {
		t.Fatalf("delete mail meta: %v", err)
	}
	if orphans, _ := ms.OrphanedMailGUIDs(userID, time.Hour); len(orphans) != 0 {
		t.Fatalf("after cleanup: orphans = %v, want none", orphans)
	}
}
