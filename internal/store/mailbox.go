package store

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// RaftApplier applies commands through Raft consensus.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// MailboxStore provides IMAP mailbox operations on top of PebbleStore + Raft.
type MailboxStore struct {
	store   *PebbleStore
	applier RaftApplier
}

// NewMailboxStore creates a new MailboxStore.
func NewMailboxStore(store *PebbleStore, applier RaftApplier) *MailboxStore {
	return &MailboxStore{store: store, applier: applier}
}

// DefaultMailboxes is the set of standard IMAP mailboxes.
var DefaultMailboxes = []string{"INBOX", "Sent", "Drafts", "Trash", "Junk"}

func mailboxMetaKey(userID, name string) []byte {
	return []byte(fmt.Sprintf("/user/%s/mailbox/%s/meta", userID, name))
}

func mailboxMsgPrefix(userID, name string) []byte {
	return []byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/", userID, name))
}

func mailboxMsgKey(userID, name string, uid uint32) []byte {
	return []byte(fmt.Sprintf("/user/%s/mailbox/%s/msg/%010d", userID, name, uid))
}

func mailboxPrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/mailbox/", userID))
}

// ListMailboxes returns all mailboxes for a user.
func (ms *MailboxStore) ListMailboxes(userID string) ([]*bongov1.MailboxMeta, error) {
	prefix := mailboxPrefix(userID)
	iter, err := ms.store.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("listing mailboxes: %w", err)
	}
	defer iter.Close()

	seen := make(map[string]bool)
	var result []*bongov1.MailboxMeta

	for iter.Next() {
		key := string(iter.Key())
		if !strings.HasSuffix(key, "/meta") {
			continue
		}
		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("reading mailbox meta: %w", err)
		}
		var meta bongov1.MailboxMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			continue
		}
		if !seen[meta.Name] {
			seen[meta.Name] = true
			result = append(result, &meta)
		}
	}
	return result, nil
}

// GetMailbox returns the metadata for a single mailbox.
func (ms *MailboxStore) GetMailbox(userID, name string) (*bongov1.MailboxMeta, error) {
	data, err := ms.store.Get(mailboxMetaKey(userID, name))
	if err != nil {
		return nil, fmt.Errorf("mailbox %q: %w", name, err)
	}
	var meta bongov1.MailboxMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing mailbox meta: %w", err)
	}
	return &meta, nil
}

// CreateMailbox creates a new mailbox via Raft.
func (ms *MailboxStore) CreateMailbox(userID, name string) error {
	// Check if it already exists.
	if _, err := ms.store.Get(mailboxMetaKey(userID, name)); err == nil {
		return fmt.Errorf("mailbox %q already exists", name)
	}

	meta := &bongov1.MailboxMeta{
		Name:        name,
		UidValidity: uint32(time.Now().Unix()),
		UidNext:     1,
	}
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshaling mailbox meta: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxMetaKey(userID, name),
		Value: metaBytes,
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}

// DeleteMailbox deletes a mailbox and all its messages via Raft.
// INBOX cannot be deleted per IMAP spec.
func (ms *MailboxStore) DeleteMailbox(userID, name string) error {
	if strings.EqualFold(name, "INBOX") {
		return fmt.Errorf("cannot delete INBOX")
	}

	// Delete all message keys.
	msgs, err := ms.ListMessages(userID, name)
	if err != nil {
		return fmt.Errorf("listing messages for delete: %w", err)
	}
	for _, msg := range msgs {
		cmd := &bongov1.RaftCommand{
			Op:  bongov1.Op_OP_DELETE,
			Key: mailboxMsgKey(userID, name, msg.Uid),
		}
		if err := ms.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			return fmt.Errorf("deleting message %d: %w", msg.Uid, err)
		}
	}

	// Delete the meta key.
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: mailboxMetaKey(userID, name),
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}

// RenameMailbox renames a mailbox via Raft.
func (ms *MailboxStore) RenameMailbox(userID, oldName, newName string) error {
	if strings.EqualFold(oldName, "INBOX") {
		return fmt.Errorf("cannot rename INBOX")
	}

	// Read old meta.
	oldMeta, err := ms.GetMailbox(userID, oldName)
	if err != nil {
		return fmt.Errorf("reading old mailbox: %w", err)
	}

	// Create new meta.
	newMeta := &bongov1.MailboxMeta{
		Name:        newName,
		UidValidity: oldMeta.UidValidity,
		UidNext:     oldMeta.UidNext,
	}
	newMetaBytes, _ := proto.Marshal(newMeta)
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxMetaKey(userID, newName),
		Value: newMetaBytes,
	}
	if err := ms.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("creating new mailbox: %w", err)
	}

	// Move all messages.
	msgs, err := ms.ListMessages(userID, oldName)
	if err != nil {
		return fmt.Errorf("listing messages for rename: %w", err)
	}
	for _, msg := range msgs {
		msgBytes, _ := proto.Marshal(msg)
		putCmd := &bongov1.RaftCommand{
			Op:    bongov1.Op_OP_PUT,
			Key:   mailboxMsgKey(userID, newName, msg.Uid),
			Value: msgBytes,
		}
		if err := ms.applier.ApplyCommand(putCmd, 5*time.Second); err != nil {
			return err
		}
		delCmd := &bongov1.RaftCommand{
			Op:  bongov1.Op_OP_DELETE,
			Key: mailboxMsgKey(userID, oldName, msg.Uid),
		}
		if err := ms.applier.ApplyCommand(delCmd, 5*time.Second); err != nil {
			return err
		}
	}

	// Delete old meta.
	delCmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: mailboxMetaKey(userID, oldName),
	}
	return ms.applier.ApplyCommand(delCmd, 5*time.Second)
}

// EnsureDefaultMailboxes idempotently creates the standard IMAP mailboxes.
func (ms *MailboxStore) EnsureDefaultMailboxes(userID string) error {
	for _, name := range DefaultMailboxes {
		if _, err := ms.store.Get(mailboxMetaKey(userID, name)); err == nil {
			continue // already exists
		}
		if err := ms.CreateMailbox(userID, name); err != nil {
			// Ignore "already exists" race.
			if !strings.Contains(err.Error(), "already exists") {
				return fmt.Errorf("creating mailbox %q: %w", name, err)
			}
		}
	}
	return nil
}

// ListMessages returns all messages in a mailbox, ordered by UID.
func (ms *MailboxStore) ListMessages(userID, mailbox string) ([]*bongov1.MailboxMessage, error) {
	prefix := mailboxMsgPrefix(userID, mailbox)
	iter, err := ms.store.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", err)
	}
	defer iter.Close()

	var msgs []*bongov1.MailboxMessage
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("reading message: %w", err)
		}
		var msg bongov1.MailboxMessage
		if err := proto.Unmarshal(val, &msg); err != nil {
			continue
		}
		msgs = append(msgs, &msg)
	}
	return msgs, nil
}

// ListMessagesPaged returns a page of messages in a mailbox, ordered by UID descending (newest first).
// offset is 0-based, limit is max messages to return.
func (ms *MailboxStore) ListMessagesPaged(userID, mailbox string, offset, limit int) ([]*bongov1.MailboxMessage, int, error) {
	all, err := ms.ListMessages(userID, mailbox)
	if err != nil {
		return nil, 0, err
	}

	total := len(all)

	// Reverse to show newest first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	if offset >= len(all) {
		return nil, total, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], total, nil
}

// CountMessages returns the total, recent, and unseen counts for a mailbox.
func (ms *MailboxStore) CountMessages(userID, mailbox string) (total, recent, unseen uint32, err error) {
	msgs, err := ms.ListMessages(userID, mailbox)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, msg := range msgs {
		total++
		if msg.Flags&FlagRecent != 0 {
			recent++
		}
		if msg.Flags&FlagSeen == 0 {
			unseen++
		}
	}
	return total, recent, unseen, nil
}

// IMAP flag constants (bitfield).
const (
	FlagSeen     uint32 = 1
	FlagAnswered uint32 = 2
	FlagFlagged  uint32 = 4
	FlagDeleted  uint32 = 8
	FlagDraft    uint32 = 16
	FlagRecent   uint32 = 32
)

// GetMessage returns a single message by UID.
func (ms *MailboxStore) GetMessage(userID, mailbox string, uid uint32) (*bongov1.MailboxMessage, error) {
	data, err := ms.store.Get(mailboxMsgKey(userID, mailbox, uid))
	if err != nil {
		return nil, fmt.Errorf("message uid %d: %w", uid, err)
	}
	var msg bongov1.MailboxMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("parsing message: %w", err)
	}
	return &msg, nil
}

// SetFlags replaces the flags for a message via Raft.
func (ms *MailboxStore) SetFlags(userID, mailbox string, uid, flags uint32) error {
	msg, err := ms.GetMessage(userID, mailbox, uid)
	if err != nil {
		return err
	}
	msg.Flags = flags
	return ms.writeMessage(userID, mailbox, uid, msg)
}

// AddFlags adds (OR) flags to a message via Raft.
func (ms *MailboxStore) AddFlags(userID, mailbox string, uid, flags uint32) error {
	msg, err := ms.GetMessage(userID, mailbox, uid)
	if err != nil {
		return err
	}
	msg.Flags |= flags
	return ms.writeMessage(userID, mailbox, uid, msg)
}

// RemoveFlags clears (AND NOT) flags from a message via Raft.
func (ms *MailboxStore) RemoveFlags(userID, mailbox string, uid, flags uint32) error {
	msg, err := ms.GetMessage(userID, mailbox, uid)
	if err != nil {
		return err
	}
	msg.Flags &^= flags
	return ms.writeMessage(userID, mailbox, uid, msg)
}

// CopyMessage copies a message from one mailbox to another via Raft.
func (ms *MailboxStore) CopyMessage(userID, srcMailbox string, uid uint32, dstMailbox string) error {
	srcMsg, err := ms.GetMessage(userID, srcMailbox, uid)
	if err != nil {
		return fmt.Errorf("reading source message: %w", err)
	}

	// Get dest mailbox meta to assign a new UID.
	dstMeta, err := ms.GetMailbox(userID, dstMailbox)
	if err != nil {
		return fmt.Errorf("reading dest mailbox: %w", err)
	}

	newUID := dstMeta.UidNext
	dstMeta.UidNext = newUID + 1

	// Write updated dest meta.
	metaBytes, _ := proto.Marshal(dstMeta)
	metaCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxMetaKey(userID, dstMailbox),
		Value: metaBytes,
	}
	if err := ms.applier.ApplyCommand(metaCmd, 5*time.Second); err != nil {
		return fmt.Errorf("updating dest mailbox meta: %w", err)
	}

	// Write the message under the new UID (clear Recent flag).
	newMsg := &bongov1.MailboxMessage{
		Uid:          newUID,
		MailGuid:     srcMsg.MailGuid,
		Flags:        srcMsg.Flags &^ FlagRecent,
		InternalDate: srcMsg.InternalDate,
	}
	return ms.writeMessage(userID, dstMailbox, newUID, newMsg)
}

// MoveMessage moves a message from one mailbox to another via Raft.
func (ms *MailboxStore) MoveMessage(userID, srcMailbox string, uid uint32, dstMailbox string) error {
	if err := ms.CopyMessage(userID, srcMailbox, uid, dstMailbox); err != nil {
		return fmt.Errorf("copy during move: %w", err)
	}

	// Delete from source.
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: mailboxMsgKey(userID, srcMailbox, uid),
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}

// Expunge removes all messages with the Deleted flag from a mailbox.
// Returns the UIDs that were expunged.
func (ms *MailboxStore) Expunge(userID, mailbox string) ([]uint32, error) {
	msgs, err := ms.ListMessages(userID, mailbox)
	if err != nil {
		return nil, fmt.Errorf("listing for expunge: %w", err)
	}

	var expunged []uint32
	for _, msg := range msgs {
		if msg.Flags&FlagDeleted != 0 {
			cmd := &bongov1.RaftCommand{
				Op:  bongov1.Op_OP_DELETE,
				Key: mailboxMsgKey(userID, mailbox, msg.Uid),
			}
			if err := ms.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
				return expunged, fmt.Errorf("expunging uid %d: %w", msg.Uid, err)
			}
			expunged = append(expunged, msg.Uid)
		}
	}
	return expunged, nil
}

// StoreMailMeta writes a MailMeta record for a user via Raft.
func (ms *MailboxStore) StoreMailMeta(userID string, meta *bongov1.MailMeta) error {
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshaling mail meta: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(fmt.Sprintf("/user/%s/mail/%s", userID, meta.Guid)),
		Value: metaBytes,
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}

// AppendMessage appends a message to a mailbox, assigning a new UID via Raft.
// It writes the MailboxMessage and bumps UidNext atomically.
// Returns the assigned UID.
func (ms *MailboxStore) AppendMessage(userID, mailbox, mailGuid string, flags uint32, internalDate int64) (uint32, error) {
	meta, err := ms.GetMailbox(userID, mailbox)
	if err != nil {
		return 0, fmt.Errorf("reading mailbox %q: %w", mailbox, err)
	}

	uid := meta.UidNext
	meta.UidNext = uid + 1

	// Write updated mailbox meta.
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		return 0, fmt.Errorf("marshaling mailbox meta: %w", err)
	}
	metaCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxMetaKey(userID, mailbox),
		Value: metaBytes,
	}
	if err := ms.applier.ApplyCommand(metaCmd, 5*time.Second); err != nil {
		return 0, fmt.Errorf("updating mailbox meta: %w", err)
	}

	// Write the message.
	msg := &bongov1.MailboxMessage{
		Uid:          uid,
		MailGuid:     mailGuid,
		Flags:        flags,
		InternalDate: internalDate,
	}
	if err := ms.writeMessage(userID, mailbox, uid, msg); err != nil {
		return 0, fmt.Errorf("writing message: %w", err)
	}

	return uid, nil
}

func (ms *MailboxStore) writeMessage(userID, mailbox string, uid uint32, msg *bongov1.MailboxMessage) error {
	msgBytes, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxMsgKey(userID, mailbox, uid),
		Value: msgBytes,
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}
