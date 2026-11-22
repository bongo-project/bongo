package handlers

import (
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// sendStepSentShared is the send log step of the copy filed in a shared
// inbox's Sent folder.
const sendStepSentShared = "sent_shared"

// fileSharedSentCopy files a copy of a message sent as a shared inbox's address
// in that account's Sent folder, beside the sender's own copy. It is
// best-effort and never fails a send that has already gone out: a shared inbox
// with no Sent folder, over its quota, or a failing write only skips the copy,
// and every skip is logged. The copy shares the sender's blobs, has its own
// GUID derived from the send ID (a retry finds it and places no second one) and
// is marked Seen.
func (h *ComposeHandler) fileSharedSentCopy(lg *sendLog, sharedID, userID, sendID string, sent *bongov1.MailMeta, subject, searchBody string) {
	if _, err := h.mailboxes.GetMailbox(sharedID, "Sent"); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			h.logger.Warn("web: shared Sent lookup failed; no shared Sent copy", "shared", sharedID, "error", err)
		}
		return
	}
	copyMeta := proto.Clone(sent).(*bongov1.MailMeta)
	copyMeta.Guid = derivedSendID(userID, sendID, "sent-shared").String()

	_, err := h.pebble.Get([]byte("/user/" + sharedID + "/mail/" + copyMeta.Guid))
	exists := err == nil
	if !exists {
		if quotaExceeded(h.quota, h.logger, sharedID, copyMeta.RawMessage.GetSize()) {
			h.logger.Warn("web: shared Sent copy skipped: shared inbox over storage quota", "shared", sharedID)
			return
		}
		if _, err := h.mailboxes.DeliverMessage(sharedID, "Sent", copyMeta, store.FlagSeen); err != nil {
			h.logger.Warn("web: shared Sent copy failed", "shared", sharedID, "error", err)
			return
		}
		if h.search != nil {
			if err := h.search.IndexMail(sharedID, copyMeta.Guid, subject, copyMeta.From, copyMeta.To, searchBody, time.Now().Unix(), false, nil); err != nil {
				h.logger.Warn("web: failed to index shared Sent copy", "guid", copyMeta.Guid, "error", err)
			}
		}
	}
	if err := lg.mark(sendStepSentShared); err != nil {
		h.logger.Warn("web: failed to record shared Sent copy", "shared", sharedID, "error", err)
	}
}
