package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// RaftApplier applies commands through Raft consensus.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// MailboxStore provides IMAP mailbox operations on top of PebbleStore + Raft.
type MailboxStore struct {
	store    *PebbleStore
	applier  RaftApplier
	notifier MailboxNotifier
}

// NewMailboxStore creates a new MailboxStore.
func NewMailboxStore(store *PebbleStore, applier RaftApplier) *MailboxStore {
	return &MailboxStore{store: store, applier: applier}
}

// DefaultMailboxes is the set of standard IMAP mailboxes.
var DefaultMailboxes = []string{"INBOX", "Sent", "Drafts", "Trash", "Junk"}

const (
	mailboxAppendPrefix  = "/__mailbox_append__/"
	mailboxCopyPrefix    = "/__mailbox_copy__/"
	mailboxDeletePrefix  = "/__mailbox_delete__/"
	mailboxDeliverPrefix = "/__mailbox_deliver__/"
	mailboxExpungePrefix = "/__mailbox_expunge__/"
	mailboxMovePrefix    = "/__mailbox_move__/"
	mailboxRenamePrefix  = "/__mailbox_rename__/"

	mailboxSubscribePrefix = "/__mailbox_subscribe__/"
)

type mailboxTransferPayload struct {
	SrcMailbox string `json:"src_mailbox"`
	SrcUID     uint32 `json:"src_uid"`
	DstMailbox string `json:"dst_mailbox"`
}

type mailboxRenamePayload struct {
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
}

type mailboxDeliveryPayload struct {
	Meta  []byte `json:"meta"`
	Flags uint32 `json:"flags"`
}

// mailboxSubscribePayload carries the state the client asked for, in the
// positive sense the IMAP commands use. The inversion to MailboxMeta's
// unsubscribed field happens at apply time, so the wire payload of a
// SUBSCRIBE reads as "subscribed": true.
type mailboxSubscribePayload struct {
	Subscribed bool `json:"subscribed"`
}

// MailboxCommandResult reports what a synthetic mailbox command changed. It is
// produced inside the apply, which runs on EVERY node, so anything derived from
// it -- derived indexes, notifications -- happens cluster-wide rather than only
// on the node whose client issued the command.
type MailboxCommandResult struct {
	// NewMail describes a delivery or an append. It is distinct from Arrivals
	// because it carries the GUID and MailMeta the caller needs for derived
	// indexing, not just the mailbox position a watcher needs.
	NewMail *MailboxNewMail

	// Arrivals lists messages that appeared in a mailbox without a new MailMeta
	// being written: the destination half of a move or a copy. Nothing else
	// announces those, so a client watching the destination would otherwise see
	// a mailbox that silently gained a message.
	Arrivals []MailboxMessageRef

	// Expunged lists messages that are no longer in their mailbox: the ones an
	// EXPUNGE removed, and the source half of a move.
	Expunged []MailboxMessageRef
}

// MailboxMessageRef names one message by its position in a mailbox. It carries
// no sequence number for the reason set out on MailboxNotifier.NotifyExpunge:
// a sequence number belongs to a session's view, not to the message.
type MailboxMessageRef struct {
	UserID  string
	Mailbox string
	UID     uint32
}

type MailboxNewMail struct {
	UserID  string
	Mailbox string
	UID     uint32
	GUID    string
	Meta    *bongov1.MailMeta
}

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

func mailboxAppendKey(userID, mailbox string) []byte {
	return []byte(mailboxAppendPrefix + userID + "/" + mailbox)
}

func mailboxCopyKey(userID string) []byte {
	return []byte(mailboxCopyPrefix + userID)
}

func mailboxDeleteKey(userID, mailbox string) []byte {
	return []byte(mailboxDeletePrefix + userID + "/" + mailbox)
}

func mailboxDeliverKey(userID, mailbox string) []byte {
	return []byte(mailboxDeliverPrefix + userID + "/" + mailbox)
}

func mailboxExpungeKey(userID, mailbox string) []byte {
	return []byte(mailboxExpungePrefix + userID + "/" + mailbox)
}

func mailboxMoveKey(userID string) []byte {
	return []byte(mailboxMovePrefix + userID)
}

func mailboxRenameKey(userID string) []byte {
	return []byte(mailboxRenamePrefix + userID)
}

func mailboxSubscribeKey(userID, mailbox string) []byte {
	return []byte(mailboxSubscribePrefix + userID + "/" + mailbox)
}

// IsMailboxAppendCommand reports whether the key represents a synthetic
// single-command mailbox append operation.
func IsMailboxAppendCommand(key []byte) bool {
	return strings.HasPrefix(string(key), mailboxAppendPrefix)
}

// IsMailboxSyntheticCommand reports whether the key represents a synthetic
// mailbox operation handled directly by the FSM.
func IsMailboxSyntheticCommand(key []byte) bool {
	keyStr := string(key)
	return strings.HasPrefix(keyStr, mailboxAppendPrefix) ||
		strings.HasPrefix(keyStr, mailboxCopyPrefix) ||
		strings.HasPrefix(keyStr, mailboxDeletePrefix) ||
		strings.HasPrefix(keyStr, mailboxDeliverPrefix) ||
		strings.HasPrefix(keyStr, mailboxExpungePrefix) ||
		strings.HasPrefix(keyStr, mailboxMovePrefix) ||
		strings.HasPrefix(keyStr, mailboxRenamePrefix) ||
		strings.HasPrefix(keyStr, mailboxSubscribePrefix)
}

// ApplyMailboxCommand applies a synthetic mailbox command directly to the
// local Pebble store.
func ApplyMailboxCommand(ps *PebbleStore, cmd *bongov1.RaftCommand) (*MailboxCommandResult, error) {
	switch {
	case IsMailboxAppendCommand(cmd.Key):
		return applyMailboxAppend(ps, cmd)
	case strings.HasPrefix(string(cmd.Key), mailboxCopyPrefix):
		return applyMailboxCopy(ps, cmd)
	case strings.HasPrefix(string(cmd.Key), mailboxDeletePrefix):
		return nil, applyMailboxDelete(ps, cmd)
	case strings.HasPrefix(string(cmd.Key), mailboxDeliverPrefix):
		return applyMailboxDelivery(ps, cmd)
	case strings.HasPrefix(string(cmd.Key), mailboxExpungePrefix):
		return applyMailboxExpunge(ps, cmd)
	case strings.HasPrefix(string(cmd.Key), mailboxMovePrefix):
		return applyMailboxMove(ps, cmd)
	case strings.HasPrefix(string(cmd.Key), mailboxRenamePrefix):
		return nil, applyMailboxRename(ps, cmd)
	case strings.HasPrefix(string(cmd.Key), mailboxSubscribePrefix):
		return nil, applyMailboxSubscribe(ps, cmd)
	default:
		return nil, fmt.Errorf("not a synthetic mailbox command")
	}
}

// applyMailboxAppend applies a synthetic mailbox append command directly to the
// local Pebble store. It updates mailbox metadata and writes the mailbox
// message as one FSM-level operation.
func applyMailboxAppend(ps *PebbleStore, cmd *bongov1.RaftCommand) (*MailboxCommandResult, error) {
	rest := strings.TrimPrefix(string(cmd.Key), mailboxAppendPrefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid mailbox append key %q", string(cmd.Key))
	}
	userID, mailbox := parts[0], parts[1]

	var msg bongov1.MailboxMessage
	if err := proto.Unmarshal(cmd.Value, &msg); err != nil {
		return nil, fmt.Errorf("parsing mailbox append payload: %w", err)
	}
	if msg.MailGuid == "" {
		return nil, fmt.Errorf("mailbox append payload missing mail guid")
	}

	meta, err := readMailboxMeta(ps, userID, mailbox)
	if err != nil {
		return nil, err
	}
	uid := meta.UidNext
	meta.UidNext = uid + 1

	msg.Uid = uid
	if err := commitSyncBatch(ps, func(batch *pebble.Batch) error {
		if err := writeMailboxMetaBatch(batch, userID, mailbox, meta); err != nil {
			return err
		}
		return writeMailboxMessageBatch(batch, userID, mailbox, uid, &msg)
	}); err != nil {
		return nil, err
	}

	return &MailboxCommandResult{
		NewMail: &MailboxNewMail{
			UserID:  userID,
			Mailbox: mailbox,
			UID:     uid,
			GUID:    msg.MailGuid,
		},
	}, nil
}

func applyMailboxDelivery(ps *PebbleStore, cmd *bongov1.RaftCommand) (*MailboxCommandResult, error) {
	userID, mailbox, err := parseMailboxScopedKey(cmd.Key, mailboxDeliverPrefix)
	if err != nil {
		return nil, err
	}

	var payload mailboxDeliveryPayload
	if err := json.Unmarshal(cmd.Value, &payload); err != nil {
		return nil, fmt.Errorf("parsing mailbox delivery payload: %w", err)
	}

	var meta bongov1.MailMeta
	if err := proto.Unmarshal(payload.Meta, &meta); err != nil {
		return nil, fmt.Errorf("parsing mail meta payload: %w", err)
	}
	if meta.Guid == "" {
		return nil, fmt.Errorf("mailbox delivery payload missing guid")
	}

	mbMeta, err := readOrInitMailboxMeta(ps, userID, mailbox)
	if err != nil {
		return nil, err
	}
	uid := mbMeta.UidNext
	mbMeta.UidNext = uid + 1

	msg := &bongov1.MailboxMessage{
		Uid:          uid,
		MailGuid:     meta.Guid,
		Flags:        payload.Flags,
		InternalDate: meta.ReceivedAt,
		Tags:         append([]string(nil), meta.Tags...),
	}

	if err := commitSyncBatch(ps, func(batch *pebble.Batch) error {
		if err := batch.Set([]byte(fmt.Sprintf("/user/%s/mail/%s", userID, meta.Guid)), payload.Meta, nil); err != nil {
			return fmt.Errorf("writing mail meta: %w", err)
		}
		if err := writeMailboxMetaBatch(batch, userID, mailbox, mbMeta); err != nil {
			return err
		}
		return writeMailboxMessageBatch(batch, userID, mailbox, uid, msg)
	}); err != nil {
		return nil, err
	}

	return &MailboxCommandResult{
		NewMail: &MailboxNewMail{
			UserID:  userID,
			Mailbox: mailbox,
			UID:     uid,
			GUID:    meta.Guid,
			Meta:    &meta,
		},
	}, nil
}

func applyMailboxCopy(ps *PebbleStore, cmd *bongov1.RaftCommand) (*MailboxCommandResult, error) {
	userID, payload, err := parseMailboxTransferCommand(cmd.Key, cmd.Value, mailboxCopyPrefix)
	if err != nil {
		return nil, err
	}

	srcMsg, err := readMailboxMessage(ps, userID, payload.SrcMailbox, payload.SrcUID)
	if err != nil {
		return nil, fmt.Errorf("reading source message: %w", err)
	}

	dstMeta, err := readMailboxMeta(ps, userID, payload.DstMailbox)
	if err != nil {
		return nil, fmt.Errorf("reading dest mailbox: %w", err)
	}
	newUID := dstMeta.UidNext
	dstMeta.UidNext = newUID + 1

	newMsg := &bongov1.MailboxMessage{
		Uid:          newUID,
		MailGuid:     srcMsg.MailGuid,
		Flags:        srcMsg.Flags &^ FlagRecent,
		InternalDate: srcMsg.InternalDate,
		Tags:         append([]string(nil), srcMsg.Tags...),
	}
	if err := commitSyncBatch(ps, func(batch *pebble.Batch) error {
		if err := writeMailboxMetaBatch(batch, userID, payload.DstMailbox, dstMeta); err != nil {
			return err
		}
		return writeMailboxMessageBatch(batch, userID, payload.DstMailbox, newUID, newMsg)
	}); err != nil {
		return nil, err
	}

	return &MailboxCommandResult{
		Arrivals: []MailboxMessageRef{{UserID: userID, Mailbox: payload.DstMailbox, UID: newUID}},
	}, nil
}

func applyMailboxDelete(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	userID, mailbox, err := parseMailboxScopedKey(cmd.Key, mailboxDeletePrefix)
	if err != nil {
		return err
	}
	if strings.EqualFold(mailbox, "INBOX") {
		return fmt.Errorf("cannot delete INBOX")
	}

	msgs, err := listMailboxMessages(ps, userID, mailbox)
	if err != nil {
		return fmt.Errorf("listing messages for delete: %w", err)
	}
	return commitSyncBatch(ps, func(batch *pebble.Batch) error {
		for _, msg := range msgs {
			if err := batch.Delete(mailboxMsgKey(userID, mailbox, msg.Uid), nil); err != nil {
				return fmt.Errorf("deleting message %d: %w", msg.Uid, err)
			}
		}
		if err := batch.Delete(mailboxMetaKey(userID, mailbox), nil); err != nil {
			return fmt.Errorf("deleting mailbox meta: %w", err)
		}
		return nil
	})
}

func applyMailboxExpunge(ps *PebbleStore, cmd *bongov1.RaftCommand) (*MailboxCommandResult, error) {
	userID, mailbox, err := parseMailboxScopedKey(cmd.Key, mailboxExpungePrefix)
	if err != nil {
		return nil, err
	}

	msgs, err := listMailboxMessages(ps, userID, mailbox)
	if err != nil {
		return nil, fmt.Errorf("listing for expunge: %w", err)
	}
	// Which UIDs go is decided here and nowhere else: the flags are read on this
	// node, inside the serialised apply. A caller that recomputed the set before
	// issuing the command could disagree with what actually happened, and every
	// other node has no caller at all.
	var removed []MailboxMessageRef
	if err := commitSyncBatch(ps, func(batch *pebble.Batch) error {
		removed = removed[:0]
		for _, msg := range msgs {
			if msg.Flags&FlagDeleted == 0 {
				continue
			}
			if err := batch.Delete(mailboxMsgKey(userID, mailbox, msg.Uid), nil); err != nil {
				return fmt.Errorf("expunging uid %d: %w", msg.Uid, err)
			}
			removed = append(removed, MailboxMessageRef{UserID: userID, Mailbox: mailbox, UID: msg.Uid})
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &MailboxCommandResult{Expunged: removed}, nil
}

func applyMailboxMove(ps *PebbleStore, cmd *bongov1.RaftCommand) (*MailboxCommandResult, error) {
	userID, payload, err := parseMailboxTransferCommand(cmd.Key, cmd.Value, mailboxMovePrefix)
	if err != nil {
		return nil, err
	}

	srcMsg, err := readMailboxMessage(ps, userID, payload.SrcMailbox, payload.SrcUID)
	if err != nil {
		return nil, fmt.Errorf("reading source message: %w", err)
	}

	dstMeta, err := readMailboxMeta(ps, userID, payload.DstMailbox)
	if err != nil {
		return nil, fmt.Errorf("reading dest mailbox: %w", err)
	}
	newUID := dstMeta.UidNext
	dstMeta.UidNext = newUID + 1

	newMsg := &bongov1.MailboxMessage{
		Uid:          newUID,
		MailGuid:     srcMsg.MailGuid,
		Flags:        srcMsg.Flags &^ FlagRecent,
		InternalDate: srcMsg.InternalDate,
		Tags:         append([]string(nil), srcMsg.Tags...),
	}
	if err := commitSyncBatch(ps, func(batch *pebble.Batch) error {
		if err := writeMailboxMetaBatch(batch, userID, payload.DstMailbox, dstMeta); err != nil {
			return err
		}
		if err := writeMailboxMessageBatch(batch, userID, payload.DstMailbox, newUID, newMsg); err != nil {
			return err
		}
		if err := batch.Delete(mailboxMsgKey(userID, payload.SrcMailbox, payload.SrcUID), nil); err != nil {
			return fmt.Errorf("deleting source message: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// A move is a removal and an arrival, and watchers of each mailbox care
	// about only their half. The destination UID is the one allocated here, not
	// one guessed from the mailbox's uid_next after the fact.
	return &MailboxCommandResult{
		Expunged: []MailboxMessageRef{{UserID: userID, Mailbox: payload.SrcMailbox, UID: payload.SrcUID}},
		Arrivals: []MailboxMessageRef{{UserID: userID, Mailbox: payload.DstMailbox, UID: newUID}},
	}, nil
}

func applyMailboxRename(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	userID, payload, err := parseMailboxRenameCommand(cmd.Key, cmd.Value)
	if err != nil {
		return err
	}
	if strings.EqualFold(payload.OldName, "INBOX") {
		return fmt.Errorf("cannot rename INBOX")
	}

	oldMeta, err := readMailboxMeta(ps, userID, payload.OldName)
	if err != nil {
		return fmt.Errorf("reading old mailbox: %w", err)
	}

	// Refuse to overwrite. Writing oldMeta over an existing target would
	// orphan its messages under a mismatched uid_next, and new == old would
	// write then delete the same keys, wiping the mailbox. This runs inside
	// the FSM so it holds under races and on followers.
	if strings.EqualFold(payload.NewName, "INBOX") || payload.NewName == payload.OldName {
		return fmt.Errorf("mailbox %q already exists", payload.NewName)
	}
	if _, err := ps.Get(mailboxMetaKey(userID, payload.NewName)); err == nil {
		return fmt.Errorf("mailbox %q already exists", payload.NewName)
	} else if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("checking rename target: %w", err)
	}

	newMeta := &bongov1.MailboxMeta{
		Name:        payload.NewName,
		UidValidity: oldMeta.UidValidity,
		UidNext:     oldMeta.UidNext,
		// Carried over so a rename does not silently resubscribe a folder the
		// user had unsubscribed. It is the same mailbox under a new name.
		Unsubscribed: oldMeta.Unsubscribed,
	}
	msgs, err := listMailboxMessages(ps, userID, payload.OldName)
	if err != nil {
		return fmt.Errorf("listing messages for rename: %w", err)
	}
	return commitSyncBatch(ps, func(batch *pebble.Batch) error {
		if err := writeMailboxMetaBatch(batch, userID, payload.NewName, newMeta); err != nil {
			return err
		}
		for _, msg := range msgs {
			if err := writeMailboxMessageBatch(batch, userID, payload.NewName, msg.Uid, msg); err != nil {
				return err
			}
			if err := batch.Delete(mailboxMsgKey(userID, payload.OldName, msg.Uid), nil); err != nil {
				return fmt.Errorf("deleting old mailbox message %d: %w", msg.Uid, err)
			}
		}
		if err := batch.Delete(mailboxMetaKey(userID, payload.OldName), nil); err != nil {
			return fmt.Errorf("deleting old mailbox meta: %w", err)
		}
		return nil
	})
}

// applyMailboxSubscribe records a mailbox's subscription state.
//
// It is a synthetic command rather than a plain PUT of a client-marshalled
// MailboxMeta because the read-modify-write has to happen here, inside the FSM
// apply that Raft serialises. A SUBSCRIBE that marshalled the whole meta on the
// session's side would carry a stale uid_next and could roll back a delivery
// that committed in between, handing the next message a duplicate UID.
func applyMailboxSubscribe(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	userID, mailbox, err := parseMailboxScopedKey(cmd.Key, mailboxSubscribePrefix)
	if err != nil {
		return err
	}
	var payload mailboxSubscribePayload
	if err := json.Unmarshal(cmd.Value, &payload); err != nil {
		return fmt.Errorf("parsing mailbox subscribe payload: %w", err)
	}

	meta, err := readMailboxMeta(ps, userID, mailbox)
	if err != nil {
		return fmt.Errorf("reading mailbox for subscribe: %w", err)
	}
	meta.Unsubscribed = !payload.Subscribed

	return commitSyncBatch(ps, func(batch *pebble.Batch) error {
		return writeMailboxMetaBatch(batch, userID, mailbox, meta)
	})
}

func readMailboxMeta(ps *PebbleStore, userID, mailbox string) (*bongov1.MailboxMeta, error) {
	data, err := ps.Get(mailboxMetaKey(userID, mailbox))
	if err != nil {
		return nil, fmt.Errorf("reading mailbox %q: %w", mailbox, err)
	}
	var meta bongov1.MailboxMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing mailbox meta: %w", err)
	}
	return &meta, nil
}

func readOrInitMailboxMeta(ps *PebbleStore, userID, mailbox string) (*bongov1.MailboxMeta, error) {
	meta, err := readMailboxMeta(ps, userID, mailbox)
	if err == nil {
		return meta, nil
	}
	if strings.Contains(err.Error(), ErrNotFound.Error()) {
		return &bongov1.MailboxMeta{
			Name:        mailbox,
			UidValidity: 1,
			UidNext:     1,
		}, nil
	}
	return nil, err
}

func parseMailboxScopedKey(key []byte, prefix string) (string, string, error) {
	rest := strings.TrimPrefix(string(key), prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid mailbox-scoped key %q", string(key))
	}
	return parts[0], parts[1], nil
}

func writeMailboxMetaBatch(batch *pebble.Batch, userID, mailbox string, meta *bongov1.MailboxMeta) error {
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshaling mailbox meta: %w", err)
	}
	if err := batch.Set(mailboxMetaKey(userID, mailbox), metaBytes, nil); err != nil {
		return fmt.Errorf("updating mailbox meta: %w", err)
	}
	return nil
}

func readMailboxMessage(ps *PebbleStore, userID, mailbox string, uid uint32) (*bongov1.MailboxMessage, error) {
	data, err := ps.Get(mailboxMsgKey(userID, mailbox, uid))
	if err != nil {
		return nil, fmt.Errorf("reading mailbox message: %w", err)
	}
	var msg bongov1.MailboxMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("parsing mailbox message: %w", err)
	}
	return &msg, nil
}

func writeMailboxMessageBatch(batch *pebble.Batch, userID, mailbox string, uid uint32, msg *bongov1.MailboxMessage) error {
	msgBytes, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling mailbox message: %w", err)
	}
	if err := batch.Set(mailboxMsgKey(userID, mailbox, uid), msgBytes, nil); err != nil {
		return fmt.Errorf("writing mailbox message: %w", err)
	}
	return nil
}

// commitSyncBatch runs fn against one Pebble batch and commits it with
// pebble.Sync, so every write fn makes lands together or not at all. It is the
// shared primitive behind the synthetic mailbox and queue commands.
//
// It is a var rather than a plain func so tests can substitute
// a commit that aborts after the batch is populated, simulating a crash in the
// window the batch exists to close. That is the only way to observe the
// all-or-nothing guarantee: with every write inside the batch an aborted commit
// leaves nothing behind, and a write hoisted out of it survives and is visible.
// See TestMailboxDeliveryIsAllOrNothing.
var commitSyncBatch = func(ps *PebbleStore, fn func(batch *pebble.Batch) error) error {
	batch := ps.db.NewBatch()
	defer batch.Close()
	if err := fn(batch); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("committing mailbox batch: %w", err)
	}
	return nil
}

func listMailboxMessages(ps *PebbleStore, userID, mailbox string) ([]*bongov1.MailboxMessage, error) {
	iter, err := ps.PrefixScan(mailboxMsgPrefix(userID, mailbox))
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
			return nil, fmt.Errorf("parsing mailbox message: %w", err)
		}
		msgs = append(msgs, &msg)
	}
	return msgs, nil
}

func parseMailboxTransferCommand(key, value []byte, prefix string) (string, mailboxTransferPayload, error) {
	userID := strings.TrimPrefix(string(key), prefix)
	if userID == "" {
		return "", mailboxTransferPayload{}, fmt.Errorf("invalid mailbox transfer key %q", string(key))
	}
	var payload mailboxTransferPayload
	if err := json.Unmarshal(value, &payload); err != nil {
		return "", mailboxTransferPayload{}, fmt.Errorf("parsing mailbox transfer payload: %w", err)
	}
	if payload.SrcMailbox == "" || payload.DstMailbox == "" || payload.SrcUID == 0 {
		return "", mailboxTransferPayload{}, fmt.Errorf("invalid mailbox transfer payload")
	}
	return userID, payload, nil
}

func parseMailboxRenameCommand(key, value []byte) (string, mailboxRenamePayload, error) {
	userID := strings.TrimPrefix(string(key), mailboxRenamePrefix)
	if userID == "" {
		return "", mailboxRenamePayload{}, fmt.Errorf("invalid mailbox rename key %q", string(key))
	}
	var payload mailboxRenamePayload
	if err := json.Unmarshal(value, &payload); err != nil {
		return "", mailboxRenamePayload{}, fmt.Errorf("parsing mailbox rename payload: %w", err)
	}
	if payload.OldName == "" || payload.NewName == "" {
		return "", mailboxRenamePayload{}, fmt.Errorf("invalid mailbox rename payload")
	}
	return userID, payload, nil
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
		UidValidity: uint32(time.Now().Unix() & 0xffffffff),
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
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_PUT,
		Key: mailboxDeleteKey(userID, name),
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}

// RenameMailbox renames a mailbox via Raft.
func (ms *MailboxStore) RenameMailbox(userID, oldName, newName string) error {
	if strings.EqualFold(oldName, "INBOX") {
		return fmt.Errorf("cannot rename INBOX")
	}
	payload, err := json.Marshal(mailboxRenamePayload{OldName: oldName, NewName: newName})
	if err != nil {
		return fmt.Errorf("marshaling mailbox rename payload: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxRenameKey(userID),
		Value: payload,
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}

// SetSubscribed records the IMAP subscription state of a mailbox via Raft.
//
// A mailbox that does not exist is an error here. RFC 3501 section 6.3.6 makes
// that optional for SUBSCRIBE ("a server MAY validate the mailbox argument"),
// and section 6.3.7 expects UNSUBSCRIBE to succeed on a name whose mailbox is
// gone, so the IMAP session maps ErrNotFound to OK for UNSUBSCRIBE. State is
// stored on the mailbox meta, so a subscription can never outlive its mailbox
// and there is never a stale one left to remove.
//
// The check is duplicated in applyMailboxSubscribe, which is the one that
// holds under a race with DELETE and on a follower replaying the log. This one
// fails fast with the mailbox name in the message and spares the cluster a
// Raft round trip for a command that cannot apply.
func (ms *MailboxStore) SetSubscribed(userID, name string, subscribed bool) error {
	if _, err := ms.GetMailbox(userID, name); err != nil {
		return err
	}
	payload, err := json.Marshal(mailboxSubscribePayload{Subscribed: subscribed})
	if err != nil {
		return fmt.Errorf("marshaling mailbox subscribe payload: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxSubscribeKey(userID, name),
		Value: payload,
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
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

// VirtualMailboxes are special virtual folder names.
var VirtualMailboxes = map[string]bool{
	"unread":   true,
	"starred":  true,
	"all-mail": true,
}

// ListVirtualMessages returns messages matching a virtual folder filter across all mailboxes.
func (ms *MailboxStore) ListVirtualMessages(userID, virtual string, offset, limit int) ([]*bongov1.MailboxMessage, int, error) {
	mailboxes, err := ms.ListMailboxes(userID)
	if err != nil {
		return nil, 0, err
	}

	var all []*bongov1.MailboxMessage
	for _, mb := range mailboxes {
		msgs, err := ms.ListMessages(userID, mb.Name)
		if err != nil {
			continue
		}
		for _, msg := range msgs {
			switch virtual {
			case "unread":
				if msg.Flags&FlagSeen == 0 {
					all = append(all, msg)
				}
			case "starred":
				if msg.Flags&FlagFlagged != 0 {
					all = append(all, msg)
				}
			case "all-mail":
				// All Mail excludes Trash, so a deleted message (moved to
				// Trash) no longer appears here — matching the expectation that
				// "delete" removes it from the everyday view.
				if mb.Name == "Trash" {
					continue
				}
				all = append(all, msg)
			}
		}
	}

	total := len(all)

	// Sort newest first by InternalDate.
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

// ListMessagesByGUIDs returns paged MailboxMessage records whose MailGuid is in the
// provided set. It scans all mailboxes for the user and filters by GUID membership.
// Results are sorted newest first by InternalDate.
func (ms *MailboxStore) ListMessagesByGUIDs(userID string, guidSet map[string]bool, offset, limit int) ([]*bongov1.MailboxMessage, int, error) {
	mailboxes, err := ms.ListMailboxes(userID)
	if err != nil {
		return nil, 0, err
	}

	var all []*bongov1.MailboxMessage
	for _, mb := range mailboxes {
		msgs, err := ms.ListMessages(userID, mb.Name)
		if err != nil {
			continue
		}
		for _, msg := range msgs {
			if guidSet[msg.MailGuid] {
				all = append(all, msg)
			}
		}
	}

	total := len(all)

	// Sort newest first by InternalDate.
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

// UnreadCount returns the number of unread messages in a single mailbox for a
// user. Used for the sidebar badge, which reflects actionable INBOX mail rather
// than the sum across Sent/Trash/Junk (see TotalUnreadCount).
func (ms *MailboxStore) UnreadCount(userID, mailbox string) int {
	_, _, unseen, _ := ms.CountMessages(userID, mailbox)
	return int(unseen)
}

// TotalUnreadCount returns the total number of unread messages across all mailboxes for a user.
func (ms *MailboxStore) TotalUnreadCount(userID string) int {
	mailboxes, err := ms.ListMailboxes(userID)
	if err != nil {
		return 0
	}
	var count int
	for _, mb := range mailboxes {
		_, _, unseen, _ := ms.CountMessages(userID, mb.Name)
		count += int(unseen)
	}
	return count
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

// GetMessageVirtual resolves a UID within a virtual mailbox (unread/starred/all-mail)
// by scanning all real mailboxes. Returns the message and the real mailbox name.
func (ms *MailboxStore) GetMessageVirtual(userID, virtual string, uid uint32) (*bongov1.MailboxMessage, string, error) {
	mailboxes, err := ms.ListMailboxes(userID)
	if err != nil {
		return nil, "", err
	}
	for _, mb := range mailboxes {
		msgs, err := ms.ListMessages(userID, mb.Name)
		if err != nil {
			continue
		}
		for _, msg := range msgs {
			if msg.Uid != uid {
				continue
			}
			switch virtual {
			case "unread":
				if msg.Flags&FlagSeen != 0 {
					continue
				}
			case "starred":
				if msg.Flags&FlagFlagged == 0 {
					continue
				}
			case "all-mail":
				// all messages qualify
			default:
				return nil, "", fmt.Errorf("unknown virtual mailbox: %s", virtual)
			}
			return msg, mb.Name, nil
		}
	}
	return nil, "", fmt.Errorf("message uid %d not found in virtual mailbox %s", uid, virtual)
}

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
	payload, err := json.Marshal(mailboxTransferPayload{
		SrcMailbox: srcMailbox,
		SrcUID:     uid,
		DstMailbox: dstMailbox,
	})
	if err != nil {
		return fmt.Errorf("marshaling mailbox copy payload: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxCopyKey(userID),
		Value: payload,
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}

// MoveMessage moves a message from one mailbox to another via Raft.
func (ms *MailboxStore) MoveMessage(userID, srcMailbox string, uid uint32, dstMailbox string) error {
	payload, err := json.Marshal(mailboxTransferPayload{
		SrcMailbox: srcMailbox,
		SrcUID:     uid,
		DstMailbox: dstMailbox,
	})
	if err != nil {
		return fmt.Errorf("marshaling mailbox move payload: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxMoveKey(userID),
		Value: payload,
	}
	return ms.applier.ApplyCommand(cmd, 5*time.Second)
}

// OrphanedMailGUIDs returns mail GUIDs whose MailMeta exists in Pebble but which
// no MailboxMessage in any of the user's mailboxes references — e.g. metadata
// left behind by EXPUNGE, which deletes the mailbox entry but not the MailMeta.
// Results are restricted to MailMeta older than minAge so the sweep never races
// with in-flight delivery that writes the MailMeta just before the mailbox entry.
// Read-only.
func (ms *MailboxStore) OrphanedMailGUIDs(userID string, minAge time.Duration) ([]string, error) {
	// Set of GUIDs still referenced by a live mailbox message.
	referenced := make(map[string]bool)
	mailboxes, err := ms.ListMailboxes(userID)
	if err != nil {
		return nil, fmt.Errorf("listing mailboxes: %w", err)
	}
	for _, mb := range mailboxes {
		msgs, err := listMailboxMessages(ms.store, userID, mb.Name)
		if err != nil {
			return nil, fmt.Errorf("listing messages in %q: %w", mb.Name, err)
		}
		for _, m := range msgs {
			referenced[m.MailGuid] = true
		}
	}

	prefix := mailMetaPrefix(userID)
	iter, err := ms.store.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("scanning mail meta: %w", err)
	}
	defer iter.Close()

	cutoff := time.Now().Add(-minAge).Unix()
	var orphans []string
	for iter.Next() {
		guid := strings.TrimPrefix(string(iter.Key()), string(prefix))
		// Only direct mail-meta keys (guid has no further path separator).
		if guid == "" || strings.Contains(guid, "/") || referenced[guid] {
			continue
		}
		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("reading mail meta: %w", err)
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			continue
		}
		// Skip recent metadata: it may be a message mid-delivery whose mailbox
		// entry has not been written yet.
		if meta.ReceivedAt > cutoff {
			continue
		}
		orphans = append(orphans, guid)
	}
	return orphans, nil
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
			expunged = append(expunged, msg.Uid)
		}
	}

	if len(expunged) == 0 {
		return nil, nil
	}

	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_PUT,
		Key: mailboxExpungeKey(userID, mailbox),
	}
	if err := ms.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return expunged, fmt.Errorf("expunging mailbox: %w", err)
	}
	return expunged, nil
}

// sanitizeUTF8 replaces invalid UTF-8 bytes with the Unicode replacement
// character so protobuf marshaling never fails on email header data.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// SanitizeMailMeta ensures all string fields in a MailMeta contain valid UTF-8.
// Email headers may contain non-UTF-8 bytes (e.g. Latin-1, raw 8-bit data)
// which cause proto.Marshal to fail.
func SanitizeMailMeta(meta *bongov1.MailMeta) {
	meta.Subject = sanitizeUTF8(meta.Subject)
	meta.From = sanitizeUTF8(meta.From)
	meta.MessageId = sanitizeUTF8(meta.MessageId)
	meta.InReplyTo = sanitizeUTF8(meta.InReplyTo)
	meta.References = sanitizeUTF8(meta.References)
	meta.ReplyTo = sanitizeUTF8(meta.ReplyTo)
	meta.ListId = sanitizeUTF8(meta.ListId)
	meta.ListPost = sanitizeUTF8(meta.ListPost)
	for i, addr := range meta.To {
		meta.To[i] = sanitizeUTF8(addr)
	}
	for i, addr := range meta.Cc {
		meta.Cc[i] = sanitizeUTF8(addr)
	}
}

// StoreMailMeta writes a MailMeta record for a user via Raft.
func (ms *MailboxStore) StoreMailMeta(userID string, meta *bongov1.MailMeta) error {
	SanitizeMailMeta(meta)
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

// DeliverMessage writes a MailMeta record and appends it to a mailbox via one
// synthetic Raft command. Missing destination mailboxes are created on demand.
func (ms *MailboxStore) DeliverMessage(userID, mailbox string, meta *bongov1.MailMeta, flags uint32) (uint32, error) {
	cmd, err := buildMailboxDeliveryCommand(userID, mailbox, meta, flags)
	if err != nil {
		return 0, err
	}
	if err := ms.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return 0, fmt.Errorf("delivering mailbox message: %w", err)
	}
	mbMeta, err := ms.GetMailbox(userID, mailbox)
	if err != nil {
		return 0, fmt.Errorf("reloading mailbox after delivery: %w", err)
	}
	return mbMeta.UidNext - 1, nil
}

// AppendMessage appends a message to a mailbox and assigns a new UID.
// It is issued as one synthetic Raft command and applied at the FSM layer as a
// paired mailbox metadata update plus message write. Returns the assigned UID.
func (ms *MailboxStore) AppendMessage(userID, mailbox, mailGuid string, flags uint32, internalDate int64) (uint32, error) {
	msg := &bongov1.MailboxMessage{
		MailGuid:     mailGuid,
		Flags:        flags,
		InternalDate: internalDate,
	}
	msgBytes, err := proto.Marshal(msg)
	if err != nil {
		return 0, fmt.Errorf("marshaling mailbox append payload: %w", err)
	}
	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxAppendKey(userID, mailbox),
		Value: msgBytes,
	}
	if err := ms.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return 0, fmt.Errorf("appending mailbox message: %w", err)
	}
	meta, err := ms.GetMailbox(userID, mailbox)
	if err != nil {
		return 0, fmt.Errorf("reloading mailbox after append: %w", err)
	}
	return meta.UidNext - 1, nil
}

// DeliverMail writes a MailMeta record and appends it to a mailbox via Raft.
//
// The 5s below bounds only how long Raft will wait to enqueue the entry, NOT
// how long this call can take — see cluster.ApplyCommand. Delivery latency is
// therefore governed by how fast FSM.Apply is, not by this value.
func DeliverMail(applier RaftApplier, userID, mailbox string, meta *bongov1.MailMeta, flags uint32) error {
	cmd, err := BuildDeliverMailCommand(userID, mailbox, meta, flags)
	if err != nil {
		return err
	}
	if err := applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("delivering mailbox message: %w", err)
	}
	return nil
}

// BuildDeliverMailCommand builds the synthetic Raft command used for a single
// mailbox delivery.
func BuildDeliverMailCommand(userID, mailbox string, meta *bongov1.MailMeta, flags uint32) (*bongov1.RaftCommand, error) {
	return buildMailboxDeliveryCommand(userID, mailbox, meta, flags)
}

func buildMailboxDeliveryCommand(userID, mailbox string, meta *bongov1.MailMeta, flags uint32) (*bongov1.RaftCommand, error) {
	SanitizeMailMeta(meta)
	metaBytes, err := proto.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshaling mail meta: %w", err)
	}
	payload, err := json.Marshal(mailboxDeliveryPayload{
		Meta:  metaBytes,
		Flags: flags,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling mailbox delivery payload: %w", err)
	}
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   mailboxDeliverKey(userID, mailbox),
		Value: payload,
	}, nil
}

// WriteMessageTags updates the denormalised Tags field on a MailboxMessage via Raft.
func (ms *MailboxStore) WriteMessageTags(userID, mailbox string, uid uint32, tags []string) error {
	msg, err := ms.GetMessage(userID, mailbox, uid)
	if err != nil {
		return err
	}
	msg.Tags = tags
	return ms.writeMessage(userID, mailbox, uid, msg)
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
