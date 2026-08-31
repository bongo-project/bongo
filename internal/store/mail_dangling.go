package store

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// danglingApplyTimeout matches the 5s ApplyCommand timeout every other store
// uses for a single-key Raft write.
const danglingApplyTimeout = 5 * time.Second

// MailboxRef locates one mailbox entry that references a mail GUID.
type MailboxRef struct {
	Mailbox string
	UID     uint32
}

// DanglingMail is a MailMeta record whose raw_message blob is no longer on
// disk. The message body is unrecoverable: the metadata describes a message
// that cannot be read, opened or indexed.
//
// Mailboxes is the field that decides what to do about it. Empty means the
// record is debris that no mailbox lists, and the existing orphan sweep would
// have reclaimed it anyway. Non-empty means a real mailbox still lists this
// message, so a user sees an entry that fails to open -- and deleting only the
// MailMeta would leave that entry pointing at nothing.
type DanglingMail struct {
	UserID      string
	GUID        string
	MissingHash string // raw_message hash that is absent from the blob store
	Subject     string
	From        string
	ReceivedAt  int64
	UserExists  bool         // a /user/{id}/profile key is present
	Mailboxes   []MailboxRef // every mailbox entry referencing GUID
	// MissingAttachments are attachment hashes also absent from the blob
	// store. Reported for completeness only: a lost attachment is never
	// grounds for deleting a record, because the message still opens.
	MissingAttachments []string
}

// FindDanglingMail returns every MailMeta whose raw_message blob is missing
// from the blob store, together with the mailbox entries that still reference
// it.
//
// Why this needs its own tool rather than an extension of the orphan sweep:
// MailboxStore.OrphanedMailGUIDs finds MailMeta that NO mailbox references and
// reclaims it. These records are the opposite case -- the mailbox entry is
// present and healthy, only the body blob is gone -- so the sweep passes over
// them forever while every indexing run logs a failed body read for each.
//
// A record with no raw_message hash at all is not reported: there is no
// missing blob, so there is nothing to conclude. A malformed hash counts as
// missing, because BlobStore.Get would reject it just as flatly as an absent
// file; either way the body cannot be read.
//
// blobs may be nil on a role that has no local blob store. Such a node cannot
// tell a missing body from one it simply does not host, so it reports nothing
// rather than reporting every message on the node as lost.
//
// Read-only.
func FindDanglingMail(ps *PebbleStore, blobs *BlobStore) ([]DanglingMail, error) {
	if ps == nil || blobs == nil {
		return nil, nil
	}

	iter, err := ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scanning mail: %w", err)
	}

	// Cache profile existence: one user commonly owns several records.
	userSeen := make(map[string]bool)

	var dangling []DanglingMail
	for iter.Next() {
		// Structural parse, not a substring test: /user/{id}/mailbox/mail/msg/{uid}
		// also contains "/mail/" but holds a MailboxMessage. See ParseMailMetaKey.
		key := iter.Key()
		userID, guid, ok := ParseMailMetaKey(key)
		if !ok {
			continue
		}
		// Value()'s error is not returned here: pebbleIterator.Value() records
		// it on the iterator, and the Close() check below re-surfaces it, so a
		// read failure still aborts the scan. Same arrangement as
		// agent.collectReferencedBlobs.
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			// Fatal, not skipped. The key already parsed as a MailMeta key, so
			// the value can only be corrupt, and this report is what an
			// operator deletes mail from: presenting a silently incomplete
			// list as complete is how the wrong record gets purged. Note this
			// does not catch a foreign-but-valid protobuf -- Unmarshal accepts
			// those (see ParseMailMetaKey); the key filter is what excludes
			// them.
			_ = iter.Close() // best-effort: already returning an error
			return nil, fmt.Errorf("unmarshaling mail meta at %q: %w", key, err)
		}

		hash := meta.GetRawMessage().GetHash()
		if hash == "" || blobs.Exists(hash) {
			continue
		}

		var missingAtts []string
		for _, att := range meta.Attachments {
			if att.GetHash() != "" && !blobs.Exists(att.GetHash()) {
				missingAtts = append(missingAtts, att.GetHash())
			}
		}

		exists, cached := userSeen[userID]
		if !cached {
			exists = userProfileExists(ps, userID)
			userSeen[userID] = exists
		}

		dangling = append(dangling, DanglingMail{
			UserID:             userID,
			GUID:               guid,
			MissingHash:        hash,
			Subject:            meta.Subject,
			From:               meta.From,
			ReceivedAt:         meta.ReceivedAt,
			UserExists:         exists,
			MissingAttachments: missingAtts,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("closing mail iterator: %w", err)
	}

	// Mailbox references, resolved once per affected user. Deferred to a
	// second pass so a healthy node -- the overwhelmingly common case -- pays
	// only for the metadata scan and never walks anyone's mailboxes.
	refsByUser := make(map[string]map[string][]MailboxRef)
	for i := range dangling {
		userID := dangling[i].UserID
		refs, ok := refsByUser[userID]
		if !ok {
			refs, err = mailboxRefsByGUID(ps, userID)
			if err != nil {
				return nil, fmt.Errorf("collecting mailbox references for %s: %w", userID, err)
			}
			refsByUser[userID] = refs
		}
		dangling[i].Mailboxes = refs[dangling[i].GUID]
	}

	return dangling, nil
}

// mailboxRefsByGUID maps every mail GUID referenced by one of a user's mailbox
// entries to the entries referencing it.
func mailboxRefsByGUID(ps *PebbleStore, userID string) (map[string][]MailboxRef, error) {
	ms := NewMailboxStore(ps, nil) // read-only use; no applier needed
	boxes, err := ms.ListMailboxes(userID)
	if err != nil {
		return nil, fmt.Errorf("listing mailboxes: %w", err)
	}

	refs := make(map[string][]MailboxRef)
	for _, mb := range boxes {
		msgs, err := listMailboxMessages(ps, userID, mb.Name)
		if err != nil {
			return nil, fmt.Errorf("listing messages in %q: %w", mb.Name, err)
		}
		for _, m := range msgs {
			if m.MailGuid == "" {
				continue
			}
			refs[m.MailGuid] = append(refs[m.MailGuid], MailboxRef{Mailbox: mb.Name, UID: m.Uid})
		}
	}
	return refs, nil
}

// DeleteDanglingMail removes the given records -- every referencing mailbox
// entry and then the MailMeta itself -- via Raft, returning the GUIDs actually
// deleted. Records whose body blob is present again are skipped, so the
// returned list is what the caller should report, not the input list.
//
// The blob is re-checked immediately before each delete rather than trusting
// the scan: a body can return between the two (blob sync pulling it from a
// peer, an operator restoring from backup, the same message redelivered and
// deduplicated onto the same hash). Purging a record whose body has come back
// destroys a readable message, which is far worse than leaving an unreadable
// one in place. Mailbox references are re-derived at delete time for the same
// reason, in the other direction: an IMAP COPY made after the scan would
// otherwise survive as an entry pointing at a MailMeta this call deletes.
//
// Partial-failure semantics, stated because this is NOT atomic: each record
// takes one ApplyCommand per mailbox entry plus one for the MailMeta, and
// Raft offers no cross-key transaction here. An interruption or a failed
// apply therefore leaves a record half-deleted, so the order is chosen to make
// the surviving half the harmless one. Mailbox entries go FIRST and the
// MailMeta LAST, which means an interruption leaves a MailMeta that no mailbox
// references -- exactly an orphan, which the existing orphan sweep
// (MailboxStore.OrphanedMailGUIDs, run every 15 minutes by the agent)
// reclaims without further help. The reverse order would leave a mailbox entry
// pointing at a deleted MailMeta: a message listed in a mailbox that cannot be
// opened at all, and nothing in the tree cleans that up. For the same reason a
// failed mailbox-entry delete aborts the record before the MailMeta is
// touched, and the whole call stops rather than moving on -- if Raft is
// refusing writes, the next record would fare no better.
//
// The derived indexes need no separate cleanup: the FSM already removes a mail
// GUID from the per-user SQLite index and from Bleve when it applies a DELETE
// of a /user/{id}/mail/{guid} key (cluster/fsm.go, indexStoreObject).
//
// Irreversible. The bodies are already gone, but the metadata is not.
func DeleteDanglingMail(ps *PebbleStore, blobs *BlobStore, applier RaftApplier, targets []DanglingMail) ([]string, error) {
	if ps == nil {
		return nil, fmt.Errorf("pebble store not available")
	}
	if blobs == nil {
		return nil, fmt.Errorf("blob store not available")
	}
	if applier == nil {
		return nil, fmt.Errorf("raft applier not available")
	}

	var deleted []string
	for _, t := range targets {
		if t.UserID == "" || t.GUID == "" {
			continue
		}
		if t.MissingHash == "" || blobs.Exists(t.MissingHash) {
			continue // body is readable again; leave the record alone
		}

		refs, err := mailboxRefsByGUID(ps, t.UserID)
		if err != nil {
			return deleted, fmt.Errorf("collecting mailbox references for %s: %w", t.UserID, err)
		}
		for _, ref := range refs[t.GUID] {
			cmd := &bongov1.RaftCommand{
				Op:  bongov1.Op_OP_DELETE,
				Key: mailboxMsgKey(t.UserID, ref.Mailbox, ref.UID),
			}
			if err := applier.ApplyCommand(cmd, danglingApplyTimeout); err != nil {
				return deleted, fmt.Errorf("deleting mailbox entry %s/%d for %s/%s: %w",
					ref.Mailbox, ref.UID, t.UserID, t.GUID, err)
			}
		}

		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_DELETE, Key: mailMetaKey(t.UserID, t.GUID)}
		if err := applier.ApplyCommand(cmd, danglingApplyTimeout); err != nil {
			return deleted, fmt.Errorf("deleting mail meta %s/%s: %w", t.UserID, t.GUID, err)
		}
		deleted = append(deleted, t.GUID)
	}
	return deleted, nil
}
