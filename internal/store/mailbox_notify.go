package store

// MailboxNotifier receives mailbox change events so that anything watching a
// mailbox -- IMAP IDLE sessions, the web SSE stream -- learns about a mutation
// no matter which protocol made it.
//
// It is declared here, at the consumer, rather than in internal/imap, because
// internal/imap already imports this package and the dependency can only run
// one way. internal/imap supplies the implementation (imap.StoreNotifier) and
// the agent injects it, exactly as RaftApplier is injected.
//
// Every method is fire-and-forget: a notification is an optimisation over the
// client's next poll, never the record of the change, so an implementation must
// not block and a caller must not fail a write because notification failed.
type MailboxNotifier interface {
	// NotifyNewMail announces that uid arrived in mailbox, which now holds
	// existsCount messages.
	NotifyNewMail(userID, mailbox string, uid, existsCount uint32)

	// NotifyFlagChange announces that uid's flags are now flags.
	NotifyFlagChange(userID, mailbox string, uid, flags uint32)

	// NotifyExpunge announces that uid is no longer in mailbox.
	//
	// It deliberately carries no sequence number. A sequence number is defined
	// relative to what one client has been told the mailbox contains, so two
	// clients with different views hold different numbers for this same
	// message; a number computed here would be wrong for all but one of them
	// and would make the others drop a message they should have kept. Each
	// consumer translates the UID against its own view at delivery time.
	NotifyExpunge(userID, mailbox string, uid uint32)
}

// SetNotifier installs the mailbox event sink. Nil is the normal state on a
// role that has no IMAP fan-out hub (edge nodes, read-only tooling), and every
// emission site is guarded, so leaving it unset degrades to silence rather
// than failing writes.
func (ms *MailboxStore) SetNotifier(n MailboxNotifier) {
	ms.notifier = n
}

func (ms *MailboxStore) notifyNewMail(userID, mailbox string, uid, existsCount uint32) {
	if ms.notifier == nil {
		return
	}
	ms.notifier.NotifyNewMail(userID, mailbox, uid, existsCount)
}

func (ms *MailboxStore) notifyFlagChange(userID, mailbox string, uid, flags uint32) {
	if ms.notifier == nil {
		return
	}
	ms.notifier.NotifyFlagChange(userID, mailbox, uid, flags)
}

func (ms *MailboxStore) notifyExpunge(userID, mailbox string, uid uint32) {
	if ms.notifier == nil {
		return
	}
	ms.notifier.NotifyExpunge(userID, mailbox, uid)
}

// notifyArrival announces a message that has just landed in mailbox, looking
// up the count the announcement has to carry.
//
// The UID is derived from the mailbox's UidNext rather than returned by the
// write, because the write goes through Raft and the apply function that
// allocates the UID runs on the far side of it. Under two concurrent arrivals
// this can name the other one's UID; that is tolerable because the UID is
// advisory here -- an IDLE session re-lists the mailbox and only uses the
// count, and the SSE stream uses the UID for a notification's subject line.
func (ms *MailboxStore) notifyArrival(userID, mailbox string) {
	if ms.notifier == nil {
		return
	}
	meta, err := ms.GetMailbox(userID, mailbox)
	if err != nil || meta == nil || meta.UidNext == 0 {
		return
	}
	total, _, _, err := ms.CountMessages(userID, mailbox)
	if err != nil {
		return
	}
	ms.notifyNewMail(userID, mailbox, meta.UidNext-1, total)
}
