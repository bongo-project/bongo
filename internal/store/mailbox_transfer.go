package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// MaxMailboxTransferItems caps the messages one cross-owner transfer command
// carries. A request for more is split into several commands, which are NOT
// atomic together: an earlier chunk stays applied when a later one is refused.
const MaxMailboxTransferItems = 500

type mailboxXferItem struct {
	SrcUID  uint32 `json:"src_uid"`
	NewGUID string `json:"new_guid"`
}

// mailboxXferPayload is the body of a cross-owner transfer. The key names the
// destination owner. The caller supplies the new guids and the flag mask (the
// FSM never reads randomness or the clock); everything else, uids included, is
// read inside the apply.
type mailboxXferPayload struct {
	SrcUser    string            `json:"src_user"`
	SrcMailbox string            `json:"src_mailbox"`
	DstMailbox string            `json:"dst_mailbox"`
	Move       bool              `json:"move"`
	FlagMask   uint32            `json:"flag_mask"`
	Items      []mailboxXferItem `json:"items"`
}

// TransferredUID pairs a source uid with the uid its copy received.
type TransferredUID struct {
	SrcUID uint32
	DstUID uint32
}

func mailboxTransferXKey(dstUser string) []byte {
	return []byte(mailboxTransferXPrefix + dstUser)
}

// validTransferOwner reports whether an owner id may become a key segment.
func validTransferOwner(id string) bool {
	return id != "" && !strings.Contains(id, "/")
}

// applyMailboxTransferX copies or moves messages from one owner's mailbox into
// a different owner's, in one Pebble batch. It validates and reads everything
// first, so any refusal writes nothing and a COPY is all or nothing.
//
// The destination MailMeta is cloned from the SOURCE record read here, never
// built from caller data, so a caller cannot point a record at an arbitrary
// blob. It keeps the same BlobRefs: blobs are content-addressed and shared, and
// are neither copied nor deleted (only the GC deletes them). Tags are cleared,
// because the source owner's tags are another namespace and would sidestep the
// destination's tag cap; folder_guid and target_mailbox are cleared for the same
// namespace reason.
//
// A MOVE removes only the source MailboxMessage, as the same-owner move does.
// The source MailMeta stays until the orphan sweep reclaims it, exactly as
// after an EXPUNGE, so the source owner's usage falls only after that sweep and
// the next recompute.
func applyMailboxTransferX(ps *PebbleStore, cmd *bongov1.RaftCommand) (*MailboxCommandResult, error) {
	dstUser := strings.TrimPrefix(string(cmd.Key), mailboxTransferXPrefix)
	var p mailboxXferPayload
	if err := json.Unmarshal(cmd.Value, &p); err != nil {
		return nil, fmt.Errorf("parsing mailbox transfer payload: %w", err)
	}
	switch {
	case !validTransferOwner(dstUser) || !validTransferOwner(p.SrcUser):
		return nil, fmt.Errorf("invalid mailbox transfer owner")
	case dstUser == p.SrcUser:
		return nil, fmt.Errorf("mailbox transfer needs two different owners")
	case p.SrcMailbox == "" || p.DstMailbox == "":
		return nil, fmt.Errorf("invalid mailbox transfer payload")
	case len(p.Items) == 0 || len(p.Items) > MaxMailboxTransferItems:
		return nil, fmt.Errorf("mailbox transfer carries %d items, want 1 to %d", len(p.Items), MaxMailboxTransferItems)
	}

	dstMeta, err := readMailboxMeta(ps, dstUser, p.DstMailbox)
	if err != nil {
		return nil, fmt.Errorf("reading dest mailbox: %w", err)
	}

	type planned struct {
		srcMsg  *bongov1.MailboxMessage
		newMeta *bongov1.MailMeta
		uid     uint32
	}
	plan := make([]planned, 0, len(p.Items))
	seenGUID := make(map[string]bool, len(p.Items))
	seenUID := make(map[uint32]bool, len(p.Items))
	for _, it := range p.Items {
		if !ValidMailboxID(it.NewGUID) || seenGUID[it.NewGUID] || it.SrcUID == 0 || seenUID[it.SrcUID] {
			return nil, fmt.Errorf("invalid mailbox transfer item")
		}
		seenGUID[it.NewGUID] = true
		seenUID[it.SrcUID] = true

		switch _, err := ps.Get(mailMetaKey(dstUser, it.NewGUID)); {
		case err == nil:
			return nil, fmt.Errorf("mailbox transfer guid already exists")
		case !errors.Is(err, ErrNotFound):
			return nil, fmt.Errorf("checking transfer guid: %w", err)
		}

		srcMsg, err := readMailboxMessage(ps, p.SrcUser, p.SrcMailbox, it.SrcUID)
		if err != nil {
			return nil, fmt.Errorf("reading source message: %w", err)
		}
		data, err := ps.Get(mailMetaKey(p.SrcUser, srcMsg.MailGuid))
		if err != nil {
			return nil, fmt.Errorf("reading source mail meta: %w", err)
		}
		var srcMeta bongov1.MailMeta
		if err := proto.Unmarshal(data, &srcMeta); err != nil {
			return nil, fmt.Errorf("parsing source mail meta: %w", err)
		}
		newMeta, ok := proto.Clone(&srcMeta).(*bongov1.MailMeta)
		if !ok {
			return nil, fmt.Errorf("cloning mail meta")
		}
		newMeta.Guid = it.NewGUID
		newMeta.Tags = nil
		// Both name a folder in the source owner's namespace; kept, they would
		// point the copy at a folder the destination owner does not have.
		newMeta.FolderGuid = ""
		newMeta.TargetMailbox = ""

		plan = append(plan, planned{srcMsg: srcMsg, newMeta: newMeta, uid: dstMeta.UidNext})
		dstMeta.UidNext++
	}

	result := &MailboxCommandResult{}
	if err := commitSyncBatch(ps, func(batch *pebble.Batch) error {
		result.Arrivals, result.Expunged, result.NewMails = nil, nil, nil
		for _, pl := range plan {
			metaBytes, err := proto.Marshal(pl.newMeta)
			if err != nil {
				return fmt.Errorf("marshaling mail meta: %w", err)
			}
			if err := batch.Set(mailMetaKey(dstUser, pl.newMeta.Guid), metaBytes, nil); err != nil {
				return fmt.Errorf("writing mail meta: %w", err)
			}
			newMsg := &bongov1.MailboxMessage{
				Uid:          pl.uid,
				MailGuid:     pl.newMeta.Guid,
				Flags:        pl.srcMsg.Flags &^ FlagRecent & p.FlagMask,
				InternalDate: pl.srcMsg.InternalDate,
			}
			if err := writeMailboxMessageBatch(batch, dstUser, p.DstMailbox, pl.uid, newMsg); err != nil {
				return err
			}
			if p.Move {
				if err := batch.Delete(mailboxMsgKey(p.SrcUser, p.SrcMailbox, pl.srcMsg.Uid), nil); err != nil {
					return fmt.Errorf("deleting source message: %w", err)
				}
				result.Expunged = append(result.Expunged, MailboxMessageRef{UserID: p.SrcUser, Mailbox: p.SrcMailbox, UID: pl.srcMsg.Uid})
			}
			result.Arrivals = append(result.Arrivals, MailboxMessageRef{UserID: dstUser, Mailbox: p.DstMailbox, UID: pl.uid})
			result.NewMails = append(result.NewMails, &MailboxNewMail{
				UserID: dstUser, Mailbox: p.DstMailbox, UID: pl.uid, GUID: pl.newMeta.Guid, Meta: pl.newMeta,
			})
		}
		return writeMailboxMetaBatch(batch, dstUser, p.DstMailbox, dstMeta)
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// TransferMessages copies (or moves) messages from srcUser's mailbox into
// dstUser's mailbox, where the two users differ. flagMask limits which of the
// source's system flags the copies keep. Each command is atomic; a request
// over MaxMailboxTransferItems is split into several, which are not atomic
// together, and the pairs for the chunks that did apply are returned alongside
// the error.
//
// The destination uid of each copy is found by the guid the caller generated,
// never from uid_next, which a concurrent delivery would move.
func (ms *MailboxStore) TransferMessages(srcUser, srcMailbox, dstUser, dstMailbox string, uids []uint32, move bool, flagMask uint32) ([]TransferredUID, error) {
	var done []TransferredUID
	for start := 0; start < len(uids); start += MaxMailboxTransferItems {
		chunk := uids[start:min(start+MaxMailboxTransferItems, len(uids))]
		items := make([]mailboxXferItem, len(chunk))
		byGUID := make(map[string]uint32, len(chunk))
		for i, uid := range chunk {
			var raw [16]byte
			if _, err := rand.Read(raw[:]); err != nil {
				return done, fmt.Errorf("generating guid: %w", err)
			}
			g := hex.EncodeToString(raw[:])
			items[i] = mailboxXferItem{SrcUID: uid, NewGUID: g}
			byGUID[g] = uid
		}
		payload, err := json.Marshal(mailboxXferPayload{
			SrcUser: srcUser, SrcMailbox: srcMailbox, DstMailbox: dstMailbox,
			Move: move, FlagMask: flagMask, Items: items,
		})
		if err != nil {
			return done, fmt.Errorf("marshaling mailbox transfer payload: %w", err)
		}
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: mailboxTransferXKey(dstUser), Value: payload}
		if err := ms.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			return done, fmt.Errorf("applying mailbox transfer: %w", err)
		}
		msgs, err := ms.ListMessages(dstUser, dstMailbox)
		if err != nil {
			return done, fmt.Errorf("mapping transferred uids: %w", err)
		}
		found := 0
		pairs := make(map[uint32]uint32, len(chunk))
		for _, m := range msgs {
			if src, ok := byGUID[m.MailGuid]; ok {
				pairs[src] = m.Uid
				found++
			}
		}
		if found != len(chunk) {
			return done, fmt.Errorf("mapping transferred uids: found %d of %d", found, len(chunk))
		}
		for _, uid := range chunk {
			done = append(done, TransferredUID{SrcUID: uid, DstUID: pairs[uid]})
		}
	}
	return done, nil
}
