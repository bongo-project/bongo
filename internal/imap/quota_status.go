package imap

import (
	"errors"
	"fmt"

	"github.com/emersion/go-imap/v2"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// statusSizes fills the size-related STATUS items. The library dereferences
// DELETED, DELETED-STORAGE and SIZE when a client asks for them, so each
// requested item must be set even for an empty mailbox. QUOTA=RES-STORAGE
// makes clients entitled to ask. It scans the mailbox only when one of them is
// requested, so a plain STATUS costs nothing extra.
func (s *session) statusSizes(data *imap.StatusData, mailbox string, options *imap.StatusOptions) error {
	if !options.NumDeleted && !options.DeletedStorage && !options.Size {
		return nil
	}
	msgs, err := s.mailboxes.ListMessages(s.userID, mailbox)
	if err != nil {
		return err
	}
	var deleted uint32
	var deletedBytes, totalBytes int64
	for _, msg := range msgs {
		isDeleted := msg.Flags&store.FlagDeleted != 0
		if isDeleted {
			deleted++
		}
		needSize := options.Size || (options.DeletedStorage && isDeleted)
		if !needSize {
			continue
		}
		size, err := s.mailSize(msg.MailGuid)
		if err != nil {
			return err
		}
		totalBytes += size
		if isDeleted {
			deletedBytes += size
		}
	}
	if options.NumDeleted {
		data.NumDeleted = &deleted
	}
	if options.DeletedStorage {
		kib := ceilKiB(deletedBytes)
		data.DeletedStorage = &kib
	}
	if options.Size {
		data.Size = &totalBytes
	}
	return nil
}

// mailSize returns the stored raw size of a message, 0 when its record is gone
// or unreadable.
func (s *session) mailSize(guid string) (int64, error) {
	data, err := s.pebble.Get([]byte("/user/" + s.userID + "/mail/" + guid))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading mail meta: %w", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return 0, nil
	}
	return meta.GetRawMessage().GetSize(), nil
}
